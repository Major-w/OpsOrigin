package agentmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/tunnel"
	"golang.org/x/crypto/ssh"
)

// Store 定义 agent 管理服务的数据持久化接口。
type Store interface {
	// Agent CRUD
	Register(ctx context.Context, host *AgentHost) error
	Get(ctx context.Context, id, tenantID string) (*AgentHost, error)
	List(ctx context.Context, tenantID string) ([]*AgentHost, error)
	Update(ctx context.Context, host *AgentHost) error
	Delete(ctx context.Context, id, tenantID string) error
	Heartbeat(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error
	// Proxy CRUD
	RegisterProxy(ctx context.Context, proxy *ProxyInfo) error
	GetProxy(ctx context.Context, id, tenantID string) (*ProxyInfo, error)
	ListProxies(ctx context.Context, tenantID string) ([]*ProxyInfo, error)
	UpdateProxy(ctx context.Context, proxy *ProxyInfo) error
	DeleteProxy(ctx context.Context, id, tenantID string) error
	// 任务
	CreateTask(ctx context.Context, agentID string, req *TaskRequest) error
	ListTasks(ctx context.Context, agentID string) ([]*TaskInfo, error)
	CompleteTask(ctx context.Context, agentID, taskID string, resp *TaskResponse) error
	// 离线检测
	MarkOfflineAgents(ctx context.Context, tenantID string, threshold time.Duration) (int, error)
}

// MemoryStore 是基于内存的 Store 实现。
type MemoryStore struct {
	mu      sync.RWMutex
	hosts   map[string]*AgentHost
	proxies map[string]*ProxyInfo
	tasks   map[string][]*TaskInfo
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		hosts:   make(map[string]*AgentHost),
		proxies: make(map[string]*ProxyInfo),
		tasks:   make(map[string][]*TaskInfo),
	}
}

