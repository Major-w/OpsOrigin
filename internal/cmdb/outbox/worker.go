package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/pkg/config"
)

type Worker struct {
	cfg    config.OutboxConfig
	pgRepo *pg.Repository
	dg     *dgraph.Client
}

func NewWorker(cfg config.OutboxConfig, pgRepo *pg.Repository, dg *dgraph.Client) *Worker {
	return &Worker{
		cfg:    cfg,
		pgRepo: pgRepo,
		dg:     dg,
	}
}

func (w *Worker) Start(ctx context.Context) error {
	slog.Info("outbox worker started",
		"poll_interval", w.cfg.PollInterval,
		"batch_size", w.cfg.BatchSize)

	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox worker stopped")
			return nil
		case <-time.After(w.cfg.PollInterval):
			if err := w.processBatch(ctx); err != nil {
				slog.Error("outbox batch processing failed", "error", err)
			}
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) error {
	records, err := w.pgRepo.GetUnprocessedOutbox(ctx, w.cfg.BatchSize)
	if err != nil {
		return fmt.Errorf("get unprocessed outbox: %w", err)
	}

	for _, rec := range records {
		if err := w.syncRecord(ctx, rec); err != nil {
			slog.Error("failed to sync outbox record",
				"id", rec.ID,
				"event", rec.EventType,
				"entity", rec.EntityID,
				"error", err)
			continue
		}

		if err := w.pgRepo.MarkOutboxProcessed(ctx, rec.ID); err != nil {
			slog.Error("failed to mark outbox processed",
				"id", rec.ID, "error", err)
		}
	}

	return nil
}

func (w *Worker) syncRecord(ctx context.Context, rec *model.OutboxRecord) error {
	switch rec.EventType {
	case model.EventCICreated, model.EventCIUpdated:
		var payload struct {
			CIID       string                   `json:"ci_id"`
			TypeID     string                   `json:"type_id"`
			Name       string                   `json:"name"`
			ChangeLog  []model.AttributeChange  `json:"change_log,omitempty"`
			Attributes map[string]any           `json:"attributes,omitempty"`
		}
		if err := json.Unmarshal([]byte(rec.Payload), &payload); err != nil {
			return fmt.Errorf("unmarshal ci payload: %w", err)
		}

		ci, err := w.pgRepo.GetCI(ctx, rec.EntityID, rec.TenantID)
		if err != nil {
			return fmt.Errorf("get ci %s: %w", rec.EntityID, err)
		}
		if ci == nil {
			slog.Warn("ci not found for outbox sync, skipping", "id", rec.EntityID)
			return nil
		}

		return w.dg.UpsertCI(ctx, ci)

	case model.EventCIDeleted:
		return w.dg.DeleteCI(ctx, rec.EntityID, rec.TenantID)

	case model.EventRelationCreated:
		var rel model.CIRelation
		if err := json.Unmarshal([]byte(rec.Payload), &rel); err != nil {
			return fmt.Errorf("unmarshal relation payload: %w", err)
		}
		return w.dg.UpsertRelation(ctx, &rel)

	case model.EventRelationDeleted:
		return w.dg.DeleteRelation(ctx, rec.EntityID, rec.TenantID)

	default:
		slog.Warn("unknown outbox event type", "event_type", rec.EventType)
	}

	return nil
}
