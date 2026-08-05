package cmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

func p2Repo(t *testing.T) *pg.Repository {
	t.Helper()
	dsn := os.Getenv("OPSNEXUS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("OPSNEXUS_POSTGRES_DSN not set; skipping live DB test")
	}
	repo, err := pg.NewRepository(dsn)
	if err != nil {
		t.Fatalf("new repo: %v", err)
	}
	if err := repo.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return repo
}

// TestP2FieldRules 验证字段级规则权限：hidden 字段被剔除、readonly 字段被标注。
func TestP2FieldRules(t *testing.T) {
	repo := p2Repo(t)
	ctx := context.Background()
	traceID := "p2-fr"

	typeID := "p2_fr_host_" + time.Now().Format("150405")
	mt := &model.CIModelType{TenantID: "default", Name: typeID, Category: model.ModelCategoryInfra,
		Schema: map[string]model.AttrDef{"ip": {Type: "string"}, "secret": {Type: "string"}}}
	if err := repo.CreateCIModelType(ctx, mt); err != nil {
		t.Fatalf("create model: %v", err)
	}
	ci := &model.CIModel{TypeID: mt.ID, TenantID: "default", Name: "fr-host", Status: model.CIStatusActive,
		Attributes: map[string]any{"ip": "10.0.0.1", "secret": "pwd-1"}, Channel: model.WeightHuman}
	if err := repo.CreateCI(ctx, ci, traceID); err != nil {
		t.Fatalf("create ci: %v", err)
	}
	defer func() {
		_ = repo.DeleteCI(ctx, ci.ID, "default", traceID)
		_ = repo.DeleteCIModelType(ctx, mt.ID, "default")
	}()

	// 规则：ip 对 cmdb:write 只读；secret 对 cmdb:write 隐藏
	for _, r := range []*model.FieldRule{
		{TenantID: "default", ModelTypeID: mt.ID, Field: "ip", Permission: "cmdb:write", Action: model.FieldActionReadonly},
		{TenantID: "default", ModelTypeID: mt.ID, Field: "secret", Permission: "cmdb:write", Action: model.FieldActionHidden},
	} {
		if err := repo.CreateFieldRule(ctx, r); err != nil {
			t.Fatalf("create rule: %v", err)
		}
		defer func(id string) { _ = repo.DeleteFieldRule(ctx, id, "default") }(r.ID)
	}

	rules, err := repo.ListFieldRules(ctx, "default", mt.ID)
	if err != nil || len(rules) != 2 {
		t.Fatalf("list rules: %v (%d)", err, len(rules))
	}

	// 拥有 cmdb:write 的用户：secret 隐藏、ip 只读
	applyFieldRules([]*model.CIModel{ci}, rules, fieldRuleSubject{perms: []string{"cmdb:write"}})
	if _, ok := ci.Attributes["secret"]; ok {
		t.Errorf("secret should be hidden")
	}
	if _, ok := ci.Attributes["ip"]; !ok {
		t.Errorf("ip should remain")
	}
	if ci.FieldAccess["ip"] != "readonly" {
		t.Errorf("ip should be readonly, got %v", ci.FieldAccess)
	}
	if ci.FieldAccess["secret"] != "hidden" {
		t.Errorf("secret should be hidden, got %v", ci.FieldAccess)
	}
}