func (s *MemoryStore) Register(ctx context.Context, host *AgentHost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	host.ID = uuid.New().String()
	now := time.Now()
	host.FirstSeen = &now
	host.CreatedAt = now
	host.UpdatedAt = now
	if host.InstallMethod == "" {
		host.InstallMethod = InstallManual
	}
	if host.Type == AgentTypeAgentless {
		host.Port = 22
	}
	host.Status = AgentStatusUnknown
	s.hosts[host.ID] = host
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*AgentHost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.hosts[id]
	if !ok || h.TenantID != tenantID {
		return nil, nil
	}
	return h, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string) ([]*AgentHost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*AgentHost
	for _, h := range s.hosts {
		if h.TenantID == tenantID {
			result = append(result, h)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, host *AgentHost) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.hosts[host.ID]
	if !ok || existing.TenantID != host.TenantID {
		return nil
	}
	host.UpdatedAt = time.Now()
	s.hosts[host.ID] = host
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[id]
	if !ok || h.TenantID != tenantID {
		return nil
	}
	delete(s.hosts, h.ID)
	delete(s.tasks, h.ID)
	return nil
}

func (s *MemoryStore) Heartbeat(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hosts[id]
	if !ok || h.TenantID != tenantID {
		return nil
	}
	now := time.Now()
	h.LastSeen = &now
	h.LastHeartbeat = &now
	h.Status = AgentStatusOnline
	h.UpdatedAt = now
	if req.AgentVersion != "" {
		h.Version = req.AgentVersion
	}
	if req.OS != "" {
		h.OSType = req.OS
	}
	if req.OSVersion != "" {
		h.OSVersion = req.OSVersion
	}
	if req.Hostname != "" {
		h.Hostname = req.Hostname
	}
	if req.CPU != "" {
		h.CPU = req.CPU
	}
	if req.Memory != "" {
		h.Memory = req.Memory
	}
	if req.Disk != "" {
		h.Disk = req.Disk
	}
	if len(req.Capabilities) > 0 {
		h.Capabilities = req.Capabilities
	}
	return nil
}

func (s *MemoryStore) RegisterProxy(ctx context.Context, proxy *ProxyInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	proxy.ID = uuid.New().String()
	now := time.Now()
	proxy.CreatedAt = now
	if proxy.Status == "" {
		proxy.Status = string(AgentStatusUnknown)
	}
	s.proxies[proxy.ID] = proxy
	return nil
}

func (s *MemoryStore) GetProxy(ctx context.Context, id, tenantID string) (*ProxyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.proxies[id]
	if !ok || p.TenantID != tenantID {
		return nil, nil
	}
	return p, nil
}

func (s *MemoryStore) ListProxies(ctx context.Context, tenantID string) ([]*ProxyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ProxyInfo
	for _, p := range s.proxies {
		if p.TenantID == tenantID {
			result = append(result, p)
		}
	}
	return result, nil
}

func (s *MemoryStore) UpdateProxy(ctx context.Context, proxy *ProxyInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.proxies[proxy.ID]
	if !ok || existing.TenantID != proxy.TenantID {
		return nil
	}
	s.proxies[proxy.ID] = proxy
	return nil
}

func (s *MemoryStore) DeleteProxy(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.proxies[id]
	if !ok || p.TenantID != tenantID {
		return nil
	}
	delete(s.proxies, id)
	return nil
}

func (s *MemoryStore) CreateTask(ctx context.Context, agentID string, req *TaskRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.TaskID == "" {
		req.TaskID = uuid.New().String()
	}
	if req.Timeout <= 0 {
		req.Timeout = 300
	}
	now := time.Now()
	info := &TaskInfo{
		TaskRequest: *req,
		Status:      TaskStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.tasks[agentID] = append(s.tasks[agentID], info)
	return nil
}

func (s *MemoryStore) ListTasks(ctx context.Context, agentID string) ([]*TaskInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tasks := s.tasks[agentID]
	result := make([]*TaskInfo, len(tasks))
	copy(result, tasks)
	return result, nil
}

func (s *MemoryStore) CompleteTask(ctx context.Context, agentID, taskID string, resp *TaskResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := s.tasks[agentID]
	for _, t := range tasks {
		if t.TaskID == taskID {
			if resp.Success {
				t.Status = TaskStatusCompleted
			} else {
				t.Status = TaskStatusFailed
			}
			t.Response = resp
			t.UpdatedAt = time.Now()
			return nil
		}
	}
	return nil
}

func (s *MemoryStore) MarkOfflineAgents(ctx context.Context, tenantID string, threshold time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	now := time.Now()
	for _, h := range s.hosts {
		if h.TenantID != tenantID {
			continue
		}
		if h.Status == AgentStatusOffline {
			continue
		}
		var ref time.Time
		if h.LastHeartbeat != nil {
			ref = *h.LastHeartbeat
		} else if h.FirstSeen != nil {
			ref = *h.FirstSeen
		} else {
			ref = h.CreatedAt
		}
		if now.Sub(ref) > threshold {
			h.Status = AgentStatusOffline
			h.UpdatedAt = now
			count++
		}
	}
	return count, nil
}

// Service 是 agent 管理服务。
type Service struct {
	store           Store
	circuitBreakers map[string]*CircuitBreakerConfig
	cbMu            sync.Mutex
	stopCh          chan struct{}
	wsHub           *AgentWSHub // Agent WebSocket 长连接 Hub(可选,启用后任务优先走 WS)
	tlsEnabled      bool        // 是否全局启用 HTTPS(影响安装脚本中的 scheme)
	agentWSPort     int         // Agent WebSocket 端口(默认 5588)
	tunnelServer    *tunnel.Server // Proxy 隧道服务(可选,启用后支持通过 Proxy 下发命令)
}

func NewService() *Service {
	s := &Service{
		store:           NewMemoryStore(),
		circuitBreakers: make(map[string]*CircuitBreakerConfig),
		stopCh:          make(chan struct{}),
		agentWSPort:     5588,
	}
	go s.offlineDetector()
	return s
}

// SetWSHub 注入 Agent WebSocket Hub,启用后任务下发优先走长连接。
func (s *Service) SetWSHub(hub *AgentWSHub) {
	s.wsHub = hub
	// 注册任务结果回调:Agent 通过 WS 上报结果时,直接写入 store
	hub.SetTaskResultHandler(func(agentID string, msg *WSMessage) {
		resp := &TaskResponse{
			TaskID:   msg.TaskID,
			Success:  msg.Success,
			Output:   msg.Output,
			ExitCode: msg.ExitCode,
			Duration: msg.Duration,
			Error:    msg.Error,
		}
		if err := s.store.CompleteTask(context.Background(), agentID, msg.TaskID, resp); err != nil {
			slog.Warn("ws complete task failed", "error", err, "agent_id", agentID, "task_id", msg.TaskID)
		}
	})
}

// SetTLSEnable 设置是否启用 HTTPS(影响安装脚本的 scheme)。
func (s *Service) SetTLSEnable(enabled bool) {
	s.tlsEnabled = enabled
}

// SetAgentWSPort 设置 Agent WebSocket 端口(用于安装脚本中写入 WS 地址)。
func (s *Service) SetAgentWSPort(port int) {
	s.agentWSPort = port
}

// SetTunnelServer 注入 tunnel server,启用后支持通过 Proxy 下发命令。
func (s *Service) SetTunnelServer(ts *tunnel.Server) {
	s.tunnelServer = ts
}

// Close 停止后台离线检测 goroutine。
func (s *Service) Close() { close(s.stopCh) }

// offlineDetector 周期性地将长时间未心跳的 agent 标记为离线。
func (s *Service) offlineDetector() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			ctx := context.Background()
			n, _ := s.store.MarkOfflineAgents(ctx, "default", OfflineThreshold)
			if n > 0 {
				slog.Info("agents marked offline", "count", n)
			}
		}
	}
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	// Agent CRUD
	mux.HandleFunc("POST /api/v1/agents", s.handleRegister)
	mux.HandleFunc("GET /api/v1/agents", s.handleList)
	mux.HandleFunc("GET /api/v1/agents/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/agents/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/agents/{id}", s.handleDelete)
	mux.HandleFunc("POST /api/v1/agents/{id}/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("POST /api/v1/agents/batch-install", s.handleBatchInstall)
	mux.HandleFunc("POST /api/v1/agents/test-ssh", s.handleTestSSH)
	mux.HandleFunc("GET /api/v1/agents/install-script", s.handleInstallScript)
	mux.HandleFunc("GET /api/v1/agents/download", s.handleDownloadAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks/{task_id}", s.handleCompleteTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/circuit-breaker", s.handleGetCB)
	mux.HandleFunc("PUT /api/v1/agents/{id}/circuit-breaker", s.handleSetCB)
	mux.HandleFunc("POST /api/v1/agents/{id}/restart", s.handleRestartAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/upgrade", s.handleUpgradeAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/uninstall", s.handleUninstallAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/cmdb", s.handleCMDBReport)
	mux.HandleFunc("GET /api/v1/agents/{id}/download-tasks", s.handleDownloadTasks)
	// Proxy CRUD
	mux.HandleFunc("GET /api/v1/proxies", s.handleListProxies)
	mux.HandleFunc("POST /api/v1/proxies", s.handleRegisterProxy)
	mux.HandleFunc("GET /api/v1/proxies/{id}", s.handleGetProxy)
	mux.HandleFunc("PUT /api/v1/proxies/{id}", s.handleUpdateProxy)
	mux.HandleFunc("DELETE /api/v1/proxies/{id}", s.handleDeleteProxy)
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 勾选了 as_proxy 时,注册为 Proxy 节点
	if req.AsProxy {
		proxy := &ProxyInfo{
			TenantID: tenantFromCtx(r.Context()),
			Name:     req.Name,
			Address:  req.Address,
			Port:     req.Port,
			Status:   string(AgentStatusOnline),
		}
		if proxy.Port == 0 {
			proxy.Port = 8080
		}
		if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
			slog.Warn("register proxy failed", "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, proxy)
		return
	}
	host := s.buildHost(&req, r)
	if err := s.store.Register(r.Context(), host); err != nil {
		slog.Warn("register agent failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, host)
}

func (s *Service) buildHost(req *RegisterAgentRequest, r *http.Request) *AgentHost {
	return &AgentHost{
		TenantID:      tenantFromCtx(r.Context()),
		Name:          req.Name,
		Type:          req.Type,
		Address:       req.Address,
		Port:          req.Port,
		OSType:        req.OSType,
		AuthType:      req.AuthType,
		SSHUser:       req.SSHUser,
		SSHPassword:   req.SSHPassword,
		SSHKey:        req.SSHKey,
		InstallMethod: req.InstallMethod,
		Labels:        req.Labels,
		ProxyID:       req.ProxyID,
	}
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	agentType := r.URL.Query().Get("type")
	hosts, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		slog.Warn("list agents failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now()
	result := make([]*AgentHost, 0, len(hosts))
	for _, h := range hosts {
		if agentType != "" && string(h.Type) != agentType {
			continue
		}
		// 实时状态判定:超过阈值未心跳则视为离线(只改返回值,不修改 DB)
		copyHost := *h
		if copyHost.LastHeartbeat != nil && now.Sub(*copyHost.LastHeartbeat) > OfflineThreshold {
			copyHost.Status = AgentStatusOffline
		}
		result = append(result, &copyHost)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req UpdateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != "" {
		host.Name = req.Name
	}
	if req.Address != "" {
		host.Address = req.Address
	}
	if req.Port != 0 {
		host.Port = req.Port
	}
	if req.SSHUser != "" {
		host.SSHUser = req.SSHUser
	}
	if req.AuthType != "" {
		host.AuthType = req.AuthType
	}
	if req.Labels != nil {
		host.Labels = req.Labels
	}
	if err := s.store.Update(r.Context(), host); err != nil {
		slog.Warn("update agent failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete agent failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.Heartbeat(r.Context(), id, tenantID, &req); err != nil {
		slog.Warn("heartbeat failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "ok",
		"breaker_open":      false,
		"restart_requested": false,
	})
}

func (s *Service) handleBatchInstall(w http.ResponseWriter, r *http.Request) {
	var req BatchInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}

	addresses, err := parseAddressList(strings.Join(req.Addresses, ","))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	serverBase := fmt.Sprintf("%s://%s", scheme, r.Host)
	tenantID := tenantFromCtx(r.Context())

	results := make([]map[string]any, 0, len(addresses))
	for _, addr := range addresses {
		authType := "ssh"
		if req.CredentialType == "ssh_key" || req.SSHKey != "" {
			authType = "ssh_key"
		}

		// 勾选 as_proxy 时,直接注册为 Proxy 节点(不需要 SSH 连接,不做 Agent 安装)
		if req.AsProxy {
			proxy := &ProxyInfo{
				TenantID: tenantID,
				Name:     addr,
				Address:  addr,
				Port:     req.Port,
				Status:   string(AgentStatusOnline),
			}
			if proxy.Port == 0 {
				proxy.Port = 8080
			}
			if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
				results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"address": addr, "status": "proxy_registered", "proxy_id": proxy.ID})
			continue
		}

		reachable, detail := s.testSSHConnectivity(addr, req.Port, req.Username, req.Password, req.SSHKey)
		if !reachable {
			slog.Warn("batch install SSH unreachable, register as agentless", "address", addr, "detail", detail)
			host := &AgentHost{
				TenantID:      tenantID,
				Name:          addr,
				Type:          AgentTypeAgentless,
				Address:       addr,
				Port:          req.Port,
				AuthType:      authType,
				SSHUser:       req.Username,
				SSHPassword:   req.Password,
				SSHKey:        req.SSHKey,
				InstallMethod: InstallManual,
				ProxyID:       req.ProxyID,
			}
			if err := s.store.Register(r.Context(), host); err != nil {
				results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
				continue
			}
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": detail, "agent_id": host.ID})
			continue
		}

		installCmd := fmt.Sprintf("curl -sSL %s/api/v1/agents/install-script?os=linux | bash", serverBase)
		output, runErr := s.runSSHCommand(addr, req.Port, req.Username, req.Password, req.SSHKey, installCmd)
		if runErr != nil {
			slog.Warn("batch install command failed, register as agentless", "address", addr, "error", runErr)
			host := &AgentHost{
				TenantID:      tenantID,
				Name:          addr,
				Type:          AgentTypeAgentless,
				Address:       addr,
				Port:          req.Port,
				AuthType:      authType,
				SSHUser:       req.Username,
				SSHPassword:   req.Password,
				SSHKey:        req.SSHKey,
				InstallMethod: InstallManual,
				ProxyID:       req.ProxyID,
			}
			s.store.Register(r.Context(), host)
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": runErr.Error(), "agent_id": host.ID, "output": output})
			continue
		}

		host := &AgentHost{
			TenantID:      tenantID,
			Name:          addr,
			Type:          AgentTypeAgent,
			Address:       addr,
			Port:          req.Port,
			AuthType:      authType,
			SSHUser:       req.Username,
			SSHPassword:   req.Password,
			SSHKey:        req.SSHKey,
			InstallMethod: InstallAuto,
			ProxyID:       req.ProxyID,
		}
		if err := s.store.Register(r.Context(), host); err != nil {
			results = append(results, map[string]any{"address": addr, "status": "failed", "error": err.Error()})
			continue
		}
		results = append(results, map[string]any{"address": addr, "status": "installed", "agent_id": host.ID, "output": output})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Service) handleTestSSH(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		SSHKey   string `json:"ssh_key,omitempty"`
		UseKey   bool   `json:"use_key,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	authType := "password"
	if req.UseKey || req.SSHKey != "" {
		authType = "ssh_key"
	}
	reachable, detail := s.testSSHConnectivity(req.Address, req.Port, req.Username, req.Password, req.SSHKey)
	if !reachable {
		slog.Warn("SSH test failed", "address", req.Address, "detail", detail)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reachable": reachable,
		"address":   fmt.Sprintf("%s:%d", req.Address, req.Port),
		"auth_type": authType,
		"detail":    detail,
	})
}

// testSSHConnectivity 使用真实 SSH 握手检测目标主机连通性。
func (s *Service) testSSHConnectivity(addr string, port int, username, password, sshKey string) (bool, string) {
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	if sshKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(sshKey))
		if err != nil {
			return false, "parse key failed: " + err.Error()
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(addr, fmt.Sprintf("%d", port)), config)
	if err != nil {
		return false, err.Error()
	}
	client.Close()
	return true, "connected"
}

// runSSHCommand 通过 SSH 在远程主机执行命令并返回合并输出。
func (s *Service) runSSHCommand(addr string, port int, username, password, sshKey, cmd string) (string, error) {
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	if sshKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(sshKey))
		if err != nil {
			return "", fmt.Errorf("parse key: %w", err)
		}
		config.Auth = append(config.Auth, ssh.PublicKeys(signer))
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(addr, fmt.Sprintf("%d", port)), config)
	if err != nil {
		return "", err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	out, err := session.CombinedOutput(cmd)
	return string(out), err
}

// parseAddressList 解析地址列表,支持以下格式:
//   - 单个 IPv4: 192.168.1.1
//   - 主机名: web-server-01
//   - IP 范围: 192.168.1.1-10 (展开为 192.168.1.1 ~ 192.168.1.10)
//   - CIDR: 192.168.1.0/24 (展开为该子网内所有 IP)
//   - 混合输入(逗号/换行/分号分隔)
func parseAddressList(input string) ([]string, error) {
	parts := regexp.MustCompile(`[\n,;]+`).Split(input, -1)
	var result []string
	ipv4Like := regexp.MustCompile(`^\d+\.\d+\.\d+\.\d+$`)
	rangePattern := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)\.(\d+)-(\d+)$`)
	for _, part := range parts {
		addr := strings.TrimSpace(part)
		if addr == "" {
			continue
		}

		// 1) IP 范围: 192.168.1.1-10
		if m := rangePattern.FindStringSubmatch(addr); m != nil {
			oct1, _ := strconv.Atoi(m[1])
			oct2, _ := strconv.Atoi(m[2])
			oct3, _ := strconv.Atoi(m[3])
			startOct, _ := strconv.Atoi(m[4])
			endOct, _ := strconv.Atoi(m[5])
			if startOct > endOct {
				startOct, endOct = endOct, startOct
			}
			if endOct-startOct > 255 {
				return nil, fmt.Errorf("address range %q too wide (max 256)", addr)
			}
			for i := startOct; i <= endOct; i++ {
				result = append(result, fmt.Sprintf("%d.%d.%d.%d", oct1, oct2, oct3, i))
			}
			continue
		}

		// 2) CIDR: 192.168.1.0/24
		if strings.Contains(addr, "/") {
			ip, ipNet, err := net.ParseCIDR(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR %q: %v", addr, err)
			}
			ones, bits := ipNet.Mask.Size()
			// 限制展开规模,避免过大范围
			hostBits := bits - ones
			if hostBits > 10 {
				return nil, fmt.Errorf("CIDR %q too wide (max /22 = 1024 hosts)", addr)
			}
			for ip := ip.Mask(ipNet.Mask); ipNet.Contains(ip); incIP(ip) {
				result = append(result, ip.String())
				if len(result) > 1024 {
					return nil, fmt.Errorf("address list too long (max 1024)")
				}
			}
			continue
		}

		// 3) 形如 IPv4 但 ParseIP 失败 → octet 超范围(如 192.168.1.999)
		if ipv4Like.MatchString(addr) && net.ParseIP(addr) == nil {
			return nil, fmt.Errorf("invalid address %q (octet out of range)", addr)
		}

		// 4) 单个 IPv4 地址(不支持主机名,确保可下发自动安装命令)
		if net.ParseIP(addr) != nil {
			result = append(result, addr)
			continue
		}
		return nil, fmt.Errorf("invalid address %q (only IPv4, range, or CIDR are supported)", addr)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one address is required")
	}
	return result, nil
}

// incIP 将 IP 地址自增 1(用于 CIDR 展开)。
func incIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func (s *Service) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	osType := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	serverAddr := r.Host
	if serverAddr == "" {
		serverAddr = "localhost:8080"
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		serverAddr = h
	}
	// 确定 scheme:优先 X-Forwarded-Proto,其次 TLS 配置,再次 r.TLS,默认 http
	scheme := s.determineScheme(r)
	// Agent WebSocket 长连接地址(独立端口 5588)
	wsScheme := "ws"
	if scheme == "https" {
		wsScheme = "wss"
	}
	wsPort := s.agentWSPort
	if wsPort == 0 {
		wsPort = 5588
	}
	// proxy 参数值为 Proxy 的地址(host:port),非空时在启动命令中追加 --proxy
	proxyAddr := r.URL.Query().Get("proxy")
	proxyFlag := ""
	if proxyAddr != "" {
		proxyFlag = " --proxy " + proxyAddr
	}

	var script string
	var filename string
	switch osType {
	case "windows":
		filename = "install-agent.ps1"
		script = fmt.Sprintf(`# OpsOrigin Agent Installer (Windows, arch=%s)
$ErrorActionPreference = "Stop"
$server = "%s"
$scheme = "%s"
$wsScheme = "%s"
$api = "$($scheme)://$($server)/api/v1/agents/download?os=windows&arch=%s"
$installPath = "C:\Program Files\OpsOrigin"
New-Item -ItemType Directory -Force -Path $installPath | Out-Null
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -Uri $api -OutFile "$installPath\agent.exe"
New-Service -Name "OpsOriginAgent" -BinaryPathName "$installPath\agent.exe --server $server --ws-server \"$($wsScheme)://$($server):%d/agent-ws\"%s" -DisplayName "OpsOrigin Agent" -StartupType Automatic
Start-Service -Name "OpsOriginAgent"
Write-Host "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, wsScheme, arch, wsPort, proxyFlag, arch)
	case "linux":
		filename = "install-agent.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Linux, arch=%s)
set -e
SERVER="%s"
SCHEME="%s"
WS_SCHEME="%s"
WS_PORT="%d"
API="$SCHEME://$SERVER/api/v1/agents/download?os=linux&arch=%s"
INSTALL_DIR="/opt/opsorigin"
sudo mkdir -p $INSTALL_DIR
sudo curl -sSL $API -o $INSTALL_DIR/agent
sudo chmod +x $INSTALL_DIR/agent
sudo tee /etc/systemd/system/opsorigin-agent.service > /dev/null <<EOF
[Unit]
Description=OpsOrigin Agent
After=network.target
[Service]
Type=simple
ExecStart=$INSTALL_DIR/agent --server $SERVER --ws-server $WS_SCHEME://$SERVER:$WS_PORT/agent-ws%s
Restart=always
RestartSec=10
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-agent
sudo systemctl start opsorigin-agent
echo "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, wsScheme, wsPort, arch, proxyFlag, arch)
	case "container":
		filename = "install-agent-container.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Container / Docker)
set -e
SERVER="%s"
SCHEME="%s"
WS_SCHEME="%s"
WS_PORT="%d"

# 检查 Docker 是否可用
if ! command -v docker >/dev/null 2>&1; then
  echo "[ERROR] docker not found. Please install Docker first."
  exit 1
fi

# 拉取并运行 Agent 容器(自动注册到 Server)
docker run -d \
  --name opsorigin-agent \
  --restart always \
  -e OPSORIGIN_SERVER="$SCHEME://%s" \
  -e OPSORIGIN_WS_SERVER="$WS_SCHEME://%s:$WS_PORT/agent-ws" \
  -v /etc/hostname:/etc/hostname:ro \
  -v /etc/os-release:/etc/os-release:ro \
  -v /proc:/host/proc:ro \
  -v /sys:/host/sys:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  opsorigin/agent:latest \
  --server "$SCHEME://%s" \
  --ws-server "$WS_SCHEME://%s:$WS_PORT/agent-ws"%s

echo "OpsOrigin Agent container started successfully"
echo "View logs: docker logs -f opsorigin-agent"`, serverAddr, scheme, wsScheme, wsPort, serverAddr, serverAddr, serverAddr, serverAddr, proxyFlag)
case "proxy":
	proxyID := r.URL.Query().Get("proxy_id")
	if proxyID == "" {
		proxyID = fmt.Sprintf("proxy-%s", uuid.New().String()[:8])
	}
	listenPort := r.URL.Query().Get("port")
	if listenPort == "" {
		listenPort = "9090"
	}
	filename = "install-proxy.sh"
	script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Proxy Installer
set -e
SERVER="%s"
SCHEME="%s"
PROXY_ID="%s"
LISTEN_PORT="%s"
API="$SCHEME://$SERVER/api/v1/agents/download?os=linux&arch=amd64&type=proxy"
INSTALL_DIR="/opt/opsorigin/proxy"
sudo mkdir -p $INSTALL_DIR
sudo curl -sSL $API -o $INSTALL_DIR/opsproxy
sudo chmod +x $INSTALL_DIR/opsproxy
sudo tee $INSTALL_DIR/config.yaml > /dev/null <<EOF
proxy:
  id: "$PROXY_ID"
  core_addr: "$SERVER"
  listen: ":$LISTEN_PORT"
EOF
sudo tee /etc/systemd/system/opsorigin-proxy.service > /dev/null <<EOF
[Unit]
Description=OpsOrigin Proxy
After=network.target
[Service]
Type=simple
ExecStart=$INSTALL_DIR/opsproxy -config $INSTALL_DIR/config.yaml
Restart=always
RestartSec=10
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-proxy
sudo systemctl start opsorigin-proxy
echo "OpsOrigin Proxy installed successfully (id=$PROXY_ID, listen=:$LISTEN_PORT)"`, serverAddr, scheme, proxyID, listenPort)
default:
	writeError(w, http.StatusBadRequest, "os parameter required: linux, windows, container, or proxy")
	return
}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(script))
}

// determineScheme 判断当前请求的 scheme(用于生成安装脚本中的下载链接)。
// 优先级: X-Forwarded-Proto > tlsEnabled 配置 > r.TLS > 默认 http
func (s *Service) determineScheme(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "https" {
		return "https"
	}
	if s.tlsEnabled {
		return "https"
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	var req TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.CreateTask(r.Context(), agentID, &req); err != nil {
		slog.Warn("create task failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Proxy 路由:如果 Agent 通过 Proxy 连接,优先通过 tunnel 下发命令
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), agentID, tenantFromCtx(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, err := json.Marshal(req)
			if err != nil {
				slog.Warn("marshal task payload failed", "error", err)
			} else {
				cmd := &tunnel.CommandRequest{
					CommandID:     req.TaskID,
					TargetID:      agentID,
					Action:        req.Action,
					Payload:       payloadBytes,
					TimeoutSecond: int32(req.Timeout),
				}
				resp, err := s.tunnelServer.SendCommand(host.ProxyID, cmd)
				if err == nil && resp != nil && resp.Success {
					taskResp := &TaskResponse{
						TaskID:  req.TaskID,
						Success: true,
						Output:  string(resp.Output),
					}
					if err := s.store.CompleteTask(r.Context(), agentID, req.TaskID, taskResp); err != nil {
						slog.Warn("complete task via proxy failed", "error", err, "agent_id", agentID, "task_id", req.TaskID)
					}
					slog.Info("task dispatched via proxy", "agent_id", agentID, "task_id", req.TaskID, "proxy_id", host.ProxyID)
					writeJSON(w, http.StatusOK, map[string]any{"task_id": req.TaskID, "status": "completed"})
					return
				}
				// 失败则降级到 WS/HTTP
				slog.Warn("proxy route failed, falling back to WS/HTTP", "error", err, "proxy_id", host.ProxyID)
			}
		}
	}
	// 如果 Agent 通过 WebSocket 在线,立即下发任务(无需等待 HTTP 轮询)
	if s.wsHub != nil {
		wsMsg := &WSMessage{
			TaskID:  req.TaskID,
			Action:  req.Action,
			Script:  req.Script,
			Params:  req.Params,
			Timeout: req.Timeout,
			Time:    time.Now().UnixMilli(),
		}
		if s.wsHub.SendTask(agentID, wsMsg) {
			// WS 下发成功,任务状态置为 running
			slog.Info("task dispatched via websocket", "agent_id", agentID, "task_id", req.TaskID)
		} else {
			slog.Info("agent not online via ws, task pending for http poll",
				"agent_id", agentID, "task_id", req.TaskID)
		}
	}
	writeJSON(w, http.StatusCreated, req)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tasks, err := s.store.ListTasks(r.Context(), agentID)
	if err != nil {
		slog.Warn("list tasks failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*TaskInfo{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Service) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	taskID := r.PathValue("task_id")
	var resp TaskResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if resp.TaskID == "" {
		resp.TaskID = taskID
	}
	if err := s.store.CompleteTask(r.Context(), agentID, taskID, &resp); err != nil {
		slog.Warn("complete task failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Service) handleDownloadAgent(w http.ResponseWriter, r *http.Request) {
	osType := r.URL.Query().Get("os")
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	downloadType := r.URL.Query().Get("type")
	if downloadType == "" {
		downloadType = "agent"
	}

	// 获取服务器地址(支持 HTTPS)
	scheme := s.determineScheme(r)
	serverAddr := fmt.Sprintf("%s://%s", scheme, r.Host)

	if downloadType == "proxy" {
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=opsproxy")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`#!/bin/bash
# opsproxy placeholder - real binary will be served in production
# Usage: opsproxy -config /path/to/config.yaml
echo "OpsOrigin Proxy (placeholder)"
echo "Config: $@"
exec sleep infinity`))
		return
	}

	switch osType {
	case "linux", "darwin":
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=install_agent.sh")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(generateInstallScript(serverAddr, osType, arch)))

	case "windows":
		w.Header().Set("Content-Type", "application/x-powershell; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=install_agent.ps1")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(generateWindowsInstallScript(serverAddr, arch)))

	default:
		writeError(w, http.StatusBadRequest, "os parameter required: linux, darwin, or windows")
	}
}

// generateInstallScript 生成 Linux/macOS 安装脚本。
// serverAddr 参数应包含 scheme(如 https://host:8080),用于下载链接。
// 安装脚本会将 scheme 和 host:port 分开,config.yaml 中只存 host:port。
func generateInstallScript(serverAddr, osType, arch string) string {
	binaryName := "opsagent"
	if osType == "darwin" {
		binaryName = "opsagent-darwin"
	}
	installDir := "/opt/opsorigin/agent"
	serviceName := "opsorigin-agent"

	return fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer
# Usage: bash install_agent.sh

set -e

SERVER_ADDR="%s"
INSTALL_DIR="%s"
SERVICE_NAME="%s"
BINARY_NAME="%s"
ARCH="%s"

echo "========================================="
echo "  OpsOrigin Agent Installer"
echo "========================================="
echo ""

# Check root
if [ "$EUID" -ne 0 ]; then
  echo "[ERROR] Please run as root (sudo bash install_agent.sh)"
  exit 1
fi

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
echo "[INFO] Detected OS: $OS ($ARCH)"

# Create install directory
echo "[INFO] Creating install directory: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

# Download agent binary (走 HTTPS 下载)
echo "[INFO] Downloading agent binary from server..."
curl -fsSL "${SERVER_ADDR}/api/v1/agents/download?os=%s&arch=${ARCH}" -o "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"
echo "[INFO] Agent binary installed to: $INSTALL_DIR/$BINARY_NAME"

# Extract host:port from SERVER_ADDR (去掉 scheme 前缀)
SERVER_HOST=$(echo "$SERVER_ADDR" | sed 's|^[a-z]*://||')

# Create config file
echo "[INFO] Creating configuration file..."
cat > "$INSTALL_DIR/config.yaml" << EOF
server:
  address: "$SERVER_HOST"
agent:
  name: "$(hostname)"
  interval: 30
  log_level: info
EOF
echo "[INFO] Configuration created at: $INSTALL_DIR/config.yaml"

# Create systemd service (Linux) or launchd plist (macOS)
if [ "$OS" = "linux" ]; then
  echo "[INFO] Creating systemd service..."
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=OpsOrigin Agent
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/$BINARY_NAME -config $INSTALL_DIR/config.yaml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  systemctl start "$SERVICE_NAME"
  echo "[INFO] Service started: $SERVICE_NAME"

elif [ "$OS" = "darwin" ]; then
  echo "[INFO] Creating launchd plist..."
  PLIST_PATH="/Library/LaunchDaemons/com.opsorigin.agent.plist"
  cat > "$PLIST_PATH" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.opsorigin.agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>$INSTALL_DIR/$BINARY_NAME</string>
        <string>-config</string>
        <string>$INSTALL_DIR/config.yaml</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/opsorigin-agent.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/opsorigin-agent.log</string>
</dict>
</plist>
EOF
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl load "$PLIST_PATH"
  echo "[INFO] Launchd service installed: com.opsorigin.agent"
fi

echo ""
echo "========================================="
echo "  Installation Complete!"
echo "========================================="
echo ""
echo "  Install Dir:  $INSTALL_DIR"
echo "  Config File:  $INSTALL_DIR/config.yaml"
echo "  Log File:     /var/log/opsorigin-agent.log"
echo ""
echo "  Service:      $SERVICE_NAME"
echo "  Status:       systemctl status $SERVICE_NAME"
echo "  Logs:         journalctl -u $SERVICE_NAME -f"
echo ""
echo "========================================="
`, serverAddr, installDir, serviceName, binaryName, arch, osType)
}

func generateWindowsInstallScript(serverAddr, arch string) string {
	return fmt.Sprintf(`# OpsOrigin Agent Installer for Windows
# Usage: Right-click -> Run with PowerShell, or: powershell -ExecutionPolicy Bypass -File install_agent.ps1

$ErrorActionPreference = "Stop"

$ServerAddr = "%s"
$Arch = "%s"
$InstallDir = "$env:ProgramFiles\OpsOrigin\Agent"
$ServiceName = "OpsOriginAgent"

Write-Host "=========================================" -ForegroundColor Cyan
Write-Host "  OpsOrigin Agent Installer" -ForegroundColor Cyan
Write-Host "=========================================" -ForegroundColor Cyan
Write-Host ""

# Check admin
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[ERROR] Please run as Administrator" -ForegroundColor Red
    Write-Host "Right-click PowerShell and select 'Run as Administrator'" -ForegroundColor Yellow
    pause
    exit 1
}

# Create install directory
Write-Host "[INFO] Creating install directory: $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Download agent binary
Write-Host "[INFO] Downloading agent binary..."
$binaryUrl = "$ServerAddr/api/v1/agents/download?os=windows&arch=$Arch"
$binaryPath = "$InstallDir\opsagent.exe"
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -Uri $binaryUrl -OutFile $binaryPath -UseBasicParsing
} catch {
    Write-Host "[ERROR] Failed to download: $_" -ForegroundColor Red
    pause
    exit 1
}
Write-Host "[INFO] Agent binary downloaded to: $binaryPath"

# Create config file
Write-Host "[INFO] Creating configuration file..."
# Extract host:port from ServerAddr (去掉 scheme 前缀)
$ServerHost = $ServerAddr -replace '^[a-z]+://', ''
$configContent = @"
server:
  address: "$ServerHost"
agent:
  name: "$env:COMPUTERNAME"
  interval: 30
  log_level: info
"@
$configPath = "$InstallDir\config.yaml"
Set-Content -Path $configPath -Value $configContent -Encoding UTF8
Write-Host "[INFO] Configuration created at: $configPath"

# Install and start Windows service
Write-Host "[INFO] Installing Windows service..."
& $binaryPath install --config $configPath

Write-Host "[INFO] Starting service..."
Start-Service -Name $ServiceName -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "=========================================" -ForegroundColor Green
Write-Host "  Installation Complete!" -ForegroundColor Green
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""
Write-Host "  Install Dir:  $InstallDir"
Write-Host "  Config File:  $configPath"
Write-Host "  Service:      $ServiceName"
Write-Host ""
Write-Host "  Check Status: Get-Service $ServiceName"
Write-Host "  View Logs:    Get-EventLog -LogName Application -Source 'OpsOriginAgent'"
Write-Host ""
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""
pause
`, serverAddr, arch)
}

// ---- Proxy handlers ----

func (s *Service) handleListProxies(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	proxies, err := s.store.ListProxies(r.Context(), tenantID)
	if err != nil {
		slog.Warn("list proxies failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if proxies == nil {
		proxies = []*ProxyInfo{}
	}
	writeJSON(w, http.StatusOK, proxies)
}

func (s *Service) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
	var req RegisterProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	proxy := &ProxyInfo{
		TenantID: tenantFromCtx(r.Context()),
		Name:     req.Name,
		Address:  req.Address,
		Port:     req.Port,
	}
	if err := s.store.RegisterProxy(r.Context(), proxy); err != nil {
		slog.Warn("register proxy failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, proxy)
}

func (s *Service) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		writeError(w, http.StatusNotFound, "proxy not found")
		return
	}
	writeJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		writeError(w, http.StatusNotFound, "proxy not found")
		return
	}
	var req UpdateProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != "" {
		proxy.Name = req.Name
	}
	if req.Address != "" {
		proxy.Address = req.Address
	}
	if req.Port != 0 {
		proxy.Port = req.Port
	}
	if req.Status != "" {
		proxy.Status = req.Status
	}
	if err := s.store.UpdateProxy(r.Context(), proxy); err != nil {
		slog.Warn("update proxy failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.DeleteProxy(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete proxy failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---- Circuit breaker ----

func (s *Service) handleGetCB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cb := s.getOrCreateBreaker(id)
	writeJSON(w, http.StatusOK, cb)
}

func (s *Service) handleSetCB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		MaxHeartbeatsPerMin int  `json:"max_heartbeats_per_min"`
		MaxReqPerMin        int  `json:"max_req_per_min"`
		AutoRestart         bool `json:"auto_restart"`
		MaxConsecutiveFails int  `json:"max_consecutive_fails"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	s.cbMu.Lock()
	cb := s.circuitBreakers[id]
	if cb == nil {
		cb = &CircuitBreakerConfig{AgentID: id}
		s.circuitBreakers[id] = cb
	}
	if req.MaxHeartbeatsPerMin > 0 {
		cb.MaxHeartbeatsPerMin = req.MaxHeartbeatsPerMin
	}
	if req.MaxReqPerMin > 0 {
		cb.MaxReqPerMin = req.MaxReqPerMin
	}
	cb.AutoRestart = req.AutoRestart
	if req.MaxConsecutiveFails > 0 {
		cb.MaxConsecutiveFails = req.MaxConsecutiveFails
	}
	s.cbMu.Unlock()
	writeJSON(w, http.StatusOK, cb)
}

// handleRestartAgent 通过下发 action=restart 任务来重启 agent,而非触发熔断。
func (s *Service) handleRestartAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := &TaskRequest{
		Action:  "restart",
		Timeout: 300,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue restart task failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("agent restart task queued", "agent_id", id, "task_id", task.TaskID)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":            "restart_command_queued",
		"agent_id":          id,
		"restart_requested": true,
		"task_id":           task.TaskID,
	})
}

// handleUpgradeAgent 通过下发 action=upgrade 任务来升级 agent 二进制。
func (s *Service) handleUpgradeAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 可选:从 body 读取 target_version,不指定则升级到最新
	var req struct {
		TargetVersion string `json:"target_version,omitempty"`
	}
	json.NewDecoder(r.Body).Decode(&req) // 忽略错误,body 可空

	task := &TaskRequest{
		Action:  "upgrade",
		Params:  map[string]any{"target_version": req.TargetVersion},
		Timeout: 600,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue upgrade task failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 如果 Agent 通过 Proxy 连接,尝试通过 tunnel 立即下发
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), id, tenantFromCtx(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     task.TaskID,
				TargetID:      id,
				Action:        "upgrade",
				Payload:       payloadBytes,
				TimeoutSecond: 600,
			}
			s.tunnelServer.SendCommand(host.ProxyID, cmd)
		}
	}
	// WS 下发
	if s.wsHub != nil {
		s.wsHub.SendTask(id, &WSMessage{
			TaskID: task.TaskID,
			Action: "upgrade",
			Params: task.Params,
			Time:   time.Now().UnixMilli(),
		})
	}

	slog.Info("agent upgrade task queued", "agent_id", id, "task_id", task.TaskID)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "upgrade_command_queued",
		"agent_id": id,
		"task_id":  task.TaskID,
	})
}

// handleUninstallAgent 通过下发 action=uninstall 任务来卸载 agent。
// 卸载成功后,Agent 会停止服务、删除二进制和配置,然后从主机列表移除。
func (s *Service) handleUninstallAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	task := &TaskRequest{
		Action:  "uninstall",
		Timeout: 120,
	}
	if err := s.store.CreateTask(r.Context(), id, task); err != nil {
		slog.Warn("queue uninstall task failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 通过 Proxy 或 WS 立即下发
	if s.tunnelServer != nil {
		if host, err := s.store.Get(r.Context(), id, tenantFromCtx(r.Context())); err == nil && host != nil && host.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     task.TaskID,
				TargetID:      id,
				Action:        "uninstall",
				Payload:       payloadBytes,
				TimeoutSecond: 120,
			}
			s.tunnelServer.SendCommand(host.ProxyID, cmd)
		}
	}
	if s.wsHub != nil {
		s.wsHub.SendTask(id, &WSMessage{
			TaskID: task.TaskID,
			Action: "uninstall",
			Time:   time.Now().UnixMilli(),
		})
	}

	slog.Info("agent uninstall task queued", "agent_id", id, "task_id", task.TaskID)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "uninstall_command_queued",
		"agent_id": id,
		"task_id":  task.TaskID,
	})
}

func (s *Service) getOrCreateBreaker(id string) *CircuitBreakerConfig {
	s.cbMu.Lock()
	defer s.cbMu.Unlock()
	cb, ok := s.circuitBreakers[id]
	if !ok {
		cb = &CircuitBreakerConfig{
			AgentID:             id,
			MaxHeartbeatsPerMin: 120,
			MaxReqPerMin:        60,
			AutoRestart:         false,
			MaxConsecutiveFails: 5,
		}
		s.circuitBreakers[id] = cb
	}
	return cb
}

func (s *Service) handleCMDBReport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	host, err := s.store.Get(r.Context(), id, tenantFromCtx(r.Context()))
	if err != nil || host == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	var report struct {
		Hostname  string            `json:"hostname"`
		OS        string            `json:"os"`
		OSVersion string            `json:"os_version"`
		CPU       string            `json:"cpu"`
		Memory    string            `json:"memory"`
		Disk      string            `json:"disk"`
		IPs       []string          `json:"ips"`
		Labels    map[string]string `json:"labels,omitempty"`
		Timestamp int64             `json:"timestamp"`
	}

	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update host with CMDB data
	if report.Hostname != "" {
		host.Hostname = report.Hostname
	}
	if report.OS != "" {
		host.OSType = report.OS
	}
	if report.OSVersion != "" {
		host.OSVersion = report.OSVersion
	}
	if report.CPU != "" {
		host.CPU = report.CPU
	}
	if report.Memory != "" {
		host.Memory = report.Memory
	}
	if report.Disk != "" {
		host.Disk = report.Disk
	}
	if len(report.IPs) > 0 {
		host.Address = report.IPs[0]
	}
	if report.Labels != nil {
		if host.Labels == nil {
			host.Labels = make(map[string]string)
		}
		for k, v := range report.Labels {
			host.Labels[k] = v
		}
	}

	s.store.Update(r.Context(), host)
	writeJSON(w, http.StatusOK, map[string]string{"status": "cmdb data received"})
}

// handleDownloadTasks 返回该 agent 待执行的 pending 任务。
func (s *Service) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	tasks, err := s.store.ListTasks(r.Context(), id)
	if err != nil {
		slog.Warn("download tasks failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pending := make([]*TaskInfo, 0)
	for _, t := range tasks {
		if t.Status == TaskStatusPending {
			pending = append(pending, t)
		}
	}
	writeJSON(w, http.StatusOK, pending)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
