# OpsOrigin v1.2 — 可观测性基础设施 & ORM 升级 PRD

| 文档版本 | 作者     | 日期       | 状态 |
| -------- | -------- | ---------- | ---- |
| v1.0     | 许清楚   | 2026-07-24 | 草稿 |

---

## 1. 产品目标（一句话）

**为 OpsOrigin 引入类型安全的 ORM 数据层并补齐可观测性、健康检查、异步任务与滚动升级能力，使平台具备支撑 8000-10000 台被管主机的生产级可运维性。**

---

## 2. 用户故事

### US-1：平台运维工程师 — 可观测性看板
> 作为平台运维工程师，我希望 OpsOrigin 暴露标准的 Prometheus `/metrics` 端点，以便我将 Core 实例接入 Grafana 监控面板，实时掌握 gRPC 连接数、心跳 QPS、HTTP 请求 P99 延迟和 goroutine 数量。当 Core 实例异常时，我能在 30 秒内通过告警定位问题。

### US-2：K8s 运维工程师 — 就绪探针
> 作为负责部署 OpsOrigin 的 K8s 运维工程师，我希望有一个标准的 `/api/v1/health` 健康检查端点，Kubelet 能通过它判断 Core 是否就绪。当 PG 或 Redis 不可用时返回 503，避免流量打到不健康的 Pod 导致用户请求失败。

### US-3：平台开发者 — ORM 开发体验
> 作为 OpsOrigin 的 Go 后端开发者，我希望用 ent 的 Schema-as-Code 方式定义数据库表结构，通过 `go generate` 自动生成类型安全的 CRUD 代码，替代手写 SQL 字符串和 `Scan` 的维护负担。编译期即可发现字段类型错误，而不是上线后才炸。

### US-4：运维管理员 — 连接池调优
> 作为运维管理员，我希望通过环境变量 `OPSORIGIN_POSTGRES_MAX_CONNS` 调整 PG 连接池大小，而不必重新编译代码。当被管主机数量从 1000 扩展到 8000 时，我能根据实际负载动态调整连接数，避免连接耗尽或资源浪费。

### US-5：运维管理员 — 批量任务下发
> 作为运维管理员，当我需要对 500 台 Agent 批量下发安全补丁任务时，希望 Core 不会因为串行执行而阻塞其他操作。任务的创建和分发应异步化，我只需提交一次批量请求，系统自行排队分发，并通过任务状态 API 查询各 Agent 的执行进度。

### US-6：安全运维工程师 — 滚动升级
> 作为安全运维工程师，当 Agent 二进制存在安全漏洞需要全量升级时，我希望通过 Core 上传新版 Agent 二进制，设定分批策略（如每批 10%），系统自动按批下发升级指令、校验 SHA256 checksum，失败自动回滚到上一版本，避免全量升级导致的业务中断。

---

## 3. 需求池

### P0 需求

| ID  | 功能                     | 描述                                                                                                                                                                                                                                                                       | 验收标准                                                                                                                                                                                                                                                                 |
| --- | ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| F01 | **可观测性 `/metrics`**  | 在 Core HTTP Server 上注册 `GET /metrics` 端点，由 `promhttp` 暴露默认 Go 运行时指标（goroutine/mem/allocs）+ 自定义业务指标：`agentmgr_grpc_connections`（gRPC 连接数）、`agentmgr_heartbeat_qps`（心跳每秒数）、`http_request_duration_seconds`（HTTP 请求延迟 histogram）。 | 1. `curl localhost:8080/metrics \| grep agentmgr` 返回非空指标行；2. 心跳模拟 100 个 Agent 持续上报，`agentmgr_heartbeat_qps` 稳定在 ~100；3. 指标采集对 Core 主流程 CPU 开销 <1%；4. 指标增加 `instance` / `tenant_id` label 以区分多 Core 实例和多租户。               |
| F02 | **健康检查 `/api/v1/health`** | 实现 HealthHandler，依次 ping PG(`SELECT 1`) 和 Redis(`PING`)，任一失败返回 `{"status":"unhealthy","services":{"postgres":"down","redis":"ok"}}` 并设置 HTTP 503；全部正常返回 200。超时 3 秒。                                                                               | 1. `curl /api/v1/health` → 200；2. 手动 `docker stop postgres` 后 → 503，response body 中 `postgres: "down"`；3. 恢复 PG 后 → 200，无人工重启；4. endpoint 不经过 auth middleware（可由 server.go 单独挂载）。                                                            |

### P1 需求

