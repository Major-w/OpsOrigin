package vuln

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

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
	res, err := s.Scan(context.Background(), "default", "scan-1", string(model.ScanTypeFull), true, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	findings := res.Findings
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
	// 每条 finding 都应有必要字段(含新生命周期字段)
	for _, f := range findings {
		if f.CVE == "" || f.Installed == "" || f.Fixed == "" || f.ScanID != "scan-1" {
			t.Errorf("finding 字段不完整: %+v", f)
		}
		if f.DedupeKey == "" || f.Host == "" || f.Status != model.FindingOpen {
			t.Errorf("生命周期字段未填充: %+v", f)
		}
		if f.FirstSeen.IsZero() || f.LastSeen.IsZero() {
			t.Errorf("first/last_seen 未填充: %+v", f)
		}
		if f.Remediation == "" {
			t.Errorf("修复建议未生成: %+v", f)
		}
	}
	// 覆盖率:demo 清单 8 台不同主机
	if res.Targets != 8 {
		t.Errorf("demo full 覆盖率应为 8 台, got %d", res.Targets)
	}
	if len(res.ScannedHosts) != 8 {
		t.Errorf("ScannedHosts 应含 8 台, got %d", len(res.ScannedHosts))
	}
}

func TestRuleScanner_ScanDemoQuick(t *testing.T) {
	s := NewRuleScanner()
	res, err := s.Scan(context.Background(), "default", "scan-2", string(model.ScanTypeQuick), true, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	// quick 仅 rank>=3: 3 critical + 3 high(nginx×2 + bash)
	if len(res.Findings) != 6 {
		t.Fatalf("demo quick 应得 6 条, got %d", len(res.Findings))
	}
	for _, f := range res.Findings {
		if f.Severity.Rank() < 3 {
			t.Errorf("quick 不应包含 low/medium: %s(%s)", f.CVE, f.Severity)
		}
	}
}

// 关键回归:非 demo 且无资产时必须是「无目标可扫」(Targets=0),而不是被当成「0 漏洞」。
func TestRuleScanner_EmptyInventoryReportsNoTarget(t *testing.T) {
	s := NewRuleScanner()
	res, err := s.Scan(context.Background(), "default", "scan-3", string(model.ScanTypeFull), false, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("非 demo 空 inventory 不应造假, got %d 条", len(res.Findings))
	}
	if res.Targets != 0 {
		t.Fatalf("空 inventory 应报无目标(Targets=0), got %d", res.Targets)
	}
}

func TestRuleScanner_ScanCustomInventory(t *testing.T) {
	s := NewRuleScanner()
	inventory := []InventoryEntry{
		{Host: "h1", Package: "openssl", Version: "3.0.5"},   // < 3.0.7 → 命中
		{Host: "h2", Package: "openssl", Version: "3.0.8"},   // >= 3.0.7 → 安全
		{Host: "h3", Package: "unknown-pkg", Version: "1.0"}, // 无规则 → 不命中
	}
	res, err := s.Scan(context.Background(), "default", "scan-4", string(model.ScanTypeFull), false, inventory)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("自定义 inventory 应只命中 openssl 3.0.5, got %d 条: %+v", len(res.Findings), res.Findings)
	}
	if res.Findings[0].CVE != "CVE-2022-3602" || res.Findings[0].Installed != "3.0.5" {
		t.Errorf("命中详情不符: %+v", res.Findings[0])
	}
	if res.Findings[0].Host != "h1" {
		t.Errorf("资产归属应为 h1: %+v", res.Findings[0])
	}
}

func TestRuleScanner_ScanFixedVersionSafe(t *testing.T) {
	s := NewRuleScanner()
	inventory := []InventoryEntry{
		{Host: "h1", Package: "log4j", Version: "2.17.1"}, // 已修复
		{Host: "h2", Package: "log4j", Version: "2.16.0"}, // 仍脆弱
	}
	res, err := s.Scan(context.Background(), "default", "scan-5", string(model.ScanTypeFull), false, inventory)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("应仅命中 2.16.0, got %d", len(res.Findings))
	}
	if res.Findings[0].Installed != "2.16.0" {
		t.Errorf("命中的应为 2.16.0: %+v", res.Findings[0])
	}
	// 覆盖率含两台主机(即使 h1 无命中,也必须算作「扫过」,否则复扫会误判为已修复)。
	if res.Targets != 2 {
		t.Errorf("覆盖率应为 2 台, got %d", res.Targets)
	}
}

