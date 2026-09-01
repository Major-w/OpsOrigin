
import React, { useState } from 'react'
import { Copy, Check, Terminal, Server, Cloud, Network, ShieldCheck, Rocket, Cpu, Database, Globe, AlertTriangle, BookOpen, Boxes, Container, ArrowRight, Settings2 } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { Card, CardHeader, CardTitle, CardDescription, CardContent } from '@/components/ui/card'
import { useThemeStore } from '@/stores/theme'
import { getMessage } from '@/lib/message'
import { Button, Tabs, Tag } from 'antd'

function CodeBlock({ code, lang = 'bash' }: { code: string; lang?: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard.writeText(code)
    setCopied(true)
    getMessage().success('已复制到剪贴板')
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <div className="relative rounded-xl overflow-hidden border border-white/5 bg-[#0B0F19]">
      <div className="flex items-center justify-between px-4 py-2 border-b border-white/5">
        <span className="text-[11px] font-mono text-[var(--muted-foreground)]">{lang}</span>
        <Button type="text" size="middle" className="h-7 text-[var(--secondary-foreground)] hover:text-white" onClick={copy}>
          {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
          {copied ? '已复制' : '复制'}
        </Button>
      </div>
      <pre className="overflow-x-auto p-4 text-xs font-mono leading-relaxed text-[var(--foreground)]"><code>{code}</code></pre>
    </div>
  )
}

interface Block { caption?: string; code: string; lang?: string }
interface Section { id: string; title: string; desc: string; Icon: LucideIcon; blocks: Block[] }

const sections: Section[] = [
  {
    id: 'agent',
    title: '主机 Agent 接入（gRPC）',
    desc: 'Agent 以 gRPC 隧道与 Core 保持长连，支持直连 Core 或经 Proxy 中转。HTTP 轮询与 WebSocket 两种模式已禁用。',
    Icon: Server,
    blocks: [
      {
        caption: '方式一：一键安装并直连 Core（替换 <CORE_HOST> 为你的 Core 地址）',
        lang: 'bash',
        code: `# 一键安装并接入 Core（gRPC 直连）
curl -fsSL http://<CORE_HOST>/api/v1/agents/install-script?os=linux | sudo bash -s -- \\
  --grpc-server <CORE_HOST>:9091

# 方式二：经 Proxy 中转接入（gRPC 隧道，适用于隔离网段）
curl -fsSL http://<CORE_HOST>/api/v1/agents/install-script?os=linux | sudo bash -s -- \\
  --proxy <PROXY_HOST>:9090`,
      },
      {
        caption: '方式三：手动构建二进制后运行',
        lang: 'bash',
        code: `# 构建 agent 二进制（产出 bin/agent）
make agent

# gRPC 直连 Core
bin/agent --grpc-server 10.0.0.10:9091

# 经 Proxy 中转（目标在隔离网段、Core 不可达时）
bin/agent --proxy 10.0.0.20:9090`,
      },
    ],
  },
  {
    id: 'agentless',
    title: 'Agentless 接入（Server 直连 / 经 Proxy）',
    desc: '无需在被管机上安装 Agent：由 Core 直接对目标发起 SSH，或指定 proxy_id 由 Proxy 节点代为 SSH（目标在隔离网段时）。',
    Icon: Terminal,
    blocks: [
      {
        caption: 'Server 直连目标 SSH（首次连接校验主机密钥 TOFU）',
        lang: 'bash',
        code: `curl -X POST http://<CORE>:8080/api/v1/ssh/execute \\
  -H 'Content-Type: application/json' \\
  -d '{
    "host": "192.168.1.50",
    "port": 22,
    "username": "root",
    "ssh_key": "<SSH 私钥内容>",
    "command": "uname -a",
    "proxy_id": ""
  }'`,
      },
      {
        caption: '经 Proxy 代为 SSH（proxy_id 填 Proxy 节点 ID；Proxy 不可达时自动降级为直连）',
        lang: 'bash',
        code: `curl -X POST http://<CORE>:8080/api/v1/ssh/execute \\
  -H 'Content-Type: application/json' \\
  -d '{
    "host": "10.20.30.40",
    "port": 22,
    "username": "root",
    "ssh_key": "<SSH 私钥内容>",
    "command": "df -h",
    "proxy_id": "<proxy-agent-id>"
  }'`,
      },
    ],
  },
  {
    id: 'proxy',
    title: 'Proxy 节点接入（双向隧道中继）',
    desc: 'Proxy 同时与 Core、Agent/Agentless 目标建连，作为中继转发 gRPC 隧道与 SSH 流量，打通隔离网段。',
    Icon: Network,
    blocks: [
      {
        caption: '启动 Proxy 节点',
        lang: 'bash',
        code: `# 启动 Proxy（gRPC 隧道 :9091，TCP 隧道 :9090，WS Hub :5588）
bin/proxy --listen :9090 --grpc-tunnel :9091 --ws-hub :5588

# Agent 侧通过 --proxy 指向该节点即可接入
bin/agent --proxy <PROXY_HOST>:9090`,
      },
    ],
  },
  {
    id: 'sandbox',
    title: '沙箱环境执行（免选目标主机）',
    desc: '勾选「沙箱执行」后无需选择目标主机：Server 在其部署机上的一个沙箱环境内执行脚本，独立临时目录 + 超时约束，且不暴露 Server 真实凭证。',
    Icon: Container,
    blocks: [
      {
        caption: '专用沙箱执行端点（不传 host，自动挑选沙箱环境）',
        lang: 'bash',
        code: `curl -X POST http://<CORE>:8080/api/v1/sandbox/exec \\
  -H 'Content-Type: application/json' \\
  -d '{
    "script": "echo hello from sandbox; df -h",
    "timeout": 60,
    "sandbox_id": "<可选；不填则随机挑选一个沙箱环境>"
  }'`,
      },
      {
        caption: '或在 SSH 执行端点中带 "sandbox": true（自动忽略 host 字段）',
        lang: 'json',
        code: `{
  "sandbox": true,
  "command": "uname -a",
  "timeout": 30
}`,
      },
    ],
  },
  {
    id: 'k8s',
    title: 'K8s 探针纳管（DaemonSet）',
    desc: '以 DaemonSet 方式在每个节点部署探针，采集节点 / 工作负载 / 事件 / 指标，每 60s 上报 Core。支持一键部署或生成清单后手动 apply。',
    Icon: Boxes,
    blocks: [
      {
        caption: '构建探针二进制并生成 DaemonSet 清单后 apply（替换变量）',
        lang: 'bash',
        code: `# 构建 k8s 探针二进制(产出 bin/k8s-probe)
make k8s-probe

# 生成 DaemonSet 清单
bin/k8s-probe --print-manifest \\
  --server <CORE> --token <TOKEN> --cluster <集群名> \\
  > k8s-probe.yaml

# 部署到目标集群
kubectl apply -f k8s-probe.yaml`,
      },
    ],
  },
  {
    id: 'cloud',
    title: '多云资源纳管',
    desc: '后端已统一实现四朵云 Provider 接口（阿里云 HMAC-SHA1 / 腾讯云 TC3-HMAC-SHA256 / 华为云 SDK-HMAC-SHA256 / AWS SigV4），零额外 SDK 依赖，自动采集 VM / 数据库 / 负载均衡 / 集群。',
    Icon: Cloud,
    blocks: [
      {
        caption: '在「凭证管理」中新增对应云厂商凭证后，系统按 Provider 接口自动采集。各云所需字段如下：',
        lang: 'text',
        code: `阿里云 Aliyun   : AccessKeyId / AccessKeySecret
腾讯云 Tencent : SecretId / SecretKey
华为云 Huawei  : AccessKey / SecretKey（项目级 project_id）
AWS           : AccessKeyId / SecretAccessKey（含 region）`,
      },
    ],
  },
  {
    id: 'collection-policy',
    title: '全局默认采集策略',
    desc: '资源接入时各项采集开关的默认值，在系统管理中统一配置。纳管单个资源时可覆盖此默认值。',
    Icon: Settings2,
    blocks: [
      {
        caption: '配置路径：系统管理 → 采集策略',
        lang: 'text',
        code: `打开「系统管理 / 采集策略」页面，可修改以下默认项：
  · 基础信息采集（CPU / 内存 / 磁盘 / 网络）
  · 进程列表采集
  · 端口监听采集
  · 日志采集开关
  · 指标采集频率

纳管资源时，「采集设置」开关默认预填此处的值，可逐资源单独覆盖。`,
      },
    ],
  },
  {
    id: 'trouble',
    title: '常见问题排查',
    desc: '接入过程中的典型报错与处置方式。',
    Icon: AlertTriangle,
    blocks: [
      {
        caption: '排障速查表',
        lang: 'text',
        code: `Agent 启动报 "仅支持 gRPC 接入"
  → 未指定 --grpc-server 或 --proxy，请二选一

gRPC 连接失败
  → 检查 Core :9091 是否可达、Agent 与 Core 的共享密钥是否一致

Agentless SSH 执行失败
  → 首次连接会校验目标主机密钥(TOFU)，请确认 known_hosts 或重新信任

Proxy 中转无响应
  → 检查 Proxy :9090 / :9091 是否在线，Agent 的 --proxy 是否指向正确

K8s 探针无数据
  → 检查 ServiceAccount 与 ClusterRole 绑定是否已创建

沙箱执行无输出
  → 检查 Server 部署机上是否存在已注册的沙箱环境(sandbox agent)`,
      },
    ],
  },
]

export default function GuidePage() {
  const { theme } = useThemeStore()
  const [active, setActive] = React.useState(sections[0].id)

  React.useEffect(() => {
    const obs = new IntersectionObserver(
      (entries) => {
        entries.forEach((e) => {
          if (e.isIntersecting) setActive(e.target.id)
        })
      },
      { rootMargin: '-80px 0px -70% 0px' }
    )
    sections.forEach((s) => {
      const el = document.getElementById(s.id)
      if (el) obs.observe(el)
    })
    return () => obs.disconnect()
  }, [])

  const go = (id: string) => (ev: React.MouseEvent) => {
    ev.preventDefault()
    const el = document.getElementById(id)
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    setActive(id)
  }

  return (
    <div className="p-6 space-y-8 max-w-7xl mx-auto">
      <div className="flex items-start justify-between gap-4 flex-wrap">
        <div>
          <div className="flex items-center gap-2">
            <div className="flex items-center justify-center w-9 h-9 rounded-xl bg-blue-500/10 text-blue-400">
              <BookOpen className="h-5 w-5" />
            </div>
            <h1 className="text-2xl font-bold tracking-tight">接入操作指引</h1>
          </div>
          <p className="text-sm text-muted-foreground mt-2 max-w-2xl">
            面向运维人员的接入指南：主机 Agent（gRPC）、Agentless（直连 / 经 Proxy）、Proxy 节点、沙箱执行、K8s 探针与多云纳管。
          </p>
        </div>
        <Tag>
          <ShieldCheck className="h-3.5 w-3.5 mr-1" /> v1.0 · gRPC only
        </Tag>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-[220px_1fr] gap-8">
        {/* 左侧目录 */}
        <aside className="hidden lg:block">
          <div className="sticky top-6 space-y-1">
            <p className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70 px-3 mb-2">目录</p>
            {sections.map((s) => {
              const isActive = active === s.id
              return (
                <a
                  key={s.id}
                  href={`#${s.id}`}
                  onClick={go(s.id)}
                  className={
                    'flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm transition-all duration-200 ' +
                    (isActive
                      ? 'bg-blue-50 text-blue-600 border border-blue-200 dark:bg-blue-500/10 dark:text-blue-400 dark:border-blue-500/20'
                      : theme === 'dark'
                        ? 'text-[var(--secondary-foreground)] hover:text-white hover:bg-white/[0.03] border border-transparent'
                        : 'text-gray-600 hover:text-gray-900 hover:bg-gray-100 border border-transparent')
                  }
                >
                  <s.Icon className="h-4 w-4" strokeWidth={1.5} />
                  <span className="font-medium tracking-wide">{s.title}</span>
                </a>
              )
            })}
          </div>
        </aside>

        {/* 右侧内容 */}
        <div className="space-y-6">
          {sections.map((s) => (
            <Card glass key={s.id} id={s.id} className="scroll-mt-24">
              <CardHeader>
                <div className="flex items-center gap-2">
                  <div className="flex items-center justify-center w-7 h-7 rounded-lg bg-blue-500/10 text-blue-400">
                    <s.Icon className="h-4 w-4" strokeWidth={1.5} />
                  </div>
                  <CardTitle className="text-base text-gray-900 dark:text-white">{s.title}</CardTitle>
                </div>
                <CardDescription className="mt-2">{s.desc}</CardDescription>
              </CardHeader>
              <CardContent className="space-y-4">
                {s.blocks.map((b, i) => (
                  <div key={i} className="space-y-2">
                    {b.caption && <p className="text-sm text-muted-foreground">{b.caption}</p>}
                    <CodeBlock code={b.code} lang={b.lang} />
                  </div>
                ))}
              </CardContent>
            </Card>
          ))}

          {/* 多云：四朵云标签页 */}
          <Card glass id="cloud-detail" className="scroll-mt-24">
            <CardHeader>
              <div className="flex items-center gap-2">
                <div className="flex items-center justify-center w-7 h-7 rounded-lg bg-blue-500/10 text-blue-400">
                  <Globe className="h-4 w-4" strokeWidth={1.5} />
                </div>
                <CardTitle className="text-base text-gray-900 dark:text-white">四朵云接入差异</CardTitle>
              </div>
              <CardDescription className="mt-2">统一 cloud.Provider 接口，签名算法各异，均无需引入厂商 SDK。</CardDescription>
            </CardHeader>
            <CardContent>
              <Tabs defaultActiveKey="aliyun" items={[
                { key: 'aliyun', label: '阿里云', children: (
                  <div className="space-y-3">
                    <p className="text-sm text-muted-foreground">签名：HMAC-SHA1（RPC 风格，参数按字典序拼接后签名）。采集 ECS / RDS / SLB。</p>
                    <CodeBlock lang="text" code={`凭证字段: AccessKeyId / AccessKeySecret
采集资源: 云服务器 ECS、云数据库 RDS、负载均衡 SLB`} />
                  </div>
                )},
                { key: 'tencent', label: '腾讯云', children: (
                  <div className="space-y-3">
                    <p className="text-sm text-muted-foreground">签名：TC3-HMAC-SHA256（TC3 标准，含日期/服务名/请求体哈希）。采集 CVM / TencentDB / CLB。</p>
                    <CodeBlock lang="text" code={`凭证字段: SecretId / SecretKey
采集资源: 云服务器 CVM、云数据库 TencentDB、负载均衡 CLB`} />
                  </div>
                )},
                { key: 'huawei', label: '华为云', children: (
                  <div className="space-y-3">
                    <p className="text-sm text-muted-foreground">签名：SDK-HMAC-SHA256（项目级，需 project_id 作用域）。采集 ECS / RDS / ELB。</p>
                    <CodeBlock lang="text" code={`凭证字段: AccessKey / SecretKey（project_id）
采集资源: 弹性云服务器 ECS、云数据库 RDS、弹性负载均衡 ELB`} />
                  </div>
                )},
                { key: 'aws', label: 'AWS', children: (
                  <div className="space-y-3">
                    <p className="text-sm text-muted-foreground">签名：SigV4（Query 协议，含日期/region/service 的派生密钥）。采集 EC2 / RDS / ELB。</p>
                    <CodeBlock lang="text" code={`凭证字段: AccessKeyId / SecretAccessKey（含 region）
采集资源: 弹性计算 EC2、关系型数据库 RDS、弹性负载均衡 ELB`} />
                  </div>
                )},
              ]} />
            </CardContent>
          </Card>

          <div className="flex items-center gap-2 text-sm text-muted-foreground pt-2">
            <ArrowRight className="h-4 w-4" />
            <span>需要更完整的脚本管理？前往「脚本管理」「凭证管理」「资源纳管」继续配置。</span>
          </div>
        </div>
      </div>
    </div>
  )
}
