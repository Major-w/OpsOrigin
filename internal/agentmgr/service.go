package agentmgr

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	pb "github.com/opsorigin/core/api/proto"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/ent"
	entFile "github.com/opsorigin/core/internal/ent/file"
	"github.com/opsorigin/core/internal/filelimit"
	"github.com/opsorigin/core/internal/remoteexec"
	"github.com/opsorigin/core/internal/token"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/pkg/validate"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/ssh"
	"slices"

	"github.com/opsorigin/core/pkg/httputil"
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

// verifyCommand 服务端命令白名单校验。whitelist 为 nil(未启用)时直接放行。
func (s *Service) verifyCommand(command string) error {
	s.whitelistMu.RLock()
	wl := s.whitelist
	s.whitelistMu.RUnlock()
	if wl == nil {
		return nil
	}
	return wl.Verify(command)
}

// CleanupLegacySandboxHosts 清理历史遗留的"伪沙箱 Agent"记录。
//
// 旧实现(已废弃)会在 Core 启动时向 agent_hosts 注册一条 type=sandbox 的伪记录
// (名称形如 "xxx (Sandbox)"),它与 Core 同进程、不发心跳,却被列表接口特判为恒
// 在线,在 UI 上表现为一条无心跳的"幽灵主机"。
//
// 新设计:沙箱是打在**真实 Agent**上的一个标签(SandboxLabelKey),脚本在该 Agent
// 所在的目标主机上于真实隔离环境中执行。因此这里在启动时把历史伪记录删除,
// 避免继续污染主机列表。
func (s *Service) CleanupLegacySandboxHosts() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hosts, err := s.store.List(ctx, "default")
	if err != nil {
		slog.Warn("cleanup legacy sandbox hosts: list failed", "error", err)
		return
	}
	removed := 0
	for _, h := range hosts {
		if h.Type != AgentTypeSandbox {
			continue
		}
		if err := s.store.Delete(ctx, h.ID, h.TenantID); err != nil {
			slog.Warn("cleanup legacy sandbox host failed", "id", h.ID, "error", err)
			continue
		}
		removed++
		slog.Info("removed legacy pseudo sandbox agent", "id", h.ID, "name", h.Name)
	}
	if removed > 0 {
		slog.Info("legacy sandbox hosts cleaned up", "count", removed)
	}
}

// listSandboxAgents 返回本租户内所有被打上沙箱标签且在线的真实 Agent。
func (s *Service) listSandboxAgents(ctx context.Context, tenantID string) ([]*AgentHost, error) {
	hosts, err := s.store.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	var onlineSet map[string]bool
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ids = append(ids, h.ID)
		}
		onlineSet = s.hbWriter.BulkOnlineStatus(ctx, ids)
	}
	now := time.Now()
	out := make([]*AgentHost, 0, 4)
	for _, h := range hosts {
		if !h.IsSandbox() {
			continue
		}
		copyHost := *h
		if onlineSet != nil {
			if onlineSet[h.ID] {
				copyHost.Status = AgentStatusOnline
			} else {
				copyHost.Status = AgentStatusOffline
			}
		} else if copyHost.LastHeartbeat != nil && now.Sub(*copyHost.LastHeartbeat) > OfflineThreshold {
			copyHost.Status = AgentStatusOffline
		}
		out = append(out, &copyHost)
	}
	return out, nil
}

// pickSandboxAgent 选择一个可用的沙箱 Agent。
// preferID 非空时校验其确实带沙箱标签且在线;否则取第一个在线的沙箱 Agent。
func (s *Service) pickSandboxAgent(ctx context.Context, tenantID, preferID string) (*AgentHost, error) {
	agents, err := s.listSandboxAgents(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if preferID != "" {
		for _, a := range agents {
			if a.ID == preferID {
				if a.Status != AgentStatusOnline {
					return nil, fmt.Errorf("指定的沙箱 Agent %s 当前离线", preferID)
				}
				return a, nil
			}
		}
		return nil, fmt.Errorf("Agent %s 未被标记为沙箱环境,请先在主机列表中为其开启沙箱标签", preferID)
	}
	for _, a := range agents {
		if a.Status == AgentStatusOnline {
			return a, nil
		}
	}
	return nil, fmt.Errorf("没有可用的沙箱 Agent:请先在主机列表中为部署 Server 的主机 Agent 开启沙箱标签,并确保其在线")
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
	// §5 文件分发:中心存储文件拉取端点(令牌保护,支持 Range 断点续传)。
	mux.HandleFunc("GET /api/v1/files/{file_id}", s.handleServeFile)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/v1/agents/{id}/tasks/{task_id}", s.handleCompleteTask)
	mux.HandleFunc("GET /api/v1/agents/{id}/circuit-breaker", s.handleGetCB)
	mux.HandleFunc("PUT /api/v1/agents/{id}/circuit-breaker", s.handleSetCB)
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

// handleListSandboxAgents 返回所有被打上沙箱标签的真实 Agent。
// 前端"沙箱环境执行"下拉框据此渲染可选环境。
func (s *Service) handleListSandboxAgents(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	agents, err := s.listSandboxAgents(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	online := 0
	for _, a := range agents {
		if a.Status == AgentStatusOnline {
			online++
		}
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"exists": online > 0,
		"count":  len(agents),
		"online": online,
		"agents": agents,
	})
}

// handleSetSandboxLabel 给指定的真实 Agent 打上/摘掉"沙箱环境"标签。
// 这取代了旧的"Core 进程内注册伪沙箱 Agent"做法:沙箱不是一台新主机,
// 而是既有 Agent 上的一个能力标记。
// handleSetSandboxLabel 设置/取消 Agent 的沙箱标签(PUT /agents/{id}/sandbox)。
func (s *Service) handleSetSandboxLabel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	if host.Type == AgentTypeAgentless {
		httputil.WriteError(w, http.StatusBadRequest, "Agentless 主机无本地 Agent,无法承载隔离沙箱环境")
		return
	}
	host.SetSandbox(req.Enabled)
	host.UpdatedAt = time.Now()
	if err := s.store.Update(r.Context(), host); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("sandbox label updated", "agent_id", id, "enabled", req.Enabled)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "sandbox": req.Enabled})
}

