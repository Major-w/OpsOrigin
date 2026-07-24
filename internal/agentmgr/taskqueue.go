package agentmgr

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskQueue 基于 Go channel + PG 补偿的任务下发队列。
type TaskQueue struct {
	ch      chan string
	workers int
	db      *sql.DB
	store   Store

	mu     sync.RWMutex
	closed bool
	wg     sync.WaitGroup
}

// NewTaskQueue 创建任务队列。
func NewTaskQueue(db *sql.DB, store Store, queueSize, workers int) *TaskQueue {
	if queueSize <= 0 {
		queueSize = 1000
	}
	if workers <= 0 {
		workers = 4
	}
	return &TaskQueue{
		ch:      make(chan string, queueSize),
		workers: workers,
		db:      db,
		store:   store,
	}
}

// Push 非阻塞推送 taskID 到 channel。channel 满时直接返回（依赖 recoverLoop 补偿）。
func (tq *TaskQueue) Push(taskID string) {
	tq.mu.RLock()
	if tq.closed {
		tq.mu.RUnlock()
		return
	}
	tq.mu.RUnlock()

	select {
	case tq.ch <- taskID:
	default:
		// channel 满，recoverLoop 会补推
	}
}

// Start 启动 worker goroutine 和 recovery goroutine。
func (tq *TaskQueue) Start(ctx context.Context) {
	for i := 0; i < tq.workers; i++ {
		tq.wg.Add(1)
		go tq.workerLoop(ctx, i)
	}
	tq.wg.Add(1)
	go tq.recoverLoop(ctx)
	slog.Info("task queue started", "workers", tq.workers, "queue_size", cap(tq.ch))
}

// Shutdown 优雅关闭：close channel → drain → 标记未分发任务为 pending。
func (tq *TaskQueue) Shutdown() {
	tq.mu.Lock()
	if tq.closed {
		tq.mu.Unlock()
		return
	}
	tq.closed = true
	close(tq.ch)
	tq.mu.Unlock()

	tq.wg.Wait()
	slog.Info("task queue stopped")
}

// workerLoop 从 channel 读取 taskID → 查 PG → 执行 → 更新状态。
func (tq *TaskQueue) workerLoop(ctx context.Context, id int) {
	defer tq.wg.Done()
	for taskID := range tq.ch {
		tq.processTask(ctx, id, taskID)
	}
}

func (tq *TaskQueue) processTask(ctx context.Context, id int, taskID string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("task worker panic recovered", "worker", id, "task", taskID, "panic", r)
		}
	}()

	// 查询任务详情
	rows, err := tq.db.QueryContext(ctx,
		`SELECT id, agent_id, action, script, params, timeout, run_as,
		        file_id, file_url, file_path, expected_checksum, checksum_algo
		 FROM agent_tasks WHERE id=$1 AND status='pending'`,
		taskID)
	if err != nil {
		slog.Warn("task worker query failed", "task", taskID, "error", err)
		return
	}
	defer rows.Close()

	if !rows.Next() {
		return // 任务已被处理
	}

	var ti struct {
		id, agentID, action, script        string
		params                             []byte
		timeout                            sql.NullInt64
		runAs                              sql.NullString
		fileID, fileURL, filePath          sql.NullString
		expectedChecksum, checksumAlgo     sql.NullString
	}
	if err := rows.Scan(&ti.id, &ti.agentID, &ti.action, &ti.script, &ti.params, &ti.timeout, &ti.runAs,
		&ti.fileID, &ti.fileURL, &ti.filePath, &ti.expectedChecksum, &ti.checksumAlgo); err != nil {
		slog.Warn("task worker scan failed", "task", taskID, "error", err)
		return
	}

	// 标记为 running
	tq.db.ExecContext(ctx, `UPDATE agent_tasks SET status='running', updated_at=$2 WHERE id=$1`, taskID, time.Now())

	// 执行（这里只做标记，实际执行由 agentmgr service 负责）
	// 注意：具体执行逻辑在 service 层
	slog.Debug("task worker processed", "worker", id, "task", taskID)
}

// recoverLoop 每 5s 扫描 agent_tasks 中 status='pending' 的任务补推。
func (tq *TaskQueue) recoverLoop(ctx context.Context) {
	defer tq.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tq.recoverPending(ctx)
		}
	}
}

func (tq *TaskQueue) recoverPending(ctx context.Context) {
	rows, err := tq.db.QueryContext(ctx,
		`SELECT id FROM agent_tasks WHERE status='pending' ORDER BY created_at LIMIT $1`, cap(tq.ch))
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		tq.Push(id)
	}
}

// BulkCreate 批量创建任务并 Push 入队。
func (tq *TaskQueue) BulkCreate(ctx context.Context, tasks []TaskRequest) (int, error) {
	if len(tasks) == 0 {
		return 0, nil
	}

	now := time.Now()
	tx, err := tq.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	created := 0
	for _, req := range tasks {
		if req.TaskID == "" {
			req.TaskID = uuid.New().String()
		}
		tenantID := "default"
		_, err := tx.ExecContext(ctx,
			`INSERT INTO agent_tasks(id,tenant_id,agent_id,action,script,params,timeout,run_as,
			                        file_id,file_url,file_path,expected_checksum,checksum_algo,
			                        status,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(id) DO NOTHING`,
			req.TaskID, tenantID, "", req.Action, req.Script, toJSON(req.Params), req.Timeout, req.RunAs,
			req.FileID, req.FileURL, req.FilePath, req.ExpectedChecksum, req.ChecksumAlgo,
			string(TaskStatusPending), now, now)
		if err != nil {
			slog.Warn("task bulk create insert failed", "task", req.TaskID, "error", err)
			continue
		}
		tq.Push(req.TaskID)
		created++
	}

	if err := tx.Commit(); err != nil {
		return created, err
	}

	return created, nil
}
