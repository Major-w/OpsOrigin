package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/tunnel"
)

type AgentInfo struct {
	ID          string
	Conn        *tunnel.Conn
	ConnectedAt time.Time
	// pending 关联命令响应: CommandID -> chan *tunnel.AgentResponse。
	// 读循环收到 MsgAgentResp 后投递, 供 SendCommand 等待。
	pending sync.Map
}

type AgentRegistry struct {
	mu     sync.RWMutex
	agents map[string]*AgentInfo
	// onMetrics 收到 Agent 的 metrics 上报(MsgMetrics)时触发。
	// 由 Proxy 注入: 透传为 gRPC MetricBatch 转发 Core(纯转发, 不聚合)。
	onMetrics func(agentID string, msg *tunnel.MetricsMessage)
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]*AgentInfo),
	}
}

// OnMetrics 注入指标上报回调(纯转发路径)。
func (r *AgentRegistry) OnMetrics(handler func(agentID string, msg *tunnel.MetricsMessage)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onMetrics = handler
}

func (r *AgentRegistry) Serve(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("agent listen on %s: %w", addr, err)
	}
	defer listener.Close()

	slog.Info("agent registry listening for agents", "addr", addr)

	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	for {
		raw, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("agent accept error", "error", err)
				continue
			}
		}

		agentID := uuid.New().String()[:8]
		info := &AgentInfo{
			ID:          agentID,
			Conn:        tunnel.NewConn(raw),
			ConnectedAt: time.Now(),
		}

		r.mu.Lock()
		r.agents[agentID] = info
		r.mu.Unlock()

		slog.Info("agent connected", "agent_id", agentID, "remote", raw.RemoteAddr())

		// 读循环: Agent 主动上报(指标/资产)与响应均经此处理。
		go r.readLoop(ctx, info)
	}
}

// readLoop 持续读取 Agent 连接上的消息。
// 当前支持 MsgMetrics(指标/资产批量上报, 纯转发 Core); 其他消息忽略。
func (r *AgentRegistry) readLoop(ctx context.Context, info *AgentInfo) {
	defer func() {
		r.mu.Lock()
		if cur, ok := r.agents[info.ID]; ok && cur == info {
			delete(r.agents, info.ID)
		}
		r.mu.Unlock()
		_ = info.Conn.Close()
	}()

	for {
		info.Conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		msg, err := info.Conn.Receive()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				slog.Debug("agent read error", "agent_id", info.ID, "error", err)
				return
			}
		}

		switch msg.Type {
		case tunnel.MsgMetrics:
			var mm tunnel.MetricsMessage
			if err := json.Unmarshal(msg.Payload, &mm); err != nil {
				slog.Warn("proxy: invalid metrics payload", "agent_id", info.ID, "error", err)
				continue
			}
			r.mu.RLock()
			cb := r.onMetrics
			r.mu.RUnlock()
			if cb != nil {
				cb(info.ID, &mm)
			}
		case tunnel.MsgAgentResp:
			// 命令响应: 投递到 SendCommand 的等待 channel(读循环独占连接读取)。
			var resp tunnel.AgentResponse
			if err := json.Unmarshal(msg.Payload, &resp); err != nil {
				slog.Warn("proxy: invalid agent response", "agent_id", info.ID, "error", err)
				continue
			}
			if ch, ok := info.pending.Load(resp.CommandID); ok {
				select {
				case ch.(chan *tunnel.AgentResponse) <- &resp:
				default: // 调用方已超时, 丢弃
				}
			}
		default:
			// 其他消息忽略。
		}
	}
}

func (r *AgentRegistry) Get(id string) *AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[id]
}

func (r *AgentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

func (a *AgentInfo) SendCommand(ctx context.Context, action string, payload []byte) (*tunnel.AgentResponse, error) {
	cmd := tunnel.AgentCommand{
		CommandID: uuid.New().String(),
		AgentID:   a.ID,
		Action:    action,
		Payload:   payload,
	}

	msgData, _ := json.Marshal(tunnel.TunnelMessage{
		Type:    tunnel.MsgAgentCommand,
		Payload: mustRaw(json.Marshal(cmd)),
	})

	if err := a.Conn.SendRaw(msgData); err != nil {
		return nil, fmt.Errorf("send to agent: %w", err)
	}

	// 响应由读循环投递到 channel(读循环独占连接读取, 避免并发 Receive)。
	ch := make(chan *tunnel.AgentResponse, 1)
	a.pending.Store(cmd.CommandID, ch)
	defer a.pending.Delete(cmd.CommandID)

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("agent command timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func mustRaw(data []byte, err error) json.RawMessage {
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}