// ApplySandboxPool 把"Server 所在主机"的 Agent 自动标记为沙箱环境(默认沙箱池)。
// 匹配规则:
//   - 主机名(hostname)等于本机主机名的 Agent;
//   - Agent ID 命中环境变量 OPSORIGIN_SANDBOX_AGENTS(逗号分隔)列表。
//
// 可通过 OPSORIGIN_SANDBOX_AUTO=0 关闭自动标记。
func (s *Service) ApplySandboxPool(ctx context.Context) {
	if os.Getenv("OPSORIGIN_SANDBOX_AUTO") == "0" {
		return
	}
	hostname, err := os.Hostname()
	if err != nil {
		hostname = ""
	}
	cfgSet := map[string]bool{}
	if v := os.Getenv("OPSORIGIN_SANDBOX_AGENTS"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				cfgSet[p] = true
			}
		}
	}
	agents, err := s.store.List(ctx, "default")
	if err != nil {
		slog.Warn("sandbox pool: list agents failed", "err", err)
		return
	}
	marked := 0
	for _, a := range agents {
		if a.Type == AgentTypeAgentless {
			continue
		}
		match := cfgSet[a.ID]
		if hostname != "" && (a.Hostname == hostname || a.Name == hostname) {
			match = true
		}
		if match && !a.IsSandbox() {
			a.SetSandbox(true)
			a.UpdatedAt = time.Now()
			if err := s.store.Update(ctx, a); err == nil {
				marked++
				slog.Info("sandbox pool: marked agent as sandbox", "agent_id", a.ID, "hostname", a.Hostname)
			}
		}
	}
	if marked > 0 {
		slog.Info("sandbox pool applied", "marked", marked)
	}
}

// handleBatchExecute 在多台主机上并发执行命令(SSH 远程执行)。
func (s *Service) handleBatchExecute(w http.ResponseWriter, r *http.Request) {
	var req remoteexec.BatchExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Hosts) == 0 {
		httputil.WriteError(w, http.StatusBadRequest, "hosts is required")
		return
	}
	if req.Command == "" {
		httputil.WriteError(w, http.StatusBadRequest, "command is required")
		return
	}

	result := s.remoteExec.Execute(r.Context(), req)
	httputil.WriteJSON(w, http.StatusOK, result)
}

// handleGetExecTask 查询批量执行任务状态。
func (s *Service) handleGetExecTask(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("task_id")
	if taskID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "task_id is required")
		return
	}

	result, ok := s.remoteExec.GetTask(taskID)
	if !ok {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}

	httputil.WriteJSON(w, http.StatusOK, result)
}

