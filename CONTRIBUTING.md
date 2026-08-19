# 参与贡献（Contributing Guide）

欢迎参与 OpsOrigin 的开发！无论你是修 bug、加功能、写文档还是提建议，都请先阅读本指南。

## 代码规范

### 后端（Go）

- 使用 `gofmt` / `goimports` 统一格式
- 提交前通过：`go build ./...`、`go vet ./...`
- 新增/修改逻辑必须补单元测试（`*_test.go`）
- 禁裸 SQL 拼接：数据库访问统一走 store 层 + 参数化查询
- 错误处理：不吞错误（不用 `_ = err` 丢弃可恢复错误），HTTP 层统一用 `pkg/httputil` 响应

### 前端（Next.js / TypeScript）

- TypeScript 严格模式，避免 `any`（业务数据定义接口类型）
- 组件化：单文件超过 400 行请拆分子组件
- API 调用统一走 `src/lib/utils.ts` 的 `apiFetch`，不散落 fetch
- 提交前通过：`npm run build`（含 `next build` + tsc）

### 提交信息

- 格式：`<type>(<scope>): <subject>`
- type：`feat` / `fix` / `refactor` / `docs` / `test` / `chore` / `perf`
- scope：`ai` / `itsm` / `cmdb` / `monitor` / `agent` / `core` / `web` 等
- 示例：`fix(itsm): 修复 SLA 升级转派不触发通知的问题`

## 开发流程

1. Fork 本仓库并 clone 到本地
2. 新建分支：`git checkout -b feat/your-feature`
3. 本地起依赖：`docker compose -f deploy/docker-compose.yml up -d postgres redis`（可选 dgraph）
4. 后端：`go run ./cmd/core`；前端：`cd web-next && npm run dev`
5. 完成开发后自测（见下），推送分支并提交 Pull Request
6. PR 需通过 CI（lint / build / test / integration）方可合并

## 测试

```bash
# 后端单元测试
go test ./...

# 集成测试（需要 postgres/redis/dgraph，未起则自动跳过）
make test-integration

# 前端
cd web-next
npm test
npm run build
```

## 分支与版本

- `main`：稳定分支，只接受已通过 CI 的合入
- `dev`：开发分支，功能合入的默认目标
- 发版采用语义化版本（SemVer）

## 问题报告

- 提 issue 时请包含：环境信息、复现步骤、期望行为、实际行为、相关日志
- 安全漏洞请**不要**公开提交 issue，按 [SECURITY.md](SECURITY.md) 的流程报告

## 行为准则

参与本项目即表示同意遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。
