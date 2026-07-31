package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
}

type Client struct {
	config Config
	client *http.Client
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
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
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
	Stream      bool      `json:"stream"`
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

func NewClient(cfg Config) *Client {
	// 独立 Transport: 放大每主机空闲连接数, 避免多智能体团队并行调用
	// (同一 LLM 主机、同一 *http.Client) 时连接池争用导致部分请求挂起/被 cancel。
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 100
	transport.MaxConnsPerHost = 0 // 0 = 不限制

	return &Client{
		config: cfg,
		client: &http.Client{Timeout: cfg.Timeout, Transport: transport},
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		req.Model = c.config.Model
	}
	req.Stream = false

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
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
		return nil, fmt.Errorf("llm: API error [%d]: %s", resp.StatusCode, string(respBody))
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
type StreamChunk struct {
	Content      string `json:"content"`       // 当前增量文本
	FinishReason string `json:"finish_reason"` // 发送完成后非空,如 "stop"
}

// StreamChat 通过 SSE 逐块接收模型输出。返回只读 channel; Context 取消时后台收束。
func (c *Client) StreamChat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if req.Model == "" {
		req.Model = c.config.Model
	}
	req.Stream = true

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
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
		resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("llm: API error [%d]: %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, 32)
	go c.readSSE(ctx, resp.Body, ch)
	return ch, nil
}

// readSSE 在后台 goroutine 中解析 SSE 流, 推送到 ch, 最后 close(ch)。
// 兼容性: 部分 OpenAI 兼容后端会忽略 stream 标志、直接返回普通 JSON。
// 当响应体不含 SSE 的 "data: " 行时, 回退为整体解析 ChatResponse 并推送单个 chunk。
func (c *Client) readSSE(ctx context.Context, r io.ReadCloser, ch chan<- StreamChunk) {
	defer r.Close()
	defer close(ch)

	raw, err := io.ReadAll(r)
	if err != nil {
		return
	}
	body := strings.TrimSpace(string(raw))
	if body == "" {
		return
	}

	if !strings.Contains(body, "\ndata: ") && !strings.HasPrefix(body, "data: ") {
		// ——— 非 SSE 回退 ———
		var cr ChatResponse
		if err := json.Unmarshal([]byte(body), &cr); err != nil {
			slog.Warn("llm: parse non-sse response failed", "err", err)
			return
		}
		if len(cr.Choices) == 0 {
			return
		}
		ch <- StreamChunk{Content: cr.Choices[0].Message.Content, FinishReason: "stop"}
		return
	}

	// ——— SSE 路径 ———
	for _, line := range strings.Split(body, "\n") {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Role    string `json:"role"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Warn("llm: parse sse chunk", "data", truncate(data, 100), "err", err)
			continue
		}
		for _, c := range chunk.Choices {
			sc := StreamChunk{Content: c.Delta.Content, FinishReason: c.FinishReason}
			ch <- sc
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/embeddings", bytes.NewReader(body))
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
	c.client = &http.Client{Timeout: cfg.Timeout}
	slog.Info("llm client config updated", "model", cfg.Model, "base_url", cfg.BaseURL)
}