// handleSSHExecute 在单台主机上执行命令。
// 支持三种目标：
//   - 默认：通过 SSH 直连目标主机（Core 出站）。
//   - proxy_id 指定：目标位于隔离网段，由 Proxy 代为 SSH 执行（见 proxy 契约）。
//   - sandbox=true：不指定目标主机，直接在 Server 本机沙箱环境执行（调试/冒烟）。
func (s *Service) handleSSHExecute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port,omitempty"`
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
		SSHKey   string `json:"ssh_key,omitempty"`
		Command  string `json:"command"`
		Timeout  int    `json:"timeout,omitempty"`
		ProxyID  string `json:"proxy_id,omitempty"` // 指定则走 Proxy 代为执行
		Sandbox  bool   `json:"sandbox,omitempty"`  // 勾选沙箱环境执行
		// SandboxAgentID 可选:指定用哪台带沙箱标签的 Agent 执行,留空自动挑选在线的。
		SandboxAgentID string `json:"sandbox_agent_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		httputil.WriteError(w, http.StatusBadRequest, "command is required")
		return
	}

	// §6 命令白名单:服务端下发前强制校验(直连 / 经 Proxy / 沙箱 三条路径统一拦截)。
	if err := s.verifyCommand(req.Command); err != nil {
		slog.Warn("ssh-execute denied by whitelist", "host", req.Host, "error", err)
		httputil.WriteError(w, http.StatusForbidden, "command denied by whitelist: "+err.Error())
		return
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// 沙箱执行：不选择普通目标主机，改为下发到一台带沙箱标签的真实 Agent，
	// 由其在所在主机上创建真实隔离环境执行。
	if req.Sandbox {
		tenantID := tenantFromCtx(r.Context())
		agent, err := s.pickSandboxAgent(r.Context(), tenantID, req.SandboxAgentID)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		start := time.Now()
		output, exitCode, execErr := s.RunScriptOnAgentWithOptions(r.Context(), agent.ID, tenantID,
			req.Command, nil, ScriptRunOptions{
				Timeout: int(timeout.Seconds()),
				Sandbox: true,
			})
		resp := map[string]any{
			"host":      agent.Name,
			"agent_id":  agent.ID,
			"output":    output,
			"exit_code": exitCode,
			"duration":  time.Since(start).Milliseconds(),
			"via":       "sandbox-agent",
		}
		if execErr != nil {
			resp["error"] = execErr.Error()
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
		return
	}

	port := req.Port
	if port == 0 {
		port = 22
	}

	// 通过 Proxy 执行：由 Proxy 代为 SSH（目标在隔离网段，Core 不可达）
	if req.ProxyID != "" && s.tunnelServer != nil {
		out, exitCode, execErr := s.execViaProxy(req.ProxyID, "ssh_exec", map[string]any{
			"host":     req.Host,
			"port":     port,
			"username": req.Username,
			"password": req.Password,
			"ssh_key":  req.SSHKey,
			"command":  req.Command,
		}, int32(timeout.Seconds()))
		if execErr == nil {
			httputil.WriteJSON(w, http.StatusOK, map[string]any{
				"host":      req.Host,
				"port":      port,
				"output":    out,
				"exit_code": exitCode,
				"via":       "proxy",
			})
			return
		}
		slog.Warn("agentless via proxy failed, fallback to direct ssh",
			"error", execErr, "proxy_id", req.ProxyID)
		// 失败降级为直连
	}

	if req.Host == "" {
		httputil.WriteError(w, http.StatusBadRequest, "host is required when not using sandbox")
		return
	}

	cred := remoteexec.Credential{
		User:     req.Username,
		Password: req.Password,
		Key:      req.SSHKey,
	}

	execReq := remoteexec.ExecRequest{
		Host:       req.Host,
		Port:       port,
		Credential: cred,
		Command:    req.Command,
		Timeout:    timeout,
	}

	result := s.remoteExec.ExecuteSingle(r.Context(), execReq)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"host":      result.Host,
		"port":      result.Port,
		"output":    result.Output,
		"exit_code": result.ExitCode,
		"error":     result.Error,
		"duration":  result.Duration.Milliseconds(),
		"via":       "direct",
	})
}

// execViaProxy 通过 Proxy 隧道代为执行命令（Agentless 目标在隔离网段时）。
// 约定：proxy 侧收到 CommandRequest 后，按 action 执行：
//   - "ssh_exec"：以 payload 中的 host/port/user/key 做 SSH 并在目标上运行 command。
//   - "ssh_test"：仅做连通性探测。
//
// 返回结构化结果经 CommandResponse.Output（JSON：{output,exit_code,error}）回传。
func (s *Service) execViaProxy(proxyID, action string, payload any, timeoutSec int32) (output string, exitCode int, err error) {
	body, mErr := json.Marshal(payload)
	if mErr != nil {
		return "", -1, fmt.Errorf("marshal proxy payload: %w", mErr)
	}
	cmd := &tunnel.CommandRequest{
		CommandID:     uuid.NewString(),
		TargetID:      proxyID,
		Action:        action,
		Payload:       body,
		TimeoutSecond: timeoutSec,
	}
	resp, e := s.tunnelServer.SendCommand(proxyID, cmd)
	if e != nil {
		return "", -1, e
	}
	if resp == nil || !resp.Success {
		msg := ""
		if resp != nil {
			msg = resp.Error
		}
		return "", -1, fmt.Errorf("proxy exec failed: %s", msg)
	}
	// 尽力解析结构化结果；失败则整体作为输出
	var structured struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(resp.Output, &structured) == nil && structured.Output != "" {
		return structured.Output, structured.ExitCode, nil
	}
	return string(resp.Output), 0, nil
}

// RunScriptOnAgent 通过 Tunnel 把脚本下发到指定 Agent 所在被管主机执行，
// 并同步等待结果返回。content 为脚本正文，params 透传给 Agent 端执行环境。
// 这是 automation/script 引擎的 AgentRunner 实现，使脚本真正在主机上运行
// （而非 Core 本机 os/exec），并复用 Agent 端命令白名单校验(§6)。
// dispatchCommand 选择一个已注册该 key 会话的 tunnel server 下发命令：
// 优先 gRPC(直连 Agent 或 gRPC 代理)，回退 TCP(TCP 代理)。
// key 语义：直连 Agent 的会话 key 即其 agentID(gRPC 直连时注册 ProxyId=agentID)；
// 经代理则 key 为 host.ProxyID(代理 ID)。
func (s *Service) dispatchCommand(ctx context.Context, key string, cmd *tunnel.CommandRequest) (*tunnel.CommandResponse, error) {
	if s.grpcTunnel != nil {
		if s.grpcTunnel.HasProxy(key) {
			return s.grpcTunnel.SendCommand(ctx, key, cmd)
		}
	}
	if s.tunnelServer != nil {
		if s.tunnelServer.HasProxy(key) {
			return s.tunnelServer.SendCommand(key, cmd)
		}
	}
	return nil, fmt.Errorf("agent/proxy %s not connected on any tunnel", key)
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

func (s *Service) RunScriptOnAgent(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, runAs string) (string, int, error) {
	return s.RunScriptOnAgentWithOptions(ctx, agentID, tenantID, content, params,
		ScriptRunOptions{Timeout: timeout, RunAs: runAs})
}

// RunScriptOnAgentSandboxed 以沙箱模式把脚本下发到带沙箱标签的真实 Agent,
// 由其在目标主机上创建真实隔离环境执行。实现 script.SandboxRunner。
func (s *Service) RunScriptOnAgentSandboxed(ctx context.Context, agentID, tenantID, content string, params map[string]string, timeout int, scriptType string, requirements []string) (string, int, error) {
	return s.RunScriptOnAgentWithOptions(ctx, agentID, tenantID, content, params, ScriptRunOptions{
		Timeout:      timeout,
		Sandbox:      true,
		ScriptType:   scriptType,
		Requirements: requirements,
	})
}

// PickSandboxAgentID 挑选一台可用的沙箱 Agent 并返回其 ID。实现 script.SandboxRunner。
func (s *Service) PickSandboxAgentID(ctx context.Context, tenantID, preferID string) (string, error) {
	agent, err := s.pickSandboxAgent(ctx, tenantID, preferID)
	if err != nil {
		return "", err
	}
	return agent.ID, nil
}

// RunScriptOnAgentWithOptions 是 RunScriptOnAgent 的可扩展版本,支持沙箱隔离执行。
func (s *Service) RunScriptOnAgentWithOptions(ctx context.Context, agentID, tenantID, content string, params map[string]string, opts ScriptRunOptions) (string, int, error) {
	if s.tunnelServer == nil && s.grpcTunnel == nil {
		return "", -1, fmt.Errorf("no tunnel server configured: cannot dispatch script to agent %s", agentID)
	}
	host, err := s.store.Get(ctx, agentID, tenantID)
	if err != nil || host == nil {
		return "", -1, fmt.Errorf("agent %s not found (tenant=%s)", agentID, tenantID)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 300
	}
	// 请求沙箱执行时,目标 Agent 必须确实带沙箱标签,避免脚本在未隔离的生产
	// 主机上裸跑。
	if opts.Sandbox && !host.IsSandbox() {
		return "", -1, fmt.Errorf("Agent %s 未被标记为沙箱环境,拒绝以沙箱方式执行", agentID)
	}
	// 沙箱高危命令硬拦截(Core 侧防御):即便 Agent 端未强制,下发前先扫描。
	// 命中即拒绝,避免高危脚本进入隔离环境执行。
	if opts.Sandbox {
		if blocked := command.BlockedInSandbox(content); len(blocked) > 0 {
			ids := make([]string, 0, len(blocked))
			for _, m := range blocked {
				ids = append(ids, fmt.Sprintf("%s(%s)", m.RuleID, m.Desc))
			}
			return "", -1, fmt.Errorf("沙箱执行被拒绝:脚本包含高危命令 %s", strings.Join(ids, ", "))
		}
	}
	p := make(map[string]any, len(params))
	for k, v := range params {
		p[k] = v
	}
	task := &TaskRequest{
		Action:       "script",
		Script:       content,
		Params:       p,
		Timeout:      timeout,
		RunAs:        opts.RunAs,
		Sandbox:      opts.Sandbox,
		ScriptType:   opts.ScriptType,
		Requirements: opts.Requirements,
	}
	body, mErr := json.Marshal(task)
	if mErr != nil {
		return "", -1, fmt.Errorf("marshal task: %w", mErr)
	}
	cmd := &tunnel.CommandRequest{
		CommandID:     uuid.NewString(),
		TargetID:      agentID,
		Action:        "script",
		Payload:       body,
		TimeoutSecond: int32(timeout),
	}
	// 直连 Agent(ProxyID 为空)会话 key 即 agentID；经代理则 key 为 host.ProxyID。
	// 按"会话落在哪个 tunnel"智能选路——修复原实现永远只走空 TCP server、
	// 且对直连 Agent 误判为"无代理绑定"而拒绝的问题。
	key := host.ProxyID
	if key == "" {
		key = agentID
	}
	resp, e := s.dispatchCommand(ctx, key, cmd)
	if e != nil {
		return "", -1, e
	}
	if resp == nil || !resp.Success {
		msg := ""
		if resp != nil {
			msg = resp.Error
		}
		return "", -1, fmt.Errorf("agent script exec failed: %s", msg)
	}
	// Agent 端 executeScript 将 TaskResponse JSON 作为 Output 回传。
	var tr TaskResponse
	if json.Unmarshal(resp.Output, &tr) == nil {
		if !tr.Success {
			return tr.Output, tr.ExitCode, fmt.Errorf("%s", tr.Error)
		}
		return tr.Output, tr.ExitCode, nil
	}
	return string(resp.Output), 0, nil
}

// handleGetWhitelist 返回本租户的命令白名单(Agent 启动拉取,§6)。
// enabled=false 表示未启用(放行一切);enabled=true 且 rules 为空表示拒绝一切。
func (s *Service) handleGetWhitelist(w http.ResponseWriter, r *http.Request) {
	s.whitelistMu.RLock()
	wl := s.whitelist
	s.whitelistMu.RUnlock()

	if wl == nil {
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"version": 0,
			"rules":   []any{},
		})
		return
	}
	// 序列化 rules 为 {type,pattern} 列表。
	raw := wl.RawRules()
	rules := make([]map[string]string, 0, len(raw))
	for _, r := range raw {
		rules = append(rules, map[string]string{"type": string(r.Type), "pattern": r.Pattern})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"version": 1,
		"rules":   rules,
	})
}

// handleExecSandbox 在"沙箱 Agent"所在的目标主机上,于真实隔离环境中执行脚本。
//
// 对应 UI 的"勾选沙箱环境执行":用户无需挑选普通目标主机,系统会选取一台被打上
// 沙箱标签的真实 Agent(通常就是部署 Server 的那几台机器),把脚本经隧道下发过去。
// Agent 端会为本次执行创建独立临时工作目录、净化环境变量、独立进程组;
// Python 脚本额外创建虚拟环境(venv)并按 requirements 安装依赖,执行后整体销毁。
func (s *Service) handleExecSandbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Script       string            `json:"script"`
		Timeout      int               `json:"timeout,omitempty"`
		SandboxID    string            `json:"sandbox_id,omitempty"` // 可选:指定某个沙箱 Agent
		AgentID      string            `json:"agent_id,omitempty"`   // 同义别名
		ScriptType   string            `json:"script_type,omitempty"`
		Requirements []string          `json:"requirements,omitempty"`
		Params       map[string]string `json:"params,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Script == "" {
		httputil.WriteError(w, http.StatusBadRequest, "script is required")
		return
	}
	// §6 命令白名单:沙箱执行同样受控(白名单启用时)。
	if err := s.verifyCommand(req.Script); err != nil {
		slog.Warn("sandbox-exec denied by whitelist", "error", err)
		httputil.WriteError(w, http.StatusForbidden, "script denied by whitelist: "+err.Error())
		return
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60
	}

	tenantID := tenantFromCtx(r.Context())
	prefer := req.SandboxID
	if prefer == "" {
		prefer = req.AgentID
	}
	agent, err := s.pickSandboxAgent(r.Context(), tenantID, prefer)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	start := time.Now()
	output, exitCode, execErr := s.RunScriptOnAgentWithOptions(r.Context(), agent.ID, tenantID,
		req.Script, req.Params, ScriptRunOptions{
			Timeout:      timeout,
			Sandbox:      true,
			ScriptType:   req.ScriptType,
			Requirements: req.Requirements,
		})
	resp := map[string]any{
		"host":       agent.Name,
		"agent_id":   agent.ID,
		"agent_name": agent.Name,
		"output":     output,
		"exit_code":  exitCode,
		"duration":   time.Since(start).Milliseconds(),
		"via":        "sandbox-agent",
	}
	if execErr != nil {
		resp["error"] = execErr.Error()
	}
	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req RegisterAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostName("主机名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
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
			httputil.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, proxy)
		return
	}
	host := s.buildHost(&req, r)
	if err := s.store.Register(r.Context(), host); err != nil {
		slog.Warn("register agent failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 方案 X 闭环：主机注册即同步为 CMDB host CI（agent_hosts → CMDB 最后一跳）。
	s.syncHostCI(r.Context(), tenantFromCtx(r.Context()), host)
	// 方案 X:主机纳管即触发 CMDB 采集任务创建(统一调度)。
	if s.onAgentOnboard != nil {
		if e := s.onAgentOnboard(tenantFromCtx(r.Context()), host.ID); e != nil {
			slog.Warn("agent onboard → cmdb failed", "agent", host.ID, "err", e)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, host)
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Redis 模式下批量查在线状态，避免逐条 EXISTS N+1
	var onlineSet map[string]bool
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		ids := make([]string, 0, len(hosts))
		for _, h := range hosts {
			ids = append(ids, h.ID)
		}
		onlineSet = s.hbWriter.BulkOnlineStatus(r.Context(), ids)
	}
	now := time.Now()
	result := make([]*AgentHost, 0, len(hosts))
	for _, h := range hosts {
		if agentType != "" && string(h.Type) != agentType {
			continue
		}
		copyHost := *h
		// 优先 Redis 判定在线状态; 回退到 last_seen 时间戳
		if onlineSet != nil {
			if !onlineSet[h.ID] {
				copyHost.Status = AgentStatusOffline
			} else {
				copyHost.Status = AgentStatusOnline
			}
		} else if copyHost.LastHeartbeat != nil && now.Sub(*copyHost.LastHeartbeat) > OfflineThreshold {
			copyHost.Status = AgentStatusOffline
		}
		result = append(result, &copyHost)
	}
	// 动态标注疑似重复记录,供前端提示用户合并
	s.decorateDuplicates(result)
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, host)
}

