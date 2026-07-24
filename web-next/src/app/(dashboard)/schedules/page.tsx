'use client'

import { useEffect, useState } from 'react'
import { useSchedulerStore } from '@/stores/scheduler'
import { useScriptStore } from '@/stores/script'
import { useWorkflowStore } from '@/stores/workflow'
import { useAgentStore } from '@/stores/agent'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, Clock, Timer } from 'lucide-react'

export default function SchedulesPage() {
  const { tasks, fetch, create, update, remove } = useSchedulerStore()
  const { scripts } = useScriptStore()
  const { workflows } = useWorkflowStore()
  const { agents } = useAgentStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', cron_expr: '0 * * * *', task_type: 'script', target_id: '', agent_ids: [] as string[], inputs: {} as any, max_retries: 0, retry_delay_secs: 60, alert_on_failure: false })

  useEffect(() => { fetch() }, [])

  const handleSave = async () => {
    await create(form)
    setDialogOpen(false); setForm({ name: '', cron_expr: '0 * * * *', task_type: 'script', target_id: '', agent_ids: [], inputs: {}, max_retries: 0, retry_delay_secs: 60, alert_on_failure: false })
  }

  const targets = form.task_type === 'script' ? scripts : workflows

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">定时任务</h1><p className="text-sm text-muted-foreground">Cron 定时调度脚本/流程</p></div>
        <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建任务</Button>
      </div>
      <Card>
        <CardContent className="p-0">
          {tasks.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无定时任务。</p> : (
            <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">
              <th className="pb-2 px-4">名称</th><th className="pb-2 px-4">Cron 表达式</th><th className="pb-2 px-4">类型</th><th className="pb-2 px-4">状态</th><th className="pb-2 px-4">重试/告警</th><th className="pb-2 px-4">最后执行</th><th className="pb-2 px-4 w-20"></th>
            </tr></thead><tbody>
              {tasks.map(t => (
                <tr key={t.id} className="border-b last:border-0">
                  <td className="py-2 px-4 font-medium">{t.name}</td>
                  <td className="py-2 px-4 font-mono text-xs">{t.cron_expr}</td>
                  <td className="py-2 px-4"><Badge variant="outline" className="text-xs">{t.task_type}</Badge></td>
                  <td className="py-2 px-4">{t.enabled ? <Badge variant="success" className="text-xs">启用</Badge> : <Badge variant="outline" className="text-xs">禁用</Badge>}</td>
                  <td className="py-2 px-4">
                    {t.max_retries ? <span className="text-xs">重试 {t.max_retries}次</span> : <span className="text-xs text-muted-foreground">不重试</span>}
                    {t.alert_on_failure ? <span className="text-xs text-amber-500 ml-1">· 告警</span> : null}
                    {t.last_error ? <span className="text-[10px] text-destructive block truncate max-w-[120px]" title={t.last_error}>{t.last_error}</span> : null}
                  </td>
                  <td className="py-2 px-4 text-muted-foreground text-xs">{t.last_run_at ? formatTime(t.last_run_at) : '-'}</td>
                  <td className="py-2 px-4"><Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => remove(t.id)}><Trash2 className="h-3 w-3" /></Button></td>
                </tr>
              ))}</tbody>
            </table>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建定时任务">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="每日巡检" /></div>
          <div><label className="text-sm font-medium">Cron 表达式 *</label><Input value={form.cron_expr} onChange={e => setForm(f => ({ ...f, cron_expr: e.target.value }))} placeholder="0 * * * *" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">任务类型</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.task_type} onChange={e => setForm(f => ({ ...f, task_type: e.target.value, target_id: '' }))}>
                <option value="script">脚本</option><option value="workflow">流程</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">目标</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.target_id} onChange={e => setForm(f => ({ ...f, target_id: e.target.value }))}>
                <option value="">选择</option>
                {targets.map(t => <option key={t.id} value={t.id}>{(t as any).name}</option>)}
              </select>
            </div>
          </div>
          <div><label className="text-sm font-medium">执行主机</label>
            <div className="space-y-1 max-h-32 overflow-auto border rounded-md p-2">
              {agents.map(a => (
                <label key={a.id} className="flex items-center gap-2 text-xs py-0.5 cursor-pointer">
                  <input type="checkbox" checked={form.agent_ids.includes(a.id)} onChange={e => {
                    if (e.target.checked) setForm(f => ({ ...f, agent_ids: [...f.agent_ids, a.id] }))
                    else setForm(f => ({ ...f, agent_ids: f.agent_ids.filter(id => id !== a.id) }))
                  }} />
                  {a.name} <Badge variant="outline" className="text-[10px]">{a.address}</Badge>
                </label>
              ))}
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">失败重试次数</label>
              <Input type="number" value={form.max_retries} onChange={e => setForm(f => ({ ...f, max_retries: parseInt(e.target.value) || 0 }))} min={0} max={10} />
            </div>
            <div><label className="text-sm font-medium">重试间隔(秒)</label>
              <Input type="number" value={form.retry_delay_secs} onChange={e => setForm(f => ({ ...f, retry_delay_secs: parseInt(e.target.value) || 60 }))} min={10} max={3600} />
            </div>
          </div>
          <div>
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" checked={form.alert_on_failure} onChange={e => setForm(f => ({ ...f, alert_on_failure: e.target.checked }))} />
              失败时发送告警通知
            </label>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.name || !form.cron_expr || !form.target_id}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
