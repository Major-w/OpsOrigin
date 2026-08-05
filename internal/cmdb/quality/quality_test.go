package quality

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestNonEmpty(t *testing.T) {
	cases := []struct {
		v    any
		want bool
	}{
		{nil, false},
		{"", false},
		{"x", true},
		{0, false},
		{1, true},
		{0.0, false},
		{1.5, true},
		{false, false},
		{true, true},
		{[]any{}, false},
		{[]any{1}, true},
		{map[string]any{}, false},
		{map[string]any{"a": 1}, true},
	}
	for _, tc := range cases {
		if got := nonEmpty(tc.v); got != tc.want {
			t.Errorf("nonEmpty(%#v)=%v want %v", tc.v, got, tc.want)
		}
	}
}

func TestFirstIP(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]any
		want  string
	}{
		{"empty", map[string]any{}, ""},
		{"ip key", map[string]any{"ip": "1.2.3.4"}, "1.2.3.4"},
		{"ip wins over management_ip", map[string]any{"ip": "1.1.1.1", "management_ip": "9.9.9.9"}, "1.1.1.1"},
		{"management_ip only", map[string]any{"management_ip": "9.9.9.9"}, "9.9.9.9"},
		{"private_ip fallback", map[string]any{"private_ip": "5.5.5.5"}, "5.5.5.5"},
		{"non-string skipped", map[string]any{"ip": 123}, ""},
	}
	for _, tc := range cases {
		if got := firstIP(tc.attrs); got != tc.want {
			t.Errorf("firstIP(%v)=%q want %q", tc.attrs, got, tc.want)
		}
	}
}

func TestSeverityRank(t *testing.T) {
	if severityRank(SeverityHigh) >= severityRank(SeverityMedium) {
		t.Error("high should rank above medium")
	}
	if severityRank(SeverityMedium) >= severityRank(SeverityLow) {
		t.Error("medium should rank above low")
	}
}

func TestSortIssues(t *testing.T) {
	issues := []Issue{
		{CIID: "3", Dimension: DimConsistency, Severity: SeverityLow, Message: "low"},
		{CIID: "1", Dimension: DimCompleteness, Severity: SeverityHigh, Message: "high"},
		{CIID: "2", Dimension: DimUniqueness, Severity: SeverityMedium, Message: "med"},
	}
	sortIssues(issues)
	if issues[0].Severity != SeverityHigh || issues[1].Severity != SeverityMedium || issues[2].Severity != SeverityLow {
		t.Errorf("sort order wrong: %v %v %v", issues[0].Severity, issues[1].Severity, issues[2].Severity)
	}
}

func TestRequiredFields(t *testing.T) {
	schema := map[string]model.AttrDef{
		"name": {Required: true},
		"ip":   {Required: true},
		"note": {Required: false},
	}
	got := requiredFields(schema)
	if len(got) != 2 {
		t.Errorf("requiredFields len=%d want 2", len(got))
	}
}
