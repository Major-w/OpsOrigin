package cmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// ============================================================
// P2-1 统计大屏
// ============================================================

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, &model.CMDBStats{})
		return
	}
	st, err := s.pg.Stats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, st)
}

// handleGetDashboard 返回当前用户的统计大屏布局定义（无记录返回空对象，前端回落默认）。
func (s *Service) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, map[string]any{})
		return
	}
	def, err := s.pg.GetDashboardLayout(r.Context(), tenantID, userID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if def == nil {
		httputil.WriteJSON(w, 200, map[string]any{})
		return
	}
	httputil.WriteJSON(w, 200, def)
}

// handleSaveDashboard 保存当前用户的统计大屏布局定义（组件列表 + 网格布局）。
func (s *Service) handleSaveDashboard(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteError(w, http.StatusServiceUnavailable, "存储不可用")
		return
	}
	var def pg.DashboardLayout
	if err := httputil.BindJSON(r, &def); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.pg.SaveDashboardLayout(r.Context(), tenantID, userID, &def); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]any{"ok": true})
}

// ============================================================
// P2-1c 大屏 AI 洞察（规则派生，确定性、零 LLM 调用）
// ============================================================

// DashboardInsight 大屏上的 AI 洞察卡：由 Stats 规则派生, 提示配置基线健康度与待办。
type DashboardInsight struct {
	Level  string `json:"level"` // info | warn | risk
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Metric string `json:"metric,omitempty"` // 关键指标原文, 如 "关系密度 0.08"
}

// handleDashboardInsights 基于 IT 资源统计派生 3-5 条数据驱动洞察, 供大屏 AI 洞察卡展示。
func (s *Service) handleDashboardInsights(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, []DashboardInsight{})
		return
	}
	st, err := s.pg.Stats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, deriveInsights(st))
}

// deriveInsights 由统计快照规则派生洞察; 优先级 risk > warn > info, 最多 5 条。
func deriveInsights(st *model.CMDBStats) []DashboardInsight {
	ins := make([]DashboardInsight, 0, 6)
	if st == nil {
		return ins
	}
	inst := st.TotalInstances
	rel := st.TotalRelations
	relDensity := 0.0
	if inst > 0 {
		relDensity = float64(rel) / float64(inst)
	}

	// 1) 关系基线：未建任何关系 / 关系密度过低
	if inst > 0 && rel == 0 {
		ins = append(ins, DashboardInsight{Level: "risk", Title: "尚未建立任何 CI 依赖关系", Detail: "影响分析、故障溯源与变更影响评估均不可用，建议尽快通过采集器或手动补全 CI 间的依赖/归属关系。", Metric: "关系数 0"})
	} else if inst > 5 && relDensity < 0.1 {
		ins = append(ins, DashboardInsight{Level: "warn", Title: "拓扑关联稀疏", Detail: "当前配置项间平均关联不足 0.1 条，拓扑图几乎为空，建议补全依赖关系以支撑影响分析与根因定位。", Metric: fmt.Sprintf("关系密度 %.2f", relDensity)})
	}

	// 2) 非活跃实例占比
	inactiveStatus := map[string]bool{"offline": true, "stopped": true, "maintenance": true, "error": true, "inactive": true, "abnormal": true, "archived": true, "故障": true, "离线": true, "维护": true, "停用": true}
	inactive := 0
	for _, s := range st.ByStatus {
		if inactiveStatus[s.Status] {
			inactive += s.Count
		}
	}
	if inst > 0 {
		ratio := float64(inactive) / float64(inst)
		if ratio > 0.2 {
			ins = append(ins, DashboardInsight{Level: "warn", Title: fmt.Sprintf("存在 %d 台非活跃实例", inactive), Detail: "离线/维护/停用资产占比偏高，建议核对后及时下线或归档，降低台账噪音与授权成本。", Metric: fmt.Sprintf("非活跃占比 %.0f%%", ratio*100)})
		}
	}

	// 3) 资源类型集中度
	if len(st.ByType) > 0 {
		top := st.ByType[0]
		if inst > 0 {
			share := float64(top.Count) / float64(inst)
			if share > 0.5 {
				ins = append(ins, DashboardInsight{Level: "info", Title: "资源类型集中", Detail: fmt.Sprintf("「%s」占实例总数 %.0f%%，建议关注该类型容量水位与高可用冗余，避免单点风险。", top.TypeName, share*100), Metric: fmt.Sprintf("%s %.0f%%", top.TypeName, share*100)})
			}
		}
	}

	// 4) 模型覆盖度
	if st.TotalModels <= 1 && inst > 0 {
		ins = append(ins, DashboardInsight{Level: "info", Title: "资源模型覆盖单一", Detail: "当前仅定义 1 个资源模型，建议按业务域补充模型（如主机/数据库/中间件），提升 CMDB 表达力。", Metric: fmt.Sprintf("模型 %d 个", st.TotalModels)})
	}

	// 5) 空基线提示
	if inst == 0 {
		ins = append(ins, DashboardInsight{Level: "info", Title: "尚未纳管任何配置项", Detail: "CMDB 基线为空，建议通过采集器接入或手动导入，开始构建统一配置源。", Metric: "实例 0"})
	}

	// 按优先级排序后截断到 5 条
	order := map[string]int{"risk": 0, "warn": 1, "info": 2}
	sort.SliceStable(ins, func(i, j int) bool { return order[ins[i].Level] < order[ins[j].Level] })
	if len(ins) > 5 {
		ins = ins[:5]
	}
	return ins
}

