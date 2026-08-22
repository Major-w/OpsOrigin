package agentmgr

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"slices"

	"github.com/google/uuid"
	pb "github.com/opsorigin/core/api/proto"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/ent"
	"github.com/opsorigin/core/internal/filelimit"
	"github.com/opsorigin/core/internal/remoteexec"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/redis/go-redis/v9"
)

// Store 定义 agent 管理服务的数据持久化接口。
type Store interface {
	// Agent CRUD
	Register(ctx context.Context, host *AgentHost) error
	GetByFingerprint(ctx context.Context, fp, tenantID string) (*AgentHost, error)
	Get(ctx context.Context, id, tenantID string) (*AgentHost, error)
	List(ctx context.Context, tenantID string) ([]*AgentHost, error)
	Update(ctx context.Context, host *AgentHost) error
	Delete(ctx context.Context, id, tenantID string) error
	Heartbeat(ctx context.Context, id, tenantID string, req *HeartbeatRequest) error
	BulkHeartbeat(ctx context.Context, batch []heartbeatBatch) error
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

	host.Fingerprint = GenerateFingerprint(host.TenantID, host.Address)
	// channel 映射: agent→"agent", agentless→"ssh", sandbox→"sandbox"
	ch := channelFromType(host.Type)
	// 去重：按指纹查找已有主机，复用 ID 并合并 channels
	if existing := s.lookupByFingerprint(host.Fingerprint); existing != nil {
		host.ID = existing.ID
		host.FirstSeen = existing.FirstSeen
		host.CreatedAt = existing.CreatedAt
		if ch != "" {
			host.Channels = mergeChannels(existing.Channels, ch)
		}
	}
	if host.ID == "" {
		host.ID = uuid.New().String()
	}
	if len(host.Channels) == 0 {
		host.Channels = []string{ch}
	}
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

func (s *MemoryStore) GetByFingerprint(ctx context.Context, fp, tenantID string) (*AgentHost, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if h := s.lookupByFingerprint(fp); h != nil && h.TenantID == tenantID {
		return h, nil
	}
	return nil, nil
}

func (s *MemoryStore) lookupByFingerprint(fp string) *AgentHost {
	for _, h := range s.hosts {
		if h.Fingerprint == fp {
			return h
		}
	}
	return nil
}

func channelFromType(t AgentType) string {
	switch t {
	case AgentTypeAgent:
		return "agent"
	case AgentTypeAgentless:
		return "ssh"
	case AgentTypeSandbox:
		return "sandbox"
	default:
		return string(t)
	}
}

func mergeChannels(existing []string, new string) []string {
	for _, c := range existing {
		if c == new {
			return existing
		}
	}
	return append(existing, new)
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

func (s *MemoryStore) BulkHeartbeat(ctx context.Context, batch []heartbeatBatch) error {
	for _, hb := range batch {
		req := &HeartbeatRequest{
			AgentVersion: hb.Version,
			OS:           hb.OS,
			OSVersion:    hb.OSVersion,
			Hostname:     hb.Hostname,
			CPU:          hb.CPU,
			Memory:       hb.Memory,
		}
		if err := s.Heartbeat(ctx, hb.ID, hb.TenantID, req); err != nil {
			return err
		}
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
// bootSeen 记录主机最近一次上报的 boot_id 时刻,用于克隆冲突检测。
type bootSeen struct {
	id   string
	seen time.Time
}

type Service struct {
	store           Store
	entClient       *ent.Client
	hbWriter        *HeartbeatWriter // Redis heartbeat writer (nil if Redis unavailable)
	circuitBreakers map[string]*CircuitBreakerConfig
	cbMu            sync.Mutex
	resourceGuards  map[string]*ResourceGuardConfig
	rgMu            sync.Mutex
	// boot_id 追踪:按 host_id 记录最近一次上报的 boot_id 与时间,
	// 用于检测镜像克隆冲突(同一 host_id 在短时间窗口内来自两个不同的 boot_id)。
	hostBootID   map[string]bootSeen
	bootIDMu     sync.Mutex
	stopCh       chan struct{}
	tlsEnabled   bool                // 是否全局启用 HTTPS(影响安装脚本中的 scheme)
	tunnelServer *tunnel.Server      // Proxy 隧道服务(TCP,可选)
	grpcTunnel   *tunnel.GRPCServer  // gRPC 直连隧道服务(可选,启用后支持直连 Agent 下发命令)
	remoteExec   *remoteexec.Service // SSH 远程执行服务(agentless 主机命令执行)

	// T11: 任务下发队列
	taskQueue *TaskQueue

	// T12: 滚动升级通道
	upgradeMgr *UpgradeManager

	// 采集任务管理(CMDB/监控数据采集上报)
	collect *CollectManager

	// onAgentOnboard 主机/容器纳管成功后的回调(方案 X:统一经 CMDB 建采集任务)。
	// 由 cmd/core 注入 cmdbSvc.OnboardResource,使纳管动作自动在 CMDB 侧建 channel=agent 任务。
	onAgentOnboard func(tenantID, agentID string) error

	// hostCIWriter 把 AgentHost 资产同步为 CMDB host CI 的实现(方案 X 闭环最后一跳)。
	// 由 cmd/core 注入 agentCMDBAdapter;nil 时静默跳过(纯演示环境不阻塞主流程)。
	hostCIWriter HostCIWriter

	// collectReportSink 把采集脚本输出(统一 collect/report 格式: model_id-字段ID)
	// 泛化 upsert 到 CMDB 实例的实现。由 cmd/core 注入 cmdbSvc(鸭子类型)。
	// nil 时 cmdb 型采集仅走旧 host 扁平兼容逻辑,不阻断主流程。
	collectReportSink CollectReportSink

	// 全局目标版本策略（Agent 版本一致性管理）
	versionPolicy   string
	versionPolicyMu sync.Mutex

	// §6 命令白名单:非 nil 时所有经 Server 直接下发的命令(SSH 直连 / 经 Proxy / 沙箱)
	// 执行前均须命中白名单,否则拒绝。由上层从 command_whitelist 表加载后通过 SetWhitelist 注入。
	whitelist       *command.Whitelist
	whitelistMu     sync.RWMutex
	fileTokenSecret string             // §6 文件下载短期令牌密钥(P1 文件代理启用)
	fileStoreDir    string             // §5 文件分发中心存储目录(以 file_id 为文件名)
	fileLimiter     *filelimit.Limiter // §5 文件下载限流/熔断器
	serverBaseURL   string             // Server 对外基址(如 http://host:8080),用于拓扑感知下发直连下载地址
}

func NewService(db *sql.DB, rdb *redis.Client, entClient *ent.Client) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db, entClient)
	}
	// 采集任务: PG 可用时持久化(重启不丢), 否则内存
	var collectStore CollectStore
	if pg, ok := st.(*pgStore); ok {
		collectStore = pg
	}
	s := &Service{
		store:           st,
		entClient:       entClient,
		hbWriter:        NewHeartbeatWriter(rdb, st),
		circuitBreakers: make(map[string]*CircuitBreakerConfig),
		resourceGuards:  make(map[string]*ResourceGuardConfig),
		stopCh:          make(chan struct{}),
		remoteExec:      remoteexec.NewService(),
		fileLimiter:     filelimit.New(0, 0), // 0 ⇒ 使用 filelimit 包默认值
		collect:         NewCollectManager(collectStore),
	}
	// Start Redis→PG heartbeat sync worker
	if rdb != nil {
		go s.hbWriter.StartSync(context.Background())
	}
	go s.offlineDetector()
	go s.startCollectScheduler(context.Background())
	return s
}

// SetEntClient 注入 ent Client（用于外部注入方式）。
func (s *Service) SetEntClient(entClient *ent.Client) {
	s.entClient = entClient
}

// SetTaskQueue 注入 TaskQueue（T11）。
func (s *Service) SetTaskQueue(tq *TaskQueue) {
	s.taskQueue = tq
}

// SetUpgradeManager 注入 UpgradeManager（T12）。
func (s *Service) SetUpgradeManager(um *UpgradeManager) {
	s.upgradeMgr = um
}

// SetTLSEnable 设置是否启用 HTTPS(影响安装脚本的 scheme)。
func (s *Service) SetTLSEnable(enabled bool) {
	s.tlsEnabled = enabled
}

// SetTunnelServer 注入 tunnel server,启用后支持通过 TCP Proxy 下发命令。
func (s *Service) SetTunnelServer(ts *tunnel.Server) {
	s.tunnelServer = ts
}

// SetOnAgentOnboard 注入主机/容器纳管成功后的回调(方案 X:统一经 CMDB 建采集任务)。
func (s *Service) SetOnAgentOnboard(fn func(tenantID, agentID string) error) {
	s.onAgentOnboard = fn
}

// SetGRPCTunnel 注入 gRPC 隧道服务,启用后支持直连 Agent(gRPC)下发命令。
// 直连 Agent 注册时 ProxyId=自身 agentID,会话落在 grpcTunnel.proxies[agentID];
// 经 Proxy 中转时会话落在 tunnelServer(或 gRPC Proxy)的 proxiles[ProxyID]。
func (s *Service) SetGRPCTunnel(gs *tunnel.GRPCServer) {
	s.grpcTunnel = gs
	if gs == nil {
		return
	}
	// 注入指纹去重解析器:gRPC 直连 Agent 注册时据此复用同机既有记录,避免重复建行。
	gs.SetAgentIDResolver(s.resolveGRPCAgentID)
	// 任务清单下发:Agent 注册成功后推送其应执行的任务定义(自定义采集任务)。
	// 基于 collect_tasks 过滤: enabled 且 target_agents 为空(全局)或包含该 Agent。
	gs.SetTaskListProvider(s.buildTaskDefs)
	// 隧道心跳回写:gRPC 直连 Agent 只发隧道内心跳(不走 HTTP heartbeat 端点),
	// 若不在此回写 Redis/DB,agents 列表会永远显示 offline。注册时一并带上
	// OS/IP/CPU/Memory,使 agent_hosts 不再是无资源信息的空壳。
	gs.OnHeartbeat(func(info tunnel.AgentHeartbeatInfo) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// gRPC 直连 Agent 注册时只建内存会话,未落 agent_hosts 表,
		// 导致前端主机列表读不到(此前只有 HTTP Register 才会建行)。
		// 这里在首跳心跳时确保 agent_hosts 已存在该行(按 agentID 去重复用),
		// 使注册即出现在主机列表;后续心跳仅刷新在线状态与资源信息。
		if _, gerr := s.store.Get(ctx, info.AgentID, "default"); gerr != nil {
			host := &AgentHost{
				ID:            info.AgentID,
				TenantID:      "default",
				Name:          info.Hostname,
				Type:          AgentTypeAgent,
				Address:       info.AgentID, // 直连身份即指纹,作为去重键
				OSType:        info.OS,
				OSVersion:     info.OSVersion,
				Version:       info.Version,
				CPU:           info.CPU,
				Memory:        info.Memory,
				Hostname:      info.Hostname,
				ProxyID:       info.AgentID,
				Channels:      []string{"agent"},
				InstallMethod: InstallManual,
				Capabilities:  info.Capabilities,
				IPs:           info.IPs,
				Status:        AgentStatusOnline,
			}
			if rerr := s.store.Register(ctx, host); rerr != nil {
				slog.Warn("tunnel register agent host failed", "agent_id", info.AgentID, "error", rerr)
			}
		}
		req := &HeartbeatRequest{
			Hostname:     info.Hostname,
			OS:           info.OS,
			OSVersion:    info.OSVersion,
			AgentVersion: info.Version,
			CPU:          info.CPU,
			Memory:       info.Memory,
			IPs:          info.IPs,
			Capabilities: info.Capabilities,
		}
		if err := s.hbWriter.Record(ctx, info.AgentID, "default", req); err != nil {
			slog.Warn("tunnel heartbeat write-back failed", "agent_id", info.AgentID, "error", err)
		}
	})
	// 断连即离线:删除 Redis 心跳 key 并回写 DB 状态,UI 立即翻转为 offline。
	gs.OnDisconnect(func(agentID string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.hbWriter.Delete(ctx, agentID)
		if host, err := s.store.Get(ctx, agentID, "default"); err == nil && host != nil {
			host.Status = AgentStatusOffline
			host.UpdatedAt = time.Now()
			if err := s.store.Update(ctx, host); err != nil {
				slog.Warn("mark agent offline failed", "agent_id", agentID, "error", err)
			}
		}
	})
}

