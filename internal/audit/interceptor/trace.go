package interceptor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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
	"/api/v1/ops/health":           true,
	"/api/v1/ops/status":           true,
	"/api/v1/auth/login":           true,
	"/api/v1/audit/events":         true,
	"/api/v1/agents/download":      true,
	"/api/v1/agents/install-script": true,
	"/api/v1/agents/test-ssh":       true,
	"/api/v1/agents/batch-install":  true,
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

		// Use tenant_id from JWT/auth context (set by auth middleware), NOT from request headers
		// to prevent tenant spoofing. Fall back to "default" only if no auth context exists.
		existingTenantID, _ := ctx.Value(KeyTenantID).(string)
		if existingTenantID == "" {
			existingTenantID = "default"
		}
		ctx = context.WithValue(ctx, KeyTenantID, existingTenantID)

		principal := r.Header.Get("X-Principal-Type")
		principalID := r.Header.Get("X-Principal-ID")
		if principal != "" && principalID != "" {
			ctx = context.WithValue(ctx, KeyPrincipal, Principal{
				Type: PrincipalType(principal),
				ID:   principalID,
			})
			slog.Debug("audit: request authenticated",
				"trace_id", traceID,
				"principal_type", principal,
				"principal_id", principalID)
		} else {
			slog.Warn("audit: request without principal credentials",
				"trace_id", traceID,
				"path", r.URL.Path)
			http.Error(w, `{"error":"missing principal credentials"}`, http.StatusForbidden)
			return
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
