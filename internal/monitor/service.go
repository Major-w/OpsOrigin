package monitor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/monitor/alert"
	"github.com/opsorigin/core/internal/monitor/collect"
	"github.com/opsorigin/core/internal/monitor/discovery"
	"github.com/opsorigin/core/internal/monitor/ingest"
	"github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/internal/monitor/plugin"
	"github.com/opsorigin/core/internal/monitor/synthetic"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	"github.com/redis/go-redis/v9"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg          *config.Config
	db           *sql.DB
	discovery    *discovery.Engine
	prober       *synthetic.Prober
	aggregator   *alert.Aggregator
	topology     *SimpleTopology
	plugins      *plugin.Registry
	wsHub        interface{ BroadcastToTenant(interface{}, string) }
	probeMu      sync.Mutex
	probeResults []*model.ProbeResult

	// vm VictoriaMetrics 写入端(指标落库)。VMAddr 为空时 nil —— 采集仍跑但不落库。
	vm *ingest.VMSink

	// sources 外部指标采集源(Zabbix Agent / Prometheus exporter), 按间隔 pull 落 VM。
	sources   map[string]*sourceEntry
	sourcesMu sync.Mutex

	// alertNotifiers 告警触发时的附加回调(可多个, 由 cmd/core 注入如值班路由/AI 接管)。
	// 与 aggregator.OnNotify(WS 广播) 并存, 避免覆盖既有行为。
	alertNotifiers []func(*model.AlertEvent)
	alertMu        sync.Mutex

	// webhook 告警通知渠道(异步 Webhook 投递, 支持热更新)。nil 安全。
	webhook *alert.WebhookNotifier

	// email 告警通知渠道(SMTP 异步投递, 支持热更新)。nil 安全。
	email *alert.EmailNotifier

	// maintenances 维护期静默窗口(变更窗口抑制误报)。内存缓存 + db 持久化。
	maintenances []*model.MaintenanceWindow
	maintMu      sync.Mutex

	// cmdbEnricher CMDB 上下文富化器(SetCMDBEnricher 注入, nil 安全):
	// 告警进入 aggregator 前按 SourceCIID 附加 owner/criticality 等上下文。
	cmdbEnricher func(ctx context.Context, ciID, tenantID string) map[string]string
}

// sourceEntry 一个运行中的采集源 + 其生命周期取消函数。
type sourceEntry struct {
	cfg         collect.Config
	source      collect.Source
	cancel      context.CancelFunc
	lastSuccess time.Time
	lastError   string
	lastDur     time.Duration
}

func NewService(cfg *config.Config, db *sql.DB, rdb *redis.Client) *Service {
	topo := NewSimpleTopology()
	var vm *ingest.VMSink
	if cfg.Core.VMAddr != "" {
		vm = ingest.NewVMSink(cfg.Core.VMAddr)
	}
	return &Service{
		cfg:          cfg,
		db:           db,
		discovery:    discovery.NewEngine(rdb, db),
		prober:       synthetic.NewProber(db),
		aggregator:   alert.NewAggregator(topo),
		topology:     topo,
		plugins:      plugin.NewDefaultRegistry(),
		probeResults: make([]*model.ProbeResult, 0),
		vm:           vm,
		sources:      make(map[string]*sourceEntry),
		webhook:      alert.NewWebhookNotifier(os.Getenv("OPSORIGIN_CORE_ALERT_WEBHOOK")),
		email:        alert.NewEmailNotifier(alert.EmailConfigFromEnv()),
		maintenances: make([]*model.MaintenanceWindow, 0),
	}
}

// Plugins 暴露插件注册表，供装配期追加自定义插件声明。
func (s *Service) Plugins() *plugin.Registry { return s.plugins }

// AddAlertNotifier 注册告警触发回调(每次新告警触发时调用, 异步安全)。
// 供 cmd/core 装配智能值班路由等扩展能力; 与内置 WS 广播互不干扰。
func (s *Service) AddAlertNotifier(fn func(*model.AlertEvent)) {
	if fn == nil {
		return
	}
	s.alertMu.Lock()
	s.alertNotifiers = append(s.alertNotifiers, fn)
	s.alertMu.Unlock()
}

// SetCMDBEnricher 注入 CMDB 上下文富化器(由 cmd/core 装配, 与 cmdb 包零耦合):
// 按告警关联的 CI 查 CMDB, 返回要附加到告警 Labels 的键值
// (owner/business_criticality/environment/ci_name 等)。
// nil 安全: 未注入时告警保持原样。
func (s *Service) SetCMDBEnricher(fn func(ctx context.Context, ciID, tenantID string) map[string]string) {
	s.cmdbEnricher = fn
}

