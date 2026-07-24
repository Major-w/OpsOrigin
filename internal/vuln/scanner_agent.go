package vuln

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// AgentRunner 经 Agent 通道在被管主机执行脚本(由 agentmgr.Service 结构性满足,
// 无需 vuln 硬依赖 agentmgr,保持零 import cycle)。
// 经 Tunnel 把脚本下发到 Agent 所在主机执行,复用 Agent 端命令白名单校验(§6)。
type AgentRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (string, int, error)
}

// AgentScanner 经 Agent 下发 trivy/grype 的真实漏洞扫描器。
// 与 RuleScanner(基于内置 CVE 规则)互补:本扫描器在主机上跑 trivy,
// 直接产出 CVE 清单,不依赖内置规则库。
type AgentScanner struct {
	runner AgentRunner
}

// NewAgentScanner 构造 Agent 扫描器。
func NewAgentScanner(runner AgentRunner) *AgentScanner {
	return &AgentScanner{runner: runner}
}

// trivyScript 在目标主机安装(若缺失)并运行 trivy 扫描文件系统。
// 输出 JSON 供 parseTrivy 解析。
const trivyScript = `#!/bin/bash
set -o pipefail
command -v trivy >/dev/null 2>&1 || {
  curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin
}
trivy fs / --format json --severity HIGH,CRITICAL --skip-db-update 2>/dev/null || true
`

// Scan 经 Agent 对每个绑定了 agent 的资产下发 trivy 扫描。
// 仅扫描 inventory 中 AgentID 非空(已绑定 Agent)的资产;无 agent 资产跳过。
// 单个 agent 失败仅记日志并跳过,不拖垮整轮扫描。
func (s *AgentScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) ([]*model.Finding, error) {
	// 按 agentID 去重聚合(同一主机多个 package 只下发一次)。
	seen := make(map[string]bool)
	type target struct {
		host    string
		agentID string
	}
	var targets []target
	for _, e := range inventory {
		if e.AgentID == "" {
			continue
		}
		if seen[e.AgentID] {
			continue
		}
		seen[e.AgentID] = true
		targets = append(targets, target{host: e.Host, agentID: e.AgentID})
	}
	if len(targets) == 0 {
		// 无绑定 Agent 的资产,无法经 Agent 通道扫描。
		return nil, nil
	}

	var findings []*model.Finding
	for _, t := range targets {
		out, _, err := s.runner.RunScriptOnAgent(ctx, t.agentID, tenantID, trivyScript, nil, 600, "")
		if err != nil {
			slog.Error("vuln: agent scan failed, skipped", "agent", t.agentID, "host", t.host, "error", err)
			continue
		}
		m, perr := parseTrivy(out)
		if perr != nil {
			slog.Error("vuln: parse trivy output failed, skipped", "agent", t.agentID, "host", t.host, "error", perr)
			continue
		}
		for _, vs := range m {
			for _, v := range vs {
			sev := mapSeverity(v.Severity)
			if scanType == string(model.ScanTypeQuick) && sev.Rank() < 3 {
				// quick 仅取 rank>=3 的 critical/high。
				continue
			}
			findings = append(findings, &model.Finding{
				ID:          uuid.New().String(),
				ScanID:      scanID,
				TenantID:    tenantID,
				CVE:         v.VulnerabilityID,
				Title:       v.Title,
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

// trivyVuln trivy JSON 中单条漏洞。
type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity        string `json:"Severity"`
	Title            string `json:"Title"`
	PrimaryURL       string `json:"PrimaryURL"`
}

// trivyResult trivy JSON 顶层结构。
// Target/ArtifactName 标记该结果所属镜像(或主机路径),供拼 Findings.Title。
type trivyResult struct {
	Target       string `json:"Target"`
	ArtifactName string `json:"ArtifactName"`
	Results      []struct {
		Vulnerabilities []trivyVuln `json:"Vulnerabilities"`
	} `json:"Results"`
}

// parseTrivy 解析 trivy --format json 输出(单对象或数组,如 `trivy registry` 输出数组),
// 返回 每个 Target 下的漏洞列表,供调用方拼 Title。空输出返回 nil(无漏洞)。
func parseTrivy(out string) (map[string][]trivyVuln, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	var results []trivyResult
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &results); err != nil {
			return nil, err
		}
	} else {
		var single trivyResult
		if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
			return nil, err
		}
		results = append(results, single)
	}
	out2 := make(map[string][]trivyVuln)
	for _, r := range results {
		tgt := r.Target
		if tgt == "" {
			tgt = r.ArtifactName
		}
		var vs []trivyVuln
		for _, res := range r.Results {
			vs = append(vs, res.Vulnerabilities...)
		}
		if len(vs) > 0 {
			out2[tgt] = append(out2[tgt], vs...)
		}
	}
	return out2, nil
}

// mapSeverity 将 trivy/grype 严重级别映射为 model.Severity。
func mapSeverity(s string) model.Severity {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return model.SeverityCritical
	case "HIGH":
		return model.SeverityHigh
	case "MEDIUM":
		return model.SeverityMedium
	case "LOW":
		return model.SeverityLow
	default:
		return model.SeverityMedium
	}
}
