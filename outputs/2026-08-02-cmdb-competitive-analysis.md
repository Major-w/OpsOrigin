# OpsOrigin CMDB 竞品对比分析

> 分析日期：2026-08-02
> 对比基准：OpsOrigin 当前 dev 分支（internal/cmdb 3481 行，Dgraph v25.4.0 已升级）

---

## 一、结论先行

**OpsOrigin 不是一个 CMDB 产品，而是一个内置 CMDB 的一体化运维平台。** 这决定了它的竞品不是 NetBox / iTop 这类单点工具，而是蓝鲸全家桶和 ServiceNow。

三句话总结当前处境：

1. **覆盖广度已进第一梯队** —— 26 个后端模块（CMDB / ITSM / 监控 / 漏洞 / 自动化 / AI / 部署 / 知识库 / 低代码），横向能力对得上蓝鲸全家桶的骨架。
2. **数据可信度正在爬坡** —— 云资源活水入口已就位：云 / 主机 / 容器由「资源纳管」层（internal/cloud）经云厂商 API 直接拉取生成基础 CI（单一数据源，2026-08-02 落地）；CMDB 自动发现改为在已纳管 CI 之上做关系 / 拓扑推断（relation 类型已落地真实逻辑），agent / 网络两类仍模拟。CMDB 行业公认"手工维护准确率不超过 40%"，OpsOrigin 当前已具备真实数据入口，正补主机 / 网络采集。
3. **有一个别人没有的技术差异点** —— PG + Dgraph 双存储。主流 CMDB 几乎都在关系库或文档库里用递归 SQL 硬啃拓扑，OpsOrigin 用原生图库做影响分析，这在百万级 CI + 复杂关系链下是结构性优势。

---

## 二、竞品横向对比

| 维度 | OpsOrigin | 蓝鲸 bk-cmdb | ServiceNow CMDB | 优维 EasyOps | 广通优云 UYUN | OpsAny | NetBox | iTop |
|---|---|---|---|---|---|---|---|---|
| **定位** | 一体化运维平台内置 CMDB | 开源 CMDB + 蓝鲸生态 | 全球 ITSM 标杆套件 | 一体化运维平台（应用 CMDB） | 平台化运维 PaaS（CMDB 主数据） | 一体化运维平台（CMDB 称"资源平台"） | 网络资产 / IPAM 专精 | ITIL 轻量开源 |
| **技术栈** | Go + PostgreSQL + **Dgraph** | Go 微服务 + MongoDB | 自有 Now Platform | Go/Java + **自研 EasyCore 图库** + EasyTSDB | Java + 运维 PaaS 组件化 | Python + 开源生态（SaltStack/Ansible/StackStorm，内置 bk-paas） | Python/Django + PG | PHP + MySQL |
| **拓扑与影响分析** | **原生图库遍历**，环检测 | 业务拓扑树 + 关联查询 | AI 依赖图谱 | **自研图库 + 时序超融合**，动静联动 | 关系图谱 + 资源图谱 | 关系简化「从属+连接」，连接用标签（轻量，非图库） | 网络物理/逻辑链路 | 基础关系视图 |
| **自动发现** | ❌ 模拟实现 | 主机 Agent + 快照 + 事件推送 | 全栈 Discovery + 联邦 | **OneAgent 插件热拔插**全栈采集 | **SaltStack Minion** 采集配置 | 公有云 API 自动导入 + opsany-agent 局域网采集 | 网络设备发现强 | 弱，手工为主 |
| **模型自定义** | 动态 CI 类型 + 属性校验 | 全自定义 CI + 关系 | 全自定义 | 分层弹性模型 OneModel | 分层弹性模型（业务视角） | 自定义 CI 模型 + 资源仓库，内置常用对象 | 网络模型强，通用模型缺 | 固定 ITIL 模型 |
| **权限控制** | ❌ IAM 存在但 CMDB 未接 | 业务角色 + 权限中心 | 细粒度 ACL | 企业级权限体系 | 服务化权限 + 共创维护 | 统一权限平台（用户/角色/RBAC 已产品化） | 对象级权限 | 用户组 + Profile |
| **变更审批** | ❌ 直接落库 | 配合 ITSM | 内置流程引擎 | 内置 ITSM 流程 | 运维流程服务（ITSM） | 工作台工单 + 事件中心工作流触发 | 无 | 内置变更单 |
| **生态集成** | 平台内原生打通 | 蓝鲸全家桶原生 | 绑定自有 ITSM | 平台内 All-in-One + 低代码 | PaaS 应用生态 + 共创 | 平台内原生 + 对接开源工具（Salt/Ansible/Grafana/Prometheus） | 插件 + API | 零散第三方 |
| **AI 能力** | `internal/ai/agentic` 引擎已落地 | 监控 AI 探索中 | 高阶 AI 模块选购 | **Elevo AI**：Agent 操作系统 + 数字员工 | AIOps 大脑（算法沉淀） | 现打"AI 原生"标签（模型管理 + 工作流，落地早中期） | 弱 | 弱 |
| **许可** | 自有 | MIT（Tencent 开源） | 商业订阅 | 商业（私有化 / 订阅） | SaaS 免费版 + 企业私有化 | **社区版永久免费 + 企业版商业订阅** | Apache 2.0 | AGPL / 商业双授权 |
| **部署复杂度** | 中（PG + Dgraph 双存储） | 高（微服务 + MongoDB 集群） | 极高（6-12 个月实施） | 中（微服务 + 自研图库） | 中（PaaS 组件化） | 低-中（社区版开箱，同质框架） | 低 | 低 |

---

## 三、逐个竞品的可借鉴点

### 1. 蓝鲸 bk-cmdb（最值得对标）

同为 Go 技术栈、同为开源、同样服务国内运维场景，架构参考价值最高。

