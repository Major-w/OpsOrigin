# API 契约（跨端 REST 契约）

> 适用范围：前后端交界的一切 HTTP 交互。前端实现见 `web-next/src/lib/utils.ts`（统一 `apiFetch/apiList`），后端实现见 `cmd/core/server.go` 路由注册。
> 校验：`make contract` + 新增端点对照 `docs/api-routes.md` 与 `docs/swagger/`。

## A1 路径与方法

- **A1.1** 统一前缀 `/api/v1`，资源用复数名词（`/cmdb/ci`、`/itsm/tickets`），子资源用路径参数（`/cmdb/ci/{id}/archive`）。
- **A1.2** 方法语义：`GET` 读、`POST` 创建/动作（含批量、归档、评估等非纯 CRUD）、`PUT/PATCH` 更新、`DELETE` 删除。批量操作用 `POST /xxx/batch-*`。
- **A1.3** 新增路由必须在 `docs/api-routes.md` 登记，并确保 `docs/swagger/` 可服务（`server.go` 直接托管该目录）。

## A2 错误与响应

- **A2.1** 成功：`WriteJSON`（列表必须 `EmptySliceIfNil` → `[]`）；失败：`WriteError` 输出 `{ error: string }` 与非 2xx 状态码。
- **A2.2** 前端 `apiFetch` 统一行为：401 → 跳 `/login`；其余非 2xx → 抛 `Error(body.error || HTTP status)`，由页面 catch 后 `toast.error` 展示。禁止前端绕过 `apiFetch` 直用 `fetch`（下载/上传等 `apiFetchRaw` 例外）。
- **A2.3** 状态码语义：`400` 参数错、`401` 未认证、`403` 无权限、`404` 不存在、`409` 冲突、`500` 服务端。

## A3 分页与排序

- **A3.1** 列表统一分页参数：`limit`（默认 20）/ `offset`；排序 `sort_by` / `sort_order`（`asc|desc`）。
- **A3.2** 列表响应体若带总数需含 `total` 字段（如 `{ items, total }`）；纯数组列表允许返回裸数组。

## A4 权限点注册表（前后端共用）

> 前端 `useCan(key)` 与后端中间件使用**同一 key 字符串**，新增权限点必须同时更新下表与 `internal/iam/service.go`。

| 权限点 | 含义 | 已用页面 |
|---|---|---|
| `cmdb:read` | 查看 CMDB 资源（列表/详情/拓扑/治理只读） | instances/search/query/topology/panorama/idc/quality/impact/change-assess/stats |
| `cmdb:write` | 创建/编辑/修复 CMDB 资源 | instances 表单、quality fix、change-assess 变更 |
| `cmdb:delete` | 删除 CMDB 资源 | instances 删除 |
| `cmdb:admin` | CMDB 管理（模型/策略） | models、data-policy |
| `itsm:*` / `monitor:*` / `ai:*` 等 | 各模块权限点 | 见 `internal/iam/service.go` 全量 |

## A5 TS 类型同步

- **A5.1** 前端每个端点的返回必须有对应 `interface`（如 `CIModel`/`CIRelation`），禁止 `any` 裸传。
- **A5.2** 后端响应字段变更（改名/删字段/改类型）必须同步修改前端 interface 与使用处；两端契约以本文件 + `openapi.json` 为准。
- **A5.3** 复杂对象（CI 动态属性、表单 schema）在两端各留一份**宽类型**定义（`Record<string, any>` 限定作用域），不许全链 `any`。

## A6 校验清单

```bash
# 前端绕过 apiFetch？应 0 命中（apiFetchRaw 例外）
grep -rn "fetch(" web-next/src --include="*.tsx" --include="*.ts" | grep -v "apiFetch\|apiFetchRaw\|fetch(" | head
```
完整校验：`make contract`。