// ingestAlert 是全部告警的统一入口: 先做 CMDB 上下文富化, 再进 aggregator
// applyCMDBEnrichment 执行 CMDB 上下文富化(幂等, nil 安全)。
// 抽成独立方法便于单测; 富化只补缺失键, 规则显式声明优先。
func (s *Service) applyCMDBEnrichment(ctx context.Context, alert *model.AlertEvent) *model.AlertEvent {
	if s.cmdbEnricher == nil || alert == nil || alert.SourceCIID == "" {
		return alert
	}
	for k, v := range s.cmdbEnricher(ctx, alert.SourceCIID, alert.TenantID) {
		if v == "" {
			continue
		}
		if alert.Labels == nil {
			alert.Labels = map[string]string{}
		}
		// 已有同键(规则自带)不覆盖, 规则显式声明优先。
		if _, exists := alert.Labels[k]; !exists {
			alert.Labels[k] = v
		}
	}
	return alert
}

// ingestAlert 是全部告警的统一入口: 先做 CMDB 上下文富化, 再进 aggregator
// (去重/聚类/抑制/通知)。所有评估路径(promql/threshold/采集)都必须经此进入,
// 保证富化在落库与通知前完成, 告警从诞生起就携带责任人/关键性上下文。
func (s *Service) ingestAlert(ctx context.Context, alert *model.AlertEvent) (*model.AlertEvent, error) {
	s.applyCMDBEnrichment(ctx, alert)
	return s.aggregator.Ingest(ctx, alert)
}

// isInMaintenance 维护期静默判定: 当前时间在窗口内、启用、且命中可选匹配条件。
// 由 aggregator.SetSilenceFunc 注入, 命中后告警被标记 IsSuppressed 抑制通知投递(仍保留活跃/历史记录)。
func (s *Service) isInMaintenance(alert *model.AlertEvent) bool {
	s.maintMu.Lock()
	wins := s.maintenances
	s.maintMu.Unlock()
	now := time.Now()
	for _, w := range wins {
		if !w.Enabled {
			continue
		}
		if now.Before(w.StartAt) || now.After(w.EndAt) {
			continue
		}
		if !maintMatches(w, alert) {
			continue
		}
		return true
	}
	return false
}

// maintMatches 维护期匹配条件: 任一维度为空(零值)表示该维度全部匹配;
// 告警需被所有非空维度包含才进入静默。
func maintMatches(w *model.MaintenanceWindow, alert *model.AlertEvent) bool {
	m := w.Match
	if len(m.Instances) > 0 && !containsStr(m.Instances, alert.Labels["instance"]) {
		return false
	}
	if len(m.CIIDs) > 0 && !containsStr(m.CIIDs, alert.SourceCIID) {
		return false
	}
	if len(m.Sources) > 0 && !containsStr(m.Sources, alert.Labels["source"]) {
		return false
	}
	if len(m.Severities) > 0 && !containsStr(m.Severities, string(alert.Severity)) {
		return false
	}
	return true
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) Topology() *SimpleTopology {
	return s.topology
}

// SnapshotTargets 返回当前所有监控目标（供全景可观测拓扑按 CIID join 节点状态）。
func (s *Service) SnapshotTargets() []*model.MonitorTarget {
	return s.discovery.GetTargets()
}

// ActiveAlerts 返回当前活跃告警（供全景可观测拓扑告警 rollup）。
func (s *Service) ActiveAlerts() []*model.AlertEvent {
	return s.aggregator.GetActiveAlerts()
}

