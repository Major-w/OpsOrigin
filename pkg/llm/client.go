package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIKey  string        `json:"api_key"`
	Model   string        `json:"model"`
	BaseURL string        `json:"base_url"`
	Timeout time.Duration `json:"timeout"`
	// EmbeddingModel/EmbeddingBaseURL 可选；留空时分别回退到 Model/BaseURL 的同名 OpenAI 兼容端点。
	EmbeddingModel   string `json:"embedding_model"`
	EmbeddingBaseURL string `json:"embedding_base_url"`
	// MaxRetries 可重试错误(网络错误/429/5xx)的最大重试次数; 0 表示使用默认值 3。
	MaxRetries int `json:"max_retries"`
}

type Client struct {
	config     Config
	client     *http.Client
	maxRetries int
}

// ImageURLPart 图片片段: URL 支持三种形态:
//   - 公网 http(s) 直链(模型自行拉取);
//   - data:image/xxx;base64,...(内联二进制, 后端按附件实时编码);
//   - attachment://<id>(本平台内部引用, 发送/回放前由 ai 层解析为 base64 data URL)。
type ImageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // auto|low|high(可选, 部分模型支持)
}

// FileURLPart 文件片段(智谱/豆包等支持文件理解的兼容扩展): 文档类(PDF/DOCX/XLSX)走此字段。
type FileURLPart struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// ContentPart 多模态内容片段(OpenAI 兼容)。同一条消息可混排文本与图片/文件。
type ContentPart struct {
	Type     string        `json:"type"`                // text | image_url | file_url
	Text     string        `json:"text,omitempty"`      // type=text
	ImageURL *ImageURLPart `json:"image_url,omitempty"` // type=image_url
	FileURL  *FileURLPart  `json:"file_url,omitempty"`  // type=file_url
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent 模型原生推理字段(DeepSeek-R1 / Qwen 思维链等),
	// 部分兼容后端将其放在与 content 并列的 reasoning_content 字段。
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ContentParts 多模态内容(图文混排)。非空时 content 序列化为数组而非字符串:
	//   [{"type":"text","text":"..."},{"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}]
	// Content 字段建议仍填"纯文本摘要"(便于 token 估算/会话标题/检索等只读场景),
	// 两者并存时以 ContentParts 为准下发模型。
	ContentParts []ContentPart `json:"-"`
	// Truncated 输出不完整: 上游 finish_reason=length(max_tokens 到限)或流式中途
	// 断连/取消导致生成未正常收尾。随会话消息持久化, 历史回放时前端据此展示
	// 「输出不完整」提示, 避免半截回答被当成完整答案。
	Truncated bool `json:"truncated,omitempty"`
}

// marshalContent 决定 content 字段的最终形态: 有多模态片段时输出数组, 否则输出字符串。
func (m Message) marshalContent() any {
	if len(m.ContentParts) > 0 {
		return m.ContentParts
	}
	return m.Content
}

type messageAlias Message

// MarshalJSON 自定义序列化: content 在纯文本与多模态数组间自适应。
func (m Message) MarshalJSON() ([]byte, error) {
	type wire struct {
		messageAlias
		Content any `json:"content,omitempty"`
	}
	w := wire{messageAlias: messageAlias(m), Content: m.marshalContent()}
	return json.Marshal(w)
}

