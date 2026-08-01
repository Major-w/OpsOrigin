package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

type AgentClient struct {
	proxyAddr string
	agentID   string
	conn      *Conn
	mu        sync.Mutex
	connected bool
	onAction  func(action string, payload []byte) ([]byte, error)
}

func NewAgentClient(proxyAddr, agentID string) *AgentClient {
	return &AgentClient{
		proxyAddr: proxyAddr,
		agentID:   agentID,
	}
}

func (a *AgentClient) OnAction(handler func(action string, payload []byte) ([]byte, error)) {
	a.onAction = handler
}

// SendMetrics 通过 Proxy 中转上报指标/资产批量(MsgMetrics)。
// 未连接或发送失败时返回错误: monitor QoS 丢弃(不缓存), cmdb 由上层补传。
func (a *AgentClient) SendMetrics(taskID, taskType string, ts time.Time, intervalSec int64, points []TunnelMetricPoint) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.conn == nil {
		return fmt.Errorf("not connected to proxy")
	}
	msgData, err := MarshalMessage(MsgMetrics, MetricsMessage{
		TaskID:      taskID,
		TaskType:    taskType,
		Timestamp:   ts.UnixMilli(),
		IntervalSec: intervalSec,
		Points:      points,
	}, a.agentID, "")
	if err != nil {
		return fmt.Errorf("marshal metrics: %w", err)
	}
	if err := a.conn.SendRaw(msgData); err != nil {
		return fmt.Errorf("send metrics to proxy: %w", err)
	}
	return nil
}

func (a *AgentClient) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := a.connect(ctx); err != nil {
				slog.Warn("agent connect failed, retrying in 5s",
					"proxy", a.proxyAddr, "error", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(5 * time.Second):
				}
				continue
			}
			return a.handleSession(ctx)
		}
	}
}

func (a *AgentClient) connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	raw, err := net.DialTimeout("tcp", a.proxyAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial proxy %s: %w", a.proxyAddr, err)
	}

	a.conn = NewConn(raw)
	a.connected = true
	slog.Info("agent connected to proxy", "proxy", a.proxyAddr, "agent_id", a.agentID)
	return nil
}

func (a *AgentClient) handleSession(ctx context.Context) error {
	defer func() {
		a.mu.Lock()
		a.connected = false
		a.conn = nil
		a.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			a.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
			msg, err := a.conn.Receive()
			if err != nil {
				return fmt.Errorf("agent receive: %w", err)
			}

			switch msg.Type {
			case MsgAgentCommand:
				var cmd AgentCommand
				if err := json.Unmarshal(msg.Payload, &cmd); err != nil {
					slog.Error("invalid agent command", "error", err)
					continue
				}
				go a.executeCommand(cmd)
			}
		}
	}
}

func (a *AgentClient) executeCommand(cmd AgentCommand) {
	resp := AgentResponse{
		CommandID: cmd.CommandID,
		AgentID:   a.agentID,
		Success:   false,
		Error:     "no action handler",
	}

	if a.onAction != nil {
		output, err := a.onAction(cmd.Action, cmd.Payload)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Success = true
			resp.Output = output
		}
	}

	msgData, _ := MarshalMessage(MsgAgentResp, resp, "", "")
	a.conn.SendRaw(msgData)
}
