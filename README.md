# OpsOrigin

> AI 原生一体化运维平台（AIOps）

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Vite](https://img.shields.io/badge/Vite-6-646CFF?logo=vite&logoColor=white)](https://vitejs.dev)
[![License](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

OpsOrigin 是一个 **AI 一体化运维平台**：CMDB、ITSM、监控告警、自动化运维、应用发布和 AI 助手不再各自为战，而是集成在同一个平台里——数据互通、流程串联。AI Agent 能查资产、看告警、起草变更方案，但每一次真实操作都必须经过人工审批（HITL）才会执行。Go 后端 + React 前端，docker-compose 一键启动。

## ✨ 核心特性

- **🤖 AI Agent 原生**：MCP 工具集 + 模型池路由 + 限流熔断 + 执行计划编排；对话可生成架构图（mermaid）、自动沉淀知识/技能、长期记忆与反馈回流闭环
- **🧩 多模 CMDB**：PostgreSQL + Dgraph 异构多模，动态发现（Agent/Agentless）+ 采集器插件化 + 拓扑环路检测
- **🔄 ITSM 工单引擎**：8 态状态机 + SLA/OLA + 会签/并行/子流程 + 流程驱动自动化执行，AI 变更强制走审批（HITL）
- **📊 统一可观测**：动态发现 + 拨测 + 告警拓扑抑制 + 系统自监控（组件健康/日志查看）
- **🔐 安全默认**：无 TraceID 一票否决、全查询强制租户隔离、边缘零入向端口、Agent 无状态
- **🚀 全场景部署**：单机 docker-compose / systemd 生产 / Kubernetes 集群

## 🏗 架构

```
┌────────────────────────────────────────────────────────────────┐
│                      Core (统一核心服务)                       │
│  ┌────┐ ┌────┐ ┌───────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌───────┐  │
│  │CMDB│ │ITSM│ │Monitor│ │ Auto │ │Deploy│ │  AI  │ │ Audit │  │
│  └─┬──┘ └─┬──┘ └───┬───┘ └──┬───┘ └──┬───┘ └──┬───┘ └───┬───┘  │
│    │      │        │        │        │        │         │      │
│  ┌─┴──────┴────────┴────────┴────────┴────────┴─────────┴───┐  │
│  │                  Tunnel Server (反向隧道)                  │  │
│  └────────────────────────────┼─────────────────────────────┘  │
├───────────────────────────────┼────────────────────────────────┤
│                       Proxy (隔离区代理)                       │
│                Tunnel Client <-> Agent Registry                │
├───────────────────────────────┼────────────────────────────────┤
│                        Agent (受控主机)                        │
└───────────────────────────────┴────────────────────────────────┘
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
docker compose -f deploy/docker-compose.yml up -d postgres dgraph-zero dgraph-alpha redis minio
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
| Automation | 自动化运维，工作流引擎（脚本/审批/并行/子流/循环/Webhook 11 类步骤）+ 脚本库（AI 辅助编写）+ 定时调度 + 巡检/补丁/软件分发/漏洞扫描 | ✅ |
| Deploy | 应用发布，应用/发布单/环境/历史全流程，主机（Agent 下发脚本）与容器（K8s set image / apply）双部署模式 | ✅ |
| AI Agent | MCP 工具箱 + 模型池 + 长期记忆 + 技能演化 + 主动知识进化 | ✅ |
| Audit | 请求级 TraceID 贯穿审计与隧道协议 + 操作主体归因 + 租户隔离 | ✅ |
| Tunnel | gRPC 反向隧道，Core↔Proxy↔Agent 三层，边缘零入向端口 | ✅ |

## 🗺 路线图

### 平台底座
- [x] 多模 CMDB（PG + Dgraph 异构）+ 动态发现（Agent / Agentless）+ 采集器插件化
- [x] host 统一资源模型 —— 收敛 VM 与裸金属，支持 Agent / Agentless 双接入
- [ ] CMDB 治理闭环 —— 实例关系按需过滤、拓扑轻量部署下沉、资源治理联动

### 流程引擎
- [x] ITSM 8 态工单状态机 + SLA/OLA + 会签/并行/子流程 + 流程驱动自动化
- [x] 表单设计器（拖拽 + 一键布局）+ 服务目录上下线联动

### AI 主航道
- [x] AI Agent 工具箱（MCP）+ 对话隔离/共享 + 长期记忆闭环
- [x] 主动知识进化 + 知识增量索引自愈
- [x] AI 模型注册表 —— 模型池 / auto 模式路由（权重+健康+类型）/ 类型标签 / 可见性
- [x] AI SRE 能力 —— 验证闭环 / Runbook / 自治分级 / RCA 基准 / 复盘
- [ ] P4：ChatOps 多渠道接入（飞书 / 企业微信优先，Slack 按需）
- [ ] P6：Agent 自治分级扩展 —— 高危场景从"建议"逐步开放到"受控执行"（HITL 审批始终兜底）
- [ ] P7：AI 效果评估体系 —— 评测集 + 回归基准 + RCA/复盘质量看板
- [ ] P8：模型路由智能化 —— auto 模式权重与健康度自学习、成本感知调度
- [ ] P9：技能编排增强 —— 技能市场 / 多 Agent 协作

### 可观测与稳定性
- [x] 统一监控：动态发现 + 拨测 + 告警收敛与拓扑抑制 + 系统自监控
- [ ] 日志注入 TraceID —— 打通请求 → 日志 → 审计的全链路串联
- [ ] P5：变更风险预测 / 容量预测 / 异常检测算子化

## 🤝 参与贡献

欢迎 Issue 与 PR！请先阅读 [CONTRIBUTING.md](CONTRIBUTING.md)（代码规范/提交流程/测试要求），并遵守 [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。发现安全漏洞请按 [SECURITY.md](SECURITY.md) 私密报告。

## 📄 许可

[GNU General Public License v3.0](LICENSE) © 2026 Major-w & OpsOrigin Contributors
