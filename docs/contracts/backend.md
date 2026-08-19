# 后端开发契约（Backend Contract）

> 适用范围：`internal/**` `pkg/**` `cmd/**`。从既有代码实践中提炼的强制约定。
> 校验：`make contract` 自动化 + 本文件逐条人工自查。

## B1 统一响应

- **B1.1** 所有 HTTP handler 的响应必须经 `pkg/httputil` 的 `WriteError` / `WriteJSON`（列表响应用 `EmptySliceIfNil` 保证空列表返回 `[]` 而非 `null`）。禁止在 handler 里手写 `w.Write([]byte(...))` / `json.NewEncoder`。
- **B1.2** 错误结构统一 `{ error: string }`（或带 code），非 2xx 一律走 `WriteError`。
- **B1.3** 请求体解析统一 `httputil.BindJSON(r, &req)`，禁止手写 `json.NewDecoder(r.Body).Decode`（2026-08-19 已全仓替换 206 处）。

## B2 Context 透传

- **B2.1** 禁止在 service/store 层使用 `context.Background()` / `context.TODO()` 作为请求上下文；必须透传 handler 的 `r.Context()`（含租户、鉴权、链路信息）。
- **B2.2** 新增函数签名若有 context 参数，调用链必须逐层下传，禁止中途丢弃。
- **B2.3** 租户取数统一 `auth.TenantFromContext(ctx)`（裸 key → principal → "default" 兜底），**禁止再定义本地 `tenantFromCtx` 副本**（2026-08-19 已收敛 11 份 → 1）。

## B3 权限点注册

- **B3.1** 权限点（`<module>:read/write/admin/delete/...`）必须**集中注册**于 `internal/iam/service.go`（现状：`cmdb:read/write/admin/delete` 等），禁止散落各模块字符串硬编码。
- **B3.2** 新增模块权限点须同步更新 `docs/contracts/api.md` 权限点注册表，并确保前端 `useCan` 使用同一 key。

## B4 SQL 安全红线（统一报表模块已立，全仓生效）

- **B4.1** 禁止裸 SQL 拼接：动态条件必须字段白名单 + `$N` 占位符。
- **B4.2** 禁止 `GROUP BY` 列序号（应使用列名）。
- **B4.3** 租户条件强制注入：所有查询必须带租户过滤，禁止跨租户读取。
- **B4.4** 报表类 SQL 的维度/指标白名单由模块在装配期 `report.Register` 声明，`internal/report` 零反向依赖。

## B5 分层与依赖方向

- **B5.1** 分层：`handler → service → store`（或 `pkg`），路由集中在 `cmd/core/server.go` 注册，禁止在 handler 中直接写 SQL / 操作 DB。
- **B5.2** 依赖单向：业务模块可依赖 `pkg/` 与共享基础设施；禁止 `pkg/` 反向依赖业务模块。
- **B5.3** 采集器可插拔：新增采集器实现 `kind` 注册进「采集器注册表」，调度器按注册表执行；CMDB/监控调度器不得硬编码具体采集实现。

## B6 共享基础设施红线

- **B6.1** `pkg/redis.Client.Conn()` 返回**共享** go-redis client，**禁止对其 Close**（会毒化全服务限流/心跳/缓存）；探活一律用 `Ping(ctx)`。
- **B6.2** 各组件探活方式：redis 用 `Ping`、dgraph 用 TCP Dial（Close 自己新建的连接）、minio 用 `Health`。
- **B6.3** 容器网络内访问依赖用容器名（如 `dgraph-alpha:9080`），探活/连接目标禁止用宿主 localhost（compose 未映射端口的情况）。

## B7 错误处理语义

- **B7.1** 正常降级（如某采集器单类失败、次要依赖不可用）应**日志记录后跳过**，禁止当作 fatal 处理或阻断主流程。
- **B7.2** 用户可感知的错误必须带可读 message 透传到前端（经 `WriteError`），禁止吞错或仅 `fmt.Println`。

## B8 校验清单（提交前自查）

```bash
grep -rn "context.Background()" internal/ pkg/ cmd/   # 应趋近 0（仅允许无请求上下文的初始化/定时任务）
grep -rnE "SELECT .* FROM" internal/ --include="*.go" # 命中须人工确认在 report 白名单体系内
gofmt -l .                                            # 必须为空
go vet ./...                                          # 必须通过
```
完整校验：`make contract`。
