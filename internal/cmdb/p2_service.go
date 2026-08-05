package cmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/pkg/principal"
)

// ============================================================
// 通用：当前用户权限 / 标识（统一取自 pkg/principal 可信上下文）
// ============================================================

var (
	errEmptyWebhook = errString("webhook target url is empty")
	errAppNotFound  = errString("app ci not found")
)

type errString string

func (e errString) Error() string { return string(e) }

func errWebhookStatus(code int) error {
	return errString("webhook returned status " + strconv.Itoa(code))
}

// fieldRuleSubject 字段级规则的判定主体。
//
// 注意字段规则的语义是「约束」而非「授权」:FieldRule.Permission 指的是
// 「持有该权限的用户受此规则约束」。因此主体不可识别时,空权限集意味着
// 不命中任何规则 = 看到全部字段(fail-open),必须显式按最严格处理。
type fieldRuleSubject struct {
	perms []string
	// restrictAll 主体无法识别时置位:视为命中全部规则(fail-closed)。
	restrictAll bool
}

func (s fieldRuleSubject) matches(permission string) bool {
	if s.restrictAll {
		return true
	}
	for _, p := range s.perms {
		if p == permission {
			return true
		}
	}
	return false
}

// subjectFromRequest 取当前请求的字段规则判定主体。
//
// 权限一律来自鉴权中间件写入的可信上下文(IAM 角色库服务端解析),
// 不再读客户端自报的 X-Permissions 头 —— 那等于把字段级鉴权交给攻击者:
// 任意登录用户改一个请求头就能改变自己受哪些字段规则约束。
func subjectFromRequest(r *http.Request) fieldRuleSubject {
	pr, ok := principal.FromContext(r.Context())
	if !ok || pr.ID == "" {
		// 走到这里说明请求绕过了鉴权链(理论上不应发生)。
		// 对数据保密类规则,宁可多遮不可少遮。
		return fieldRuleSubject{restrictAll: true}
	}
	return fieldRuleSubject{perms: pr.Permissions}
}

// ============================================================
// P2-4 字段级规则权限
// ============================================================

// applyFieldRules 按当前用户权限应用字段规则：
//   - hidden 字段从 attributes/attribute_meta 剔除，并记入 field_access；
//   - readonly 字段仅记入 field_access（前端据此禁用编辑）；
//   - 同一字段命中多条规则取最严格：hidden > readonly > 默认可见。
//
// 仅处理带 FieldAccess 需求的实例；无规则时原样返回。
func applyFieldRules(cis []*model.CIModel, rules []*model.FieldRule, subject fieldRuleSubject) {
	if len(rules) == 0 {
		return
	}
	// model_type_id|field → action（取最严格）
	eff := map[string]string{}
	for _, rl := range rules {
		if !subject.matches(rl.Permission) {
			continue
		}
		key := rl.ModelTypeID + "|" + rl.Field
		cur, exists := eff[key]
		// hidden > readonly；无更严格则取新规则
		if !exists || (rl.Action == model.FieldActionHidden && cur != string(model.FieldActionHidden)) {
			eff[key] = string(rl.Action)
		}
	}
	if len(eff) == 0 {
		return
	}
	for _, ci := range cis {
		access := map[string]string{}
		for _, field := range fieldKeys(ci.Attributes) {
			act, ok := eff[ci.TypeID+"|"+field]
			if !ok {
				act, ok = eff["*|"+field] // 支持跨模型通配
			}
			if !ok {
				continue
			}
			access[field] = act
			if act == string(model.FieldActionHidden) {
				delete(ci.Attributes, field)
				delete(ci.AttributeMeta, field)
			}
		}
		if len(access) > 0 {
			ci.FieldAccess = access
		}
	}
}

func fieldKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ============================================================
// P2-5 消息订阅（CI 变更推送）
// ============================================================

// onNotify 是平台通知中心回调（cmd/core 注入 → notification.NotifyEvent），
// 与 SetMetricSink 同模式，避免 cmdb↔notification 循环依赖。
type notifyFunc func(ctx context.Context, tenantID, title, content string) error