// TestP2SubscriptionWebhook 验证订阅 webhook 投递 + 投递日志（同步 test 接口）。
func TestP2SubscriptionWebhook(t *testing.T) {
	repo := p2Repo(t)
	svc := &Service{pg: repo}

	// 本地 webhook 接收端
	var got string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		got = string(buf[:n])
		w.WriteHeader(200)
	}))
	defer hook.Close()

	sub := &model.Subscription{TenantID: "default", Name: "p2-test-webhook", EventType: model.SubEventCIAll, Channel: model.SubChannelWebhook, Target: hook.URL, Enabled: true}
	if err := repo.CreateSubscription(ctxWithTenant(), sub); err != nil {
		t.Fatalf("create sub: %v", err)
	}
	defer func() { _ = repo.DeleteSubscription(context.Background(), sub.ID, "default") }()

	// 走真实 mux 触发测试投递（PathValue 需要经 ServeMux 路由才能解析）
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	req := httptest.NewRequest("POST", "/api/v1/cmdb/subscriptions/"+sub.ID+"/test", nil)
	req = req.WithContext(contextWithTenant(context.Background(), "default"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("test sub status %d body=%s", rec.Code, rec.Body.String())
	}
	var d model.SubscriptionDelivery
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("parse delivery: %v", err)
	}
	if d.Status != "success" {
		t.Fatalf("delivery status=%s err=%s", d.Status, d.Error)
	}
	if !strings.Contains(got, "event_type") {
		t.Fatalf("webhook payload missing: %s", got)
	}
	// 投递日志应已记录
	items, err := repo.ListSubscriptionDeliveries(context.Background(), "default", 5)
	if err != nil || len(items) == 0 {
		t.Fatalf("deliveries: %v (%d)", err, len(items))
	}
	found := false
	for _, it := range items {
		if it.SubID == sub.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("delivery log missing for sub")
	}
}

// TestP2IDC 验证机房/机柜/U 位分配与非法占用检测。
func TestP2IDC(t *testing.T) {
	repo := p2Repo(t)
	ctx := context.Background()

	room := &model.IDCRoom{TenantID: "default", Name: "p2-idc"}
	if err := repo.CreateIDCRoom(ctx, room); err != nil {
		t.Fatalf("create idc: %v", err)
	}
	defer func() { _ = repo.DeleteIDCRoom(ctx, room.ID, "default") }()

	rack := &model.Rack{TenantID: "default", IDCRoomID: room.ID, Name: "R01", MaxUnits: 42}
	if err := repo.CreateRack(ctx, rack); err != nil {
		t.Fatalf("create rack: %v", err)
	}
	defer func() { _ = repo.DeleteRack(ctx, rack.ID, "default") }()

	// 分配 U1/U2 给同一台 CI（设备占 2U）
	ciID := "p2-device-001"
	if err := repo.AssignRackUnits(ctx, rack.ID, "default", []*model.RackUnit{
		{RackID: rack.ID, Unit: 1, CIID: ciID, Label: "server-a"},
		{RackID: rack.ID, Unit: 2, CIID: ciID, Label: "server-a"},
	}); err != nil {
		t.Fatalf("assign units: %v", err)
	}
	units, err := repo.ListRackUnits(ctx, rack.ID, "default")
	if err != nil || len(units) != 2 {
		t.Fatalf("list units: %v (%d)", err, len(units))
	}

	// 第二台机柜：同一 CI 再次占用 → 非法占用检测
	rack2 := &model.Rack{TenantID: "default", IDCRoomID: room.ID, Name: "R02", MaxUnits: 42}
	if err := repo.CreateRack(ctx, rack2); err != nil {
		t.Fatalf("create rack2: %v", err)
	}
	defer func() { _ = repo.DeleteRack(ctx, rack2.ID, "default") }()
	err = repo.AssignRackUnits(ctx, rack2.ID, "default", []*model.RackUnit{{RackID: rack2.ID, Unit: 1, CIID: ciID}})
	if err == nil || !strings.Contains(err.Error(), "非法占用") {
		t.Fatalf("expected 非法占用 error, got %v", err)
	}

	// 超范围 U 位
	err = repo.AssignRackUnits(ctx, rack.ID, "default", []*model.RackUnit{{RackID: rack.ID, Unit: 43}})
	if err == nil {
		t.Fatalf("expected out-of-range error")
	}
}

// TestP2Stats 验证统计聚合返回基础数字。
func TestP2Stats(t *testing.T) {
	repo := p2Repo(t)
	ctx := context.Background()
	st, err := repo.Stats(ctx, "default")
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.TotalModels < 0 || st.TotalInstances < 0 || st.TotalRelations < 0 {
		t.Fatalf("negative stats: %+v", st)
	}
}

func ctxWithTenant() context.Context {
	return contextWithTenant(context.Background(), "default")
}