func (s *Service) SetWebSocketHub(hub interface{ BroadcastToTenant(interface{}, string) }) {
	s.wsHub = hub
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/monitor/targets", s.handleListTargets)
	mux.HandleFunc("GET /api/v1/monitor/targets/{id}", s.handleGetTarget)
	mux.HandleFunc("POST /api/v1/monitor/targets", s.handleAddTarget)
	mux.HandleFunc("DELETE /api/v1/monitor/targets/{id}", s.handleDeleteTarget)
	mux.HandleFunc("POST /api/v1/monitor/probe", s.handleRunProbe)
	mux.HandleFunc("GET /api/v1/monitor/alerts", s.handleListAlerts)
	mux.HandleFunc("GET /api/v1/monitor/alert-stats", s.handleAlertStats)
	mux.HandleFunc("GET /api/v1/monitor/alerts/rca", s.handleAlertRCA)
	mux.HandleFunc("GET /api/v1/monitor/alerts/rca/paths", s.handleAlertRCAPaths)
	// 第三方告警统一接入(Zabbix / Prometheus Alertmanager / 通用 webhook)。
	mux.HandleFunc("POST /api/v1/monitor/alerts/ingest", s.handleIngestExternalAlert)
	mux.HandleFunc("POST /api/v1/monitor/predict", s.handlePredict)
	mux.HandleFunc("POST /api/v1/monitor/alerts/{id}/ack", s.handleAckAlert)
	mux.HandleFunc("POST /api/v1/monitor/alerts/{id}/resolve", s.handleResolveAlert)
	mux.HandleFunc("GET /api/v1/monitor/rules", s.handleListRules)
	mux.HandleFunc("POST /api/v1/monitor/rules", s.handleCreateRule)
	mux.HandleFunc("DELETE /api/v1/monitor/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("GET /api/v1/monitor/metrics", s.handleGetMetrics)
	mux.HandleFunc("GET /api/v1/monitor/plugins", s.handleListPlugins)
	mux.HandleFunc("POST /api/v1/monitor/topology/parent", s.handleSetParent)

	// 外部指标采集源(Zabbix Agent / Prometheus exporter)管理。
	mux.HandleFunc("GET /api/v1/monitor/sources", s.handleListSources)
	mux.HandleFunc("POST /api/v1/monitor/sources", s.handleCreateSource)
	mux.HandleFunc("DELETE /api/v1/monitor/sources/{id}", s.handleDeleteSource)
	// 指标目录：列举某数据源当前上报的指标名(供建告警时选择)。仅在配置了 VM 时有效。
	mux.HandleFunc("GET /api/v1/monitor/sources/{id}/metrics", s.handleSourceMetrics)

	// 告警通知渠道(Webhook 投递): 查看/设置/测试。
	mux.HandleFunc("GET /api/v1/monitor/notify/webhook", s.handleGetWebhook)
	mux.HandleFunc("POST /api/v1/monitor/notify/webhook", s.handleSetWebhook)
	mux.HandleFunc("POST /api/v1/monitor/notify/webhook/test", s.handleTestWebhook)
	mux.HandleFunc("GET /api/v1/monitor/notify/email", s.handleGetEmail)
	mux.HandleFunc("POST /api/v1/monitor/notify/email", s.handleSetEmail)
	mux.HandleFunc("POST /api/v1/monitor/notify/email/test", s.handleTestEmail)

	// 维护期静默(变更窗口抑制误报)
	mux.HandleFunc("GET /api/v1/monitor/maintenances", s.handleListMaintenances)
	mux.HandleFunc("POST /api/v1/monitor/maintenances", s.handleCreateMaintenance)
	mux.HandleFunc("DELETE /api/v1/monitor/maintenances/{id}", s.handleDeleteMaintenance)

	// 时序查询代理(供趋势图/大盘使用): 转发到 VictoriaMetrics query/query_range。
	// 仅在配置了 VMAddr 时挂载, 否则前端图表会收到 502(后端不可达)。
	if s.vm != nil {
		mux.Handle("GET /api/v1/monitor/ts/query", ingest.QueryHandler(s.cfg.Core.VMAddr))
	}
}

func (s *Service) Start(ctx context.Context) error {
	s.prober.OnResult(func(target *model.MonitorTarget, result *model.ProbeResult) {
		s.probeMu.Lock()
		s.probeResults = append(s.probeResults, result)
		if len(s.probeResults) > 500 {
			s.probeResults = s.probeResults[len(s.probeResults)-300:]
		}
		metrics.MonitorProbeResults.Set(float64(len(s.probeResults)))
		s.probeMu.Unlock()

		if !result.Success {
			alert := &model.AlertEvent{
				TenantID:    target.TenantID,
				TargetID:    target.ID,
				SourceCIID:  target.CIID,
				RuleName:    "probe_failure",
				Severity:    model.AlertWarning,
				Title:       target.Name + " probe failed",
				Description: string(target.Probes[0]) + " check to " + target.Address + " failed: " + result.ErrorMessage,
			}
			if _, err := s.ingestAlert(ctx, alert); err != nil {
				slog.Error("ingest probe alert failed", "error", err)
			}
		}
	})

	s.discovery.Subscribe(func(ctx context.Context, event discovery.ChangeEvent) {
		if event.EventType == "ci.created" || event.EventType == "ci.updated" {
			targets := s.discovery.GetTargets()
			for _, t := range targets {
				if t.CIID == event.CIID {
					s.prober.AddTarget(t)
					s.topology.RecordTenant(ctx, event.CIID, event.TenantID)
				}
			}
		}
		if event.EventType == "ci.deleted" {
			for _, t := range s.discovery.GetTargets() {
				if t.CIID == event.CIID {
					s.prober.RemoveTarget(t.ID)
				}
			}
		}
	})

	s.aggregator.OnNotify(func(alert *model.AlertEvent) {
		if s.wsHub != nil {
			s.wsHub.BroadcastToTenant(map[string]any{
				"type":    "alert",
				"payload": alert,
				"time":    time.Now().UnixMilli(),
			}, alert.TenantID)
		}
		s.alertMu.Lock()
		fns := append([]func(*model.AlertEvent){}, s.alertNotifiers...)
		s.alertMu.Unlock()
		for _, fn := range fns {
			fn(alert)
		}
	})

	if s.webhook != nil {
		s.AddAlertNotifier(s.webhook.Notify)
	}
	if s.email != nil && s.email.Enabled() {
		s.AddAlertNotifier(s.email.Notify)
	}

	// 维护期静默: 恢复持久化窗口并注入聚合器静默判定(变更窗口抑制误报)。
	ensureMaintenanceTable(ctx, s.db)
	if wins, err := loadMaintenanceFromDB(ctx, s.db); err != nil {
		slog.Warn("monitor: load maintenance windows failed", "err", err)
	} else {
		s.maintMu.Lock()
		s.maintenances = wins
		s.maintMu.Unlock()
	}
	s.aggregator.SetSilenceFunc(s.isInMaintenance)

	go func() {
		if err := s.discovery.Start(ctx); err != nil {
			slog.Error("discovery engine failed", "error", err)
		}
	}()
	go func() {
		if err := s.prober.Run(ctx); err != nil {
			slog.Error("synthetic prober failed", "error", err)
		}
	}()
	go func() {
		if err := s.aggregator.Run(ctx); err != nil {
			slog.Error("alert aggregator failed", "error", err)
		}
	}()

	// 阶段 4: PromQL 告警规则评估器(需配置 VictoriaMetrics 地址)。
	// no_data 检测: 规则写 absent(<metric>) 表达式, 指标断流时命中告警。
	if s.cfg.Core.VMAddr != "" {
		go s.promqlEvalLoop(ctx)
	}

	// 启动已注册的数据源采集(pull 落 VM)。先自愈建表 + 从 DB 恢复, 避免重启丢配置。
	ensureSourcesTable(ctx, s.db)
	if cfgs, err := loadSourcesFromDB(ctx, s.db); err != nil {
		slog.Warn("monitor: load sources from db failed (in-memory only)", "err", err)
	} else {
		s.sourcesMu.Lock()
		for _, cfg := range cfgs {
			if _, ok := s.sources[cfg.ID]; ok {
				continue
			}
			src, err := collect.NewSource(cfg)
			if err != nil {
				slog.Warn("monitor: restore source skipped", "id", cfg.ID, "err", err)
				continue
			}
			s.sources[cfg.ID] = &sourceEntry{cfg: cfg, source: src}
		}
		s.sourcesMu.Unlock()
	}
	s.sourcesMu.Lock()
	for _, e := range s.sources {
		s.runSource(ctx, e)
	}
	s.sourcesMu.Unlock()

	// 启动已持久化的告警规则(避免重启丢配置, 与数据源同源问题)。
	ensureRulesTable(ctx, s.db)
	if rules, err := loadRulesFromDB(ctx, s.db); err != nil {
		slog.Warn("monitor: load alert rules from db failed (in-memory only)", "err", err)
	} else {
		for _, r := range rules {
			s.aggregator.AddRule(r)
		}
	}
	return nil
}

func (s *Service) handleListTargets(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	targets := s.discovery.GetTargets()
	filtered := make([]*model.MonitorTarget, 0)
	for _, t := range targets {
		if t.TenantID == tenantID {
			filtered = append(filtered, t)
		}
	}
	if filtered == nil {
		filtered = []*model.MonitorTarget{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

// pluginWithUsage 在插件声明之上附加「当前租户已接入多少个目标」，
// 让目录页一眼看出哪些插件是真在跑的，而不是一份静态说明书。
type pluginWithUsage struct {
	*plugin.Plugin
	InUse   int      `json:"in_use"`
	Targets []string `json:"targets,omitempty"`
}

func (s *Service) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())

	// 统计各插件的接入目标：绑定关系记录在 target.Labels["plugin"]，复用既有目标存储。
	usage := make(map[string][]string)
	for _, t := range s.discovery.GetTargets() {
		if t.TenantID != tenantID || t.Labels == nil {
			continue
		}
		if key := t.Labels["plugin"]; key != "" {
			usage[key] = append(usage[key], t.Name)
		}
	}

	list := s.plugins.List()
	out := make([]pluginWithUsage, 0, len(list))
	for _, p := range list {
		names := usage[p.Key]
		out = append(out, pluginWithUsage{Plugin: p, InUse: len(names), Targets: names})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	target := s.discovery.GetTarget(id)
	if target == nil || target.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "target not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, target)
}

func (s *Service) handleAddTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string                `json:"name"`
		Address string                `json:"address"`
		Port    int                   `json:"port,omitempty"`
		Probes  []model.ProbeProtocol `json:"probes,omitempty"`
		CIID    string                `json:"ci_id,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Address == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and address required")
		return
	}
	if len(req.Probes) == 0 {
		req.Probes = []model.ProbeProtocol{model.ProbeTCP}
	}
	now := time.Now()
	ciID := req.CIID
	if ciID == "" {
		ciID = "manual-" + req.Name
	}
	target := &model.MonitorTarget{
		ID:        "manual-" + req.Name,
		TenantID:  auth.TenantFromContext(r.Context()),
		CIID:      ciID,
		Name:      req.Name,
		Address:   req.Address,
		Port:      req.Port,
		Probes:    req.Probes,
		Interval:  30 * time.Second,
		Timeout:   5 * time.Second,
		Status:    model.TargetUnknown,
		CreatedAt: now,
	}
	s.prober.AddTarget(target)
	s.topology.RecordTenant(r.Context(), ciID, target.TenantID)
	httputil.WriteJSON(w, http.StatusCreated, target)
}

func (s *Service) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.prober.RemoveTarget(id)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Service) handleRunProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string              `json:"address"`
		Port     int                 `json:"port"`
		Protocol model.ProbeProtocol `json:"protocol"`
		Timeout  string              `json:"timeout,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	timeout := 5 * time.Second
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			timeout = d
		}
	}
	task := &model.ProbeTask{
		Address:  req.Address,
		Port:     req.Port,
		Protocol: req.Protocol,
		Timeout:  timeout,
		TenantID: auth.TenantFromContext(r.Context()),
	}
	result := s.prober.Probe(r.Context(), task)
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	ciID := r.URL.Query().Get("ci_id") // 按配置项过滤(CMDB 详情联动)
	// status=history 返回已确认/已解决的处理历史; 其余返回活跃告警
	if r.URL.Query().Get("status") == "history" {
		history := s.aggregator.GetAlertHistory()
		filtered := make([]*model.AlertEvent, 0)
		for _, a := range history {
			if a.TenantID == tenantID && (ciID == "" || a.SourceCIID == ciID) {
				filtered = append(filtered, a)
			}
		}
		if filtered == nil {
			filtered = []*model.AlertEvent{}
		}
		httputil.WriteJSON(w, http.StatusOK, filtered)
		return
	}
	allAlerts := s.aggregator.GetActiveAlerts()
	filtered := make([]*model.AlertEvent, 0)
	for _, a := range allAlerts {
		if a.TenantID == tenantID && (ciID == "" || a.SourceCIID == ciID) {
			filtered = append(filtered, a)
		}
	}
	if filtered == nil {
		filtered = []*model.AlertEvent{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleAlertStats(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.aggregator.GetStats())
}

// handleAlertRCA 返回活跃告警的启发式根因分析(P0 补短板: 对标 Dynatrace Davis / AISWare RCA)。
// 基于拓扑 + 严重度 + 触发时序评分, 输出根因候选列表。
func (s *Service) handleAlertRCA(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	alerts := s.aggregator.GetActiveAlerts()
	filtered := make([]*model.AlertEvent, 0, len(alerts))
	for _, a := range alerts {
		if a.TenantID == tenantID {
			filtered = append(filtered, a)
		}
	}
	topN := 5
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			topN = n
		}
	}
	entries := s.aggregator.AnalyzeRootCause(r.Context(), filtered, topN)
	if entries == nil {
		entries = []alert.RCAEntry{}
	}
	httputil.WriteJSON(w, http.StatusOK, entries)
}

