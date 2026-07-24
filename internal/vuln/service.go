package vuln

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/opsorigin/core/internal/vuln/model"
	"github.com/opsorigin/core/internal/vuln/store"
	"github.com/opsorigin/core/pkg/config"
)

// InventoryProvider 提供真实资产清单(从 CMDB 拉取并按约定 schema 映射)。
// 由 cmd/core/main.go 的适配器注入(包装 cmdb.Service.ListCI),
// 解耦 vuln↔cmdb,避免反向依赖。
type InventoryProvider interface {
	ListHostInventory(ctx context.Context, tenantID string) ([]InventoryEntry, error)
}

// Service 漏洞扫描服务；Scanner 预留 Trivy/Grype/Nuclei/OpenSCAP 热插拔位。
type Service struct {
	cfg          *config.Config
	store        store.Store
	scanner      Scanner
	invProvider  InventoryProvider
	agentRunner  AgentRunner
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
	mux.HandleFunc("GET /api/v1/vuln/scans/{id}", s.handleGetScan)
	mux.HandleFunc("DELETE /api/v1/vuln/scans/{id}", s.handleDeleteScan)
	mux.HandleFunc("GET /api/v1/vuln/scans/{id}/findings", s.handleListFindings)
	mux.HandleFunc("GET /api/v1/vuln/stats", s.handleStats)
	mux.HandleFunc("GET /api/v1/vuln/scans/export/csv", s.handleExportCSV)
}

func (s *Service) handleCreateScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name     string          `json:"name"`
		Type     string          `json:"type"`
		DemoMode bool            `json:"demo_mode"`
		Images       []string        `json:"images"`
		AgentID      string          `json:"agent_id"`
		Registry     *RegistryConfig `json:"registry"`
		RegistryAuto bool            `json:"registry_auto"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	stype := model.ScanType(req.Type)
	if stype != model.ScanTypeFull && stype != model.ScanTypeQuick && stype != model.ScanTypeCVE && stype != model.ScanTypeAgent && stype != model.ScanTypeImage {
		stype = model.ScanTypeFull
	}
	tenantID := tenantFromCtx(r.Context())
	now := time.Now()
	scan := &model.Scan{
		TenantID:      tenantID,
		Name:          req.Name,
		Type:          stype,
		DemoMode:      req.DemoMode,
		Status:        model.ScanStatusRunning,
		SeverityCounts: map[string]int{},
		CreatedAt:     now,
	}
	if err := s.store.CreateScan(r.Context(), scan); err != nil {
		slog.Error("vuln: create scan failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create scan")
		return
	}

	// 非 demo(或 agent 类型)且注入了 InventoryProvider 时,从 CMDB 拉真实资产清单;
	// 拉取失败或为空时 scanner 在非 demo 下返回空(不造假)。
	// agent 类型强制拉取(无视 demo 开关),供 AgentScanner 经 Agent 下发 trivy。
	var inv []InventoryEntry
	if s.invProvider != nil && (!req.DemoMode || stype == model.ScanTypeAgent) && stype != model.ScanTypeImage {
		inv, _ = s.invProvider.ListHostInventory(r.Context(), tenantID)
	}
	// 按扫描类型选扫描器:
	//  - agent 类型用 AgentScanner(经 Agent 下发 trivy fs 扫主机);
	//  - image 类型用 ImageScanner(经 Agent 下发 trivy image 扫仓库镜像);
	//  - 其余用内置 RuleScanner(基于 CVE 规则库)。
	var sc Scanner = s.scanner
	switch stype {
	case model.ScanTypeAgent:
		if s.agentRunner == nil {
			writeError(w, http.StatusBadRequest, "agent scan requires agent runner (agentmgr not available)")
			return
		}
		sc = NewAgentScanner(s.agentRunner)
	case model.ScanTypeImage:
		if s.agentRunner == nil {
			writeError(w, http.StatusBadRequest, "image scan requires agent runner (agentmgr not available)")
			return
		}
		if req.AgentID == "" {
			writeError(w, http.StatusBadRequest, "image scan requires agent_id (host with registry egress)")
			return
		}
		reg := RegistryConfig{}
		if req.Registry != nil {
			reg = *req.Registry
		}
		// 镜像清单默认由请求显式给出;registry_auto=true 时改由 trivy registry 枚举。
		inv = nil
		if req.RegistryAuto {
			if reg.URL == "" {
				writeError(w, http.StatusBadRequest, "registry_auto requires registry.url")
				return
			}
			sc = NewImageScanner(s.agentRunner, reg, true, req.AgentID)
		} else {
			if len(req.Images) == 0 {
				writeError(w, http.StatusBadRequest, "image scan requires images[] (full refs, e.g. registry.io/team/app:tag) or registry_auto:true")
				return
			}
			for _, ref := range req.Images {
				inv = append(inv, InventoryEntry{
					ImageRef:     ref,
					AgentID:      req.AgentID,
					Registry:     reg.URL,
					RegistryUser: reg.Username,
					RegistryPass: reg.Password,
				})
			}
			sc = NewImageScanner(s.agentRunner, reg, false, req.AgentID)
		}
	}
	findings, err := sc.Scan(r.Context(), tenantID, scan.ID, string(stype), req.DemoMode, inv)
	if err != nil {
		slog.Error("vuln: scan failed", "error", err)
		scan.Status = model.ScanStatusFailed
		s.store.UpdateScan(r.Context(), scan)
		writeError(w, http.StatusInternalServerError, "scan failed")
		return
	}
	counts := map[string]int{}
	for _, f := range findings {
		counts[string(f.Severity)]++
	}
	scan.SeverityCounts = counts
	scan.TotalFindings = len(findings)
	scan.Status = model.ScanStatusCompleted
	completed := now
	scan.CompletedAt = &completed

	if err := s.store.CreateFindings(r.Context(), findings); err != nil {
		slog.Error("vuln: store findings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to store findings")
		return
	}
	if err := s.store.UpdateScan(r.Context(), scan); err != nil {
		slog.Error("vuln: finalize scan failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to finalize scan")
		return
	}
	writeJSON(w, http.StatusOK, scan)
}

func (s *Service) handleListScans(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	scans, err := s.store.ListScans(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if scans == nil {
		scans = []*model.Scan{}
	}
	writeJSON(w, http.StatusOK, scans)
}

func (s *Service) handleGetScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	scan, err := s.store.GetScan(r.Context(), id, tenantID)
	if err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "scan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, scan)
}

func (s *Service) handleDeleteScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.DeleteScan(r.Context(), id, tenantID); err != nil {
		if err == store.ErrNotFound {
			writeError(w, http.StatusNotFound, "scan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleListFindings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	findings, err := s.store.ListFindings(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if findings == nil {
		findings = []*model.Finding{}
	}
	writeJSON(w, http.StatusOK, findings)
}

func (s *Service) handleStats(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	stats, err := s.store.GetStats(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if stats.BySeverity == nil {
		stats.BySeverity = map[string]int{}
	}
	if stats.ByStatus == nil {
		stats.ByStatus = map[string]int{}
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Service) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	findings, err := s.store.ListFindingsAll(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	writer.Write([]string{
		"scan_id", "cve", "title", "severity", "affected_package",
		"installed_version", "fixed_version", "reference", "rank",
	})
	for _, f := range findings {
		writer.Write([]string{
			f.ScanID, f.CVE, f.Title, string(f.Severity),
			f.Affected, f.Installed, f.Fixed, f.Reference,
			strconv.Itoa(f.Rank),
		})
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=vuln_findings.csv")
	w.Write([]byte("\xEF\xBB\xBF"))
	w.Write(buf.Bytes())
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
