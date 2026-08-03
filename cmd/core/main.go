package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"github.com/opsorigin/core/internal/automation/scheduler"
	"github.com/opsorigin/core/internal/automation/script"
	"github.com/opsorigin/core/internal/automation/workflow"
	cloud "github.com/opsorigin/core/internal/cloud"
	k8s "github.com/opsorigin/core/internal/cloud/k8s"
	_ "github.com/opsorigin/core/internal/cloud/providers/all"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/outbox"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/deploy"
	"github.com/opsorigin/core/internal/ent"
	"github.com/opsorigin/core/internal/iam"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/itsm/catalog"
	itsmmodel "github.com/opsorigin/core/internal/itsm/model"
	monitormodel "github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/internal/itsm/oncall"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/internal/keystore"
	"github.com/opsorigin/core/internal/knowledge"
	"github.com/opsorigin/core/internal/lowcode/service"
	"github.com/opsorigin/core/internal/monitor"
	healthhandler "github.com/opsorigin/core/internal/monitor/health"
	"github.com/opsorigin/core/internal/monitor/ingest"
	metricsregistry "github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/opsorigin/core/internal/notification"
	"github.com/opsorigin/core/internal/ops"
	"github.com/opsorigin/core/internal/sysconfig"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/internal/vuln"
	"github.com/opsorigin/core/internal/ws"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	entdbpkg "github.com/opsorigin/core/pkg/db"
	"github.com/opsorigin/core/pkg/llm"
	redisclient "github.com/opsorigin/core/pkg/redis"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
)

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

	// T03: 注册 bypass 前缀 — /metrics /healthz /readyz /api/v1/health 跳过认证/审计/RBAC 中间件
	server.WithBypassPrefixes("/metrics", "/healthz", "/readyz", "/api/v1/health")

	// T07: 给 Core http.Server 的 Handler 包一层 promhttp.InstrumentHandlerDuration
	server.WithHandlerWrapper(func(next http.Handler) http.Handler {
		return promhttp.InstrumentHandlerDuration(
			metricsregistry.HttpRequestDuration,
			next,
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

	processSvc := process.NewService(db)
	server.RegisterService(processSvc)

	itsmSvc.SetProcessStore(processSvc)
	slaStore := sla.NewStore(db)
	server.RegisterService(slaStore)
	itsmSvc.SetSLAStore(slaStore)
	catalogStore := catalog.NewStore(db)
	server.RegisterService(catalogStore)

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
	// 配置 OPSNEXUS_CORE_VM_ADDR 后写入 VictoriaMetrics(高频 monitor, Prometheus 文本导入);
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
		slog.Warn("metrics ingest: OPSNEXUS_CORE_VM_ADDR not set, using in-memory sink (demo only)")
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

	// 系统配置管理服务
	sysconfigSvc := sysconfig.NewService(cfg)
	server.RegisterService(sysconfigSvc)

	scriptSvc := script.NewService(db)
	server.RegisterService(scriptSvc)

	workflowSvc := workflow.NewService(db)
	workflowSvc.SetScriptExecutor(scriptSvc)
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

	schedulerSvc := scheduler.NewService(db)
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

	knowledgeSvc := knowledge.NewService(db)
	server.RegisterService(knowledgeSvc)

	oncallSvc := oncall.NewService(db)
	server.RegisterService(oncallSvc)

	keySvc := keystore.NewService(db)
	server.RegisterService(keySvc)

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
	knowledgeSvc.SetEmbedder(llmClient) // 注入向量生成器,新建/更新知识库文章自动写入 embedding

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
	knowledgeSvc.StartReindexLoop(context.Background(), 30*time.Second)

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

	userSvc := user.NewService(db, authProvider)
	server.RegisterService(userSvc)

	// IAM:角色/用户组/权限管理(PG 存储,预置 3 内置角色 + 权限点目录)
	iamSvc := iam.NewService(db)
	server.RegisterService(iamSvc)

	// 通知管理:邮件/钉钉/企业微信/飞书/Webhook 五种渠道 + 事件订阅策略
	notifySvc := notification.NewService(db)
	server.RegisterService(notifySvc)
	schedulerSvc.SetNotifier(notifySvc)

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
	aiSvc.Engine.SetStore(agentic.NewPGStore(db))
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
	if s := os.Getenv("OPSNEXUS_AI_APPROVED_SCRIPTS"); s != "" {
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
	// 场景包自动落库: 启用场景时可将推荐的标准化工单类型真实建到 itsm_catalog(幂等同名跳过)。
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
		return notifySvc.NotifyEvent(ctx, tenantID, "ai.oncall_takeover", "值班Agent已接管告警: "+alertTitle, alertDesc)
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

	opsSvc := ops.NewService(cfg, db)
	server.RegisterService(opsSvc)

	deploySvc := deploy.NewService(db)
	deploySvc.SetAgentRunner(agentSvc) // 注入 Agent 下发通道，使部署真实在目标主机执行
	server.RegisterService(deploySvc)
	server.RegisterService(knowledge.NewSocialStore(db))

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
