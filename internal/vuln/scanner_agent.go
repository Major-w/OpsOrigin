package vuln

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/vuln/model"
)

// AgentRunner 经 Agent 通道在被管主机执行脚本(由 agentmgr.Service 结构性满足,
// 无需 vuln 硬依赖 agentmgr,保持零 import cycle)。
// 经 Tunnel 把脚本下发到 Agent 所在主机执行,复用 Agent 端命令白名单校验(§6)。
type AgentRunner interface {
	RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (string, int, error)
}

// metaPrefix 扫描脚本在 stdout 前置的元信息行前缀(供 Go 侧解析漏洞库更新时间)。
// trivy 的 JSON 输出紧随其后,解析前由 parseScanOutput 剥离。
const metaPrefix = "opsorigin-meta:"

// trivyScanPreamble 脚本公共前导:安装 trivy + 库新鲜度策略 + 元信息输出。
// 关键约定(修静默假阴性):
//  1. trivy 的 JSON 只写 stdout,所有日志/告警写 stderr,二者不混;
//  2. 脚本以 trivy 的真实退出码结束 —— 绝不用 `|| true` 把「二进制缺失/库不可用/网络失败」
//     吞成空输出,否则上游会把「没扫成」错报成「0 漏洞」;
//  3. 漏洞库超过 MAX_AGE_MIN 分钟(默认 360=6h,与页面文案一致)先尝试刷新;
//     刷新失败但本地有库时降级用旧库继续扫(stderr 明确告警),本地无库则如实失败。
func trivyScanPreamble() string {
	return `#!/bin/bash
set -o pipefail
TRIVY_CACHE_DIR="${OPSORIGIN_TRIVY_CACHE:-/var/lib/trivy}"
export TRIVY_CACHE_DIR
MAX_AGE_MIN="${OPSORIGIN_TRIVY_DB_MAX_AGE_MIN:-360}"
DB_FILE="$TRIVY_CACHE_DIR/db/trivy.db"

if ! command -v trivy >/dev/null 2>&1; then
  curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin >&2 2>&1 || true
fi
if ! command -v trivy >/dev/null 2>&1; then
  echo "opsorigin: trivy binary unavailable (install failed)" >&2
  exit 127
fi

db_mtime() {
  if [ -f "$DB_FILE" ]; then
    stat -c %Y "$DB_FILE" 2>/dev/null || stat -f %m "$DB_FILE" 2>/dev/null || echo 0
  else
    echo 0
  fi
}
emit_meta() {
  echo "` + metaPrefix + `db_mtime_epoch=$(db_mtime)"
}

db_stale() {
  [ ! -f "$DB_FILE" ] || [ -n "$(find "$DB_FILE" -mmin +"$MAX_AGE_MIN" 2>/dev/null)" ]
}
`
}

// trivyRunBlock 生成「先尝试刷新漏洞库并执行,失败则降级用本地库重试」的脚本块。
// cmdline 为不含 --skip-db-update / --cache-dir 的 trivy 子命令与参数。
// 关键:最后一次执行保留 trivy 真实退出码,使失败可被上游识别(不再吞成 0 漏洞)。
func trivyRunBlock(cmdline string) string {
	return `
STAMP="/tmp/opsorigin-trivy-$$"
if db_stale; then
  # 库缺失/过期:先让 trivy 自行刷新库并扫描(不传 --skip-db-update)
  if ` + cmdline + ` --cache-dir "$TRIVY_CACHE_DIR" >"${STAMP}.out" 2>"${STAMP}.err"; then
    emit_meta
    cat "${STAMP}.out"
    rm -f "${STAMP}.out" "${STAMP}.err"
    exit 0
  fi
  echo "opsorigin: db refresh/scan failed" >&2
  sed -n '1,20p' "${STAMP}.err" >&2 2>/dev/null
  if [ -f "$DB_FILE" ]; then
    echo "opsorigin: falling back to local vulnerability db (results may be stale)" >&2
    emit_meta
    ` + cmdline + ` --skip-db-update --cache-dir "$TRIVY_CACHE_DIR"
    rc=$?
    rm -f "${STAMP}.out" "${STAMP}.err"
    exit $rc
  fi
  rm -f "${STAMP}.out" "${STAMP}.err"
  echo "opsorigin: no local vulnerability db available" >&2
  exit 1
fi
emit_meta
` + cmdline + ` --skip-db-update --cache-dir "$TRIVY_CACHE_DIR"
exit $?
`
}

