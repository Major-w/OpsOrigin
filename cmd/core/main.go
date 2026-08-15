package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/opsorigin/core/internal/agentmgr"
	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/internal/ai/agentic"
	"github.com/opsorigin/core/internal/audit"
	"github.com/opsorigin/core/internal/automation/actions"
	"github.com/opsorigin/core/internal/automation/inboundbot"
	"github.com/opsorigin/core/internal/automation/inboundconfig"
	"github.com/opsorigin/core/internal/automation/inboundmail"
	"github.com/opsorigin/core/internal/automation/scheduler"
	"github.com/opsorigin/core/internal/automation/script"
	"github.com/opsorigin/core/internal/automation/workflow"
	cloud "github.com/opsorigin/core/internal/cloud"
	k8s "github.com/opsorigin/core/internal/cloud/k8s"
	_ "github.com/opsorigin/core/internal/cloud/providers/all"
	"github.com/opsorigin/core/internal/cmdb"
	changeassess "github.com/opsorigin/core/internal/cmdb/changeassess"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/discovery/plugins"
	"github.com/opsorigin/core/internal/cmdb/outbox"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/cmdb/registry"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/deploy"
	"github.com/opsorigin/core/internal/ent"
	"github.com/opsorigin/core/internal/iam"
	"github.com/opsorigin/core/internal/identity"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/itsm/catalog"
	itsmmodel "github.com/opsorigin/core/internal/itsm/model"
	"github.com/opsorigin/core/internal/itsm/ola"
	"github.com/opsorigin/core/internal/itsm/oncall"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/internal/keystore"
	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/internal/lowcode/service"
	"github.com/opsorigin/core/internal/message"
	"github.com/opsorigin/core/internal/monitor"
	healthhandler "github.com/opsorigin/core/internal/monitor/health"
	"github.com/opsorigin/core/internal/monitor/ingest"
	metricsregistry "github.com/opsorigin/core/internal/monitor/metrics"
	monitormodel "github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/internal/notification"
	"github.com/opsorigin/core/internal/ops"
	"github.com/opsorigin/core/internal/report"
	"github.com/opsorigin/core/internal/report/datasets"
	"github.com/opsorigin/core/internal/sysconfig"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/internal/vuln"
	"github.com/opsorigin/core/internal/ws"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	entdbpkg "github.com/opsorigin/core/pkg/db"
	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/llm"
	redisclient "github.com/opsorigin/core/pkg/redis"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

// @title OpsOrigin API
// @version 1.0
// @description OpsOrigin 一体化运维平台 API 文档
// @termsOfService http://swagger.io/terms/

// @contact.name OpsOrigin Team
// @contact.url https://github.com/opsorigin/core
// @contact.email support@opsorigin.com

// @license.name Apache 2.0
// @license.url https://www.apache.org/licenses/LICENSE-2.0.html

