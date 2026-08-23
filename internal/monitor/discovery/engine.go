package discovery

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/monitor/model"
	"github.com/redis/go-redis/v9"
)

type EventHandler func(ctx context.Context, event ChangeEvent)

type ChangeEvent struct {
	EventType string `json:"event_type"`
	CIID      string `json:"ci_id"`
	TenantID  string `json:"tenant_id"`
	Payload   string `json:"payload"`
}

type Engine struct {
	mu       sync.RWMutex
	targets  map[string]*model.MonitorTarget
	handlers []EventHandler
	rdb      *redis.Client
	db       *sql.DB // 启动全量重建发现目标用（CMDB 权威源）
}

func NewEngine(rdb *redis.Client, db *sql.DB) *Engine {
	return &Engine{
		targets: make(map[string]*model.MonitorTarget),
		rdb:     rdb,
		db:      db,
	}
}

func (e *Engine) Start(ctx context.Context) error {
	slog.Info("discovery engine started")

	if e.db != nil {
		// 启动从 CMDB 全量重建发现目标（重启恢复，避免仅靠事件增量积累）
		if err := e.RebuildFromDB(ctx); err != nil {
			slog.Warn("discovery: rebuild targets from db failed", "error", err)
		}
	}

	if e.rdb != nil {
		go e.subscribeRedisStream(ctx)
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("discovery engine stopped")
			return nil
		case <-ticker.C:
			e.processPollCycle(ctx)
		}
	}
}

// RebuildFromDB 从 cmdb_ci 全量加载有 address/ip 的 CI 重建探测目标（启动恢复）。
// 与 OnChange 的 ci_created/updated 增量路径共用同一构建逻辑，幂等（重建前清空）。
func (e *Engine) RebuildFromDB(ctx context.Context) error {
	rows, err := e.db.QueryContext(ctx, `
		SELECT id, tenant_id, type_id, name, attributes
		FROM cmdb_ci
		WHERE attributes ? 'address' OR attributes ? 'ip'`)
	if err != nil {
		return err
	}
	defer rows.Close()

	e.mu.Lock()
	defer e.mu.Unlock()
	// 重建前清空旧内存态（避免事件残留与 DB 不一致）
	e.targets = make(map[string]*model.MonitorTarget)
	added := 0
	for rows.Next() {
		var id, tenantID, typeID, name string
		var attrsJSON []byte
		if err := rows.Scan(&id, &tenantID, &typeID, &name, &attrsJSON); err != nil {
			slog.Warn("discovery: scan ci row failed", "error", err)
			continue
		}
		attrs := map[string]any{}
		_ = json.Unmarshal(attrsJSON, &attrs)
		address := firstString(attrs, "address", "ip")
		if address == "" {
			continue
		}
		target := &model.MonitorTarget{
			ID:        uuid.New().String(),
			TenantID:  tenantID,
			CIID:      id,
			Name:      name,
			Address:   address,
			Probes:    e.guessProbes(typeID),
			Interval:  60 * time.Second,
			Timeout:   5 * time.Second,
			Status:    model.TargetUnknown,
			CreatedAt: time.Now(),
		}
		e.targets[target.ID] = target
		added++
	}
	slog.Info("discovery: targets rebuilt from db", "count", added)
	return rows.Err()
}