// buildTaskDefs 构造指定 Agent 应执行的任务清单(TaskDef 列表)。
// 基于 collect_tasks 过滤: enabled 且 target_agents 为空(全局)或包含该 Agent。
// 注册下发与变更推送共用, 保证两份清单一致。
func (s *Service) buildTaskDefs(agentID string) []*pb.TaskDef {
	tasks := s.collect.List("default")
	defs := make([]*pb.TaskDef, 0, len(tasks))
	for _, t := range tasks {
		if !t.Enabled {
			continue
		}
		// 内置类型(空脚本)由 Agent 本地调度器执行,投影任务不通过 TaskSync 下发
		// (Agent 端 applyTaskList 也会跳过空脚本),避免无意义推送。
		if t.Script == "" {
			continue
		}
		if len(t.TargetAgents) > 0 && !slices.Contains(t.TargetAgents, agentID) {
			continue
		}
		defs = append(defs, &pb.TaskDef{
			TaskId:      t.ID,
			Name:        t.Name,
			Type:        string(t.Type),
			IntervalSec: int64(t.IntervalSec),
			Script:      t.Script,
			ScriptType:  t.ScriptType,
		})
	}
	return defs
}

// pushTaskListToOnline 在 collect_tasks 变更后主动推送最新清单给在线 Agent。
// 全局任务(无 target_agents)推送所有在线 Agent; 指定目标任务只推送目标中在线的。
// 增量同步无需 Agent 重连; 离线 Agent 下次注册时由 taskListProvider 全量补齐。
func (s *Service) pushTaskListToOnline() {
	if s.grpcTunnel == nil {
		return
	}
	push := func(agentID string) {
		defs := s.buildTaskDefs(agentID)
		// 无任务也要推送空清单: 通知 Agent 移除已删除任务(enabled=false 语义)。
		if err := s.grpcTunnel.PushTaskList(agentID, defs); err != nil {
			slog.Debug("task list push skipped (agent offline)", "agent_id", agentID, "error", err)
			return
		}
		slog.Info("task list pushed on change", "agent_id", agentID, "tasks", len(defs))
	}
	for _, id := range s.grpcTunnel.OnlineAgentIDs() {
		push(id)
	}
}

