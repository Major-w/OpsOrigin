package alert

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/monitor/model"
)

// clusterWindow 告警聚类时间窗口: 窗口内到达的相似告警归为一簇。
const clusterWindow = 10 * time.Minute

// clusterRuleFamily 取 rule 名称的"族"前缀(截断到首个分隔符前), 用于相似度匹配。
// 例如 "probe_failure" -> "probe", "cpu_high" -> "cpu"。
func clusterRuleFamily(ruleName string) string {
	for _, sep := range []string{".", "_", "-", ":"} {
		if i := strings.Index(ruleName, sep); i > 0 {
			return ruleName[:i]
		}
	}
	return ruleName
}

// clusterLabelsSubset 判断 candidate 的 labels 是否与 cluster 的代表告警共享关键键值:
// 任取代表告警 labels 的 2 个键(存在的话)在 candidate 中命中即视为相似。
// 标签(component/app/tier)是故障域的强信号, 共享即大概率同源。
func clusterLabelsSubset(rep, cand map[string]string) bool {
	if len(rep) == 0 || len(cand) == 0 {
		return false
	}
	hits := 0
	for k, v := range rep {
		if cv, ok := cand[k]; ok && cv == v {
			hits++
			if hits >= 2 {
				return true
			}
		}
	}
	return hits >= 2
}

// sameTopologyDomain 判断两个 CI 是否在同一拓扑域(祖先/后代或相同), 需 topology 支持。
func (a *Aggregator) sameTopologyDomain(ctx context.Context, ciA, ciB string) bool {
	if ciA == "" || ciB == "" {
		return false
	}
	if ciA == ciB {
		return true
	}
	if a.topology == nil {
		return false
	}
	isA, err1 := a.topology.IsAncestorOf(ctx, ciA, ciB)
	if err1 == nil && isA {
		return true
	}
	isB, err2 := a.topology.IsAncestorOf(ctx, ciB, ciA)
	if err2 == nil && isB {
		return true
	}
	return false
}

// clusterAlert 尝试把新告警归入既有簇(相似度聚类降噪):
//   - 返回 (clusterID, matched): 匹配到已有簇时返回其 ID
//   - 未匹配则创建新簇并返回新 ID
//   - matched=true 时调用方应抑制通知(仅代表告警通知, 降低告警风暴)
//
// 相似判据(满足其一):
//   1. 同 rule 族 + 同 CI
//   2. 同 rule 族 + labels 共享 ≥2 键 + 同拓扑域
//   3. 不同 rule 但同 CI + labels 共享 ≥2 键(同目标多指标异常)
func (a *Aggregator) clusterAlert(ctx context.Context, alert *model.AlertEvent) (string, bool) {
	now := time.Now()
	cutoff := now.Add(-clusterWindow)
	family := clusterRuleFamily(alert.RuleName)

	a.mu.Lock()
	defer a.mu.Unlock()

	// 找最早触发的同族代表告警(簇首)
	var best *model.AlertEvent
	for _, existing := range a.activeAlerts {
		if existing.ClusterID == "" || existing.TriggeredAt.Before(cutoff) {
			continue
		}
		// 规则 1: 同族 + 同 CI
		if clusterRuleFamily(existing.RuleName) == family && existing.SourceCIID == alert.SourceCIID {
			best = existing
			break
		}
		// 规则 2: 同族 + labels 共享 + 同拓扑域
		if clusterRuleFamily(existing.RuleName) == family &&
			clusterLabelsSubset(existing.Labels, alert.Labels) &&
			a.sameTopologyDomain(ctx, existing.SourceCIID, alert.SourceCIID) {
			best = existing
			break
		}
		// 规则 3: 同 CI + labels 共享(跨 rule 的多指标异常)
		if existing.SourceCIID == alert.SourceCIID &&
			clusterLabelsSubset(existing.Labels, alert.Labels) {
			best = existing
			break
		}
	}

	if best != nil {
		return best.ClusterID, true
	}
	// 新簇: 以告警自身为簇首
	alert.ClusterID = "cl-" + uuid.New().String()[:12]
	return alert.ClusterID, false
}

// RCAEntry 根因分析候选(按评分降序)。
type RCAEntry struct {
	CIID      string `json:"ci_id"`
	RuleName  string `json:"rule_name"`
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	Score     float64 `json:"score"`
	Triggered string `json:"triggered_at"`
}

// AnalyzeRootCause 对一组活跃告警做启发式根因分析:
// 评分 = severity 权重(2) + 拓扑深度(1.5, 越深越可能是根因源头) - 触发时距(0.5, 越早越高)。
// 返回按评分降序的候选列表(取前 N)。
func (a *Aggregator) AnalyzeRootCause(ctx context.Context, alerts []*model.AlertEvent, topN int) []RCAEntry {
	if topN <= 0 {
		topN = 5
	}
	severityWeight := map[model.AlertSeverity]float64{
		model.AlertCritical: 2.0,
		model.AlertWarning:  1.2,
		model.AlertInfo:     0.6,
	}
	now := time.Now()

	type scored struct {
		entry RCAEntry
	}
	entries := make([]scored, 0, len(alerts))
	for _, al := range alerts {
		if al == nil || al.SourceCIID == "" {
			continue
		}
		sw := severityWeight[al.Severity]
		if sw == 0 {
			sw = 0.6
		}
		// 拓扑深度: 有 topology 时算祖先链长度(近似用 IsAncestorOf 计数, 成本高; 这里用
		// 告警自身是否为其他告警的祖先来近似"更靠源头"——若它是别的告警祖先, 深度分加成)。
		depthScore := 0.0
		for _, other := range alerts {
			if other == nil || other.SourceCIID == al.SourceCIID || other.SourceCIID == "" {
				continue
			}
			if a.topology != nil {
				if isA, err := a.topology.IsAncestorOf(ctx, al.SourceCIID, other.SourceCIID); err == nil && isA {
					depthScore += 0.5
				}
			}
		}
		// 触发越早分越高(时间衰减)
		age := now.Sub(al.TriggeredAt)
		timeScore := 0.0
		if age >= 0 {
			timeScore = 1.0 - minF(age.Minutes()/30.0, 1.0) // 30 分钟内线性衰减
		}
		score := sw + depthScore*1.5 + timeScore*0.5
		entries = append(entries, scored{entry: RCAEntry{
			CIID:      al.SourceCIID,
			RuleName:  al.RuleName,
			Title:     al.Title,
			Severity:  string(al.Severity),
			Score:     score,
			Triggered: al.TriggeredAt.UTC().Format(time.RFC3339),
		}})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].entry.Score > entries[j].entry.Score
	})
	if len(entries) > topN {
		entries = entries[:topN]
	}
	out := make([]RCAEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.entry)
	}
	return out
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
