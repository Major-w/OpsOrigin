package cmdb

import (
	"encoding/json"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"
	"strconv"

	"github.com/opsorigin/core/internal/cmdb/model"
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