func firstString(attrs map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := attrs[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func (e *Engine) subscribeRedisStream(ctx context.Context) {
	const stream = "cmdb:changes"
	const group = "monitor:discovery"
	const consumer = "core-1"

	err := e.rdb.XGroupCreate(ctx, stream, group, "0").Err()
	if err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			slog.Error("failed to create consumer group, falling back to poll", "error", err)
			return
		}
	}
	slog.Info("subscribed to cmdb change stream", "stream", stream, "group", group)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			result, err := e.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group:    group,
				Consumer: consumer,
				Streams:  []string{stream, ">"},
				Count:    10,
				Block:    2 * time.Second,
				NoAck:    false,
			}).Result()
			if err != nil {
				if err.Error() == "redis: nil" {
					continue
				}
				slog.Warn("redis stream read error", "error", err)
				time.Sleep(time.Second)
				continue
			}

			for _, xstream := range result {
				for _, msg := range xstream.Messages {
					payload, ok := msg.Values["payload"].(string)
					if !ok {
						payloadBytes, _ := json.Marshal(msg.Values)
						payload = string(payloadBytes)
					}

					eventType, _ := msg.Values["event_type"].(string)
					ciID, _ := msg.Values["ci_id"].(string)
					tenantID, _ := msg.Values["tenant_id"].(string)

					event := ChangeEvent{
						EventType: eventType,
						CIID:      ciID,
						TenantID:  tenantID,
						Payload:   payload,
					}

					e.OnChange(event)

					e.rdb.XAck(ctx, stream, group, msg.ID)
				}
			}
		}
	}
}

func (e *Engine) processPollCycle(ctx context.Context) {
	e.mu.RLock()
	count := len(e.targets)
	e.mu.RUnlock()
	slog.Debug("discovery engine poll cycle", "active_targets", count)
}

func (e *Engine) OnChange(event ChangeEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch event.EventType {
	case "ci_created", "ci.created", "ci_updated", "ci.updated":
		var ciData struct {
			ID      string            `json:"id"`
			TypeID  string            `json:"type_id"`
			Name    string            `json:"name"`
			Address string            `json:"address"`
			Labels  map[string]string `json:"labels"`
		}
		if err := json.Unmarshal([]byte(event.Payload), &ciData); err != nil {
			slog.Error("failed to parse ci event payload", "error", err)
			return
		}

		target := &model.MonitorTarget{
			ID:        uuid.New().String(),
			TenantID:  event.TenantID,
			CIID:      event.CIID,
			Name:      ciData.Name,
			Address:   ciData.Address,
			Probes:    e.guessProbes(ciData.TypeID),
			Interval:  60 * time.Second,
			Timeout:   5 * time.Second,
			Labels:    ciData.Labels,
			Status:    model.TargetUnknown,
			CreatedAt: time.Now(),
		}
		e.targets[target.ID] = target
		slog.Info("discovery: target added from stream", "name", target.Name, "address", target.Address)

	case "ci_deleted", "ci.deleted":
		for _, t := range e.targets {
			if t.CIID == event.CIID {
				delete(e.targets, t.ID)
				slog.Info("discovery: target removed from stream", "name", t.Name)
			}
		}
	}

	for _, h := range e.handlers {
		h(context.Background(), event)
	}
}

func (e *Engine) Subscribe(handler EventHandler) {
	e.handlers = append(e.handlers, handler)
}

func (e *Engine) GetTargets() []*model.MonitorTarget {
	e.mu.RLock()
	defer e.mu.RUnlock()

	targets := make([]*model.MonitorTarget, 0, len(e.targets))
	for _, t := range e.targets {
		targets = append(targets, t)
	}
	return targets
}

func (e *Engine) GetTarget(id string) *model.MonitorTarget {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.targets[id]
}

func (e *Engine) guessProbes(ciTypeID string) []model.ProbeProtocol {
	switch ciTypeID {
	case "server", "host", "vm":
		return []model.ProbeProtocol{model.ProbeICMP, model.ProbeTCP}
	case "switch", "router", "firewall":
		return []model.ProbeProtocol{model.ProbeICMP, model.ProbeTCP}
	case "loadbalancer":
		return []model.ProbeProtocol{model.ProbeHTTP, model.ProbeTCP}
	case "database":
		return []model.ProbeProtocol{model.ProbeTCP, model.ProbeSSL}
	case "web-server":
		return []model.ProbeProtocol{model.ProbeHTTP, model.ProbeSSL}
	case "dns-server":
		return []model.ProbeProtocol{model.ProbeDNS, model.ProbeICMP}
	default:
		return []model.ProbeProtocol{model.ProbeICMP}
	}
}