// decorateDuplicates 动态标注"疑似重复"关系:同一结果集内,若两条记录主机名相同
// 或 IP 有交集且 ID 不同,则将非规范记录标记为 duplicate_of 规范记录 ID,供前端提示合并。
// 规范记录优先取在线者,其次取最近心跳者。该字段仅运行时计算,不落库。
func (s *Service) decorateDuplicates(hosts []*AgentHost) {
	n := len(hosts)
	if n < 2 {
		return
	}
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	linked := func(a, b *AgentHost) bool {
		if a.Hostname != "" && a.Hostname == b.Hostname {
			return true
		}
		for _, ip := range a.IPs {
			for _, ip2 := range b.IPs {
				if ip != "" && ip == ip2 {
					return true
				}
			}
		}
		return false
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if hosts[i].ID != hosts[j].ID && linked(hosts[i], hosts[j]) {
				ra, rb := find(i), find(j)
				if ra != rb {
					parent[ra] = rb
				}
			}
		}
	}
	groups := map[int][]*AgentHost{}
	for i := 0; i < n; i++ {
		groups[find(i)] = append(groups[find(i)], hosts[i])
	}
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		canonical := g[0]
		for _, h := range g[1:] {
			if h.Status == AgentStatusOnline && canonical.Status != AgentStatusOnline {
				canonical = h
				continue
			}
			if h.Status == canonical.Status && h.LastHeartbeat != nil &&
				(canonical.LastHeartbeat == nil || h.LastHeartbeat.After(*canonical.LastHeartbeat)) {
				canonical = h
			}
		}
		for _, m := range g {
			if m.ID != canonical.ID {
				m.DuplicateOf = canonical.ID
			}
		}
	}
}