**它做对的三件事：**
- **资源分层抽象** —— 把 CI 分成「主机 / 进程 / 通用对象」三类，底层建原子操作层，上层建场景层。这个分层让"主机"这种高频对象有专用优化路径，而不是所有 CI 一视同仁走通用逻辑。OpsOrigin 目前是纯通用模型，规模上去后主机查询会吃亏。
- **主机数据快照** —— Agent 定期上报全量快照，而非只报增量事件。这是数据保鲜的兜底机制，避免事件丢失导致的数据漂移。
- **变更事件主动推送** —— CMDB 变更主动推给下游消费方。OpsOrigin 的 outbox 已经具备这个能力，但目前只推给 Dgraph，没有对外的订阅接口。

**它的包袱：** MongoDB 做关系查询天然吃力，业务拓扑是树而非图，跨业务的影响分析要绕。这恰好是 OpsOrigin 的图库优势区。

### 2. ServiceNow CMDB

**唯一值得抄的是「数据治理闭环」**：质检规则 → 质量看板 → 自动生成整改待办 → 跟踪修正。Gartner 说 75% 的 CMDB 部署死于数据质量，不是死于功能不够。

这套机制成本不高（一组规则引擎 + 一个看板页），但决定 CMDB 是"活数据"还是"数据坟场"。OpsOrigin 现在连数据从哪来都还没解决，谈治理为时尚早，但**模型设计时要预埋质检字段**（数据来源、最后采集时间、置信度）。

其余部分不必对标 —— 实施周期 6-12 个月、深度绑定自有 ITSM，是完全不同的商业形态。

### 3. NetBox

网络领域的"精神家园"，IPAM 做到了极致。**对 OpsOrigin 的意义不是竞争而是集成** —— 很多客户已经有 NetBox 在管网络，CMDB 应该能把 NetBox 作为数据源接进来，而不是要求客户放弃它。

值得学的是它的 **API-first 设计**：所有界面能力都有对等 API，配置即代码。OpsOrigin 的 30+ REST 接口已有基础，但缺少批量导入的界面和 Terraform Provider 这类基础设施集成入口。

### 4. iTop

**反面教材价值大于正面** —— 功能齐全、ITIL 规范、界面友好，但自动发现能力弱（业内评估自动化上限 20% 以内），最终沦为手工台账。这正是 OpsOrigin 现在的风险路径：**功能页面越做越多，但数据没有活水注入。**

### 5. 嘉为蓝鲸（商业版）

国内金融政务的实际赢家，核心壁垒是 **100+ 开箱即用采集插件 + 信创适配资质**。

要注意的是：搜索到的多数"选型对比"文章出自 canway.net（嘉为官网），数据（95% 采集覆盖率、日采集百万级）为厂商自述，不宜直接采信。但它揭示了一个真实的商业逻辑 —— **CMDB 的护城河是采集插件的数量和质量，不是功能列表的长度。**

---

### 6. 优维科技 EasyOps（最凶的国内直接竞品）

定位与 OpsOrigin **高度重叠**：一体化运维平台、内置应用 CMDB、覆盖监控（HyperInsight 超融合监控）/ 自动化 / ITSM / 链路追踪 / 低代码 / 持续交付。2015 年深圳创立（创始人王金银），服务招商银行、国信证券、国家电网等 500+ 企业。它是国内唯一在"图原生 + AI 原生 + 一体化"赛道上和 OpsOrigin **正面撞车**的厂商。

**它做对的三件事（也是 OpsOrigin 要对标的）：**
- **自研图数据库 EasyCore + 时序库 EasyTSDB「超融合」** —— 这和 OpsOrigin 的「图原生」定位**直接交锋**。优维也认定影响分析 / 动静联动必须靠图，且自己造了图库。区别：**优维是闭源自研（商业护城河），OpsOrigin 用开源 Dgraph（自主可控、可审计、可改源码）**。
- **OneAgent 单 Agent 多能力** —— 一个 EasyAgent 通过插件热拔插覆盖部署 / 监控采集 / 自动化所有产品线。这和 OpsOrigin 的 `internal/tunnel` 长连接 + `internal/agentmgr` 任务下发**思路一致**，但优维把"一个 Agent 覆盖全平台"产品化得更彻底。OpsOrigin 的 Agent 目前主要跑任务，采集器还缺。
- **OneModel / OneData 全景资源地图** —— 所有产品线下层共用同一份 IT 资源数据蓝图，统一存 EasyCMDB，让产品线互通。这正是 OpsOrigin「平台内原生联动」想做的事，优维已经做成标准架构。

**最该警惕的：AI 已经商业化包装。** 优维的 Elevo AI 是"AI Agent 操作系统 + 数字员工 + 多 Agent 协作"，已是卖点而非 demo。OpsOrigin 的 `internal/ai/agentic` 引擎技术底子不差（engine / team / tools 都落地了），但还没包装成面向客户的 AI 能力，也没和 CMDB 图数据形成**可见的产品闭环**。

**可借鉴：** 单 Agent 收敛（向 OneAgent 学）、图 + 时序超融合的动静联动（OpsOrigin 只有图，缺时序融合层）。

### 7. 广通优云 UYUN（平台化 / 中台路线，理念最契合 P0）

广通软件旗下（近 19 年沉淀），定位"平台化运维 Platform Ops"，把 CMDB 当**运维中台主数据**。

**最有价值的一句话理念：「从维护型 CMDB 走向管控型 CMDB」** —— CMDB 必须和自动化、运维即服务流程闭环，否则就是数据孤岛。这和我前面对 OpsOrigin 的判断（P0 发现引擎 + ITSM 联动）**完全一致**，等于被行业老兵背书。

**具体可抄的点：**
- **自动发现用 SaltStack** —— 优云用 Salt 的 grains / pillar 做配置采集，Minion 代理上报。这是条**低成本路线**：不一定要自研采集器，可集成成熟 Agent 框架。OpsOrigin 有自研 Agent，但采集脚本体系可参考 Salt grains 的"按需扩展采集项"思路（写一次 grains 脚本，全量 Minion 同步）。
- **分层共创模型** —— 模型分层 + 多团队共创维护（由集中维护转向分权维护），解决"CMDB 维护成本高"痛点。OpsOrigin 的模型自定义已具备，但缺"分团队维护"的协作机制（和 RBAC 打通后正好能做）。
- **SaaS 免费版获客** —— 优云有永久免费 SaaS 版引流。OpsOrigin 是开源自有，本身就是最大获客杠杆，但可考虑社区版 / 商业支持分层。