// @host localhost:8080
// @BasePath /

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description 输入格式: "Bearer {token}"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	pgRepo, err := pg.NewRepository(cfg.CMDB.PostgresDSN)
	if err != nil {
		slog.Warn("pg not available, running in stub mode", "error", err)
	}

	if pgRepo != nil {
		if err := pgRepo.Migrate(ctx); err != nil {
			slog.Error("cmdb schema migrate failed", "error", err)
		}
	}

	dgClient, err := dgraph.NewClient(cfg.CMDB.DgraphTarget)
	if err != nil {
		slog.Warn("dgraph not available, running in stub mode", "error", err)
	}

	if dgClient != nil {
		if err := dgClient.SetupSchema(ctx); err != nil {
			slog.Warn("dgraph schema setup failed", "error", err)
		}
	}

	if pgRepo != nil {
		outboxWorker := outbox.NewWorker(cfg.Outbox, pgRepo, dgClient)
		go func() {
			if err := outboxWorker.Start(ctx); err != nil {
				slog.Error("outbox worker stopped", "error", err)
			}
		}()
	} else {
		slog.Warn("outbox worker disabled (pg not available)")
	}

	var rdbClient *redisclient.Client
	rdbClient, err = redisclient.NewClient(cfg.RedisAddr, cfg.RedisPoolSize)
	if err != nil {
		slog.Warn("redis not available, running without distributed rate limiting / stream subscription", "error", err)
		rdbClient = nil
	}

	var ldapProv *auth.LDAPProvider
	if cfg.LDAP.Enabled {
		ldapProv = auth.NewLDAPProvider(cfg.LDAP)
	}

	var ssoProv *auth.SSOProvider
	if cfg.SSO.Enabled {
		ssoProv = auth.NewSSOProvider(cfg.SSO)
	}

	authProvider := auth.NewProvider(cfg.Auth.JWTSecret, ldapProv, ssoProv)
	// P0.5 可信边界:仅携带此密钥的内部服务调用才被允许用 X-Principal-* 断言主体;
	// 留空 = 断言头通道关闭(fail-closed),浏览器请求一律从 JWT 派生身份。
	authProvider.SetServiceToken(cfg.Auth.ServiceToken)
	if cfg.Auth.ServiceToken == "" {
		slog.Info("auth: internal principal assertion channel disabled (OPSORIGIN_SERVICE_TOKEN unset)")
	}

	var db *sql.DB
	if pgRepo != nil {
		db = pgRepo.DB()
		// T09: PG 连接池可配
		db.SetMaxOpenConns(cfg.Postgres.MaxConns)
		db.SetMaxIdleConns(cfg.Postgres.MaxIdleConns)
		db.SetConnMaxLifetime(cfg.Postgres.ConnMaxLifetime)
	}

	// T06: ent Client 初始化
	var entClient *ent.Client
	if db != nil {
		entClient = entdbpkg.NewEntClient(db)
	}

	auditSvc := audit.NewService(cfg, db)

	server := NewServer(cfg)
	server.RegisterService(auditSvc)
	server.WithMiddleware(authProvider.Middleware)
	server.WithMiddleware(auditSvc.Middleware)

	// T03: 注册 bypass 前缀 — /metrics /healthz /readyz /api/v1/health /swagger 跳过认证/审计/RBAC 中间件
	server.WithBypassPrefixes("/metrics", "/healthz", "/readyz", "/api/v1/health", "/swagger/")

	// Swagger UI 路由
	server.RegisterService(&swaggerRegistrar{})

	// T07: 给 Core http.Server 的 Handler 包一层 promhttp.InstrumentHandlerDuration
	// P0: 最外层再包统一 panic recovery —— recover 贴近业务 handler, promhttp 在
	// 外记录所有请求(含被恢复的 panic 请求, 状态码 500)。recovery 作用于整个
	// final handler(bypass 前缀在内的全部端点), 任何 handler panic 不再拖垮进程。
	server.WithHandlerWrapper(func(next http.Handler) http.Handler {
		recovered := httputil.RecoverHandler(next, slog.Default())
		return promhttp.InstrumentHandlerDuration(
			metricsregistry.HttpRequestDuration,
			recovered,
		)
	})

	var rdb *redis.Client
	if rdbClient != nil {
		rdb = rdbClient.Conn()
	}

	// T07: 注册 /metrics Prometheus 端点
	metricsHandler := metricsregistry.NewMetricsHandler()
	server.RegisterService(&staticHandlerRegistrar{
		routes: map[string]http.Handler{
			"GET /metrics": metricsHandler,
		},
	})

	// T08: 创建 health handler 并通过注册器注册到 mux
	// /healthz = liveness(存活), /readyz = readiness(依赖就绪), /api/v1/health 兼容旧路径
	healthHandler := healthhandler.NewHealthHandler(db, rdb)
	server.RegisterService(&staticHandlerRegistrar{
		routes: map[string]http.Handler{
			"GET /healthz":       healthHandler,
			"GET /readyz":        healthHandler,
			"GET /api/v1/health": healthHandler,
		},
	})

	cmdbSvc := cmdb.NewService(cfg, pgRepo, dgClient)
	server.RegisterService(cmdbSvc)
	if db != nil {
		server.RegisterService(changeassess.NewStore(db)) // 变更影响评估：变更请求持久化
	}

	// 云资源接出：注册 4 云 Provider（空白导入触发 init）并把云 Service 接入 Core mux，
	// 经 cloudCMDBAdapter 把发现的资源同步进 CMDB（复用完整 CI 生命周期）。
	// 注意：云/主机/容器属于「资源纳管」层职责，由 cloudSvc 拉取生成基础 CI；
	// CMDB 的自动发现(relation/agent/snmp)不重复拉取资源，只在已纳管 CI 之上推断关系。
	cloudCredStore := cloud.NewMemStore()
	cloudSvc := cloud.NewService(cloudCredStore, cloudCMDBAdapter{pgRepo: pgRepo, cmdbSvc: cmdbSvc})
	server.RegisterService(cloudSvc)

	// 采集架构：CMDB 作为统一调度者。把云采集器注册进 CMDB 注册表，
	// 云凭证纳管（建立通道）后联动 CMDB 创建采集任务，调度器按频率触发真实采集。
	// 采集架构：CMDB 作为统一调度者。
	// - cmdb:cloud → server 拉模式（拉云资源 → 落 CI）
	// - monitor:cloud → server 拉模式（拉云监控指标 → 经 onMetric 落 ingest 管道）
	// 两者共享同一 cloud.Collector 实例（复用凭证存储与厂商注册）。
	cloudCol := cloud.NewCollector(cloudCredStore)
	cmdbSvc.RegisterCollector("cmdb", "cloud", &cloudCollector{col: cloudCol})
	cmdbSvc.RegisterCollector("monitor", "cloud", &cloudMetricCollector{col: cloudCol})
	cloudSvc.SetOnOnboard(func(resourceType, target string, enableCMDB, enableMonitor *bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cmdbSvc.OnboardResource(ctx, "default", resourceType, target, enableCMDB, enableMonitor); err != nil {
			slog.Error("onboard resource → create collection task failed", "target", target, "err", err)
		}
	})
	cmdbSvc.StartScheduler(ctx)

	monitorSvc := monitor.NewService(cfg, rdb)
	server.RegisterService(monitorSvc)

	monitorSvc.Start(ctx)

	// 全景可观测拓扑：把 monitor 数据快照注入 cmdb 服务（nil 安全）。
	cmdbSvc.SetMonitorSnapshotProvider(&monitorSnapshotAdapter{svc: monitorSvc})

	// AI 模型注册表 / 本体服务（设计文档阶段 1）：以 pg.Repository 为目录存储，
	// 由 registry.New 鸭子类型满足 TypeCatalog 窄接口，零耦合注入。
	cmdbSvc.SetTypeRegistry(registry.New(pgRepo))

	monitorTopo := monitorSvc.Topology()
	cmdbSvc.OnCICreated(func(ctx context.Context, ciID, tenantID string) {
		monitorTopo.RecordTenant(ctx, ciID, tenantID)
	})

	wsHub := ws.NewHub()
	server.WithMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/ws" {
				wsHub.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	monitorSvc.SetWebSocketHub(wsHub)

	itsmSvc := itsm.NewService(cfg, db)
	server.RegisterService(itsmSvc)
	itsmSvc.SetWebSocketHub(wsHub)
	itsmSvc.StartSLAChecker(ctx)
	itsmSvc.StartOLAChecker(ctx)

	// 站内消息:独立收件箱服务。PG 建表失败会自动回落内存实现,
	// 不会因 DDL 问题拖垮主流程。实时推送走 wsHub(由下方 SetAuthenticator 鉴权)。
	msgSvc := message.NewService(db)
	server.RegisterService(msgSvc)
	msgSvc.SetPusher(wsHub)

	var processSvc *process.Service
	var catalogStore *catalog.Store
	if db != nil {
		processSvc = process.NewService(db)
		server.RegisterService(processSvc)
		processSvc.StartTimerChecker(ctx)
		itsmSvc.SetProcessStore(processSvc)

		slaStore := sla.NewStore(db)
		server.RegisterService(slaStore)
		olaStore := ola.NewStore(db)
		server.RegisterService(olaStore) // OLA 独立管理
		itsmSvc.SetSLAStore(slaStore)
		itsmSvc.SetOLAStore(olaStore)

		catalogStore = catalog.NewStore(db)
		server.RegisterService(catalogStore)
		itsmSvc.SetCatalogStore(catalogStore)
	}

	// 创建 tunnel server,监听 Proxy 连接(用于通过 Proxy 下发命令给 Agent)
	tunnelServer := tunnel.NewServer(cfg.Proxy.Listen, cfg.Tunnel.AuthSecret, cfg.Tunnel.Insecure)
	tunnelServer.OnCommand(func(ctx context.Context, proxyID string, cmd *tunnel.CommandRequest) *tunnel.CommandResponse {
		slog.Info("tunnel command routed", "proxy_id", proxyID, "action", cmd.Action, "target", cmd.TargetID)
		return &tunnel.CommandResponse{
			CommandID: cmd.CommandID,
			Success:   true,
			Output:    []byte("routed"),
		}
	})

	// 创建 gRPC tunnel server,支持 Agent 直连 Core(绕过 Proxy)
	grpcTunnelServer := tunnel.NewGRPCServer(cfg.Core.GRPCListen, cfg.Tunnel.AuthSecret, cfg.Tunnel.Insecure)
	grpcTunnelServer.OnCommand(func(ctx context.Context, proxyID string, cmd *tunnel.CommandRequest) *tunnel.CommandResponse {
		slog.Info("grpc tunnel command routed", "proxy_id", proxyID, "action", cmd.Action, "target", cmd.TargetID)
		return &tunnel.CommandResponse{
			CommandID: cmd.CommandID,
			Success:   true,
			Output:    []byte("routed"),
		}
	})

	// 监控/CMDB 指标上报管道(阶段 1/2): OnMetrics → 批量缓冲(背压) → Sink。
	// 配置 OPSORIGIN_CORE_VM_ADDR 后写入 VictoriaMetrics(高频 monitor, Prometheus 文本导入);
	// 未配置时回退内存 sink(仅演示/测试)。
	var ingestSink ingest.Sink = ingest.NewMemorySink()
	if cfg.Core.VMAddr != "" {
		ingestSink = ingest.NewVMSink(cfg.Core.VMAddr)
		slog.Info("metrics ingest: writing to VictoriaMetrics", "addr", cfg.Core.VMAddr)
		// PromQL 查询代理(监控大盘/告警): GET /api/v1/monitor/ts/query
		server.RegisterService(&staticHandlerRegistrar{
			routes: map[string]http.Handler{
				"GET /api/v1/monitor/ts/query": ingest.QueryHandler(cfg.Core.VMAddr),
			},
		})
	} else {
		slog.Warn("metrics ingest: OPSORIGIN_CORE_VM_ADDR not set, using in-memory sink (demo only)")
	}
	ingestBuf := ingest.NewBuffer(ingestSink, ingest.WithFlushInterval(2*time.Second))
	ingestBuf.Start(ctx)

	// 将 CMDB 调度器(server 拉模式)产出的 monitor 指标路由到 ingest 管道
	// (VM/内存 sink)。Agent 侧 push 的指标已由 grpcTunnelServer.OnMetrics 接入同一管道,
	// 两条路径在此汇聚为统一的指标落库。
	cmdbSvc.SetMetricSink(func(p cmdb.MetricPoint) {
		ingestBuf.Submit(tunnel.MetricsReport{
			AgentID:     p.ResourceID,
			TaskID:      p.ResourceID,
			TaskType:    "monitor",
			Timestamp:   p.Timestamp,
			IntervalSec: 0,
			Points: []tunnel.MetricPoint{{
				Name:   p.Name,
				Value:  p.Value,
				Labels: p.Labels,
			}},
		})
	})
	grpcTunnelServer.OnMetrics(func(report tunnel.MetricsReport) {
		ingestBuf.Submit(report)
	})

	// ingest 管道运行统计: GET /api/v1/monitor/ingest/stats
	// 前端监控页据此展示上报管道健康度(收到/落库/背压丢弃/失败)。
	statsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ingestBuf.Stats())
	})
	server.RegisterService(&staticHandlerRegistrar{
		routes: map[string]http.Handler{
			"GET /api/v1/monitor/ingest/stats": statsHandler,
		},
	})

	agentSvc := agentmgr.NewService(db, rdb, entClient)
	// 设置 TLS 状态(影响安装脚本中下载链接的 scheme)
	if cfg.TLS.Enabled {
		agentSvc.SetTLSEnable(true)
	}
	agentSvc.SetTunnelServer(tunnelServer)
	agentSvc.SetGRPCTunnel(grpcTunnelServer) // 直连 Agent(gRPC)经此下发命令
	// §5 文件分发:注入中心存储目录、短期令牌密钥、Server 对外基址(直连 Agent 下载地址)。
	agentSvc.SetFileStoreDir(cfg.FileStore.Dir)
	agentSvc.SetFileTokenSecret(cfg.FileStore.TokenSecret)
	agentSvc.SetServerBaseURL(cfg.Core.PublicURL)

	// T11: 创建并启动任务下发队列
	if db != nil {
		taskQueue := agentmgr.NewTaskQueue(db, nil, cfg.TaskQueue.QueueSize, cfg.TaskQueue.Workers)
		agentSvc.SetTaskQueue(taskQueue)
		go taskQueue.Start(ctx)
	}

	// T12: 创建滚动升级管理器
	if db != nil {
		upgradeMgr := agentmgr.NewUpgradeManager(db)
		agentSvc.SetUpgradeManager(upgradeMgr)
	}

	server.RegisterService(agentSvc)

	// 沙箱环境不再是 Core 进程内的伪 Agent 记录,而是打在真实 Agent 上的标签
	// (见 agentmgr.SandboxLabelKey)。此处清理历史遗留的伪记录。
	agentSvc.CleanupLegacySandboxHosts()
	// 默认沙箱池: 把 Server 所在主机的 Agent 自动标记为沙箱环境(可经环境变量调整)。
	agentSvc.ApplySandboxPool(ctx)

	// 方案 X:CMDB 统一调度主机/容器采集任务。
	// 主机/容器经 Agent 纳管成功(注册或批量安装)后,回调 OnboardResource 创建 channel=agent 的
	// cmdb/monitor 任务;cmdbSvc 通过 AgentChannel 适配器把这些任务投影到 agentmgr 下发,
	// 采集动作由 Agent 本地执行并 push 上报(server 侧不主动拉取)。
	agentSvc.SetOnAgentOnboard(func(tenantID, agentID string) error {
		obCtx, obCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer obCancel()
		return cmdbSvc.OnboardResource(obCtx, tenantID, "host", "host:"+agentID, nil, nil)
	})
	cmdbSvc.SetAgentChannel(&agentChannelAdapter{agentSvc: agentSvc})

	// 方案 X 闭环最后一跳：把 Agent 纳管的主机资产同步为 CMDB host CI。
	// 此前 agent_hosts 的资产从没进 CMDB（与云路径的 cloudCMDBAdapter 不对称），此处补齐。
	agentSvc.SetHostCIWriter(agentCMDBAdapter{pgRepo: pgRepo, cmdbSvc: cmdbSvc})

	// K8s 容器资源纳管：注入经 Agent 通道的发现器（鸭子类型 *agentmgr.Service 满足 cloud.K8sAgentRunner）。
	cloudSvc.SetK8sDiscoverer(k8s.NewDiscoverer(agentSvc))

	// CMDB 自动发现插件：注册内置发现器（物理服务器/网络设备/数据库/中间件/应用），
	// 注入探针 Agent 脚本通道（*agentmgr.Service 满足 discovery.ScriptRunner 鸭子接口），
	// 并启动确保中间件等发现产出所需的模型类型存在。
	discoveryReg := plugins.NewRegistry()
	cmdbSvc.SetDiscoveryRegistry(discoveryReg)
	cmdbSvc.SetDiscoveryScriptRunner(agentSvc)
	if pgRepo != nil {
		go func() {
			ensureCtx, ec := context.WithTimeout(context.Background(), 30*time.Second)
			defer ec()
			if err := cmdbSvc.EnsureDiscoveryModelTypes(ensureCtx, "default"); err != nil {
				slog.Error("ensure discovery model types failed", "err", err)
			}
		}()
	}

	// 系统配置管理服务
	sysconfigSvc := sysconfig.NewService(cfg)
	server.RegisterService(sysconfigSvc)

	var scriptSvc *script.Service
	var workflowSvc *workflow.Service
	var schedulerSvc *scheduler.Service
	var knowledgeSvc *knowledge.Service
	var oncallSvc *oncall.Service
	var keySvc *keystore.Service
	var userSvc *user.Service
	var iamSvc *iam.Service
	var notifySvc *notification.Service
	var inboundStore *inboundconfig.Store
	var deploySvc *deploy.Service

	if db != nil {
		scriptSvc = script.NewService(db)
		server.RegisterService(scriptSvc)

		workflowSvc = workflow.NewService(db)
		workflowSvc.SetScriptExecutor(scriptSvc)
		// 让 ITSM 流程的服务任务(StepService)也能驱动同一套自动化脚本执行器。
		if processSvc != nil {
			processSvc.SetScriptExecutor(scriptSvc)
		}
		// 让脚本/工作流在指定 AgentID 时，通过 Tunnel 派发到被管主机执行
		// （而非 Core 本机 os/exec），并复用 Agent 端命令白名单校验。
		scriptSvc.SetAgentRunner(agentSvc)

		// 本地兜底执行路径(无 Agent 时)的命令白名单：仅允许 bash/python3/cmd/sudo。
		// dev 期宽松，生产应替换为 sysconfig 加载的租户白名单。
		wl, _ := command.New("dev-allowlist", []command.Rule{
			{Type: command.RulePrefix, Pattern: "bash"},
			{Type: command.RulePrefix, Pattern: "python3"},
			{Type: command.RulePrefix, Pattern: "cmd"},
			{Type: command.RulePrefix, Pattern: "sudo"},
		})
		scriptSvc.SetCommandWhitelist(wl)
		scriptSvc.SetDangerousPolicy(command.NewDangerousPolicy())

		server.RegisterService(workflowSvc)

		schedulerSvc = scheduler.NewService(db)
		schedulerSvc.SetWorkflowTrigger(func(ctx context.Context, tenantID, workflowID string) error {
			_, err := workflowSvc.Trigger(ctx, tenantID, workflowID, nil, nil)
			return err
		})
		schedulerSvc.SetScriptTrigger(func(ctx context.Context, tenantID, scriptID string, agentIDs []string) error {
			agentID := ""
			if len(agentIDs) > 0 {
				agentID = agentIDs[0]
			}
			_, _, err := scriptSvc.Execute(scriptID, nil, 3600, "", agentID, tenantID)
			return err
		})
		server.RegisterService(schedulerSvc)
		// 启动定时扫描（按 CronExpr 触发 workflow）。
		go schedulerSvc.Start(ctx)

		knowledgeSvc = knowledge.NewService(db)
		server.RegisterService(knowledgeSvc)

		// 统一报表模块: 注册内置数据集 + 装配控制面 REST(看板持久化、统一查询)。
		if err := datasets.Install(db, report.Default()); err != nil {
			log.Printf("report datasets install failed: %v", err)
		}
		reportSvc := report.NewService(report.Default(), db)
		reportSvc.InitSchema(ctx)
		server.RegisterService(reportSvc)

		oncallSvc = oncall.NewService(db)
		server.RegisterService(oncallSvc)

		keySvc = keystore.NewService(db)
		server.RegisterService(keySvc)

		userSvc = user.NewService(db, authProvider)
		server.RegisterService(userSvc)

		iamSvc = iam.NewService(db)
		server.RegisterService(iamSvc)

		notifySvc = notification.NewService(db)
		server.RegisterService(notifySvc)

		inboundStore = inboundconfig.NewStore(db)
		server.RegisterService(inboundStore)

		deploySvc = deploy.NewService(db)
		server.RegisterService(deploySvc)
	} else {
		slog.Warn("db not available, skipping script/workflow/scheduler/knowledge/oncall/keystore/iam/notification/inbound/deploy")
	}

	// user service always created (has built-in admin fallback for stub mode)
	if userSvc == nil {
		userSvc = user.NewService(nil, authProvider)
		server.RegisterService(userSvc)
	}

	// process & catalog always created (nil-db = empty results, routes still registered)
	if processSvc == nil {
		processSvc = process.NewService(nil)
		server.RegisterService(processSvc)
		itsmSvc.SetProcessStore(processSvc)
	}
	if catalogStore == nil {
		catalogStore = catalog.NewStore(nil)
		server.RegisterService(catalogStore)
		itsmSvc.SetCatalogStore(catalogStore)
	}

	// 初始化低代码引擎
	var lowcodeSvc *service.Service
	if db != nil {
		lowcodeStore := service.NewPostgresStore(db)
		lowcodeSvc = service.NewService(lowcodeStore)
		lowcodeHandler := service.NewHandler(lowcodeSvc)
		server.RegisterService(lowcodeHandler)

		// 组件市场
		marketHandler := service.NewMarketHandler(lowcodeSvc.GetMarket())
		server.RegisterService(marketHandler)

		// 版本管理
		versionHandler := service.NewVersionHandler(lowcodeSvc.GetVersion())
		server.RegisterService(versionHandler)
	} else {
		slog.Warn("lowcode engine disabled (pg not available)")
	}

	llmClient := llm.NewClient(llm.Config{
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		BaseURL: cfg.LLM.BaseURL,
		Timeout: time.Duration(cfg.LLM.Timeout) * time.Second,
		// 语义检索:embedding 端点可独立配置,缺省复用聊天 Model/BaseURL 的同名端点。
		EmbeddingModel:   cfg.LLM.EmbeddingModel,
		EmbeddingBaseURL: cfg.LLM.EmbeddingBaseURL,
	})
	if knowledgeSvc != nil {
		knowledgeSvc.SetEmbedder(llmClient) // 注入向量生成器,新建/更新知识库文章自动写入 embedding
	}

	// 多模型路由池(P3): 主配置为兜底 provider, 附加 provider 按权重参与路由(失败自动降级)。
	var llmProviders []agentic.ProviderConfig
	for _, p := range cfg.LLM.Providers {
		llmProviders = append(llmProviders, agentic.ProviderConfig{
			Name:    p.Name,
			APIKey:  p.APIKey,
			Model:   p.Model,
			BaseURL: p.BaseURL,
			Weight:  p.Weight,
		})
	}
	llmRouter := agentic.NewModelRouter(llm.Config{
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		BaseURL: cfg.LLM.BaseURL,
		Timeout: time.Duration(cfg.LLM.Timeout) * time.Second,
	}, llmProviders)

	// 存量知识库语义检索回填: 启动时幂等、非阻塞地补算 embedding(仅对 embedding IS NULL 的文章)。
	// 失败(如 pgvector 未启用)仅告警, 不影响启动。
	go func() {
		defer func() { _ = recover() }()
		n, err := knowledgeSvc.ReindexAll(context.Background())
		if err != nil {
			slog.Warn("knowledge reindex skipped", "err", err)
			return
		}
		if n > 0 {
			slog.Info("knowledge reindex done", "reindexed", n)
		}
	}()

	// P3-1 增量索引自愈: 后台循环定时扫描 embedding IS NULL 的文章带退避重试,
	// 并刷新 knowledge_unindexed_articles 指标(覆盖启动回填之后新出现/失败的 embedding)。
	if knowledgeSvc != nil {
		knowledgeSvc.StartReindexLoop(context.Background(), 30*time.Second)
	}

	// 持续改进闭环: AI 巡检工单完成后, 处置案例自动沉淀到知识库(供 AI 检索复用)。
	itsmSvc.SetKnowledgeArchiver(func(ctx context.Context, tenantID, title, content string, tags []string) (string, error) {
		article := &knowledge.Article{
			TenantID: tenantID,
			Title:    title,
			Content:  content,
			Category: "处置案例",
			Tags:     tags,
			Status:   "published",
		}
		if err := knowledgeSvc.CreateArticle(ctx, article); err != nil {
			return "", err
		}
		return article.ID, nil
	})

	// user/iam/notification 已在 db!=nil 块中创建

	// P0.5 身份富化:鉴权链在派生主体后回填「服务端解析的权限集合 + 用户展示名」。
	// 权限来自 IAM 角色库聚合(角色 + 用户组),不再由客户端 X-Permissions 头自报;
	// 展示名按 uid 回查 users.username,让审计 actor 可读。两者均带 TTL 缓存。
	if db != nil && iamSvc != nil {
		identityEnricher := identity.NewEnricher(db, iamSvc.GetAggregatedPermissions)
		authProvider.SetPrincipalEnricher(identityEnricher)
		// 角色权限被编辑后立即失效富化器缓存,不必等 TTL。
		iamSvc.SetPermissionChangeHook(identityEnricher.InvalidatePermissions)

		// WebSocket 握手鉴权:浏览器无法带自定义头,握手时从 ?token= 取 JWT,
		// 由 auth.Provider 校验后派生身份。username 从 uid 经身份富化器解析
		// (users.username),使「按 username 投递的站内消息」能精确推到在线用户。
		wsHub.SetAuthenticator(func(token string) (ws.Identity, bool) {
			claims, err := authProvider.ValidateToken(token)
			if err != nil || claims == nil {
				return ws.Identity{}, false
			}
			username := claims.UserID
			if name := identityEnricher.DisplayName(ctx, claims.TenantID, claims.UserID); name != "" {
				username = name
			}
			return ws.Identity{
				UserID:   claims.UserID,
				Username: username,
				TenantID: claims.TenantID,
			}, true
		})

		// ITSM 动态表单选项来源:cmdb:<type> 取配置项、iam:user/role/group 取人与角色。
		// 解析器由 itsm 侧定义(form 包只认接口),此处在 cmdb/user/iam 就绪后装配,带 TTL 缓存。
		itsmSvc.SetSourceResolver(itsm.NewCMDBIAMResolver(cmdbSvc, userSvc, iamSvc))

		// 服务目录「服务对象」按用户组授权: catalog 不反向依赖 iam,由此处注入组解析器。
		if catalogStore != nil {
			catalogStore.SetGroupResolver(func(ctx context.Context, tenantID, userID string) ([]string, error) {
				groups, err := iamSvc.ListGroups(ctx, tenantID)
				if err != nil {
					return nil, err
				}
				var ids []string
				for _, g := range groups {
					for _, uid := range g.UserIDs {
						if uid == userID {
							ids = append(ids, g.ID)
							break
						}
					}
				}
				return ids, nil
			})
		}

		// 通知管理:邮件/钉钉/企业微信/飞书/Webhook 五种渠道 + 事件订阅策略
		if notifySvc != nil {
			schedulerSvc.SetNotifier(notifySvc)
		}
		itsmSvc.SetNotifier(notifySvc)

		// 站内消息投递桥:把 ITSM 的 username 收件人转成 message.Message 落库+推送。
		// 用匿名适配器隔离两个包,ITSM 只依赖极小的 MessageSender 接口。
		itsmSvc.SetMessageSender(messageInboxSender{svc: msgSvc})

		// 流程引擎动作库: service 步骤除脚本外可执行 http / notify / create_ticket / cmdb_update 预置动作。
		actionExec := actions.New()
		if scriptSvc != nil {
			actionExec.SetScriptExecutor(scriptSvc)
		}
		if notifySvc != nil {
			actionExec.SetNotifier(notifySvc.NotifyEvent)
		}
		actionExec.SetUpdateCI(func(ctx context.Context, tenantID, ciID string, attrs map[string]any) error {
			return cmdbSvc.UpdateCIAttributes(ctx, ciID, tenantID, attrs)
		})
		actionExec.SetCreateTicket(func(ctx context.Context, tenantID string, params map[string]any) (string, error) {
			req := itsmmodel.CreateTicketRequest{
				Title:        strAny(params["title"]),
				Description:  strAny(params["description"]),
				RiskLevel:    itsmmodel.RiskLevel(strAny(params["risk_level"])),
				ChangePlan:   strAny(params["change_plan"]),
				CreatedBy:    strAny(params["created_by"]),
				CustomFields: params,
			}
			if req.Title == "" {
				return "", fmt.Errorf("create_ticket action: title is required")
			}
			if req.CreatedBy == "" {
				req.CreatedBy = "action"
			}
			tctx := context.WithValue(ctx, "tenant_id", tenantID)
			t, err := itsmSvc.CreateTicket(tctx, req)
			if err != nil {
				return "", err
			}
			return t.ID, nil
		})
		if processSvc != nil {
			processSvc.SetActionExecutor(actionExec)
		}
	}

	// 入站渠道配置统一由「入站配置」存储(automation_inbound_channels)驱动,
	// 界面可动态管理渠道开关与参数; 渠道未配置时回退到环境变量(兼容旧部署)。

	// 邮件入站: 从存储读取 IMAP 配置(缺省回退环境变量), 启动后每 30s 检查变更并热重载。
	if inboundStore != nil {
		mailSvc := inboundmail.New(inboundmail.Config{})
		mailSvc.SetHandlers(inboundmail.Handlers{
			ReplyToTicket: func(ctx context.Context, tenantID, ticketID, author, body string) error {
				return itsmSvc.AddTicketComment(ctx, tenantID, ticketID, "mail:"+author, body)
			},
			CreateTicket: func(ctx context.Context, tenantID, from, subject, body string) (string, error) {
				req := itsmmodel.CreateTicketRequest{
					Title:         subject,
					Description:   body,
					RiskLevel:     itsmmodel.RiskLow,
					CreatedBy:     "mail:" + from,
					CreatedByType: "mail",
				}
				tctx := context.WithValue(ctx, "tenant_id", tenantID)
				tk, err := itsmSvc.CreateTicket(tctx, req)
				if err != nil {
					return "", err
				}
				return tk.ID, nil
			},
		})
		var mailSig string
		applyMail := func() {
			ch, _ := inboundStore.GetByType(ctx, "default", inboundconfig.TypeEmail)
			if ch == nil {
				return
			}
			var k struct {
				Host     string `json:"host"`
				Port     string `json:"port"`
				Username string `json:"username"`
				Password string `json:"password"`
				Mailbox  string `json:"mailbox"`
				PollSecs int    `json:"poll_secs"`
			}
			json.Unmarshal([]byte(ch.Config), &k)
			host := k.Host
			if host == "" {
				host = os.Getenv("ITSM_MAIL_IMAP_HOST")
			}
			port := k.Port
			if port == "" {
				port = os.Getenv("ITSM_MAIL_IMAP_PORT")
			}
			user := k.Username
			if user == "" {
				user = os.Getenv("ITSM_MAIL_USERNAME")
			}
			pass := k.Password
			if pass == "" {
				pass = os.Getenv("ITSM_MAIL_PASSWORD")
			}
			mb := k.Mailbox
			if mb == "" {
				mb = os.Getenv("ITSM_MAIL_MAILBOX")
			}
			enable := ch.Enabled || host != ""
			if !enable {
				return
			}
			cfg := inboundmail.Config{Enable: true, Host: host, Port: port, Username: user, Password: pass, Mailbox: mb, TenantID: "default"}
			if k.PollSecs > 0 {
				cfg.PollSecs = k.PollSecs
			}
			sig := fmt.Sprintf("%v|%s|%s|%s|%s|%s|%d", enable, host, port, user, mb, pass, k.PollSecs)
			if sig != mailSig {
				mailSig = sig
				mailSvc.Reload(cfg)
			}
		}
		applyMail()
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					mailSvc.Stop()
					return
				case <-ticker.C:
					applyMail()
				}
			}
		}()
	}

	// 多渠道机器人入站(企业微信/钉钉/飞书): 配置从「入站配置」存储读取,
	// 经 SetConfigProvider 在每次请求时实时生效(界面改动无需重启)。
	// 渠道未配置时回退到环境变量(兼容旧部署)。
	buildBotConfig := func() inboundbot.Config {
		c := inboundbot.Config{}
		if ch, _ := inboundStore.GetByType(ctx, "default", inboundconfig.TypeWeCom); ch != nil {
			var k struct {
				Token     string `json:"token"`
				AESKey    string `json:"aeskey"`
				CorpID    string `json:"corpid"`
				AgentID   string `json:"agentid"`
				ProcessID string `json:"process_id"`
			}
			json.Unmarshal([]byte(ch.Config), &k)
			token := firstNonEmpty(k.Token, os.Getenv("ITSM_WECOM_TOKEN"))
			if ch.Enabled || token != "" {
				c.WeCom = inboundbot.WeComConfig{
					Enable:    true,
					Token:     token,
					AESKey:    firstNonEmpty(k.AESKey, os.Getenv("ITSM_WECOM_AESKEY")),
					CorpID:    firstNonEmpty(k.CorpID, os.Getenv("ITSM_WECOM_CORPID")),
					AgentID:   firstNonEmpty(k.AgentID, os.Getenv("ITSM_WECOM_AGENTID")),
					TenantID:  "default",
					ProcessID: firstNonEmpty(k.ProcessID, os.Getenv("ITSM_WECOM_PROCESS_ID")),
				}
			}
		}
		if ch, _ := inboundStore.GetByType(ctx, "default", inboundconfig.TypeDingTalk); ch != nil {
			var k struct {
				Token     string `json:"token"`
				AESKey    string `json:"aeskey"`
				ProcessID string `json:"process_id"`
			}
			json.Unmarshal([]byte(ch.Config), &k)
			token := firstNonEmpty(k.Token, os.Getenv("ITSM_DINGTALK_TOKEN"))
			if ch.Enabled || token != "" {
				c.DingTalk = inboundbot.DingTalkConfig{
					Enable:    true,
					Token:     token,
					AESKey:    firstNonEmpty(k.AESKey, os.Getenv("ITSM_DINGTALK_AESKEY")),
					TenantID:  "default",
					ProcessID: firstNonEmpty(k.ProcessID, os.Getenv("ITSM_DINGTALK_PROCESS_ID")),
				}
			}
		}
		if ch, _ := inboundStore.GetByType(ctx, "default", inboundconfig.TypeFeishu); ch != nil && ch.Enabled {
			var k struct {
				Token      string `json:"token"`
				Secret     string `json:"secret"`
				EncryptKey string `json:"encrypt_key"`
				ProcessID  string `json:"process_id"`
			}
			json.Unmarshal([]byte(ch.Config), &k)
			c.Feishu = inboundbot.FeishuConfig{
				Enable: true, Token: k.Token, Secret: k.Secret,
				EncryptKey: k.EncryptKey, TenantID: "default", ProcessID: k.ProcessID,
			}
		}
		return c
	}

	botSvc := inboundbot.New(inboundbot.Config{})
	botSvc.SetHandlers(inboundbot.Handlers{
		ReplyToTicket: func(ctx context.Context, tenantID, ticketID, author, body string) error {
			return itsmSvc.AddTicketComment(ctx, tenantID, ticketID, author, body)
		},
		CreateTicket: func(ctx context.Context, tenantID, channel, from, subject, body string) (string, error) {
			cfg := botSvc.CurrentConfig()
			var proc string
			switch channel {
			case "wecom":
				proc = cfg.WeCom.ProcessID
			case "dingtalk":
				proc = cfg.DingTalk.ProcessID
			case "feishu":
				proc = cfg.Feishu.ProcessID
			}
			req := itsmmodel.CreateTicketRequest{
				Title:         subject,
				Description:   body,
				RiskLevel:     itsmmodel.RiskLow,
				CreatedBy:     channel + ":" + from,
				CreatedByType: channel,
			}
			if proc != "" {
				req.ProcessID = proc
			}
			tctx := context.WithValue(ctx, "tenant_id", tenantID)
			tk, err := itsmSvc.CreateTicket(tctx, req)
			if err != nil {
				return "", err
			}
			return tk.ID, nil
		},
	})
	botSvc.SetConfigProvider(buildBotConfig)
	server.RegisterService(botSvc)

	// P2-5 CMDB 消息订阅：通知渠道经平台通知中心分发（避免 cmdb↔notification 循环依赖）
	cmdbSvc.SetNotifyCallback(func(ctx context.Context, tenantID, title, content string) error {
		return notifySvc.NotifyEvent(ctx, tenantID, string(notification.EventCMDBChanged), title, content)
	})

	// 初始化RBAC中间件,并将角色权限解析接入 IAM 角色库,
	// 使「系统设置 / 角色管理」中对权限的编辑实时生效(回落写死映射兜底)。
	rbacMiddleware := auth.NewRBACMiddleware(authProvider)
	rbacMiddleware.SetPermissionResolver(iamSvc.ResolvePermissions)
	server.WithMiddleware(rbacMiddleware.AutoPermissionMiddleware)

	aiSvc := ai.NewService(cfg, llmClient, rdbClient, db)
	if db != nil {
		aiSvc.Engine.SetStore(agentic.NewPGStore(db))
	}
	aiSvc.Engine.SetModelRouter(llmRouter) // 多模型路由池(权重+健康+失败降级)
	// 生成参数热更新:仅当配置显式给定时才覆盖引擎内置默认(温度0.7/上限1024/并发32)。
	if cfg.LLM.GenTemperature > 0 {
		aiSvc.Engine.SetGenParams(cfg.LLM.GenMaxTokens, cfg.LLM.GenTemperature)
	} else if cfg.LLM.GenMaxTokens > 0 {
		aiSvc.Engine.SetGenParams(cfg.LLM.GenMaxTokens, 0)
	}
	if cfg.LLM.MaxInflight > 0 {
		aiSvc.Engine.SetLLMMaxInflight(cfg.LLM.MaxInflight)
	}
	toolSet := agentic.NewMCPToolSet(db)
	toolSet.SetScriptExecutor(scriptSvc) // AI 的 script_run 工具经 script 服务→Agent 通道真实执行
	toolSet.SetEmbedder(llmClient)       // 知识库检索启用语义向量匹配
	// 审批策略: 引擎层按策略(每次询问/始终允许/仅敏感)做交互式确认, 白名单外且未获确认的脚本走 ITSM 工单兜底。
	toolSet.SetExternalApproval(aiSvc.Engine.ScriptApprovalChecker)
	// 知识库-AI 绑定: 按 Agent 解析其绑定的知识库分类, RAG 检索与 knowledge_search 工具据此过滤。
	toolSet.SetKnowledgeCategoriesResolver(aiSvc.Engine.AgentKnowledgeCategories)
	// P0-2: AI 可直接执行的脚本白名单(免人工审批); 缺省全部须经审批。格式: 逗号分隔的 script_id 或 script_name。
	if s := os.Getenv("OPSORIGIN_AI_APPROVED_SCRIPTS"); s != "" {
		var ids []string
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				ids = append(ids, p)
			}
		}
		toolSet.SetApprovedScripts(ids)
		slog.Info("ai script approval allowlist loaded", "count", len(ids))
	}
	toolSet.Register(aiSvc.Registry)
	aiSvc.SetRAGFunc(toolSet.RAGContext)
	ai.LLMConfigHook = func(cfg2 llm.Config) { aiSvc.UpdateLLMConfig(cfg2) }

	// AI 巡检闭环: 巡检发现转 ITSM 工单 + 完成/失败走平台通知中心(五渠道)。
	// 回调由 cmd/core 装配, 避免 internal/ai 直接依赖 itsm/notification 造成模块耦合。
	aiSvc.Engine.SetInspectionCallbacks(
		func(ctx context.Context, tenantID, title, description string, labels map[string]string) (string, error) {
			tctx := context.WithValue(ctx, "tenant_id", tenantID)
			ticket, err := itsmSvc.CreateTicket(tctx, itsmmodel.CreateTicketRequest{
				Title:         title,
				Description:   description,
				CreatedBy:     "ai-inspection",
				CreatedByType: "system",
				Labels:        labels,
			})
			if err != nil {
				return "", err
			}
			return ticket.ID, nil
		},
		func(ctx context.Context, tenantID, event, title, content string) error {
			return notifySvc.NotifyEvent(ctx, tenantID, event, title, content)
		},
	)
	// 数字员工运营效果: ITSM 工单统计(来源=ai_inspection 的工单)。
	aiSvc.Engine.SetTicketMetricsProvider(func(ctx context.Context, tenantID string) (int, map[string]int, float64, map[string]int, error) {
		return itsmSvc.TicketMetricsBySource(ctx, tenantID, "ai_inspection")
	})
	// P0-1 长期记忆: 工单处置成功后自动沉淀为 Agent 记忆(经验跨会话复用)
	itsmSvc.SetExperienceSink(func(ctx context.Context, tenantID, agentID, problem, resolution string, outcome bool) error {
		_, err := aiSvc.Engine.AddMemory(&agentic.ExperienceMemory{
			TenantID:   tenantID,
			AgentID:    agentID,
			Category:   "处置经验",
			Problem:    problem,
			Resolution: resolution,
			Outcome:    "succeeded",
			Source:     "disposal_case",
		})
		return err
	})
	// 场景包自动落库: 启用场景时可将推荐的标准化工单类型真实建到 itsm_catalog(幂等同名跳过)。
	if catalogStore != nil {
		aiSvc.Engine.SetSceneCatalogCreator(func(ctx context.Context, tenantID, name, description, category string) error {
			items, err := catalogStore.List(ctx, tenantID)
			if err != nil {
				return err
			}
			for _, it := range items {
				if it.Name == name {
					return agentic.ErrCatalogItemExists // 同名已存在, 幂等跳过(引擎不计入 created_catalog)
				}
			}
			return catalogStore.Create(ctx, &catalog.ServiceItem{
				TenantID:    tenantID,
				Name:        name,
				Description: description,
				Category:    category,
				Enabled:     true,
			})
		})
	}
	// 训练数据集导出(P3-1): 处置案例(知识库 disposal-case)经回调注入 AI 引擎。
	aiSvc.Engine.SetDisposalCaseProvider(func(ctx context.Context, tenantID string) ([]agentic.DisposalCase, error) {
		items, err := knowledgeSvc.ListDisposalCases(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		out := make([]agentic.DisposalCase, 0, len(items))
		for _, it := range items {
			out = append(out, agentic.DisposalCase{ID: it.ID, Title: it.Title, Content: it.Content})
		}
		return out, nil
	})
	server.RegisterService(aiSvc)
	aiSvc.Start(ctx)

	// —— 智能值班(P1): 告警路由到当班 Agent + AI 自动接管 ——
	// oncall(itsm 子包) 不依赖 monitor/ai: 告警经 monitor.AddAlertNotifier 注入路由,
	// AI 接管经 SetAgentHandler 注入(复用 ITSM 建单 + 通知中心, 与巡检闭环同模式)。
	if oncallSvc != nil {
		oncallSvc.SetAgentHandler(func(ctx context.Context, tenantID, agentID, alertTitle, alertDesc string) error {
			tctx := context.WithValue(ctx, "tenant_id", tenantID)
			_, err := itsmSvc.CreateTicket(tctx, itsmmodel.CreateTicketRequest{
				Title:         "[AI接管] " + alertTitle,
				Description:   alertDesc + "\n\n值班 Agent: " + agentID + "\n来源: 智能值班告警自动接管",
				CreatedBy:     "oncall-agent",
				CreatedByType: "system",
				Labels:        map[string]string{"source": "ai_oncall", "agent_id": agentID, "kind": "alert_takeover"},
			})
			if err != nil {
				return err
			}
			if notifySvc != nil {
				return notifySvc.NotifyEvent(ctx, tenantID, "ai.oncall_takeover", "值班Agent已接管告警: "+alertTitle, alertDesc)
			}
			return nil
		})
		monitorSvc.AddAlertNotifier(func(ae *monitormodel.AlertEvent) {
			if ae == nil || ae.IsSuppressed {
				return
			}
			oncallSvc.HandleAlert(context.Background(), &oncall.Alert{
				ID:          ae.ID,
				TenantID:    ae.TenantID,
				Title:       ae.Title,
				Description: ae.Description,
				Severity:    string(ae.Severity),
				Labels:      ae.Labels,
				TriggeredAt: ae.TriggeredAt,
			})
		})
	}

	opsSvc := ops.NewService(cfg, db)
	server.RegisterService(opsSvc)

	if deploySvc != nil {
		deploySvc.SetAgentRunner(agentSvc) // 注入 Agent 下发通道，使部署真实在目标主机执行
	}
	if knowledgeSvc != nil {
		socialStore := knowledge.NewSocialStore(db)
		server.RegisterService(socialStore)
		knowledgeSvc.SetSocialStore(socialStore)
	}
	// 自动沉淀策略: 可配置的"运维事件 → 知识库文章"沉淀规则(替代硬编码的处置案例沉淀)。
	if db != nil && knowledgeSvc != nil {
		server.RegisterService(knowledge.NewPrecipitationStore(db, knowledgeSvc))
	}

	// 漏洞扫描：规则匹配器 + 真实 inventory 注入(经 CMDB 适配器)。
	// 适配器把 cmdb.Service.ListCI 按约定 schema 映射为 vuln.InventoryEntry,
	// 解耦 vuln↔cmdb,无反向依赖。
	vulnSvc := vuln.NewService(cfg, db, cmdbInventoryAdapter{pgRepo: pgRepo})
	// 注入 Agent 下发通道(供 AgentScanner 经 Tunnel 下发 trivy/grype)。
	vulnSvc.SetAgentRunner(agentSvc)
	server.RegisterService(vulnSvc)

	go func() {
		if err := tunnelServer.Start(ctx); err != nil {
			slog.Error("tunnel server stopped", "error", err)
		}
	}()

	// 启动 gRPC tunnel server(Agent 直连 Core 通道)
	go func() {
		if err := grpcTunnelServer.Start(ctx); err != nil {
			slog.Error("grpc tunnel server stopped", "error", err)
		}
	}()

	go func() {
		if err := server.Serve(ctx); err != nil {
			slog.Error("server stopped", "error", err)
			cancel()
		}
	}()

	slog.Info("core server started", "addr", cfg.Core.Addr)
	<-sigCh
	slog.Info("shutting down...")
	server.GracefulStop()

	if rdbClient != nil {
		if err := rdbClient.Close(); err != nil {
			slog.Error("redis close error", "error", err)
		}
	}
}

