# 开发动作流程契约（Workflow Contract）

> 适用范围：**一切开发动作**——新功能、重构、修 bug、依赖升级、文档调整、模型/权限变更。
> 本文件定义「做什么之前」与「做完之后」的强制流程，配合 `scripts/contract-check.sh` 落地。

## W1 开发前（必读）

- **W1.1** 通读 `docs/contracts/README.md`，按任务域加载对应契约：
  - 前端改动 → `frontend.md` + `api.md`
  - 后端改动 → `backend.md` + `api.md`
  - 新增/变更接口 → `api.md` + `docs/api-routes.md`
- **W1.2** 涉及资源纳管/采集/监控/报表/云接入等既有架构域时，先读 `.docs/` 对应设计文档，确认符合既有架构事实（见项目长期记忆摘要）。

## W2 开发中（按契约实现）

- **W2.1** 前端：五支柱逐条对照（`frontend.md` F1–F6）；后端：统一响应/context/权限点/SQL 红线逐条对照（`backend.md` B1–B7）。
- **W2.2** 新增权限点、路由、依赖、模型字段等跨端变更，同步更新契约注册表与文档，禁止"代码先上、文档后补"。
- **W2.3** 不引入竞品（EasyOps/easyops/优维）相关代码、注释、命名；不引入平台品牌字样（WorkBuddy/CodeBuddy）。

## W3 开发后（必检，make contract）

```bash
make contract
```
校验项（`scripts/contract-check.sh`）：
1. 竞品/品牌字样扫描（git grep，全仓）——**FAIL 级**
2. 前端 `tsc --noEmit`——**FAIL 级**
3. 前端 `web/` Vitest——**FAIL 级**
4. 后端 `gofmt -l` + `go vet ./...`——**FAIL 级**
5. 前端原生 `confirm(` 残留——**FAIL 级**（必须 0）
6. 前端硬编码色（`bg-[#`/`text-[#`/`border-[#`）——**WARN 级**（SVG 图元必要除外）
7. 后端裸 SQL / `context.Background()`——**WARN 级**（命中须人工确认在白名单场景）

> 校验未通过 = 任务未完成。WARN 项需在提交说明中注明理由。

## W4 提交规范

- **W4.1** commit message 用中文，`type(scope): 摘要` 格式（如 `feat(cmdb):`、`fix(itsm):`、`chore:`、`contract:`）。
- **W4.2** 禁止：`Co-Authored-By` trailer、平台品牌字样、竞品字样。
- **W4.3** 禁止提交：`.workbuddy/`（记忆/元数据）、`outputs/` 新增文档（分析报告）、临时构建产物。这些由 `.gitignore` 覆盖，若出现必须 `git reset` 后重新 add。
- **W4.4** 一次提交一个逻辑变更；大批量可拆多个 commit（如安全审计 + 功能整改分提）。

## W5 推送验证

- **W5.1** 推送前 `unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy`（GitHub 推送需无代理）。
- **W5.2** 推送后**必须** `git fetch origin && git log origin/dev..dev --oneline` 验证为空（输出 "Everything up-to-date" 不可信），或 `git rev-parse origin/dev` 与本地 HEAD 一致才算成功。
- **W5.3** 推送到远程前需用户明确指令（"推送"/"push"），不得擅自 push。

## W6 契约更新

- 新发现的高频返工、评审反复出现的问题、踩过的坑 → 提炼为契约条款，能自动化的必须同步进 `scripts/contract-check.sh`。
- 契约变更 commit message 带 `contract:` 前缀。

## W7 存量欠账清单（契约建立时点，逐步消化）

契约于 2026-08-19 建立。`make contract` 暴露的存量违规登记于此，**消化完一项即从清单移除**，对应校验项转绿：

| 欠账 | 数量 | 分布 | 消化方式 | 状态 |
|---|---|---|---|---|
| 原生 `confirm(` 残留（F2.1 违规） | 0 | — | 统一替换为 `ConfirmDialog`（参照 `cmdb/instances` 模式） | **已完成** |
| 前端硬编码色（F5.1 违规） | 31+ | button(6) / badge(2) / card(1) / layout(7+) / pure-mode(3) / login(3) / guide(5) / ai(2+) / message-bell(1) / ai-render(2) | 主要为暗色主题 override（`#0B0F19` 背景、`#94A3B8` 次要文字、`#C7D2E0` 辅助文字）→ 收敛到 `globals.css` CSS 变量；2.5D 图形渲染色豁免 | 待收敛 |
| 后端裸 SQL（B4） | 8 处 | `internal/vuln/store/pg.go` | 已确认全部使用 `$N` 占位符 + 租户条件强制注入，**符合红线**，仅需人工复核时抽查 | 已确认合规 |
| `context.Background()`（B2.1） | 25+ 生产 / 7 测试+生成 | agentmgr(5) / monitor(2) / deploy(4) / vuln(1) / keystore/notification/oncall 初始化 / cmd/core 启动 / ent 生成 | 生产代码中多数为后台 goroutine/定时任务/启动初始化，**符合豁免**（无请求上下文场景）；agentmgr 后台协程建议后续传入 lifecycle context | 已确认豁免 |

> 消化进度会反映在 `make contract` 输出中；WARN 级项（SQL/context/硬编码色）不阻塞提交，但须在提交说明注明理由。