// MergeHosts 将 sourceID 合并进 targetID:target 作为规范记录保留,source 的标签(含沙箱)、
// 环境等字段合并进 target(仅补 target 缺失项),随后删除 source。在线记录不可作为 source 删除,
// 以免误删正在运行的 Agent 主机。
func (s *Service) MergeHosts(ctx context.Context, sourceID, targetID, tenantID string) error {
	if sourceID == targetID {
		return fmt.Errorf("不能将记录合并到自身")
	}
	src, err := s.store.Get(ctx, sourceID, tenantID)
	if err != nil {
		return err
	}
	tgt, err := s.store.Get(ctx, targetID, tenantID)
	if err != nil {
		return err
	}
	if src == nil || tgt == nil {
		return fmt.Errorf("记录不存在")
	}
	// 以真实在线状态(Redis 心跳)判断,而非可能陈旧的 DB status 列:
	// Redis 模式下离线检测器不回写 DB,旧记录的 status 列可能仍停留在 online。
	if s.isHostOnline(ctx, src) {
		return fmt.Errorf("源记录当前在线,无法合并删除;请改为将另一条记录合并到本条")
	}
	if src.Labels != nil {
		if tgt.Labels == nil {
			tgt.Labels = map[string]string{}
		}
		for k, v := range src.Labels {
			if _, ok := tgt.Labels[k]; !ok {
				tgt.Labels[k] = v
			}
		}
	}
	if tgt.Environment == "" && src.Environment != "" {
		tgt.Environment = src.Environment
	}
	tgt.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, tgt); err != nil {
		return err
	}
	return s.store.Delete(ctx, sourceID, tenantID)
}

// isHostOnline 返回主机真实在线状态:优先 Redis 心跳(权威),回退到 DB status 列或
// last_heartbeat 时间戳。避免依赖可能陈旧的 agent_hosts.status 列(离线检测器在 Redis
// 模式下不回写该列)。
func (s *Service) isHostOnline(ctx context.Context, h *AgentHost) bool {
	if s.hbWriter != nil && s.hbWriter.rdb != nil {
		return s.hbWriter.BulkOnlineStatus(ctx, []string{h.ID})[h.ID]
	}
	if h.LastHeartbeat != nil && time.Since(*h.LastHeartbeat) <= OfflineThreshold {
		return true
	}
	return h.Status == AgentStatusOnline
}

