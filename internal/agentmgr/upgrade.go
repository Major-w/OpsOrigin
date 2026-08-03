package agentmgr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// UpgradeManager 管理 Agent 二进制文件的滚动升级。
type UpgradeManager struct {
	mu sync.RWMutex

	db *sql.DB

	// 当前活跃的升级策略
	activeVersion string
	activeSHA256  string
	filePath      string
	strategy      RolloutStrategy

	// 升级进度跟踪
	progress map[string]*UpgradeProgress // agentID → progress
}

// RolloutStrategy 滚动升级策略。
type RolloutStrategy struct {
	BatchSize     int           `json:"batch_size"`     // 每批升级数量
	BatchInterval time.Duration `json:"batch_interval"` // 批次间隔
	StartedAt     time.Time     `json:"started_at"`
}

// UpgradeProgress 单个 Agent 的升级进度。
type UpgradeProgress struct {
	AgentID     string     `json:"agent_id"`
	UpgradeID   string     `json:"upgrade_id"`
	Status      string     `json:"status"` // pending, downloading, completed, failed
	Version     string     `json:"version"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// UpgradeInfo 心跳响应中的升级信息。
type UpgradeInfo struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
	SHA256      string `json:"sha256"`
	Force       bool   `json:"force"`
}

// NewUpgradeManager 创建升级管理器。
func NewUpgradeManager(db *sql.DB) *UpgradeManager {
	um := &UpgradeManager{
		db:       db,
		progress: make(map[string]*UpgradeProgress),
	}
	um.initDB()
	return um
}

func (um *UpgradeManager) initDB() {
	ctx, cancel := contextTimeout(3 * time.Second)
	defer cancel()
	um.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS agent_upgrades (
		id TEXT PRIMARY KEY,
		version TEXT NOT NULL,
		file_path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size_bytes BIGINT DEFAULT 0,
		created_at TIMESTAMPTZ NOT NULL
	)`)
	um.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS agent_upgrade_progress (
		id TEXT PRIMARY KEY,
		agent_id TEXT NOT NULL,
		upgrade_id TEXT NOT NULL,
		version TEXT NOT NULL,
		status TEXT NOT NULL,
		started_at TIMESTAMPTZ,
		completed_at TIMESTAMPTZ,
		error TEXT,
		UNIQUE(agent_id, upgrade_id)
	)`)
}

func contextTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// Upload 上传 Agent 二进制文件。
func (um *UpgradeManager) Upload(version string, data []byte) (*UpgradeInfo, error) {
	um.mu.Lock()
	defer um.mu.Unlock()

	// 计算 SHA256
	h := sha256.Sum256(data)
	checksum := hex.EncodeToString(h[:])

	// 保存文件
	filePath := fmt.Sprintf("./data/upgrades/%s", version)
	if err := os.MkdirAll("./data/upgrades", 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	// 写 DB 记录
	id := uuid.New().String()
	ctx, cancel := contextTimeout(3 * time.Second)
	defer cancel()
	_, err := um.db.ExecContext(ctx,
		`INSERT INTO agent_upgrades(id, version, file_path, sha256, size_bytes, created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		id, version, filePath, checksum, len(data), time.Now())
	if err != nil {
		return nil, fmt.Errorf("insert upgrade: %w", err)
	}

	um.activeVersion = version
	um.activeSHA256 = checksum
	um.filePath = filePath

	slog.Info("upgrade uploaded", "version", version, "sha256", checksum, "size", len(data))
	return &UpgradeInfo{
		Version:     version,
		DownloadURL: filePath,
		SHA256:      checksum,
		Force:       false,
	}, nil
}

// StartRollout 启动滚动升级。
func (um *UpgradeManager) StartRollout(version string, batchSize int, batchInterval time.Duration) error {
	um.mu.Lock()
	defer um.mu.Unlock()

	if um.activeVersion != version {
		return fmt.Errorf("version %s not uploaded", version)
	}
	if batchSize <= 0 {
		batchSize = 10
	}
	if batchInterval <= 0 {
		batchInterval = 60 * time.Second
	}

	um.strategy = RolloutStrategy{
		BatchSize:     batchSize,
		BatchInterval: batchInterval,
		StartedAt:     time.Now(),
	}

	slog.Info("upgrade rollout started", "version", version, "batch_size", batchSize, "batch_interval", batchInterval)
	return nil
}

// CheckEligibility 检查 Agent 是否满足升级条件。
// 由 handleHeartbeat 在收到心跳后调用。
func (um *UpgradeManager) CheckEligibility(agentID string) *UpgradeInfo {
	um.mu.RLock()
	defer um.mu.RUnlock()

	if um.activeVersion == "" {
		return nil
	}

	// 检查该 Agent 的升级进度
	prog, exists := um.progress[agentID]
	if !exists {
		// 首次检查：分配升级
		um.progress[agentID] = &UpgradeProgress{
			AgentID:   agentID,
			UpgradeID: uuid.New().String(),
			Status:    "downloading",
			Version:   um.activeVersion,
			StartedAt: time.Now(),
		}
		return &UpgradeInfo{
			Version:     um.activeVersion,
			DownloadURL: um.filePath,
			SHA256:      um.activeSHA256,
			Force:       false,
		}
	}

	if prog.Status == "completed" || prog.Status == "failed" {
		return nil
	}

	// still downloading — don't retry
	return nil
}

// RecordUpgradeProgress 记录升级进度变更。
func (um *UpgradeManager) RecordUpgradeProgress(agentID, status, errMsg string) {
	um.mu.Lock()
	defer um.mu.Unlock()

	prog, exists := um.progress[agentID]
	if !exists {
		return
	}
	prog.Status = status
	if status == "completed" || status == "failed" {
		now := time.Now()
		prog.CompletedAt = &now
		prog.Error = errMsg
	}
}

// GetProgress 获取升级进度汇总。
func (um *UpgradeManager) GetProgress() map[string]int {
	um.mu.RLock()
	defer um.mu.RUnlock()

	summary := map[string]int{"total": 0, "pending": 0, "downloading": 0, "completed": 0, "failed": 0}
	summary["total"] = len(um.progress)
	for _, p := range um.progress {
		summary[p.Status]++
	}
	return summary
}
