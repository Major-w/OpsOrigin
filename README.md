# OpsOrigin

> 下一代 AI 原生全自动运维控制面（AIOps Control Plane）

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Vite](https://img.shields.io/badge/Vite-6-646CFF?logo=vite&logoColor=white)](https://vitejs.dev)
[![License](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)
[![CI](https://img.shields.io/github/actions/workflow/status/Major-w/OpsOrigin/ci.yml?branch=dev&label=CI)](https://github.com/Major-w/OpsOrigin/actions)

OpsOrigin 是面向企业 IT 运维的 **AI 原生控制面底座**：以 Go 高性能后端 + React 前端为核心，深度集成大模型 AI Agent 与人在回路（HITL）控制，把 CMDB、ITSM、监控、AI 运维能力统一到一张控制面上。

## ✨ 核心特性

- **🤖 AI Agent 原生**：MCP 工具集 + 模型池路由 + 限流熔断 + 执行计划编排；对话可生成架构图（mermaid）、自动沉淀知识/技能、长期记忆与反馈回流闭环
- **🧩 多模 CMDB**：PostgreSQL + Dgraph 异构多模，动态发现（Agent/Agentless）+ 采集器插件化 + 拓扑环路检测
- **🔄 ITSM 工单引擎**：8 态状态机 + SLA/OLA + 会签/并行/子流程 + 流程驱动自动化执行，AI 变更强制走审批（HITL）
- **📊 统一可观测**：动态发现 + 拨测 + 告警拓扑抑制 + 系统自监控（组件健康/日志查看）
- **🔐 安全默认**：无 TraceID 一票否决、全查询强制租户隔离、边缘零入向端口、Agent 无状态
- **🚀 全场景部署**：单机 docker-compose / systemd 生产 / Kubernetes 集群

## 🏗 架构

```
┌─────────────────────────────────────────────────────────┐
│                      Core (中心控制面)                     │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐          │
│  │ CMDB │ │ITSM  │ │Monitor│ │  AI  │ │Audit │          │
│  └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └──┬───┘          │
│     │        │        │        │        │               │
│  ┌──┴────────┴────────┴────────┴────────┴──┐            │
│  │         Tunnel Server (反向隧道)           │            │
│  └────────────────────┬──────────────────────┘            │
├───────────────────────┼──────────────────────────────────┤
│              Proxy (隔离区代理)                            │
│         Tunnel Client ←→ Agent Registry                  │
├───────────────────────┼──────────────────────────────────┤
│                  Agent (受控主机)                          │
└───────────────────────┴──────────────────────────────────┘
```

## 🚀 快速开始

**前提**：Go 1.25+ · Node.js 22+ · Docker（可选）

### 一键启动（推荐）

```bash
./start.sh            # 依赖(PG/Dgraph/Redis) + 后端 + 前端
./start.sh status     # 查看运行状态
./start.sh stop       # 停止所有服务
```

首次启动后访问 **http://localhost:3000**（默认 `admin / admin123`，生产环境请通过 `OPSORIGIN_DEFAULT_ADMIN_*` 修改）。

### 手动启动

```bash
cp .env.example .env
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis
go run ./cmd/core                       # 后端 :8080
cd web && npm install && npm run dev   # 前端 :3000
```

### 构建与测试

```bash
make build          # 编译所有二进制
make test           # 单元测试（秒级，无外部依赖）
make test-integration  # 集成测试（需 PG+Dgraph，未起自动跳过）
make lint           # 代码检查
```

### 生产部署

| 方式 | 位置 | 适用 |
| --- | --- | --- |
| docker-compose | `deploy/docker-compose.yml` + `start.sh` | 单机快速验证 |
| systemd | `deploy/systemd/*.service` | 单机生产（自愈 + 开机自启） |
| Kubernetes | `deploy/k8s/` | 集群生产 |

完整部署、持久化与备份说明见 [docs/deployment-guide.md](docs/deployment-guide.md)。生产必须设 `OPSORIGIN_ENV=production`（弱密钥拒绝启动）。

### API 文档

- 路由清单（509+ 端点）：`docs/api-routes.md`（由 `make routes` 自动生成）
- Swagger UI：运行后访问 `http://localhost:8080/swagger/`

## 📦 模块

| 模块 | 说明 | 状态 |
|------|------|------|
| CMDB | 资产配置中心，PG + Dgraph 异构多模，动态发现 + 采集插件化 | ✅ |
| Monitor | 可观测性，动态发现 + 拨测 + eBPF + 告警收敛/拓扑抑制 | ✅ |
| ITSM | 工单引擎，8 态状态机 + SLA/OLA + 会签/并行子流程 + HITL | ✅ |
| AI Agent | MCP 工具箱 + 模型池 + 长期记忆 + 技能演化 + 主动知识进化 | ✅ |
| Audit | TraceID 全链路 + 双主体归因 + 租户隔离 | ✅ |
| Tunnel | gRPC 反向隧道，Core↔Proxy↔Agent 三层，边缘零入向端口 | ✅ |

## 🛡 安全断言（核心红线）

- AI 变更必须经过 ITSM 审批；60s/10 次限流 + 3 连败熔断
- 边缘零入向端口；Agent 完全无状态
- 无 TraceID 一票否决；所有查询强制 tenant_id 注入
- 严禁显式双写；拓扑环路检测阻断；告警必须经拓扑抑制

## 🗺 路线图

- [x] P1：多模 CMDB + 动态发现 + ITSM 工单引擎
- [x] P2：AI Agent 工具箱 + 对话隔离/共享 + 长期记忆闭环
- [x] P3：主动知识进化 + 系统自监控 + 多租户开关
- [ ] P4：ChatOps 多渠道接入（Slack / 飞书 / 企业微信）
- [ ] P5：变更风险预测 / 容量预测 / 异常检测算子化

## 🤝 参与贡献

欢迎 Issue 与 PR！请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md)（代码规范/提交流程/测试要求），并遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。发现安全漏洞请按 [SECURITY.md](SECURITY.md) 私密报告。

## 📄 许可

[GNU General Public License v3.0](LICENSE) © 2026 Major-w & OpsOrigin Contributors
