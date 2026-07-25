'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useScriptStore, type ExecutionResult } from '@/stores/script'
import { useSchedulerStore } from '@/stores/scheduler'
import { Play, Clock, CheckCircle, XCircle, AlertCircle, Loader2, RefreshCw } from 'lucide-react'
import { formatTime } from '@/lib/utils'
import { apiFetch } from '@/lib/utils'

export default function JobCenterPage() {
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { tasks, fetch: fetchSchedules } = useSchedulerStore()
  const [execHistory, setExecHistory] = useState<any[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetchScripts(); fetchSchedules()
    // 拉取所有脚本的执行历史
    apiFetch<any[]>('/scripts/executions').then(data => {
      setExecHistory(data || [])
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [])

  const runningJobs = execHistory.filter(e => e.status === 'running')
  const recentJobs = execHistory.slice(0, 20)

  const statusIcon = (s: string) => {
    if (s === 'succeeded') return <CheckCircle className="h-4 w-4 text-emerald-500"/>
    if (s === 'failed') return <XCircle className="h-4 w-4 text-red-500"/>
    if (s === 'running') return <Loader2 className="h-4 w-4 text-blue-500 animate-spin"/>
    return <AlertCircle className="h-4 w-4 text-amber-500"/>
  }

  return <div className="p-6 space-y-4">
    <div><h1 className="text-xl font-bold">作业中心</h1><p className="text-xs text-muted-foreground">查看脚本执行状态和定时任务运行历史</p></div>

    <Tabs defaultValue="running">
      <TabsList>
        <TabsTrigger value="running"><Play className="h-3 w-3 mr-1"/>运行中 ({runningJobs.length})</TabsTrigger>
        <TabsTrigger value="history">执行历史 ({recentJobs.length})</TabsTrigger>
        <TabsTrigger value="scheduled">定时任务 ({tasks.length})</TabsTrigger>
      </TabsList>

      <TabsContent value="running" className="mt-3 space-y-2">
        {runningJobs.length === 0 ? <Card><CardContent className="py-6 text-center text-muted-foreground text-sm">暂无运行中的作业</CardContent></Card> :
        runningJobs.map(j => <Card key={j.id}><CardContent className="p-3 flex items-center gap-3 text-sm">{statusIcon(j.status)}<span className="flex-1 font-medium truncate">{j.script_name || j.id?.slice(0,8)}</span><Badge variant="default" className="text-xs">运行中</Badge></CardContent></Card>)}
      </TabsContent>

      <TabsContent value="history" className="mt-3 space-y-2">
        {loading ? <Loader2 className="h-6 w-6 animate-spin mx-auto my-8"/> :
        recentJobs.length === 0 ? <Card><CardContent className="py-6 text-center text-muted-foreground text-sm">暂无执行记录</CardContent></Card> :
        recentJobs.map(j => <Card key={j.id}><CardContent className="p-3 flex items-center justify-between text-sm">{statusIcon(j.status)}<span className="flex-1 ml-2 font-medium truncate">{j.script_name || j.id?.slice(0,8)}</span><span className="text-xs text-muted-foreground">{j.duration_ms}ms</span><span className="text-xs text-muted-foreground">{formatTime(j.created_at)}</span>{j.error && <span className="text-xs text-red-500 truncate max-w-[120px]">{j.error}</span>}</CardContent></Card>)}
      </TabsContent>

      <TabsContent value="scheduled" className="mt-3 space-y-2">
        {tasks.length === 0 ? <Card><CardContent className="py-6 text-center text-muted-foreground text-sm">暂无定时任务</CardContent></Card> :
        tasks.map(t => <Card key={t.id}><CardContent className="p-3 flex items-center justify-between text-sm"><Clock className="h-4 w-4 text-amber-500"/><span className="flex-1 ml-2 font-medium">{t.name}</span><Badge variant={t.enabled?'success':'outline'} className="text-xs">{t.enabled?'启用':'禁用'}</Badge><span className="text-xs text-muted-foreground ml-2">{t.cron_expr}</span>{t.last_error && <span className="text-xs text-red-500 truncate max-w-[100px]">⚠{t.last_error}</span>}</CardContent></Card>)}
      </TabsContent>
    </Tabs>
  </div>
}
