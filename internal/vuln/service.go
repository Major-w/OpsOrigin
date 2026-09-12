package vuln

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/vuln/model"
	"github.com/opsorigin/core/internal/vuln/store"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	"github.com/opsorigin/core/pkg/httputil"
)

// InventoryProvider 提供真实资产清单(从 CMDB 拉取并按约定 schema 映射)。
// 由 cmd/core/main.go 的适配器注入(包装 cmdb.Service.ListCI),
// 解耦 vuln↔cmdb,避免反向依赖。
type InventoryProvider interface {
	ListHostInventory(ctx context.Context, tenantID string) ([]InventoryEntry, error)
}

// Service 漏洞扫描服务；Scanner 预留 Trivy/Grype/Nuclei/OpenSCAP 热插拔位。
type Service struct {
	cfg         *config.Config
	store       store.Store
	scanner     Scanner
	invProvider InventoryProvider
	agentRunner AgentRunner
}

// createScanRequest 创建/执行扫描的入参。同时作为持久化的执行参数(scan_options),
// 供计划任务按原参数重跑。
type createScanRequest struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Schedule     string          `json:"schedule"`
	DemoMode     bool            `json:"demo_mode"`
	Images       []string        `json:"images"`
	AgentID      string          `json:"agent_id"`
	Registry     *RegistryConfig `json:"registry"`
	RegistryAuto bool            `json:"registry_auto"`
}

// NewService 构造服务；优先 PG，Init 失败回退内存(租户隔离)。
func NewService(cfg *config.Config, db *sql.DB, inv InventoryProvider) *Service {
	var st store.Store
	if db != nil {
		pg := store.NewPGStore(db)
		if err := pg.Init(context.Background()); err != nil {
			slog.Error("vuln: failed to init pg store, falling back to memory", "error", err)
			st = store.NewMemoryStore()
		} else {
			st = pg
			slog.Info("vuln: using postgresql store")
		}
	}
	if st == nil {
		st = store.NewMemoryStore()
		slog.Info("vuln: using in-memory store")
	}
	return &Service{
		cfg:         cfg,
		store:       st,
		scanner:     NewRuleScanner(),
		invProvider: inv,
	}
}

// SetAgentRunner 注入 Agent 下发通道(供 AgentScanner 经 Tunnel 下发 trivy/grype),
// 保持 vuln 对 agentmgr 零硬依赖(仅依赖 AgentRunner 接口)。
func (s *Service) SetAgentRunner(ar AgentRunner) {
	s.agentRunner = ar
}

// RegisterRoutes 注册漏洞扫描 REST 路由。
func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/vuln/scans", s.handleCreateScan)
	mux.HandleFunc("GET /api/v1/vuln/scans", s.handleListScans)
	mux.HandleFunc("GET /api/v1/vuln/scans/export/csv", s.handleExportCSV)
	mux.HandleFunc("GET /api/v1/vuln/scans/{id}", s.handleGetScan)
	mux.HandleFunc("DELETE /api/v1/vuln/scans/{id}", s.handleDeleteScan)
	mux.HandleFunc("GET /api/v1/vuln/scans/{id}/findings", s.handleListFindings)
	// 跨扫描的漏洞总表(闭环工作台主视图:按状态/级别/资产过滤)。
	mux.HandleFunc("GET /api/v1/vuln/findings", s.handleListAllFindings)
	mux.HandleFunc("PATCH /api/v1/vuln/findings/{id}", s.handleUpdateFinding)
	mux.HandleFunc("GET /api/v1/vuln/stats", s.handleStats)
}