// handleMerge 合并接口:POST /api/v1/agents/{id}/merge  body {"target_id": "..."}
// 将 {id}(疑似重复/旧记录)合并进 target_id(规范记录)。
func (s *Service) handleMerge(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var body struct {
		TargetID string `json:"target_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TargetID == "" {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request: target_id required")
		return
	}
	if err := s.MergeHosts(r.Context(), id, body.TargetID, tenantID); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "merged", "source": id, "target": body.TargetID})
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req UpdateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostNameOptional("主机名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, host)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.Delete(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete agent failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	var req HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.hbWriter.Record(r.Context(), id, tenantID, &req); err != nil {
		slog.Warn("heartbeat failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]any{
		"status":            "ok",
		"breaker_open":      false,
		"restart_requested": false,
	}

	// T12: 心跳响应中注入升级指令
	if s.upgradeMgr != nil {
		if upgrade := s.upgradeMgr.CheckEligibility(id); upgrade != nil {
			resp["upgrade"] = upgrade
		}
	}

	httputil.WriteJSON(w, http.StatusOK, resp)
}

func (s *Service) handleBatchInstall(w http.ResponseWriter, r *http.Request) {
	var req BatchInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}

	addresses, err := parseAddressList(strings.Join(req.Addresses, ","))
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	serverBase := fmt.Sprintf("%s://%s", scheme, r.Host)
	tenantID := tenantFromCtx(r.Context())

	// 方案 X:主机纳管即触发 CMDB 采集任务创建(统一调度)。
	onboard := func(agentID string) {
		if s.onAgentOnboard != nil {
			if e := s.onAgentOnboard(tenantID, agentID); e != nil {
				slog.Warn("batch-install onboard → cmdb failed", "agent", agentID, "err", e)
			}
		}
	}

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
			onboard(host.ID)
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
			onboard(host.ID)
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
		onboard(host.ID)
		results = append(results, map[string]any{"address": addr, "status": "installed", "agent_id": host.ID, "output": output})
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Service) handleTestSSH(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string `json:"address"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		SSHKey   string `json:"ssh_key,omitempty"`
		UseKey   bool   `json:"use_key,omitempty"`
		ProxyID  string `json:"proxy_id,omitempty"` // 指定则经 Proxy 探测
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port == 0 {
		req.Port = 22
	}
	authType := "password"
	if req.UseKey || req.SSHKey != "" {
		authType = "ssh_key"
	}

	// 通过 Proxy 探测：目标在隔离网段，由 Proxy 代为 SSH 连通性检测
	if req.ProxyID != "" && s.tunnelServer != nil {
		_, _, execErr := s.execViaProxy(req.ProxyID, "ssh_test", map[string]any{
			"host":     req.Address,
			"port":     req.Port,
			"username": req.Username,
			"password": req.Password,
			"ssh_key":  req.SSHKey,
		}, 5)
		reachable := execErr == nil
		detail := "connected via proxy"
		if !reachable {
			detail = execErr.Error()
			slog.Warn("SSH test via proxy failed", "address", req.Address, "proxy_id", req.ProxyID, "detail", detail)
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"reachable": reachable,
			"address":   fmt.Sprintf("%s:%d", req.Address, req.Port),
			"auth_type": authType,
			"via":       "proxy",
			"detail":    detail,
		})
		return
	}

	reachable, detail := s.testSSHConnectivity(req.Address, req.Port, req.Username, req.Password, req.SSHKey)
	if !reachable {
		slog.Warn("SSH test failed", "address", req.Address, "detail", detail)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
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
		HostKeyCallback: remoteexec.DefaultHostKeyCallback(),
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
		HostKeyCallback: remoteexec.DefaultHostKeyCallback(),
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
$api = "$($scheme)://$($server)/api/v1/agents/download?os=windows&arch=%s"
$installPath = "C:\Program Files\OpsOrigin"
New-Item -ItemType Directory -Force -Path $installPath | Out-Null
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
Invoke-WebRequest -Uri $api -OutFile "$installPath\agent.exe"
New-Service -Name "OpsOriginAgent" -BinaryPathName "$installPath\agent.exe --server $server%s" -DisplayName "OpsOrigin Agent" -StartupType Automatic
Start-Service -Name "OpsOriginAgent"
Write-Host "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, arch, proxyFlag, arch)
	case "linux":
		filename = "install-agent.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Linux, arch=%s)
set -e
SERVER="%s"
SCHEME="%s"
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
ExecStart=$INSTALL_DIR/agent --server $SERVER%s
Restart=always
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-agent
sudo systemctl start opsorigin-agent
echo "OpsOrigin Agent installed successfully (arch=%s)"`, arch, serverAddr, scheme, arch, proxyFlag, arch)
	case "container":
		filename = "install-agent-container.sh"
		script = fmt.Sprintf(`#!/bin/bash
# OpsOrigin Agent Installer (Container / Docker)
set -e
SERVER="%s"
SCHEME="%s"

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
  -v /etc/hostname:/etc/hostname:ro \
  -v /etc/os-release:/etc/os-release:ro \
  -v /proc:/host/proc:ro \
  -v /sys:/host/sys:ro \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  opsorigin/agent:latest \
  --server "$SCHEME://%s"%s

echo "OpsOrigin Agent container started successfully"
echo "View logs: docker logs -f opsorigin-agent"`, serverAddr, scheme, serverAddr, serverAddr, proxyFlag)
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
RestartSec=10s
ExecStartPre=/bin/bash -c 'sleep $((RANDOM %% 30))'
[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable opsorigin-proxy
sudo systemctl start opsorigin-proxy
echo "OpsOrigin Proxy installed successfully (id=$PROXY_ID, listen=:$LISTEN_PORT)"`, serverAddr, scheme, proxyID, listenPort)
	default:
		httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, windows, container, or proxy")
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
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.store.CreateTask(r.Context(), agentID, &req); err != nil {
		slog.Warn("create task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
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
					httputil.WriteJSON(w, http.StatusOK, map[string]any{"task_id": req.TaskID, "status": "completed"})
					return
				}
				// 失败则降级到 WS/HTTP
				slog.Warn("proxy route failed, falling back to WS/HTTP", "error", err, "proxy_id", host.ProxyID)
			}
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, req)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tasks, err := s.store.ListTasks(r.Context(), agentID)
	if err != nil {
		slog.Warn("list tasks failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*TaskInfo{}
	}
	httputil.WriteJSON(w, http.StatusOK, tasks)
}

func (s *Service) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	taskID := r.PathValue("task_id")
	var resp TaskResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if resp.TaskID == "" {
		resp.TaskID = taskID
	}
	if err := s.store.CompleteTask(r.Context(), agentID, taskID, &resp); err != nil {
		slog.Warn("complete task failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 采集任务完成: 按类型解析输出入库(CMDB 更新 / 指标记录)
	s.handleCollectCompletion(r.Context(), agentID, taskID, &resp)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
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

	// 二进制文件下载
	if downloadType == "proxy-bin" || downloadType == "agent-bin" {
		cwd, _ := os.Getwd()

		// 支持的二进制文件名列表
		var binaryNames []string
		if downloadType == "agent-bin" {
			binaryNames = []string{"opsagent", "agent"}
		} else {
			binaryNames = []string{"opsproxy", "proxy"}
		}

		if osType == "darwin" {
			for i, n := range binaryNames {
				binaryNames[i] = n + "-darwin"
			}
		} else if osType == "windows" {
			for i, n := range binaryNames {
				binaryNames[i] = n + ".exe"
			}
		}
		if arch == "arm64" {
			for i, n := range binaryNames {
				binaryNames[i] = n + "-arm64"
			}
		}

		var binPath string
		for _, name := range binaryNames {
			candidate := filepath.Join(cwd, "bin", name)
			if _, err := os.Stat(candidate); err == nil {
				binPath = candidate
				break
			}
		}
		if binPath == "" {
			httputil.WriteError(w, 404, "binary not found, please build first: make build")
			return
		}

		fileName := filepath.Base(binPath)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", fileName))
		http.ServeFile(w, r, binPath)
		return
	}

	if downloadType == "proxy" {
		switch osType {
		case "linux", "darwin":
			w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=install_proxy.sh")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(generateProxyInstallScript(serverAddr, osType, arch)))
		case "windows":
			w.Header().Set("Content-Type", "application/x-powershell; charset=utf-8")
			w.Header().Set("Content-Disposition", "attachment; filename=install_proxy.ps1")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(generateWindowsProxyInstallScript(serverAddr, arch)))
		default:
			httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, darwin, or windows")
		}
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
		httputil.WriteError(w, http.StatusBadRequest, "os parameter required: linux, darwin, or windows")
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

func (s *Service) handleListProxies(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	proxies, err := s.store.ListProxies(r.Context(), tenantID)
	if err != nil {
		slog.Warn("list proxies failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if proxies == nil {
		proxies = []*ProxyInfo{}
	}
	httputil.WriteJSON(w, http.StatusOK, proxies)
}

func (s *Service) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
	var req RegisterProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validate.HostName("Proxy 名称", req.Name); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, proxy)
}

func (s *Service) handleGetProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		httputil.WriteError(w, http.StatusNotFound, "proxy not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	proxy, err := s.store.GetProxy(r.Context(), id, tenantID)
	if err != nil || proxy == nil {
		httputil.WriteError(w, http.StatusNotFound, "proxy not found")
		return
	}
	var req UpdateProxyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, proxy)
}

