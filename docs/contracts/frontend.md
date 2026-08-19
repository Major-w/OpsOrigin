# 前端开发契约（Frontend Contract）

> 适用范围：`web-next/src/**`。依据 `docs/frontend-review-checklist.md` 五支柱扩展为可执行契约。
> 校验：`make contract` 自动化 + 本文件逐条人工自查。

## F1 用户旅程闭环

- **F1.1 导航单一数据源**：新增/修改页面入口只能在 `components/layout.tsx` 的导航树中定义，禁止在页面内散落入口链接。校验：grep 侧边栏外是否出现 `/xxx` 路由硬编码。
- **F1.2 模块归属明确**：每个页面必须归属一个能力簇（建模/纳管/运营/查询/治理/度量），新页面需在 `docs/api-routes.md` 同步登记。
- **F1.3 跨页状态延续**：列表/筛选/详情定位类状态必须用 URL query 持久化（如 `?q=` `?type=` `?ci=`），禁止仅存内存（刷新即丢、无法深链）。参照 `cmdb/instances` 的 `syncUrl` + `useState(() => searchParams.get(...))` 模式。

## F2 操作逻辑闭环

- **F2.1 危险操作二次确认**：删除/合并/批量删除/批量覆盖等**破坏性操作必须用 `components/ui/confirm-dialog.tsx`**，禁止原生 `confirm()`。校验：`grep -r "confirm(" web-next/src` 必须为 0。
- **F2.2 提交防重**：所有写操作（提交/保存/创建/批量）必须带 `submitting` 锁态：按钮 `disabled={... || submitting}` + 进行中文案 `…`，入口处 `if (submitting) return`。校验：人工 + grep 抽查。
- **F2.3 反馈三件套**：写操作完成后必须「toast 结果 + 数据刷新（loadData/fetch）+ 状态迁移（关 Dialog/清选中）」，三者缺一不可。
- **F2.4 权限门控**：页级读视图用 `useCan('xxx:read')` + 无权限态 UI；破坏性按钮额外 `useCan('xxx:write'|'delete'|'admin')`。权限 key 必须与 `docs/contracts/api.md` 权限点注册表一致。校验：grep 页面是否 import `useCan`。

## F3 状态完备

- **F3.1 四态齐备**：每个数据视图必须有 loading / success / empty / error 四态；empty 态需「说明 + 主操作入口」（如「暂无实例 + 新建配置项」），禁止裸文本。
- **F3.2 错误态重试**：数据加载失败必须用 `components/ui/error-state.tsx`（含重试按钮），禁止只 toast 无恢复路径。
- **F3.3 错误边界**：每个一级模块（如 `(dashboard)/cmdb`）必须提供 `error.tsx`，防止运行时崩溃白屏（重试 + 返回模块首页）。
- **F3.4 加载态**：首屏/翻页加载需 loading 指示（Skeleton/spinner/「加载中…」）。

## F4 布局层级

- **F4.1 一屏一主任务**：页面主操作应聚焦（单一高亮主按钮），禁止多任务并列指挥台式布局；复杂操作进 Dialog/Tab。
- **F4.2 卡片化**：列表/统计/治理内容统一 `components/ui` 的 Card/Dialog/Tabs，禁止自造容器样式。
- **F4.3 栅格与响应式**：间距遵循 Tailwind 4px 步进；表格超宽必须横向滚动容器，Dialog 不得超屏。

## F5 色彩语义

- **F5.1 变量驱动**：颜色只能引用 `globals.css` 的 CSS 变量（`--primary/--destructive/--warning/--muted/--accent/...`），**禁止在组件内硬编码 hex**（含 `bg-[#...]`、`text-[#...]`、`style={{color:'#...'}}`）。校验：grep `#[0-9a-fA-F]{6}` 于 `web-next/src`（白名单：`globals.css` 变量定义、`lib/graph-colors.ts`）。
- **F5.2 语义色受控**：语义色 ≤5（主/危险/告警/次要/强调），红=危险、琥珀=告警、主色=健康/正常，符合运维语义。
- **F5.3 图表色 token 化**：拓扑/全景等图形颜色一律集中到 `lib/graph-colors.ts`（`RELATION_COLORS`/`HEALTH_COLOR`/`GRAPH_DEFAULT_COLOR`），禁止页面内散落图形色字面量。

## F6 复用与类型

- **F6.1 表单引擎复用**：CI/模型等结构化编辑表单必须接入 ITSM `DynamicForm`/`FormBuilder` 引擎，禁止手写重复 JSON 表单（历史遗留项须在重构时消除）。
- **F6.2 组件抽取**：同构 UI（确认框/错误态/空态/加载态）必须复用 `components/ui` 公共组件，禁止复制粘贴实现。
- **F6.3 类型契约**：API 返回必须定义 `interface`（与后端字段对齐），禁止裸 `any` 传递响应；后端字段变更需同步更新前端 interface（见 `api.md` C4）。

## F7 校验清单（提交前自查）

```bash
grep -rn "confirm(" web-next/src                    # 必须 0 命中
grep -rnE "bg-\[#|text-\[#|border-\[#" web-next/src # 必须 0 命中（SVG 图元必要除外）
npx tsc --noEmit                                    # 0 error（在 web-next 下）
```
完整校验：`make contract`。
