# OpsOrigin 开发工作总结 — 2026-07-31

> 本日聚焦两大块：**AI 助手差距修复**（上午-下午）与 **Agent 能力建设**（晚上）。
> 全部改动通过 `go build` / `go vet` / 单测验证，前端 TS 无新增错误。

---

## 一、AI 助手模块（差距分析 → 全部落地）

> 起点：`outputs/ai-frontend-gap-analysis.md` 走查报告（58 前端页面 + 26 后端 AI 端点）

### 1. 思维链（CoT）：模式 → 全局开关 + 流式
- 设计修正：CoT 是**展示维度**，不是独立模式，改为全局「💭 思考过程」开关（localStorage 持久化）
- 后端：SSE 新增 `event: thinking` 命名事件，与回答 token 分流（`StreamChunk` 类型）
- 前端：普通/自主模式均生效，thinking 以可折叠 `<details>` 展示
- 文件：`engine.go`、`service.go`、`stores/ai.ts`、`ai/page.tsx`

### 2. 自主模式（explore）
- `/ai/chat` 新增 `explore` 参数；注入"规划→执行→检查→总结"方法论（`<plan>/<step>/<report>` 标签）
- 工具循环上限 5 → 12；前端「自主」Tab 卡片式流式渲染
- 命名决策：`solo` → **自主模式**（用户拍板，PRD 同步更新）

### 3. 敏感操作审批策略（PRD 5.2.5）
- 新增 `approval.go`：`always_ask` / `always_allow` / `sensitive_only` 三模式，全局 + Agent 级覆盖，超时 60s 视为拒绝（fail-closed）
- SSE `event: ask` 交互确认流：AI 要执行敏感操作 → 前端弹窗（操作/参数/截止时间）→ 批准执行 / 拒绝停止
- 双轨兜底：未获交互确认的脚本仍走 ITSM 工单离线审批
- 配置 UI：AI 设置 →「审批策略」Tab

### 4. 知识库-AI 关联绑定（PRD 5.2.6）
- 绑定维度 = 知识库文章分类；Agent `knowledge_categories`（extra JSONB 持久化）
- RAG 检索与 `knowledge_search` 工具按绑定分类过滤；留空 = 全部（兼容旧行为）
- 配置 UI：Agent 定义表单勾选绑定分类

### 5. Agent 定义增强（PRD 5.2.2）
- `AgentInstance` 加 `Tools`（per-Agent MCP 工具白名单，空=全部）+ `Skills`（能力指令注入 system prompt）
- `buildToolDefs(agentID)` 按白名单过滤工具；`renderSkills()` 注入「【你可用的技能】」块
- UI：Agent 表单升级为完整配置（基础信息/提示词/知识库/工具/Skills）

### 6. 自定义 MCP 工具注册（PRD 5.2.2）
- 新增 `customtool.go`：HTTP 工具（URL 模板 `{param}` 占位 + 请求头 + 超时）与 CLI 工具（shell 模板，带风险警示）
- 动态注册/反注册（`Registry.Unregister`）；`ai_custom_tools` 表持久化；启动自动加载
- CRUD API：`POST/GET/PUT/DELETE /api/v1/ai/tools/custom`

### 7. 审计日志完善
- 后端：`AuditFilter` 加主体模糊/关键词/成功状态过滤；`Query` 返回 total 分页计数
- 前端：筛选栏（关键词/类型/主体/结果/日期）+ 分页 + 详情弹窗 + CSV 导出

### 8. 监控中心合理拆分
- 按工作流聚类为 3 页：`/monitor` 总览（告警滚动展示）/ `/monitor/alerts` 告警中心（含处理历史轨迹）/ `/monitor/targets` 监控配置
- 后端：`alertHistory` 接线（确认/解决记录，环形 200 条），`?status=history` 查询
- PRD 缺失能力（插件库/转义/收敛/通知）在总览页标注"规划中"，不虚构

### 9. CMDB 相关页面 → ⏸️ 暂缓
- 用户决策：CMDB 数据模型/关系/发现整体不完善，非单页问题，暂缓（报告已标注）

---

## 二、Agent 能力建设（三能力闭环）

> 需求：场景 1 = Server 主机装 Agent 默认沙箱隔离供测试；场景 2 = 所有 Agent 三种能力并存（自动化执行 / CMDB 采集 / 监控采集）