// resolveGRPCAgentID 在 gRPC 直连 Agent (或 TCP 隧道 Agent) 注册/心跳时执行主机身份解析与
// 克隆冲突检测,返回(应使用的规范 host_id, 是否需要重置为独立主机)。
// 决策逻辑:
//  1. forceNew → 跳过复用,直接采用入站 ID 为新主机(Agent 在收到 RESET_REGISTRATION 后重注册时置此标志)。
//  2. 已存在该 ID → 直接复用(稳定指纹 ID 重启场景)。
//  3. 不存在 → 按 hostname/IP 找疑似同机记录,复用规范 ID(优先在线、其次最近心跳)。
//  4. 对最终 host_id 查 boot_id 碰撞:同一 id 的两次注册心跳的 boot_id 不同且窗口 5min 内
//     → 判定为 VM 镜像克隆冲突,要求新到者执行 RESET_REGISTRATION(追加随机盐重注册为新主机)。
func (s *Service) resolveGRPCAgentID(ctx context.Context, tenantID, incomingID, hostname string, ips []string, bootID string, forceNew bool) (string, bool) {
	if incomingID == "" {
		return incomingID, false
	}
	// 1) forceNew:Agent 在收到 RESET_REGISTRATION 后以全新指纹(含随机盐)重新注册,
	// 跳过按 hostname/IP 的复用逻辑,直接接纳为独立新主机以避免无限重置循环。
	if forceNew {
		return incomingID, s.recordBootID(incomingID, bootID)
	}
	// 2) 已存在该 ID 的直接复用(稳定指纹 ID 重启场景)
	agentID := incomingID
	if h, err := s.store.Get(ctx, incomingID, tenantID); err != nil || h == nil {
		// 3) 按 hostname / IP 找疑似同机记录,复用规范 ID(优先在线、其次最近心跳)
		hosts, err := s.store.List(ctx, tenantID)
		if err != nil {
			return incomingID, false
		}
		bestID := ""
		bestScore := -1
		var bestHB time.Time
		for _, h := range hosts {
			if h.ID == incomingID {
				continue
			}
			matched := false
			if hostname != "" && h.Hostname == hostname {
				matched = true
			} else {
				for _, ip := range ips {
					for _, hip := range h.IPs {
						if ip != "" && ip == hip {
							matched = true
							break
						}
					}
					if matched {
						break
					}
				}
			}
			if !matched {
				continue
			}
			score := 0
			if h.Status == AgentStatusOnline {
				score += 2
			}
			var hb time.Time
			if h.LastHeartbeat != nil {
				hb = *h.LastHeartbeat
				score += 1
			}
			if score > bestScore || (score == bestScore && hb.After(bestHB)) {
				bestID = h.ID
				bestScore = score
				bestHB = hb
			}
		}
		if bestID != "" {
			slog.Info("gRPC agent reuses existing record by fingerprint",
				"incoming_id", incomingID, "reused_id", bestID, "hostname", hostname)
			agentID = bestID
		}
	}
	// 4) boot_id 碰撞检测:同一 host_id 的不同 boot_id 在短窗口内同时在线 → 克隆冲突
	collision := s.recordBootID(agentID, bootID)
	if collision {
		slog.Warn("clone collision detected: same host_id from different boot_id",
			"host_id", agentID, "boot_id", bootID, "hostname", hostname)
	}
	return agentID, collision
}