func (s *Service) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.store.DeleteProxy(r.Context(), id, tenantID); err != nil {
		slog.Warn("delete proxy failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ---- Circuit breaker ----

func (s *Service) handleGetCB(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cb := s.getOrCreateBreaker(id)
	httputil.WriteJSON(w, http.StatusOK, cb)
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
	httputil.WriteJSON(w, http.StatusOK, cb)
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("agent restart task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
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

	slog.Info("agent upgrade task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
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
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
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

	slog.Info("agent uninstall task queued", "agent_id", id, "task_id", task.TaskID)
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
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
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}

	var report cmdbReportPayload
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Update host with CMDB data
	applyCMDBReport(host, &report)

	s.store.Update(r.Context(), host)
	// 方案 X 闭环：Agent 上报 CMDB 资产后立即同步为 CMDB host CI。
	s.syncHostCI(r.Context(), tenantFromCtx(r.Context()), host)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "cmdb data received"})
}

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

// handleDownloadTasks 返回该 agent 待执行的 pending 任务。
func (s *Service) handleDownloadTasks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	host, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}
	tasks, err := s.store.ListTasks(r.Context(), id)
	if err != nil {
		slog.Warn("download tasks failed", "error", err)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pending := make([]*TaskInfo, 0)
	for _, t := range tasks {
		if t.Status == TaskStatusPending {
			pending = append(pending, t)
		}
	}
	httputil.WriteJSON(w, http.StatusOK, pending)
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

// handleCreateDownloadTask 创建文件分发任务(§5):
//  1. 校验目标 Agent 存在;
//  2. 依拓扑(host.ProxyID)选择直连 Server 或经 Proxy 的基址;
//  3. 签发 §6 短期下载令牌(file_id + agent_id 绑定);
//  4. 生成含令牌的下载 URL,落库为 pending 任务,Agent 轮询后直连/经代理拉取。
func (s *Service) handleCreateDownloadTask(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())

	var req downloadTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.FileID == "" || req.FilePath == "" {
		httputil.WriteError(w, http.StatusBadRequest, "file_id and file_path are required")
		return
	}
	if s.fileTokenSecret == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "file token secret not configured")
		return
	}

	host, err := s.store.Get(r.Context(), agentID, tenantID)
	if err != nil || host == nil {
		httputil.WriteError(w, http.StatusNotFound, "agent not found")
		return
	}

	// 拓扑感知:经 Proxy 的 Agent 从 Proxy 公网地址拉取,否则直连 Server。
	base := s.serverBaseURL + "/api/v1"
	if host.ProxyID != "" {
		proxy, perr := s.store.GetProxy(r.Context(), host.ProxyID, tenantID)
		if perr != nil || proxy == nil {
			httputil.WriteError(w, http.StatusBadRequest, "agent's proxy not found")
			return
		}
		scheme := "http"
		if s.tlsEnabled {
			scheme = "https"
		}
		base = fmt.Sprintf("%s://%s:%d/api/v1", scheme, proxy.Address, proxy.Port)
	} else if s.serverBaseURL == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "server base url not configured")
		return
	}

	ttl := time.Duration(req.TTLSec) * time.Second
	if ttl <= 0 {
		ttl = token.MaxTTL()
	}
	tok, err := token.Generate(s.fileTokenSecret, req.FileID, agentID, ttl)
	if err != nil {
		slog.Warn("mint download token failed", "error", err, "agent_id", agentID)
		httputil.WriteError(w, http.StatusInternalServerError, "mint token failed")
		return
	}

	task := &TaskRequest{
		TaskID:           uuid.New().String(),
		FileID:           req.FileID,
		FileURL:          buildFileURL(base, req.FileID, tok),
		FilePath:         req.FilePath,
		ExpectedChecksum: req.ExpectedChecksum,
		ChecksumAlgo:     req.ChecksumAlgo,
	}
	if err := s.store.CreateTask(r.Context(), agentID, task); err != nil {
		slog.Warn("create download task failed", "error", err, "agent_id", agentID)
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	slog.Info("download task created",
		"agent_id", agentID, "file_id", req.FileID,
		"via_proxy", host.ProxyID != "", "ttl", ttl.String())
	httputil.WriteJSON(w, http.StatusCreated, task)
}

// handleServeFile 中心存储文件拉取端点(§5):
//   - 校验短期令牌(file_id 绑定 + 未过期);
//   - 限流/熔断(按 agent_id);
//   - 以 http.ServeContent 支持 Range 断点续传;
//   - 失败计入熔断,成功清零。
func (s *Service) handleServeFile(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("file_id")
	tok := r.URL.Query().Get("token")
	if tok == "" {
		httputil.WriteError(w, http.StatusUnauthorized, "missing download token")
		return
	}
	claims, err := token.Verify(s.fileTokenSecret, tok)
	if err != nil {
		status := http.StatusForbidden
		if err == token.ErrExpired {
			status = http.StatusUnauthorized
		}
		httputil.WriteError(w, status, "invalid download token: "+err.Error())
		return
	}
	if claims.FileID != fileID {
		httputil.WriteError(w, http.StatusForbidden, "token/file mismatch")
		return
	}

	key := claims.AgentID
	if !s.fileLimiter.Allow(key) {
		httputil.WriteError(w, http.StatusTooManyRequests, "download rate limited")
		return
	}

	if s.fileStoreDir == "" {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusInternalServerError, "file store not configured")
		return
	}
	// 防目录穿越:file_id 必须是裸文件名,不含路径分隔符。
	if filepath.Base(fileID) != fileID {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusBadRequest, "invalid file_id")
		return
	}
	fp := filepath.Join(s.fileStoreDir, fileID)
	info, err := os.Stat(fp)
	if err != nil {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	f, err := os.Open(fp)
	if err != nil {
		s.fileLimiter.RecordFailure(key)
		httputil.WriteError(w, http.StatusInternalServerError, "open file failed")
		return
	}
	defer f.Close()

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileID))
	http.ServeContent(w, r, fileID, info.ModTime(), f)
	s.fileLimiter.RecordSuccess(key)
}

