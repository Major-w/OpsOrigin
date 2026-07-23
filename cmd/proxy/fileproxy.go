package main

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/internal/filelimit"
	"github.com/opsorigin/core/internal/token"
)

// FileProxy 是 §5 文件分发在 Proxy 节点上的 HTTP 文件代理。
//
// 拓扑:经 Proxy 接入的 Agent 不直接连 Core,而是向 Proxy 公网地址拉取文件;
// Proxy 在边缘校验短期令牌(§6)后,再向 Core 内部端点(/api/v1/files/{id})
// 拉取并流式转发,支持 Range 断点续传。Core 会再次校验同一令牌(纵深防御)。
type FileProxy struct {
	coreBase    string // 形如 http://core:8080
	tokenSecret string
	limiter     *filelimit.Limiter
	client       *http.Client
}

// NewFileProxy 构造文件代理。coreAddr 为 Core 地址(如 localhost:8080)。
func NewFileProxy(coreAddr, tokenSecret string) *FileProxy {
	return &FileProxy{
		coreBase:    "http://" + coreAddr,
		tokenSecret: tokenSecret,
		limiter:     filelimit.New(0, 0),
		client:       &http.Client{Timeout: 30 * time.Minute}, // 大文件下载,长超时
	}
}

// Handler 返回挂载文件代理路由的 http.Handler。
func (fp *FileProxy) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/files/{file_id}", fp.handle)
	return mux
}

func (fp *FileProxy) handle(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	tok := r.URL.Query().Get("token")
	if tok == "" {
		http.Error(w, "missing download token", http.StatusUnauthorized)
		return
	}

	claims, err := token.Verify(fp.tokenSecret, tok)
	if err != nil {
		status := http.StatusForbidden
		if err == token.ErrExpired {
			status = http.StatusUnauthorized
		}
		http.Error(w, "invalid download token: "+err.Error(), status)
		return
	}
	if claims.FileID != fileID {
		http.Error(w, "token/file mismatch", http.StatusForbidden)
		return
	}

	key := claims.AgentID
	if !fp.limiter.Allow(key) {
		http.Error(w, "download rate limited", http.StatusTooManyRequests)
		return
	}

	upURL := fp.coreBase + "/api/v1/files/" + fileID + "?token=" + tok
	upReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upURL, nil)
	if err != nil {
		fp.limiter.RecordFailure(key)
		http.Error(w, "build upstream request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if rg := r.Header.Get("Range"); rg != "" {
		upReq.Header.Set("Range", rg)
	}

	resp, err := fp.client.Do(upReq)
	if err != nil {
		fp.limiter.RecordFailure(key)
		slog.Warn("file proxy upstream failed", "file_id", fileID, "error", err)
		http.Error(w, "upstream fetch failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 透传上游响应头(Range/Content-Length/Content-Type/Accept-Ranges)。
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		slog.Warn("file proxy stream failed", "file_id", fileID, "error", err)
		// 响应已部分写出,无法再改状态码;仅记录。
		fp.limiter.RecordFailure(key)
		return
	}
	fp.limiter.RecordSuccess(key)
}