**对 OpsOrigin 的定位修正：** 广通优云证明了"管控闭环"是 CMDB 活得下去的硬标准，而 OpsOrigin 当前的 P0 路线（发现引擎 + IAM + ITSM 联动）正踩在这条线上。

---

### 8. OpsAny（开源社区版，最贴近 OpsOrigin 形态的直接竞品）

OpsAny 是和 OpsOrigin **形态最接近的开源竞品**：社区版永久免费 + 企业版商业订阅，一体化运维平台，CMDB 叫"资源平台"。底层大量复用开源生态（SaltStack / Ansible / StackStorm 做采集编排，内置腾讯 bk-paas 做开发中心，监控兼容 Grafana / Prometheus），而非全自研。

**它做对的三件事：**
- **多云纳管已经成熟** —— 公有云（阿里 / 腾讯 / 华为 / AWS / 金山）API 自动导入资产，私有云（OpenStack / VMware）快速导入。这正是 OpsOrigin「云资源由纳管层经云厂商 API 拉取」的**已验证路线**：OpsAny 用云厂商 API 直接拉，成本低、数据真。
- **关系模型做减法** —— 把关系简化为"从属"和"连接"两类，连接用标签过滤搜索。这是和 OpsOrigin 图库任意关系的**路线分歧**：OpsAny 用轻量二维关系换可维护性，OpsOrigin 用图库换灵活性与复杂影响分析。**取舍没有对错，但 OpsAny 的"关系简化"恰好反证了复杂关系模型的维护成本是行业痛点**。
- **统一权限产品化** —— 有独立的"统一权限"平台（用户 / 角色 / RBAC），且和 CMDB 打通。这恰恰是 OpsOrigin 的 P0-2 缺口（IAM 有但 CMDB 没接）。

**最该正视的：它是 OpsOrigin 在"开源自有"定位上最直接的对照。** 两者都是开源一体化平台，但 OpsAny 的社区版已经免费跑起来、云纳管和权限都产品化了，而 OpsOrigin 的 CMDB 还卡在"数据从哪来"（发现引擎模拟）和"谁能用"（IAM 未接）。**差距不在架构，在成熟度。**

**可借鉴：** 云资源 API 直拉的发现器实现（OpsOrigin `internal/cloud` adapter 已有，照此路径补齐即可）；关系模型"从属 / 连接"二分是否适合高频运维场景（可审视 OpsOrigin 当前任意关系的建模成本）。

注：OpsAny 现在也打"AI 原生运维管理平台"标签（模型管理 + 工作流），但公开资料看落地尚在早期，未形成与 CMDB 的可见闭环——和优维 Elevo 已商业化不同，暂不构成实质威胁。

---

## 四、OpsOrigin 的差异化机会

### 已有但没讲清楚的优势

**1. 图原生的影响分析（技术差异，但已非独家）**

昨天修复的 4 个 Dgraph 预存 bug 之后，拓扑查询 / 影响分析 / 环检测才第一次真正可用。这个能力在竞品里的实现方式：

- 蓝鲸：MongoDB + 业务拓扑树，跨业务关系链要多次查询拼接
- iTop：MySQL 递归查询，层级深了直接卡死
- ServiceNow：有 AI 依赖图谱，但要订阅高阶模块
- **优维 EasyOps**：自研 EasyCore 图库 + EasyTSDB 时序超融合，动静联动——已经把"图原生"商业化落地了。**修正此前判断**：我原说"图原生是最硬的技术差异点"，但优维的存在证明**图路线是行业共识而非独占优势**，这条路不再是 OpsOrigin 独有。

OpsOrigin 用 Dgraph 做多跳遍历，这是**架构级优势**，但目前只体现在两个前端页面上，没有转化为业务价值。真正的杀手场景是：**变更前自动算出爆炸半径 → 联动 ITSM 审批 → 变更后验证影响面**。而 ITSM 模块就在同一个平台里。

**2. 平台内原生联动**

CMDB → 监控（告警自动关联 CI）、CMDB → 漏洞（资产清单已打通）、CMDB → ITSM（变更影响评估）、CMDB → 自动化（执行目标选取）。竞品要么靠 API 拼（NetBox / iTop），要么要买全套（ServiceNow）。OpsOrigin 天生在一个进程里，这个集成成本优势是真实的。

**3. AI Agent 编排能力**

`internal/ai/agentic` 里已有 engine / team / tools 的完整实现。CMDB + 图关系 + AI Agent 的组合，理论上能做到"自然语言问：这台数据库挂了会影响哪些业务系统"，直接跑图遍历回答。

**修正此前判断**：原说"这是竞品目前都没有的形态"，但优维的 Elevo AI 已经把"AI Agent 操作系统 + 数字员工"商业化包装成卖点——**AI Agent 路线同样不是独家**。真正还站得住的差异是：**OpsOrigin 的 agentic 引擎与 CMDB 图数据在同一个进程内，联动零成本、零集成**；而优维的 AI 和图是两个商业模块，要打通需付费订阅。广通优云的 AIOps 大脑也还停在"算法沉淀"阶段，未与 CMDB 形成可见闭环。

### 必须补的短板（按优先级）

**P0-1 · 发现引擎真实化（架构边界已定：纳管 ≠ 发现）** —— 决定 CMDB 有没有活数据

✅ **架构边界（2026-08-02 修正）**：云 / 主机 / 容器属于「资源纳管」层（`internal/cloud`）职责，由纳管层经云厂商 API / K8s Agent 通道直接拉取生成基础 CI——**单一数据源**，不重复。CMDB 的自动发现（discovery 任务）不再重复拉取云资源（此前 `type=="cloud"` 直接调 `cloud.Collector.Collect` 的实现已移除，避免与纳管层重复采集），改为在**已纳管 CI 之上**做自动发现：
- `relation` 类型（✅ 已落地真实逻辑）：扫描每个 CI 属性中以 `_id` 结尾的外键，匹配其他 CI 的 `resource_id`（按裸 id + provider 作用域收敛），去重 + 环检测后自动生成关系 / 拓扑，直接落库。这是「纳管后 CMDB 实现自动发现」的具体落地。
- `agent`（主机深度扫描）/ `snmp`（网络设备）两类仍走 `simulateDiscovery` 模拟。

