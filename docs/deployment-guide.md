# OpsOrigin 部署手册

> 覆盖从依赖准备到升级回滚的标准流程。OpsOrigin 采用三层架构：
> **Core(中心控制面) ↔ Proxy(隔离区代理) ↔ Agent(受控主机)**，边缘由
> Agent/Proxy 主动向中心发起连接，中心不对边缘开放入向端口。

## 1. 环境要求

- 运行 Core / Proxy / Agent 的 Linux 服务器（x86_64 / arm64），或 Docker 20.10+ / Kubernetes 1.24+
- 依赖服务：PostgreSQL 16（第一事实源）、Dgraph（图存储，拓扑读路径）、Redis 7（限流熔断/缓存）
- 构建环境：Go 1.25+、Node.js 22+（前端）

## 2. 依赖服务

| 服务 | 用途 | 默认端口 |
| --- | --- | --- |
| PostgreSQL | 关系存储（第一事实源，RLS 多租户） | 5432 |
| Dgraph Zero / Alpha | 图存储（读拓扑，Outbox 异步同步） | 5080 / 7080 / 9080 |
| Redis | 令牌桶限流 / 熔断 / 会话缓存 | 6379 |

一键拉起（docker-compose，含数据卷、健康检查与初始化 SQL）：

```bash
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis
```

## 3. 构建

```bash
make build        # 产出 bin/{core,proxy,agent,k8s-probe} 四个二进制
# 前端（可选，独立部署时）：
cd web-next && npm ci && npm run build
```

容器镜像（经 go.work 聚合构建，四者构建方式一致）：

```bash
docker build -f deploy/Dockerfile -t opsorigin/core:latest .             # Core
docker build -f deploy/Dockerfile.proxy -t opsorigin/proxy:latest .       # Proxy
docker build -f deploy/Dockerfile.k8s-gateway -t opsorigin/k8s-gateway . # K8s 网关 Agent
docker build -f deploy/Dockerfile.k8s-probe -t opsorigin/k8s-probe .      # K8s 探针
```

## 4. 部署方式

### 4.1 单机（docker-compose 全量）

`deploy/docker-compose.yml` 已含 core 服务；`start.sh` 可一键拉起依赖 + 后端 + 前端：

```bash
./start.sh            # 依赖 + 后端 + 前端
./start.sh status     # 查看运行状态
./start.sh logs b     # 后端日志（f=前端）
```

### 4.2 单机（systemd，自愈 + 开机自启）

```bash
sudo install -m 0755 bin/core /usr/local/bin/opsorigin-core
sudo cp .env.example /etc/opsorigin/core.env
sudoedit /etc/opsorigin/core.env   # 必须替换全部占位密钥，OPSORIGIN_ENV=production
sudo install -m 0644 deploy/systemd/opsorigin-core.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now opsorigin-core
```

Proxy / Agent 同理，unit 文件见 `deploy/systemd/`。`Restart=on-failure` 自愈，
`EnvironmentFile` 统一管理配置。

### 4.3 Kubernetes

```bash
# 1. 创建密钥 Secret（生产环境密钥切勿写进清单 commit）
kubectl -n opsorigin create secret generic opsorigin-core-secrets \
  --from-literal=OPSORIGIN_JWT_SECRET=<64字节随机串> \
  --from-literal=OPSORIGIN_TUNNEL_AUTH_SECRET=<64字节随机串> \
  --from-literal=OPSORIGIN_POSTGRES_DSN='postgres://opsorigin:opsorigin@postgres:5432/opsorigin?sslmode=disable' \
  --from-literal=OPSORIGIN_FILE_TOKEN_SECRET=<32字节随机串>

# 2. 部署 Core（含 Service + HPA，就绪/存活探针走 /readyz /healthz）
kubectl apply -f deploy/k8s/core.yaml

# 3. 隔离区 Proxy（TUNNEL_AUTH_SECRET 必须与 Core 一致）
kubectl -n opsorigin create secret generic opsorigin-proxy-secrets \
  --from-literal=OPSORIGIN_TUNNEL_AUTH_SECRET=<与Core相同的密钥>
kubectl apply -f deploy/k8s/proxy.yaml

# 4. 边缘侧（可选）：K8s 网关 Agent 与集群探针
kubectl apply -f deploy/k8s/opsorigin-k8s-gateway.yaml   # 资源纳管网关
kubectl apply -f deploy/k8s/probe-daemonset.yaml          # 节点探针 DaemonSet
```

> 镜像地址 `<your-registry>/...` 需替换为实际 registry；HPA 依赖集群安装
> metrics-server。

## 5. 配置项说明

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OPSORIGIN_ENV` | `development` | `production` 时启动强制凭据强度校验（fail-fast） |
| `OPSORIGIN_JWT_SECRET` | dev 默认值 | JWT 签名密钥，生产必须 ≥32 字节强随机 |
| `OPSORIGIN_TUNNEL_AUTH_SECRET` | 空 | Core 与 Proxy 隧道共享密钥，必须一致 |
| `OPSORIGIN_POSTGRES_DSN` | 本地 dev DSN | PostgreSQL 连接串 |
| `OPSORIGIN_DGRAPH_TARGET` | `localhost:9080` | Dgraph alpha gRPC 地址 |
| `OPSORIGIN_REDIS_ADDR` | `localhost:6379` | Redis 地址 |
| `OPSORIGIN_TUNNEL_INSECURE` | `false` | 生产必须保持 `false` |
| `OPSORIGIN_CORE_PORT` / `OPSORIGIN_CORE_GRPC_LISTEN` | `8080` / `:9091` | Core HTTP / 隧道 gRPC 监听 |

完整清单见 `.env.example`；系统设置页可在线查看（敏感值掩码显示）。

## 6. 升级与回滚

**升级**（compose / 单机）：
1. 拉取新镜像或编译新二进制；
2. 执行迁移：Core 启动时自动执行 `deploy/migrations/*.sql`（幂等），也可手动
   `psql $OPSORIGIN_POSTGRES_DSN -f deploy/migrations/0XX_xxx.sql`；
3. 滚动重启 Core → Proxy → Agent（顺序不可颠倒，Core 先就绪）。

**升级**（Kubernetes）：`kubectl set image deployment/opsorigin-core core=<new-image>`，
滚动发布由 Deployment 自动完成；观察 `kubectl rollout status`。

**回滚**：切回上一镜像/二进制标签即可；涉及表结构变更的迁移回退需先评估
数据兼容性（迁移均为增量 `IF NOT EXISTS` 风格，多数可直接跳过）。

## 7. 常见问题（FAQ）

- **Core 启动即退出**：检查日志是否报 `production 环境禁止使用默认/占位/过短密钥`
  —— 生产环境未配置强密钥会 fail-fast，这是安全行为；按 §5 配置后重启。
- **Proxy 无法注册**：确认 `OPSORIGIN_TUNNEL_AUTH_SECRET` 与 Core 一致、
  `OPSORIGIN_TUNNEL_INSECURE=false`、Core 的 gRPC 端口（默认 9091）可达。
- **Dgraph 同步延迟**：Outbox worker 默认 1s 轮询（`OPSORIGIN_OUTBOX_POLL_INTERVAL`）；
  检查 Core 日志中 outbox batch 是否报错；Dgraph 不可达时 Core 以 stub 模式
  继续运行（Postgres 仍是源真相），恢复后自动补同步。
- **迁移失败恢复**：迁移为增量幂等（`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`），
  失败多为权限问题；确认 DSN 用户有 DDL 权限后重试即可。