func (s *Service) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		AcknowledgedBy string `json:"acknowledged_by"`
	}
	httputil.BindJSON(r, &req)
	if req.AcknowledgedBy == "" {
		req.AcknowledgedBy = "admin"
	}
	if err := s.aggregator.Acknowledge(id, req.AcknowledgedBy); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func (s *Service) handleResolveAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.aggregator.Resolve(id); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (s *Service) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules := s.aggregator.GetRules()
	if rules == nil {
		rules = []*model.AlertRule{}
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

func (s *Service) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var rule model.AlertRule
	if err := httputil.BindJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule.TenantID = auth.TenantFromContext(r.Context())
	rule.Enabled = true
	s.aggregator.AddRule(&rule)
	if err := saveRuleToDB(context.Background(), s.db, &rule); err != nil {
		slog.Warn("monitor: persist alert rule failed (in-memory only)", "id", rule.ID, "err", err)
	}
	httputil.WriteJSON(w, http.StatusCreated, rule)
}

func (s *Service) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.aggregator.DeleteRule(id)
	if err := deleteRuleFromDB(context.Background(), s.db, id); err != nil {
		slog.Warn("monitor: delete alert rule from db failed", "id", id, "err", err)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleListMaintenances(w http.ResponseWriter, r *http.Request) {
	s.maintMu.Lock()
	wins := append([]*model.MaintenanceWindow{}, s.maintenances...)
	s.maintMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, wins)
}

