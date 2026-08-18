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

## 8. 日志与存储持久化（各组件）

### 8.1 总览

`deploy/docker-compose.yml` 为有状态组件声明了 5 个 **Docker 命名卷**；Core / 前端
**无状态**（数据全部落在依赖组件中），日志输出到容器 stdout。

| 组件 | 状态 | 数据卷 | 数据内容 | 日志去向 |
| --- | --- | --- | --- | --- |
| PostgreSQL | 有状态 | `pg_data` → `/var/lib/postgresql/data` | 全量业务数据（第一事实源） | 容器 stdout |
| Redis | 有状态 | `redis_data` → `/data` | 限流/熔断/会话缓存/心跳 | 容器 stdout |
| MinIO | 有状态 | `minio_data` → `/data` | 附件与对话产物（ITSM/知识库/工作区） | 容器 stdout |
| Dgraph Zero | 有状态（可选） | `dgraph_zero` → `/data` | 图数据（拓扑读路径，PG 为源真相） | 容器 stdout |
| Dgraph Alpha | 有状态（可选） | `dgraph_alpha` → `/data` | 图数据 | 容器 stdout |
| Core | **无状态** | 无（只读 docker.sock + `opsorigin_logs` 日志卷） | — | stdout（JSON）+ 固定文件 `/var/log/opsorigin/core.log` |
| 前端 | **无状态** | `opsorigin_logs` 日志卷 | — | stdout 重定向 `/var/log/opsorigin/frontend.log` |

命名卷默认存放在 Docker 数据目录（Linux 通常 `/var/lib/docker/volumes/`）。生产环境
建议：**① 大文件/数据库改 bind mount 到独立磁盘；② 配置日志轮转；③ 定期备份**。

### 8.2 卷持久化位置与改 bind mount

命名卷路径（Linux 默认）：

```bash
/var/lib/docker/volumes/deploy_pg_data/_data      # 注意卷名带 compose 项目前缀 deploy_
/var/lib/docker/volumes/deploy_redis_data/_data
/var/lib/docker/volumes/deploy_minio_data/_data
/var/lib/docker/volumes/deploy_dgraph_zero/_data
/var/lib/docker/volumes/deploy_dgraph_alpha/_data
```

改 bind mount（把数据放到宿主机固定目录，便于备份/迁移），修改
`deploy/docker-compose.yml` 对应服务：

```yaml
  postgres:
    volumes:
      # - pg_data:/var/lib/postgresql/data   # 原命名卷
      - /srv/opsorigin/pgdata:/var/lib/postgresql/data   # 改为 bind mount
      - ./migrations/001_init.sql:/docker-entrypoint-initdb.d/001_init.sql
```

> **首次切换注意事项**：bind mount 目录需先赋权（`chown -R 999:999` 或按镜像
> USER 配置），且**空目录首次启动会重新初始化**（001_init.sql 会重灌）；已有数据
> 迁移请先用 `docker run --rm -v deploy_pg_data:/src -v /srv/opsorigin/pgdata:/dst
> alpine cp -a /src/. /dst/` 拷贝，再切挂载。

### 8.3 PostgreSQL：备份与恢复

```bash
# 逻辑备份（推荐日常）：全库导出
docker compose -f deploy/docker-compose.yml exec -T postgres \
  pg_dump -U opsorigin -d opsorigin -Fc -f /tmp/opsorigin.dump
docker compose -f deploy/docker-compose.yml cp postgres:/tmp/opsorigin.dump ./backup-$(date +%F).dump

# 恢复（新建库）
docker compose -f deploy/docker-compose.yml exec -T postgres \
  pg_restore -U opsorigin -d opsorigin --clean --if-exists /tmp/opsorigin.dump

# 任意时刻的一致性快照（PG 需与逻辑备份结合判断点）
docker compose -f deploy/docker-compose.yml exec -T postgres \
  pg_basebackup -D /tmp/pgbase -U opsorigin -h 127.0.0.1   # 需要足够权限，见 PG 文档
```

数据迁移（换机/换盘）：停 postgres → 卷整体拷贝（见 §8.2）→ 启 postgres。

### 8.4 Redis：持久化与备份

默认配置为 **RDB 快照**（`redis:7-alpine` 默认 save 策略，数据写入 `/data/dump.rdb`，
重启不丢）。如需更高可靠性开启 AOF：

```bash
# 临时验证（直接改命令参数）
docker compose -f deploy/docker-compose.yml exec redis \
  redis-cli CONFIG SET appendonly yes

# 持久化配置：compose 中 redis 服务加 command 或在 redis.conf 挂载
  redis:
    command: ["redis-server", "--appendonly", "yes", "--appendfsync", "everysec"]
```

备份：

```bash
docker compose -f deploy/docker-compose.yml exec redis redis-cli SAVE   # 触发快照
docker compose -f deploy/docker-compose.yml cp redis:/data/dump.rdb ./redis-$(date +%F).rdb
```