| ID  | 功能                   | 描述                                                                                                                                                                                           | 验收标准                                                                                                                                                                                                                                                |
| --- | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| F03 | **PG 连接池可配**      | 在 `config.Load()` 中新增 `OPSORIGIN_POSTGRES_MAX_CONNS` 环境变量（默认 25），注入 `db.SetMaxOpenConns()`。同时暴露 `OPSORIGIN_POSTGRES_MAX_IDLE_CONNS`（默认 5）和 `OPSORIGIN_POSTGRES_CONN_MAX_LIFETIME`（默认 30m）。 | 1. 设置 `OPSORIGIN_POSTGRES_MAX_CONNS=50` 启动 Core，`SHOW max_connections` 或 pprof 中观察到连接池上限为 50；2. 不设环境变量时默认值为 25；3. 设置 `MAX_CONNS=0` 时使用 `database/sql` 默认行为（无上限）+ 日志 Warn；4. 配置变更无需重新编译。 |
| F04 | **任务下发队列**        | 在 `agentmgr` 中增加 `TaskQueue` 抽象。P0 阶段用 Go channel（buffered 1000）实现进程内异步，后续可替换为 Redis Stream。`BulkCreateTasks` 批量写入 PG `agent_tasks` 表后，逐个 push 到队列，由 worker goroutine 消费并分发。 | 1. 调用 `POST /api/v1/agents/tasks/bulk` 提交 500 个任务，接口在 500ms 内返回（任务记录已落库）；2. 各 Agent 异步收到任务并在 60s 内全部完成；3. worker panic 不影响 Core 主流程，含 recover + 重试 3 次；4. 队列深度可配（`OPSORIGIN_TASK_QUEUE_SIZE`，默认 1000）。 |
| F05 | **滚动升级通道**        | Core 新增 `POST /api/v1/agents/upgrade/upload` 上传 Agent 二进制 + SHA256；`POST /api/v1/agents/upgrade/rollout` 设定策略（`batch_size`, `batch_interval`）。Agent 心跳时获取升级元数据，下载新版本、校验 checksum、替换并重启。 | 1. 上传 Agent v2.0 二进制后，SHA256 checksum 校验通过；2. 设置 `batch_size: 10` → 每 60s 最多 10 个 Agent 升级；3. 某 Agent 下载失败或 checksum 不匹配 → 状态标记为 `upgrade_failed`，不重启，保留旧版本继续运行；4. 升级进度可通过 `GET /api/v1/agents/upgrade/status` 查询。 |

---

## 4. ent ORM 迁移方案

### 4.1 当前现状

当前 ~10 张 PG 表分布在以下模块，均使用手写 SQL + `database/sql.Rows.Scan`：

| 模块            | 表名                  | 状态     |
| --------------- | --------------------- | -------- |
| `agentmgr`      | `agent_hosts`         | 已实现   |
| `agentmgr`      | `agent_proxies`       | 已实现   |
| `agentmgr`      | `agent_tasks`         | 已实现   |
| `notification`  | `notification_channels` | 已实现 |
| `notification`  | `notification_policies` | 已实现 |
| `notification`  | `notification_history`  | 已实现 |
| `iam`           | `iam_roles`           | 已实现   |
| `iam`           | `iam_groups`          | 已实现   |
| `iam`           | `iam_user_roles`      | 已实现   |
| `ai`            | `ai_conversations`    | 已实现   |
| `keystore`      | `keystore_entries`    | 已实现   |
| `automation/script`   | 脚本表           | 已实现   |
| `automation/workflow` | 工作流表         | 已实现   |
| `automation/scheduler` | 定时任务表       | 已实现   |
| `itsm/knowledge` | 知识库表              | 已实现   |
| `itsm/oncall`   | 值班表                | 已实现   |
| `cmdb/pg`       | CMDB 资产表           | 已实现   |

### 4.2 迁移策略（分阶段）

**Phase 1：建立 ent schema + 双写验证（本次迭代）**

1. 在 `internal/ent/schema/` 下为上述所有表定义 ent Go Schema struct（字段、索引、Edge 关系）。
2. `go generate ./internal/ent` 生成类型安全 client。
3. 新建 `pkg/db/entdb.go` 封装 ent Client 初始化。
4. 选取 `agentmgr` 模块作为试点，将其 `pgStore` 逐步替换为 ent 调用。
5. 其余模块的旧 SQL 代码保持不动，待 Phase 1 试点稳定后再逐模块迁移。

**Phase 2：全量迁移（下一迭代）**

6. 逐模块迁移至 ent，删除手写 SQL。
7. 移除 `internal/**/pgstore.go` 中对 `database/sql` 的直接引用。

