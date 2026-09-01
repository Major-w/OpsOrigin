
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Select, Tag, Checkbox } from 'antd'
import { useScriptStore } from '@/stores/script'
import { useAgentStore } from '@/stores/agent'
import { useSchedulerStore } from '@/stores/scheduler'
import { apiFetch } from '@/lib/utils'
import { Scan, Play, Clock, CheckCircle, XCircle, Loader2 } from 'lucide-react'
import { getMessage } from '@/lib/message'

export default function InspectionPage() {
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { agents, fetch: fetchAgents } = useAgentStore()
  const { tasks, fetch: fetchSchedules, create, update: updateSched } = useSchedulerStore()

  const [scriptId, setScriptId] = useState('')
  const [agentIds, setAgentIds] = useState<string[]>([])
  const [cron, setCron] = useState('0 8 * * *')
  const [schedName, setSchedName] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<any>(null)

  useEffect(() => { fetchScripts(); fetchAgents(); fetchSchedules() }, [])

  const runNow = async () => {
    if (!scriptId || agentIds.length === 0) { getMessage().error('请选择巡检脚本和目标主机'); return }
    setRunning(true); setResult(null)
    try {
      const r = await apiFetch<any>(`/scripts/${scriptId}/execute`, {
        method: 'POST',
        body: JSON.stringify({ agent_id: agentIds[0], timeout: 600, params: {} }),
      })
      setResult(r)
      getMessage().success(r.exit_code === 0 ? '巡检通过' : `巡检完成, exitCode=${r.exit_code}`)
    } catch (e: any) { getMessage().error('执行失败: ' + e.message) }
    setRunning(false)
  }

  const schedule = async () => {
    if (!scriptId || !schedName.trim() || !cron.trim()) { getMessage().error('请填写完整'); return }
    await create({ name: schedName, cron_expr: cron, task_type: 'script', target_id: scriptId, agent_ids: agentIds, enabled: true })
    setSchedName('')
    getMessage().success('巡检计划已创建')
    fetchSchedules()
  }

  const onlineAgents = agents.filter(a => a.status === 'online')
  const inspectionScripts = scripts.filter(s => s.type === 'shell' || s.type === 'python')

  return <div className="p-6 space-y-4">
    <div><h1 className="text-2xl font-bold">自动化巡检</h1><p className="text-xs text-muted-foreground">选择脚本在指定主机上执行巡检, 可设置为定时任务</p></div>

    <div className="grid md:grid-cols-2 gap-4">
      <Card><CardHeader className="py-3"><CardTitle className="text-sm">立即巡检</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div><label className="text-xs font-medium">巡检脚本</label>
            <Select value={scriptId || undefined} onChange={v => setScriptId(v)} placeholder="选择脚本" className="w-full mt-1" options={inspectionScripts.map(s => ({ label: s.name, value: s.id }))} />
          </div>
          <div><label className="text-xs font-medium">目标主机</label>
            <div className="max-h-32 overflow-y-auto border rounded-md mt-1 p-1">
              {onlineAgents.length===0?<p className="text-[10px] text-muted-foreground p-2">无在线Agent</p>:
              onlineAgents.map(a=><label key={a.id} className="flex items-center gap-1.5 text-[11px] cursor-pointer hover:bg-accent rounded px-1 py-0.5">
                <Checkbox checked={agentIds.includes(a.id)} onChange={()=>setAgentIds(prev=>prev.includes(a.id)?prev.filter(i=>i!==a.id):[...prev,a.id])}/>
                {a.name||a.id.slice(0,8)} ({a.address})
              </label>)}
            </div>
          </div>
          <Button type="primary" onClick={runNow} disabled={running||!scriptId||agentIds.length===0}><Play className="h-4 w-4 mr-1"/>{running?'执行中...':'立即巡检'}</Button>
          {result && <div className={`text-xs p-2 rounded ${result.exit_code===0?'bg-blue-50 text-blue-700':'bg-red-50 text-red-700'}`}>
            {result.exit_code===0?<CheckCircle className="h-4 w-4 inline mr-1"/>:<XCircle className="h-4 w-4 inline mr-1"/>}
            exitCode={result.exit_code}, {result.duration_ms}ms
            {result.stdout && <pre className="mt-1 text-[10px] overflow-auto max-h-24">{result.stdout}</pre>}
          </div>}
        </CardContent>
      </Card>

      <Card><CardHeader className="py-3"><CardTitle className="text-sm">定时巡检</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div><label className="text-xs font-medium">计划名称</label><Input value={schedName} onChange={e=>setSchedName(e.target.value)} placeholder="每日主机巡检" className="mt-1"/></div>
          <div><label className="text-xs font-medium">Cron 表达式</label><Input value={cron} onChange={e=>setCron(e.target.value)} placeholder="0 8 * * *" className="mt-1"/></div>
          <Button type="primary" onClick={schedule} disabled={!schedName.trim()||!scriptId}><Clock className="h-4 w-4 mr-1"/>创建定时计划</Button>
        </CardContent>
      </Card>
    </div>

    <Card><CardHeader className="py-3"><CardTitle className="text-sm">已有巡检计划</CardTitle></CardHeader>
      <CardContent>
        {tasks.filter(t=>t.task_type==='script').length===0?<p className="text-xs text-muted-foreground">暂无</p>:
        tasks.filter(t=>t.task_type==='script').map(t=><div key={t.id} className="flex items-center justify-between text-sm py-1"><span className="font-medium">{t.name}</span><span className="text-xs text-muted-foreground">{t.cron_expr}</span><Tag color={t.enabled?'green':'default'} className="text-[10px]">{t.enabled?'启用':'禁用'}</Tag></div>)}
      </CardContent>
    </Card>
  </div>
}
