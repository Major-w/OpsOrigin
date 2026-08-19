# Changelog

All notable changes to **OpsOrigin** are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

> 项目当前处于开源筹备阶段（未发布正式版本），以下记录归入 `Unreleased`。
> 更完整的历史以 `git log` 为准。

## [Unreleased]

### Added
- **开源许可证与社区文档**：新增 `LICENSE`（GPL-3.0）、`CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`SECURITY.md`，仓库具备开源就绪基础。
- **CHANGELOG**：本文件，记录版本演进与安全/质量状态。
- **安全审计闭环**：引入 `govulncheck` 与 `npm audit` 作为开源发布前的依赖体检手段。

### Changed
- **默认管理员凭据 env 化**：移除 `internal/user/service.go` 中硬编码的 `admin/admin123` bcrypt 哈希，改为读取 `OPSORIGIN_DEFAULT_ADMIN_USERNAME` / `OPSORIGIN_DEFAULT_ADMIN_PASSWORD`（默认仍为 `admin/admin123`，但可在部署时覆盖）。
- **Next.js 升级**：`web-next` 由 `14.2.5` 升级至 `14.2.35`（14.2.x 最新安全补丁版）。
- **Go 工具链升级**：`go.mod` / `go.work` 由 `1.25.5` 提升至 `1.26.6`，清零 5 个标准库 CVE（`net/url`、`crypto/tls`、`encoding/xml`、`encoding/asn1`、`net/http`）。
- **关键依赖安全升级**：
  - `golang.org/x/image` `0.41.0` → `0.43.0`（修复 webp 解码 panic）。
  - `google.golang.org/grpc` `1.75.1` → `1.82.1`（修复 gRPC 鉴权绕过 CVE）。
- **设计文档退出版本跟踪**：`.docs/` 从 git 跟踪中移除（保留于本地，不进仓库）。

### Fixed
- **`pkg/auth` 测试回归**：修复因单租户模式（`TenantIDFromContext` 强制返回 `default`）导致的 2 个测试失败：
  - `TestLegacyContextHelpersStillWork`（旧 `*FromContext` 兼容入口取值断言）。
  - `TestAutoPermissionMiddleware_Resolver/unknown_tenant_falls_back`（未知租户回落写死映射路径）。
  - 两个用例现显式在**多租户模式**下运行以验证多租户语义。

### Security
- **`govulncheck`**：代码路径 **0 漏洞**（5 个标准库 CVE 已随 Go 1.26.6 清零；`x/image`、`grpc` 已升级）。仍发现 2 个「导入但未调用」、3 个「依赖但代码未触达」的传递依赖告警，不影响运行时。
- **`npm audit`**：修复 `nanoid` 高危项（`3.3.16` → `3.3.18`）。剩余 `next@14.2.x` 与传递依赖 `postcss` 的多个高危公告**仅能通过升级至 Next.js 16（破坏性变更）彻底修复**，列为后续里程碑（见下方「已知风险」）。

### Known Risks / 已知风险
- **Next.js 14.2.x 高危公告**：`npm audit` 仍报告 `next` 与 `postcss` 共 2 项高危（DoS / 缓存投毒 / SSRF / XSS 等，多数为特定配置才可利用：Image Optimizer `remotePatterns`、自定义服务器、i18n 中间件、Server Actions）。彻底修复需 `next@16`，属破坏性升级，计划在 P3/P4 评估 Next 15 LTS 或 16 迁移。当前为自托管内部运维系统，相关攻击面（公网 Image Optimizer、公网 rewrites、i18n 中间件）默认不启用，风险可控。