// SetNotifyCallback 注入平台通知中心回调。
func (s *Service) SetNotifyCallback(fn notifyFunc) {
	s.notifyCb = fn
}

// dispatchCIEvent 在 CI 单条操作成功后触发订阅分发（异步，不阻塞主流程）。
// event 为 ci_created / ci_updated / ci_deleted / ci_archived。
func (s *Service) dispatchCIEvent(ctx context.Context, tenantID string, event string, ci *model.CIModel) {
	if s.pg == nil {
		return
	}
	subs, err := s.pg.ListEnabledSubscriptions(ctx, tenantID)
	if err != nil || len(subs) == 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"event_type": event,
		"time":       time.Now().Format(time.RFC3339),
		"ci": map[string]any{
			"id":         ci.ID,
			"type_id":    ci.TypeID,
			"name":       ci.Name,
			"status":     ci.Status,
			"attributes": ci.Attributes,
		},
	})
	for _, sub := range subs {
		if sub.ModelTypeID != "" && sub.ModelTypeID != ci.TypeID {
			continue
		}
		if sub.EventType != model.SubEventCIAll && string(sub.EventType) != event {
			continue
		}
		go s.deliverSubscription(context.Background(), tenantID, sub, event, ci.ID, string(payload))
	}
}

// deliverSubscription 执行一次投递并记录日志。
func (s *Service) deliverSubscription(ctx context.Context, tenantID string, sub *model.Subscription, event, ciID, payload string) {
	d := &model.SubscriptionDelivery{
		SubID: sub.ID, TenantID: tenantID, EventType: event, CIID: ciID, Payload: payload, Status: "success",
	}
	switch sub.Channel {
	case model.SubChannelWebhook:
		if err := postWebhook(ctx, sub.Target, payload); err != nil {
			d.Status = "failed"
			d.Error = err.Error()
		}
	case model.SubChannelNotification:
		if s.notifyCb != nil {
			title := "CMDB 变更通知：" + sub.Name
			content := payload
			if err := s.notifyCb(ctx, tenantID, title, content); err != nil {
				d.Status = "failed"
				d.Error = err.Error()
			}
		} else {
			d.Status = "failed"
			d.Error = "notification channel not configured"
		}
	default:
		d.Status = "failed"
		d.Error = "unknown channel: " + string(sub.Channel)
	}
	if err := s.pg.RecordSubscriptionDelivery(ctx, d); err != nil {
		slog.Warn("record subscription delivery failed", "sub", sub.ID, "err", err)
	}
}

// postWebhook 发送 webhook（5s 超时）。
func postWebhook(ctx context.Context, url, payload string) error {
	if url == "" {
		return errEmptyWebhook
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errWebhookStatus(resp.StatusCode)
	}
	return nil
}

// ============================================================
// P2-3 应用架构拓扑（业务/分层视图数据）
// ============================================================

type AppTopoNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TypeID   string `json:"type_id"`
	Category string `json:"category"`
	Layer    string `json:"layer"` // app | middleware | database | infra | other
}

type AppTopoEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type AppTopology struct {
	AppCIID string        `json:"app_ci_id"`
	AppName string        `json:"app_name"`
	Layers  []TopoLayer   `json:"layers"`
	Nodes   []AppTopoNode `json:"nodes"`
	Edges   []AppTopoEdge `json:"edges"`
	TotalCI int           `json:"total_ci"`
}

type TopoLayer struct {
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"` // node ids
}

