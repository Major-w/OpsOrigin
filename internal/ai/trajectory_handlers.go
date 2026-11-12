package ai

import (
	"net/http"
	"strconv"

	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// 数字员工运行轨迹的只读回放端点(P0 治理层)。
//
// 安全约束: tenantID 只从鉴权上下文取, 不接受请求参数; 所有查询强制带 tenant,
// 跨租户 turn 一律 404。权限点 ai:trace:read 已在 pkg/auth/rbac.go 显式登记。

func (s *Service) handleListTrajectories(w http.ResponseWriter, r *http.Request) {
	if s.trajRecorder == nil {
		httputil.WriteJSON(w, http.StatusOK, []*agentic.TrajectoryTurn{})
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v >= 0 {
			offset = v
		}
	}
	turns, err := s.trajRecorder.ListTurns(r.Context(), auth.TenantFromContext(r.Context()),
		r.URL.Query().Get("conversation_id"), limit, offset)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if turns == nil {
		turns = []*agentic.TrajectoryTurn{}
	}
	httputil.WriteJSON(w, http.StatusOK, turns)
}

func (s *Service) handleGetTrajectory(w http.ResponseWriter, r *http.Request) {
	if s.trajRecorder == nil {
		httputil.WriteError(w, http.StatusNotFound, "trajectory not found")
		return
	}
	turn, err := s.trajRecorder.GetTurn(r.Context(), auth.TenantFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if turn == nil {
		httputil.WriteError(w, http.StatusNotFound, "trajectory not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, turn)
}

// handleGetTrajectoryOTel 导出为 OTel GenAI 语义约定的 trace 结构, 供观测后端/评测集消费。
func (s *Service) handleGetTrajectoryOTel(w http.ResponseWriter, r *http.Request) {
	if s.trajRecorder == nil {
		httputil.WriteError(w, http.StatusNotFound, "trajectory not found")
		return
	}
	turn, err := s.trajRecorder.GetTurn(r.Context(), auth.TenantFromContext(r.Context()), r.PathValue("id"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if turn == nil {
		httputil.WriteError(w, http.StatusNotFound, "trajectory not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, agentic.ToOTelJSON(turn))
}
