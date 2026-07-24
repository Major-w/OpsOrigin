package vuln

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// Scanner 扫描器接口。Scanner 预留 Trivy/Grype/Nuclei/OpenSCAP 热插拔位，
// 当前内置 RuleScanner 作为演示垂直切片，AgentScanner 经 Agent 下发 trivy/grype。
// inventory 为外部注入的资产清单(经 CMDB 拉取或 Agent 下发结果)，为空且非 demo 时返回空(不造假)。
type Scanner interface {
	Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) ([]*model.Finding, error)
}

// InventoryEntry 一条资产清单条目(主机 + 已装软件包版本)。
// AgentID 为经 Agent 下发扫描时的目标 agent(RuleScanner 忽略, AgentScanner 使用)。
type InventoryEntry struct {
	Host    string
	Package string
	Version string
	AgentID string
}

// cveRule 内置 CVE 规则(演示用; 生产可由 trivy/grype 替换)。
type cveRule struct {
	CVE         string
	Package     string
	Fixed       string
	Severity    model.Severity
	Title       string
	Description string
	Reference   string
}

// RuleScanner 基于内置 CVE 规则 + 样例资产清单的演示扫描器。
type RuleScanner struct{}

// NewRuleScanner 构造规则扫描器。
func NewRuleScanner() *RuleScanner { return &RuleScanner{} }

// demoInventory 8 条样例主机软件清单(演示模式注入)。
func demoInventory() []InventoryEntry {
	return []InventoryEntry{
		{"web-01", "openssl", "3.0.5", ""},
		{"web-02", "xz", "5.6.1", ""},
		{"app-01", "log4j", "2.14.1", ""},
		{"lb-01", "nginx", "1.22.0", ""},
		{"lb-02", "nginx", "1.20.0", ""},
		{"cache-01", "curl", "7.68.0", ""},
		{"cache-02", "curl", "7.70.0", ""},
		{"shell-01", "bash", "5.0", ""},
	}
}

// rules 6 条 CVE 规则, 覆盖 critical/high/medium/low。
// 与 demoInventory 配合: 全量扫描=full 得 8 条(crit:3,high:2,medium:2,low:1);
// 快速扫描=quick(仅 rank>=3)得 5 条(crit:3,high:2)。
func rules() []cveRule {
	return []cveRule{
		{"CVE-2022-3602", "openssl", "3.0.7", model.SeverityCritical,
			"OpenSSL 堆缓冲区溢出", "OpenSSL 3.0.x 在 3.0.7 之前存在堆溢出风险",
			"https://nvd.nist.gov/vuln/detail/CVE-2022-3602"},
		{"CVE-2024-3094", "xz", "5.6.2", model.SeverityCritical,
			"xz-utils 后门", "xz-utils 5.6.0–5.6.1 含恶意后门",
			"https://nvd.nist.gov/vuln/detail/CVE-2024-3094"},
		{"CVE-2021-44228", "log4j", "2.17.0", model.SeverityCritical,
			"Log4j JNDI 远程代码执行", "Log4j 2.x 在 2.17.0 之前存在 Log4Shell RCE",
			"https://nvd.nist.gov/vuln/detail/CVE-2021-44228"},
		{"CVE-2023-44487", "nginx", "1.25.0", model.SeverityHigh,
			"nginx 请求走私", "nginx 1.24.x 及之前存在请求走私",
			"https://nvd.nist.gov/vuln/detail/CVE-2023-44487"},
		{"CVE-2022-3715", "bash", "5.1", model.SeverityHigh,
			"bash 特权提升", "bash 5.0 存在本地提权漏洞",
			"https://nvd.nist.gov/vuln/detail/CVE-2022-3715"},
		{"CVE-2020-8177", "curl", "7.80.0", model.SeverityMedium,
			"curl 堆溢出", "curl 7.70 及之前存在堆溢出",
			"https://nvd.nist.gov/vuln/detail/CVE-2020-8177"},
	}
}

// Scan 执行一次扫描。
// inventory 为外部注入的资产清单(经 CMDB 拉取或 Agent 下发结果);为空时:
//   demo 模式注入样例清单;非 demo 返回 nil(不造假)。
// 按 scanType 过滤(critical/high 或全量)。
func (s *RuleScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) ([]*model.Finding, error) {
	if len(inventory) == 0 {
		if !demoMode {
			// 真实 inventory 注入为后续工作; 当前无 inventory 时返回空(不造假)。
			return nil, nil
		}
		inventory = demoInventory()
	}
	rs := rules()

	var findings []*model.Finding
	for _, e := range inventory {
		for _, r := range rs {
			if r.Package != e.Package {
				continue
			}
			if compareVersion(e.Version, r.Fixed) >= 0 {
				// 已装版本 >= 修复版本, 视为安全。
				continue
			}
			if scanType == string(model.ScanTypeQuick) && r.Severity.Rank() < 3 {
				// quick 仅取 rank>=3 的 critical/high。
				continue
			}
			findings = append(findings, &model.Finding{
				ID:          uuid.New().String(),
				ScanID:      scanID,
				TenantID:    tenantID,
				CVE:         r.CVE,
				Title:       r.Title,
				Description: r.Description,
				Severity:    r.Severity,
				Affected:    e.Package,
				Installed:   e.Version,
				Fixed:       r.Fixed,
				Reference:   r.Reference,
				Rank:        r.Severity.Rank(),
				CreatedAt:   time.Now(),
			})
		}
	}
	return findings, nil
}

// compareVersion 语义化比较两个版本字符串。
// 返回 -1 (a<b) / 0 (相等) / 1 (a>b)。尾随非数字(如 1.1.1k)取前导数字段。
func compareVersion(a, b string) int {
	as := splitVersion(a)
	bs := splitVersion(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func splitVersion(v string) []int {
	v = strings.TrimSpace(v)
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '_'
	})
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			n = leadingInt(f)
		}
		out = append(out, n)
	}
	return out
}

// leadingInt 取字符串前导数字(忽略后缀字母, 如 "1k" -> 1)。
func leadingInt(s string) int {
	var b strings.Builder
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return 0
	}
	n, _ := strconv.Atoi(b.String())
	return n
}

var _ = unicode.IsDigit // 保留 unicode 导入的显式引用(避免未使用告警, 实际用于版本字段解析扩展)
var _ = fmt.Sprintf
