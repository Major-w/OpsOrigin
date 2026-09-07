// Package chatops 提供 ChatOps 多渠道接入能力(P3-2):
// 将 Slack / 飞书 / 企业微信等 IM 的入站消息桥接到 AI 引擎,
// 并把 AI 审批请求推回 IM(卡片按钮交互), 实现在 IM 内完成运维闭环。
//
// 模块边界: 本包只做「渠道适配 + 会话绑定 + 消息桥接」, 不实现 AI 逻辑
// (复用 internal/ai/agentic.Engine), 不做单向告警推送(归 internal/notification)。
package chatops

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Channel 一个 IM 渠道接入实例(如某个飞书自建应用)。
// Settings 内容因平台而异(app_id/app_secret/verification_token/encrypt_key...), 以 JSONB 存储。
type Channel struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	Platform  string          `json:"platform"` // feishu / slack / wecom
	Name      string          `json:"name"`
	Enabled   bool            `json:"enabled"`
	Settings  json.RawMessage `json:"settings"` // 平台凭据, 读取接口对 secret 字段脱敏
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Binding (platform, chat_id) → AI 会话的绑定关系。
// 同一个 IM 群/单聊续接同一 AI 会话上下文; 支持显式指定 agent。
type Binding struct {
	Platform       string    `json:"platform"`
	ChatID         string    `json:"chat_id"`
	TenantID       string    `json:"tenant_id"`
	AgentID        string    `json:"agent_id"`        // 空=平台默认 agent
	ConversationID string    `json:"conversation_id"` // AI 会话 ID(Engine.Chat 续接)
	UpdatedAt      time.Time `json:"updated_at"`
}

// store pg 持久化。
type store struct {
	db *sql.DB
}

func newStore(db *sql.DB) *store { return &store{db: db} }

func (s *store) init(ctx context.Context) error {
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS chatops_channels (
			id          TEXT PRIMARY KEY,
			tenant_id   TEXT NOT NULL DEFAULT 'default',
			platform    TEXT NOT NULL,
			name        TEXT NOT NULL DEFAULT '',
			enabled     BOOLEAN NOT NULL DEFAULT TRUE,
			settings    JSONB NOT NULL DEFAULT '{}',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS chatops_bindings (
			platform        TEXT NOT NULL,
			chat_id         TEXT NOT NULL,
			tenant_id       TEXT NOT NULL DEFAULT 'default',
			agent_id        TEXT NOT NULL DEFAULT '',
			conversation_id TEXT NOT NULL DEFAULT '',
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (platform, chat_id)
		)`,
	} {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	return nil
}

// ---- channels ----

func (s *store) createChannel(ctx context.Context, c *Channel) error {
	return s.db.QueryRowContext(ctx,
		`INSERT INTO chatops_channels (id, tenant_id, platform, name, enabled, settings)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at, updated_at`,
		c.ID, c.TenantID, c.Platform, c.Name, c.Enabled, []byte(c.Settings),
	).Scan(&c.CreatedAt, &c.UpdatedAt)
}

func (s *store) updateChannel(ctx context.Context, c *Channel) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE chatops_channels SET name=$2, enabled=$3, settings=$4, updated_at=now()
		 WHERE id=$1 AND tenant_id=$5`,
		c.ID, c.Name, c.Enabled, []byte(c.Settings), c.TenantID,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (s *store) deleteChannel(ctx context.Context, id, tenantID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM chatops_channels WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (s *store) getChannel(ctx context.Context, id string) (*Channel, error) {
	return s.scanChannel(s.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, platform, name, enabled, settings, created_at, updated_at
		 FROM chatops_channels WHERE id=$1`, id))
}

func (s *store) listChannels(ctx context.Context, tenantID string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, platform, name, enabled, settings, created_at, updated_at
		 FROM chatops_channels WHERE tenant_id=$1 ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Channel
	for rows.Next() {
		c, err := s.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// listEnabledChannels 全租户范围取启用渠道(回调处理按 channel id 直查, 此处供健康汇总)。
func (s *store) listEnabledChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tenant_id, platform, name, enabled, settings, created_at, updated_at
		 FROM chatops_channels WHERE enabled ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Channel
	for rows.Next() {
		c, err := s.scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func (s *store) scanChannel(r rowScanner) (*Channel, error) {
	c := &Channel{}
	var settings []byte
	if err := r.Scan(&c.ID, &c.TenantID, &c.Platform, &c.Name, &c.Enabled, &settings, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Settings = json.RawMessage(settings)
	return c, nil
}

// ---- bindings ----

func (s *store) getBinding(ctx context.Context, platform, chatID string) (*Binding, error) {
	b := &Binding{}
	err := s.db.QueryRowContext(ctx,
		`SELECT platform, chat_id, tenant_id, agent_id, conversation_id, updated_at
		 FROM chatops_bindings WHERE platform=$1 AND chat_id=$2`, platform, chatID,
	).Scan(&b.Platform, &b.ChatID, &b.TenantID, &b.AgentID, &b.ConversationID, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil // 首次会话, 无绑定属正常路径
	}
	return b, err
}

func (s *store) upsertBinding(ctx context.Context, b *Binding) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO chatops_bindings (platform, chat_id, tenant_id, agent_id, conversation_id, updated_at)
		 VALUES ($1,$2,$3,$4,$5,now())
		 ON CONFLICT (platform, chat_id)
		 DO UPDATE SET tenant_id=$3, agent_id=$4, conversation_id=$5, updated_at=now()`,
		b.Platform, b.ChatID, b.TenantID, b.AgentID, b.ConversationID,
	)
	return err
}

func (s *store) listBindings(ctx context.Context, tenantID string) ([]*Binding, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT platform, chat_id, tenant_id, agent_id, conversation_id, updated_at
		 FROM chatops_bindings WHERE tenant_id=$1 ORDER BY updated_at DESC LIMIT 200`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Binding
	for rows.Next() {
		b := &Binding{}
		if err := rows.Scan(&b.Platform, &b.ChatID, &b.TenantID, &b.AgentID, &b.ConversationID, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *store) deleteBinding(ctx context.Context, platform, chatID, tenantID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM chatops_bindings WHERE platform=$1 AND chat_id=$2 AND tenant_id=$3`,
		platform, chatID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}
