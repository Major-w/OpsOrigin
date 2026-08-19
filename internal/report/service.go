package report

import (
	"context"
	"database/sql"
	"github.com/opsorigin/core/pkg/auth"
	"net/http"

	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/principal"
)

// Service 是统一报表模块的控制面：数据集浏览、统一查询、看板 CRUD 与渲染。
// 它持有注册表(取数契约)与看板存储(持久化)，自身不依赖任何业务模块。
type Service struct {
	reg   *Registry
	store DashboardStore
}

// NewService 构造报表 service。db 为 nil 时回落到内存存储(测试/轻量场景)。
func NewService(reg *Registry, db *sql.DB) *Service {
	var st DashboardStore = NewMemoryDashboardStore()
	if db != nil {
		st = newPGDashboardStore(db)
	}
	return &Service{reg: reg, store: st}
}

// SetStore 替换底层存储(测试注入)。
func (s *Service) SetStore(st DashboardStore) { s.store = st }

// InitSchema 在装配期显式建表(仅 PG 实现有效)。
func (s *Service) InitSchema(ctx context.Context) {
	if pg, ok := s.store.(*pgDashboardStore); ok {
		pg.InitSchema(ctx)
	}
}

// RegisterRoutes 把报表 REST 端点挂载到网关 mux。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/report/datasets", s.handleListDatasets)
	mux.HandleFunc("POST /api/v1/report/query", s.handleQuery)
	mux.HandleFunc("GET /api/v1/report/dashboards", s.handleListDashboards)
	mux.HandleFunc("POST /api/v1/report/dashboards", s.handleCreateDashboard)
	mux.HandleFunc("GET /api/v1/report/dashboards/{id}", s.handleGetDashboard)
	mux.HandleFunc("GET /api/v1/report/dashboards/{id}/render", s.handleRenderDashboard)
	mux.HandleFunc("PUT /api/v1/report/dashboards/{id}", s.handleUpdateDashboard)
	mux.HandleFunc("DELETE /api/v1/report/dashboards/{id}", s.handleDeleteDashboard)
}

// ——— 数据集浏览器 ———

func (s *Service) handleListDatasets(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.reg.List())
}

// ——— 统一查询 ———

func (s *Service) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q Query
	if err := httputil.BindJSON(r, &q); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := s.reg.Query(r.Context(), auth.TenantFromContext(r.Context()), &q)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, res)
}

// ——— 看板 CRUD ———

func (s *Service) handleListDashboards(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	ds, _ := s.store.List(r.Context(), tenantID)
	if ds == nil {
		ds = []*Dashboard{}
	}
	// 列表视图省略体积较大的 widgets 明细，前端点开时再拉详情
	summary := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		summary = append(summary, map[string]any{
			"id":            d.ID,
			"tenantId":      d.TenantID,
			"name":          d.Name,
			"description":   d.Description,
			"widgetCount":   len(d.Widgets),
			"createdAt":     d.CreatedAt,
			"updatedAt":     d.UpdatedAt,
			"createdBy":     d.CreatedBy,
			"createdByName": d.CreatedByName,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, summary)
}

func (s *Service) handleCreateDashboard(w http.ResponseWriter, r *http.Request) {
	var d Dashboard
	if err := httputil.BindJSON(r, &d); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 归属信息一律由服务端可信上下文覆盖，忽略请求体传入值(防伪造)
	d.TenantID = auth.TenantFromContext(r.Context())
	d.CreatedBy = userFromCtx(r.Context())
	d.CreatedByName = userNameFromCtx(r.Context())
	// 存储前校验所有 Widget 的查询引用，拒绝指向未注册字段/数据集的看板配置
	if err := d.Validate(s.reg); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Create(r.Context(), &d); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, d)
}

func (s *Service) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.Get(r.Context(), id, auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if d == nil {
		httputil.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, d)
}

func (s *Service) handleRenderDashboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := s.store.Get(r.Context(), id, auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if d == nil {
		httputil.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	rendered := make([]map[string]any, 0, len(d.Widgets))
	for _, wgt := range d.Widgets {
		q := wgt.Query
		res, qErr := s.reg.Query(r.Context(), tenantID, &q)
		if qErr != nil {
			rendered = append(rendered, map[string]any{
				"id":     wgt.ID,
				"type":   wgt.Type,
				"title":  wgt.Title,
				"layout": wgt.Layout,
				"error":  qErr.Error(),
			})
			continue
		}
		rendered = append(rendered, map[string]any{
			"id":     wgt.ID,
			"type":   wgt.Type,
			"title":  wgt.Title,
			"layout": wgt.Layout,
			"data":   res,
		})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"id":      d.ID,
		"name":    d.Name,
		"widgets": rendered,
	})
}

func (s *Service) handleUpdateDashboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		httputil.WriteError(w, http.StatusNotFound, "dashboard not found")
		return
	}
	var incoming Dashboard
	if err := httputil.BindJSON(r, &incoming); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	incoming.ID = existing.ID
	incoming.TenantID = existing.TenantID
	incoming.CreatedAt = existing.CreatedAt
	// 创建者归属不可被更新覆盖
	incoming.CreatedBy = existing.CreatedBy
	incoming.CreatedByName = existing.CreatedByName
	if err := incoming.Validate(s.reg); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.Update(r.Context(), &incoming); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, incoming)
}

func (s *Service) handleDeleteDashboard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.Delete(r.Context(), id, auth.TenantFromContext(r.Context())); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ——— 上下文辅助 ———

// tenantFromCtx 从可信用户上下文取租户 ID。
// 优先读 principal(由鉴权中间件从 JWT / 内部服务凭证派生，客户端无法伪造)，
// 兼容仍以裸 key 注入上下文的旧调用方，最终回落 default。

// userFromCtx 从可信用户上下文取操作人 ID，用于记录"谁创建了看板"(审计与协作)。
// 上下文缺失(内部定时任务等无主体场景)时回落 system，避免出现空归属的孤儿看板。
func userFromCtx(ctx context.Context) string {
	if u := principal.UserID(ctx); u != "" {
		return u
	}
	if u, ok := ctx.Value("user_id").(string); ok && u != "" {
		return u
	}
	return "system"
}

// userNameFromCtx 取操作人展示名(由 identity 富化器带缓存回填)，缺失时回落到 ID。
func userNameFromCtx(ctx context.Context) string {
	if n := principal.DisplayName(ctx); n != "" {
		return n
	}
	return userFromCtx(ctx)
}

// ——— HTTP 响应封装 ———
