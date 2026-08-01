package cmdb

import (
	"testing"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestApplyCollectionMeta(t *testing.T) {
	now := time.Now()

	// agent 通道 → 每个字段标记为 agent 来源
	agentTask := &model.CollectionTask{Channel: "agent"}
	cis := []*model.CIModel{
		{Attributes: map[string]any{"hostname": "h1", "ip": "1.1.1.1"}},
	}
	applyCollectionMeta(cis, agentTask)
	m := cis[0].AttributeMeta
	if m["hostname"].Source != model.SourceAgent {
		t.Errorf("agent task: hostname source = %s, want agent", m["hostname"].Source)
	}
	if m["hostname"].Confidence != model.DefaultConfidence(model.SourceAgent) {
		t.Errorf("agent task: confidence = %v, want %v", m["hostname"].Confidence, model.DefaultConfidence(model.SourceAgent))
	}
	if m["ip"].LastSeenAt.IsZero() {
		t.Errorf("ip last_seen_at is zero")
	}

	// server 通道（当前仅云）→ 标记为 cloud_api 来源
	serverTask := &model.CollectionTask{Channel: "server"}
	cis2 := []*model.CIModel{{Attributes: map[string]any{"region": "cn"}}}
	applyCollectionMeta(cis2, serverTask)
	if cis2[0].AttributeMeta["region"].Source != model.SourceCloudAPI {
		t.Errorf("server task: region source = %s, want cloud_api", cis2[0].AttributeMeta["region"].Source)
	}

	// 采集器已显式上报 → 跳过兜底，保留其覆盖值
	cis3 := []*model.CIModel{{
		Attributes:    map[string]any{"k": "v"},
		AttributeMeta: map[string]model.AttrMeta{"k": {Source: model.SourceManual, Confidence: 1.0, LastSeenAt: now}},
	}}
	applyCollectionMeta(cis3, &model.CollectionTask{Channel: "server"})
	if cis3[0].AttributeMeta["k"].Source != model.SourceManual {
		t.Errorf("explicit override should be preserved, got %s", cis3[0].AttributeMeta["k"].Source)
	}
}