// trivyAgentScript 在目标主机安装(若缺失)并运行 trivy 扫描文件系统。
// 输出:前置元信息行 + trivy JSON;退出码 = trivy 退出码(非 0 即扫描失败)。
func trivyAgentScript() string {
	return trivyScanPreamble() + trivyRunBlock("trivy fs / --format json --severity HIGH,CRITICAL")
}

// AgentScanner 经 Agent 下发 trivy 的真实漏洞扫描器(trivy fs 扫主机文件系统)。
// 与 RuleScanner(内置 CVE 规则)互补:本扫描器在主机上跑 trivy,直接产出 CVE 清单。
type AgentScanner struct {
	runner AgentRunner
}

// NewAgentScanner 构造 Agent 扫描器。
func NewAgentScanner(runner AgentRunner) *AgentScanner {
	return &AgentScanner{runner: runner}
}

// Scan 经 Agent 对每个绑定了 agent 的资产下发 trivy 扫描。
// 仅扫描 inventory 中 AgentID 非空(已绑定 Agent)的资产;无 agent 资产跳过并在 Errors 中说明。
// 单个 agent 失败不拖垮整轮,但会计入 Failed —— 上游据此把扫描标为 partial/failed,
// 使「部分失败」不再被静默地当成「全部干净」。
func (s *AgentScanner) Scan(ctx context.Context, tenantID, scanID, scanType string, demoMode bool, inventory []InventoryEntry) (*ScanResult, error) {
	// 按 agentID 去重聚合(同一主机多个 package 只下发一次)。
	seen := make(map[string]bool)
	type target struct {
		host    string
		agentID string
	}
	var targets []target
	skippedNoAgent := 0
	for _, e := range inventory {
		if e.AgentID == "" {
			skippedNoAgent++
			continue
		}
		if seen[e.AgentID] {
			continue
		}
		seen[e.AgentID] = true
		targets = append(targets, target{host: e.Host, agentID: e.AgentID})
	}
	res := &ScanResult{}
	if skippedNoAgent > 0 {
		res.addError("有 %d 条资产未绑定 Agent，未纳入扫描", skippedNoAgent)
	}
	if len(targets) == 0 {
		// 无绑定 Agent 的资产,无法经 Agent 通道扫描 —— 显式记录,不静默返回空。
		res.addError("没有任何已绑定 Agent 的资产可供主机扫描")
		return res, nil
	}
	res.Targets = len(targets)

	script := trivyAgentScript()
	for _, t := range targets {
		out, exitCode, err := s.runner.RunScriptOnAgent(ctx, t.agentID, tenantID, script, nil, 600, "")
		if err != nil {
			slog.Error("vuln: agent scan failed", "agent", t.agentID, "host", t.host, "error", err)
			res.Failed++
			res.addError("%s 执行失败: %v", targetLabel(t.host, t.agentID), err)
			continue
		}
		// 退出码非 0 = trivy 未成功产出 → 该目标失败(而非「0 漏洞」)。
		if exitCode != 0 {
			slog.Error("vuln: agent scan non-zero exit", "agent", t.agentID, "host", t.host, "exit", exitCode)
			res.Failed++
			res.addError("%s 扫描退出码 %d(trivy 未成功执行)", targetLabel(t.host, t.agentID), exitCode)
			continue
		}
		body, dbAt := parseScanOutput(out)
		m, perr := parseTrivy(body)
		if perr != nil {
			slog.Error("vuln: parse trivy output failed", "agent", t.agentID, "host", t.host, "error", perr)
			res.Failed++
			res.addError("%s 输出解析失败(非预期 JSON)", targetLabel(t.host, t.agentID))
			continue
		}
		if dbAt != nil && (res.DBUpdatedAt == nil || dbAt.After(*res.DBUpdatedAt)) {
			res.DBUpdatedAt = dbAt
		}
		host := t.host
		if host == "" {
			host = t.agentID
		}
		res.appendHost(host)
		res.Findings = append(res.Findings, findingsFromTrivy(tenantID, scanID, scanType, host, m)...)
	}
	return res, nil
}