// staticHandlerRegistrar 实现 routeRegistrar 接口，用于注册静态 HTTP Handler 到 mux。
type staticHandlerRegistrar struct {
	routes map[string]http.Handler
}

func (s *staticHandlerRegistrar) RegisterRoutes(mux *http.ServeMux) {
	for pattern, handler := range s.routes {
		mux.HandleFunc(pattern, handler.ServeHTTP)
	}
}

// agentChannelAdapter 实现 cmdb.AgentChannel:把 CMDB 统一调度的 channel=agent 采集任务
// 投影到 agentmgr(Agent 传输层)。CMDB 持有调度权(采集频率/启停),采集动作 delegated 给
// Agent 本地采集器执行并 push 上报。投影任务 ID 固定为 "cmdb:" + CmdbTaskID,保证幂等。
type agentChannelAdapter struct {
	agentSvc *agentmgr.Service
}

// SyncAgentCollectTask 把一条 cmdb 采集任务同步为 Agent 侧采集任务(投影 + 下发)。
func (a *agentChannelAdapter) SyncAgentCollectTask(ctx context.Context, spec cmdb.AgentCollectTaskSpec) error {
	agentType := agentmgr.CollectTypeCMDB
	switch spec.Kind {
	case "monitor":
		agentType = agentmgr.CollectTypeMetric
	case "cmdb":
		agentType = agentmgr.CollectTypeCMDB
	default:
		slog.Warn("agent channel: unknown collect kind, defaulting to cmdb", "kind", spec.Kind)
	}
	// 目标解析: "host:<agentID>" → 该 Agent;容器等前缀后续扩展(container 纳管投影)。
	// 未识别前缀回退为全量在线 Agent(空 TargetAgents),由 Agent 内置调度器按类型执行。
	var targetAgents []string
	if after, ok := strings.CutPrefix(spec.Target, "host:"); ok {
		targetAgents = []string{after}
	} else {
		slog.Warn("agent channel: unmapped collect target, projecting to all online agents", "target", spec.Target)
	}
	return a.agentSvc.UpsertCollectTaskFromCMDB(agentmgr.CMDBCollectSpec{
		CmdbTaskID:   spec.CmdbTaskID,
		TenantID:     spec.TenantID,
		AgentType:    agentType,
		TargetAgents: targetAgents,
		IntervalSec:  spec.IntervalSec,
		Enabled:      spec.Enabled,
	})
}