// ------- Agent 全局版本一致性 -------

// handleGetVersionPolicy 返回全局目标版本及 Agent 合规统计。
func (s *Service) handleGetVersionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	agents, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.versionPolicyMu.Lock()
	target := s.versionPolicy
	s.versionPolicyMu.Unlock()

	total := len(agents)
	onTarget := 0
	for _, a := range agents {
		if a.Type != "agent" {
			continue
		}
		if target != "" && a.Version == target {
			onTarget++
		}
	}
	pending := 0
	if target != "" {
		pending = total - onTarget
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"target_version": target,
		"agents_total":   total,
		"on_target":      onTarget,
		"pending":        pending,
	})
}

// handleSetVersionPolicy 设置全局目标版本。
func (s *Service) handleSetVersionPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetVersion string `json:"target_version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s.versionPolicyMu.Lock()
	s.versionPolicy = req.TargetVersion
	s.versionPolicyMu.Unlock()

	slog.Info("agent version policy updated", "target_version", req.TargetVersion)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{
		"status":         "ok",
		"target_version": req.TargetVersion,
	})
}

// handleBatchUpgrade 批量升级所有在线 Agent 到目标版本。
func (s *Service) handleBatchUpgrade(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}

	s.versionPolicyMu.Lock()
	target := s.versionPolicy
	s.versionPolicyMu.Unlock()

	if target == "" {
		httputil.WriteError(w, http.StatusBadRequest, "no target version set; use PUT /api/v1/agents/version-policy first")
		return
	}

	agents, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	upgraded := 0
	skipped := 0
	for _, a := range agents {
		if a.Type != "agent" || a.Status != "online" {
			continue
		}
		if a.Version == target {
			skipped++
			continue
		}
		task := &TaskRequest{
			Action:  "upgrade",
			Params:  map[string]any{"target_version": target},
			Timeout: 600,
		}
		if err := s.store.CreateTask(r.Context(), a.ID, task); err != nil {
			slog.Warn("batch upgrade: failed to create task", "agent_id", a.ID, "error", err)
			continue
		}
		// 尝试经 tunnel 立即下发
		if s.tunnelServer != nil && a.ProxyID != "" {
			payloadBytes, _ := json.Marshal(task)
			cmd := &tunnel.CommandRequest{
				CommandID:     uuid.NewString(),
				TargetID:      a.ID,
				Action:        "upgrade",
				Payload:       payloadBytes,
				TimeoutSecond: 600,
			}
			s.tunnelServer.SendCommand(a.ProxyID, cmd)
		}
		upgraded++
	}

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"target_version": target,
		"upgrade_queued": upgraded,
		"skipped":        skipped,
	})
}

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

func (s *Service) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if s.fileStoreDir == "" {
		httputil.WriteError(w, http.StatusInternalServerError, "file store not configured (SetFileStoreDir)")
		return
	}
	r.ParseMultipartForm(256 << 20) // 256MB max
	file, header, err := r.FormFile("file")
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "missing file field: "+err.Error())
		return
	}
	defer file.Close()
	fid := uuid.New().String()
	storedPath := filepath.Join(s.fileStoreDir, fid)
	out, err := os.Create(storedPath)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to store file: "+err.Error())
		return
	}
	defer out.Close()
	h := sha256.New()
	tr := io.TeeReader(file, h)
	size, err := io.Copy(out, tr)
	if err != nil {
		os.Remove(storedPath)
		httputil.WriteError(w, http.StatusInternalServerError, "failed to write file: "+err.Error())
		return
	}
	checksum := fmt.Sprintf("%x", h.Sum(nil))
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	meta := fileMeta{
		ID: fid, OriginalName: header.Filename, StoredPath: storedPath,
		SizeBytes: size, Checksum: checksum, ChecksumAlgo: "sha256",
		TenantID: tenantID, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	// Save metadata via ent if available
	if s.entClient != nil {
		_, err = s.entClient.File.Create().
			SetID(fid).SetTenantID(tenantID).
			SetOriginalName(header.Filename).SetStoredPath(storedPath).
			SetSizeBytes(size).SetChecksum(checksum).SetChecksumAlgo("sha256").
			SetCreatedAt(time.Now()).SetUpdatedAt(time.Now()).
			Save(r.Context())
		if err != nil {
			slog.Warn("file meta save failed", "error", err)
		}
	}
	httputil.WriteJSON(w, http.StatusCreated, meta)
}

func (s *Service) handleListFiles(w http.ResponseWriter, r *http.Request) {
	if s.entClient == nil {
		httputil.WriteJSON(w, http.StatusOK, []fileMeta{})
		return
	}
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = "default"
	}
	files, err := s.entClient.File.Query().Where(entFile.TenantIDEQ(tenantID)).Order(ent.Desc(entFile.FieldCreatedAt)).All(r.Context())
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := make([]fileMeta, 0, len(files))
	for _, f := range files {
		result = append(result, fileMeta{
			ID: f.ID, OriginalName: f.OriginalName, SizeBytes: f.SizeBytes,
			Checksum: f.Checksum, ChecksumAlgo: f.ChecksumAlgo,
			TenantID: f.TenantID, CreatedAt: f.CreatedAt.Format(time.RFC3339),
		})
	}
	if result == nil {
		result = []fileMeta{}
	}
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("file_id")
	if s.entClient != nil {
		s.entClient.File.DeleteOneID(fid).Exec(r.Context())
	}
	if s.fileStoreDir != "" {
		os.Remove(filepath.Join(s.fileStoreDir, fid))
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleFileMeta(w http.ResponseWriter, r *http.Request) {
	fid := r.PathValue("file_id")
	if s.entClient == nil {
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	f, err := s.entClient.File.Get(r.Context(), fid)
	if err != nil {
		httputil.WriteError(w, http.StatusNotFound, "file not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, fileMeta{
		ID: f.ID, OriginalName: f.OriginalName, SizeBytes: f.SizeBytes,
		Checksum: f.Checksum, ChecksumAlgo: f.ChecksumAlgo,
		TenantID: f.TenantID, CreatedAt: f.CreatedAt.Format(time.RFC3339),
	})
}
