// AI 图像生成工具 (image_generate)。
//
// 设计目标: 让对话模型按需生成真实图片(智谱 cogview-3 / 3-flash)。
// 调用链路:
//  1. 模型在回复中调用 MCP 工具 image_generate(prompt, size?)
//  2. 本工具从 sysconfig 读智谱 provider 的 api_key + base_url,
//     POST /images/generations 获取图片 URL(智谱通常返回 24h 有效的 CDN URL)
//  3. 若已接入 MinIO: 下载 CDN 图片 → 上传到 MinIO(ai-images/<uuid>.png) → 返回 7 天预签名 URL
//     未接入 MinIO 时优雅降级: 直接返回智谱 CDN URL(24h 有效)
//  4. 返回 {url, model, size} 给引擎, 引擎将 URL 注入模型上下文
//  5. 模型在最终回复中按 markdown 图片语法 `![描述](url)` 输出
//  6. 前端 renderInline 已支持 ![alt](url) 渲染(URL 协议白名单限 http/https/data/绝对路径)
//
// 选型说明:
//   - 优先用名字含 zhipu 的 provider 的 key(base_url 直接来自 provider, 走 /api/paas/v4),
//     若用户没单独建 zhipu provider 则回退到 cfg.LLM 主配置(默认 api_key + base_url)。
//   - 已接入 MinIO 时图片持久化到对象存储(7 天预签名 URL), 未接入时降级为智谱 CDN 直链(24h 有效)。
package agentic

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

	"github.com/google/uuid"
)

// cogview 默认尺寸(智谱官方推荐区间)
const cogviewDefaultSize = "1024x1024"

// cogview 调用超时: 智谱图片生成通常 5-15s, 给 60s 留余量
const cogviewTimeout = 60 * time.Second

// imageGenerateArgs 工具入参(JSON Schema 由 Register 描述)。
type imageGenerateArgs struct {
	Prompt string `json:"prompt"`          // 必填: 中文/英文图片描述
	Size   string `json:"size,omitempty"`  // 可选: "1024x1024" / "768x1344" / "1344x768" / "512x512"
	Model  string `json:"model,omitempty"` // 可选: 默认 cogview-3-flash (免费/快), 可填 cogview-3 (高质量)
}

// imageGenerateResult 工具出参(返回给模型继续生成回答)。
type imageGenerateResult struct {
	URL    string `json:"url"`    // 图片直链(MinIO 预签名 7d 有效; 未接 MinIO 时为智谱 CDN 24h 有效)
	Model  string `json:"model"`  // 实际使用的模型 ID
	Size   string `json:"size"`   // 实际尺寸
	Prompt string `json:"prompt"` // 实际提示词(便于模型在回答中引用)
}

// imageGenerate 实现 MCP 工具 image_generate。
// 失败时不 panic, 而是返回 error; 模型收到错误后可换 prompt 重试或转用文本描述。
func (ts *MCPToolSet) imageGenerate(ctx context.Context, args map[string]any) (any, error) {
	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("image_generate: prompt 不能为空")
	}
	size, _ := args["size"].(string)
	if size == "" {
		size = cogviewDefaultSize
	}
	model, _ := args["model"].(string)
	if model == "" {
		model = "cogview-3-flash"
	}

	apiKey, baseURL := ts.resolveZhipuCreds()
	if apiKey == "" {
		return nil, fmt.Errorf("image_generate: 未配置智谱 API Key(请在「模型」中添加 zhipu 类型的 provider 并填写 key)")
	}
	if baseURL == "" {
		baseURL = "https://open.bigmodel.cn/api/paas/v4"
	}

	cdnURL, err := callCogview(ctx, apiKey, baseURL, model, prompt, size)
	if err != nil {
		return nil, fmt.Errorf("image_generate: %w", err)
	}

	finalURL := cdnURL
	if ts.objStore != nil {
		if persisted, perr := ts.persistImage(ctx, cdnURL); perr != nil {
			slog.Warn("image_generate: MinIO persist failed, fallback to CDN URL", "err", perr)
		} else {
			finalURL = persisted
		}
	}

	slog.Info("image_generate ok", "model", model, "size", size, "prompt_len", len(prompt), "persisted", ts.objStore != nil)
	return imageGenerateResult{URL: finalURL, Model: model, Size: size, Prompt: prompt}, nil
}

// persistImage 从 CDN 下载图片并上传到 MinIO, 返回 7 天有效的预签名 URL。
func (ts *MCPToolSet) persistImage(ctx context.Context, cdnURL string) (string, error) {
	resp, err := http.Get(cdnURL)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	key := "ai-images/" + uuid.NewString() + ".png"
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}
	if err := ts.objStore.Put(ctx, key, bytes.NewReader(data), int64(len(data)), ct); err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}

	u, err := ts.objStore.PresignedURL(key, 7*24*time.Hour)
	if err != nil {
		return "", fmt.Errorf("presign: %w", err)
	}
	return u.String(), nil
}

// resolveZhipuCreds 优先从 cfg.LLM.Providers 中找名字含 zhipu 的 provider; 否则回退到主 LLM 配置。
// 若主 LLM 也不是智谱(API key 仍可用, 只要 base_url 指向智谱 endpoint), 返回的 key 仍是真值。
func (ts *MCPToolSet) resolveZhipuCreds() (apiKey, baseURL string) {
	if ts.cfg != nil {
		for _, p := range ts.cfg.LLM.Providers {
			if strings.Contains(strings.ToLower(p.Name), "zhipu") || strings.Contains(strings.ToLower(p.BaseURL), "bigmodel.cn") {
				if p.APIKey != "" {
					return p.APIKey, p.BaseURL
				}
			}
		}
		// 回退: 主 LLM 配置(base_url 含 bigmodel.cn 才视为智谱)
		if strings.Contains(ts.cfg.LLM.BaseURL, "bigmodel.cn") {
			return ts.cfg.LLM.APIKey, ts.cfg.LLM.BaseURL
		}
	}
	return "", ""
}

// cogviewResp 智谱图片生成 API 响应体。
type cogviewResp struct {
	Created int64 `json:"created"`
	Data    []struct {
		URL     string `json:"url,omitempty"`
		B64JSON string `json:"b64_json,omitempty"`
	} `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callCogview 调智谱 POST /images/generations, 返回首张图片的 URL。
func callCogview(ctx context.Context, apiKey, baseURL, model, prompt, size string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":  model,
		"prompt": prompt,
		"size":   size,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/images/generations", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	httpClient := &http.Client{Timeout: cogviewTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游状态码 %d: %s", resp.StatusCode, truncateRespBody(string(raw), 200))
	}
	var out cogviewResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("解析响应失败: %w", err)
	}
	if out.Error != nil {
		return "", fmt.Errorf("上游错误 %s: %s", out.Error.Code, out.Error.Message)
	}
	if len(out.Data) == 0 || out.Data[0].URL == "" {
		return "", fmt.Errorf("上游未返回图片 URL")
	}
	return out.Data[0].URL, nil
}

// truncateRespBody 截断上游响应体摘要用于错误日志(避免堆全量响应; 同包 dataset.go 已有 truncate 不重名)。
func truncateRespBody(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
