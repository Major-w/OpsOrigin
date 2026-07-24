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
	runner AgentRunner
	reg    RegistryConfig
}

// NewImageScanner 构造镜像扫描器;reg 为仓库凭据(可为空,扫公开镜像)。
func NewImageScanner(runner AgentRunner, reg RegistryConfig) *ImageScanner {
	return &ImageScanner{runner: runner, reg: reg}
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

// quoteShellArg 单引号包裹并转义内单引号,避免注入/断裂。
func quoteShellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Scan 对每个 ImageRef 经 Agent 在目标主机运行 `trivy image`,解析 JSON 汇总 findings。
// 单镜像失败仅记日志并跳过,不拖垮整轮;无 ImageRef 资产跳过。
func (s *ImageScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) ([]*model.Finding, error) {
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
		vs, perr := parseTrivy(out)
		if perr != nil {
			slog.Error("vuln: parse trivy image output failed, skipped", "agent", t.agentID, "ref", t.ref, "error", perr)
			continue
		}
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
				Title:       fmt.Sprintf("%s (image: %s)", v.Title, t.ref),
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
