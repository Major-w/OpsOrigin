package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Reporter 把采集结果上报 Core（与主机 Agent 的 HTTP 上报范式一致）。
type Reporter struct {
	server string
	token  string
	client *http.Client
}

// NewReporter 构造上报器。
func NewReporter(server, token string) *Reporter {
	return &Reporter{
		server: server,
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Report 上报一次采集结果。
// agentID 为空时视为首次注册，返回 Core 分配的 agent_id。
func (r *Reporter) Report(ctx context.Context, agentID string, rep *K8sReport) (string, error) {
	rep.AgentID = agentID
	body, err := json.Marshal(rep)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/api/v1/k8s/report", r.server)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("report failed: HTTP %d", resp.StatusCode)
	}
	if agentID == "" {
		var out struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err == nil {
			return out.AgentID, nil
		}
	}
	return agentID, nil
}