⏳ **待落地**：agent 与 snmp 两类接 `internal/tunnel` + `internal/agentmgr` 的 Agent 资产采集通道回填真实数据；裸金属主机纳管（非云主机）也需在纳管层补上。

**P0-2 · CMDB 接入 IAM 鉴权** —— 决定敢不敢给别人用

`internal/iam` 有完整 RBAC 模型，但 CMDB 的 handler 零引用。当前任何登录用户都能删任意 CI。这是上生产的硬门槛，工作量不大但不能拖。

**P1-1 · 数据来源与置信度字段**

在 CI 模型上加 `source`（manual / discovery / cloud / api）、`last_seen_at`、`confidence`。**必须在有真实数据之前加**，否则后面要洗数据。这是 ServiceNow 数据治理闭环的地基。

**P1-2 · 前端搜索走后端 API**

`handleSearchCI` 已经写好了，前端却在拉全量再内存过滤。CI 上万条就崩。这是纯粹的浪费，改起来两小时。

**P1-3 · 主机对象专用优化路径**

参考蓝鲸的资源分层。主机是所有 CMDB 里访问频率最高的对象，通用模型走一遍属性校验 + JSON 解析的开销在规模上去后会很明显。

**P2 · 变更审批流 / CI 状态机 / 批量导入界面**

这些是"成熟度"而非"可用性"问题，等前面跑通再说。

---

## 五、定位建议

**不要试图做一个更好的 CMDB —— 做一个 CMDB 能被真正消费的运维平台。**

单纯比 CMDB 功能，短期打不过蓝鲸（生态成熟）和嘉为（插件积累）。但换个赛道：

> **"图原生 + AI 原生的一体化运维平台"**

- 图原生：影响分析、根因定位、变更爆炸半径，这些天然是图问题，Dgraph 已经就位
- AI 原生：agentic 引擎已有，CMDB 图数据是最好的 AI 上下文来源
- 一体化：26 个模块在一个进程里，集成成本趋近于零

这三点叠加起来，是蓝鲸（MongoDB 架构包袱）、ServiceNow（重实施、贵）都不好复制的位置。**但必须正视优维 EasyOps——它在"图原生 + AI 原生 + 一体化"上和 OpsOrigin 是同一个赛道的正面对手，且图库自研、AI 已商业化。OpsOrigin 要在它面前立住，靠的不是"也做了图"，而是：① 开源 Dgraph 自主可控、可审计、可改（优维图库闭源）；② agentic 引擎与 CMDB 同进程零成本联动（优维需付费打通）；③ 开源自有、无厂商锁定。**；同时正视 OpsAny——它与 OpsOrigin 同为开源一体化平台（社区版永久免费），但云纳管与统一权限已产品化，证明差距在成熟度而非架构，P0 必须尽快补齐。

**但前提只有一个：CMDB 里得有真数据。** 没有数据，图是空的，AI 是瞎的，一体化是自嗨。所以 P0 就是发现引擎，没有第二选项。

---

## 六、采集架构设计（全局默认 + 纳管覆盖）

> 本节是对 P0-1「发现引擎真实化」的架构落定。它直接回应竞品分析中反复出现的行业铁律——**CMDB 的护城河是采集能力，不是功能列表**。架构目标是：资源纳管只建通道、CMDB 统一调度、采集器可插拔、支持灰度与计费差异化。

### 6.1 核心设计原则

1. **资源纳管层只负责建立通道** —— 主机装 Agent / 验证 agentless(SSH/WinRM) 连通性、云验证连通性、容器装容器 Agent。不拉取数据、不直接写 CMDB。
2. **真实数据采集统一由 CMDB 模块调度** —— CMDB 是采集任务的唯一编排者（采什么、何时采、结果归 CMDB 由它定）。
3. **采集器可插拔** —— 云模块、Agent 通道、监控模块等各自注册采集器，CMDB 仅定义采集器契约，不直接依赖具体采集实现（不硬编码 `cloud.Collector`）。

### 6.2 资源纳管与采集开关（全局默认 + 纳管覆盖）

纳管资源时，表单提供两个开关：**「开启 CMDB 采集」** 与 **「开启监控采集」**。

- **平台级全局默认策略（兜底）**：系统设置中维护两个全局默认开关
  - `default_cmdb_enabled = true`（CMDB 是平台基石，新资源默认开启基础 CMDB 采集）
  - `default_monitor_enabled = false`（监控涉及额外资源消耗与兼容性风险，默认关闭）
  - 各采集类型（cmdb / monitor）维护各自的**默认采集频率**，频率由对应模块独立设置
- **纳管界面勾选 = 覆盖全局默认**：勾选框预填全局默认值，运维人员可逐资源改写
  - 例：全局默认（CMDB 开 / 监控关）；纳管核心数据库时勾选「监控」→ 实际（CMDB 开，监控开）；纳管临时测试机时取消「CMDB」→ 实际（CMDB 关，监控关）；纳管老旧 IBM 小型机 → 仅开 CMDB、关监控以规避插件不兼容
- **为什么「单一全局开关」不够**（用户论证）：
  - *灰度与验证* —— 生产环境有遗留老系统（如 IBM 小型机），新 Agent 监控插件不兼容，全局开会导致采集报错、日志刷屏，需要能按资源组关闭。
  - *计费与资源消耗* —— 高频监控消耗 Agent CPU / 带宽，商业化后核心业务买"深度监控"、测试环境只买"基础 CMDB"，单一开关无法做精细化区分。
  - *推荐模式*＝**全局默认 + 纳管覆盖**（策略继承 + 个性化覆盖），集两者优势：全局兜底保障数据完整性，纳管覆盖应对特殊需求。

### 6.3 采集任务模型

勾选开启后，系统针对该纳管资源创建对应的**采集任务（CollectionTask）**：

