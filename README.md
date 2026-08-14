# OpsOrigin Core

下一代 AI 原生全自动运维控制面底座。基于 Go + React，深度集成大模型 AI Agent 与人在回路（HITL）控制技术。

## 架构

```
┌─────────────────────────────────────────────────────────┐
│                      Core (中心控制面)                     │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐          │
│  │ CMDB │ │ITSM  │ │Monitor│ │  AI  │ │Audit │          │
│  └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘          │
│     │        │        │        │        │               │
│  ┌──┴────────┴────────┴────────┴────────┴──┐            │
│  │         Tunnel Server (反向隧道)           │            │
│  └────────────────────┬──────────────────────┘            │
├───────────────────────┼──────────────────────────────────┤
│              Proxy (隔离区代理)                            │
│         Tunnel Client ←→ Agent Registry                  │
├───────────────────────┼──────────────────────────────────┤
│                  Agent (受控主机)                          │
└───────────────────────┴──────────────────────────────────┘
```

## 快速开始

### 前提条件

- Go 1.25+
- Node.js 22+
- Docker & Docker Compose（可选）

### 依赖服务

OpsOrigin Core 依赖三个外部服务：PostgreSQL（结构化数据）、Dgraph（图数据库，CMDB 拓扑关系）、Redis（限流熔断与缓存）。`deploy/docker-compose.yml` 已内置相同配置，版本与镜像如下：

| 服务 | 镜像 | 版本 | 用途 |
|------|------|------|------|
| PostgreSQL | `postgres:16-alpine` | 16 | CMDB 资产、审计日志、用户/ITSM/脚本/工作流等结构化数据 |
| Dgraph (zero) | `dgraph/dgraph:latest` | latest | 图数据库 Zero 节点，集群调度 |
| Dgraph (alpha) | `dgraph/dgraph:latest` | latest | 图数据库 Alpha 节点，存储 CMDB 拓扑关系图，暴露 gRPC:9080 |
| Redis | `redis:7-alpine` | 7 | AI 限流熔断、会话缓存、分布式状态 |

单独拉取镜像：

```bash
docker pull postgres:16-alpine
docker pull dgraph/dgraph:latest
docker pull redis:7-alpine
```

通过 docker-compose 一键启动（已含数据卷、健康检查与初始化 SQL 挂载）：

```bash
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis
```

连接参数见 `.env.example`，默认如下（本地开发）：

- PostgreSQL DSN：`postgres://opsorigin:opsorigin@localhost:5432/opsorigin?sslmode=disable`
- Dgraph gRPC：`localhost:9080`
- Redis：`localhost:6379`

> 生产环境建议将 Dgraph 镜像从 `latest` 固定为具体版本号（如 `dgraph/dgraph:v25.4.0`），避免拉取到不兼容版本。

### 一键启动

项目根目录提供 `start.sh` 脚本，可一键拉起依赖服务（PostgreSQL / Dgraph / Redis）、后端 Core 和前端 web-next。

```bash
./start.sh            # 启动所有服务（依赖 + 后端 + 前端）
./start.sh status     # 查看运行状态
./start.sh logs b     # 查看后端日志（f=前端）
./start.sh stop        # 停止所有服务
./start.sh restart     # 重启所有服务
```

脚本会自动完成：依赖检查、`.env` 初始化、Docker 依赖服务启动与就绪探测、后端 `go run ./cmd/core`、前端 `npm run dev`，日志写入 `logs/`，PID 文件写入 `.run/`。

首次启动后访问：

- 前端：<http://localhost:3000>（登录 `admin / admin123`）
- 后端：<http://localhost:8080>

其他子命令（按需单独启动某一部分）：

```bash
./start.sh deps        # 仅启动依赖服务（PG/Dgraph/Redis）
./start.sh backend      # 仅启动后端
./start.sh frontend    # 仅启动前端
./start.sh help         # 查看完整帮助
```

### 手动启动（可选）

如需分别手动启动各组件：

```bash
# 复制环境配置
cp .env.example .env

# 启动依赖服务
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis

# 启动 Core
go run ./cmd/core

# 启动前端（另开终端）
cd web-next
npm install
npm run dev
```

### 构建

```bash
make build        # 编译所有二进制
make test         # 运行单元测试(快, 无外部依赖)
make test-integration  # 集成测试(需 PostgreSQL + Dgraph, 见下)
make lint         # 代码检查
```

### 集成测试

集成测试（`//go:build integration` 标签）连接真实的 PostgreSQL / Dgraph，
验证跨组件链路：CMDB 写库 → Outbox → Dgraph 拓扑同步（ASSERT-CMDB-01/02 载体）、
ITSM 工单 PG 存取流转、Dgraph 客户端完整方法（拓扑/影响分析/环路检测）。

```bash
# 1. 拉起依赖（与本地开发一致）
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis

# 2. 跑集成测试（未起的依赖对应用例自动 Skip，不会失败）
make test-integration
```

依赖连接参数沿用 `.env` 的 `OPSORIGIN_POSTGRES_DSN` / `OPSORIGIN_DGRAPH_TARGET`。
CI 的 `integration` job 会在每次 push / PR 时以干净环境自动执行。

> 单元测试与集成测试分轨：日常 `make test` 不触碰外部依赖，保持秒级。

## 模块

| 模块 | 说明 | 状态 |
|------|------|------|
| CMDB | 资产配置中心，PG + Dgraph 异构多模 | ✅ |
| Monitor | 可观测性，动态发现 + 拨测 + eBPF + 告警收敛 | ✅ |
| ITSM | 工单引擎，8 态状态机 + 4 级风险 + HITL | ✅ |
| AI Agent | MCP 工具箱 + 限流熔断 + 执行计划编排 | ✅ |
| Audit | TraceID 全链路 + 双主体归因 + RLS | ✅ |
| Tunnel | gRPC 反向隧道，Core↔Proxy↔Agent 三层 | ✅ |

## 核心断言

系统严格遵循以下 10 条绝对安全红线：

- [ASSERT 01] AI 变更必须经过 ITSM 审批
- [ASSERT 02] 60s/10 次限流 + 3 连败熔断
- [ASSERT 04] 边缘零入向端口
- [ASSERT 05] Agent 完全无状态
- [ASSERT 07] 渠道权威权重 + Value Diff 防抖
- [ASSERT 08] 无 TraceID 一票否决
- [ASSERT 09] 所有查询强制 tenant_id 注入
- [ASSERT-CMDB-01] 严禁显式双写
- [ASSERT-CMDB-02] 拓扑环路检测阻断
- [ASSERT-MONITOR-02] 告警必须经拓扑抑制
