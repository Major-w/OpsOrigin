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
	"syscall"
	"time"

	cloud "github.com/opsorigin/core/internal/cloud"
	_ "github.com/opsorigin/core/internal/cloud/providers/all"
	k8s "github.com/opsorigin/core/internal/cloud/k8s"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/outbox"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/ent"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/itsm/catalog"
	"github.com/opsorigin/core/internal/itsm/process"
	"github.com/opsorigin/core/internal/itsm/sla"
	"github.com/opsorigin/core/internal/vuln"
	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/internal/audit"
	"github.com/opsorigin/core/internal/monitor"
	healthhandler "github.com/opsorigin/core/internal/monitor/health"
	metricsregistry "github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/opsorigin/core/internal/ops"
	"github.com/opsorigin/core/internal/tunnel"
	"github.com/opsorigin/core/internal/user"
	"github.com/opsorigin/core/internal/ws"
	"github.com/opsorigin/core/internal/agentmgr"
	"github.com/opsorigin/core/internal/automation/script"
	"github.com/opsorigin/core/internal/automation/workflow"
	"github.com/opsorigin/core/internal/automation/scheduler"
	"github.com/opsorigin/core/internal/command"
	"github.com/opsorigin/core/internal/iam"
	"github.com/opsorigin/core/internal/notification"
	"github.com/opsorigin/core/internal/itsm/knowledge"
	"github.com/opsorigin/core/internal/itsm/oncall"
	"github.com/opsorigin/core/internal/keystore"
	"github.com/opsorigin/core/internal/lowcode/service"
	"github.com/opsorigin/core/internal/sysconfig"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/config"
	entdbpkg "github.com/opsorigin/core/pkg/db"
	"github.com/opsorigin/core/pkg/llm"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	redisclient "github.com/opsorigin/core/pkg/redis"
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

	// T03: 注册 bypass 前缀 — /metrics 和 /health 跳过认证/审计/RBAC 中间件
	server.WithBypassPrefixes("/metrics", "/api/v1/health")

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
	healthHandler := healthhandler.NewHealthHandler(db, rdb)
	server.RegisterService(&staticHandlerRegistrar{
		routes: map[string]http.Handler{
			"GET /api/v1/health": healthHandler,
		},
	})

	cmdbSvc := cmdb.NewService(cfg, pgRepo, dgClient)
	server.RegisterService(cmdbSvc)

	// 云资源接出：注册 4 云 Provider（空白导入触发 init）并把云 Service 接入 Core mux，
	// 经 cloudCMDBAdapter 把发现的资源同步进 CMDB（复用完整 CI 生命周期）。
	cloudSvc := cloud.NewService(cloud.NewMemStore(), cloudCMDBAdapter{pgRepo: pgRepo, cmdbSvc: cmdbSvc})
	server.RegisterService(cloudSvc)

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
	server.RegisterService(sla.NewStore(db))
	server.RegisterService(catalog.NewStore(db))

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

	agentSvc := agentmgr.NewService(db, rdb, entClient)
	// 设置 TLS 状态(影响安装脚本中下载链接的 scheme)
	if cfg.TLS.Enabled {
		agentSvc.SetTLSEnable(true)
	}
	agentSvc.SetTunnelServer(tunnelServer)
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

	// 初始化沙箱环境:在 Server 端自动注册一个本地 Agent,用于脚本调试
	agentSvc.InitSandbox()

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
	// 让定时任务真正触发 workflow 执行（而非只存定义）。
	schedulerSvc.SetWorkflowTrigger(func(ctx context.Context, tenantID, workflowID string) error {
		_, err := workflowSvc.Trigger(ctx, tenantID, workflowID, nil, nil)
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
	})

	userSvc := user.NewService(db, authProvider)
	server.RegisterService(userSvc)

	// IAM:角色/用户组/权限管理(独立内存存储,预置 3 个内置角色 + 14 个权限点)
	iamSvc := iam.NewService(db)
	server.RegisterService(iamSvc)

	// 通知管理:邮件/钉钉/企业微信/飞书/Webhook 五种渠道 + 事件订阅策略
	notifySvc := notification.NewService(db)
	server.RegisterService(notifySvc)
	schedulerSvc.SetNotifier(notifySvc)

	// 初始化RBAC中间件
	rbacMiddleware := auth.NewRBACMiddleware(authProvider)
	server.WithMiddleware(rbacMiddleware.AutoPermissionMiddleware)

	aiSvc := ai.NewService(cfg, llmClient, rdbClient, db)
	ai.LLMConfigHook = func(cfg2 llm.Config) { aiSvc.UpdateLLMConfig(cfg2) }
	server.RegisterService(aiSvc)
	aiSvc.Start(ctx)

	opsSvc := ops.NewService(cfg, db)
	server.RegisterService(opsSvc)

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
