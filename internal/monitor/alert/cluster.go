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

// RootCausePath 一条因果传播链路径(L2 预测性运维-因果 RCA 升级):
// 根因节点(最早触发的上游) -> 中间传播 -> 受影响告警。
type RootCausePath struct {
	RootCIID      string   `json:"root_ci_id"`
	RootTitle     string   `json:"root_title"`
	RootSeverity  string   `json:"root_severity"`
	Path          []string `json:"path"`            // [root, ..., leaf] CI 传播链
	AffectedCount int      `json:"affected_count"`  // 受影响告警数(含根因)
	EarliestAt    string   `json:"earliest_triggered"`
	Confidence    float64  `json:"confidence"`      // 0-1: 拓扑确认的传播关系占比
}

// AnalyzeRootCausePaths 因果链分析: 把活跃告警按 拓扑祖先 + 触发时序 串成传播路径。
// 规则:
//   1. 告警 A 是 B 的拓扑祖先 且 A 先触发 -> B 受 A 影响(有向边 A→B);
//   2. 无入边的节点 = 根因候选(不被任何其他告警影响);
//   3. 从每个根因沿出边收集受影响节点 -> 一条传播路径(含多跳链);
//   4. Confidence = 路径中拓扑确认边 / 总边数(无 topology 时按同 rule 族时间窗口近似, 置信度 0.5)。
// 相比 AnalyzeRootCause(扁平评分候选), 本方法直接给出"根因→传播"因果链, 更贴近根因定位语义。
func (a *Aggregator) AnalyzeRootCausePaths(ctx context.Context, alerts []*model.AlertEvent) []RootCausePath {
	type node struct {
		al    *model.AlertEvent
		out   []int // 出边(受影响节点下标)
		inCnt int   // 入边数(被影响次数)
	}
	nodes := make([]*node, 0, len(alerts))
	for _, al := range alerts {
		if al == nil || al.SourceCIID == "" {
			continue
		}
		nodes = append(nodes, &node{al: al})
	}
	if len(nodes) == 0 {
		return nil
	}
	// 按触发时间升序(先触发者先建立影响关系)
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].al.TriggeredAt.Before(nodes[j].al.TriggeredAt)
	})

	confirmedEdges := 0
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			if nodes[i].al.SourceCIID == nodes[j].al.SourceCIID {
				continue // 同一 CI 的告警视为同源, 不构成传播
			}
			affected := false
			if a.topology != nil {
				if isA, err := a.topology.IsAncestorOf(ctx, nodes[i].al.SourceCIID, nodes[j].al.SourceCIID); err == nil && isA {
					affected = true
					confirmedEdges++
				}
			} else {
				// 无 topology 时: 同 rule 族(前缀相同)且 30 分钟窗口内先触发 -> 近似传播
				if sameRuleFamily(nodes[i].al.RuleName, nodes[j].al.RuleName) &&
					nodes[j].al.TriggeredAt.Sub(nodes[i].al.TriggeredAt) <= 30*time.Minute {
					affected = true
				}
			}
			if affected {
				nodes[i].out = append(nodes[i].out, j)
				nodes[j].inCnt++
			}
		}
	}

	var paths []RootCausePath
	for i, n := range nodes {
		if n.inCnt > 0 {
			continue // 不是根因
		}
		path := RootCausePath{
			RootCIID:      n.al.SourceCIID,
			RootTitle:     n.al.Title,
			RootSeverity:  string(n.al.Severity),
			EarliestAt:    n.al.TriggeredAt.UTC().Format(time.RFC3339),
			AffectedCount: 1,
		}
		chain := []string{n.al.SourceCIID}
		// BFS 收集受影响节点(去重)
		visited := map[int]bool{i: true}
		queue := []int{i}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, o := range nodes[cur].out {
				if visited[o] {
					continue
				}
				visited[o] = true
				chain = append(chain, nodes[o].al.SourceCIID)
				path.AffectedCount++
				queue = append(queue, o)
			}
		}
		path.Path = chain
		// 置信度: 拓扑确认边占比(无 topology 全为近似边时给 0.5)
		if confirmedEdges == 0 {
			path.Confidence = 0.5
		} else {
			path.Confidence = float64(confirmedEdges) / float64(confirmedEdges+maxI(0, len(nodes)-confirmedEdges))
		}
		paths = append(paths, path)
	}
	// 按受影响范围降序
	sort.SliceStable(paths, func(i, j int) bool {
		return paths[i].AffectedCount > paths[j].AffectedCount
	})
	return paths
}

// sameRuleFamily 判断两个规则名是否同族(前缀相同, 如 "probe.fail" 与 "probe.timeout")。
func sameRuleFamily(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	// 取前缀(到第一个 "." 之后的分隔)比较
	fa := a
	fb := b
	if i := indexByte(a, '.'); i > 0 {
		fa = a[:i]
	}
	if i := indexByte(b, '.'); i > 0 {
		fb = b[:i]
	}
	return fa == fb
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