```text
CollectionTask {
  ID, TenantID,
  Target,            // 指向某纳管资源（主机 / 云资源 / 容器）
  Kind,              // cmdb | monitor | …（可扩展）
  Frequency,         // 采集频率，继承对应模块默认、可逐任务覆盖
  Enabled,           // 是否启用（来自有效配置布尔）
  LastRunAt, Status  // 调度器维护
}
```

- **cmdb 类任务**由 CMDB 模块持有（定义 + 频率 + 落库）；**monitor 类任务**由监控模块持有，同理。
- 结果按 `Kind` 路由：cmdb → CI / 关系落 CMDB；monitor → 指标落监控。

### 6.4 采集器注册表与调度器

- 采用**注册表模式**取代硬编码分支：`RegisterCollector(kind, 资源类型, 采集器)`，后续新增采集类型 / 任务类型只需注册新采集器，无需改动调度核心（直接满足"后续自动发现类型及任务扩展"诉求）。
- 调度器（进程内 ticker）扫描到期且启用的采集任务，按 `(kind × 资源类型)` 查注册表执行采集，结果按 kind 路由落库。
- 采集器可插拔：云模块注册云采集器、Agent 通道注册主机 / 容器采集器、监控模块注册监控采集器；CMDB 仅定义采集器契约，不依赖具体实现。
- 依赖方向无环：cmdb 只定义 `Collector` 接口 + 注册表 + 调度器；具体采集器在 `cmd/core` 实现（core 同时持有 cmdb / cloud / agentmgr / 监控模块）。

### 6.5 Agent 配置下发与优雅启停

- 每资源最终**有效配置** = `enable_cmdb_collect` / `enable_monitor_collect` 布尔 + 频率，下发 Agent。
- Agent **定期拉取**（如每 30s 请求配置端点）+ CMDB 经已有 gRPC 隧道（`internal/tunnel` + `agentmgr`）**立即推送**更新（纳管后改勾选时）。
- Agent 比对配置变更，**仅优雅启停对应采集协程**，避免反复重启整个 Agent（用户强调的工程细节）。
- 纳管后修改勾选 → 触发"配置更新指令"推 Agent → Agent 优雅重启/停止对应采集任务。

### 6.6 落地分阶段

- **Phase 1（核心闭环，云优先）**：`PlatformPolicy` + `CollectionTask` 模型与 repo → 纳管表单预填全局默认、提交时建对应 `CollectionTask` → 采集器注册表 + 进程内调度器（ticker 扫到期任务）→ 先接 `cmdb×cloud`（cloud 模块现成，经 core 注入桥接，CMDB 不依赖 cloud 实现）→ 验证。
- **Phase 2**：`monitor` 类任务骨架 + 基础指标 → Agent 配置拉取 / 推送 + 优雅启停。
- **Phase 3**：`host` / `container` 经 Agent 通道的采集器、高级监控类型（进程 / 日志，手动开启）、以及策略设置页 / 纳管勾选 UI / 纳管后开关 UI。

### 6.6.1 Phase 1 落地状态（已代码实现并验证）

> 状态：✅ 已实现 + `go build` / `go vet` / `go test` 全绿（2026-08-02）。

**新增 / 修改文件**

| 文件 | 说明 |
| --- | --- |
| `internal/cmdb/model/collection.go` | `CollectionTask`（`IsDue` 判定到期）+ `PlatformPolicy` 单例行 |
| `internal/cmdb/pg/collection_repo.go` | `CollectionTask` / `PlatformPolicy` 的 CRUD + `scanCollectionTasks` + null 辅助 |
| `deploy/migrations/012_cmdb_collection.sql` | 建 `cmdb_collection_task` + `cmdb_platform_policy` 两表 |
| `internal/cmdb/collector.go` | `Collector` 接口（`Run`）+ `CollectionOutput` / `MetricPoint` + 注册表 |
| `internal/cmdb/scheduler.go` | 进程内 ticker 调度器：扫到期、防并发、按 Kind 路由、未注册采集器标 `pending` |
| `internal/cmdb/collection_service.go` | `OnboardResource`/`RegisterCollector`/`StartScheduler`/`ingestCollectionOutput`/`upsertCIsByResourceID`（按 `resource_id` 幂等） |
| `internal/cmdb/collection_handlers.go` | 策略 + 采集任务 CRUD + onboard + run 的 HTTP handlers |
| `cmd/core/cloud_collector.go` | `cmdb.Collector` 桥接：复用 `cloud.Collector.Collect` → `cloud.ToCIMapping` → `[]CIModel` |
| `internal/cloud/service.go` | `Service` 加 `onOnboard` 回调 + `SetOnOnboard` |
| `internal/cloud/http_credential.go` | `handleCreateCredential` 接收 `enable_cmdb_collect` / `enable_monitor_collect`，保存后触发 `onOnboard` |
| `cmd/core/main.go` | 注册 `cmdb×cloud` 采集器；`cloudSvc.SetOnOnboard(cmdbSvc.OnboardResource)`；`StartScheduler(ctx)` |

**核心数据流（已闭环）**

1. 云凭证纳管（建立通道）→ `handleCreateCredential` 触发 `onOnboard("cloud","cloud:<provider>",flags...)`；
2. `OnboardResource` 读 `PlatformPolicy`（全局默认），按「默认 + 纳管覆盖」为 `cmdb`/`monitor` 各建 `CollectionTask`（已存在则跳过）；
3. 调度器每 30s 扫启用任务，按 `(kind×resourceType)` 查注册表 → `cloudCollector.Run` 拉真实云资源 → `upsertCIsByResourceID` 按 `resource_id` 幂等 upsert；
4. `monitor` 类任务因采集器未注册，标记为 `pending`（Phase 2 接入）。

**关键设计校验**

- 依赖单向：`cmdb` 只定义 `Collector` 契约与注册表，`cloud` 实现经 `cmd/core` 注入，无 import 环；
- 幂等正确：`ToCIMapping` 已写 `attributes["resource_id"] = "<provider>:<type>:<id>"`，调度多次不重复建 CI；
- 健壮性：stub 模式（PG 不可用）下 `listAllEnabledTasks` 返回空，调度器不空指针 panic。

### 6.6.2 Phase 2 落地状态（已代码实现并验证）

