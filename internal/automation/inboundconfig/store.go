// Package inboundconfig 提供邮件 / IM 机器人（企业微信、钉钉、飞书）入站渠道的统一配置持久化。
//
// 设计：原先各渠道配置由 cmd/core 从环境变量装配（inboundmail.Config / inboundbot.Config），
// 无法在界面动态调整。本包把渠道配置落到 DB，由 cmd/core 在启动时（及邮件轮询定时重载时）
// 读取并注入到 inboundmail / inboundbot 服务，使「入站配置 UI」可实时管理渠道开关与参数。
// 飞书(Lark)渠道为本次新增，inboundbot 新增对应回调处理。
package inboundconfig

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

// ChannelType 渠道类型。
const (
	TypeEmail    = "email"
	TypeWeCom    = "wecom"
	TypeDingTalk = "dingtalk"
	TypeFeishu   = "feishu"
)

// Channel 一个入站渠道的配置（按类型存于 config JSON 中）。
type Channel struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Type      string    `json:"type"` // email | wecom | dingtalk | feishu
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Config    string    `json:"config"` // 类型相关配置（JSON）
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store {
	s := &Store{db: db}
	s.migrate()
	s.seed()
	return s
}

func (s *Store) migrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS automation_inbound_channels (
		id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL DEFAULT 'default',
		type TEXT NOT NULL, name TEXT NOT NULL, enabled BOOLEAN DEFAULT FALSE,
		config TEXT DEFAULT '{}', created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW()
	)`)
}

func (s *Store) seed() {
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM automation_inbound_channels`).Scan(&count)
	if count > 0 {
		return
	}
	seeds := []Channel{
		{Type: TypeEmail, Name: "邮件入站(IMAP)"},
		{Type: TypeWeCom, Name: "企业微信机器人"},
		{Type: TypeDingTalk, Name: "钉钉机器人"},
		{Type: TypeFeishu, Name: "飞书机器人"},
	}
	for _, c := range seeds {
		c.ID = uuid.New().String()
		c.TenantID = "default"
		c.CreatedAt = time.Now()
		c.UpdatedAt = c.CreatedAt
		s.db.Exec(`INSERT INTO automation_inbound_channels(id,tenant_id,type,name,enabled,config,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			c.ID, c.TenantID, c.Type, c.Name, c.Enabled, c.Config, c.CreatedAt, c.UpdatedAt)
	}
}

const cols = `id,tenant_id,type,name,enabled,config,created_at,updated_at`

func (s *Store) List(ctx context.Context, tenantID string) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cols+` FROM automation_inbound_channels WHERE tenant_id=$1 ORDER BY type`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		var cfg []byte
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Type, &c.Name, &c.Enabled, &cfg, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		if len(cfg) > 0 {
			c.Config = string(cfg)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetByType 按渠道类型取当前租户的渠道（用于运行时装配 bot/mail 配置）。
func (s *Store) GetByType(ctx context.Context, tenantID, typ string) (*Channel, error) {
	var c Channel
	var cfg []byte
	err := s.db.QueryRowContext(ctx, `SELECT `+cols+` FROM automation_inbound_channels WHERE tenant_id=$1 AND type=$2`, tenantID, typ).
		Scan(&c.ID, &c.TenantID, &c.Type, &c.Name, &c.Enabled, &cfg, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(cfg) > 0 {
		c.Config = string(cfg)
	}
	return &c, nil
}

func (s *Store) Upsert(ctx context.Context, c *Channel) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.TenantID == "" {
		c.TenantID = "default"
	}
	if c.Config == "" {
		c.Config = "{}"
	}
	now := time.Now()
	c.UpdatedAt = now
	// 简单 upsert：已存在同类型则更新，否则插入
	var existID string
	s.db.QueryRowContext(ctx, `SELECT id FROM automation_inbound_channels WHERE tenant_id=$1 AND type=$2`, c.TenantID, c.Type).Scan(&existID)
	if existID != "" {
		c.ID = existID
		_, err := s.db.ExecContext(ctx, `UPDATE automation_inbound_channels SET name=$1,enabled=$2,config=$3,updated_at=$4 WHERE id=$5`,
			c.Name, c.Enabled, c.Config, c.UpdatedAt, c.ID)
		return err
	}
	c.CreatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO automation_inbound_channels(id,tenant_id,type,name,enabled,config,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		c.ID, c.TenantID, c.Type, c.Name, c.Enabled, c.Config, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) Delete(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM automation_inbound_channels WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *Store) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/automation/inbound/channels", s.handleList)
	mux.HandleFunc("POST /api/v1/automation/inbound/channels", s.handleUpsert)
	mux.HandleFunc("PUT /api/v1/automation/inbound/channels/{id}", s.handleUpsert)
	mux.HandleFunc("DELETE /api/v1/automation/inbound/channels/{id}", s.handleDelete)
}

func tenantOf(r *http.Request) string {
	t := r.Header.Get("X-Tenant-ID")
	if t == "" {
		return "default"
	}
	return t
}

func (s *Store) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.List(r.Context(), tenantOf(r))
	if err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, httputil.EmptySliceIfNil(list))
}

func (s *Store) handleUpsert(w http.ResponseWriter, r *http.Request) {
	var c Channel
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		httputil.WriteJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	c.TenantID = tenantOf(r)
	if err := s.Upsert(r.Context(), &c); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 200, c)
}

func (s *Store) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Delete(r.Context(), id, tenantOf(r)); err != nil {
		httputil.WriteJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	httputil.WriteJSON(w, 204, nil)
}