// ============================================================
// P2-2 IDC / Rack / U 位
// ============================================================

func (s *Service) handleListIDC(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	if s.pg == nil {
		httputil.WriteJSON(w, 200, []*model.IDCRoom{})
		return
	}
	rooms, err := s.pg.ListIDCRooms(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rooms == nil {
		rooms = []*model.IDCRoom{}
	}
	httputil.WriteJSON(w, 200, rooms)
}

func (s *Service) handleCreateIDC(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var room model.IDCRoom
	if err := httputil.BindJSON(r, &room); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if room.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "机房名称不能为空")
		return
	}
	room.TenantID = tenantID
	if err := s.pg.CreateIDCRoom(r.Context(), &room); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, &room)
}

func (s *Service) handleDeleteIDC(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteIDCRoom(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Service) handleListRacks(w http.ResponseWriter, r *http.Request) {
	idcID := r.PathValue("idcId")
	tenantID := auth.TenantFromContext(r.Context())
	racks, err := s.pg.ListRacks(r.Context(), tenantID, idcID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if racks == nil {
		racks = []*model.Rack{}
	}
	httputil.WriteJSON(w, 200, racks)
}

func (s *Service) handleCreateRack(w http.ResponseWriter, r *http.Request) {
	idcID := r.PathValue("idcId")
	tenantID := auth.TenantFromContext(r.Context())
	var rack model.Rack
	if err := httputil.BindJSON(r, &rack); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if rack.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "机柜编号不能为空")
		return
	}
	rack.TenantID = tenantID
	rack.IDCRoomID = idcID
	if err := s.pg.CreateRack(r.Context(), &rack); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, &rack)
}

