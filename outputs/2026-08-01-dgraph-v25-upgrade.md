# OpsOrigin Dgraph v24 → v25 升级说明

日期：2026-08-01
适用版本：v24.0.0 → v25.4.0（2026-07-30 发布）

## 一、升级内容总览

| 组件 | 升级前 | 升级后 |
|------|--------|--------|
| Dgraph 镜像（zero/alpha） | `dgraph/dgraph:v24.0.0` | `dgraph/dgraph:v25.4.0` |
| Go SDK（dgo） | `dgo/v240 v240.2.0` | `dgo/v250 v250.0.0` |
| 业务代码改动 | — | `client.go`：import + NewClient + upsert 重构 + 2 处预存 bug 修复 |

## 二、为什么升级

1. **License**：v25 起单一 Apache 2.0，原企业功能（二进制备份/静态加密/审计日志/多命名空间）全部开源
2. **维护**：v25 是活跃主线（v25.4.0 刚发布），v24 已是旧分支
3. **安全**：v25.4.0 修复了多个 CVE；Zero admin 端点（6080）默认不再对远程开放（安全强化）

## 三、代码改动清单

### 1. `go.mod`
```
github.com/dgraph-io/dgo/v240 v240.2.0  →  github.com/dgraph-io/dgo/v250 v250.0.0
```
连带升级：grpc v1.71→v1.75.1、protobuf、otel 等传递依赖（`go mod tidy` 自动处理）。

### 2. `internal/cmdb/dgraph/client.go`

**a. import 路径**：`v240` → `v250`（2 行）

**b. `NewClient` 必须显式传 insecure 传输凭证**（v250 行为变更）：
```go
// v240: dgo.NewClient(target) 内部默认注入 insecure
// v250: 不再默认注入, 必须显式传入, 否则报 "no transport security set"
dg, err := dgo.NewClient(target,
    dgo.WithGrpcOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
```

### 3. `deploy/docker-compose.yml`
```yaml
image: dgraph/dgraph:v24.0.0  →  image: dgraph/dgraph:v25.4.0
# dgraph-zero 新增 --security(v25.4.0 Zero admin 端点安全强化)
command: dgraph zero --my=dgraph-zero:5080 --security "whitelist=127.0.0.1,dgraph-zero,dgraph-alpha"
```

## 四、⚠️ 升级过程中发现并修复的 4 个预存 Bug

> 这些 bug 在 v24 上一直存在，**CMDB 拓扑链路此前从未真正工作过**。本次升级通过真实 Dgraph 端到端验证（docker 起 v24 alpha + dgo v250 客户端跑完整 CRUD/拓扑/影响/环检测）暴露并全部修复：

| # | Bug | 影响 | 修复 |
|---|-----|------|------|
| 1 | schema 含空 `type Tenant.ci` 声明 | Dgraph 解析报 `Missing colon`，schema 建不起来 | 删除空 type 声明 |
| 2 | `UpsertCI`/`UpsertRelation` 用空白节点 `_:id` | 重复 upsert 生成**重复节点**（无 upsert 语义） | 重构为 Dgraph upsert 块（`query { ... u as uid }` + `uid(u)` mutation） |
| 3 | `UpsertRelation` 把业务 ID 直接写 `<source_ci> <%s>` | Dgraph uid 谓词要求 `0x` 十六进制 UID，**关系从未真正落库** | 先 `resolveCIUID` 解析业务 ID → UID 再写入 |
| 4 | `QueryTopology` 用 `@recurse` + 嵌套谓词 / parse 把 `source_ci` 声明为数组 | `@recurse` 要求谓词平铺报语法错；对象→数组静默解空，**拓扑永远返回空** | 改为显式 2 层查询；parse 中 `source_ci` 改为对象类型 |

另修复：`detectCycleInGraph` 忽略自环（`source_ci` 指向自身不算业务环）；`resolveCIUID`/`resolveRelationUID` 改用 `eq(ci_id/rel_id, ...)` 查询（原 `uid(%s)` 语法错误）。

**Schema 新增谓词**：`ci_id: string @index(exact)`、`rel_id: string @index(exact)`（业务 ID 落库，供解析/删除）。

### 4. 顺带修复 `internal/cloud/cmdb_sync_test.go`
`TestCITypeOf` 预存失败：K8s 类型测试期望误写 `cloud_k8s_cluster`，实际是显式映射分支 `k8s_cluster`（K8s 经 Agent 通道发现，非 Provider，不带 cloud_ 前缀）。**该失败与 dgo 升级无关（升级前已存在）**。

## 五、数据迁移（关键步骤）

> ⚠️ **升级前必须先备份**。v24→v25 未查到官方明确的"文件格式直接兼容"声明，**建议走 export/import 迁移**。

```bash
# 1. 备份: 在旧集群(v24)上导出
docker compose exec dgraph-alpha dgraph export --outdir /data/export
docker cp <alpha容器>:/data/export ./dgraph-backup-v24-$(date +%Y%m%d)

# 2. 停旧集群 → 改 compose 镜像为 v25.4.0 → 起新集群
docker compose up -d dgraph-zero dgraph-alpha

# 3. 导入(把备份目录挂载/拷贝进新容器)
docker cp ./dgraph-backup-v24-$(date +%Y%m%d) <新alpha容器>:/data/import
docker compose exec dgraph-alpha dgraph live \
  --files /data/import/*.rdf.gz --schema /data/import/schema.json \
  --zero dgraph-zero:5080

# 4. 验证
curl -s localhost:8081/health | jq .
curl -s "localhost:8080/api/v1/cmdb/topology/<ci_id>" | jq .
```

> 注意：旧数据中 **CI 没有 `ci_id` 谓词**（预存 bug #2/#3 导致业务 ID 未落库），迁移后旧节点无法通过 `resolveCIUID` 按业务 ID 解析。**建议迁移后重建拓扑数据**（从 PG 重新同步），或对存量数据执行一次补齐 `ci_id`/`rel_id` 的脚本。

## 六、验证结果（真实 Dgraph 端到端）

- `go build ./...` ✅
- 全量测试 21 包 ✅ 零失败（含新增 `internal/cmdb/dgraph` 集成测试包）
- 新增 `TestClientCompat_V250`：docker 起真实 Dgraph + dgo v250 客户端，验证 schema/upsert(含重复 upsert 不建重复节点)/删除/拓扑(非空断言)/影响分析(无环断言)/环检测 全链路 ✅

## 七、回滚方案

```bash
git revert <升级commit>          # 代码回滚
git checkout dev -- deploy/docker-compose.yml  # compose 回滚到 v24
docker compose up -d dgraph-zero dgraph-alpha
```

## 八、注意事项

1. **Zero 安全强化**：v25.4.0 起 Zero admin 端点默认仅放行 loopback，compose 已显式配 whitelist
2. **dgo v250 依赖**：`NewClient` 需显式 insecure（见 §三.2.b）；升级 grpc/protobuf/otel 传递依赖
3. **CMDB 拓扑链路修复**：升级后拓扑/影响分析/环检测 API 首次真正可用（此前因预存 bug 返回空）
4. README 版本示例已同步更新为 v25.4.0