// 去重键必须只由 (租户, CVE, 包, 已装版本, 资产) 决定:
// 同一漏洞跨扫描稳定同键,修复版本变化/不同资产不得混同。
func TestDedupeKeySemantics(t *testing.T) {
	a := dedupeKey("default", "CVE-2021-44228", "log4j", "2.14.1", "app-01")
	b := dedupeKey("default", "CVE-2021-44228", "log4j", "2.14.1", "app-01")
	if a != b {
		t.Fatalf("同实例去重键应稳定: %s != %s", a, b)
	}
	if a == dedupeKey("default", "CVE-2021-44228", "log4j", "2.14.1", "app-02") {
		t.Error("不同资产不应共用去重键")
	}
	if a == dedupeKey("default", "CVE-2021-44228", "log4j", "2.15.0", "app-01") {
		t.Error("不同已装版本不应共用去重键")
	}
	if a == dedupeKey("other", "CVE-2021-44228", "log4j", "2.14.1", "app-01") {
		t.Error("跨租户不应共用去重键")
	}
	// 拼接歧义防护:字段边界不同不得碰撞
	if dedupeKey("t", "a|b", "c", "d", "e") == dedupeKey("t", "a", "b|c", "d", "e") {
		t.Error("字段边界不同不应碰撞(需 \x00 分隔)")
	}
}

// 元信息行必须被剥离,且从中解析出漏洞库更新时间;JSON 本体保持原样。
func TestParseScanOutput(t *testing.T) {
	epoch := time.Date(2026, 9, 12, 3, 0, 0, 0, time.UTC).Unix()
	out := metaPrefix + "db_mtime_epoch=" + itoa(epoch) + "\n" + `{"SchemaVersion":2,"Results":[]}`
	body, dbAt := parseScanOutput(out)
	if strings.Contains(body, metaPrefix) {
		t.Errorf("元信息行未被剥离: %q", body)
	}
	if !strings.Contains(body, "SchemaVersion") {
		t.Errorf("JSON 本体被破坏: %q", body)
	}
	if dbAt == nil || dbAt.Unix() != epoch {
		t.Errorf("漏洞库时间解析失败: %v", dbAt)
	}
	// 无元信息时原样返回
	body2, dbAt2 := parseScanOutput(`{"Results":[]}`)
	if dbAt2 != nil || !strings.Contains(body2, "Results") {
		t.Errorf("无元信息场景解析异常: %q %v", body2, dbAt2)
	}
}

// trivy 输出为真 JSON 时正常解析;空输出视为无漏洞;非 JSON 必须报错(供上游判失败)。
func TestParseTrivy(t *testing.T) {
	if m, err := parseTrivy(""); err != nil || m != nil {
		t.Errorf("空输出应返回 nil,nil: %v %v", m, err)
	}
	if _, err := parseTrivy("trivy: command not found"); err == nil {
		t.Error("非 JSON 输出必须返回错误(否则会被误判为 0 漏洞)")
	}
	good := `{"ArtifactName":"/","Results":[{"Vulnerabilities":[{"VulnerabilityID":"CVE-1","Severity":"CRITICAL","PkgName":"openssl","CVSS":{"nvd":{"V3Score":9.8}}}]}]}`
	m, err := parseTrivy(good)
	if err != nil {
		t.Fatalf("合法 JSON 解析失败: %v", err)
	}
	for _, vs := range m {
		if len(vs) != 1 || vs[0].cvssScore() != 9.8 {
			t.Errorf("解析结果不符: %+v", vs)
		}
	}
}

func TestScanResultErrorSummary(t *testing.T) {
	r := &ScanResult{}
	if r.errorSummary() != "" {
		t.Error("无错误时应返回空串")
	}
	r.addError("a 失败")
	r.addError("b 失败")
	r.addError("c 失败")
	r.addError("d 失败")
	s := r.errorSummary()
	if !strings.Contains(s, "a 失败") || !strings.Contains(s, "共 4 条") {
		t.Errorf("摘要应含前三条并提示总数: %q", s)
	}
	// 超长原因需截断
	r2 := &ScanResult{}
	r2.addError("%s", strings.Repeat("x", 500))
	if len(r2.Errors[0]) > 210 {
		t.Errorf("错误原因未截断: %d", len(r2.Errors[0]))
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}
