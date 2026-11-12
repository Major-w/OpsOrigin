package ai

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/lib/pq"
	"github.com/opsorigin/core/internal/ai/agentic"
)

// PGApprovalPolicyStore 审批策略的 PostgreSQL 实现。
//
// 与 deploy/migrations/041_ai_agent_governance.sql 双写自愈 DDL; 无迁移 runner 时
// 由本文件 init() 保证表存在。策略按 (tenant_id, agent_id) 唯一: agent_id=” 为租户级全局默认。
type PGApprovalPolicyStore struct {
	db *sql.DB
}

var _ agentic.ApprovalPolicyStore = (*PGApprovalPolicyStore)(nil)

func NewPGApprovalPolicyStore(db *sql.DB) *PGApprovalPolicyStore {
	s := &PGApprovalPolicyStore{db: db}
	s.init()
	return s
}

func (s *PGApprovalPolicyStore) init() {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_approval_policies (
			tenant_id       TEXT NOT NULL DEFAULT 'default',
			agent_id        TEXT NOT NULL DEFAULT '',
			mode            TEXT NOT NULL DEFAULT 'sensitive_only',
			sensitive_tools TEXT[] NOT NULL DEFAULT '{}'::text[],
			ask_timeout_sec INTEGER NOT NULL DEFAULT 60,
			updated_by      TEXT NOT NULL DEFAULT '',
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (tenant_id, agent_id)
		)`); err != nil {
		slog.Warn("ai approval policy store init DDL failed", "err", err)
	}
}

// GetApprovalPolicy 读取指定员工的覆盖策略; 不存在返回 (nil, nil), 由调用方回落全局/内存默认。
func (s *PGApprovalPolicyStore) GetApprovalPolicy(ctx context.Context, tenantID, agentID string) (*agentic.ApprovalPolicy, error) {
	if tenantID == "" {
		tenantID = "default"
	}
	p := &agentic.ApprovalPolicy{}
	err := s.db.QueryRowContext(ctx,
		`SELECT mode, sensitive_tools, ask_timeout_sec FROM ai_approval_policies WHERE tenant_id=$1 AND agent_id=$2`,
		tenantID, agentID).Scan(&p.Mode, pq.Array(&p.SensitiveTools), &p.AskTimeoutSec)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.SensitiveTools == nil {
		p.SensitiveTools = []string{}
	}
	if p.AskTimeoutSec <= 0 {
		p.AskTimeoutSec = 60
	}
	return p, nil
}

// SaveApprovalPolicy 幂等写入策略(存在即更新)。sensitive_tools 归一为非 nil 切片,
// 避免 pq.Array(nil) 编码成 SQL NULL 撞 NOT NULL 列。
func (s *PGApprovalPolicyStore) SaveApprovalPolicy(ctx context.Context, tenantID, agentID string, p agentic.ApprovalPolicy, updatedBy string) error {
	if tenantID == "" {
		tenantID = "default"
	}
	if p.Mode == "" {
		p.Mode = agentic.ApprovalSensitiveOnly
	}
	if p.SensitiveTools == nil {
		p.SensitiveTools = []string{}
	}
	if p.AskTimeoutSec <= 0 {
		p.AskTimeoutSec = 60
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ai_approval_policies(tenant_id,agent_id,mode,sensitive_tools,ask_timeout_sec,updated_by,updated_at)
		 VALUES($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (tenant_id, agent_id) DO UPDATE SET
		   mode=$3, sensitive_tools=$4, ask_timeout_sec=$5, updated_by=$6, updated_at=$7`,
		tenantID, agentID, string(p.Mode), pq.Array(p.SensitiveTools), p.AskTimeoutSec, updatedBy, time.Now())
	return err
}
