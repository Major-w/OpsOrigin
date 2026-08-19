# 安全策略（Security Policy）

## 支持的版本

| 版本 | 支持状态 |
|---|---|
| main (dev) | ✅ 积极开发，接受安全修复 |
| 最新 release | ✅ 接受安全修复 |
| 旧 release | ⚠️ 视严重程度评估 |

## 报告漏洞

**请不要公开提交安全漏洞 issue。** 请通过以下渠道私密报告：

- 邮箱：<opsorigin.maintainers@gmail.com>
- 主题请注明 `[SECURITY]` 前缀

请在报告中包含：

- 漏洞类型（如 XSS / SQL 注入 / 越权 / 信息泄露等）
- 受影响的版本
- 复现步骤（最小化）
- 影响评估
- 可选：修复建议

我们承诺：

- 24 小时内确认收到报告
- 5 个工作日内给出初步评估与修复计划
- 修复发布前不会公开漏洞细节

## 部署安全清单

生产部署请务必确认：

- [ ] `OPSORIGIN_ENV=production`（强制凭据强度校验，弱密钥直接拒绝启动）
- [ ] `OPSORIGIN_JWT_SECRET` 与 `OPSORIGIN_TUNNEL_AUTH_SECRET` 替换为随机强值
- [ ] 兜底管理员凭据通过 `OPSORIGIN_DEFAULT_ADMIN_USERNAME/PASSWORD` 修改
- [ ] 前置 HTTPS 反向代理（推荐 Nginx / Caddy）
- [ ] PostgreSQL / Redis / MinIO 不直接暴露公网
- [ ] 定期备份（见 `docs/deployment-guide.md` §8）
