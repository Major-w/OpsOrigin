package agentic

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// 文本形态工具调用兜底解析。
//
// 背景: 部分模型(尤其经 OpenAI 兼容端点接入的非原生 FC 模型/推理模型偶发"降级")不遵守
// Function Calling 协议, 而是把工具调用以纯文本吐出, 典型样式:
//
//	<tool_call>{"name":"knowledge_search","arguments":{"query":"..."}}</tool_call>
//	<tool_call>
//	```python
//	knowledge_search(query="...")
//	```
//	</tool_call>
//
// 引擎若只认原生 tool_calls, 会把这段伪调用当最终答案直接返回: 用户看到"我要调用 xx 工具"
// 加一段代码块后再无下文(表现为"卡住")。extractTextToolCalls 把这两类文本还原为
// llm.ToolCall, 让工具循环正常继续。
//
// 安全边界: 只收录名称命中本轮请求工具白名单(known)的调用, 防止把普通代码示例误判成
// 工具调用; 参数串解析失败一律丢弃(fail-closed), 不做模糊猜参。

const (
	tcTagOpen  = "<tool_call>"
	tcTagClose = "</tool_call>"
)

var (
	// reToolCallBlock 匹配 <tool_call>…</tool_call>; 兜底匹配未闭合的尾部块(模型常忘写闭标签)。
	reToolCallBlock = regexp.MustCompile(`(?s)<tool_call>\s*(.*?)\s*(?:</tool_call>|\z)`)
	// reFenceBlock 匹配 ``` 围栏代码块(语言标识可选)。
	reFenceBlock = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*[ \\t]*\\n?(.*?)```")
	// reFuncCall 匹配 name(key=value, ...) 单函数调用形态。
	reFuncCall = regexp.MustCompile(`(?s)^\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\((.*)\)\s*$`)
)

// toolNameSet 把本轮请求携带的工具定义转成名称集合。
func toolNameSet(tools []llm.ToolDef) map[string]bool {
	set := make(map[string]bool, len(tools))
	for _, t := range tools {
		set[t.Function.Name] = true
	}
	return set
}

// extractTextToolCalls 从 assistant 文本中解析文本形态工具调用, 返回 0 个表示无。
// 优先解析 <tool_call> 块; 其中一个都解析不出时再尝试围栏代码块(避免误判普通代码示例)。
func extractTextToolCalls(content string, known map[string]bool) []llm.ToolCall {
	if !strings.Contains(content, tcTagOpen) && !strings.Contains(content, "```") {
		return nil
	}
	var out []llm.ToolCall
	add := func(body string) {
		name, args, ok := parseTextCallBody(body, known)
		if !ok {
			return
		}
		out = append(out, makeTextToolCall(name, args, len(out)))
	}
	for _, m := range reToolCallBlock.FindAllStringSubmatch(content, -1) {
		add(m[1])
	}
	if len(out) == 0 {
		for _, m := range reFenceBlock.FindAllStringSubmatch(content, -1) {
			add(m[1])
		}
	}
	return out
}

// parseTextCallBody 解析单个调用体: 先试 JSON 形态 {"name","arguments"|"parameters"},
// 再试函数调用形态 name(key=value)。返回的 argsJSON 统一为 JSON 对象字符串。
func parseTextCallBody(body string, known map[string]bool) (name, argsJSON string, ok bool) {
	body = stripCodeFence(strings.TrimSpace(body))
	if body == "" {
		return "", "", false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err == nil {
		n, _ := obj["name"].(string)
		if !known[n] {
			return "", "", false
		}
		raw, has := obj["arguments"]
		if !has {
			raw, has = obj["parameters"]
		}
		args := "{}"
		if has {
			if b, err := json.Marshal(raw); err == nil {
				args = string(b)
			}
		}
		return n, args, true
	}
	if m := reFuncCall.FindStringSubmatch(body); m != nil {
		n := m[1]
		if !known[n] {
			return "", "", false
		}
		argsMap, err := parseCallArgs(m[2])
		if err != nil {
			slog.Debug("text tool call args parse failed", "tool", n, "err", err)
			return "", "", false
		}
		b, err := json.Marshal(argsMap)
		if err != nil {
			return "", "", false
		}
		return n, string(b), true
	}
	return "", "", false
}

// stripCodeFence 若 body 整体是 ``` 围栏块则取内部代码(模型常把调用再包一层 fence)。
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if m := reFenceBlock.FindStringSubmatch(s); m != nil {
		return strings.TrimSpace(m[1])
	}
	return s
}

func makeTextToolCall(name, argsJSON string, idx int) llm.ToolCall {
	tc := llm.ToolCall{
		ID:   fmt.Sprintf("textcall-%d-%d", time.Now().UnixMilli(), idx),
		Type: "function",
	}
	tc.Function.Name = name
	tc.Function.Arguments = argsJSON
	return tc
}

// parseCallArgs 解析 Python/JSON 风格的参数串: key=value, key2='v', flag=true, cfg={...}。
// 支持带引号字符串(含转义)、数字、true/false/null(None/nil)、以及花/方括号包裹的 JSON 值
// (单引号键值做宽松转换后解析)。返回 nil map 表示空参。
func parseCallArgs(s string) (map[string]any, error) {
	out := make(map[string]any)
	i := 0
	n := len(s)
	skipWS := func() {
		for i < n {
			switch s[i] {
			case ' ', '\t', '\n', '\r', ',':
				i++
			default:
				return
			}
		}
	}
	for {
		skipWS()
		if i >= n {
			break
		}
		ks := i
		for i < n && isArgKeyByte(s[i]) {
			i++
		}
		key := s[ks:i]
		if key == "" {
			return nil, fmt.Errorf("invalid argument key at offset %d", i)
		}
		skipWS()
		if i >= n || (s[i] != '=' && s[i] != ':') {
			return nil, fmt.Errorf("expected '=' after key %q", key)
		}
		i++
		skipWS()
		val, err := parseCallValue(s, &i)
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", key, err)
		}
		out[key] = val
	}
	return out, nil
}

