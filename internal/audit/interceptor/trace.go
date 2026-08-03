package interceptor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/opsorigin/core/pkg/auth"
)

type contextKey string

const (
	KeyTraceID   contextKey = "trace_id"
	KeyTenantID  contextKey = "tenant_id"
	KeyPrincipal contextKey = "principal"
)

type PrincipalType string

const (
	PrincipalHuman   PrincipalType = "HUMAN"
	PrincipalAIAgent PrincipalType = "AI_AGENT"
)

type Principal struct {
	Type     PrincipalType `json:"type"`
	ID       string        `json:"id"`
	TicketID string        `json:"ticket_id,omitempty"`
}

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

		// principal 解析优先级:
		//  1) 显式 X-Principal-* 请求头(服务间调用 / AI Agent 旁路鉴权场景)
		//  2) 否则从 JWT 鉴权上下文推导(浏览器/客户端走 Bearer 登录,不携带 X-Principal 头)
		// 此前仅认请求头,导致所有 Bearer 登录请求被误判为 "missing principal credentials" 而 403。
		principalType := r.Header.Get("X-Principal-Type")
		principalID := r.Header.Get("X-Principal-ID")
		if principalType != "" && principalID != "" {
			ctx = context.WithValue(ctx, KeyPrincipal, Principal{
				Type: PrincipalType(principalType),
				ID:   principalID,
			})
			slog.Debug("audit: request authenticated via explicit principal header",
				"trace_id", traceID,
				"principal_type", principalType,
				"principal_id", principalID)
		} else {
			uid := auth.UserIDFromContext(ctx)
			if uid == "" {
				// 鉴权中间件应已拦截未登录请求;此处兜底为 401 而非 403。
				slog.Warn("audit: authenticated context missing user_id",
					"trace_id", traceID,
					"path", r.URL.Path)
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			// Bearer 登录的用户一律视为人类主体。
			ctx = context.WithValue(ctx, KeyPrincipal, Principal{
				Type: PrincipalHuman,
				ID:   uid,
			})
		}

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

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(KeyPrincipal).(Principal)
	return p, ok
}

func ValidateRequest(r *http.Request) error {
	traceID := r.Header.Get("X-Trace-ID")
	if traceID == "" {
		return fmt.Errorf("[ASSERT-AUDIT-01] missing trace ID")
	}

	principal := r.Header.Get("X-Principal-Type")
	principalID := r.Header.Get("X-Principal-ID")
	if principal == "" || principalID == "" {
		return fmt.Errorf("[ASSERT-AUDIT-01] missing principal credentials")
	}

	if principal != string(PrincipalHuman) && principal != string(PrincipalAIAgent) {
		return fmt.Errorf("[ASSERT-AUDIT-01] invalid principal type: %s", principal)
	}

	return nil
}