// layerForCI 按模型类型名/分类推断层级。
func layerForCI(ci *model.CIModel, mt *model.CIModelType) string {
	if mt != nil {
		switch mt.Category {
		case model.ModelCategoryApp:
			return "app"
		case model.ModelCategoryMiddleware:
			return "middleware"
		case model.ModelCategoryDatabase:
			return "database"
		case model.ModelCategoryInfra:
			return "infra"
		case model.ModelCategoryBusiness:
			return "business"
		}
	}
	t := strings.ToLower(ci.TypeID)
	switch {
	case strings.Contains(t, "db"), strings.Contains(t, "database"), strings.Contains(t, "redis"), strings.Contains(t, "mq"), strings.Contains(t, "kafka"):
		return "database"
	case strings.Contains(t, "nginx"), strings.Contains(t, "gateway"), strings.Contains(t, "lb"), strings.Contains(t, "middleware"), strings.Contains(t, "mq"):
		return "middleware"
	case strings.Contains(t, "app"), strings.Contains(t, "service"), strings.Contains(t, "web"):
		return "app"
	case strings.Contains(t, "host"), strings.Contains(t, "server"), strings.Contains(t, "switch"), strings.Contains(t, "storage"):
		return "infra"
	default:
		return "other"
	}
}

// BuildAppTopology 以应用 CI 为中心，BFS 按关系展开子图并分层。
func (s *Service) BuildAppTopology(ctx context.Context, tenantID, appCIID string, depth int) (*AppTopology, error) {
	if depth <= 0 {
		depth = 2
	}
	app, err := s.pg.GetCI(ctx, appCIID, tenantID)
	if err != nil {
		return nil, err
	}
	if app == nil {
		return nil, errAppNotFound
	}
	all, err := s.loadAllCIs(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	rels, err := s.pg.ListRelations(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	byID := map[string]*model.CIModel{}
	for _, c := range all {
		byID[c.ID] = c
	}
	adj := map[string][]string{} // ciID → neighbor ciIDs
	for _, r := range rels {
		adj[r.SourceID] = append(adj[r.SourceID], r.TargetID)
		adj[r.TargetID] = append(adj[r.TargetID], r.SourceID)
	}

	// BFS 分层
	dist := map[string]int{appCIID: 0}
	queue := []string{appCIID}
	for len(queue) > 0 && depth > 0 {
		cur := queue[0]
		queue = queue[1:]
		if dist[cur] >= depth {
			continue
		}
		for _, nb := range adj[cur] {
			if _, seen := dist[nb]; seen {
				continue
			}
			dist[nb] = dist[cur] + 1
			queue = append(queue, nb)
		}
	}

	result := &AppTopology{AppCIID: appCIID, AppName: app.Name, Nodes: []AppTopoNode{}, Edges: []AppTopoEdge{}}
	layerOrder := []string{"business", "app", "middleware", "database", "infra", "other"}
	nodeLayer := map[string]string{}
	// 收集节点并分层
	ids := make([]string, 0, len(dist))
	for id := range dist {
		ids = append(ids, id)
	}
	for _, id := range ids {
		ci := byID[id]
		if ci == nil {
			continue
		}
		mt, _ := s.ResolveModelType(ctx, ci.TypeID, tenantID)
		layer := layerForCI(ci, mt)
		// 根节点强制 app 层
		if id == appCIID {
			layer = "app"
		}
		nodeLayer[id] = layer
		result.Nodes = append(result.Nodes, AppTopoNode{
			ID: id, Name: ci.Name, TypeID: ci.TypeID,
			Category: ciCategory(mt), Layer: layer,
		})
	}
	// 边（仅保留子图内）
	for _, r := range rels {
		if _, ok := dist[r.SourceID]; ok {
			if _, ok2 := dist[r.TargetID]; ok2 {
				result.Edges = append(result.Edges, AppTopoEdge{Source: r.SourceID, Target: r.TargetID, Type: r.RelationType})
			}
		}
	}
	for _, layer := range layerOrder {
		ids := []string{}
		for id, l := range nodeLayer {
			if l == layer {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			sort.Strings(ids)
			result.Layers = append(result.Layers, TopoLayer{Name: layer, Nodes: ids})
		}
	}
	result.TotalCI = len(result.Nodes)
	return result, nil
}

func ciCategory(mt *model.CIModelType) string {
	if mt != nil {
		return mt.Category
	}
	return ""
}