func isArgKeyByte(c byte) bool {
	return c == '_' || c == '-' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// parseCallValue 从 s[*i:] 解析一个值并推进 *i。
func parseCallValue(s string, i *int) (any, error) {
	n := len(s)
	if *i >= n {
		return nil, fmt.Errorf("unexpected end of arguments")
	}
	switch c := s[*i]; {
	case c == '"' || c == '\'':
		*i++
		var b strings.Builder
		for *i < n {
			ch := s[*i]
			if ch == '\\' && *i+1 < n {
				*i++
				switch s[*i] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				default:
					b.WriteByte(s[*i])
				}
				*i++
				continue
			}
			if ch == c {
				*i++
				return b.String(), nil
			}
			b.WriteByte(ch)
			*i++
		}
		return nil, fmt.Errorf("unterminated string literal")
	case c == '{' || c == '[':
		start := *i
		depth := 0
		var inStr byte
		for *i < n {
			ch := s[*i]
			if inStr != 0 {
				if ch == '\\' {
					*i++
				} else if ch == inStr {
					inStr = 0
				}
			} else if ch == '"' || ch == '\'' {
				inStr = ch
			} else if ch == '{' || ch == '[' {
				depth++
			} else if ch == '}' || ch == ']' {
				depth--
				if depth == 0 {
					*i++
					raw := s[start:*i]
					var v any
					if err := json.Unmarshal([]byte(raw), &v); err == nil {
						return v, nil
					}
					// 单引号宽松转换后重试(Python dict 形态)。
					if err := json.Unmarshal([]byte(strings.ReplaceAll(raw, "'", `"`)), &v); err == nil {
						return v, nil
					}
					return nil, fmt.Errorf("invalid JSON value")
				}
			}
			*i++
		}
		return nil, fmt.Errorf("unterminated JSON value")
	default:
		st := *i
		for *i < n && s[*i] != ',' && s[*i] != '}' && s[*i] != ']' {
			*i++
		}
		tok := strings.TrimSpace(s[st:*i])
		switch tok {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null", "None", "nil":
			return nil, nil
		}
		if f, err := strconv.ParseFloat(tok, 64); err == nil {
			return f, nil
		}
		return tok, nil // 退化按字符串处理
	}
}

// pseudoCallFilter 流式抑制文本形态工具调用块: 把 <tool_call>…</tool_call>(含跨 chunk
// 切分与未闭合兜底)从可见正文中剥离, 原文经 captured 返回 —— 引擎将其折叠进 thinking
// 并用于 extractTextToolCalls 解析, 用户不再看到"伪调用卡住"的观感。
type pseudoCallFilter struct {
	pending  string
	inCall   bool
	captured strings.Builder
}

func newPseudoCallFilter() *pseudoCallFilter { return &pseudoCallFilter{} }

// feed 输入一个流式分片, 返回 (clean, captured):
//   - clean:   伪调用块之外的可见文本, 可直接 emit("token", ...);
//   - captured: 本次分片中完整闭合的 <tool_call>…</tool_call> 原文(可能为空)。
func (f *pseudoCallFilter) feed(s string) (clean, captured string) {
	f.pending += s
	var out strings.Builder
	for {
		if !f.inCall {
			idx := strings.Index(f.pending, tcTagOpen)
			if idx < 0 {
				// 尾部可能是不完整开标签(跨 chunk), 保留待下个分片判定。
				keep := partialTagSuffixLen(f.pending, tcTagOpen)
				split := len(f.pending) - keep
				out.WriteString(f.pending[:split])
				f.pending = f.pending[split:]
				break
			}
			out.WriteString(f.pending[:idx])
			f.pending = f.pending[idx+len(tcTagOpen):]
			f.inCall = true
			continue
		}
		idx := strings.Index(f.pending, tcTagClose)
		if idx < 0 {
			keep := partialTagSuffixLen(f.pending, tcTagClose)
			split := len(f.pending) - keep
			f.captured.WriteString(f.pending[:split])
			f.pending = f.pending[split:]
			break
		}
		f.captured.WriteString(f.pending[:idx])
		f.pending = f.pending[idx+len(tcTagClose):]
		f.inCall = false
		captured += tcTagOpen + f.captured.String() + tcTagClose
		f.captured.Reset()
	}
	return out.String(), captured
}

// flush 流结束时兜底: 未闭合的 <tool_call> 残留整体作为 captured 返回(不再进正文);
// 无伪调用时返回剩余普通文本。
func (f *pseudoCallFilter) flush() (clean, captured string) {
	rest := f.pending
	f.pending = ""
	if f.inCall {
		return "", tcTagOpen + f.captured.String() + rest
	}
	return rest, ""
}

// partialTagSuffixLen 返回 s 尾部与 tag 前缀重合的最长长度(跨 chunk 标签切分保护)。
func partialTagSuffixLen(s, tag string) int {
	max := len(tag) - 1
	if len(s) < max {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return k
		}
	}
	return 0
}
