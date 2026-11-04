// Package servicehours 实现 ITSM「服务时间」：定义服务窗口（每周工作时段 + 时区 + 节假日），
// 供 SLA 策略与服务目录绑定，使工单截止时间按业务时间（而非 7x24 墙钟）推进。
//
// 计算纯逻辑（AddBusinessTime / BusinessElapsed / IsOpen）与持久化分离，
// 纯逻辑可零依赖单测。包不反向依赖 itsm 主包，由 main.go 装配（同 sla/ola 范式）。
package servicehours

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// DayWindow 一周中某天的服务时段。Start/End 为本地 HH:MM（按窗口时区解释）。
type DayWindow struct {
	Enabled bool   `json:"enabled"`
	Start   string `json:"start"` // "09:00"
	End     string `json:"end"`   // "18:00"
}

// Window 服务时间窗口定义。
// Weekly 索引 = time.Weekday（0=周日 ... 6=周六），长度恒补齐为 7。
type Window struct {
	ID          string      `json:"id"`
	TenantID    string      `json:"tenant_id"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Timezone    string      `json:"timezone"`           // IANA 名，默认 Asia/Shanghai
	Weekly      []DayWindow `json:"weekly"`             // 每周时段（normalize 后长度 7）
	Holidays    []string    `json:"holidays,omitempty"` // 额外排除日 "2006-01-02"（法定节假日等）
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

const defaultTimezone = "Asia/Shanghai"

// Loc 解析时区；非法值先回退 Asia/Shanghai，仍失败（无 tzdata 环境）回退 UTC，绝不返回 nil。
func (w *Window) Loc() *time.Location {
	if w.Timezone == "" {
		w.Timezone = defaultTimezone
	}
	if loc, err := time.LoadLocation(w.Timezone); err == nil {
		return loc
	}
	slog.Warn("servicehours: invalid timezone, fallback", "tz", w.Timezone)
	if loc, err := time.LoadLocation(defaultTimezone); err == nil {
		return loc
	}
	return time.UTC
}

var dateOnly = func(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

// IsOpen 判定时刻 t 是否落在服务窗口内（按窗口时区的本地日历）。
func (w *Window) IsOpen(t time.Time) bool {
	dw, ok := w.dayWindow(t)
	if !ok {
		return false
	}
	hm := t.In(w.Loc()).Format("15:04")
	return hm >= dw.Start && hm < dw.End
}

// dayWindow 取 t 当天（按窗口时区）的时段配置；节假日/未启用返回 false。
func (w *Window) dayWindow(t time.Time) (DayWindow, bool) {
	loc := w.Loc()
	local := t.In(loc)
	day := dateOnly(local, loc)
	for _, h := range w.Holidays {
		if h == day {
			return DayWindow{}, false
		}
	}
	wd := int(local.Weekday())
	if wd >= len(w.Weekly) {
		return DayWindow{}, false
	}
	dw := w.Weekly[wd]
	if !dw.Enabled {
		return DayWindow{}, false
	}
	return dw, true
} // parseHM 解析 "HH:MM" 为当天分钟数；非法返回 -1。
func parseHM(s string) int {
	var h, m int
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h < 0 || h > 24 || m < 0 || m > 59 {
		return -1
	}
	return h*60 + m
}

// AddBusinessTime 从 from 起在服务窗口内推进时长 d，返回墙钟截止时刻。
// 严格模式：跨窗口边界快进到下一窗口开始时刻；365 天内找不到窗口则按墙钟兜底
// （配置全空时退化为 from+d，行为与未绑定服务窗口一致）。
func (w *Window) AddBusinessTime(from time.Time, d time.Duration) time.Time {
	loc := w.Loc()
	cur := from.In(loc)
	remaining := d
	for day := 0; day <= 366; day++ {
		dw, ok := w.dayWindow(cur)
		if ok {
			startM, endM := parseHM(dw.Start), parseHM(dw.End)
			if startM >= 0 && endM > startM {
				dayStart := time.Date(cur.Year(), cur.Month(), cur.Day(), startM/60, startM%60, 0, 0, loc)
				dayEnd := time.Date(cur.Year(), cur.Month(), cur.Day(), endM/60, endM%60, 0, 0, loc)
				segStart := cur
				if segStart.Before(dayStart) {
					segStart = dayStart
				}
				if segStart.Before(dayEnd) {
					avail := dayEnd.Sub(segStart)
					if avail >= remaining {
						return segStart.Add(remaining)
					}
					remaining -= avail
				}
			}
		}
		// 快进到下一天 00:00（本地）
		cur = time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	}
	// 兜底：窗口配置异常（全禁用等）按墙钟，保证 deadline 仍会被设置
	return from.Add(d)
}

// BusinessElapsed 计算从到时刻之间的业务时长（预览/统计用；to<=from 返回 0）。
func (w *Window) BusinessElapsed(from, to time.Time) time.Duration {
	if !to.After(from) {
		return 0
	}
	loc := w.Loc()
	cur := from.In(loc)
	var total time.Duration
	for day := 0; day <= 366 && cur.Before(to); day++ {
		dw, ok := w.dayWindow(cur)
		if ok {
			startM, endM := parseHM(dw.Start), parseHM(dw.End)
			if startM >= 0 && endM > startM {
				dayStart := time.Date(cur.Year(), cur.Month(), cur.Day(), startM/60, startM%60, 0, 0, loc)
				dayEnd := time.Date(cur.Year(), cur.Month(), cur.Day(), endM/60, endM%60, 0, 0, loc)
				segStart, segEnd := cur, to
				if segStart.Before(dayStart) {
					segStart = dayStart
				}
				if segEnd.After(dayEnd) {
					segEnd = dayEnd
				}
				if segEnd.After(segStart) {
					total += segEnd.Sub(segStart)
				}
			}
		}
		cur = time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	}
	return total
}

// Store 服务窗口持久化（PG 自愈 DDL，同 sla/ola 范式）。
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	return s
}

func (s *Store) migrate() {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS itsm_service_windows (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		name TEXT NOT NULL, description TEXT DEFAULT '',
		timezone TEXT DEFAULT 'Asia/Shanghai',
		weekly JSONB DEFAULT '[]'::jsonb,
		holidays JSONB DEFAULT '[]'::jsonb,
		created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`); err != nil {
		slog.Warn("servicehours: migrate failed", "err", err)
	}
}

