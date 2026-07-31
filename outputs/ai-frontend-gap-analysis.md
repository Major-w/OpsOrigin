# OpsOrigin AI 助手 & 前端页面 差距分析报告

> 分析时间：2026-07-31（首次）
> 最近更新：2026-07-31（同步今日已完成改动）
> 范围：web-next/ 前端 + internal/ai/ 后端

---

## 一、AI 助手优化点

### ✅ 已完成 — AI 自主模式
- PRD 5.2.4："聊天框支持自主模式，支持 AI 自己主动规划、调用工具、多步执行"
- **2026-07-31 已实现**：`/api/v1/ai/chat` 新增 `explore` 参数；后端注入"规划→执行→检查→总结"方法论（`<plan>/<step>/<report>` 结构化标签），工具循环上限 5→12；前端「自主」Tab 卡片式流式渲染（计划列表/工具调用面板/推理折叠/执行报告）
- 安全：复用现有 RBAC + 限流 + script_run 审批闸门；prompt 约束优先只读工具
- **遗留**：交互级"敏感操作确认点"（弹窗批准/拒绝）未实现，见下条

### ✅ 已完成 — 敏感操作审批策略配置 + 交互式确认
- PRD 5.2.5："可选择每次询问、始终允许、敏感操作询问"
- **2026-07-31 已实现**：
  - 策略模型：`always_ask` / `always_allow` / `sensitive_only`（默认，仅敏感操作询问），全局默认 + Agent 级覆盖，超时默认 60s 视为拒绝（fail-closed）
  - 交互式确认流：AI 执行 script_run 等敏感操作 → SSE `event: ask` 挂起 → 聊天界面弹窗（操作/参数/截止时间）→ 批准执行 / 拒绝停止
  - 双轨兜底：未获交互式确认的脚本仍创建 ITSM 变更单离线审批；`always_allow` 需手动开启（仅建议非生产）
  - 配置 UI：AI 设置 →「审批策略」Tab（模式/敏感工具名单/超时）
  - API：`GET/PUT /api/v1/ai/approval-policy`、`POST /api/v1/ai/approvals/{id}/respond`、`GET /api/v1/ai/approvals/pending`
- **遗留**：Agent 级覆盖策略在工具执行链中已可读取（ctx 传递），但前端暂未提供按 Agent 单独配置的入口（后续可加）

### ✅ 已完成 — MCP 工具注册 UI
- **2026-07-31 已实现（PRD 5.2.2 全链路）**：
  - 自定义工具注册：HTTP API 工具（URL 模板 + {param} 占位符 + 请求头 + 超时）与 CLI 工具（shell 模板，带风险警示）
  - 参数 Schema 定义：名称/类型(string|number|boolean)/必填/描述，渲染给 LLM
  - 动态生效：注册/启用即时注入 registry，禁用/删除即反注册；Agent 定义处可勾选自定义工具进白名单
  - API：`POST/GET/PUT/DELETE /api/v1/ai/tools/custom`（需 `ai:tool:exec` 权限）
  - 持久化：`ai_custom_tools` 表（租户内 name 唯一），启动自动加载
- **遗留**：CLI 工具在服务端 shell 执行（风险提示已做，生产建议仅内网启用）；无工具调试/测试按钮（可在 Agent 聊天中验证）

### ✅ 已完成 — 知识库-AI 关联绑定
- PRD 5.2.6："支持关联知识、知识库"
- **2026-07-31 已实现**：
  - 绑定维度：知识库文章分类（category），Agent 的 `knowledge_categories` 字段（extra JSONB 持久化）
  - 检索生效：Agent 聊天时 system prompt RAG 注入 + `knowledge_search` 工具均按绑定分类过滤；留空 = 检索全部（兼容旧行为）
  - UI：AI 设置 → 智能体 Tab，创建/编辑 Agent 时勾选绑定分类；Agent 卡片显示 📚 绑定 badge
  - 团队模式：系统注入检索全部，成员工具链内按各自 Agent 绑定过滤
- **遗留**：知识库分类无元数据管理接口（前后端均为自由字符串/硬编码），可后续补 category 管理 API

### ✅ 已完成 — 思维链（CoT）改造：模式 → 全局开关 + 流式
- **2026-07-31 已实现**（用户提出设计修正：CoT 是展示维度，不应是独立模式）：
  - 后端：SSE 新增 `event: thinking` 命名事件，与回答 token 分流（`StreamChunk` 类型）；`/ai/chat` 请求体 `reasoning` 参数在流式模式下生效
  - 前端：去掉「思维链」模式 Tab；新增「💭 思考过程」全局开关（localStorage 持久化），普通/自主模式均生效；thinking 以可折叠 `<details>` 展示
  - 已验证：go build / vet 通过，TS 无新增错误

### 🟢 P2 — 聊天体验优化
- 缺少"停止生成"按钮（流式中断 AbortController）
- 普通模式 Markdown 仍用正则渲染（自主模式已做结构化卡片，普通模式建议升级 react-markdown + rehype-highlight）
- 缺少文件上传/图片粘贴
- 缺少对话导出/分享链接
- 缺少 Prompt 模板库（仅 4 个快捷操作）

---

## 二、前端页面补齐项

### ⏸️ 暂缓 — CMDB 相关页面（整体待完善，非单页问题）
> 决策（2026-07-31）：CMDB 数据模型/关系/自动发现机制整体不完善，单补展示层页面治标不治本。以下项**暂缓**，待 CMDB 核心能力完善后随整体路线推进。

