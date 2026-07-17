package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Callback struct {
	client *http.Client
}

type EventPayload struct {
	Event     string `json:"event"`
	TicketID  string `json:"ticket_id"`
	TenantID  string `json:"tenant_id"`
	Status    string `json:"status"`
	RiskLevel string `json:"risk_level"`
	ChangedBy string `json:"changed_by,omitempty"`
	Timestamp string `json:"timestamp"`
}

func New() *Callback {
	return &Callback{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Callback) Notify(ctx context.Context, url string, event string, ticketID, tenantID, status, riskLevel, changedBy string) error {
	if url == "" {
		return nil
	}

	payload := EventPayload{
		Event:     event,
		TicketID:  ticketID,
		TenantID:  tenantID,
		Status:    status,
		RiskLevel: riskLevel,
		ChangedBy: changedBy,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	return c.deliverWithRetry(ctx, url, body, 3)
}

func (c *Callback) deliverWithRetry(ctx context.Context, url string, body []byte, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("create webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("webhook delivery failed, retrying", "url", url, "attempt", attempt+1, "error", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
		slog.Warn("webhook non-200 response, retrying", "url", url, "status", resp.StatusCode, "attempt", attempt+1)
	}

	return fmt.Errorf("webhook delivery failed after %d retries: %w", maxRetries, lastErr)
}
