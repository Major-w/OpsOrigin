'use client'

import { useCallback, useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { toast } from '@/stores/toast'
import { useAgentStore } from '@/stores/agent'
import { formatTime, cn } from '@/lib/utils'
import { Plus, Trash2, Play, Pause, Activity, Database, Gauge, History, Loader2, Rocket } from 'lucide-react'

interface CollectTask {
  id: string
  name: string
  type: 'cmdb' | 'metric'
  script: string
  script_type?: string
  interval_sec: number
  target_agents?: string[]
  enabled: boolean
  timeout_sec?: number
  last_run_at?: string
  created_at: string
}

interface CollectRun {
  run_id: string
  collect_task_id: string
  agent_id: string
  success: boolean
  output?: string
  metrics?: { name: string; value: number; unit?: string }[]
  error?: string
  started_at: string
  finished_at: string
}

const EMPTY_FORM = {
  name: '', type: 'cmdb' as 'cmdb' | 'metric', script: '', script_type: 'shell',
  interval_sec: 60, target_agents: [] as string[], enabled: true, timeout_sec: 60,
}

export default function CollectTasksPage() {
  const agents = useAgentStore(s => s.agents)
  const fetchAgents = useAgentStore(s => s.fetch)
  const [tasks, setTasks] = useState<CollectTask[]>([])
  const [runs, setRuns] = useState<Record<string, CollectRun[]>>({})
  const [metrics, setMetrics] = useState<any[]>([])
  const [tab, setTab] = useState<'tasks' | 'metrics'>('tasks')
  const [loading, setLoading] = useState(false)
  const [dialog, setDialog] = useState(false)
  const [runsDialog, setRunsDialog] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState(EMPTY_FORM)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const r = await fetch('/api/v1/collect-tasks')
      if (r.ok) setTasks(await r.json())
    } catch { toast.error('加载采集任务失败') }
    setLoading(false)
  }, [])

  useEffect(() => {
    load()
    fetchAgents()
  }, [load, fetchAgents])

  const loadMetrics = async () => {
    try {
      const r = await fetch('/api/v1/collect-metrics?limit=200')
      if (r.ok) setMetrics(await r.json())
    } catch {}
  }

  useEffect(() => { if (tab === 'metrics') loadMetrics() }, [tab])

  const loadRuns = async (id: string) => {
    try {
      const r = await fetch(`/api/v1/collect-tasks/${id}/runs`)
      if (r.ok) {
        const data = await r.json()
        setRuns(prev => ({ ...prev, [id]: data }))
      }
    } catch {}
  }

  const handleCreate = async () => {
    if (!form.name.trim()) return toast.error('请输入任务名称')
    if (!form.script.trim()) return toast.error('请输入采集脚本')
    setSaving(true)
    try {
      const r = await fetch('/api/v1/collect-tasks', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(form),
      })
      if (!r.ok) { const d = await r.json().catch(() => ({})); return toast.error(d?.error || '创建失败') }
      toast.success('采集任务已创建')
      setDialog(false); setForm(EMPTY_FORM); load()
    } catch { toast.error('创建失败') } finally { setSaving(false) }
  }

  const handleToggle = async (t: CollectTask) => {
    try {
      await fetch(`/api/v1/collect-tasks/${t.id}`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...t, enabled: !t.enabled }),
      })
      load()
    } catch { toast.error('更新失败') }
  }

  const handleDelete = async (id: string) => {
    if (!confirm('删除该采集任务？')) return
    try {
      await fetch(`/api/v1/collect-tasks/${id}`, { method: 'DELETE' })
      toast.success('已删除'); load()
    } catch { toast.error('删除失败') }
  }

  const openRuns = (id: string) => { setRunsDialog(id); loadRuns(id) }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><Rocket className="h-6 w-6 text-emerald-500" />数据采集</h1>
          <p className="text-sm text-muted-foreground">定时向 Agent 派发只读采集任务，上报 CMDB 与监控指标</p>
        </div>
        <Button size="sm" onClick={() => setDialog(true)}><Plus className="h-4 w-4 mr-1" />新建采集任务</Button>
      </div>

      {/* 能力说明 */}
      <div className="flex gap-3">
        <div className="flex items-center gap-2 text-xs p-3 rounded-lg bg-emerald-50 dark:bg-emerald-500/5 border border-emerald-200 dark:border-emerald-500/20 flex-1">
          <Database className="h-4 w-4 text-emerald-600 shrink-0" />
          <span><b>CMDB 采集</b> — 脚本输出 JSON 对象（hostname/os/cpu/memory/disk/ips），结果自动更新主机 CMDB 数据</span>
        </div>
        <div className="flex items-center gap-2 text-xs p-3 rounded-lg bg-blue-50 dark:bg-blue-500/5 border border-blue-200 dark:border-blue-500/20 flex-1">
          <Gauge className="h-4 w-4 text-blue-600 shrink-0" />
          <span><b>监控采集</b> — 脚本输出 JSON 指标数组 [{"{name,value,unit}"}]，结果记录为指标点</span>
        </div>
      </div>

      {/* 视图切换 */}
      <div className="flex rounded-md border border-gray-200 dark:border-[rgba(255,255,255,0.08)] overflow-hidden w-fit">
        <button onClick={() => setTab('tasks')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors', tab === 'tasks' ? 'bg-emerald-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
          采集任务 ({tasks.length})
        </button>
        <button onClick={() => setTab('metrics')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors flex items-center gap-1', tab === 'metrics' ? 'bg-emerald-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
          <Gauge className="h-3 w-3" />指标数据 ({metrics.length})
        </button>
      </div>

      {tab === 'tasks' ? (<>
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-base flex items-center gap-2"><Activity className="h-4 w-4 text-emerald-500" />采集任务 ({tasks.length})</CardTitle></CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="py-10 text-sm text-muted-foreground text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />加载中...</p>
          ) : tasks.length === 0 ? (
            <p className="py-10 text-sm text-muted-foreground text-center">暂无采集任务，点击右上角新建</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 px-4 font-medium">名称</th>
                  <th className="pb-2 px-4 font-medium">类型</th>
                  <th className="pb-2 px-4 font-medium">周期</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">目标</th>
                  <th className="pb-2 px-4 font-medium">最近运行</th>
                  <th className="pb-2 px-4 font-medium">状态</th>
                  <th className="pb-2 px-4 font-medium w-32">操作</th>
                </tr></thead>
                <tbody>
                  {tasks.map(t => {
                    const runsFor = runs[t.id] || []
                    const lastRun = runsFor[0]
                    return (
                      <tr key={t.id} className="border-b last:border-0">
                        <td className="py-2 px-4 font-medium">{t.name}</td>
                        <td className="py-2 px-4">
                          <Badge variant={t.type === 'cmdb' ? 'info' : 'warning'} className="text-[10px]">{t.type === 'cmdb' ? 'CMDB' : '监控'}</Badge>
                        </td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{t.interval_sec}s</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{t.target_agents?.length ? `${t.target_agents.length} 台` : '全部在线'}</td>
                        <td className="py-2 px-4 text-xs">
                          {lastRun ? (
                            <span className={cn('flex items-center gap-1', lastRun.success ? 'text-emerald-600' : 'text-red-500')}>
                              {lastRun.success ? '✓' : '✗'} {formatTime(lastRun.finished_at)}
                            </span>
                          ) : <span className="text-muted-foreground">未运行</span>}
                        </td>
                        <td className="py-2 px-4">{t.enabled ? <Badge variant="success" className="text-[10px]">启用</Badge> : <Badge variant="outline" className="text-[10px]">暂停</Badge>}</td>
                        <td className="py-2 px-4">
                          <div className="flex gap-1">
                            <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => openRuns(t.id)}><History className="h-3 w-3 mr-0.5" />记录</Button>
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => handleToggle(t)} title={t.enabled ? '暂停' : '启用'}>
                              {t.enabled ? <Pause className="h-3.5 w-3.5 text-amber-500" /> : <Play className="h-3.5 w-3.5 text-emerald-500" />}
                            </Button>
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => handleDelete(t.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                          </div>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
      </>) : (
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-base flex items-center gap-2"><Gauge className="h-4 w-4 text-blue-500" />采集指标（最近 {metrics.length} 条）</CardTitle></CardHeader>
          <CardContent className="p-0">
            {metrics.length === 0 ? (
              <p className="py-10 text-sm text-muted-foreground text-center">暂无指标数据，等待 metric 类型采集任务执行</p>
            ) : (
              <div className="max-h-[480px] overflow-auto">
                <table className="w-full text-sm">
                  <thead><tr className="border-b text-left text-muted-foreground sticky top-0 bg-background">
                    <th className="pb-2 px-4 font-medium">指标名</th>
                    <th className="pb-2 px-4 font-medium">值</th>
                    <th className="pb-2 px-4 font-medium">单位</th>
                    <th className="pb-2 px-4 font-medium">Agent</th>
                    <th className="pb-2 px-4 font-medium">采集时间</th>
                  </tr></thead>
                  <tbody>
                    {metrics.map((m, i) => (
                      <tr key={i} className="border-b last:border-0">
                        <td className="py-2 px-4 font-mono text-xs">{m.name}</td>
                        <td className="py-2 px-4 font-medium">{m.value}</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{m.unit || ''}</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{m.agent_id?.slice(0, 12)}</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{formatTime(m.collected_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      {/* 新建任务弹窗 */}
      <Dialog open={dialog} onClose={() => setDialog(false)} title="新建采集任务">
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">任务名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="主机基础信息采集" /></div>
            <div><label className="text-sm font-medium">类型</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.type} onChange={e => setForm(f => ({ ...f, type: e.target.value as any }))}>
                <option value="cmdb">CMDB 采集</option>
                <option value="metric">监控指标采集</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">脚本类型</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.script_type} onChange={e => setForm(f => ({ ...f, script_type: e.target.value }))}>
                <option value="shell">Shell</option>
                <option value="python">Python</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">周期(秒)</label><Input type="number" min={10} value={form.interval_sec} onChange={e => setForm(f => ({ ...f, interval_sec: parseInt(e.target.value) || 60 }))} /></div>
          </div>
          <div>
            <label className="text-sm font-medium">采集脚本 * <span className="text-xs text-muted-foreground">({form.type === 'cmdb' ? '输出 CMDB JSON 对象' : '输出指标 JSON 数组'})</span></label>
            <textarea
              rows={5}
              value={form.script}
              onChange={e => setForm(f => ({ ...f, script: e.target.value }))}
              placeholder={form.type === 'cmdb'
                ? '#!/bin/sh\nhostname=$(hostname)\nfree -h | awk ...\necho "{\\"hostname\\":\\"$hostname\\",\\"os\\":\\"$(uname -s)\\"}"'
                : '#!/bin/sh\necho "[{\\"name\\":\\"cpu_usage\\",\\"value\\":42.5,\\"unit\\":\\"%\\"}]"'}
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono resize-none"
            />
          </div>
          <div>
            <label className="text-sm font-medium mb-1 block">目标 Agent <span className="text-muted-foreground text-xs">(留空 = 全部在线)</span></label>
            <div className="flex gap-2 flex-wrap max-h-24 overflow-y-auto">
              {agents.map(a => (
                <label key={a.id} className={cn('flex items-center gap-1.5 text-xs px-2 py-1 rounded-md border cursor-pointer', form.target_agents.includes(a.id) ? 'border-emerald-300 bg-emerald-50 text-emerald-700' : 'border-gray-200 text-muted-foreground')}>
                  <input type="checkbox" checked={form.target_agents.includes(a.id)}
                    onChange={e => setForm(f => ({ ...f, target_agents: e.target.checked ? [...f.target_agents, a.id] : f.target_agents.filter(x => x !== a.id) }))} className="accent-emerald-500" />
                  {a.name || a.id.slice(0, 8)}
                </label>
              ))}
            </div>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialog(false)}>取消</Button><Button onClick={handleCreate} disabled={saving}>{saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}创建</Button></DialogFooter>
      </Dialog>

      {/* 运行记录弹窗 */}
      <Dialog open={!!runsDialog} onClose={() => setRunsDialog(null)} title="采集运行记录">
        {runsDialog && (runs[runsDialog]?.length ? (
          <div className="space-y-2 max-h-80 overflow-y-auto">
            {runs[runsDialog].map(r => (
              <div key={r.run_id} className={cn('rounded-lg border p-3 text-xs', r.success ? 'border-emerald-200 dark:border-emerald-500/20' : 'border-red-200 dark:border-red-500/20')}>
                <div className="flex items-center justify-between">
                  <span className="font-medium">{r.agent_id.slice(0, 12)}</span>
                  <Badge variant={r.success ? 'success' : 'destructive'} className="text-[9px]">{r.success ? '成功' : '失败'}</Badge>
                </div>
                <p className="text-muted-foreground mt-1">{formatTime(r.finished_at)}</p>
                {r.metrics?.length ? <p className="mt-1 text-emerald-600">📊 {r.metrics.map(m => `${m.name}=${m.value}${m.unit || ''}`).join(' · ')}</p> : null}
                {r.error ? <p className="mt-1 text-red-500 break-all">{r.error}</p> : null}
                {r.output && r.output.length < 500 ? <pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 overflow-x-auto whitespace-pre-wrap">{r.output}</pre> : null}
              </div>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground py-6 text-center">暂无运行记录（等待调度执行）</p>
        ))}
        <DialogFooter><Button variant="outline" onClick={() => setRunsDialog(null)}>关闭</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
