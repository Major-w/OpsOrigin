package notification

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type pgStore struct {
	db *sql.DB
}

func newPGStore(db *sql.DB) *pgStore {
	s := &pgStore{db: db}
	s.init(context.Background())
	return s
}

func (s *pgStore) init(ctx context.Context) {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS notification_channels (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, type TEXT,
			config TEXT DEFAULT '{}', enabled BOOLEAN DEFAULT TRUE,
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_channels_tenant ON notification_channels(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS notification_policies (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, name TEXT, event TEXT,
			channel_ids TEXT DEFAULT '[]', template TEXT, quiet_hours TEXT,
			enabled BOOLEAN DEFAULT TRUE, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_policies_tenant ON notification_policies(tenant_id)`,
		`CREATE TABLE IF NOT EXISTS notification_history (
			id TEXT PRIMARY KEY, tenant_id TEXT NOT NULL, channel_id TEXT,
			event TEXT, title TEXT, content TEXT, status TEXT, error TEXT,
			created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_notification_history_tenant ON notification_history(tenant_id, created_at DESC)`,
	}
	for _, st := range stmts {
		if _, err := s.db.ExecContext(ctx, st); err != nil {
			slog.Warn("notification pg store init DDL failed", "err", err)
			return
		}
	}
}

func toJSON(v any) []byte {
	if v == nil {
		return []byte("null")
	}
	b, _ := json.Marshal(v)
	return b
}

func (s *pgStore) CreateChannel(ctx context.Context, c *Channel) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	if c.TenantID == "" {
		c.TenantID = "default"
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_channels(id,tenant_id,name,type,config,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`,
		c.ID, c.TenantID, c.Name, string(c.Type), toJSON(c.Config), c.Enabled, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *pgStore) GetChannel(ctx context.Context, id, tenantID string) (*Channel, error) {
	c := &Channel{}
	var cfg []byte
	var typ string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,type,config,enabled,created_at,updated_at FROM notification_channels WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&c.ID, &c.TenantID, &c.Name, &typ, &cfg, &c.Enabled, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Type = ChannelType(typ)
	_ = json.Unmarshal(cfg, &c.Config)
	return c, nil
}

func (s *pgStore) ListChannels(ctx context.Context, tenantID string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,type,config,enabled,created_at,updated_at FROM notification_channels WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Channel
	for rows.Next() {
		c := &Channel{}
		var cfg []byte
		var typ string
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &typ, &cfg, &c.Enabled, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.Type = ChannelType(typ)
		_ = json.Unmarshal(cfg, &c.Config)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdateChannel(ctx context.Context, c *Channel) error {
	c.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_channels SET name=$2,type=$3,config=$4,enabled=$5,updated_at=$6 WHERE id=$1 AND tenant_id=$7`,
		c.ID, c.Name, string(c.Type), toJSON(c.Config), c.Enabled, c.UpdatedAt, c.TenantID)
	return err
}

func (s *pgStore) DeleteChannel(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) CreatePolicy(ctx context.Context, p *Policy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	if p.TenantID == "" {
		p.TenantID = "default"
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_policies(id,tenant_id,name,event,channel_ids,template,quiet_hours,enabled,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(id) DO NOTHING`,
		p.ID, p.TenantID, p.Name, string(p.Event), toJSON(p.ChannelIDs), p.Template, p.QuietHours, p.Enabled, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *pgStore) GetPolicy(ctx context.Context, id, tenantID string) (*Policy, error) {
	p := &Policy{}
	var cids []byte
	var evt string
	err := s.db.QueryRowContext(ctx,
		`SELECT id,tenant_id,name,event,channel_ids,template,quiet_hours,enabled,created_at,updated_at FROM notification_policies WHERE id=$1 AND tenant_id=$2`, id, tenantID).
		Scan(&p.ID, &p.TenantID, &p.Name, &evt, &cids, &p.Template, &p.QuietHours, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Event = EventType(evt)
	_ = json.Unmarshal(cids, &p.ChannelIDs)
	return p, nil
}

func (s *pgStore) ListPolicies(ctx context.Context, tenantID string) ([]*Policy, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,name,event,channel_ids,template,quiet_hours,enabled,created_at,updated_at FROM notification_policies WHERE tenant_id=$1 ORDER BY updated_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Policy
	for rows.Next() {
		p := &Policy{}
		var cids []byte
		var evt string
		if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &evt, &cids, &p.Template, &p.QuietHours, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Event = EventType(evt)
		_ = json.Unmarshal(cids, &p.ChannelIDs)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *pgStore) UpdatePolicy(ctx context.Context, p *Policy) error {
	p.UpdatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE notification_policies SET name=$2,event=$3,channel_ids=$4,template=$5,quiet_hours=$6,enabled=$7,updated_at=$8 WHERE id=$1 AND tenant_id=$9`,
		p.ID, p.Name, string(p.Event), toJSON(p.ChannelIDs), p.Template, p.QuietHours, p.Enabled, p.UpdatedAt, p.TenantID)
	return err
}

func (s *pgStore) DeletePolicy(ctx context.Context, id, tenantID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notification_policies WHERE id=$1 AND tenant_id=$2`, id, tenantID)
	return err
}

func (s *pgStore) SaveNotification(ctx context.Context, n *Notification) error {
	if n.ID == "" {
		n.ID = uuid.New().String()
	}
	if n.TenantID == "" {
		n.TenantID = "default"
	}
	if n.Status == "" {
		n.Status = "sent"
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO notification_history(id,tenant_id,channel_id,event,title,content,status,error,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		n.ID, n.TenantID, n.ChannelID, string(n.Event), n.Title, n.Content, n.Status, n.Error, n.CreatedAt)
	return err
}

func (s *pgStore) ListNotifications(ctx context.Context, tenantID string, limit int) ([]*Notification, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,tenant_id,channel_id,event,title,content,status,error,created_at FROM notification_history WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		n := &Notification{}
		var evt, chID sql.NullString
		if err := rows.Scan(&n.ID, &n.TenantID, &chID, &evt, &n.Title, &n.Content, &n.Status, &n.Error, &n.CreatedAt); err != nil {
			return nil, err
		}
		if evt.Valid {
			n.Event = EventType(evt.String)
		}
		if chID.Valid {
			n.ChannelID = chID.String
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
