package cmdb

import (
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func mkCI(id, name, typeID string, ip string, conf float64) *model.CIModel {
	ci := &model.CIModel{
		ID: id, Name: name, TypeID: typeID, TenantID: "default",
		Status: model.CIStatusActive, UpdatedAt: time.Now(),
		Attributes: map[string]any{},
	}
	if ip != "" {
		ci.Attributes["ip"] = ip
	}
	meta := map[string]model.AttrMeta{}
	for k := range ci.Attributes {
		meta[k] = model.AttrMeta{Source: model.SourceManual, Confidence: conf, LastSeenAt: time.Now()}
	}
	ci.AttributeMeta = meta
	return ci
}

func TestGroupCandidates_RepeatIP(t *testing.T) {
	cis := []*model.CIModel{
		mkCI("a", "web-01", "server", "10.0.0.1", 1.0),
		mkCI("b", "i-0x123", "server", "10.0.0.1", 0.3), // 云 API 低置信
		mkCI("c", "db-01", "database", "10.0.0.2", 0.9),
	}
	groups := groupCandidates(cis)
	if len(groups) != 1 {
		t.Fatalf("应只有 1 组重复(IP 10.0.0.1), got %d: %+v", len(groups), groups)
	}
	g := groups[0]
	if g.Reason != "重复 IP 10.0.0.1" {
		t.Errorf("reason 不符: %s", g.Reason)
	}
	if len(g.Members) != 2 {
		t.Fatalf("组内应有 2 个成员, got %d", len(g.Members))
	}
	// 按置信度降序: 高置信(手工 web-01)在前
	if g.Members[0].CIID != "a" || g.Members[1].CIID != "b" {
		t.Errorf("成员应按置信度降序: %v", g.Members)
	}
}

func TestGroupCandidates_RepeatNameSameType(t *testing.T) {
	cis := []*model.CIModel{
		mkCI("a", "web-01", "server", "10.0.0.1", 0.9),
		mkCI("b", "web-01", "server", "10.0.0.2", 0.5), // 同名同类型不同 IP
		mkCI("c", "web-01", "database", "10.0.0.3", 0.8), // 同名不同类型 → 不归组
	}
	groups := groupCandidates(cis)
	if len(groups) != 1 {
		t.Fatalf("同名同类型应归 1 组, got %d", len(groups))
	}
	if groups[0].Reason != "重复名称(同类型)" {
		t.Errorf("reason 不符: %s", groups[0].Reason)
	}
	if len(groups[0].Members) != 2 {
		t.Errorf("组内应为 2 成员: %d", len(groups[0].Members))
	}
}

func TestGroupCandidates_NoDuplicates(t *testing.T) {
	cis := []*model.CIModel{
		mkCI("a", "web-01", "server", "10.0.0.1", 0.9),
		mkCI("b", "db-01", "database", "10.0.0.2", 0.9),
		mkCI("c", "redis-1", "redis", "", 0.5), // 无 IP
	}
	if groups := groupCandidates(cis); len(groups) != 0 {
		t.Fatalf("无重复应返回空, got %d", len(groups))
	}
}

func TestCiIP_KeyCompatibility(t *testing.T) {
	if got := ciIP(map[string]any{"ip": "1.2.3.4"}); got != "1.2.3.4" {
		t.Errorf("ip: %s", got)
	}
	if got := ciIP(map[string]any{"management_ip": "5.6.7.8"}); got != "5.6.7.8" {
		t.Errorf("management_ip: %s", got)
	}
	if got := ciIP(map[string]any{"hostname": "x"}); got != "" {
		t.Errorf("无 IP 键应返回空: %s", got)
	}
}

func TestAvgConfidence(t *testing.T) {
	meta := map[string]model.AttrMeta{
		"a": {Confidence: 1.0},
		"b": {Confidence: 0.5},
	}
	if got := avgConfidence(meta); got != 0.75 {
		t.Errorf("avg = %v, want 0.75", got)
	}
	if got := avgConfidence(nil); got != 0 {
		t.Errorf("空 meta avg 应为 0: %v", got)
	}
}
