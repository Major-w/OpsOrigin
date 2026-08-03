package agentmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/redis/go-redis/v9"
)

// HeartbeatTTL is how long a heartbeat key lives in Redis without refresh.
// Agents heartbeat every 30s, so 90s gives 3 missed beats before expiry.
const HeartbeatTTL = 90 * time.Second

// HeartbeatSyncInterval is how often we batch-sync Redis heartbeat data to PG.
const HeartbeatSyncInterval = 60 * time.Second

// hbKey builds the Redis key for an agent heartbeat.
func hbKey(id string) string { return "agent:hb:" + id }

// HeartbeatWriter writes heartbeat data to Redis with TTL auto-expiry.
// Falls back to direct Store.Heartbeat when Redis is unavailable.
type HeartbeatWriter struct {
	rdb      *redis.Client
	store    Store                // fallback store for PG sync
	lastSync map[string]time.Time // track last PG sync time per agent
	mu       sync.Mutex
}

// NewHeartbeatWriter creates a heartbeat writer. If rdb is nil, heartbeats
// fall through to the Store directly (current behavior).
func NewHeartbeatWriter(rdb *redis.Client, store Store) *HeartbeatWriter {
	return &HeartbeatWriter{
		rdb:      rdb,
		store:    store,
		lastSync: make(map[string]time.Time),
	}
}

// Record writes agent heartbeat data to Redis with TTL.
// Falls back to store.Heartbeat when Redis is unavailable.
func (hw *HeartbeatWriter) Record(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error {
	// T13: 心跳指标打点
	metrics.AgentmgrHeartbeatTotal.WithLabelValues(id, tenantID).Inc()

	if hw.rdb == nil {
		return hw.store.Heartbeat(ctx, id, tenantID, req)
	}

	now := time.Now()
	fields := map[string]any{
		"id":             id,
		"tenant_id":      tenantID,
		"status":         "online",
		"last_seen":      now.Format(time.RFC3339),
		"last_heartbeat": now.Format(time.RFC3339),
	}
	if req.AgentVersion != "" {
		fields["version"] = req.AgentVersion
	}
	if req.OS != "" {
		fields["os"] = req.OS
	}
	if req.OSVersion != "" {
		fields["os_version"] = req.OSVersion
	}
	if req.Hostname != "" {
		fields["hostname"] = req.Hostname
	}
	if req.CPU != "" {
		fields["cpu"] = req.CPU
	}
	if req.Memory != "" {
		fields["memory"] = req.Memory
	}
	if len(req.IPs) > 0 {
		if b, err := json.Marshal(req.IPs); err == nil {
			fields["ips"] = string(b)
		}
	}
	if len(req.Capabilities) > 0 {
		if b, err := json.Marshal(req.Capabilities); err == nil {
			fields["capabilities"] = string(b)
		}
	}

	pipe := hw.rdb.Pipeline()
	key := hbKey(id)
	pipe.HSet(ctx, key, fields)
	pipe.Expire(ctx, key, HeartbeatTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		slog.Warn("heartbeat redis write failed, falling back to store", "error", err)
		return hw.store.Heartbeat(ctx, id, tenantID, req)
	}
	return nil
}

// Delete removes an agent's heartbeat key from Redis (used when the tunnel
// session disconnects, so the UI flips to offline immediately instead of
// waiting for the 90s TTL to expire).
func (hw *HeartbeatWriter) Delete(ctx context.Context, id string) {
	if hw.rdb == nil {
		return
	}
	if err := hw.rdb.Del(ctx, hbKey(id)).Err(); err != nil {
		slog.Warn("heartbeat redis delete failed", "agent_id", id, "error", err)
	}
	hw.mu.Lock()
	delete(hw.lastSync, id)
	hw.mu.Unlock()
}

// IsOnline checks if an agent is online via Redis. Returns (online, error).
// If Redis is unavailable, falls back to checking the store.
func (hw *HeartbeatWriter) IsOnline(ctx context.Context, id string) (bool, error) {
	if hw.rdb == nil {
		return false, nil // caller should check store
	}
	exists, err := hw.rdb.Exists(ctx, hbKey(id)).Result()
	if err != nil {
		return false, fmt.Errorf("redis exists: %w", err)
	}
	return exists > 0, nil
}

// GetStatus returns the status field from Redis heartbeat. Returns empty string if not found.
func (hw *HeartbeatWriter) GetStatus(ctx context.Context, id string) string {
	if hw.rdb == nil {
		return ""
	}
	s, err := hw.rdb.HGet(ctx, hbKey(id), "status").Result()
	if err != nil {
		return ""
	}
	return s
}

// StartSync begins a background goroutine that periodically flushes
// Redis heartbeat data to the PG store in batches.
func (hw *HeartbeatWriter) StartSync(ctx context.Context) {
	if hw.rdb == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(HeartbeatSyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hw.syncToStore(ctx)
			}
		}
	}()
	slog.Info("heartbeat sync worker started", "interval", HeartbeatSyncInterval)
}