const windowCols = `id,tenant_id,name,description,timezone,weekly,holidays,created_at,updated_at`

func scanWindow(sc interface{ Scan(...any) error }) (*Window, error) {
	var w Window
	var weeklyJSON, holidaysJSON []byte
	if err := sc.Scan(&w.ID, &w.TenantID, &w.Name, &w.Description, &w.Timezone,
		&weeklyJSON, &holidaysJSON, &w.CreatedAt, &w.UpdatedAt); err != nil {
		return nil, err
	}
	// weekly 兼容老数据：未配置（null/空数组）时补齐标准 5x8
	if err := json.Unmarshal(weeklyJSON, &w.Weekly); err != nil || len(w.Weekly) != 7 {
		w.Weekly = defaultWeekly()
	}
	if len(holidaysJSON) > 0 {
		_ = json.Unmarshal(holidaysJSON, &w.Holidays)
	}
	if w.Timezone == "" {
		w.Timezone = defaultTimezone
	}
	return &w, nil
}

func defaultWeekly() []DayWindow {
	weekly := make([]DayWindow, 7)
	for i := 1; i <= 5; i++ { // 周一~周五 09:00-18:00 为新窗口默认
		weekly[i] = DayWindow{Enabled: true, Start: "09:00", End: "18:00"}
	}
	return weekly
}