// targetLabel 生成失败信息里的目标标识。
func targetLabel(host, agentID string) string {
	if host != "" {
		return host
	}
	return "agent:" + agentID
}

// findingsFromTrivy 把 trivy 结果映射为统一发现项。
// host 为资产归属(主机名或镜像全引用),会写进 Title 便于多资产并排查看。
func findingsFromTrivy(tenantID, scanID, scanType, host string, m map[string][]trivyVuln) []*model.Finding {
	var out []*model.Finding
	for tgt, vs := range m {
		for _, v := range vs {
			sev := mapSeverity(v.Severity)
			if scanType == string(model.ScanTypeQuick) && sev.Rank() < 3 {
				// quick 仅取 rank>=3 的 critical/high。
				continue
			}
			title := v.Title
			if title == "" {
				title = v.VulnerabilityID + " 影响 " + v.PkgName
			}
			if tgt != "" && tgt != host {
				title = fmt.Sprintf("%s (%s)", title, tgt)
			}
			out = append(out, newFinding(
				tenantID, scanID, host, v.VulnerabilityID, title, v.Title,
				sev, v.cvssScore(), v.PkgName, v.InstalledVersion, v.FixedVersion, v.PrimaryURL,
			))
		}
	}
	return out
}

// trivyVuln trivy JSON 中单条漏洞。
type trivyVuln struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	PrimaryURL       string `json:"PrimaryURL"`
	// CVSS 为 数据源(厂商名)→ 评分 的映射(键名不固定: nvd/redhat/ghsa/...)。
	CVSS map[string]struct {
		V2Score float64 `json:"V2Score"`
		V3Score float64 `json:"V3Score"`
	} `json:"CVSS"`
}

// cvssScore 取各数据源中的最高 CVSS 评分(无评分返回 0)。
func (v trivyVuln) cvssScore() float64 {
	var best float64
	for _, s := range v.CVSS {
		if s.V3Score > best {
			best = s.V3Score
		}
		if s.V2Score > best {
			best = s.V2Score
		}
	}
	return best
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

// parseScanOutput 剥离脚本前置的元信息行,返回纯 JSON 体与漏洞库更新时间。
// 元信息行格式: origins-meta:db_mtime_epoch=<unix 秒>。
func parseScanOutput(out string) (string, *time.Time) {
	if !strings.Contains(out, metaPrefix) {
		return out, nil
	}
	var dbAt *time.Time
	var body strings.Builder
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, metaPrefix) {
			kv := strings.TrimPrefix(trimmed, metaPrefix)
			if k, v, ok := strings.Cut(kv, "="); ok && k == "db_mtime_epoch" {
				if epoch, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && epoch > 0 {
					t := time.Unix(epoch, 0)
					dbAt = &t
				}
			}
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	return body.String(), dbAt
}

// parseTrivy 解析 trivy --format json 输出(单对象或数组,如 `trivy registry` 输出数组),
// 返回每个 Target 下的漏洞列表,供调用方拼 Title。
// 空输出返回 nil(确实无漏洞);非 JSON 且非空 → 返回错误,由调用方判定为扫描失败。
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
	case "UNKNOWN", "":
		return model.SeverityUnknown
	default:
		return model.SeverityUnknown
	}
}
