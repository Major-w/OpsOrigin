package agentic

import (
	"encoding/json"
	"strings"
	"testing"
)

var testKnownTools = map[string]bool{
	"knowledge_search": true,
	"cmdb_query":       true,
	"coding_read_file": true,
}

func textCallArgs(t *testing.T, tcJSON string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(tcJSON), &m); err != nil {
		t.Fatalf("arguments is not valid JSON: %v", err)
	}
	return m
}

// <tool_call> 内标准 JSON 形态。
func TestExtractTextToolCallTagJSON(t *testing.T) {
	content := `我需要调用工具。
<tool_call>
{"name": "knowledge_search", "arguments": {"query": "OpsOrigin 角色介绍"}}
</tool_call>`
	tcs := extractTextToolCalls(content, testKnownTools)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Function.Name != "knowledge_search" {
		t.Errorf("name = %q", tcs[0].Function.Name)
	}
	args := textCallArgs(t, tcs[0].Function.Arguments)
	if args["query"] != "OpsOrigin 角色介绍" {
		t.Errorf("query = %v", args["query"])
	}
}

// 截图场景: <tool_call> 内套 ```python 围栏 + 函数调用形态。
func TestExtractTextToolCallPythonFence(t *testing.T) {
	content := "我需要调用 knowledge_search 工具来查找关于我的角色和功能的文档。\n<tool_call>\n```python\nknowledge_search(query=\"OpsOrigin 智能运维助手 角色介绍\")\n```\n</tool_call>"
	tcs := extractTextToolCalls(content, testKnownTools)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Function.Name != "knowledge_search" {
		t.Errorf("name = %q", tcs[0].Function.Name)
	}
	args := textCallArgs(t, tcs[0].Function.Arguments)
	if args["query"] != "OpsOrigin 智能运维助手 角色介绍" {
		t.Errorf("query = %v", args["query"])
	}
}

// 无 <tool_call> 标签, 仅围栏代码块内的函数调用。
func TestExtractTextToolCallBareFence(t *testing.T) {
	content := "好的\n```python\ncmdb_query(model='host', limit=10)\n```"
	tcs := extractTextToolCalls(content, testKnownTools)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Function.Name != "cmdb_query" {
		t.Errorf("name = %q", tcs[0].Function.Name)
	}
	args := textCallArgs(t, tcs[0].Function.Arguments)
	if args["model"] != "host" {
		t.Errorf("model = %v", args["model"])
	}
	f, ok := args["limit"].(float64)
	if !ok || f != 10 {
		t.Errorf("limit = %v", args["limit"])
	}
}

// 未闭合的 <tool_call> 也要兜底(模型常忘写闭标签)。
func TestExtractTextToolCallUnclosed(t *testing.T) {
	content := `<tool_call>
{"name": "coding_read_file", "arguments": {"path": "README.md"}}`
	tcs := extractTextToolCalls(content, testKnownTools)
	if len(tcs) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tcs))
	}
	if tcs[0].Function.Name != "coding_read_file" {
		t.Errorf("name = %q", tcs[0].Function.Name)
	}
}

// 未知工具名一律忽略(防误判普通代码示例)。
func TestExtractTextToolCallUnknownIgnored(t *testing.T) {
	content := "示例:\n```python\nos.listdir(path='.')\n```"
	if tcs := extractTextToolCalls(content, testKnownTools); len(tcs) != 0 {
		t.Fatalf("unknown tool should be ignored, got %d", len(tcs))
	}
}

// 纯文本无标签/围栏时不解析。
func TestExtractTextToolCallPlainText(t *testing.T) {
	content := "你可以调用 knowledge_search 工具查询知识库。"
	if tcs := extractTextToolCalls(content, testKnownTools); len(tcs) != 0 {
		t.Fatalf("plain text should not parse, got %d", len(tcs))
	}
}

// parseCallArgs: 混合类型/单引号/嵌套 JSON 值。
func TestParseCallArgs(t *testing.T) {
	m, err := parseCallArgs(`query="OpsOrigin 介绍", limit=5, deep=true, cfg={'a': 1}, raw=null`)
	if err != nil {
		t.Fatalf("parseCallArgs: %v", err)
	}
	if m["query"] != "OpsOrigin 介绍" {
		t.Errorf("query = %v", m["query"])
	}
	if f, ok := m["limit"].(float64); !ok || f != 5 {
		t.Errorf("limit = %v", m["limit"])
	}
	if m["deep"] != true {
		t.Errorf("deep = %v", m["deep"])
	}
	if cfg, ok := m["cfg"].(map[string]any); !ok || cfg["a"] != float64(1) {
		t.Errorf("cfg = %v", m["cfg"])
	}
	if m["raw"] != nil {
		t.Errorf("raw = %v", m["raw"])
	}
}

// pseudoCallFilter: 跨 chunk 切分开标签 + 闭合后 captured 完整、clean 不含伪调用。
func TestPseudoCallFilterChunked(t *testing.T) {
	f := newPseudoCallFilter()
	var cleanAll, capturedAll strings.Builder
	chunks := []string{"你好, ", "我需要调用 <tool", "_call>{\"name\": \"knowledge_search\", \"argu", "ments\": {\"query\": \"x\"}}</tool", "_call> 查询。"}
	for _, c := range chunks {
		clean, captured := f.feed(c)
		cleanAll.WriteString(clean)
		capturedAll.WriteString(captured)
	}
	if capturedAll.String() == "" {
		t.Fatalf("expected captured block")
	}
	if !strings.Contains(capturedAll.String(), `"name": "knowledge_search"`) {
		t.Errorf("captured incomplete: %q", capturedAll.String())
	}
	if strings.Contains(cleanAll.String(), "<tool") || strings.Contains(cleanAll.String(), "knowledge_search") {
		t.Errorf("clean should exclude pseudo call: %q", cleanAll.String())
	}
	if cleanAll.String() != "你好, 我需要调用  查询。" {
		t.Errorf("clean = %q", cleanAll.String())
	}
}

// pseudoCallFilter: 流结束仍未闭合 → flush 把残留整体作为 captured。
func TestPseudoCallFilterUnclosedFlush(t *testing.T) {
	f := newPseudoCallFilter()
	clean, captured := f.feed("前文 <tool_call>{\"name\": \"cmdb_query\"")
	if clean != "前文 " {
		t.Errorf("clean = %q", clean)
	}
	if captured != "" {
		t.Errorf("captured before close = %q", captured)
	}
	tail, captured2 := f.flush()
	if tail != "" {
		t.Errorf("tail should be empty, got %q", tail)
	}
	if !strings.Contains(captured2, `"name": "cmdb_query"`) {
		t.Errorf("flush captured = %q", captured2)
	}
	if !strings.HasPrefix(captured2, "<tool_call>") {
		t.Errorf("flush captured should keep open tag, got %q", captured2)
	}
}
