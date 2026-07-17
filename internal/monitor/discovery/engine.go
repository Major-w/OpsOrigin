package discovery

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/opsorigin/core/internal/monitor/model"
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
}

func NewEngine(rdb *redis.Client) *Engine {
	return &Engine{
		targets: make(map[string]*model.MonitorTarget),
		rdb:     rdb,
	}
}

func (e *Engine) Start(ctx context.Context) error {
	slog.Info("discovery engine started")

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
	case "ci.created", "ci.updated":
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

	case "ci.deleted":
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