func (s *Service) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var m model.MaintenanceWindow
	if err := httputil.BindJSON(r, &m); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if m.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name required")
		return
	}
	if m.EndAt.Before(m.StartAt) {
		httputil.WriteError(w, http.StatusBadRequest, "end_at must be after start_at")
		return
	}
	m.ID = uuid.New().String()
	m.TenantID = auth.TenantFromContext(r.Context())
	m.Enabled = true
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	s.maintMu.Lock()
	s.maintenances = append(s.maintenances, &m)
	s.maintMu.Unlock()
	if err := saveMaintenanceToDB(context.Background(), s.db, &m); err != nil {
		slog.Warn("monitor: persist maintenance window failed (in-memory only)", "id", m.ID, "err", err)
	}
	httputil.WriteJSON(w, http.StatusCreated, m)
}

func (s *Service) handleDeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.maintMu.Lock()
	kept := make([]*model.MaintenanceWindow, 0, len(s.maintenances))
	for _, w := range s.maintenances {
		if w.ID != id {
			kept = append(kept, w)
		}
	}
	s.maintenances = kept
	s.maintMu.Unlock()
	if err := deleteMaintenanceFromDB(context.Background(), s.db, id); err != nil {
		slog.Warn("monitor: delete maintenance window from db failed", "id", id, "err", err)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	type metricPoint struct {
		Name      string  `json:"name"`
		Value     float64 `json:"value"`
		Timestamp string  `json:"timestamp"`
		Unit      string  `json:"unit,omitempty"`
		Success   *bool   `json:"success,omitempty"`
	}
	points := make([]metricPoint, 0)
	s.probeMu.Lock()
	for _, r := range s.probeResults {
		success := r.Success
		points = append(points, metricPoint{
			Name:      "probe",
			Value:     float64(r.Latency.Milliseconds()),
			Timestamp: r.CheckedAt.Format(time.RFC3339),
			Unit:      "ms",
			Success:   &success,
		})
	}
	s.probeMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, points)
}

