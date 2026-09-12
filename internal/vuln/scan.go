package vuln

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/vuln/model"
)

// Scanner 扫描器接口。Scanner 预留 Trivy/Grype/Nuclei/OpenSCAP 热插拔位，
// 当前内置 RuleScanner(基于内置 CVE 规则的演示/离线切片)、
// AgentScanner(经 Agent 下发 trivy fs 扫主机)、ImageScanner(经 Agent 下发 trivy image 扫仓库镜像)。
//
// 返回值语义(关键):error 表示「整轮不可用」;ScanResult.Targets==0 表示「无目标可扫」;
// 这两者都不得被上游当成 completed 0 漏洞 —— 那是最典型的静默假阴性。
type Scanner interface {
	Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) (*ScanResult, error)
}

// ScanResult 一次扫描的执行结果:发现项 + 覆盖率 + 失败诊断。
type ScanResult struct {
	Findings []*model.Finding
	// Targets 实际尝试扫描的目标数(主机 / 镜像)。
	Targets int
	// Failed 其中失败的目标数。
	Failed int
	// Errors 失败原因摘要(每条截断,避免超长刷屏)。
	Errors []string
	// DBUpdatedAt 本轮 Agent 端漏洞库的时间戳(取自 trivy 库文件 mtime),nil = 未知。
	DBUpdatedAt *time.Time
	// ScannedHosts 本轮「成功扫到」的资产清单(主机名或镜像全引用)。
	// 复扫验证据此判定哪些资产上的漏洞本轮未再出现 → 自动闭环为 fixed。
	ScannedHosts []string
}

// appendHost 记录一个成功扫描的资产(去重)。
func (r *ScanResult) appendHost(h string) {
	if h == "" {
		return
	}
	for _, v := range r.ScannedHosts {
		if v == h {
			return
		}
	}
	r.ScannedHosts = append(r.ScannedHosts, h)
}

// addError 记录一条失败原因(截断到 200 字符)。
func (r *ScanResult) addError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	r.Errors = append(r.Errors, msg)
}

// errorSummary 汇总失败原因(最多 3 条)供 Scan.ErrorDetail 展示。
func (r *ScanResult) errorSummary() string {
	if len(r.Errors) == 0 {
		return ""
	}
	n := len(r.Errors)
	if n > 3 {
		n = 3
	}
	s := strings.Join(r.Errors[:n], "; ")
	if len(r.Errors) > n {
		s += fmt.Sprintf("; 等共 %d 条", len(r.Errors))
	}
	return s
}

// InventoryEntry 一条资产清单条目。
// 主机软件清单形态: Host + Package + Version(AgentID 可选,供 AgentScanner 下发)。
// 容器镜像清单形态: ImageRef(镜像全引用) + AgentID(必填,运行 trivy image 的目标 agent)
//
//   - Registry/RegistryUser/RegistryPass(镜像仓库凭据,供 ImageScanner)。
//
// RuleScanner/AgentScanner 忽略镜像字段; ImageScanner 忽略主机软件字段。
type InventoryEntry struct {
	Host         string
	Package      string
	Version      string
	AgentID      string
	ImageRef     string
	Registry     string
	RegistryUser string
	RegistryPass string
}

// --- 发现项构造:统一填充去重键/生命周期初值/修复建议,避免三个扫描器各写一遍 ---

// newFinding 构造一条规范化的发现项。
// host 为资产归属(主机名或镜像全引用);severity 决定 rank。
func newFinding(tenantID, scanID, host, cve, title, desc string, sev model.Severity, cvss float64,
	pkg, installed, fixed, reference string) *model.Finding {
	now := time.Now()
	f := &model.Finding{
		TenantID:    tenantID,
		ScanID:      scanID,
		CVE:         cve,
		Title:       title,
		Description: desc,
		Severity:    sev,
		CVSS:        cvss,
		Affected:    pkg,
		Installed:   installed,
		Fixed:       fixed,
		Reference:   reference,
		Remediation: remediationOf(pkg, fixed),
		Rank:        sev.Rank(),
		Host:        host,
		Status:      model.FindingOpen,
		Occurrences: 1,
		FirstSeen:   now,
		LastSeen:    now,
		CreatedAt:   now,
	}
	f.DedupeKey = dedupeKey(tenantID, cve, pkg, installed, host)
	return f
}