func (s *Service) handleCreateScan(w http.ResponseWriter, r *http.Request) {
	var req createScanRequest
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	stype := normalizeScanType(req.Type)
	sched := normalizeSchedule(req.Schedule)
	if err := s.validateScanRequest(stype, sched, req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	tenantID := auth.TenantFromContext(r.Context())
	now := time.Now()
	scan := &model.Scan{
		TenantID:       tenantID,
		Name:           req.Name,
		Type:           stype,
		Schedule:       sched,
		DemoMode:       req.DemoMode,
		Status:         model.ScanStatusRunning,
		SeverityCounts: map[string]int{},
		AgentID:        req.AgentID,
		CreatedAt:      now,
		LastRunAt:      &now,
		Options:        encodeOptions(req),
	}
	scan.NextRunAt = nextRunAt(sched, now)
	if err := s.store.CreateScan(r.Context(), scan); err != nil {
		slog.Error("vuln: create scan failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create scan")
		return
	}

	if err := s.execute(r.Context(), scan, req); err != nil {
		// execute 内部已把失败原因写进 scan 并落库;此处只回传结果,不再二次覆盖。
		slog.Error("vuln: execute scan failed", "scan", scan.ID, "error", err)
	}
	httputil.WriteJSON(w, http.StatusOK, scan)
}

// validateScanRequest 入参校验(类型/频率/镜像扫描所需参数)。
func (s *Service) validateScanRequest(stype model.ScanType, sched model.Schedule, req createScanRequest) error {
	switch stype {
	case model.ScanTypeAgent:
		if s.agentRunner == nil {
			return errors.New("agent scan requires agent runner (agentmgr not available)")
		}
	case model.ScanTypeImage:
		if s.agentRunner == nil {
			return errors.New("image scan requires agent runner (agentmgr not available)")
		}
		if req.AgentID == "" {
			return errors.New("image scan requires agent_id (host with registry egress)")
		}
		if req.RegistryAuto {
			if req.Registry == nil || req.Registry.URL == "" {
				return errors.New("registry_auto requires registry.url")
			}
		} else if len(req.Images) == 0 {
			return errors.New("image scan requires images[] (full refs, e.g. registry.io/team/app:tag) or registry_auto:true")
		}
		// 带仓库凭据的镜像扫描不落库(scan_options 不存密钥),因此不允许定时执行。
		if sched != model.ScheduleOnce && req.Registry != nil && req.Registry.Password != "" {
			return errors.New("image scan with registry credentials cannot be scheduled (credentials are not persisted); use schedule=once or a public registry")
		}
	}
	return nil
}

// execute 执行一次扫描并把结果写回 scan(创建路径与调度路径共用)。
// 状态语义(修静默假阴性):
//   - Scanner 返回 error,或「无目标可扫」,或「全部目标失败」→ failed;
//   - 部分目标失败 → partial(有结果但覆盖率不完整,结论需打折);
//   - 其余 → completed。
//
// 返回的 error 仅用于日志,不改变已落库的状态。
func (s *Service) execute(ctx context.Context, scan *model.Scan, req createScanRequest) error {
	startedAt := time.Now()
	scan.Status = model.ScanStatusRunning
	scan.CompletedAt = nil
	scan.ErrorDetail = ""

	stype := scan.Type
	var inv []InventoryEntry
	// 非 demo(或 agent 类型)且注入了 InventoryProvider 时,从 CMDB 拉真实资产清单;
	// agent 类型强制拉取(无视 demo 开关),供 AgentScanner 经 Agent 下发 trivy。
	if s.invProvider != nil && (!req.DemoMode || stype == model.ScanTypeAgent) && stype != model.ScanTypeImage {
		var err error
		inv, err = s.invProvider.ListHostInventory(ctx, scan.TenantID)
		if err != nil {
			slog.Error("vuln: list inventory failed", "error", err)
			return s.failScan(ctx, scan, "拉取资产清单失败: "+err.Error())
		}
	}

	sc, err := s.pickScanner(stype, req, &inv)
	if err != nil {
		return s.failScan(ctx, scan, err.Error())
	}

	res, err := sc.Scan(ctx, scan.TenantID, scan.ID, string(stype), req.DemoMode, inv)
	if err != nil {
		return s.failScan(ctx, scan, "扫描器执行失败: "+err.Error())
	}
	if res == nil {
		res = &ScanResult{}
	}

	scan.Targets = res.Targets
	scan.FailedTargets = res.Failed
	scan.DBUpdatedAt = res.DBUpdatedAt

	// 覆盖率判定:先判「没扫成」,再判「扫成多少」。
	switch {
	case res.Targets == 0:
		msg := "未覆盖任何资产：没有可扫描的目标"
		if len(res.Errors) > 0 {
			msg += "(" + res.errorSummary() + ")"
		}
		return s.failScan(ctx, scan, msg)
	case res.Failed >= res.Targets:
		return s.failScan(ctx, scan, "全部 "+strconv.Itoa(res.Targets)+" 个目标均失败: "+res.errorSummary())
	}

	if err := s.store.UpsertFindings(ctx, res.Findings); err != nil {
		slog.Error("vuln: store findings failed", "error", err)
		return s.failScan(ctx, scan, "写入漏洞结果失败: "+err.Error())
	}

	counts := map[string]int{}
	for _, f := range res.Findings {
		counts[string(f.Severity)]++
	}
	scan.SeverityCounts = counts
	scan.TotalFindings = len(res.Findings)

	// 复扫验证的自动闭环:仅在「非 demo + 有真实覆盖 + 本轮无失败目标」时执行,
	// 把本轮未再出现的 open 项标记为 fixed(修复状态由数据驱动,而非人工猜测)。
	if !req.DemoMode && len(res.ScannedHosts) > 0 && res.Failed == 0 {
		if n, cerr := s.store.CloseMissedFindings(ctx, scan.TenantID, res.ScannedHosts, startedAt); cerr != nil {
			slog.Error("vuln: close missed findings failed", "error", cerr)
		} else if n > 0 {
			slog.Info("vuln: auto-closed findings not seen this run", "count", n, "scan", scan.ID)
		}
	}

	completed := time.Now()
	scan.CompletedAt = &completed
	switch {
	case res.Failed > 0:
		scan.Status = model.ScanStatusPartial
		scan.ErrorDetail = "部分目标失败(" + strconv.Itoa(res.Failed) + "/" + strconv.Itoa(res.Targets) + "): " + res.errorSummary()
	default:
		scan.Status = model.ScanStatusCompleted
	}
	if err := s.store.UpdateScan(ctx, scan); err != nil {
		slog.Error("vuln: finalize scan failed", "error", err)
		return err
	}
	return nil
}

// failScan 把扫描置为失败并落库(不返回错误给调用方,由调用方决定是否响应)。
func (s *Service) failScan(ctx context.Context, scan *model.Scan, detail string) error {
	scan.Status = model.ScanStatusFailed
	scan.ErrorDetail = detail
	scan.SeverityCounts = map[string]int{}
	scan.TotalFindings = 0
	completed := time.Now()
	scan.CompletedAt = &completed
	if err := s.store.UpdateScan(ctx, scan); err != nil {
		slog.Error("vuln: persist failed status failed", "error", err)
		return err
	}
	return errors.New(detail)
}

// pickScanner 按扫描类型选扫描器,并可能改写 inventory(image 类型按镜像清单重建)。
func (s *Service) pickScanner(stype model.ScanType, req createScanRequest, inv *[]InventoryEntry) (Scanner, error) {
	switch stype {
	case model.ScanTypeAgent:
		return NewAgentScanner(s.agentRunner), nil
	case model.ScanTypeImage:
		reg := RegistryConfig{}
		if req.Registry != nil {
			reg = *req.Registry
		}
		if req.RegistryAuto {
			return NewImageScanner(s.agentRunner, reg, true, req.AgentID), nil
		}
		var list []InventoryEntry
		for _, ref := range req.Images {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			list = append(list, InventoryEntry{
				ImageRef:     ref,
				AgentID:      req.AgentID,
				Registry:     reg.URL,
				RegistryUser: reg.Username,
				RegistryPass: reg.Password,
			})
		}
		*inv = list
		return NewImageScanner(s.agentRunner, reg, false, req.AgentID), nil
	default:
		return s.scanner, nil
	}
}

// normalizeScanType 归一化扫描类型(未知值回落 full)。
func normalizeScanType(t string) model.ScanType {
	st := model.ScanType(strings.TrimSpace(strings.ToLower(t)))
	switch st {
	case model.ScanTypeFull, model.ScanTypeQuick, model.ScanTypeCVE, model.ScanTypeAgent, model.ScanTypeImage:
		return st
	}
	return model.ScanTypeFull
}

// normalizeSchedule 归一化执行频率(未知值回落 once)。
func normalizeSchedule(s string) model.Schedule {
	sc := model.Schedule(strings.TrimSpace(strings.ToLower(s)))
	if sc.Valid() {
		return sc
	}
	return model.ScheduleOnce
}

// nextRunAt 计算下一次执行时间(nil = 不重复)。
func nextRunAt(sched model.Schedule, from time.Time) *time.Time {
	d := sched.Interval()
	if d == 0 {
		return nil
	}
	t := from.Add(d)
	return &t
}

// encodeOptions 持久化执行参数,供计划任务重跑。
// 刻意剥离仓库密码:scan_options 是明文列,凭据不落库(定时+私有凭据已被 validateScanRequest 拒绝)。
func encodeOptions(req createScanRequest) string {
	req.Registry = sanitizedRegistry(req.Registry)
	b, err := json.Marshal(req)
	if err != nil {
		return ""
	}
	return string(b)
}

// sanitizedRegistry 返回去掉密码的仓库配置副本。
func sanitizedRegistry(reg *RegistryConfig) *RegistryConfig {
	if reg == nil {
		return nil
	}
	c := *reg
	c.Password = ""
	return &c
}

// decodeOptions 还原持久化的执行参数(计划任务用)。
// scan_options 中不含凭据,故 image 计划任务仅支持公开仓库。
func decodeOptions(raw string) createScanRequest {
	var req createScanRequest
	if raw == "" {
		return req
	}
	_ = json.Unmarshal([]byte(raw), &req)
	return req
}

func (s *Service) handleListScans(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	f := store.ScanFilter{
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Type:   strings.TrimSpace(r.URL.Query().Get("type")),
		Limit:  queryInt(r, "limit", 0),
		Offset: queryInt(r, "offset", 0),
	}
	scans, total, err := s.store.ListScans(r.Context(), tenantID, f)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scans == nil {
		scans = []*model.Scan{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": scans, "total": total})
}

func (s *Service) handleGetScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	scan, err := s.store.GetScan(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "scan not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, scan)
}

func (s *Service) handleDeleteScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	if err := s.store.DeleteScan(r.Context(), id, tenantID); err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "scan not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// parseFindingFilter 解析发现项列表的过滤与分页参数(白名单校验,防注入到等值比较)。
func parseFindingFilter(r *http.Request) store.FindingFilter {
	q := r.URL.Query()
	f := store.FindingFilter{
		Severity: strings.TrimSpace(q.Get("severity")),
		Status:   strings.TrimSpace(q.Get("status")),
		Host:     strings.TrimSpace(q.Get("host")),
		Search:   strings.TrimSpace(q.Get("search")),
		Limit:    queryInt(r, "limit", 0),
		Offset:   queryInt(r, "offset", 0),
	}
	if !validSeverity(f.Severity) {
		f.Severity = ""
	}
	if !model.FindingStatus(f.Status).Valid() {
		f.Status = ""
	}
	return f
}

func validSeverity(s string) bool {
	switch model.Severity(s) {
	case model.SeverityCritical, model.SeverityHigh, model.SeverityMedium, model.SeverityLow:
		return true
	}
	return false
}

func (s *Service) handleListFindings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	findings, total, err := s.store.ListFindings(r.Context(), id, tenantID, parseFindingFilter(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if findings == nil {
		findings = []*model.Finding{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": findings, "total": total})
}

func (s *Service) handleListAllFindings(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	findings, total, err := s.store.ListFindingsAll(r.Context(), tenantID, parseFindingFilter(r))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if findings == nil {
		findings = []*model.Finding{}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"items": findings, "total": total})
}

// handleUpdateFinding 处置一条漏洞(PATCH /api/v1/vuln/findings/{id})。
// 支持改状态(open/fixed/ignored/false_positive)、写处置说明、设置忽略到期时间。
func (s *Service) handleUpdateFinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httputil.WriteError(w, http.StatusBadRequest, "missing finding id")
		return
	}
	var req struct {
		Status      *string `json:"status"`
		Note        *string `json:"note"`
		IgnoreUntil *string `json:"ignore_until"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var upd store.FindingUpdate
	if req.Status != nil {
		st := model.FindingStatus(strings.TrimSpace(*req.Status))
		if !st.Valid() {
			httputil.WriteError(w, http.StatusBadRequest, "invalid status: must be open/fixed/ignored/false_positive")
			return
		}
		upd.Status = &st
	}
	if req.Note != nil {
		note := strings.TrimSpace(*req.Note)
		if len(note) > 1000 {
			note = note[:1000]
		}
		upd.Note = &note
	}
	if req.IgnoreUntil != nil {
		raw := strings.TrimSpace(*req.IgnoreUntil)
		if raw == "" || raw == "null" {
			upd.ClearIgnore = true
		} else {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				httputil.WriteError(w, http.StatusBadRequest, "invalid ignore_until: expect RFC3339 or empty/null")
				return
			}
			upd.IgnoreUntil = &t
		}
	}
	tenantID := auth.TenantFromContext(r.Context())
	f, err := s.store.UpdateFinding(r.Context(), id, tenantID, upd)
	if err != nil {
		if err == store.ErrNotFound {
			httputil.WriteError(w, http.StatusNotFound, "finding not found")
			return
		}
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, f)
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stats.BySeverity == nil {
		stats.BySeverity = map[string]int{}
	}
	if stats.ByStatus == nil {
		stats.ByStatus = map[string]int{}
	}
	if stats.ByFindingStatus == nil {
		stats.ByFindingStatus = map[string]int{}
	}
	if stats.OpenBySeverity == nil {
		stats.OpenBySeverity = map[string]int{}
	}
	httputil.WriteJSON(w, http.StatusOK, stats)
}

// handleExportCSV 导出全量漏洞(CSV)。前端须以带鉴权头的方式下载
// (window.open 不会携带 Authorization,见 web 侧导出实现)。
func (s *Service) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	f := parseFindingFilter(r)
	f.Limit = 5000
	f.Offset = 0
	findings, _, err := s.store.ListFindingsAll(r.Context(), tenantID, f)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Write([]string{
		"host", "cve", "title", "severity", "cvss", "status",
		"affected_package", "installed_version", "fixed_version",
		"first_seen", "last_seen", "occurrences", "remediation", "reference", "note",
	})
	for _, f := range findings {
		writer.Write([]string{
			f.Host, f.CVE, f.Title, string(f.Severity), trimFloat(f.CVSS), string(f.Status),
			f.Affected, f.Installed, f.Fixed,
			fmtTime(f.FirstSeen), fmtTime(f.LastSeen), strconv.Itoa(f.Occurrences),
			f.Remediation, f.Reference, f.Note,
		})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=vuln_findings.csv")
	w.Write([]byte("\xEF\xBB\xBF"))
	w.Write(buf.Bytes())
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func trimFloat(f float64) string {
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', 1, 64)
}

// queryInt 读取整型查询参数(缺失/非法返回默认值),并夹取到 [0, 5000]。
func queryInt(r *http.Request, name string, def int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < 0 {
		return 0
	}
	if n > 5000 {
		return 5000
	}
	return n
}