// syncToStore scans Redis for heartbeat keys and batch-updates PG.
func (hw *HeartbeatWriter) syncToStore(ctx context.Context) {
	// SCAN for agent:hb:* keys
	var cursor uint64
	var keys []string
	for {
		var scanKeys []string
		var err error
		scanKeys, cursor, err = hw.rdb.Scan(ctx, cursor, "agent:hb:*", 500).Result()
		if err != nil {
			slog.Warn("heartbeat sync scan failed", "error", err)
			return
		}
		keys = append(keys, scanKeys...)
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return
	}

	// Batch GET all heartbeat data via pipeline
	type hbData struct {
		ID           string
		TenantID     string
		Version      string
		OS           string
		OSVer        string
		Hostname     string
		CPU          string
		Memory       string
		IPs          string
		Capabilities string
		LastSeen     time.Time
	}

	var batch []hbData
	pipe := hw.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(keys))
	for i, key := range keys {
		cmds[i] = pipe.HGetAll(ctx, key)
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		slog.Warn("heartbeat sync pipeline failed", "error", err)
		return
	}

	now := time.Now()
	for i, cmd := range cmds {
		m, err := cmd.Result()
		if err != nil || len(m) == 0 {
			continue
		}
		// Skip if we synced this agent recently (within SyncInterval)
		id := m["id"]
		if id == "" {
			// fallback: extract from key
			id = keys[i][len("agent:hb:"):]
		}
		hw.mu.Lock()
		lastSync := hw.lastSync[id]
		hw.mu.Unlock()
		if now.Sub(lastSync) < HeartbeatSyncInterval-time.Second {
			continue
		}

		ls, _ := time.Parse(time.RFC3339, m["last_seen"])
		batch = append(batch, hbData{
			ID:           id,
			TenantID:     m["tenant_id"],
			Version:      m["version"],
			OS:           m["os"],
			OSVer:        m["os_version"],
			Hostname:     m["hostname"],
			CPU:          m["cpu"],
			Memory:       m["memory"],
			IPs:          m["ips"],
			Capabilities: m["capabilities"],
			LastSeen:     ls,
		})
	}

	if len(batch) == 0 {
		return
	}

	// Sync to PG via bulk upsert
	hkBatch := make([]heartbeatBatch, 0, len(batch))
	for _, hb := range batch {
		hkBatch = append(hkBatch, heartbeatBatch{
			ID:            hb.ID,
			TenantID:      hb.TenantID,
			Version:       hb.Version,
			OS:            hb.OS,
			OSVersion:     hb.OSVer,
			Hostname:      hb.Hostname,
			CPU:           hb.CPU,
			Memory:        hb.Memory,
			IPs:           hb.IPs,
			Capabilities:  hb.Capabilities,
			LastSeen:      hb.LastSeen,
			LastHeartbeat: hb.LastSeen,
		})
	}

	if err := hw.store.BulkHeartbeat(ctx, hkBatch); err != nil {
		slog.Warn("heartbeat sync bulk upsert failed", "error", err, "count", len(hkBatch))
		return
	}

	for _, hb := range batch {
		hw.mu.Lock()
		hw.lastSync[hb.ID] = now
		hw.mu.Unlock()
	}

	slog.Debug("heartbeat sync batch done", "keys", len(keys), "synced", len(hkBatch))
}

// BulkOnlineStatus returns a map of agentID→online for batch status checks.
func (hw *HeartbeatWriter) BulkOnlineStatus(ctx context.Context, agentIDs []string) map[string]bool {
	if hw.rdb == nil || len(agentIDs) == 0 {
		return nil
	}
	pipe := hw.rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(agentIDs))
	for i, id := range agentIDs {
		cmds[i] = pipe.Exists(ctx, hbKey(id))
	}
	_, err := pipe.Exec(ctx)
	if err != nil {
		slog.Warn("bulk online check failed", "error", err)
		return nil
	}
	result := make(map[string]bool, len(agentIDs))
	for i, cmd := range cmds {
		result[agentIDs[i]] = cmd.Val() > 0
	}
	return result
}

// DumpHeartbeat exports agent heartbeat data as JSON (for testing).
func (hw *HeartbeatWriter) DumpHeartbeat(_ context.Context, _ string, _ *HeartbeatRequest) []byte {
	// For backwards compat: no-op exported method
	return nil
}