- **CMDB 影响分析页 (/cmdb/impact)**：235 行，使用硬编码 `MOCK_CI_LIST` 和 `MOCK_IMPACTS`，需对接真实拓扑关系查询
- **CMDB 变更评估页 (/cmdb/change-assess)**：207 行，使用硬编码 `MOCK_CHANGES`，需对接 ITSM 变更 + CMDB 关系
- **CMDB 搜索 (/cmdb/search)**：仅 62 行，PRD 要求全局搜索 + 关系路径查询 → 完全缺失

> 推进建议：后续做 CMDB 时先建「数据模型 + 关系管理 + 自动发现」的数据闭环，再统一补查询/影响分析/搜索页面，而非逐页打补丁。

### ✅ 已完成 — 监控中心合理拆分（按工作流聚类，非机械拆 Tab）
- **2026-07-31 已实现**，拆为 3 个职责清晰的页面 + 侧边栏分组：

| 页面 | 职责 | 覆盖 PRD 项 |
|------|------|------------|
| `/monitor` 监控总览 | 统计卡 + 告警实时滚动展示（自动轮播/悬停暂停/WebSocket）+ 快捷入口 | 告警中心面板（统计分析+滚动展示） |
| `/monitor/alerts` 告警中心 | 活跃/处理历史切换 + 严重度/关键词筛选 + 确认/解决 + 详情弹窗 | 告警事件处理历史轨迹（后端新增 history 记录） |
| `/monitor/targets` 监控配置 | 采集目标 + 告警规则 + 手动探测 + 指标历史 | 监控采集任务管理、服务拨测（单步骤） |

- **规划中**（不虚构，总览页明确标注）：监控采集插件库、多步骤服务拨测、第三方告警接入、告警转义、告警丰富/压缩收敛、通知升级方式 — 需后端对应能力后逐个开放

### ✅ 已完成 — 审计日志 (/audit) 完善
- **2026-07-31 已实现**：
  - 筛选：关键词（动作/目标名/Trace）、事件类型、操作主体类型/ID、目标类型、结果（成功/失败）、日期范围
  - 分页：limit/offset + 总条数（COUNT），前端页码导航
  - 详情弹窗：时间/Trace/主体/目标/动作/结果/IP/UA/错误 + 请求响应 JSON 查看
  - 导出：CSV 导出（带 BOM 支持 Excel 中文）
  - 后端：`GET /api/v1/audit/events` 返回 `{items,total,limit,offset}`，支持 `q/event_type/actor_id/success/from/to` 等参数

---

## 三、推进优先级建议（已剔除今日完成项与暂缓项）

| 优先级 | 项 | 预估工作量 |
|--------|-----|-----------|
| P0-1 | 监控采集插件库 + 多步骤服务拨测 | 1.5d |
| P0-2 | 告警治理（第三方接入/转义/收敛/通知升级） | 2d |
| P1-1 | 知识库分类元数据管理（category API + 前端） | 0.5d |
| P1-2 | 审批策略：Agent 级配置入口（前端） | 0.5d |
| P2-1 | 聊天体验（停止按钮/普通模式 Markdown 升级） | 1d |
| P2-2 | 文件上传/对话导出/Prompt 模板库 | 1d |
| P2-3 | 自定义工具调试/测试（界面内试运行） | 0.5d |
| ⏸️ | CMDB 整体（数据模型/关系/发现 → 再补查询/影响分析/搜索） | 待定 |

## 四、本次变更记录（2026-07-31）

| 改动 | 文件 |
|------|------|
| CoT 模式 → 全局思考开关 + SSE `event: thinking` | `internal/ai/agentic/engine.go`、`internal/ai/service.go`、`web-next/src/stores/ai.ts`、`web-next/src/app/(dashboard)/ai/page.tsx` |
| 自主模式（explore 参数 + 规划/执行/总结 + 结构化渲染） | 同上 4 个文件 |
| 审批策略（3 模式 + SSE `ask` 交互确认 + 弹窗 + 配置 Tab） | `internal/ai/agentic/approval.go`(新)、`engine.go`、`tools.go`、`service.go`、`cmd/core/main.go`、`stores/ai.ts`、`ai/settings/page.tsx`、`ai/page.tsx` |
| 知识库-AI 绑定（Agent 分类绑定 + RAG 过滤） | `internal/ai/model/model.go`、`pgstore.go`、`engine.go`、`tools.go`、`service.go`、`cmd/core/main.go`、`ai/settings/page.tsx` |
| Agent 定义增强（per-Agent 工具白名单 + Skills 注入） | `internal/ai/model/model.go`、`pgstore.go`、`engine.go`、`service.go`、`ai/settings/page.tsx` |
| 自定义 MCP 工具注册（HTTP/CLI 工具 + 参数 Schema + 动态注册） | `internal/ai/mcp/registry.go`、`agentic/customtool.go`(新)、`pgstore.go`、`engine.go`、`service.go`、`ai/settings/page.tsx` |
| 审计日志完善（筛选/分页/详情/CSV 导出） | `internal/audit/model/model.go`、`store/store.go`、`store/pg.go`、`service.go`、`web-next/src/stores/audit.ts`、`audit/page.tsx` |
| 监控中心拆分（总览/告警中心/监控配置 + 告警历史） | `internal/monitor/alert/aggregator.go`、`monitor/service.go`、`web-next/src/stores/monitor.ts`、`monitor/page.tsx`、`monitor/alerts/page.tsx`(新)、`monitor/targets/page.tsx`(新)、`layout.tsx` |
| 全局 AI 悬浮助手（任意页面呼出：普通/自主双模式 + 审批确认条 + localStorage 会话） | `web-next/src/components/ai-assistant.tsx`(新)、`lib/ai-render.ts`(新)、`stores/ai.ts`、`components/layout.tsx`、`ai/page.tsx` |
| PRD 5.2.4 solo → 自主模式 | `PRD.md` |