// dedupeKey 计算发现项去重键。
// 口径:同一租户 + 同一 CVE + 同一受影响包 + 同一已装版本 + 同一资产 = 同一漏洞实例。
// 不含修复版本(升级补丁发布不该被当成新漏洞),不含标题(不同数据源表述可能不同)。
func dedupeKey(tenantID, cve, pkg, installed, host string) string {
	h := sha256.New()
	// 用 \x00 分隔避免字段拼接歧义(如 a|b 与 a + |b)。
	for _, part := range []string{tenantID, cve, pkg, installed, host} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// remediationOf 依据是否有官方修复版本生成可执行的修复建议。
// 无修复版本时给出诚实的缓解方向(不编造补丁号)。
func remediationOf(pkg, fixed string) string {
	if pkg == "" {
		return ""
	}
	if fixed != "" {
		return fmt.Sprintf("升级 %s 至 %s 或更高版本后重新扫描验证", pkg, fixed)
	}
	return fmt.Sprintf("%s 暂无官方修复版本，建议评估隔离/缓解措施或替换该组件", pkg)
}

// cveRule 内置 CVE 规则(演示/离线用; 生产由 trivy/grype 替换)。
type cveRule struct {
	CVE         string
	Package     string
	Fixed       string
	Severity    model.Severity
	Title       string
	Description string
	Reference   string
	CVSS        float64
}

// RuleScanner 基于内置 CVE 规则 + 资产清单的离线扫描器。
type RuleScanner struct{}

// NewRuleScanner 构造规则扫描器。
func NewRuleScanner() *RuleScanner { return &RuleScanner{} }

// demoInventory 8 条样例主机软件清单(演示模式注入)。
func demoInventory() []InventoryEntry {
	return []InventoryEntry{
		{Host: "web-01", Package: "openssl", Version: "3.0.5"},
		{Host: "web-02", Package: "xz", Version: "5.6.1"},
		{Host: "app-01", Package: "log4j", Version: "2.14.1"},
		{Host: "lb-01", Package: "nginx", Version: "1.22.0"},
		{Host: "lb-02", Package: "nginx", Version: "1.20.0"},
		{Host: "cache-01", Package: "curl", Version: "7.68.0"},
		{Host: "cache-02", Package: "curl", Version: "7.70.0"},
		{Host: "shell-01", Package: "bash", Version: "5.0"},
	}
}

// rules 6 条 CVE 规则, 覆盖 critical/high/medium。
// 与 demoInventory 配合: 全量扫描=full 得 8 条(crit:3,high:3,medium:2);
// 快速扫描=quick(仅 rank>=3)得 6 条(crit:3,high:3)。
func rules() []cveRule {
	return []cveRule{
		{"CVE-2022-3602", "openssl", "3.0.7", model.SeverityCritical,
			"OpenSSL 堆缓冲区溢出", "OpenSSL 3.0.x 在 3.0.7 之前存在堆溢出风险",
			"https://nvd.nist.gov/vuln/detail/CVE-2022-3602", 9.8},
		{"CVE-2024-3094", "xz", "5.6.2", model.SeverityCritical,
			"xz-utils 后门", "xz-utils 5.6.0–5.6.1 含恶意后门",
			"https://nvd.nist.gov/vuln/detail/CVE-2024-3094", 10.0},
		{"CVE-2021-44228", "log4j", "2.17.0", model.SeverityCritical,
			"Log4j JNDI 远程代码执行", "Log4j 2.x 在 2.17.0 之前存在 Log4Shell RCE",
			"https://nvd.nist.gov/vuln/detail/CVE-2021-44228", 10.0},
		{"CVE-2023-44487", "nginx", "1.25.0", model.SeverityHigh,
			"nginx 请求走私", "nginx 1.24.x 及之前存在请求走私",
			"https://nvd.nist.gov/vuln/detail/CVE-2023-44487", 7.5},
		{"CVE-2022-3715", "bash", "5.1", model.SeverityHigh,
			"bash 特权提升", "bash 5.0 存在本地提权漏洞",
			"https://nvd.nist.gov/vuln/detail/CVE-2022-3715", 7.8},
		{"CVE-2020-8177", "curl", "7.80.0", model.SeverityMedium,
			"curl 堆溢出", "curl 7.70 及之前存在堆溢出",
			"https://nvd.nist.gov/vuln/detail/CVE-2020-8177", 6.5},
	}
}

// Scan 执行一次规则扫描。
// inventory 为空时:demo 模式注入样例清单;非 demo 返回「无目标可扫」(Targets=0),
// 由上游据此给出「未覆盖任何资产」的结论,而不是谎报 0 漏洞。
func (s *RuleScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) (*ScanResult, error) {
	demoModeUsed := false
	if len(inventory) == 0 {
		if !demoMode {
			return &ScanResult{}, nil
		}
		inventory = demoInventory()
		demoModeUsed = true
	}
	_ = demoModeUsed
	rs := rules()

	res := &ScanResult{}
	// host 去重计数作为覆盖率口径(同一主机多条软件只算一台)。
	hosts := make(map[string]struct{})
	for _, e := range inventory {
		if e.Host != "" {
			hosts[e.Host] = struct{}{}
		}
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
			res.Findings = append(res.Findings, newFinding(
				tenantID, scanID, e.Host, r.CVE, r.Title, r.Description,
				r.Severity, r.CVSS, e.Package, e.Version, r.Fixed, r.Reference,
			))
		}
	}
	if len(hosts) > 0 {
		res.Targets = len(hosts)
		for h := range hosts {
			res.appendHost(h)
		}
	} else {
		res.Targets = len(inventory)
	}
	return res, nil
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
