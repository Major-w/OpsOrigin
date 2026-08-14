package vuln

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/vuln/model"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"3.0.5", "3.0.7", -1},
		{"3.0.7", "3.0.5", 1},
		{"3.0.5", "3.0.5", 0},
		{"5.6.1", "5.6.2", -1},
		{"1.20.0", "1.22.0", -1},
		{"1.22.0", "1.20.0", 1},
		{"5.0", "5.1", -1},
		{"7.68.0", "7.80.0", -1},
		{"2.14.1", "2.17.0", -1},
		// 段数不同
		{"1.0", "1.0.1", -1},
		{"1.0.1", "1.0", 1},
		{"1", "1.0", 0},
		// 尾随非数字后缀取前导数字
		{"1.1.1k", "1.1.1", 0},
		{"1.1.2a", "1.1.1k", 1},
		// 空白
		{" 3.0.5 ", "3.0.5", 0},
		// 预发布分隔符
		{"2.0.0-rc1", "2.0.0", 0},
	}
	for _, tc := range cases {
		if got := compareVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersion(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSplitVersion(t *testing.T) {
	cases := map[string][]int{
		"3.0.5":     {3, 0, 5},
		"5.6.1":     {5, 6, 1},
		"1.1.1k":    {1, 1, 1},
		"2.0.0-rc1": {2, 0, 0, 0}, // "rc1" 无前导数字 → 0
		"1.0":       {1, 0},
		"abc":       {0},
		"":          nil,
	}
	for in, want := range cases {
		got := splitVersion(in)
		if len(got) != len(want) {
			t.Errorf("splitVersion(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("splitVersion(%q)[%d] = %d, want %d", in, i, got[i], want[i])
			}
		}
	}
}

func TestLeadingInt(t *testing.T) {
	cases := map[string]int{
		"1k":   1,
		"5a":   5,
		"123x": 123,
		"abc":  0,
		"42":   42,
		"":     0,
	}
	for in, want := range cases {
		if got := leadingInt(in); got != want {
			t.Errorf("leadingInt(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestRuleScanner_ScanDemoFull(t *testing.T) {
	s := NewRuleScanner()
	findings, err := s.Scan(context.Background(), "default", "scan-1", string(model.ScanTypeFull), true, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// 8 条样例清单: openssl/xz/log4j 各 1, nginx 2, curl 2, bash 1
	if len(findings) != 8 {
		t.Fatalf("demo full 应得 8 条, got %d", len(findings))
	}
	severities := map[model.Severity]int{}
	for _, f := range findings {
		severities[f.Severity]++
	}
	if severities[model.SeverityCritical] != 3 || severities[model.SeverityHigh] != 3 || severities[model.SeverityMedium] != 2 {
		t.Errorf("severity 分布不符: %v", severities)
	}
	// 每条 finding 都应有必要字段
	for _, f := range findings {
		if f.CVE == "" || f.Installed == "" || f.Fixed == "" || f.ScanID != "scan-1" {
			t.Errorf("finding 字段不完整: %+v", f)
		}
	}
}

func TestRuleScanner_ScanDemoQuick(t *testing.T) {
	s := NewRuleScanner()
	findings, err := s.Scan(context.Background(), "default", "scan-2", string(model.ScanTypeQuick), true, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// quick 仅 rank>=3: 3 critical + 3 high(nginx×2 + bash)
	if len(findings) != 6 {
		t.Fatalf("demo quick 应得 6 条, got %d", len(findings))
	}
	for _, f := range findings {
		if f.Severity.Rank() < 3 {
			t.Errorf("quick 不应包含 low/medium: %s(%s)", f.CVE, f.Severity)
		}
	}
}

func TestRuleScanner_ScanEmptyInventoryNoFabrication(t *testing.T) {
	s := NewRuleScanner()
	findings, err := s.Scan(context.Background(), "default", "scan-3", string(model.ScanTypeFull), false, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("非 demo 空 inventory 不应造假, got %d 条", len(findings))
	}
}

func TestRuleScanner_ScanCustomInventory(t *testing.T) {
	s := NewRuleScanner()
	inventory := []InventoryEntry{
		{Host: "h1", Package: "openssl", Version: "3.0.5"},   // < 3.0.7 → 命中
		{Host: "h2", Package: "openssl", Version: "3.0.8"},   // >= 3.0.7 → 安全
		{Host: "h3", Package: "unknown-pkg", Version: "1.0"}, // 无规则 → 不命中
	}
	findings, err := s.Scan(context.Background(), "default", "scan-4", string(model.ScanTypeFull), false, inventory)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("自定义 inventory 应只命中 openssl 3.0.5, got %d 条: %+v", len(findings), findings)
	}
	if findings[0].CVE != "CVE-2022-3602" || findings[0].Installed != "3.0.5" {
		t.Errorf("命中详情不符: %+v", findings[0])
	}
}

func TestRuleScanner_ScanFixedVersionSafe(t *testing.T) {
	s := NewRuleScanner()
	inventory := []InventoryEntry{
		{Host: "h1", Package: "log4j", Version: "2.17.1"}, // 已修复
		{Host: "h2", Package: "log4j", Version: "2.16.0"}, // 仍脆弱
	}
	findings, err := s.Scan(context.Background(), "default", "scan-5", string(model.ScanTypeFull), false, inventory)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("应仅命中 2.16.0, got %d", len(findings))
	}
	if findings[0].Installed != "2.16.0" {
		t.Errorf("命中的应为 2.16.0: %+v", findings[0])
	}
}
