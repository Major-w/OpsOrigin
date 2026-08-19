# OpsOrigin 开发契约（Dev Contracts）

> **本目录是项目开发行为的唯一约束来源**。任何开发动作（新功能、重构、修 bug、加依赖、改路由）都必须先读本契约、完成后跑契约校验。
>
> 契约是「沉淀 + 强制」的：既定的五支柱前端规范、后端安全红线、提交规范等在此固化，并配套自动化校验器 `scripts/contract-check.sh`（`make contract`），把「人靠记忆遵守」升级为「机器强制校验」。

## 一、契约构成

| 文档 | 约束对象 | 核心内容 |
|---|---|---|
| [`frontend.md`](frontend.md) | `web-next/src` | 五支柱 UI 规范 + 本次整改沉淀的硬规则（ConfirmDialog 统一、提交防重、四态完备、CSS 变量、URL 状态延续、表单复用） |
| [`backend.md`](backend.md) | `internal/` `pkg/` `cmd/` | Go 侧契约：统一响应、context 透传、权限点注册、SQL 安全红线、分层与依赖方向、共享基础设施红线 |
| [`api.md`](api.md) | 前后端交界 | REST 契约：路径/方法、错误结构、分页、权限点注册表、TS 类型同步 |
| [`workflow.md`](workflow.md) | 一切开发动作 | 开发前/中/后流程：读契约 → 按契约实现 → `make contract` 校验 → 提交规范 → 推送验证 |

## 二、遵守方式（三层强制）

1. **开发前必读**：开始任何任务前，通读 `README.md` 与本任务涉及域的契约文件（前端任务读 `frontend.md`+`api.md`，后端任务读 `backend.md`+`api.md`）。
2. **开发后必检**：完成改动后必须运行 `make contract`（或 `bash scripts/contract-check.sh`），校验失败不得视为完成。
3. **提交前必扫**：`workflow.md` 中的提交红线（竞品字样、品牌字样、Co-Authored-By、`.workbuddy/`/`outputs/` 不入库）由契约脚本首项强制扫描。

## 三、契约演进

- 契约不是冻结的：新发现的高频返工点、踩过的坑、评审清单里反复出现的问题，应**提炼成新条款**写回对应契约文件，并同步到 `scripts/contract-check.sh`（能自动化的必须自动化）。
- 修改契约 = 修改「开发行为基准」，需在 commit message 中注明 `contract:` 前缀。
- 契约文件用**短条款 + 校验方式**组织，每条可执行、可验证，避免大而全的论文式描述。

## 四、快速入口

```bash
make contract   # 一键契约校验（等价 bash scripts/contract-check.sh）
```

> 相关历史：`docs/frontend-review-checklist.md` 为五支柱评审清单（本契约 `frontend.md` 是其可执行化升级版）。