**本期目标**：`monitor` 类任务骨架 + 基础指标闭环；并修正 Phase 1 遗留的「monitor 类任务一律 pending」问题。Agent 配置拉取/推送 + 优雅启停的「Agent 侧框架」经核查**已由 `agentmgr` 模块完整实现**（详见下方「重大架构发现」），本阶段不再重复造轮子，改为明确边界并打通指标落库。

**已落地的代码改动**

1. **`monitor×cloud` 采集器（server 拉骨架）**
   - 新增 `cmd/core/cloud_metric_collector.go`：实现 `cmdb.Collector`，复用 `cloud.Collector.Collect(provider)` 已产出的 `[]cloud.Metric`，转成 `[]cmdb.MetricPoint`。
   - `cmd/core/main.go` 注册 `monitor:cloud`（与 `cmdb:cloud` 共享同一 `cloud.Collector` 实例）。
   - 各云厂商 `CollectMetrics` 当前为占位（返回 nil），接入云监控 API（阿里云 CMS / 腾讯云 Barad / AWS CloudWatch / 华为云 CES）后即产出真实基础指标；当前保持「骨架通」——任务标 `success`，不造假数据。

2. **cmdb monitor 指标落库接线（补 Phase 1 缺口）**
   - `cmd/core/main.go` 在 `ingestBuf` 创建后调 `cmdbSvc.SetMetricSink(...)`，把 `cmdb.MetricPoint` 转成 `tunnel.MetricsReport` 提交给 ingest 管道（VM / 内存 sink）。
   - 至此两条 monitor 指标路径汇聚：**server 拉（cmdb 调度器 → onMetric）** 与 **Agent push（grpcTunnelServer.OnMetrics）** 都进同一 ingest 缓冲，统一落库。

3. **调度器对 agent 通道 monitor 任务的正确语义**
   - `scheduler.runOne` 对 `kind=monitor 且 resourceType≠cloud`（主机 / 容器 / Agent 自身）的任务，标记 `delegated` 而非查 collector / 刷 `pending`——因为这类任务由 Agent 本地采集并 push 上报，server 侧不主动拉取。
   - `collection_service.go` 新增 `markTaskDelegated`（`status="delegated"`，正常预期状态，区别于配置缺失的 `pending`）。

**验证**：`go build ./...` / `go vet ./internal/cmdb/... ./cmd/core/...` / `go test ./internal/cmdb/...` 全绿。

#### 🔴 重大架构发现：两套并行的「采集任务」系统

实现 Phase 2 时核查发现，项目中已存在两套职责重叠的采集任务系统，需拍板整合方向：

| 系统 | 位置 | 职责 | 状态 |
|---|---|---|---|
| **CMDB 统一调度框架** | `cmdb.CollectionTask` + `scheduler.go` + 注册表 | 持有采集任务、进程内 ticker 按频率触发；`cmdb×cloud` 已 server 拉；`monitor×cloud` 本期接入 | Phase 1–2 新建 |
| **Agent 通道采集任务** | `agentmgr.CollectTask` + gRPC `TaskSync` 下发 + Agent 本地 `scheduler.AddOrUpdate/Remove` | 面向主机/容器经 Agent 通道的采集；注册时经 `SetTaskListProvider` 下发、变更时 `PushTaskList` 主动推送；Agent `CollectSystemMetrics()` 采 CPU/内存/磁盘/网络并 push 回 ingest | **已完整可用** |

**矛盾点**：用户的架构意图是「CMDB 是统一调度者」，但 host/容器经 Agent 通道的采集任务实际由 `agentmgr` 持有与下发，与 `cmdb.CollectionTask` 并存。纳管主机/容器走 `agentmgr`，并不会创建 `cmdb.CollectionTask`；目前 `cmdb` 里只有 cloud 任务。

**整合方向（待决策）**

- **方案 X（统一到 CMDB，推荐）**：`cmdb.CollectionTask` 为唯一真相源，增加 `channel` 字段（`server` 拉 / `agent` 推送）。`agentmgr` 的 `CollectTask` 改为 cmdb 任务的「下发投影」——`buildTaskDefs(agentID)` 改为消费 cmdb 中归属该 agent 的任务；纳管主机/容器时统一经 `cmdb.OnboardResource` 建任务。CMDB 调度器对 `channel=agent` 的任务驱动 `agentmgr.PushTaskList`。代价：需改 `agentmgr` 数据源与 `cmdb↔agentmgr` 注入。
- **方案 Y（保留 agentmgr 独立）**：`cmdb` 只管 server 拉模式（cloud 的 cmdb/monitor），`agentmgr.CollectTask` 管 Agent 通道任务，两套并存。代价：用户「统一调度」意图未完全落地，运维时需同时看两处。

**本期处理**：当时采用方案 Y 的「不破坏现有」变体——`cmdb` 侧 monitor 指标已正确落库、server 拉 monitor 骨架已通、agent 通道 monitor 任务语义已修正；`agentmgr` 的 Agent 通道采集维持现状（已可用）。整合统一留待方案 X 决策后实施（建议并入 Phase 3）。

> **决策（2026-08-01）**：用户明确选择 **方案 X（统一到 CMDB）**。理由：与"CMDB 是统一调度者"的架构意图完全一致，避免运维时同时看两处。落地见下方 **6.6.3 Phase 3 · 方案 X 落地**。

### 6.6.3 Phase 3 · 方案 X 落地状态（已代码实现并验证）

> 决策：✅ 采用**方案 X（统一到 CMDB）**。状态：✅ 已实现 + `go build ./...` / `go vet ./internal/cmdb/... ./internal/agentmgr/... ./cmd/core/...` / `go test ./internal/cmdb/... ./internal/agentmgr/...` 全绿（2026-08-01）。

**核心思路**：`cmdb.CollectionTask` 立为唯一采集任务真相源，新增 `channel` 字段区分执行方（`server`=CMDB 服务端拉取；`agent`=经 Agent 推送上报）。`agentmgr.CollectTask` 退居"Agent 传输层（投影）"——CMDB 创建 `channel=agent` 任务时，经依赖倒置的 `AgentChannel` 接口把任务投影到 agentmgr 下发，采集动作仍由 Agent 本地执行并 push 上报，调度权统一在 CMDB。

