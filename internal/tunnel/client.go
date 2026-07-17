package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	coreAddr   string
	proxyID    string
	hostname   string
	capabilities []string
	conn       *Conn
	mu         sync.Mutex
	connected  bool
	onCommand  func(cmd *AgentCommand) *AgentResponse
	pending    sync.Map
}

func NewClient(coreAddr, proxyID, hostname string, capabilities []string) *Client {
	return &Client{
		coreAddr:     coreAddr,
		proxyID:      proxyID,
		hostname:     hostname,
		capabilities: capabilities,
	}
}

func (c *Client) OnAgentCommand(handler func(cmd *AgentCommand) *AgentResponse) {
	c.onCommand = handler
}

func (c *Client) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := c.connect(ctx); err != nil {
				slog.Warn("tunnel connect failed, retrying in 5s",
					"core", c.coreAddr, "error", err)
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(5 * time.Second):
				}
				continue
			}
			return c.handleSession(ctx)
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	raw, err := net.DialTimeout("tcp", c.coreAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("dial core %s: %w", c.coreAddr, err)
	}

	c.conn = NewConn(raw)

	reg := RegisterRequest{
		ProxyID:      c.proxyID,
		Version:      "1.0.0",
		Hostname:     c.hostname,
		Capabilities: c.capabilities,
	}
	msgData, err := MarshalMessage(MsgRegister, reg, c.proxyID, "")
	if err != nil {
		return fmt.Errorf("marshal register: %w", err)
	}
	if err := c.conn.SendRaw(msgData); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	resp, err := c.conn.Receive()
	if err != nil {
		return fmt.Errorf("receive register response: %w", err)
	}

	var regResp RegisterResponse
	if err := json.Unmarshal(resp.Payload, &regResp); err != nil {
		return fmt.Errorf("unmarshal register response: %w", err)
	}

	if !regResp.Accepted {
		return fmt.Errorf("registration rejected: %s", regResp.Error)
	}

	c.connected = true
	slog.Info("tunnel connected to core",
		"session_id", regResp.SessionID,
		"core", c.coreAddr)
	return nil
}

func (c *Client) handleSession(ctx context.Context) error {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.conn = nil
		c.mu.Unlock()
	}()

	hbTicker := time.NewTicker(15 * time.Second)
	defer hbTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hbTicker.C:
			c.sendHeartbeat()
		default:
			c.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
			msg, err := c.conn.Receive()
			if err != nil {
				return fmt.Errorf("tunnel receive: %w", err)
			}

			switch msg.Type {
			case MsgHeartbeat:
				continue

			case MsgCommand:
				var cmd CommandRequest
				if err := json.Unmarshal(msg.Payload, &cmd); err != nil {
					slog.Error("invalid command payload", "error", err)
					continue
				}

				go c.handleCommand(cmd)

			case MsgAgentResp:
				var agentResp AgentResponse
				if err := json.Unmarshal(msg.Payload, &agentResp); err != nil {
					slog.Error("invalid agent response", "error", err)
					continue
				}
				if ch, ok := c.pending.Load(agentResp.CommandID); ok {
					ch.(chan *AgentResponse) <- &agentResp
				}
			}
		}
	}
}

func (c *Client) handleCommand(cmd CommandRequest) {
	resp := &CommandResponse{
		CommandID: cmd.CommandID,
		Success:   false,
		Error:     "no handler registered",
	}

	agentCmd := &AgentCommand{
		CommandID: cmd.CommandID,
		AgentID:   cmd.TargetID,
		Action:    cmd.Action,
		Payload:   cmd.Payload,
	}

	if c.onCommand != nil {
		agentResp := c.onCommand(agentCmd)
		resp.Success = agentResp.Success
		resp.Output = agentResp.Output
		resp.Error = agentResp.Error
	}

	msgData, _ := MarshalMessage(MsgCommandResp, resp, c.proxyID, "")
	c.conn.SendRaw(msgData)
}

func (c *Client) sendHeartbeat() {
	hb := Heartbeat{
		Timestamp: time.Now().UnixMilli(),
	}
	msgData, _ := MarshalMessage(MsgHeartbeat, hb, c.proxyID, "")
	c.conn.SendRaw(msgData)
}

func (c *Client) SendToAgent(ctx context.Context, agentID, action string, payload []byte) (*AgentResponse, error) {
	cmd := AgentCommand{
		CommandID: uuid.New().String(),
		AgentID:   agentID,
		Action:    action,
		Payload:   payload,
	}

	ch := make(chan *AgentResponse, 1)
	c.pending.Store(cmd.CommandID, ch)
	defer c.pending.Delete(cmd.CommandID)

	msgData, _ := MarshalMessage(MsgAgentCommand, cmd, c.proxyID, "")
	if err := c.conn.SendRaw(msgData); err != nil {
		return nil, fmt.Errorf("send agent command: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("agent command timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Conn) SetReadDeadline(t time.Time) {
	c.raw.SetReadDeadline(t)
}