func (s *Service) handleSetParent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CIID     string `json:"ci_id"`
		ParentID string `json:"parent_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if !s.topology.VerifyTenant(r.Context(), req.CIID, tenantID) {
		httputil.WriteError(w, http.StatusNotFound, "ci not found in tenant")
		return
	}
	s.topology.SetParent(r.Context(), req.CIID, req.ParentID)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ============================================================
// 外部指标采集源管理(Zabbix Agent / Prometheus exporter)
// ============================================================

// SourceView 数据源对外视图。
type SourceView struct {
	ID       string   `json:"id"`
	TenantID string   `json:"tenant_id"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Address  string   `json:"address"`
	Port     int      `json:"port"`
	Path     string   `json:"path,omitempty"`
	Keys     []string `json:"keys,omitempty"`
	Interval int      `json:"interval_sec"`
	CIID     string   `json:"ci_id,omitempty"`

	// 采集健康状态：避免 agent 挂掉 / exporter 不通时静默失效。
	LastSuccess *time.Time `json:"last_success,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastDurMs   int64      `json:"last_duration_ms"`
	Healthy     bool       `json:"healthy"`
}

// AddSource 注册并立即启动一个采集源(按间隔 pull 落 VM)。幂等: 同 ID 先停后启。
func (s *Service) AddSource(ctx context.Context, cfg collect.Config) error {
	if cfg.ID == "" {
		return fmt.Errorf("source id required")
	}
	if cfg.TenantID == "" {
		cfg.TenantID = "default"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	src, err := collect.NewSource(cfg)
	if err != nil {
		return err
	}
	entry := &sourceEntry{cfg: cfg, source: src}
	if s.vm == nil {
		slog.Warn("monitor: VMAddr 未配置, 采集源将运行但不落库(图表无数据)", "id", cfg.ID)
	}
	if err := saveSourceToDB(context.Background(), s.db, cfg); err != nil {
		slog.Warn("monitor: persist source failed (in-memory only)", "id", cfg.ID, "err", err)
	}
	s.sourcesMu.Lock()
	if old, ok := s.sources[cfg.ID]; ok && old.cancel != nil {
		old.cancel()
	}
	s.sources[cfg.ID] = entry
	s.sourcesMu.Unlock()
	// 立即跑一轮 + 启动周期调度。
	s.runSource(ctx, entry)
	return nil
}

// RemoveSource 停止并移除采集源。
func (s *Service) RemoveSource(id string) {
	s.sourcesMu.Lock()
	defer s.sourcesMu.Unlock()
	if e, ok := s.sources[id]; ok {
		if e.cancel != nil {
			e.cancel()
		}
		delete(s.sources, id)
	}
	if err := deleteSourceFromDB(context.Background(), s.db, id); err != nil {
		slog.Warn("monitor: delete source from db failed", "id", id, "err", err)
	}
}

// ListSources 返回当前租户的数据源。
func (s *Service) ListSources(tenantID string) []SourceView {
	s.sourcesMu.Lock()
	defer s.sourcesMu.Unlock()
	out := make([]SourceView, 0, len(s.sources))
	for _, e := range s.sources {
		if e.cfg.TenantID != tenantID {
			continue
		}
		healthy := e.lastError == "" && !e.lastSuccess.IsZero() &&
			time.Since(e.lastSuccess) <= 3*e.cfg.Interval
		var lastSuccess *time.Time
		if !e.lastSuccess.IsZero() {
			ts := e.lastSuccess
			lastSuccess = &ts
		}
		out = append(out, SourceView{
			ID: e.cfg.ID, TenantID: e.cfg.TenantID, Name: e.cfg.Name, Type: e.cfg.Type,
			Address: e.cfg.Address, Port: e.cfg.Port, Path: e.cfg.Path, Keys: e.cfg.Keys,
			Interval: int(e.cfg.Interval.Seconds()), CIID: e.cfg.CIID,
			LastSuccess: lastSuccess, LastError: e.lastError, LastDurMs: e.lastDur.Milliseconds(),
			Healthy: healthy,
		})
	}
	return out
}

// runSource 启动单个采集源的调度(立即一次 + 周期)。cancel 由 entry.cancel 持有。
func (s *Service) runSource(parent context.Context, e *sourceEntry) {
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	go func() {
		s.collectOnce(ctx, e)
		ticker := time.NewTicker(e.cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.collectOnce(ctx, e)
			}
		}
	}()
}

// collectOnce 拉取一次并落 VM。单源失败不影响全局。
func (s *Service) collectOnce(ctx context.Context, e *sourceEntry) {
	start := time.Now()
	samples, err := e.source.Collect(ctx)
	e.lastDur = time.Since(start)
	if err != nil {
		e.lastError = err.Error()
		slog.Warn("monitor: source collect failed", "id", e.cfg.ID, "type", e.cfg.Type, "err", err)
		return
	}
	e.lastError = ""
	e.lastSuccess = time.Now()
	if s.vm == nil {
		return
	}
	points := make([]tunnel.MetricPoint, 0, len(samples))
	for _, sm := range samples {
		points = append(points, tunnel.MetricPoint{Name: sm.Name, Value: sm.Value, Unit: sm.Unit, Labels: sm.Labels})
	}
	if len(points) == 0 {
		return
	}
	batch := ingest.Batch{
		TenantID:  e.cfg.TenantID,
		AgentID:   "collect:" + e.cfg.ID,
		TaskID:    e.cfg.ID,
		TaskType:  "monitor",
		Timestamp: time.Now(),
		Points:    points,
	}
	if err := s.vm.Write(ctx, batch); err != nil {
		slog.Warn("monitor: source write to vm failed", "id", e.cfg.ID, "err", err)
	}
}

func (s *Service) handleListSources(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	httputil.WriteJSON(w, http.StatusOK, s.ListSources(tenantID))
}

func (s *Service) handleCreateSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string   `json:"name"`
		Type     string   `json:"type"`
		Address  string   `json:"address"`
		Port     int      `json:"port"`
		Path     string   `json:"path"`
		Keys     []string `json:"keys"`
		Interval int      `json:"interval_sec"`
		CIID     string   `json:"ci_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Address == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and address required")
		return
	}
	if req.Type != collect.TypeZabbix && req.Type != collect.TypePrometheus {
		httputil.WriteError(w, http.StatusBadRequest, "type must be zabbix or prometheus")
		return
	}
	cfg := collect.Config{
		ID:       "src-" + req.Type + "-" + req.Address,
		TenantID: auth.TenantFromContext(r.Context()),
		Name:     req.Name,
		Type:     req.Type,
		Address:  req.Address,
		Port:     req.Port,
		Path:     req.Path,
		Keys:     req.Keys,
		Interval: time.Duration(req.Interval) * time.Second,
		CIID:     req.CIID,
	}
	if err := s.AddSource(r.Context(), cfg); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, cfg)
}