**关键设计点**

1. **依赖倒置，无 import 环**：`cmdb` 定义 `AgentChannel` 接口（`SyncAgentCollectTask` / `RemoveAgentCollectTask`）与 `AgentCollectTaskSpec`，不 import `agentmgr`；具体适配器 `agentChannelAdapter` 在 `cmd/core` 实现，把 `cmdb.Kind`（`cmdb`/`monitor`）映射为 `agentmgr.CollectTypeCMDB/CollectTypeMetric`，把 `spec.Target`（`host:<agentID>`）解析为 `TargetAgents`，再调 `agentSvc.UpsertCollectTaskFromCMDB`。
2. **投影幂等**：agentmgr 侧投影任务 ID 固定为 `"cmdb:" + CmdbTaskID`，重复同步 = 覆盖，避免孤儿任务。
3. **纳管即建任务**：主机经 Agent 注册 / 批量安装成功后，`agentmgr` 回调 `OnAgentOnboard(tenantID, agentID)` → `cmdbSvc.OnboardResource(ctx, tenantID, "host", "host:"+agentID, nil, nil)`，按平台策略（全局默认 + 纳管覆盖）建 `channel=agent` 的 cmdb/monitor 任务。`OnboardResource` 对 `resourceType≠cloud` 一律置 `channel=agent`；云资源走 `server`（复用 cloud 采集器）。
4. **避免重复采集 / 双下发**：Agent 本地 `applyTaskList` 会跳过空脚本 `TaskDef`（"内置任务由 Agent 本地管理"），且 `builtin.cmdb.basic`/`builtin.monitor.basic` 内置任务始终在跑并上报。投影任务用空脚本（`Script:""`），并在 `agentmgr` 的 `dispatchCollectTask` 与 `buildTaskDefs` 两处跳过空脚本，双保险——CMDB 投影仅作"统一调度源 + 状态跟踪"，不与内置任务冲突。
5. **调度器语义修正**：`scheduler.runOne` 对 `channel=agent` 任务标记 `delegated`（而非 `pending`）后即返回——采集权 delegated 给 Agent，server 侧不主动拉取。`delegated` 是预期正常状态，区别于配置缺失的 `pending`。
6. **生命周期同步**：`Create/Update/DeleteCollectionTask` 在 `channel=agent` 时同步 `SyncAgentCollectTask` / `RemoveAgentCollectTask`；`channel` 由 `agent` 切回 `server` 时自动 `RemoveAgentCollectTask` 移除投影。

**新增 / 修改文件**

| 文件 | 说明 |
| --- | --- |
| `internal/cmdb/model/collection.go` | `CollectionTask` 新增 `Channel string`（json `channel`：`server`=CMDB 服务端拉取 / `agent`=经 Agent 推送上报） |
| `deploy/migrations/013_cmdb_collection_channel.sql` | `ALTER TABLE cmdb_collection_task ADD COLUMN channel TEXT NOT NULL DEFAULT 'server'` + 索引 |
| `internal/cmdb/pg/collection_repo.go` | `Create/Get/List/Update` 与 `scanCollectionTasks` 全链路读写 `channel`；`Create` 兜底 `channel=""` → `server` |
| `internal/cmdb/collection_service.go` | `AgentChannel` 接口 + `AgentCollectTaskSpec` + `SetAgentChannel`/`toAgentSpec`；`OnboardResource` 按 `resourceType` 计算 `channel` 并传入建任务；`ensureCollectionTask` 经 Service 层 `CreateCollectionTask`（确保 agent 同步触发）；`Create/Update/Delete` 同步 `AgentChannel` |
| `internal/cmdb/scheduler.go` | `runOne` 对 `channel=agent` 标记 `delegated` 返回 |
| `internal/agentmgr/collect.go` | `PutProjection`（跳过脚本非空校验的投影写入，幂等）；`CMDBCollectSpec`；`UpsertCollectTaskFromCMDB` / `RemoveCollectTaskFromCMDB`；`dispatchCollectTask` 跳过空脚本 |
| `internal/agentmgr/collect_handlers.go` | `dispatchCollectTask` 空脚本任务不在此一次性派发路径 |
| `internal/agentmgr/service.go` | `onAgentOnboard` 字段 + `SetOnAgentOnboard`；`buildTaskDefs` 跳过空脚本投影；`handleRegister` + `handleBatchInstall`（3 处注册成功分支）触发 `onAgentOnboard` |
| `cmd/core/main.go` | `agentChannelAdapter`（实现 `cmdb.AgentChannel`）；`agentSvc.SetOnAgentOnboard(...)` + `cmdbSvc.SetAgentChannel(...)` 接线 |

**数据流（主机纳管闭环）**

1. 主机经 Agent 注册 / 批量安装成功 → `agentmgr` 触发 `onAgentOnboard(tenantID, agentID)`；
2. → `cmdbSvc.OnboardResource(ctx, tenantID, "host", "host:"+agentID, nil, nil)`，读 `PlatformPolicy` 按「全局默认 + 纳管覆盖」为 `cmdb`/`monitor` 各建 `channel=agent` 的 `CollectionTask`（`ensureCollectionTask` 走 `CreateCollectionTask` 触发同步）；
3. `CreateCollectionTask` 见 `channel=agent` → `agentChannelAdapter.SyncAgentCollectTask(...)` → `agentSvc.UpsertCollectTaskFromCMDB(...)`，写空脚本投影（ID=`cmdb:<taskID>`）并 `pushTaskListToOnline()` 推送在线 Agent；
4. Agent 端 `applyTaskList` 跳过空脚本投影，由内置 `builtin.cmdb.basic`/`builtin.monitor.basic` 本地采集并 push 上报；CMDB 调度器对 `channel=agent` 任务标 `delegated`，server 侧不重复拉取；
5. 指标路径：Agent push → `grpcTunnelServer.OnMetrics` → `ingestBuf`（与 server 拉路径同一管道统一落库）。

**关键设计校验**