### 4.3 验收标准

- ent schema 定义与当前 PG DDL 完全一致（字段名/类型/约束/索引）。
- `go generate` 通过，无编译错误。
- 现有集成测试（`service_test.go`）全部通过。
- 无损迁移：API 响应结构与旧实现完全一致。

---

## 5. 技术约束

| 约束项                     | 说明                                                                                       |
| -------------------------- | ------------------------------------------------------------------------------------------ |
| Go 版本                    | Go 1.22+（当前 go.mod 声明 1.25.0，满足要求）                                              |
| ent 版本                   | `entgo.io/ent` v0.14+（最新稳定版）                                                       |
| 新增构建步骤               | `go generate ./...` 需在 `go build` 前执行（Makefile 中增加 `generate` target）            |
| 向后兼容                   | 旧 API 路径、请求/响应结构不变；不删除旧接口，不更改路由签名                               |
| 中间件隔离                 | `/metrics` 和 `/api/v1/health` 不经过 auth/audit/RBAC 中间件                               |
| 多租户                     | 指标中不暴露 `tenant_id` 的敏感业务数据内容，仅作为 label 使用                             |
| 不引入新外部依赖           | prometheus 指标用 `prometheus/client_golang`（目前未引入，需 added via `go get`）；健康检查无新依赖 |

---

## 6. 待确认问题

### Q1：可观测性指标后端选型 — 是否引入 VictoriaMetrics？

当前架构文档中提到时序数据库使用 VictoriaMetrics（VM）。`/metrics` 端点可以用 Prometheus 直接 scrape，也可以 push 到 VM 的 InfluxDB line protocol 接口。

- **方案 A**：仅暴露 `/metrics`，由运维侧 Prometheus + Grafana 拉起（简单，零额外开发）。
- **方案 B**：内置 VM Remote Write 推送，Core 主动将指标写入 VM（适合 SaaS 部署）。

**建议架构师决策**：本期是否只做 Prometheus pull 模式，VM push 延后到 v1.3？

### Q2：任务下发队列 — Channel vs Redis Stream？

P0 阶段建议用 buffered channel（Go 进程内），但存在 Core 重启丢失队列中未分发任务的风险。

- **方案 A**：Go channel + 重启后从 PG `agent_tasks` 表补拉 `status='pending'` 任务（可靠、复杂度低）。
- **方案 B**：Redis Stream + Consumer Group（支持多 Core 实例消费、持久化）。

**建议架构师决策**：本期是否先用方案 A（channel + PG 补偿），Redis Stream 留到 v1.3 多实例部署时再升级？

### Q3：ent 迁移 — 全量表一次性迁移还是渐进式试点？

ent Schema 定义是 all-or-nothing 的（每个 schema 文件对应一张表），但代码调用可以渐进替换。

- **方案 A**：一次性定义所有 schema，`go generate` 后，只改 `agentmgr` 模块代码，其余模块保留旧 SQL（建议）。
- **方案 B**：全部 15+ 张表一次性迁移到 ent，删除所有旧 pgstore.go。

**建议架构师决策**：是否同意方案 A — 全量 schema 定义 + 单模块试点？

---

## 7. 排期建议

| 迭代       | 内容                                              | 预估工作量 |
| ---------- | ------------------------------------------------- | ---------- |
| v1.2-S1    | ent schema 定义 + go generate + agentmgr 试点迁移 | 3-4 人天   |
| v1.2-S2    | `/metrics` + `/api/v1/health`                     | 2-3 人天   |
| v1.2-S3    | PG 连接池可配 + 任务下发队列                      | 2-3 人天   |
| v1.2-S4    | 滚动升级通道                                      | 3-4 人天   |
| v1.2-S5    | 集成测试 + 联调 + 文档                            | 2 人天     |
| **合计**   |                                                   | **12-17 人天** |

---

## 附录 A：涉及文件清单

```
internal/ent/schema/           — [新增] ent schema 定义
internal/agentmgr/pgstore.go   — [重构] agentmgr 试点迁移至 ent
internal/monitor/metrics/      — [新增] Prometheus 指标采集
internal/monitor/health/       — [新增] /api/v1/health handler
cmd/core/server.go             — [修改] 注册 /metrics + /health + 新中间件
cmd/core/main.go               — [修改] 注入 ent client + 连接池配置 + task queue
pkg/config/config.go           — [修改] 新增 PG 连接池、升级策略、任务队列配置项
pkg/db/                        — [新增] ent client 初始化封装
Makefile                       — [修改] 新增 generate target
```
