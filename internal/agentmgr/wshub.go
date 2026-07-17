package agentmgr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// WSMessage 是 Agent WebSocket 通信的消息格式。
type WSMessage struct {
	Type    string         `json:"type"`              // task / task_result / ping / pong / register
	TaskID  string         `json:"task_id,omitempty"`  // 任务 ID
	Action  string         `json:"action,omitempty"`   // 任务动作
	Script  string         `json:"script,omitempty"`  // 脚本内容
	Params  map[string]any `json:"params,omitempty"`  // 脚本参数
	Timeout int            `json:"timeout,omitempty"` // 超时秒数
	Success bool           `json:"success,omitempty"` // 执行结果
	Output  string         `json:"output,omitempty"`  // 执行输出
	ExitCode int           `json:"exit_code,omitempty"` // 退出码
	Duration int64         `json:"duration_ms,omitempty"` // 执行时长
	Error   string         `json:"error,omitempty"`   // 错误信息
	AgentID string         `json:"agent_id,omitempty"` // Agent ID
	Time    int64          `json:"time"`              // 时间戳
}

// wsClient 表示一个 Agent 的 WebSocket 连接。
type wsClient struct {
	conn    *websocket.Conn
	send    chan []byte
	done    chan struct{}
	agentID string
	tenant  string
	remote  string
}

// AgentWSHub 维护 Agent WebSocket 连接池，支持通过长连接下发任务和接收结果。
type AgentWSHub struct {
	mu      sync.RWMutex
	clients map[string]*wsClient // agentID → client

	// 任务结果回调（由 Service 注册）
	onTaskResult func(agentID string, msg *WSMessage)
}

// NewAgentWSHub 创建 Agent WebSocket Hub。
func NewAgentWSHub() *AgentWSHub {
	return &AgentWSHub{
		clients: make(map[string]*wsClient),
	}
}

// SetTaskResultHandler 注册任务结果回调函数。
func (h *AgentWSHub) SetTaskResultHandler(fn func(agentID string, msg *WSMessage)) {
	h.mu.Lock()
	h.onTaskResult = fn
	h.mu.Unlock()
}

// IsOnline 检查指定 Agent 是否通过 WebSocket 在线。
func (h *AgentWSHub) IsOnline(agentID string) bool {
	h.mu.RLock()
	_, ok := h.clients[agentID]
	h.mu.RUnlock()
	return ok
}

// SendTask 通过 WebSocket 向指定 Agent 下发任务。
// 返回 false 表示 Agent 不在线（调用方应回退到 HTTP 轮询）。
func (h *AgentWSHub) SendTask(agentID string, msg *WSMessage) bool {
	h.mu.RLock()
	client, ok := h.clients[agentID]
	h.mu.RUnlock()
	if !ok {
		return false
	}

	msg.Type = "task"
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("ws marshal task", "error", err, "agent_id", agentID)
		return false
	}

	select {
	case client.send <- data:
		slog.Info("task dispatched via websocket", "agent_id", agentID, "task_id", msg.TaskID)
		return true
	case <-client.done:
		return false
	default:
		slog.Warn("ws send channel full, agent may be slow", "agent_id", agentID)
		return false
	}
}

// ServeHTTP 处理 Agent WebSocket 连接升级。
func (h *AgentWSHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if agentID == "" {
		http.Error(w, `{"error":"agent_id required"}`, http.StatusBadRequest)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Error("ws accept agent", "error", err, "agent_id", agentID)
		return
	}

	client := &wsClient{
		conn:    conn,
		send:    make(chan []byte, 64),
		done:    make(chan struct{}),
		agentID: agentID,
		tenant:  r.URL.Query().Get("tenant_id"),
		remote:  r.RemoteAddr,
	}

	h.mu.Lock()
	// 如果已有旧连接，关闭旧的
	if old, ok := h.clients[agentID]; ok {
		close(old.done)
	}
	h.clients[agentID] = client
	h.mu.Unlock()

	slog.Info("agent websocket connected", "agent_id", agentID, "remote", client.remote)

	go h.writePump(client)
	h.readPump(client)
}

// writePump 向 Agent 发送消息的 goroutine。
func (h *AgentWSHub) writePump(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close(websocket.StatusNormalClosure, "")
		h.removeClient(c)
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Error("ws write to agent", "error", err, "agent_id", c.agentID)
				return
			}
		case <-ticker.C:
			// 发送 ping
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			pingMsg, _ := json.Marshal(WSMessage{Type: "ping", Time: time.Now().UnixMilli()})
			err := c.conn.Write(ctx, websocket.MessageText, pingMsg)
			cancel()
			if err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

// readPump 读取 Agent 发来的消息（任务结果）的 goroutine。
func (h *AgentWSHub) readPump(c *wsClient) {
	defer h.removeClient(c)

	for {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		_, data, err := c.conn.Read(ctx)
		cancel()
		if err != nil {
			slog.Info("agent ws disconnected", "agent_id", c.agentID, "error", err)
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Warn("ws unmarshal agent message", "error", err, "agent_id", c.agentID)
			continue
		}

		switch msg.Type {
		case "task_result":
			// Agent 上报任务执行结果
			msg.AgentID = c.agentID
			h.mu.RLock()
			fn := h.onTaskResult
			h.mu.RUnlock()
			if fn != nil {
				fn(c.agentID, &msg)
			}
			slog.Info("task result received via websocket",
				"agent_id", c.agentID, "task_id", msg.TaskID,
				"success", msg.Success, "exit_code", msg.ExitCode)
		case "pong":
			// 心跳响应，忽略
		default:
			slog.Debug("ws unknown message", "type", msg.Type, "agent_id", c.agentID)
		}
	}
}

// removeClient 移除断开的 Agent 连接。
func (h *AgentWSHub) removeClient(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// 只有当前 client 还是同一个时才删除（避免删除新连接）
	if current, ok := h.clients[c.agentID]; ok && current == c {
		delete(h.clients, c.agentID)
		slog.Info("agent websocket removed", "agent_id", c.agentID)
	}
}

// OnlineAgentIDs 返回所有在线的 Agent ID 列表。
func (h *AgentWSHub) OnlineAgentIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]string, 0, len(h.clients))
	for id := range h.clients {
		ids = append(ids, id)
	}
	return ids
}
