package cmdb

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/opsorigin/core/pkg/auth"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"

	"github.com/opsorigin/core/pkg/httputil"
)

func (s *Service) handleListDiscoveryTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	tasks, err := s.pg.ListDiscoveryTasks(r.Context(), tenantID)
	if err != nil {
		slog.Error("list discovery tasks failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, tasks)
}

func (s *Service) handleCreateDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var input struct {
		Name   string         `json:"name"`
		Type   string         `json:"type"`
		Target string         `json:"target"`
		Config map[string]any `json:"config"`
	}
	if err := httputil.BindJSON(r, &input); err != nil {
		httputil.WriteError(w, 400, "invalid body")
		return
	}
	if input.Name == "" || input.Type == "" || input.Target == "" {
		httputil.WriteError(w, 400, "name, type, target are required")
		return
	}

	task := &model.DiscoveryTask{
		TenantID: tenantID,
		Name:     input.Name,
		Type:     input.Type,
		Target:   input.Target,
		Config:   input.Config,
	}
	if task.Config == nil {
		task.Config = make(map[string]any)
	}

	if err := s.pg.CreateDiscoveryTask(r.Context(), task); err != nil {
		slog.Error("create discovery task failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 201, task)
}

func (s *Service) handleUpdateDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	existing, err := s.pg.GetDiscoveryTask(r.Context(), id, tenantID)
	if err != nil {
		slog.Error("get discovery task failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil {
		httputil.WriteError(w, 404, "task not found")
		return
	}

	var input struct {
		Name   string         `json:"name"`
		Type   string         `json:"type"`
		Target string         `json:"target"`
		Config map[string]any `json:"config"`
	}
	if err := httputil.BindJSON(r, &input); err != nil {
		httputil.WriteError(w, 400, "invalid body")
		return
	}

	if input.Name != "" {
		existing.Name = input.Name
	}
	if input.Type != "" {
		existing.Type = input.Type
	}
	if input.Target != "" {
		existing.Target = input.Target
	}
	if input.Config != nil {
		existing.Config = input.Config
	}

	if err := s.pg.UpdateDiscoveryTask(r.Context(), existing); err != nil {
		slog.Error("update discovery task failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, existing)
}

func (s *Service) handleDeleteDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteDiscoveryTask(r.Context(), id, tenantID); err != nil {
		slog.Error("delete discovery task failed", "err", err)
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Service) handleRunDiscoveryTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())

	task, err := s.pg.GetDiscoveryTask(r.Context(), id, tenantID)
	if err != nil || task == nil {
		httputil.WriteError(w, 404, "task not found")
		return
	}

	// 架构边界：云/主机/容器属于「资源纳管」层（internal/cloud）职责，由纳管层
	// 拉取后直接生成基础 CI（单一数据源）；CMDB 自动发现只负责在已纳管 CI 之上做
	// 关系/拓扑推断(relation)，以及在探针 Agent 上跑插件深度扫描网络/中间件/数据库/
	// 应用/物理服务器（经 discovery.Registry 注册的插件），不得重复拉取资源。
	switch task.Type {
	case "cloud", "host", "container":
		task.Status = "failed"
		_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
		httputil.WriteError(w, http.StatusBadRequest, "类型 "+task.Type+" 属于资源纳管层职责，请使用资源纳管（云/主机/容器）同步，而非 CMDB 自动发现")
		return
	case "relation":
		// 内置关系发现（在已纳管 CI 之上推断）
	default:
		// 其余类型经注册表路由到真实发现插件；引擎未初始化或类型未注册则拒绝。
		if s.discoveryReg == nil || s.discoveryRunner == nil {
			task.Status = "failed"
			_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
			httputil.WriteError(w, http.StatusInternalServerError, "自动发现引擎未初始化（discovery registry / runner 未注入）")
			return
		}
		if _, ok := s.discoveryReg.Lookup(task.Type); !ok {
			task.Status = "failed"
			_ = s.pg.UpdateDiscoveryTask(r.Context(), task)
			httputil.WriteError(w, http.StatusBadRequest, "不支持的发现类型: "+task.Type+"（可用类型见 GET /api/v1/cmdb/discovery/types）")
			return
		}
	}

	// 更新状态为 running
	task.Status = "running"
	s.pg.UpdateDiscoveryTask(r.Context(), task)

	go func() {
		ctx := context.Background()
		now := time.Now()
		task.LastRunAt = &now

		var found int
		switch task.Type {
		case "relation":
			// 纳管后自动发现：在已纳管 CI 之上推断关系，直接落库（不走 CI 预览/确认流程）
			n, derr := s.discoverRelations(ctx, tenantID)
			if derr != nil {
				slog.Error("relation discovery failed", "err", derr)
				task.Status = "failed"
				_ = s.pg.UpdateDiscoveryTask(ctx, task)
				return
			}
			found = n
		default:
			// 经注册表路由到真实发现插件（server/host/snmp/network/database/middleware/application/app），
			// 在探针 Agent 上执行脚本并解析产出预览结果，供确认后落库为 CI。
			got, derr := s.runDiscoveryPlugin(ctx, task)
			if derr != nil {
				slog.Error("discovery plugin failed", "type", task.Type, "err", derr)
				task.Status = "failed"
				_ = s.pg.UpdateDiscoveryTask(ctx, task)
				return
			}
			for _, it := range got {
				// report 格式的 model_id 优先于插件 ciType（统一规范：脚本可直接声明模型类型）。
				ciType := it.CIType
				if it.ModelID != "" {
					ciType = it.ModelID
				}
				res := &model.DiscoveryResult{
					TaskID:       task.ID,
					TenantID:     tenantID,
					CIName:       it.Name,
					CIType:       ciType,
					IPAddress:    it.IPAddress,
					Attributes:   it.Attributes,
					Source:       task.Name,
					DiscoveredAt: now,
				}
				s.pg.CreateDiscoveryResult(ctx, res)
			}
			found = len(got)
		}

		task.Status = "success"
		task.FoundCount = found
		s.pg.UpdateDiscoveryTask(ctx, task)
	}()

	httputil.WriteJSON(w, 200, map[string]string{"status": "started"})
}

// runDiscoveryPlugin 按任务类型从注册表取出对应插件，在探针 Agent 上执行真实发现。
func (s *Service) runDiscoveryPlugin(ctx context.Context, task *model.DiscoveryTask) ([]*discovery.Item, error) {
	d, ok := s.discoveryReg.Lookup(task.Type)
	if !ok {
		return nil, fmt.Errorf("未注册的发现类型 %q", task.Type)
	}
	return d.Discover(ctx, task, s.discoveryRunner)
}

// EnsureDiscoveryModelTypes 在启动时确保内置模型类型存在（幂等：已存在则跳过）。
func (s *Service) EnsureDiscoveryModelTypes(ctx context.Context, tenantID string) error {
	for _, mt := range buildBuiltinModelTypes() {
		mt.TenantID = tenantID
		// 注意: GetCIModelType 在记录不存在时返回 (nil, nil),
		// 必须按返回值非 nil 判断"已存在", 而非 err==nil。
		existing, err := s.pg.GetCIModelType(ctx, mt.ID, tenantID)
		if err != nil {
			return fmt.Errorf("check model type %q: %w", mt.ID, err)
		}
		if existing != nil {
			continue // 已存在，跳过
		}
		if err := s.pg.CreateCIModelType(ctx, &mt); err != nil {
			return fmt.Errorf("ensure model type %q: %w", mt.ID, err)
		}
		slog.Info("builtin model type ensured", "id", mt.ID, "tenant", tenantID)
	}
	return nil
}
