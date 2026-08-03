package vuln

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
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
// 仓库凭据经环境变量注入(避开参数转义),--skip-db-update 适配无 DB 的 agent 环境。
func trivyImageScript(ref, user, pass string) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -o pipefail\n")
	b.WriteString("command -v trivy >/dev/null 2>&1 || { curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin; }\n")
	if user != "" {
		b.WriteString("export TRIVY_USERNAME=" + quoteShellArg(user) + "\n")
		b.WriteString("export TRIVY_PASSWORD=" + quoteShellArg(pass) + "\n")
	}
	b.WriteString("trivy image --format json --severity HIGH,CRITICAL --skip-db-update " + quoteShellArg(ref) + " 2>/dev/null || true\n")
	return b.String()
}

// trivyRegistryScript 在目标主机自动发现并扫描整个仓库的镜像(trivy registry)。
// 区别于 trivyImageScript(单镜像): 本脚本枚举仓库内全部 repos/tags 并逐一扫描,
// 输出 JSON 数组供 parseTrivy 解析。仓库凭据经环境变量注入。
func trivyRegistryScript(url, user, pass string) string {
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -o pipefail\n")
	b.WriteString("command -v trivy >/dev/null 2>&1 || { curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin; }\n")
	if user != "" {
		b.WriteString("export TRIVY_USERNAME=" + quoteShellArg(user) + "\n")
		b.WriteString("export TRIVY_PASSWORD=" + quoteShellArg(pass) + "\n")
	}
	b.WriteString("trivy registry --format json --severity HIGH,CRITICAL --skip-db-update " + quoteShellArg(url) + " 2>/dev/null || true\n")
	return b.String()
}

// quoteShellArg 单引号包裹并转义内单引号,避免注入/断裂。
func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Scan 对每个 ImageRef 经 Agent 在目标主机运行 `trivy image`,解析 JSON 汇总 findings。
// enumerate 模式(registry_auto)且无显式清单时,改走 scanRegistry 自动枚举仓库全部镜像。
// 单镜像/单仓库失败仅记日志并跳过,不拖垮整轮;无目标资产返回空。
func (s *ImageScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) ([]*model.Finding, error) {
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
	if len(targets) == 0 {
		return nil, nil
	}

	var findings []*model.Finding
	for _, t := range targets {
		script := trivyImageScript(t.ref, s.reg.Username, s.reg.Password)
		out, _, err := s.runner.RunScriptOnAgent(ctx, t.agentID, tenantID, script, nil, 600, "")
		if err != nil {
			slog.Error("vuln: image scan failed, skipped", "agent", t.agentID, "ref", t.ref, "error", err)
			continue
		}
		m, perr := parseTrivy(out)
		if perr != nil {
			slog.Error("vuln: parse trivy image output failed, skipped", "agent", t.agentID, "ref", t.ref, "error", perr)
			continue
		}
		for tgt, vs := range m {
			for _, v := range vs {
				sev := mapSeverity(v.Severity)
				if scanType == string(model.ScanTypeQuick) && sev.Rank() < 3 {
					continue
				}
				findings = append(findings, &model.Finding{
					ID:          uuid.New().String(),
					ScanID:      scanID,
					TenantID:    tenantID,
					CVE:         v.VulnerabilityID,
					Title:       fmt.Sprintf("%s (image: %s)", v.Title, tgt),
					Description: v.Title,
					Severity:    sev,
					Affected:    v.PkgName,
					Installed:   v.InstalledVersion,
					Fixed:       v.FixedVersion,
					Reference:   v.PrimaryURL,
					Rank:        sev.Rank(),
					CreatedAt:   time.Now(),
				})
			}
		}
	}
	return findings, nil
}

// scanRegistry 经 Agent 在目标主机跑 `trivy registry <url>`,自动枚举仓库内
// 全部 repos/tags 并扫描,解析 JSON 数组汇总 findings。
// 依赖仓库开放 catalog 列举(私有仓库需相应配置);无 agent/无 url/失败 时返回空(不报错拖垮)。
func (s *ImageScanner) scanRegistry(ctx context.Context, tenantID, scanID, scanType string) ([]*model.Finding, error) {
	if s.runner == nil || s.agentID == "" {
		return nil, nil
	}
	if s.reg.URL == "" {
		slog.Error("vuln: registry auto-enumerate requires registry.url")
		return nil, nil
	}
	script := trivyRegistryScript(s.reg.URL, s.reg.Username, s.reg.Password)
	out, _, err := s.runner.RunScriptOnAgent(ctx, s.agentID, tenantID, script, nil, 900, "")
	if err != nil {
		slog.Error("vuln: registry enumerate scan failed, skipped", "agent", s.agentID, "url", s.reg.URL, "error", err)
		return nil, nil
	}
	m, perr := parseTrivy(out)
	if perr != nil {
		slog.Error("vuln: parse trivy registry output failed, skipped", "url", s.reg.URL, "error", perr)
		return nil, nil
	}
	var findings []*model.Finding
	for tgt, vs := range m {
		for _, v := range vs {
			sev := mapSeverity(v.Severity)
			if scanType == string(model.ScanTypeQuick) && sev.Rank() < 3 {
				continue
			}
			ref := tgt
			if ref == "" {
				ref = "(unknown)"
			}
			findings = append(findings, &model.Finding{
				ID:          uuid.New().String(),
				ScanID:      scanID,
				TenantID:    tenantID,
				CVE:         v.VulnerabilityID,
				Title:       fmt.Sprintf("%s (image: %s)", v.Title, ref),
				Description: v.Title,
				Severity:    sev,
				Affected:    v.PkgName,
				Installed:   v.InstalledVersion,
				Fixed:       v.FixedVersion,
				Reference:   v.PrimaryURL,
				Rank:        sev.Rank(),
				CreatedAt:   time.Now(),
			})
		}
	}
	return findings, nil
}