// recordBootID 记录主机最近一次上报的 boot_id 与时刻,并返回是否为克隆冲突:
// 若该 hostID 已有记录、boot_id 不同且上次记录在 5 分钟内 → true(需触发 RESET_REGISTRATION)。
// 非冲突或无记录时更新记录并返回 false。
func (s *Service) recordBootID(hostID, bootID string) bool {
	if bootID == "" {
		return false
	}
	s.bootIDMu.Lock()
	defer s.bootIDMu.Unlock()
	if s.hostBootID == nil {
		s.hostBootID = make(map[string]bootSeen)
	}
	prev, ok := s.hostBootID[hostID]
	now := time.Now()
	if ok {
		if prev.id != bootID && now.Sub(prev.seen) < 5*time.Minute {
			return true // 冲突:不覆盖,保留��记录
		}
	}
	s.hostBootID[hostID] = bootSeen{id: bootID, seen: now}
	return false
}

// SetWhitelist 注入本租户的命令白名单(§6)。传 nil 表示未启用(放行一切)。
func (s *Service) SetWhitelist(wl *command.Whitelist) {
	s.whitelistMu.Lock()
	s.whitelist = wl
	s.whitelistMu.Unlock()
}

// SetFileTokenSecret 配置文件下载短期令牌密钥(§6),供 P1 文件代理签发/校验。
func (s *Service) SetFileTokenSecret(secret string) {
	s.fileTokenSecret = secret
}

