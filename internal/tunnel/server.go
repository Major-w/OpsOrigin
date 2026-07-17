package tunnel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/google/uuid"
)

type Server struct {
	addr      string
	listener  net.Listener
	proxies   sync.Map
	sessions  sync.Map
	onCommand func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse
}

type ProxySession struct {
	ID         string
	ProxyID    string
	Conn       *Conn
	Hostname   string
	Capabilities []string
}

func NewServer(addr string) *Server {
	return &Server{addr: addr}
}

func (s *Server) OnCommand(handler func(ctx context.Context, proxyID string, cmd *CommandRequest) *CommandResponse) {
	s.onCommand = handler
}

func (s *Server) Start(ctx context.Context) error {
	var err error
	s.listener, err = net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("tunnel listen on %s: %w", s.addr, err)
	}

	slog.Info("tunnel server started", "addr", s.addr)

	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		raw, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("tunnel accept error", "error", err)
				continue
			}
		}
		go s.handleConnection(ctx, raw)
	}
}

func (s *Server) handleConnection(ctx context.Context, raw net.Conn) {
	conn := NewConn(raw)
	defer conn.Close()

	slog.Info("new tunnel connection", "remote", raw.RemoteAddr())

	// wait for register
	msg, err := conn.Receive()
	if err != nil {
		slog.Error("failed to receive register", "error", err)
		return
	}

	var reg RegisterRequest
	if err := json.Unmarshal(msg.Payload, &reg); err != nil {
		slog.Error("invalid register payload", "error", err)
		return
	}

	sessionID := uuid.New().String()
	session := &ProxySession{
		ID:           sessionID,
		ProxyID:      reg.ProxyID,
		Conn:         conn,
		Hostname:     reg.Hostname,
		Capabilities: reg.Capabilities,
	}

	s.proxies.Store(reg.ProxyID, session)
	s.sessions.Store(sessionID, session)

	resp := RegisterResponse{
		Accepted:  true,
		SessionID: sessionID,
	}
	respData, _ := MarshalMessage(MsgRegisterResp, resp, reg.ProxyID, msg.TraceID)
	conn.SendRaw(respData)

	slog.Info("proxy registered",
		"proxy_id", reg.ProxyID,
		"hostname", reg.Hostname,
		"session_id", sessionID,
		"capabilities", reg.Capabilities)

	// read loop for heartbeat, responses
	for {
		msg, err := conn.Receive()
		if err != nil {
			slog.Warn("tunnel read error, disconnecting proxy",
				"proxy_id", reg.ProxyID, "error", err)
			break
		}

		switch msg.Type {
		case MsgHeartbeat:
			var hb Heartbeat
			json.Unmarshal(msg.Payload, &hb)
			slog.Debug("heartbeat from proxy", "proxy_id", reg.ProxyID, "active", hb.ActiveCount)

		case MsgCommandResp:
			var cmdResp CommandResponse
			if err := json.Unmarshal(msg.Payload, &cmdResp); err != nil {
				slog.Error("invalid command response", "error", err)
				continue
			}
			slog.Info("command response received",
				"command_id", cmdResp.CommandID,
				"success", cmdResp.Success)
		}
	}

	s.proxies.Delete(reg.ProxyID)
	s.sessions.Delete(sessionID)
	slog.Info("proxy disconnected", "proxy_id", reg.ProxyID)
}

func (s *Server) SendCommand(proxyID string, cmd *CommandRequest) (*CommandResponse, error) {
	val, ok := s.proxies.Load(proxyID)
	if !ok {
		return nil, fmt.Errorf("proxy %s not connected", proxyID)
	}
	session := val.(*ProxySession)

	msgData, err := MarshalMessage(MsgCommand, cmd, proxyID, "")
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	if err := session.Conn.SendRaw(msgData); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	resp, err := session.Conn.Receive()
	if err != nil {
		return nil, fmt.Errorf("receive command response: %w", err)
	}

	var cmdResp CommandResponse
	if err := json.Unmarshal(resp.Payload, &cmdResp); err != nil {
		return nil, fmt.Errorf("unmarshal command response: %w", err)
	}

	return &cmdResp, nil
}

// raw send for internal use to avoid circular marshal
func (c *Conn) SendRaw(data []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := c.raw.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := c.raw.Write(data); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}