func (s *Service) handleDeleteSource(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.RemoveSource(id)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleSourceMetrics 列举某数据源当前上报的指标名(指标目录)。
// 通过 VictoriaMetrics label values API 按 source/instance 过滤。
func (s *Service) handleSourceMetrics(w http.ResponseWriter, r *http.Request) {
	if s.vm == nil {
		httputil.WriteError(w, http.StatusBadGateway, "VictoriaMetrics not configured")
		return
	}
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	s.sourcesMu.Lock()
	e, ok := s.sources[id]
	s.sourcesMu.Unlock()
	if !ok || e.cfg.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "source not found")
		return
	}
	match := fmt.Sprintf(`{source="%s",instance="%s"}`, e.cfg.Type, e.cfg.Address)
	api := strings.TrimRight(s.cfg.Core.VMAddr, "/") + "/api/v1/label/__name__/values?match[]=" + url.QueryEscape(match)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, api, nil)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "query vm failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		httputil.WriteError(w, http.StatusBadGateway, "vm returned "+resp.Status)
		return
	}
	var parsed struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		httputil.WriteError(w, http.StatusBadGateway, "decode vm response failed: "+err.Error())
		return
	}
	if parsed.Data == nil {
		parsed.Data = []string{}
	}
	httputil.WriteJSON(w, http.StatusOK, parsed.Data)
}

