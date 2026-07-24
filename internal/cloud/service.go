// Package cloud 提供多云资源纳管的统一抽象层与 HTTP 接入。
//
// 本文件定义云资源 Service 的契约与 HTTP 注册骨架：
//   - Service 组合一个凭证 Store 与一个 CMDBWriter 接口（不直接依赖 cmdb，
//     避免 import 环，保持纯库性）；
//   - RegisterRoutes 把云资源 REST 路由注册进 Core mux，自动获得
//     auth→audit→rbac 中间件链；
//   - 统一 JSON 响应（writeJSON/writeError）与租户解析（tenantFromCtx），
//     与 vuln / cmdb 等既有 service 风格对齐。
package cloud

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
)

// CMDBWriter 是云资源同步进 CMDB 的契约。由 cmd/core/cloud_adapter.go
// 实现（内部 import cmdb），internal/cloud 本身不感知具体 CMDB 实现。
type CMDBWriter interface {
	// SyncResources 把一批 Resource 幂等地同步为 CMDB CI，返回同步结果统计。
	SyncResources(ctx context.Context, tenantID string, resources []Resource) SyncResult
}

// SyncResult 是云资源同步进 CMDB 的统计结果。
type SyncResult struct {
	Created int      `json:"created"`
	Updated int      `json:"updated"`
	Skipped int      `json:"skipped"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// Service 是云资源「接出」的 HTTP 服务：提供凭证管理与资源发现/同步能力。
type Service struct {
	store Store       // 凭证持久层（MVP 用内存实现，重启即失）
	cmdb  CMDBWriter // CMDB 写入适配（同步发现的资源为 CI）
}

// NewService 构造云资源 Service。
// store 为 nil 时回退到内存凭证存储；cmdb 为写入适配，不可为 nil（否则同步失败）。
func NewService(store Store, cmdb CMDBWriter) *Service {
	if store == nil {
		store = NewMemStore()
	}
	return &Service{store: store, cmdb: cmdb}
}

// RegisterRoutes 把云资源 REST 路由注册进 Core mux。
// 路由前缀 /api/v1/cloud，经 Core 中间件链自动叠加 auth→audit→rbac。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/cloud/providers", s.handleListProviders)
	mux.HandleFunc("GET /api/v1/cloud/credentials", s.handleListCredentials)
	mux.HandleFunc("POST /api/v1/cloud/credentials", s.handleCreateCredential)
	mux.HandleFunc("DELETE /api/v1/cloud/credentials/{id}", s.handleDeleteCredential)
	mux.HandleFunc("POST /api/v1/cloud/resources/list", s.handleListResources)
	mux.HandleFunc("POST /api/v1/cloud/resources/sync", s.handleSyncResources)
}

// tenantFromCtx 从 context 取租户 ID，缺省返回 "default"。
func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

// traceIDFromCtx 从 context 取链路追踪 ID，缺省返回 "unknown"。
func traceIDFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("trace_id").(string); ok && t != "" {
		return t
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("cloud: write json failed", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