// RemoveAgentCollectTask 移除对应 Agent 侧投影任务(并推送空清单让 Agent 移除)。
func (a *agentChannelAdapter) RemoveAgentCollectTask(ctx context.Context, cmdbTaskID string) error {
	return a.agentSvc.RemoveCollectTaskFromCMDB(cmdbTaskID)
}

// cmdbInventoryAdapter 将 PG 资产表(pg.Repository.ListCI)按约定 schema 映射为 vuln.InventoryEntry。
// 约定:CI 的 attributes["software_packages"] 为 JSON 数组 [{"name":..,"version":..}];
// attributes["agent_id"] 为经 Agent 下发扫描的目标 agent(可选,供 AgentScanner 使用)。
// stub 模式(无 PG)pgRepo 为 nil,返回空(不造假)。
type cmdbInventoryAdapter struct {
	pgRepo *pg.Repository
}

func (a cmdbInventoryAdapter) ListHostInventory(ctx context.Context, tenantID string) ([]vuln.InventoryEntry, error) {
	if a.pgRepo == nil {
		return nil, nil
	}
	cis, err := a.pgRepo.ListCI(ctx, tenantID, "")
	if err != nil {
		return nil, err
	}
	var inv []vuln.InventoryEntry
	for _, ci := range cis {
		raw, ok := ci.Attributes["software_packages"]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var pkgs []struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(b, &pkgs) != nil {
			continue
		}
		agentID := ""
		if v, ok := ci.Attributes["agent_id"].(string); ok {
			agentID = v
		}
		for _, p := range pkgs {
			if p.Name == "" {
				continue
			}
			inv = append(inv, vuln.InventoryEntry{
				Host:    ci.Name,
				Package: p.Name,
				Version: p.Version,
				AgentID: agentID,
			})
		}
	}
	return inv, nil
}

// strAny 将任意值转为字符串(动作参数取值辅助)。
func strAny(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// messageInboxSender 把 ITSM 的站内消息投递桥接到 message.Service。
// ITSM 业务主体一律是 username(assignee / created_by / @提及),适配层只填
// Recipient.Username;收件箱查询与 WS 推送都按 username 命中,与前端的
// 当前用户展示名(= users.username)保持一致。
type messageInboxSender struct {
	svc *message.Service
}

func (a messageInboxSender) SendToUsers(ctx context.Context, tenantID string, usernames []string, m itsm.InboxMessage) error {
	recips := make([]message.Recipient, 0, len(usernames))
	for _, u := range usernames {
		recips = append(recips, message.Recipient{Username: u})
	}
	tpl := message.Message{
		TenantID: tenantID,
		Category: message.Category(m.Category),
		Level:    message.Level(m.Level),
		Title:    m.Title,
		Content:  m.Content,
		Link:     m.Link,
		RefType:  m.RefType,
		RefID:    m.RefID,
		Actor:    m.Actor,
	}
	return a.svc.DeliverTo(ctx, recips, tpl)
}