// handleGetWebhook 返回当前 Webhook 通知渠道配置状态(URL 脱敏)。
func (s *Service) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	u := ""
	if s.webhook != nil {
		u = s.webhook.URL()
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"configured": u != "",
		"format":     alert.DetectWebhookFormat(u),
		"url":        alert.MaskWebhookURL(u),
	})
}

// handleSetWebhook 设置 Webhook 投递地址(空串停用)。支持热更新, 无需重启。
func (s *Service) handleSetWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	if s.webhook == nil {
		httputil.WriteError(w, http.StatusInternalServerError, "webhook notifier unavailable")
		return
	}
	s.webhook.SetURL(body.URL)
	slog.Info("alert webhook updated", "configured", body.URL != "")
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"configured": body.URL != "",
		"format":     alert.DetectWebhookFormat(body.URL),
		"url":        alert.MaskWebhookURL(body.URL),
	})
}

// handleTestWebhook 用当前配置的 Webhook 发一条测试告警, 验证投递链路可达。
func (s *Service) handleTestWebhook(w http.ResponseWriter, r *http.Request) {
	if s.webhook == nil || s.webhook.URL() == "" {
		httputil.WriteError(w, http.StatusBadRequest, "webhook not configured")
		return
	}
	sample := &model.AlertEvent{
		TenantID:    auth.TenantFromContext(r.Context()),
		SourceCIID:  "test-ci",
		RuleName:    "webhook_test",
		Severity:    model.AlertWarning,
		Title:       "Webhook 投递测试",
		Description: "这是一条来自 OpsOrigin 的告警通知测试消息，若收到说明投递链路正常。",
		TriggeredAt: time.Now(),
		Labels:      map[string]string{"source": "opsorigin", "test": "true"},
	}
	s.webhook.Notify(sample)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

// handleGetEmail 返回当前邮件通知渠道配置状态(密码脱敏)。
func (s *Service) handleGetEmail(w http.ResponseWriter, r *http.Request) {
	cfg := alert.EmailConfig{}
	if s.email != nil {
		cfg = s.email.Config()
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"configured": s.email != nil && s.email.Enabled(),
		"host":       cfg.Host,
		"port":       cfg.Port,
		"user":       cfg.User,
		"from":       cfg.From,
		"to":         cfg.To,
		"tls":        cfg.TLS,
		"pass_set":   cfg.Pass != "",
	})
}

// handleSetEmail 设置 SMTP 投递配置(空 to/from 停用)。支持热更新, 无需重启。密码不回显。
func (s *Service) handleSetEmail(w http.ResponseWriter, r *http.Request) {
	var body alert.EmailConfig
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if s.email == nil {
		httputil.WriteError(w, http.StatusInternalServerError, "email notifier unavailable")
		return
	}
	s.email.SetConfig(body)
	cfg := s.email.Config()
	slog.Info("alert email updated", "configured", s.email.Enabled(), "host", cfg.Host)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"configured": s.email.Enabled(),
		"host":       cfg.Host,
		"port":       cfg.Port,
		"user":       cfg.User,
		"from":       cfg.From,
		"to":         cfg.To,
		"tls":        cfg.TLS,
		"pass_set":   cfg.Pass != "",
	})
}

// handleTestEmail 用当前配置的 SMTP 发一条测试告警, 验证投递链路可达。
func (s *Service) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	if s.email == nil || !s.email.Enabled() {
		httputil.WriteError(w, http.StatusBadRequest, "email not configured")
		return
	}
	sample := &model.AlertEvent{
		TenantID:    auth.TenantFromContext(r.Context()),
		SourceCIID:  "test-ci",
		RuleName:    "email_test",
		Severity:    model.AlertWarning,
		Title:       "邮件投递测试",
		Description: "这是一条来自 OpsOrigin 的告警邮件测试，若收到说明投递链路正常。",
		TriggeredAt: time.Now(),
		Labels:      map[string]string{"source": "opsorigin", "test": "true"},
	}
	s.email.Notify(sample)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}
