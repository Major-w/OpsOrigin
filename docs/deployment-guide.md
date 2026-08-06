# OpsOrigin 部署手册

> 本文档为部署占位骨架，覆盖从依赖准备到升级回滚的标准流程。具体命令与镜像版本以发版说明为准。

## 1. 环境要求

- Linux 服务器（x86_64 / arm64）
- Docker 20.10+ 与 docker-compose v2（单机）/ Kubernetes 1.24+（生产）
- 外网或内网镜像仓库可达

## 2. 依赖服务

| 服务 | 用途 | 默认端口 |
| --- | --- | --- |
| PostgreSQL | 关系存储（第一事实源） | 5432 |
| Dgraph Zero / Alpha | 图存储（读拓扑） | 5080 / 8080 |
| Redis（可选） | 令牌桶 / 缓存 | 6379 |

## 3. 构建

- 后端：`go build ./cmd/core`（按需构建 proxy / agent）
- 前端：`web-next` 下 `npm install && npm run build`

## 4. 部署方式

- **docker-compose**：`docker compose -f deploy/docker-compose.yml up -d`
- **Kubernetes**：`kubectl apply -f deploy/k8s/`

## 5. 配置项说明

- 数据库连接串、租户种子、AI 模型接入密钥、通知渠道（邮件 / IM）。
- 全局默认策略：采集开关与默认频率（系统设置内配置）。

## 6. 升级与回滚

- 升级：拉取新镜像 → 执行迁移 SQL → 滚动重启。
- 回滚：切回上一镜像标签，必要时回退迁移（需评估数据兼容性）。

## 7. 常见问题（FAQ）

- 待补充：隧道断连排查、Dgraph 同步延迟、迁移失败恢复。