### 1. Agent 执行模式升级（Mode 字段）
- `TaskRequest` 加 `Mode`：`sandbox`（隔离）/ `collect`（只读采集）/ `normal`（常规+审批），空时回退旧 `Sandbox` 布尔（向后兼容）
- 修复隐藏 bug：轮询任务路径此前不检查 Sandbox（只有 tunnel 模式走沙箱）
- `sandbox.go` 重构：公共 `runScriptIsolated(task, mode)` 骨架，`collect` 模式 = 允许读主机 + 写限制 + 环境净化 + 拦截逃逸高危 + venv，为将来 bwrap 化预留分叉点

### 2. Server 侧采集任务管理
- 新增 `collect.go` + `collect_handlers.go`：`CollectTask`（cmdb | metric 类型）+ 调度器（10s 扫描到期派发）+ CRUD API
- 派发 `Mode=collect` 任务给在线目标 Agent；完成回调按类型解析入库
- 前端 `/auto/collect` 数据采集页（任务管理 + 运行记录 + 指标数据）

### 3. 采集持久化 + 指标落库
- `CollectStore` 接口 + pgStore 实现（`collect_tasks` 表，重启不丢）
- `collect_metrics` 表 + 批量事务写入 + 查询 API（`GET /api/v1/collect-metrics`）

### 4. Server 默认沙箱池
- `ApplySandboxPool(ctx)`：Server 启动自动给**本机 hostname 匹配的 Agent** 打沙箱标签
- 环境变量：`OPSNEXUS_SANDBOX_AGENTS`（额外指定）、`OPSNEXUS_SANDBOX_AUTO=0`（关闭）
- 主机列表沙箱开关（`PUT /agents/{id}/sandbox` + `/resources` toggle）确认已存在

---

## 三、架构决策记录

| 决策 | 结论 |
|------|------|
| CoT 定位 | 展示维度 → 全局开关，非独立模式 |
| 自主模式命名 | solo → 自主模式（业界主流 Agent/auto，避免与"Agent"概念冲突） |
| 审批策略默认 | sensitive_only（仅敏感操作询问）+ fail-closed 超时 |
| Agent 定义 | 知识库/工具/Skills/提示词在"定义 Agent"时配置，非会话入口 |
| 沙箱定位 | 进程级隔离（防脏），非容器级（防逃逸）；后续可 bwrap 化 |
| 采集任务隔离 | `collect` 模式"读开放 + 写限制"；严禁静默降级 |
| CMDB 页面 | 暂缓，待数据闭环后再统一补页面 |

---

## 四、遗留待办

### Agent 能力
- [ ] 采集指标接入 monitor 图表（monitor metrics 为内存模型，需单独改造）
- [ ] 采集任务完整调度历史审计
- [ ] 沙箱 bwrap 化（无容器运行时也能容器级隔离）—— 需"能力探测 + 三级降级策略"

### AI 助手 / 前端
- [ ] 告警治理（第三方接入/转义/收敛/通知升级）
- [ ] 监控采集插件库 + 多步骤拨测
- [ ] 聊天体验（停止按钮/普通模式 Markdown 升级）
- [ ] 知识库分类元数据管理、审批策略 Agent 级入口
- [ ] 自定义工具调试/测试
- [ ] CMDB 整体（数据模型/关系/发现 → 再补页面）

---

## 五、关键文件清单

### 新增
- `internal/ai/agentic/approval.go`、`customtool.go`
- `internal/agentmgr/collect.go`、`collect_handlers.go`、`collect_pg.go`
- `web-next/src/app/(dashboard)/monitor/alerts/page.tsx`、`monitor/targets/page.tsx`
- `web-next/src/app/(dashboard)/auto/collect/page.tsx`

### 核心修改
- `internal/ai/agentic/engine.go`（CoT/explore/审批/工具白名单/Skills/自定义工具）
- `internal/ai/service.go`、`internal/ai/mcp/registry.go`、`internal/ai/model/model.go`
- `internal/audit/*`、`internal/monitor/*`、`internal/agentmgr/*`
- `cmd/agent/agent.go`、`cmd/agent/sandbox.go`、`cmd/core/main.go`
- `web-next/src/app/(dashboard)/ai/*`、`audit/*`、`monitor/*`、`auto/collect/*`

---

*详细差距跟踪见 `outputs/ai-frontend-gap-analysis.md`；每日记录见 `.workbuddy/memory/2026-07-31.md`。*
