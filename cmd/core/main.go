package main

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/dgraph"
	"github.com/opsorigin/core/internal/cmdb/outbox"
	"github.com/opsorigin/core/internal/cmdb/pg"
	"github.com/opsorigin/core/internal/itsm"
	"github.com/opsorigin/core/internal/ai"
	"github.com/opsorigin/core/internal/audit"
	"github.com/opsorigin/core/internal/monitor"
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
	"github.com/opsorigin/core/pkg/llm"
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
	rdbClient, err = redisclient.NewClient(cfg.RedisAddr)
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
	}

	auditSvc := audit.NewService(cfg, db)

	server := NewServer(cfg)
	server.RegisterService(auditSvc)
	server.WithMiddleware(authProvider.Middleware)
	server.WithMiddleware(auditSvc.Middleware)

	var rdb *redis.Client
	if rdbClient != nil {
		rdb = rdbClient.Conn()
	}

	cmdbSvc := cmdb.NewService(cfg, pgRepo, dgClient)
	server.RegisterService(cmdbSvc)

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

	agentSvc := agentmgr.NewService()
	// 设置 TLS 状态(影响安装脚本中下载链接的 scheme)
	if cfg.TLS.Enabled {
		agentSvc.SetTLSEnable(true)
	}
	agentSvc.SetTunnelServer(tunnelServer)
	// §5 文件分发:注入中心存储目录、短期令牌密钥、Server 对外基址(直连 Agent 下载地址)。
	agentSvc.SetFileStoreDir(cfg.FileStore.Dir)
	agentSvc.SetFileTokenSecret(cfg.FileStore.TokenSecret)
	agentSvc.SetServerBaseURL(cfg.Core.PublicURL)
	server.RegisterService(agentSvc)

	// 初始化沙箱环境:在 Server 端自动注册一个本地 Agent,用于脚本调试
	agentSvc.InitSandbox()

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

	knowledgeSvc := knowledge.NewService()
	server.RegisterService(knowledgeSvc)

	oncallSvc := oncall.NewService()
	server.RegisterService(oncallSvc)

	keySvc := keystore.NewService()
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
	iamSvc := iam.NewService()
	server.RegisterService(iamSvc)

	// 通知管理:邮件/钉钉/企业微信/飞书/Webhook 五种渠道 + 事件订阅策略
	notifySvc := notification.NewService()
	server.RegisterService(notifySvc)

	// 初始化RBAC中间件
	rbacMiddleware := auth.NewRBACMiddleware(authProvider)
	server.WithMiddleware(rbacMiddleware.AutoPermissionMiddleware)

	aiSvc := ai.NewService(cfg, llmClient, rdbClient, db)
	ai.LLMConfigHook = func(cfg2 llm.Config) { aiSvc.UpdateLLMConfig(cfg2) }
	server.RegisterService(aiSvc)
	aiSvc.Start(ctx)

	opsSvc := ops.NewService(cfg, db)
	server.RegisterService(opsSvc)

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