func (w *Window) normalize() {
	if w.TenantID == "" {
		w.TenantID = "default"
	}
	if w.Timezone == "" {
		w.Timezone = defaultTimezone
	}
	if len(w.Weekly) != 7 { // 未配置/老数据：补齐为标准 5x8
		base := w.Weekly
		w.Weekly = defaultWeekly()
		copy(w.Weekly, base)
	}
	for i := range w.Weekly {
		if parseHM(w.Weekly[i].Start) < 0 {
			w.Weekly[i].Start = "09:00"
		}
		if parseHM(w.Weekly[i].End) < 0 {
			w.Weekly[i].End = "18:00"
		}
	}
	if w.Holidays == nil {
		w.Holidays = []string{}
	}
}

func (s *Store) List(ctx context.Context, tenantID string) ([]*Window, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+windowCols+` FROM itsm_service_windows WHERE tenant_id=$1 ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Window
	for rows.Next() {
		w, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id, tenantID string) (*Window, error) {
	w, err := scanWindow(s.db.QueryRowContext(ctx,
		`SELECT `+windowCols+` FROM itsm_service_windows WHERE id=$1 AND tenant_id=$2`, id, tenantID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return w, err
}

func (s *Store) Upsert(ctx context.Context, w *Window) error {
	w.normalize()
	if w.ID == "" {
		w.ID = uuid.New().String()
	}
	now := time.Now()
	w.UpdatedAt = now
	weeklyJSON, _ := json.Marshal(w.Weekly)
	holidaysJSON, _ := json.Marshal(w.Holidays)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO itsm_service_windows (id,tenant_id,name,description,timezone,weekly,holidays,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, description=EXCLUDED.description,
			timezone=EXCLUDED.timezone, weekly=EXCLUDED.weekly, holidays=EXCLUDED.holidays, updated_at=EXCLUDED.updated_at`,
		w.ID, w.TenantID, w.Name, w.Description, w.Timezone, string(weeklyJSON), string(holidaysJSON), now)
	if w.CreatedAt.IsZero() {
		w.CreatedAt = now
	}
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM itsm_service_windows WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

// RegisterRoutes 注册服务窗口端点（RBAC 走 /api/v1/itsm 前缀逐级回退，itsm:read/write）。
func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/itsm/service-windows", s.handleList)
	mux.HandleFunc("POST /api/v1/itsm/service-windows", s.handleUpsert)
	mux.HandleFunc("GET /api/v1/itsm/service-windows/{id}/preview", s.handlePreview)
	mux.HandleFunc("DELETE /api/v1/itsm/service-windows/{id}", s.handleDelete)
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	ws, err := s.List(r.Context(), auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ws == nil {
		ws = []*Window{}
	}
	httputil.WriteJSON(w, http.StatusOK, ws)
}

func (s *Store) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var win Window
	if err := httputil.BindJSON(r, &win); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if win.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	win.TenantID = auth.TenantFromContext(r.Context())
	if err := s.Upsert(r.Context(), &win); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, win)
}

// handlePreview 试算：GET /{id}/preview?hours=N&from=RFC3339
// 返回「从 from 起在服务窗口内推进 N 小时后的墙钟截止时刻」，供前端编辑器即时验证。
func (s *Store) handlePreview(w http.ResponseWriter, r *http.Request) {
	win, err := s.Get(r.Context(), r.PathValue("id"), auth.TenantFromContext(r.Context()))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if win == nil {
		httputil.WriteError(w, http.StatusNotFound, "window not found")
		return
	}
	hours := 8.0
	if h := r.URL.Query().Get("hours"); h != "" {
		if _, err := fmt.Sscanf(h, "%f", &hours); err != nil || hours <= 0 {
			httputil.WriteError(w, http.StatusBadRequest, "invalid hours")
			return
		}
	}
	from := time.Now()
	if f := r.URL.Query().Get("from"); f != "" {
		t, err := time.Parse(time.RFC3339, f)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "invalid from (want RFC3339)")
			return
		}
		from = t
	}
	deadline := win.AddBusinessTime(from, time.Duration(hours*float64(time.Hour)))
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"from":             from,
		"hours":            hours,
		"deadline":         deadline,
		"elapsed_business": win.BusinessElapsed(from, deadline).String(),
	})
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Delete(r.Context(), r.PathValue("id"), auth.TenantFromContext(r.Context())); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