// SetFileStoreDir 配置文件分发中心存储目录(§5)。
func (s *Service) SetFileStoreDir(dir string) {
	s.fileStoreDir = dir
}

// SetServerBaseURL 配置 Server 对外基址(如 http://host:8080),
// 用于拓扑感知下发"直连 Agent"的下载地址。留空则无法生成直连 URL。
func (s *Service) SetServerBaseURL(url string) {
	s.serverBaseURL = strings.TrimRight(url, "/")
}

// Close 停止后台离线检测 goroutine。
func (s *Service) Close() { close(s.stopCh) }

// offlineDetector 周期性地将长时间未心跳的 agent 标记为离线。
// Redis 模式下：心跳 key TTL 到期自动消失=离线，此处只做 PG 批量同步。
// 无 Redis 时：回退到 store.MarkOfflineAgents 基于 last_seen 时间戳判断。
func (s *Service) offlineDetector() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			ctx := context.Background()
			if s.hbWriter != nil && s.hbWriter.rdb != nil {
				// Redis 模式：自动过期即离线，不需要主动标记
				continue
			}
			n, _ := s.store.MarkOfflineAgents(ctx, "default", OfflineThreshold)
			if n > 0 {
				slog.Info("agents marked offline", "count", n)
			}
		}
	}
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
	// §5 文件分发:中心存储文件拉取端点(令牌保护,支持 Range 断点续传)。
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks/{task_id}", s.handleCompleteTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/circuit-breaker", s.handleGetCB)
	mux.HandleFunc("PUT /api/v1/agents/{id}/circuit-breaker", s.handleSetCB)
	mux.HandleFunc("GET /api/v1/agents/{id}/resource-guard", s.handleGetRG)
	mux.HandleFunc("PUT /api/v1/agents/{id}/resource-guard", s.handleSetRG)
	mux.HandleFunc("POST /api/v1/agents/{id}/restart", s.handleRestartAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/upgrade", s.handleUpgradeAgent)
	// Agent 全局版本一致性管理
	mux.HandleFunc("GET /api/v1/agents/version-policy", s.handleGetVersionPolicy)
	mux.HandleFunc("PUT /api/v1/agents/version-policy", s.handleSetVersionPolicy)
	mux.HandleFunc("POST /api/v1/agents/batch-upgrade", s.handleBatchUpgrade)
	mux.HandleFunc("POST /api/v1/agents/{id}/uninstall", s.handleUninstallAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/cmdb", s.handleCMDBReport)
	// 采集任务管理(CMDB/监控数据采集上报)
	mux.HandleFunc("POST /api/v1/collect-tasks", s.handleCreateCollectTask)
	mux.HandleFunc("GET /api/v1/collect-tasks", s.handleListCollectTasks)
	mux.HandleFunc("GET /api/v1/collect-tasks/{id}/runs", s.handleCollectTaskRuns)
	mux.HandleFunc("GET /api/v1/collect-metrics", s.handleQueryCollectMetrics)
	mux.HandleFunc("PUT /api/v1/collect-tasks/{id}", s.handleUpdateCollectTask)
	mux.HandleFunc("DELETE /api/v1/collect-tasks/{id}", s.handleDeleteCollectTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/download-tasks", s.handleDownloadTasks)
	// §5 文件分发:创建下载任务,签发短期令牌并生成拓扑感知下载地址(POST)。
	mux.HandleFunc("POST /api/v1/agents/{id}/download-tasks", s.handleCreateDownloadTask)
	// §5 文件上传与管理
	mux.HandleFunc("POST /api/v1/files/upload", s.handleUploadFile)
	mux.HandleFunc("GET /api/v1/files", s.handleListFiles)
	mux.HandleFunc("DELETE /api/v1/files/{file_id}", s.handleDeleteFile)
	mux.HandleFunc("GET /api/v1/files/{file_id}/meta", s.handleFileMeta)
	// 命令白名单(Agent 启动拉取,§6)
	mux.HandleFunc("GET /api/v1/agents/{id}/whitelist", s.handleGetWhitelist)
	// Sandbox:沙箱=打在真实 Agent 上的标签,执行时在目标主机上创建真实隔离环境
	mux.HandleFunc("GET /api/v1/agents/sandbox", s.handleListSandboxAgents)
	mux.HandleFunc("PUT /api/v1/agents/{id}/sandbox", s.handleSetSandboxLabel)
	mux.HandleFunc("POST /api/v1/sandbox/exec", s.handleExecSandbox)
	// 疑似重复记录合并:将 {id}(旧/重复记录)合并进 target_id(规范记录)
	mux.HandleFunc("POST /api/v1/agents/{id}/merge", s.handleMerge)
	// Remote Execution (SSH)
	mux.HandleFunc("POST /api/v1/agents/batch-execute", s.handleBatchExecute)
	mux.HandleFunc("GET /api/v1/agent-tasks/{task_id}", s.handleGetExecTask)
	mux.HandleFunc("POST /api/v1/agents/ssh-execute", s.handleSSHExecute)
	// Proxy CRUD
	mux.HandleFunc("GET /api/v1/proxies", s.handleListProxies)
	mux.HandleFunc("POST /api/v1/proxies", s.handleRegisterProxy)
	mux.HandleFunc("GET /api/v1/proxies/{id}", s.handleGetProxy)
	mux.HandleFunc("PUT /api/v1/proxies/{id}", s.handleUpdateProxy)
	mux.HandleFunc("DELETE /api/v1/proxies/{id}", s.handleDeleteProxy)
}

// ScriptRunOptions 是脚本下发的可选参数集合。
// 独立成结构体,以便在不破坏 RunScriptOnAgent 既有签名(被 cloud.K8sAgentRunner
// 等鸭子类型依赖)的前提下扩展沙箱等能力。
type ScriptRunOptions struct {
	Timeout int    // 秒,<=0 时取 300
	RunAs   string // 以指定系统用户身份执行
	// Sandbox 为 true 时,Agent 端在目标主机上创建真实隔离环境执行
	// (独立临时工作目录 + 净化环境变量 + 独立进程组;python 另建 venv)。
	Sandbox      bool
	ScriptType   string   // shell(默认) | python | bat
	Requirements []string // python 沙箱内 pip 安装的依赖
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
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'

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

// generateProxyInstallScript 生成 Proxy 的 Linux/macOS 安装脚本。
func generateProxyInstallScript(serverAddr, osType, arch string) string {
	binaryName := "opsproxy"
	if osType == "darwin" {
		binaryName = "opsproxy-darwin"
	}
	installDir := "/opt/opsorigin/proxy"
	serviceName := "opsorigin-proxy"

	return fmt.Sprintf(`#!/bin/bash
# OpsOrigin Proxy Installer
# Usage: bash install_proxy.sh

set -e

SERVER_ADDR="%s"
INSTALL_DIR="%s"
SERVICE_NAME="%s"
BINARY_NAME="%s"
ARCH="%s"

echo "========================================="
echo "  OpsOrigin Proxy Installer"
echo "========================================="
echo ""

# Check root
if [ "$EUID" -ne 0 ]; then
  echo "[ERROR] Please run as root (sudo bash install_proxy.sh)"
  exit 1
fi

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
echo "[INFO] Detected OS: $OS ($ARCH)"

# Create install directory
echo "[INFO] Creating install directory: $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"

# Download proxy binary
echo "[INFO] Downloading proxy binary from server..."
curl -fsSL "${SERVER_ADDR}/api/v1/agents/download?os=%s&arch=${ARCH}&type=proxy-bin" -o "$INSTALL_DIR/$BINARY_NAME"
chmod +x "$INSTALL_DIR/$BINARY_NAME"
echo "[INFO] Proxy binary installed to: $INSTALL_DIR/$BINARY_NAME"

# Extract host:port from SERVER_ADDR
SERVER_HOST=$(echo "$SERVER_ADDR" | sed 's|^[a-z]*://||')

# Create config file
echo "[INFO] Creating configuration file..."
cat > "$INSTALL_DIR/config.yaml" << EOF
core:
  address: "$SERVER_ADDR"
proxy:
  id: ""
  listen: ":9090"
  agent_listen: ":9091"
log:
  level: info
EOF
echo "[INFO] Configuration created at: $INSTALL_DIR/config.yaml"

# Create systemd service (Linux) or launchd plist (macOS)
if [ "$OS" = "linux" ]; then
  echo "[INFO] Creating systemd service..."
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" << EOF
[Unit]
Description=OpsOrigin Proxy
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_DIR/$BINARY_NAME -config $INSTALL_DIR/config.yaml
Restart=always
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "$SERVICE_NAME"
  systemctl start "$SERVICE_NAME"
  echo "[INFO] Service started: $SERVICE_NAME"

elif [ "$OS" = "darwin" ]; then
  echo "[INFO] Creating launchd plist..."
  PLIST_PATH="/Library/LaunchDaemons/com.opsorigin.proxy.plist"
  cat > "$PLIST_PATH" << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.opsorigin.proxy</string>
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
    <string>/var/log/opsorigin-proxy.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/opsorigin-proxy.log</string>
</dict>
</plist>
EOF
  launchctl unload "$PLIST_PATH" 2>/dev/null || true
  launchctl load "$PLIST_PATH"
  echo "[INFO] Launchd service installed: com.opsorigin.proxy"
fi

echo ""
echo "========================================="
echo "  Installation Complete!"
echo "========================================="
echo ""
echo "  Install Dir:  $INSTALL_DIR"
echo "  Config File:  $INSTALL_DIR/config.yaml"
echo "  Log File:     /var/log/opsorigin-proxy.log"
echo ""
echo "  Service:      $SERVICE_NAME"
echo "  Status:       systemctl status $SERVICE_NAME"
echo "  Logs:         journalctl -u $SERVICE_NAME -f"
echo ""
echo "========================================="
`, serverAddr, installDir, serviceName, binaryName, arch, osType)
}

// generateWindowsProxyInstallScript 生成 Proxy 的 Windows 安装脚本。
func generateWindowsProxyInstallScript(serverAddr, arch string) string {
	return fmt.Sprintf(`# OpsOrigin Proxy Installer for Windows
# Usage: Right-click -> Run with PowerShell, or: powershell -ExecutionPolicy Bypass -File install_proxy.ps1

$ErrorActionPreference = "Stop"

$ServerAddr = "%s"
$Arch = "%s"
$InstallDir = "$env:LOCALAPPDATA\OpsOrigin\Proxy"
$ServiceName = "OpsOriginProxy"

Write-Host "=========================================" -ForegroundColor Green
Write-Host "  OpsOrigin Proxy Installer"
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""

# Check admin
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
    Write-Host "[ERROR] Please run as Administrator" -ForegroundColor Red
    pause
    exit 1
}

# Create install directory
Write-Host "[INFO] Creating install directory: $InstallDir"
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Download proxy binary
Write-Host "[INFO] Downloading proxy binary from server..."
$binaryUrl = "$ServerAddr/api/v1/agents/download?os=windows&arch=$Arch&type=proxy-bin"
$binaryPath = "$InstallDir\proxy.exe"
try {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    Invoke-WebRequest -Uri $binaryUrl -OutFile $binaryPath -UseBasicParsing
} catch {
    Write-Host "[ERROR] Failed to download: $_" -ForegroundColor Red
    pause
    exit 1
}
Write-Host "[INFO] Proxy binary downloaded to: $binaryPath"

# Create config file
Write-Host "[INFO] Creating configuration file..."
$configContent = @"
core:
  address: "$ServerAddr"
proxy:
  id: ""
  listen: ":9090"
  agent_listen: ":9091"
log:
  level: info
"@
$configPath = "$InstallDir\config.yaml"
Set-Content -Path $configPath -Value $configContent -Encoding UTF8
Write-Host "[INFO] Configuration created at: $configPath"

# Install Windows service
Write-Host "[INFO] Installing Windows service..."
New-Service -Name $ServiceName -BinaryPathName "$InstallDir\proxy.exe -config $configPath" -DisplayName "OpsOrigin Proxy" -StartupType Automatic -Description "OpsOrigin Proxy - Agent relay and tunnel service"

Start-Service $ServiceName
Write-Host "[INFO] Service started: $ServiceName"

Write-Host ""
Write-Host "=========================================" -ForegroundColor Green
Write-Host "  Installation Complete!"
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""
Write-Host "  Install Dir:  $InstallDir"
Write-Host "  Config File:  $configPath"
Write-Host "  Service:      $ServiceName"
Write-Host ""
Write-Host "  Check Status: Get-Service $ServiceName"
Write-Host ""
Write-Host "=========================================" -ForegroundColor Green
Write-Host ""
pause
`, serverAddr, arch)
}

// ---- Proxy handlers ----

// ---- Circuit breaker ----

// cmdbReportPayload 是 Agent 上报 CMDB 数据的载荷结构(供采集结果解析复用)。
type cmdbReportPayload struct {
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

// applyCMDBReport 把 CMDB 采集数据合并进 AgentHost(供上报端点与采集任务结果复用)。
func applyCMDBReport(host *AgentHost, report *cmdbReportPayload) {
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
}

// buildFileURL 依据基址生成含短期令牌的下载地址。base 形如 http://host:port/api/v1。
func buildFileURL(base, fileID, tok string) string {
	return fmt.Sprintf("%s/files/%s?token=%s", strings.TrimRight(base, "/"), fileID, tok)
}

// downloadTaskRequest 创建下载任务的请求体。
type downloadTaskRequest struct {
	FileID           string `json:"file_id"`
	FilePath         string `json:"file_path"`
	ExpectedChecksum string `json:"expected_checksum,omitempty"`
	ChecksumAlgo     string `json:"checksum_algo,omitempty"`
	TTLSec           int    `json:"ttl_sec,omitempty"` // 令牌有效期(秒),0 ⇒ 上限 10 分钟
}

// ------- Agent 全局版本一致性 -------

// ------- §5 文件上传与管理 -------

type fileMeta struct {
	ID           string `json:"id"`
	OriginalName string `json:"original_name"`
	StoredPath   string `json:"-"`
	SizeBytes    int64  `json:"size_bytes"`
	Checksum     string `json:"checksum"`
	ChecksumAlgo string `json:"checksum_algo"`
	TenantID     string `json:"tenant_id"`
	CreatedAt    string `json:"created_at"`
}