- 依赖单向：`cmdb` 仅定义 `AgentChannel` 契约 + `CollectionTask` 真相源，`agentmgr` 仅作投影传输层，具体适配在 `cmd/core`，无 import 环；
- 幂等正确：投影 ID 固定 `cmdb:<CmdbTaskID>`，重复同步=覆盖；`OnboardResource` 按 `target+kind` 跳过已存在任务；
- 无重复下发：空脚本投影在 `dispatchCollectTask` 与 `buildTaskDefs` 双处跳过，与 Agent 内置任务互不冲突；
- 健壮性：`channel=""` 兜底 `server`；`agentChan==nil` 时（未接线）仅告警不阻塞建任务；stub 模式不空指针。

> **遗留（Phase 3 范围外）**：`container` 纳管的 `OnboardResource` 同样建 `channel=agent` 任务，但当前适配器仅处理 `host:` 前缀（`container:` 投影暂回退为全量在线 Agent）；`container` 类型的精确投影与 K8s 采集回填为后续项。

### 6.7 架构图示

<svg viewBox="0 0 680 496" width="100%" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="CMDB 采集架构：全局默认加纳管覆盖">
  <title>采集策略：全局默认 + 纳管覆盖（策略继承）</title>
  <desc>平台全局策略预填纳管勾选，用户覆盖后形成每资源有效配置，下发 Agent 并路由到 cmdb/monitor 采集器注册表。</desc>
  <defs>
    <marker id="arr" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path d="M2 1L8 5L2 9" fill="none" stroke="#94A3B8" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
    </marker>
  </defs>

  <text x="40" y="30" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="15" font-weight="600" fill="#0F172A">采集策略：全局默认 + 纳管覆盖（策略继承）</text>

  <rect x="40" y="48" width="600" height="64" rx="12" fill="#ECFDF5" stroke="#10B981" stroke-width="1"/>
  <text x="340" y="74" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="14" font-weight="600" fill="#065F46">① 平台全局默认策略（系统设置 · 兜底）</text>
  <text x="340" y="98" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#047857">default_cmdb_enabled = true（基石）· default_monitor_enabled = false（兼容风险）· 各 kind 默认频率</text>

  <path d="M340 112 L340 130" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>
  <text x="352" y="126" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="11" fill="#64748B">新资源纳管时继承</text>

  <rect x="40" y="130" width="600" height="64" rx="12" fill="#EFF6FF" stroke="#3B82F6" stroke-width="1"/>
  <text x="340" y="156" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="14" font-weight="600" fill="#1E40AF">② 纳管界面勾选 = 覆盖全局默认</text>
  <text x="340" y="180" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#1D4ED8">勾选框预填全局默认；用户可逐资源改写 → 记录为有效配置</text>

  <path d="M340 194 L340 212" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>

  <rect x="40" y="212" width="600" height="64" rx="12" fill="#FEF3C7" stroke="#F59E0B" stroke-width="1"/>
  <text x="340" y="238" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="14" font-weight="600" fill="#92400E">③ 每资源有效配置（最终下发态）</text>
  <text x="340" y="262" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#B45309">enable_cmdb_collect / enable_monitor_collect + 频率 · 核心DB(✓✓) 测试机(✗✗) 老IBM(✓✗)</text>

  <path d="M200 276 L200 298" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>
  <path d="M480 276 L480 298" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>

  <rect x="40" y="298" width="290" height="80" rx="12" fill="#EFF6FF" stroke="#3B82F6" stroke-width="1"/>
  <text x="185" y="322" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="13" font-weight="600" fill="#1E40AF">④-A CMDB 采集器注册表</text>
  <text x="185" y="344" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#1D4ED8">kind=cmdb × 资源类型</text>
  <text x="185" y="364" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#1D4ED8">调度器 → 落 CMDB</text>

  <rect x="350" y="298" width="290" height="80" rx="12" fill="#FFFBEB" stroke="#F59E0B" stroke-width="1"/>
  <text x="495" y="322" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="13" font-weight="600" fill="#92400E">④-B 监控采集器注册表</text>
  <text x="495" y="344" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#B45309">kind=monitor × 资源类型</text>
  <text x="495" y="364" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#B45309">基础默认·高级手动 → 落监控</text>

  <path d="M185 378 L185 396" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>
  <path d="M495 378 L495 396" fill="none" stroke="#94A3B8" stroke-width="1.5" marker-end="url(#arr)"/>

  <rect x="40" y="396" width="600" height="56" rx="12" fill="#F1F5F9" stroke="#64748B" stroke-width="1"/>
  <text x="340" y="418" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="14" font-weight="600" fill="#334155">⑤ Agent 拉取/接收有效配置</text>
  <text x="340" y="438" text-anchor="middle" dominant-baseline="central" font-family="-apple-system,Segoe UI,Roboto,sans-serif" font-size="12" fill="#475569">按布尔启停采集协程 · 配置变更优雅重启（不反复重启 Agent）</text>
</svg>

> 图示源文件：`docs/cmdb-collection-architecture.svg`

---

## 附：信息来源说明

- 竞品能力信息来自公开资料检索（2026-08-01），其中 canway.net 系嘉为官网，其"95% 采集覆盖率""日采集百万级"等数据为厂商自述，未经第三方验证。
- 蓝鲸 bk-cmdb 架构信息来自腾讯官方技术分享（Go 微服务 + 资源三分层 + MongoDB）。
- OpsOrigin 侧数据均为代码级核对，非估算。
- 优维科技 EasyOps 信息来自 uwintech.cn 官网、cfanz.cn 报道、baike.com 词条、studygolang.com 发布会回顾（2026-08-01 检索）。
- 广通优云 UYUN 信息来自 uyun.cn 官网、uyun.cn/news 刘东海访谈、blog.csdn.net 优云 SaltStack 实践文（2026-08-01 检索）。
- 注：优维 / 广通官网含产品宣传口径，量化指标（如"80% 效率""60% 成本"）为厂商自述，未经第三方验证。
- OpsAny 信息来自 opsany.com 官网、docs.opsany.com 文档站、demo.opsany.com/docs 功能概述（2026-08-01 检索）。其文档站功能列表 / 架构描述相对客观，量化宣传少，可信度高于优维 / 广通官网。
