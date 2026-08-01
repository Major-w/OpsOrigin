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
