package interceptor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/principal"
)

type contextKey string

const (
	KeyTraceID   contextKey = "trace_id"
	KeyTenantID  contextKey = "tenant_id"
	KeyPrincipal contextKey = "principal"
)

// 审计主体统一复用 pkg/principal 的模型,避免同一概念两套结构。
// 保留这些别名以兼容既有调用方。
type (
	PrincipalType = principal.Type
	Principal     = principal.Principal
)

const (
	PrincipalHuman   = principal.TypeHuman
	PrincipalAIAgent = principal.TypeAIAgent
	PrincipalService = principal.TypeService
)

func generateTraceID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func EnsureTraceID(ctx context.Context) (context.Context, string) {
	traceID, ok := ctx.Value(KeyTraceID).(string)
	if !ok || traceID == "" {
		traceID = generateTraceID()
		ctx = context.WithValue(ctx, KeyTraceID, traceID)
	}
	return ctx, traceID
}

var skipPaths = map[string]bool{
	"/api/v1/ops/health":            true,
	"/api/v1/ops/status":            true,
	"/api/v1/auth/login":            true,
	"/api/v1/audit/events":          true,
	"/api/v1/agents/download":       true,
	"/api/v1/agents/install-script": true,
	"/api/v1/agents/test-ssh":       true,
	"/api/v1/agents/batch-install":  true,
	"/api/v1/agents/sandbox":        true,
}

var skipPrefixes = []string{
	"/api/v1/audit/",
	"/api/v1/ws",
}

// shouldSkipAgentPath 判断是否为 Agent 通信路径(免审计 principal 校验)。
// Agent 注册/心跳/任务拉取/结果上报/CMDB 上报等路径。
func shouldSkipAgentPath(path, method string) bool {
	// POST /api/v1/agents (Agent 注册)
	if path == "/api/v1/agents" && method == "POST" {
		return true
	}
	// /api/v1/agents/{id}/heartbeat, /tasks, /cmdb, /download-tasks, /tasks/{task_id}
	if len(path) > len("/api/v1/agents/") && path[:len("/api/v1/agents/")] == "/api/v1/agents/" {
		if path == "/api/v1/agents" {
			return false
		}
		suffix := path[len("/api/v1/agents/"):]
		// suffix 可能是 {id}/heartbeat, {id}/tasks, {id}/cmdb 等
		if strings.HasSuffix(suffix, "/heartbeat") ||
			strings.HasSuffix(suffix, "/tasks") ||
			strings.HasSuffix(suffix, "/cmdb") ||
			strings.HasSuffix(suffix, "/download-tasks") ||
			strings.Contains(suffix, "/tasks/") {
			return true
		}
	}
	return false
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		traceID := r.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = generateTraceID()
		}
		ctx = context.WithValue(ctx, KeyTraceID, traceID)

		if skipPaths[r.URL.Path] {
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}
		// 跳过 Agent 通信路径(免审计 principal 校验)
		if shouldSkipAgentPath(r.URL.Path, r.Method) {
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}
		for _, prefix := range skipPrefixes {
			if len(r.URL.Path) >= len(prefix) && r.URL.Path[:len(prefix)] == prefix {
				r = r.WithContext(ctx)
				next.ServeHTTP(w, r)
				return
			}
		}

		// 租户：从鉴权上下文(JWT)取,禁止从请求头取,防止租户伪造。
		// 回落 "default" 仅当无任何鉴权上下文(理论上鉴权中间件已先行拦截未登录请求)。
		tenantID := auth.TenantIDFromContext(ctx)
		if tenantID == "" {
			tenantID = "default"
		}
		ctx = context.WithValue(ctx, KeyTenantID, tenantID)

		// principal 一律取自鉴权中间件写入的可信上下文。
		// 请求头 X-Principal-* 已在鉴权层完成校验:仅内部服务凭证通道可产生,
		// 其余来源被剥离 —— 审计层不再自行读头,杜绝主体伪造。
		pr, ok := principal.FromContext(ctx)
		if !ok || pr.ID == "" {
			// 兜底:少数上下文由旧式裸 key 构造(如后台任务派生的请求)。
			if uid := auth.UserIDFromContext(ctx); uid != "" {
				pr = principal.Principal{Type: PrincipalHuman, ID: uid, TenantID: tenantID}
			} else {
				// 鉴权中间件应已拦截未登录请求;此处兜底为 401 而非 403。
				slog.Warn("audit: authenticated context missing principal",
					"trace_id", traceID,
					"path", r.URL.Path)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		if pr.TenantID == "" {
			pr.TenantID = tenantID
		}
		ctx = context.WithValue(ctx, KeyPrincipal, pr)

		w.Header().Set("X-Trace-ID", traceID)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	})
}

func TraceIDFromContext(ctx context.Context) string {
	if t, ok := ctx.Value(KeyTraceID).(string); ok {
		return t
	}
	return ""
}

func TenantIDFromContext(ctx context.Context) string {
	if t, ok := ctx.Value(KeyTenantID).(string); ok {
		return t
	}
	return ""
}

// PrincipalFromContext 读取审计主体。优先取本中间件写入的值,
// 回落到鉴权层写入的统一主体(便于未经审计中间件的内部调用也能取到)。
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if p, ok := ctx.Value(KeyPrincipal).(Principal); ok {
		return p, true
	}
	return principal.FromContext(ctx)
}
