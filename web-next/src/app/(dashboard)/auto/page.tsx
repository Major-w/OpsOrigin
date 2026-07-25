'use client'

import { useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { useScriptStore } from '@/stores/script'
import { useWorkflowStore } from '@/stores/workflow'
import { useSchedulerStore } from '@/stores/scheduler'
import { Terminal, GitBranch, Clock, Bug, Download, Scan, PackagePlus, ShieldCheck, Play } from 'lucide-react'

export default function AutoDashboard() {
  const router = useRouter()
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { workflows, fetch: fetchWorkflows } = useWorkflowStore()
  const { tasks, fetch: fetchSchedules } = useSchedulerStore()

  useEffect(() => { fetchScripts(); fetchWorkflows(); fetchSchedules() }, [])

  const onlineScripts = scripts.filter(s => !s.public).length
  const publicScripts = scripts.filter(s => s.public).length
  const activeWorkflows = workflows.filter(w => w.steps?.length > 0).length
  const activeSchedules = tasks.filter(t => t.enabled).length

  const cards = [
    { title: '脚本库', count: scripts.length, sub: `${publicScripts} 公共`, icon: Terminal, color: 'text-blue-500', href: '/scripts' },
    { title: '作业流程', count: activeWorkflows, sub: `${workflows.length} 个定义`, icon: GitBranch, color: 'text-emerald-500', href: '/workflows' },
    { title: '定时任务', count: activeSchedules, sub: `${tasks.length} 个配置`, icon: Clock, color: 'text-amber-500', href: '/schedules' },
    { title: '漏洞扫描', count: 'Trivy', sub: 'CVE 扫描', icon: ShieldCheck, color: 'text-red-500', href: '/auto/vuln-scan' },
    { title: '文件分发', count: '批量', sub: 'Agent 分发', icon: PackagePlus, color: 'text-teal-500', href: '/auto/software' },
    { title: '自动化巡检', count: '执行', sub: '定时巡检', icon: Scan, color: 'text-purple-500', href: '/auto/inspection' },
  ]

  return <div className="p-6 space-y-4">
    <div><h1 className="text-xl font-bold">自动化运维</h1><p className="text-xs text-muted-foreground">脚本执行、定时调度、漏洞扫描、补丁安装</p></div>

    <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
      {cards.map(c => <Card key={c.href} className="cursor-pointer hover:border-primary/50" onClick={()=>router.push(c.href)}>
        <CardContent className="p-4 flex items-center gap-3">
          <c.icon className={`h-7 w-7 ${c.color} shrink-0`}/>
          <div><p className="text-2xl font-bold">{c.count}</p><p className="text-xs text-muted-foreground">{c.sub}</p></div>
        </CardContent>
      </Card>)}
    </div>

    <Card><CardHeader className="py-3"><CardTitle className="text-sm">快速操作</CardTitle></CardHeader>
      <CardContent className="flex gap-2 flex-wrap">
        <Button size="sm" variant="outline" onClick={()=>router.push('/scripts/new')}><Terminal className="h-3.5 w-3.5 mr-1"/>新建脚本</Button>
        <Button size="sm" variant="outline" onClick={()=>router.push('/workflows/new')}><GitBranch className="h-3.5 w-3.5 mr-1"/>新建作业流程</Button>
        <Button size="sm" variant="outline" onClick={()=>router.push('/schedules')}><Clock className="h-3.5 w-3.5 mr-1"/>新建定时任务</Button>
        <Button size="sm" variant="outline" onClick={()=>router.push('/auto/vuln-scan')}><ShieldCheck className="h-3.5 w-3.5 mr-1"/>安全扫描</Button>
      </CardContent>
    </Card>

    <Card><CardHeader className="py-3"><CardTitle className="text-sm">最近脚本执行</CardTitle></CardHeader>
      <CardContent>
        {scripts.slice(0, 5).map(s => <div key={s.id} className="flex items-center justify-between py-1 text-sm">
          <span className="truncate"><Badge variant="outline" className="text-[10px] mr-2">{s.type}</Badge>{s.name}</span>
          <Button variant="ghost" size="sm" className="h-6 text-xs" onClick={()=>router.push(`/scripts/${s.id}`)}><Play className="h-3 w-3 mr-1"/>执行</Button>
        </div>)}
        {scripts.length === 0 && <p className="text-xs text-muted-foreground">暂无脚本，先创建第一个脚本</p>}
      </CardContent>
    </Card>
  </div>
}
