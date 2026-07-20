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
	ID      string
	Conn    *tunnel.Conn
	ConnectedAt time.Time
}

type AgentRegistry struct {
	mu      sync.RWMutex
	agents  map[string]*AgentInfo
}

func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{
		agents: make(map[string]*AgentInfo),
	}
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

	a.Conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	msg, err := a.Conn.Receive()
	if err != nil {
		return nil, fmt.Errorf("receive agent response: %w", err)
	}

	var resp tunnel.AgentResponse
	if err := json.Unmarshal(msg.Payload, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal agent response: %w", err)
	}

	return &resp, nil
}

func mustRaw(data []byte, err error) json.RawMessage {
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(data)
}