// UnmarshalJSON 兼容两种 content 形态: 字符串(旧数据/纯文本) 与 数组(多模态)。
func (m *Message) UnmarshalJSON(b []byte) error {
	type wire struct {
		messageAlias
		Content json.RawMessage `json:"content,omitempty"`
	}
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	*m = Message(w.messageAlias)
	if len(w.Content) == 0 || string(w.Content) == "null" {
		return nil
	}
	// 数组形态 → 解析为 ContentParts, 并把文本片段汇总回 Content 便于只读逻辑复用。
	if strings.HasPrefix(strings.TrimSpace(string(w.Content)), "[") {
		var parts []ContentPart
		if err := json.Unmarshal(w.Content, &parts); err != nil {
			return err
		}
		m.ContentParts = parts
		if m.Content == "" {
			var b strings.Builder
			for _, p := range parts {
				if p.Type == "text" {
					b.WriteString(p.Text)
				}
			}
			m.Content = b.String()
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(w.Content, &s); err != nil {
		m.Content = string(w.Content)
		return nil
	}
	m.Content = s
	return nil
}

// HasVision 该消息是否携带图片/文件片段(供路由挑选多模态模型)。
func (m Message) HasVision() bool {
	for _, p := range m.ContentParts {
		if p.Type == "image_url" || p.Type == "file_url" {
			return true
		}
	}
	return false
}

type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Type     string   `json:"type"`
	Function ToolFunc `json:"function"`
}

type ToolFunc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
	// ToolChoice 控制函数调用策略, OpenAI 兼容语义:
	// 字符串 "auto"/"none"/"required", 或对象 {"type":"function","function":{"name":"xxx"}}。
	// 留空时不下发该字段, 与既有调用方完全兼容。
	ToolChoice  any     `json:"tool_choice,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	Stream      bool    `json:"stream"`
	// User 发起请求的用户 ID(供多模型路由做可见性过滤: private 模型仅创建者可用)。
	// 空字符串表示系统/内部调用, 放行全部模型。
	User string `json:"-"`
	// PreferType 期望的模型能力类型(text/image/video)。auto 模式下路由据此过滤候选;
	// 空表示不限定(由路由按权重+健康选)。引擎按消息是否含图片推导。
	PreferType string `json:"-"`
}

// ForceTool 构造强制调用指定函数的 tool_choice 值。
// 用于「schema 填空器」场景: 让模型必须以结构化参数回答, 而不是自由文本。
func ForceTool(name string) any {
	return map[string]any{
		"type":     "function",
		"function": map[string]any{"name": name},
	}
}

type ChatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int     `json:"index"`
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// pooledTransport 返回共享连接池配置的 Transport 副本:
// 放大每主机空闲连接数, 避免并发调用(同一 LLM 主机)时连接池争用导致请求挂起/被 cancel。
// NewClient 与 UpdateConfig 共用同一配置, 保证热更新后连接池参数不回退。
func pooledTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 100
	tr.MaxConnsPerHost = 0 // 0 = 不限制
	return tr
}

func NewClient(cfg Config) *Client {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	return &Client{
		config:     cfg,
		client:     &http.Client{Timeout: cfg.Timeout, Transport: pooledTransport()},
		maxRetries: cfg.MaxRetries,
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.config.Model
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// 重试前退避; ctx 取消则立即放弃。
			if err := sleepCtx(ctx, backoffDur(attempt, lastErr)); err != nil {
				return nil, lastErr
			}
			slog.Warn("llm: chat request retryable failure, retrying", "attempt", attempt+1, "max", c.maxRetries, "err", lastErr)
		}
		resp, err := c.doChatOnce(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// versionSegmentRe 匹配 BaseURL 末尾的版本段(如 /v1、/v4)。
var versionSegmentRe = regexp.MustCompile(`/v\d+$`)

// joinEndpoint 拼接 BaseURL 与 OpenAI 兼容端点路径, 兼容两类厂商:
//   - 标准 OpenAI 兼容: BaseURL 不含版本段(如 https://api.deepseek.com), 追加 /v1/xxx;
//   - 自带版本号的厂商: BaseURL 已含版本段(如智谱 https://open.bigmodel.cn/api/paas/v4),
//     只拼 /xxx, 避免出现 /v4/v1/chat/completions 这类必然 404 的路径。
//
// 对已显式带上 /v1 的 BaseURL(存量配置)行为保持不变, 因此该改动向后兼容。
func joinEndpoint(base, path string) string {
	b := strings.TrimRight(base, "/")
	if versionSegmentRe.MatchString(b) {
		return b + strings.TrimPrefix(path, "/v1")
	}
	return b + path
}

// doChatOnce 执行单次(非流式)聊天请求, 不做重试。
func (c *Client) doChatOnce(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	req.Stream = false
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinEndpoint(c.config.BaseURL, "/v1/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &apiError{Status: resp.StatusCode, Body: string(respBody), RetryAfter: parseRetryAfter(resp)}
	}
	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("llm: unmarshal response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("llm: empty choices in response")
	}
	return &chatResp, nil
}

// StreamChunk 流式响应片段。Model 发送完内容块后, FinishReason 非空。
// ToolCalls 在工具调用轮次末片(或流结束后兜底片)携带已聚合的全部工具调用,
// 供上层 Function Calling 循环判断是否需执行工具。
type StreamChunk struct {
	Content      string     `json:"content"`       // 当前增量文本
	Reasoning    string     `json:"reasoning"`     // 当前增量推理文本(原生思维链)
	ToolCalls    []ToolCall `json:"tool_calls"`    // 本片携带的(已聚合)工具调用
	FinishReason string     `json:"finish_reason"` // 发送完成后非空,如 "stop"
}

// StreamChat 通过 SSE 逐块接收模型输出。返回只读 channel; Context 取消时后台收束。
func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if req.Model == "" {
		req.Model = c.config.Model
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, backoffDur(attempt, lastErr)); err != nil {
				return nil, lastErr
			}
			slog.Warn("llm: stream request retryable failure, retrying", "attempt", attempt+1, "max", c.maxRetries, "err", lastErr)
		}
		resp, err := c.doStreamOnce(ctx, req)
		if err == nil {
			ch := make(chan StreamChunk, 32)
			go c.readSSE(ctx, resp.Body, ch)
			return ch, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// doStreamOnce 建立一次流式请求(到首字节为止), 不做重试。流建立后的中途断流不重试,
// 以避免向客户端重复推送已发出的内容片段。
func (c *Client) doStreamOnce(ctx context.Context, req ChatRequest) (*http.Response, error) {
	req.Stream = true
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinEndpoint(c.config.BaseURL, "/v1/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: do request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, &apiError{Status: resp.StatusCode, Body: string(b), RetryAfter: parseRetryAfter(resp)}
	}
	return resp, nil
}

// readSSE 在后台 goroutine 中逐行解析 SSE 流, 边解析边推送到 ch, 最后 close(ch)。
// 采用 bufio.Reader 逐行读取而非一次性 ReadAll, 实现真正的边收边推(长推理/长回复不再整段滞留内存)。
// 兼容性: 部分 OpenAI 兼容后端会忽略 stream 标志、直接返回普通 JSON。
// 当响应体从未出现 "data:" 行时, 回退为整体解析 ChatResponse 并推送单个 chunk。
func (c *Client) readSSE(ctx context.Context, r io.ReadCloser, ch chan<- StreamChunk) {
	defer r.Close()
	defer close(ch)

	reader := bufio.NewReader(r)
	// toolAcc 按 index 聚合流式到达的 tool_calls 增量(arguments 分片拼接)。
	toolAcc := map[int]*toolCallAcc{}
	sawData := false
	var nonSSE strings.Builder

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data:") {
				sawData = true
				data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if data == "[DONE]" {
					break
				}
				var chunk struct {
					Choices []struct {
						Delta struct {
							Content          string `json:"content"`
							Reasoning        string `json:"reasoning"`
							ReasoningContent string `json:"reasoning_content"`
							Role             string `json:"role"`
							ToolCalls        []struct {
								Index    int    `json:"index"`
								ID       string `json:"id"`
								Type     string `json:"type"`
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
						FinishReason string `json:"finish_reason"`
					} `json:"choices"`
				}
				if uerr := json.Unmarshal([]byte(data), &chunk); uerr != nil {
					slog.Warn("llm: parse sse chunk", "data", truncate(data, 100), "err", uerr)
				} else {
					for _, c := range chunk.Choices {
						// 聚合 tool_calls(按 index 流式拼接 arguments)
						for _, tc := range c.Delta.ToolCalls {
							acc, ok := toolAcc[tc.Index]
							if !ok {
								acc = &toolCallAcc{}
								toolAcc[tc.Index] = acc
							}
							if tc.ID != "" {
								acc.id = tc.ID
							}
							if tc.Type != "" {
								acc.typ = tc.Type
							}
							if tc.Function.Name != "" {
								acc.name = tc.Function.Name
							}
							acc.args += tc.Function.Arguments
						}
						reasoning := c.Delta.Reasoning
						if reasoning == "" {
							reasoning = c.Delta.ReasoningContent
						}
						sc := StreamChunk{Content: c.Delta.Content, Reasoning: reasoning, FinishReason: c.FinishReason}
						ch <- sc
					}
				}
			} else if !sawData && !strings.HasPrefix(trimmed, ":") {
				// 非 SSE 行(如纯 JSON 响应)且尚未见过 data: 行, 累积用于回退解析。
				nonSSE.WriteString(line)
			}
		}
		if err != nil {
			break
		}
	}

	// 兜底: 若流中已聚合到 tool_calls 但未随任一 chunk 显式带出(部分实现不在流中回传 tool_calls),
	// 补发一个携带 ToolCalls 的末片, 供上层 Function Calling 循环执行工具。
	if len(toolAcc) > 0 {
		ch <- StreamChunk{ToolCalls: buildToolCalls(toolAcc), FinishReason: "tool_calls"}
	}

	// 非 SSE 回退: 整个响应从未出现 data: 行, 整体解析为 ChatResponse。
	if !sawData && nonSSE.Len() > 0 {
		var cr ChatResponse
		if uerr := json.Unmarshal([]byte(strings.TrimSpace(nonSSE.String())), &cr); uerr == nil && len(cr.Choices) > 0 {
			ch <- StreamChunk{Content: cr.Choices[0].Message.Content, FinishReason: "stop"}
		}
	}
}

// toolCallAcc tool_calls 流式增量累加器。
type toolCallAcc struct {
	id   string
	typ  string
	name string
	args string
}

// buildToolCalls 将按 index 聚合的增量还原为有序 ToolCall 列表。
func buildToolCalls(m map[int]*toolCallAcc) []ToolCall {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	out := make([]ToolCall, 0, len(keys))
	for _, k := range keys {
		a := m[k]
		tc := ToolCall{ID: a.id, Type: a.typ}
		if tc.Type == "" {
			tc.Type = "function"
		}
		tc.Function.Name = a.name
		tc.Function.Arguments = a.args
		out = append(out, tc)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// apiError 描述 HTTP 层非 2xx 错误, 携带状态码供重试判定。
type apiError struct {
	Status int
	Body   string
	// RetryAfter 服务端通过 Retry-After 头建议的等待时长; 0 表示未给出。
	RetryAfter time.Duration
}

func (e *apiError) Error() string {
	return fmt.Sprintf("llm: API error [%d]: %s", e.Status, truncate(e.Body, 300))
}

// IsRateLimited 判定错误是否为服务端限流(HTTP 429)。
// 供上层把限流映射为 429 与友好提示, 而不是当作网关故障(502)抛原始错误文本。
func IsRateLimited(err error) bool {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusTooManyRequests
	}
	return false
}

// parseRetryAfter 解析 Retry-After 响应头(秒数或 HTTP 日期); 无法解析时返回 0。
// 超出 maxRetryAfter 的取值会被截断, 避免单次调用被服务端指令挂起过久。
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		d := time.Duration(secs) * time.Second
		if d > maxRetryAfter {
			return maxRetryAfter
		}
		return d
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		if d > maxRetryAfter {
			return maxRetryAfter
		}
		return d
	}
	return 0
}

// maxRetryAfter 单次重试等待上限(服务端给出的 Retry-After 超过此值仍按此值等待)。
const maxRetryAfter = 15 * time.Second

// isRetryable 判定错误是否可重试: 网络错误 / 429 / 5xx 可重试;
// ctx 取消(用户中断)与 4xx(除 429)不重试。
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status == http.StatusTooManyRequests || ae.Status >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	for _, s := range []string{"connection refused", "connection reset", "EOF", "timeout", "broken pipe", "i/o timeout", "tls: "} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// backoffDur 计算第 attempt(>=1) 次重试前的指数退避时长, 上限 5s, 并附加 0~50% 抖动避免惊群。
// backoffDur 计算第 attempt 次重试前的退避时长。
// 限流(429)需要明显更长的等待: 部分厂商(如智谱)的限流窗口以数十秒计,
// 沿用 5xx 的短退避会在窗口内反复失败。服务端给出 Retry-After 时优先遵从。
func backoffDur(attempt int, lastErr error) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if IsRateLimited(lastErr) {
		var ae *apiError
		if errors.As(lastErr, &ae) && ae.RetryAfter > 0 {
			return ae.RetryAfter + jitterOf(500*time.Millisecond)
		}
		d := time.Duration(1<<uint(attempt-1)) * time.Second
		if d > 8*time.Second {
			d = 8 * time.Second
		}
		return d + jitterOf(d/2)
	}
	d := time.Duration(1<<uint(attempt-1)) * 200 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d + jitterOf(d/2)
}

// jitterOf 返回 [0, max) 的随机抖动, 避免多个请求同时重试造成再次拥塞。
func jitterOf(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}

// sleepCtx 在 ctx 取消时立即返回 ctx.Err(), 否则休眠 d 后返回 nil。
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Embed 调用 OpenAI 兼容的 /v1/embeddings 端点，返回文本的向量表示。
// 用于 RAG 知识库语义检索。embedding 模型/端点可独立配置，缺省复用聊天配置。
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	model := c.config.EmbeddingModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	base := c.config.EmbeddingBaseURL
	if base == "" {
		base = c.config.BaseURL
	}
	if base == "" {
		return nil, fmt.Errorf("llm: embedding base url not configured")
	}

	body, _ := json.Marshal(map[string]any{"model": model, "input": text})
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, joinEndpoint(base, "/v1/embeddings"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("llm: embed do request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("llm: embed read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm: embed API error [%d]: %s", resp.StatusCode, string(respBody))
	}

	var er struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &er); err != nil {
		return nil, fmt.Errorf("llm: embed unmarshal: %w", err)
	}
	if len(er.Data) == 0 || len(er.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("llm: embed returned empty vector")
	}
	return er.Data[0].Embedding, nil
}

// UpdateConfig 原子更新 Client 的配置与 http.Client(热生效, 无需重启)。
func (c *Client) UpdateConfig(cfg Config) {
	c.config = cfg
	// 复用 pooledTransport 而非裸 http.Client: 避免热更新后退化为默认 Transport
	// (每主机仅 2 条空闲连接), 并发对话将频繁新建 TCP 连接, 徒增首字延迟。
	c.client = &http.Client{Timeout: cfg.Timeout, Transport: pooledTransport()}
	slog.Info("llm client config updated", "model", cfg.Model, "base_url", cfg.BaseURL)
}
