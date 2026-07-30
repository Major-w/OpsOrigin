'use client'

import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { AppWindow, Rocket, Cloud, History, ArrowRight, Server, Container } from 'lucide-react'

const CARDS = [
  {
    href: '/deploy/apps', title: '应用管理', desc: '管理应用版本发布配置、构建脚本与镜像（主机 / 容器）',
    icon: AppWindow, color: 'text-blue-600', border: 'hover:border-blue-500/30',
  },
  {
    href: '/deploy/releases', title: '发布单', desc: '创建发布单，含审批、执行与回滚流程',
    icon: Rocket, color: 'text-emerald-600', border: 'hover:border-emerald-500/30',
  },
  {
    href: '/deploy/environments', title: '环境管理', desc: '配置部署模式（主机环境 / K8s 容器环境）与目标 Agent',
    icon: Cloud, color: 'text-violet-600', border: 'hover:border-violet-500/30',
  },
  {
    href: '/deploy/history', title: '发布历史', desc: '所有发布记录的审计与回滚入口',
    icon: History, color: 'text-amber-600', border: 'hover:border-amber-500/30',
  },
]

export default function DeployIndexPage() {
  const router = useRouter()
  return (
    <div className="p-6 space-y-6">
      <div>
        <h1 className="text-2xl font-bold">应用发布</h1>
        <p className="text-sm text-muted-foreground">统一的应用发布引擎，同时支持主机环境与 K8s 容器环境两种部署模式</p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {CARDS.map(c => {
          const Icon = c.icon
          return (
            <Card key={c.href} className={`cursor-pointer transition-colors ${c.border}`} onClick={() => router.push(c.href)}>
              <CardHeader className="flex flex-row items-center justify-between space-y-0">
                <div className="flex items-center gap-3">
                  <div className={`w-10 h-10 rounded-lg flex items-center justify-center bg-muted/40`}>
                    <Icon className={`h-5 w-5 ${c.color}`} />
                  </div>
                  <div>
                    <CardTitle className="text-base">{c.title}</CardTitle>
                    <CardDescription className="text-xs mt-0.5">{c.desc}</CardDescription>
                  </div>
                </div>
                <ArrowRight className="h-4 w-4 text-muted-foreground" />
              </CardHeader>
            </Card>
          )
        })}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="rounded-lg border bg-muted/20 p-4 flex items-start gap-3">
          <Server className="h-5 w-5 text-slate-500 mt-0.5" />
          <div>
            <p className="text-sm font-medium">主机环境</p>
            <p className="text-xs text-muted-foreground mt-1">经被管主机 Agent 下发部署脚本（shell），真实在目标主机执行，并做 HTTP 健康检查。</p>
          </div>
        </div>
        <div className="rounded-lg border bg-muted/20 p-4 flex items-start gap-3">
          <Container className="h-5 w-5 text-violet-500 mt-0.5" />
          <div>
            <p className="text-sm font-medium">容器环境</p>
            <p className="text-xs text-muted-foreground mt-1">经 K8s 网关 Agent 下发 kubectl 指令（set image / apply），以 rollout status 探活。</p>
          </div>
        </div>
      </div>
    </div>
  )
}
