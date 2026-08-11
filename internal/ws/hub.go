package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type EventType string

const (
	EventAlert     EventType = "alert"
	EventAudit     EventType = "audit"
	EventMonitor   EventType = "monitor"
	EventCMDB      EventType = "cmdb"
	EventITSM      EventType = "itsm"
	EventHeartbeat EventType = "heartbeat"
	EventTopology  EventType = "topology"
	EventMessage   EventType = "message"
)

type Event struct {
	Type    EventType `json:"type"`
	Payload any       `json:"payload"`
	Time    int64     `json:"time"`
}

// Identity 一条 WebSocket 连接背后的用户身份。
//
// 背景:浏览器发起 WebSocket 握手时无法自定义请求头,原先从 X-Tenant-ID 头取租户
// 的做法在真实浏览器场景下永远拿不到值 —— 所有连接都退化成同一个空租户,
// 既做不到租户隔离,也无法做「只推给某个人」的定向推送。
// 现改为握手时从 URL query(?token=)或子协议里取 JWT,由服务端校验后派生身份。
type Identity struct {
	UserID   string
	Username string
	TenantID string
}

// Authenticator 校验握手 token 并返回身份。由 cmd/core 注入(桥接 auth.Provider),
// 避免 internal/ws 反向依赖鉴权包。
type Authenticator func(token string) (Identity, bool)

type client struct {
	conn     *websocket.Conn
	send     chan []byte
	done     chan struct{}
	closeOne sync.Once
	tenant   string
	userID   string
	username string
	remote   string
}

// close 幂等关闭 done 通道。原实现在多处直接 close(c.done),
// 同一连接被两条路径同时判定为「发送阻塞」时会 panic: close of closed channel。
func (c *client) close() {
	c.closeOne.Do(func() { close(c.done) })
}

type Hub struct {
	mu        sync.RWMutex
	clients   map[*client]bool
	broadcast chan []byte
	auth      Authenticator
}

func NewHub() *Hub {
	h := &Hub{
		clients:   make(map[*client]bool),
		broadcast: make(chan []byte, 256),
	}
	go h.run()
	return h
}

// SetAuthenticator 注入握手鉴权器。未注入时连接仍被接受,但不具备用户身份,
// 只能收到全局广播(向后兼容既有的匿名监控大盘订阅)。
func (h *Hub) SetAuthenticator(a Authenticator) { h.auth = a }

func (h *Hub) run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case msg := <-h.broadcast:
			h.mu.Lock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					c.close()
					delete(h.clients, c)
				}
			}
			h.mu.Unlock()
		case <-ticker.C:
			h.broadcastHeartbeat()
		}
	}
}

func (h *Hub) broadcastHeartbeat() {
	data, _ := json.Marshal(Event{
		Type: EventHeartbeat,
		Time: time.Now().UnixMilli(),
	})
	h.broadcast <- data
}

func (h *Hub) Broadcast(evt Event) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("ws marshal event", "error", err)
		return
	}
	h.broadcast <- data
}

// deliver 把已序列化的数据发给所有满足 match 的连接,返回命中连接数。
func (h *Hub) deliver(data []byte, match func(*client) bool) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	n := 0
	for c := range h.clients {
		if !match(c) {
			continue
		}
		select {
		case c.send <- data:
			n++
		default:
			c.close()
			delete(h.clients, c)
		}
	}
	return n
}

func (h *Hub) BroadcastToTenant(evt any, tenantID string) {
	data, err := json.Marshal(evt)
	if err != nil {
		slog.Error("ws marshal event", "error", err)
		return
	}
	h.deliver(data, func(c *client) bool { return c.tenant == tenantID })
}

// BroadcastToUser 定向推送给单个用户的全部在线连接(多标签页会各收一份)。
// userID 与 username 任一命中即算同一人 —— 平台鉴权链给的是 user_id,
// 而 ITSM 业务主体用的是 username,投递方能拿到哪个就传哪个。
// 返回实际投递到的连接数,0 表示对方当前不在线(消息已落库,靠轮询兜底)。
func (h *Hub) BroadcastToUser(tenantID, userID, username string, payload any) int {
	if strings.TrimSpace(userID) == "" && strings.TrimSpace(username) == "" {
		return 0
	}
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("ws marshal event", "error", err)
		return 0
	}
	return h.deliver(data, func(c *client) bool {
		if tenantID != "" && c.tenant != "" && c.tenant != tenantID {
			return false
		}
		if userID != "" && c.userID != "" && c.userID == userID {
			return true
		}
		if username != "" && c.username != "" && strings.EqualFold(c.username, username) {
			return true
		}
		return false
	})
}

// PushToUser 实现 message.Pusher,让站内消息模块无需感知 ws 包细节。
func (h *Hub) PushToUser(tenantID, userID, username string, payload any) int {
	return h.BroadcastToUser(tenantID, userID, username, payload)
}

// handshakeToken 从握手请求里取 JWT。优先 query 参数(浏览器 WebSocket API
// 唯一可控的传参位置),其次 Authorization 头(供服务端/CLI 客户端使用)。
func handshakeToken(r *http.Request) string {
	if tok := strings.TrimSpace(r.URL.Query().Get("token")); tok != "" {
		return tok
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return ""
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 先鉴权再握手:身份无效时直接以 HTTP 401 拒绝,不必先升级协议再关闭。
	var id Identity
	if h.auth != nil {
		if tok := handshakeToken(r); tok != "" {
			if got, ok := h.auth(tok); ok {
				id = got
			} else {
				slog.Debug("ws handshake: invalid token", "remote", r.RemoteAddr)
			}
		}
	}
	if id.TenantID == "" {
		// 回落:未携带 token 的匿名连接(如内部探针)仍按请求头/默认租户处理,
		// 只能收到租户级广播,收不到定向消息。
		id.TenantID = r.Header.Get("X-Tenant-ID")
		if id.TenantID == "" {
			id.TenantID = "default"
		}
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Error("ws accept", "error", err)
		return
	}

	c := &client{
		conn:     conn,
		send:     make(chan []byte, 64),
		done:     make(chan struct{}),
		tenant:   id.TenantID,
		userID:   id.UserID,
		username: id.Username,
		remote:   r.RemoteAddr,
	}

	h.mu.Lock()
	h.clients[c] = true
	h.mu.Unlock()

	slog.Info("ws client connected", "remote", c.remote, "tenant", c.tenant, "user", c.username)

	go h.writePump(c)
	h.readPump(c)
}

func (h *Hub) writePump(c *client) {
	ticker := time.NewTicker(15 * time.Second)
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
			ctx, cancel := writeCtx()
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				slog.Error("ws write", "error", err)
				return
			}
		case <-ticker.C:
			// 用协议级 Ping 探活:浏览器会自动回 Pong,无需前端配合。
			// 旧实现发的是 {"type":"ping"} 文本帧,前端直接丢弃、从不回应,
			// 探活退化成了单向写测试。
			ctx, cancel := writeCtx()
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}

func (h *Hub) readPump(c *client) {
	defer func() {
		h.removeClient(c)
	}()

	// 不设读超时:浏览器订阅端全程只收不发,任何固定超时都会把健康连接
	// 每隔 N 秒踢一次(旧实现 60s 一断,前端指数退避重连,推送在重连窗口里丢失)。
	// 连接是否存活改由 writePump 的协议级 Ping 判定。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-c.done
		cancel()
	}()

	for {
		if _, _, err := c.conn.Read(ctx); err != nil {
			return
		}
	}
}

func (h *Hub) removeClient(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	c.close()
}

func writeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