### 8.5 MinIO：对象存储备份（附件与对话产物）

MinIO 存有：ITSM 附件、知识库附件、对话产物归档（`chat/` 前缀）。备份用官方客户端：

```bash
docker compose -f deploy/docker-compose.yml exec minio sh -c \
  'mc alias set local http://localhost:9000 opsorigin opsorigin123 >/dev/null && \
   mc mirror local/opsorigin /backup/minio-$(date +%F)'
```

> 生产应修改 `OPSORIGIN_S3_ACCESS_KEY / SECRET_KEY`（compose 中 `opsorigin`/`opsorigin123`
> 为开发默认值）；MinIO 自身也支持 `mc mirror` 双活/异地同步。

### 8.6 Dgraph：备份（可选组件）

Dgraph 为可选组件（拓扑读路径；Postgres 是源真相，重建可回放）。备份：

```bash
# 方式一：卷快照（最简单，需停机一致点）
docker compose -f deploy/docker-compose.yml stop dgraph-alpha dgraph-zero
tar czf dgraph-$(date +%F).tgz /var/lib/docker/volumes/deploy_dgraph_*/_data
docker compose -f deploy/docker-compose.yml start dgraph-alpha dgraph-zero

# 方式二：dgraph live backup（在线，alpha 内执行）
docker compose -f deploy/docker-compose.yml exec dgraph-alpha \
  dgraph live --alpha 127.0.0.1:9080 --backup /data/backup --namespace 0
```

> 重建后 Core 的 Outbox 会自动补同步拓扑（PG → Dgraph），无需手工对账。

### 8.7 日志持久化与轮转

**固定日志文件（compose 已内置）**：所有组件日志落到共享卷 `opsorigin_logs`
（容器内 `/var/log/opsorigin/`）：

| 组件 | 日志文件 | 机制 |
| --- | --- | --- |
| Core | `/var/log/opsorigin/core.log` | 代码级：`OPSORIGIN_CORE_LOG_FILE` 配置后 slog 双写（stdout + 文件，JSON，追加模式，目录自动创建） |
| 前端 | `/var/log/opsorigin/frontend.log` | compose `command` 将 stdout/stderr 重定向到文件 |
| 其他依赖（pg/redis/minio/dgraph） | 容器 stdout | 见下方 json-file 轮转 |

宿主机查看（命名卷默认路径）：

```bash
tail -f /var/lib/docker/volumes/deploy_opsorigin_logs/_data/core.log
```

日志卷改 bind mount（compose `opsorigin_logs` 命名卷 → 宿主机目录，便于 ELK/审计）：

```yaml
volumes:
  opsorigin_logs:
    driver: local
    driver_opts:
      type: none
      o: bind
      device: /srv/opsorigin/logs    # 宿主机固定目录
```

**实时/界面查看**：
- `docker compose -f deploy/docker-compose.yml logs -f core`（stdout 通道）
- `系统管理 → 自监控`（`/system/health`）内置日志查看器
  （Core 内存环形缓冲 600 条 + 其他容器经 docker socket 读取）

**文件轮转**（`core.log` / `frontend.log` 为追加模式，靠外部 logrotate）：

```bash
# /etc/logrotate.d/opsorigin
/srv/opsorigin/logs/*.log {
    daily
    rotate 14
    compress
    delaycompress
    missingok
    copytruncate
}
```

**容器 stdout 轮转**（依赖组件日志，compose 顶层或按服务）：

```yaml
  core:
    logging:
      driver: json-file
      options:
        max-size: "50m"      # 单文件上限
        max-file: "5"        # 保留 5 个文件（共 ~250MB）
        compress: "true"
```

### 8.8 备份策略建议

| 组件 | 频率建议 | 保留 | 命令 |
| --- | --- | --- | --- |
| PostgreSQL | 每日全量 + 每 6h WAL（可选 PITR） | 7–30 天 | §8.3 |
| MinIO | 每日增量/全量 `mc mirror` | 7–30 天 | §8.5 |
| Redis | 每日 SAVE 快照 | 3–7 天 | §8.4 |
| Dgraph | 每周（可选组件） | 30 天 | §8.6 |

> **恢复演练**：至少每月一次在测试环境执行 pg_restore + minio mirror 回灌验证，
> 备份是否可恢复只有演练过才算数。

### 8.9 常见问题

- **容器重建后数据还在吗**：只要卷未删（`docker compose down` 不删卷，
  `docker compose down -v` 才删），数据都在；
- **误删卷如何恢复**：卷被删后数据不可逆，只能从备份恢复——务必先 §8.8 备份；
- **`docker compose up -d` 后 Postgres 数据为空**：检查是否误加 `-v`（volumes 清除）
  或 bind mount 指向了空目录（§8.2 首次切换会重灌）。