func (s *Service) handleDeleteRack(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteRack(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Service) handleGetRackUnits(w http.ResponseWriter, r *http.Request) {
	rackID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	units, err := s.pg.ListRackUnits(r.Context(), rackID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if units == nil {
		units = []*model.RackUnit{}
	}
	httputil.WriteJSON(w, 200, units)
}

func (s *Service) handleAssignRackUnits(w http.ResponseWriter, r *http.Request) {
	rackID := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	var req struct {
		Units []*model.RackUnit `json:"units"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Units) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "units is required")
		return
	}
	if err := s.pg.AssignRackUnits(r.Context(), rackID, tenantID, req.Units); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "assigned"})
}

// ============================================================
// P2-3 应用架构拓扑
// ============================================================

func (s *Service) handleAppTopology(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	appCIID := r.URL.Query().Get("app_ci_id")
	depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))
	if appCIID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "app_ci_id is required")
		return
	}
	topo, err := s.BuildAppTopology(r.Context(), tenantID, appCIID, depth)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, topo)
}

// ============================================================
// P2-4 字段级规则权限
// ============================================================

func (s *Service) handleListFieldRules(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	modelTypeID := r.URL.Query().Get("model_type_id")
	rules, err := s.pg.ListFieldRules(r.Context(), tenantID, modelTypeID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rules == nil {
		rules = []*model.FieldRule{}
	}
	httputil.WriteJSON(w, 200, rules)
}

func (s *Service) handleCreateFieldRule(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var rule model.FieldRule
	if err := httputil.BindJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if rule.ModelTypeID == "" || rule.Field == "" || rule.Permission == "" {
		httputil.WriteError(w, http.StatusBadRequest, "model_type_id/field/permission are required")
		return
	}
	if rule.Action != model.FieldActionHidden && rule.Action != model.FieldActionReadonly {
		httputil.WriteError(w, http.StatusBadRequest, "action must be hidden|readonly")
		return
	}
	rule.TenantID = tenantID
	if err := s.pg.CreateFieldRule(r.Context(), &rule); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, &rule)
}

func (s *Service) handleDeleteFieldRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteFieldRule(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

// ============================================================
// P2-5 消息订阅
// ============================================================

func (s *Service) handleListSubscriptions(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	subs, err := s.pg.ListSubscriptions(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subs == nil {
		subs = []*model.Subscription{}
	}
	httputil.WriteJSON(w, 200, subs)
}

func (s *Service) handleCreateSubscription(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	var sub model.Subscription
	if err := httputil.BindJSON(r, &sub); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if sub.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "订阅名称不能为空")
		return
	}
	if sub.Channel != model.SubChannelWebhook && sub.Channel != model.SubChannelNotification {
		httputil.WriteError(w, http.StatusBadRequest, "channel must be webhook|notification")
		return
	}
	if sub.Channel == model.SubChannelWebhook && sub.Target == "" {
		httputil.WriteError(w, http.StatusBadRequest, "webhook target url is required")
		return
	}
	if sub.EventType == "" {
		sub.EventType = model.SubEventCIAll
	}
	sub.TenantID = tenantID
	if err := s.pg.CreateSubscription(r.Context(), &sub); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 201, &sub)
}

func (s *Service) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	subs, err := s.pg.ListSubscriptions(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *model.Subscription
	for _, s2 := range subs {
		if s2.ID == id {
			target = s2
			break
		}
	}
	if target == nil {
		httputil.WriteError(w, 404, "subscription not found")
		return
	}
	var req struct {
		Name      *string `json:"name"`
		EventType *string `json:"event_type"`
		Target    *string `json:"target"`
		Enabled   *bool   `json:"enabled"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil && *req.Name != "" {
		target.Name = *req.Name
	}
	if req.EventType != nil && *req.EventType != "" {
		target.EventType = model.SubscriptionEventType(*req.EventType)
	}
	if req.Target != nil {
		target.Target = *req.Target
	}
	if req.Enabled != nil {
		target.Enabled = *req.Enabled
	}
	if err := s.pg.UpdateSubscription(r.Context(), target); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, target)
}

func (s *Service) handleDeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.pg.DeleteSubscription(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleTestSubscription 发送一条测试事件验证订阅链路。
func (s *Service) handleTestSubscription(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	subs, err := s.pg.ListSubscriptions(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var target *model.Subscription
	for _, s2 := range subs {
		if s2.ID == id {
			target = s2
			break
		}
	}
	if target == nil {
		httputil.WriteError(w, 404, "subscription not found")
		return
	}
	// 同步执行一次投递，返回结果便于验证
	payload, _ := json.Marshal(map[string]any{
		"event_type": "test",
		"time":       "now",
		"ci":         map[string]any{"id": "test", "name": "订阅测试", "type_id": "*"},
	})
	d := &model.SubscriptionDelivery{SubID: target.ID, TenantID: tenantID, EventType: "test", CIID: "test", Payload: string(payload), Status: "success"}
	switch target.Channel {
	case model.SubChannelWebhook:
		if err := postWebhook(r.Context(), target.Target, string(payload)); err != nil {
			d.Status = "failed"
			d.Error = err.Error()
		}
	case model.SubChannelNotification:
		if s.notifyCb == nil {
			d.Status = "failed"
			d.Error = "notification channel not configured"
		} else if err := s.notifyCb(r.Context(), tenantID, "CMDB 订阅测试："+target.Name, string(payload)); err != nil {
			d.Status = "failed"
			d.Error = err.Error()
		}
	}
	if s.pg != nil {
		_ = s.pg.RecordSubscriptionDelivery(r.Context(), d)
	}
	httputil.WriteJSON(w, 200, d)
}

func (s *Service) handleListSubscriptionDeliveries(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.pg.ListSubscriptionDeliveries(r.Context(), tenantID, limit)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []*model.SubscriptionDelivery{}
	}
	httputil.WriteJSON(w, 200, items)
}
