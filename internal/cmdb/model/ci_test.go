package model

import (
	"testing"
	"time"
)

func TestDefaultConfidence(t *testing.T) {
	cases := map[SourceKind]float64{
		SourceManual:     1.0,
		SourceITSM:       0.85,
		SourceAgent:      0.9,
		SourceCloudAPI:   0.8,
		SourceAutoScript: 0.5,
		SourceDiscovery:  0.3,
	}
	for src, want := range cases {
		if got := DefaultConfidence(src); got != want {
			t.Errorf("DefaultConfidence(%s) = %v, want %v", src, got, want)
		}
	}
}

func TestSourceFromChannelWeight(t *testing.T) {
	if got := SourceFromChannelWeight(WeightHuman); got != SourceManual {
		t.Errorf("WeightHuman -> %s, want manual", got)
	}
	if got := SourceFromChannelWeight(WeightITSM); got != SourceITSM {
		t.Errorf("WeightITSM -> %s, want itsm", got)
	}
	if got := SourceFromChannelWeight(WeightDiscovery); got != SourceDiscovery {
		t.Errorf("WeightDiscovery -> %s, want discovery", got)
	}
}

func TestShouldOverwriteField(t *testing.T) {
	// 人工字段不可被任何采集来源覆盖
	if ShouldOverwriteField(SourceCloudAPI, SourceManual) {
		t.Error("cloud_api must NOT overwrite manual")
	}
	if ShouldOverwriteField(SourceAgent, SourceManual) {
		t.Error("agent must NOT overwrite manual")
	}
	// 同/高权重采集源之间可相互刷新（新值胜出）
	if !ShouldOverwriteField(SourceCloudAPI, SourceDiscovery) {
		t.Error("cloud_api should overwrite discovery (equal-or-higher weight)")
	}
	if !ShouldOverwriteField(SourceAgent, SourceCloudAPI) {
		t.Error("agent should overwrite cloud_api (equal weight)")
	}
	// 人工可覆盖采集
	if !ShouldOverwriteField(SourceManual, SourceCloudAPI) {
		t.Error("manual should overwrite cloud_api")
	}
}

func TestBuildAttrMeta(t *testing.T) {
	attrs := map[string]any{"ip": "1.2.3.4", "os": "linux"}
	now := time.Now()
	m := BuildAttrMeta(attrs, SourceManual, now)
	if len(m) != 2 {
		t.Fatalf("len = %d, want 2", len(m))
	}
	for k, v := range m {
		if v.Source != SourceManual {
			t.Errorf("%s source = %s, want manual", k, v.Source)
		}
		if v.Confidence != 1.0 {
			t.Errorf("%s confidence = %v, want 1.0", k, v.Confidence)
		}
		if v.LastSeenAt.IsZero() {
			t.Errorf("%s last_seen_at is zero", k)
		}
	}
}
