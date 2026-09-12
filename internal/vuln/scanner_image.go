package vuln

import (
	"context"
	"log/slog"
	"strings"
)

// RegistryConfig 镜像仓库凭据(随扫描请求传入,按扫描隔离)。
// 注意: 凭据经 Agent 通道下发的脚本内环境(TRIVY_USERNAME/PASSWORD)传入,
// 生产应改走密钥库,避免凭据出现在脚本内容中。
type RegistryConfig struct {
	URL      string `json:"url"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// ImageScanner 经 Agent 下发 trivy 扫描镜像仓库中的容器镜像(trivy image)。
// 与 AgentScanner(trivy fs 扫主机)并列: 本扫描器针对容器镜像,不依赖主机软件清单,
// 每条 InventoryEntry.ImageRef 为一个待扫镜像全引用(如 registry.io/team/app:tag)。
type ImageScanner struct {
	runner    AgentRunner
	reg       RegistryConfig
	enumerate bool   // true=经 trivy registry 自动枚举仓库内全部 repos/tags
	agentID   string // enumerate 模式下执行 trivy registry 的受管主机
}

// NewImageScanner 构造镜像扫描器;reg 为仓库凭据(可为空,扫公开镜像)。
// enumerate=true 且 agentID 非空时走自动枚举模式(registry_auto),否则按显式 images[] 逐扫。
func NewImageScanner(runner AgentRunner, reg RegistryConfig, enumerate bool, agentID string) *ImageScanner {
	return &ImageScanner{runner: runner, reg: reg, enumerate: enumerate, agentID: agentID}
}

// trivyImageScript 在目标主机安装(若缺失)并运行 `trivy image <ref>`。
// 仓库凭据经环境变量注入(避开参数转义);库新鲜度与退出码处理与主机扫描一致:
// 库过期先刷新,刷新失败但本地有库则降级重试,最终退出码透传给上游。
func trivyImageScript(ref, user, pass string) string {
	var b strings.Builder
	b.WriteString(trivyScanPreamble())
	if user != "" {
		b.WriteString("export TRIVY_USERNAME=" + quoteShellArg(user) + "\n")
		b.WriteString("export TRIVY_PASSWORD=" + quoteShellArg(pass) + "\n")
	}
	b.WriteString(trivyRunBlock("trivy image --format json --severity HIGH,CRITICAL " + quoteShellArg(ref)))
	return b.String()
}

// trivyRegistryScript 在目标主机自动发现并扫描整个仓库的镜像(trivy registry)。
// 区别于 trivyImageScript(单镜像): 本脚本枚举仓库内全部 repos/tags 并逐一扫描,
// 输出 JSON 数组供 parseTrivy 解析。仓库凭据经环境变量注入。
func trivyRegistryScript(url, user, pass string) string {
	var b strings.Builder
	b.WriteString(trivyScanPreamble())
	if user != "" {
		b.WriteString("export TRIVY_USERNAME=" + quoteShellArg(user) + "\n")
		b.WriteString("export TRIVY_PASSWORD=" + quoteShellArg(pass) + "\n")
	}
	b.WriteString(trivyRunBlock("trivy registry --format json --severity HIGH,CRITICAL " + quoteShellArg(url)))
	return b.String()
}

// quoteShellArg 单引号包裹并转义内单引号,避免注入/断裂。
func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Scan 对每个 ImageRef 经 Agent 在目标主机运行 `trivy image`,解析 JSON 汇总 findings。
// enumerate 模式(registry_auto)且无显式清单时,改走 scanRegistry 自动枚举仓库全部镜像。
// 单个镜像/单仓库失败不拖垮整轮,但计入 Failed;无目标资产显式记录原因。
func (s *ImageScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) (*ScanResult, error) {
	if s.enumerate && len(inventory) == 0 {
		return s.scanRegistry(ctx, tenantID, scanID, scanType)
	}
	type target struct {
		ref     string
		agentID string
	}
	seen := make(map[string]bool)
	var targets []target
	for _, e := range inventory {
		if e.ImageRef == "" {
			continue
		}
		if seen[e.ImageRef] {
			continue
		}
		seen[e.ImageRef] = true
		targets = append(targets, target{ref: e.ImageRef, agentID: e.AgentID})
	}
	res := &ScanResult{}
	if len(targets) == 0 {
		res.addError("未提供任何待扫描镜像")
		return res, nil
	}
	res.Targets = len(targets)

	for _, t := range targets {
		script := trivyImageScript(t.ref, s.reg.Username, s.reg.Password)
		out, exitCode, err := s.runner.RunScriptOnAgent(ctx, t.agentID, tenantID, script, nil, 600, "")
		if err != nil {
			slog.Error("vuln: image scan failed", "agent", t.agentID, "ref", t.ref, "error", err)
			res.Failed++
			res.addError("镜像 %s 执行失败: %v", t.ref, err)
			continue
		}
		if exitCode != 0 {
			slog.Error("vuln: image scan non-zero exit", "agent", t.agentID, "ref", t.ref, "exit", exitCode)
			res.Failed++
			res.addError("镜像 %s 扫描退出码 %d(trivy 未成功执行)", t.ref, exitCode)
			continue
		}
		body, dbAt := parseScanOutput(out)
		m, perr := parseTrivy(body)
		if perr != nil {
			slog.Error("vuln: parse trivy image output failed", "agent", t.agentID, "ref", t.ref, "error", perr)
			res.Failed++
			res.addError("镜像 %s 输出解析失败(非预期 JSON)", t.ref)
			continue
		}
		if dbAt != nil && (res.DBUpdatedAt == nil || dbAt.After(*res.DBUpdatedAt)) {
			res.DBUpdatedAt = dbAt
		}
		// 镜像扫描以镜像全引用作为资产归属,使去重键能区分不同镜像的同一 CVE。
		res.appendHost(t.ref)
		res.Findings = append(res.Findings, findingsFromTrivy(tenantID, scanID, scanType, t.ref, m)...)
	}
	return res, nil
}

// scanRegistry 经 Agent 在目标主机跑 `trivy registry <url>`,自动枚举仓库内
// 全部 repos/tags 并扫描,解析 JSON 数组汇总 findings。
// 依赖仓库开放 catalog 列举(私有仓库需相应配置);失败原因显式回传,不再静默返回空。
func (s *ImageScanner) scanRegistry(ctx context.Context, tenantID, scanID, scanType string) (*ScanResult, error) {
	res := &ScanResult{}
	if s.runner == nil || s.agentID == "" {
		res.addError("registry 自动枚举需要指定执行 Agent")
		return res, nil
	}
	if s.reg.URL == "" {
		res.addError("registry 自动枚举需要 registry.url")
		return res, nil
	}
	res.Targets = 1
	script := trivyRegistryScript(s.reg.URL, s.reg.Username, s.reg.Password)
	out, exitCode, err := s.runner.RunScriptOnAgent(ctx, s.agentID, tenantID, script, nil, 900, "")
	if err != nil {
		slog.Error("vuln: registry enumerate scan failed", "agent", s.agentID, "url", s.reg.URL, "error", err)
		res.Failed++
		res.addError("仓库 %s 枚举失败: %v", s.reg.URL, err)
		return res, nil
	}
	if exitCode != 0 {
		slog.Error("vuln: registry enumerate non-zero exit", "agent", s.agentID, "url", s.reg.URL, "exit", exitCode)
		res.Failed++
		res.addError("仓库 %s 扫描退出码 %d(trivy 未成功执行)", s.reg.URL, exitCode)
		return res, nil
	}
	body, dbAt := parseScanOutput(out)
	m, perr := parseTrivy(body)
	if perr != nil {
		slog.Error("vuln: parse trivy registry output failed", "url", s.reg.URL, "error", perr)
		res.Failed++
		res.addError("仓库 %s 输出解析失败(非预期 JSON)", s.reg.URL)
		return res, nil
	}
	res.DBUpdatedAt = dbAt
	for tgt, vs := range m {
		ref := tgt
		if ref == "" {
			ref = s.reg.URL
		}
		res.Findings = append(res.Findings, findingsFromTrivy(tenantID, scanID, scanType, ref, map[string][]trivyVuln{ref: vs})...)
	}
	return res, nil
}
