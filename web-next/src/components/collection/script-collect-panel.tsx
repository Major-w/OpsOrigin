'use client'

/**
 * 脚本采集面板（Agent 侧只读脚本采集，后端 /collect-tasks）。
 *
 * 与调度采集任务的区别：这里下发的是自定义脚本，由 Agent 本地执行并回传结构化结果。
 * 按 type 切片后：cmdb 型脚本归 CMDB「自动采集」，metric 型脚本归监控中心「监控采集」。
 */

import { useCallback, useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { toast } from '@/stores/toast'
import { useAgentStore } from '@/stores/agent'
import { apiFetch, apiList, cn, formatTime } from '@/lib/utils'
import { Activity, Database, Gauge, History, Loader2, Pause, Play, Plus, Trash2 } from 'lucide-react'

export type ScriptCollectType = 'cmdb' | 'metric'

interface CollectTask {
  id: string
  name: string
  type: ScriptCollectType
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

const SCRIPT_HINT: Record<ScriptCollectType, string> = {
  cmdb: '#!/bin/sh\nhostname=$(hostname)\necho "{\\"hostname\\":\\"$hostname\\",\\"os\\":\\"$(uname -s)\\"}"',
  metric: '#!/bin/sh\necho "[{\\"name\\":\\"cpu_usage\\",\\"value\\":42.5,\\"unit\\":\\"%\\"}]"',
}

interface Props {
  /** 面板作用域：cmdb=资产字段回写，metric=指标点上报。 */
  type: ScriptCollectType
}

export function ScriptCollectPanel({ type }: Props) {
  const agents = useAgentStore(s => s.agents)
  const fetchAgents = useAgentStore(s => s.fetch)
  const [tasks, setTasks] = useState<CollectTask[]>([])
  const [runs, setRuns] = useState<Record<string, CollectRun[]>>({})
  const [loading, setLoading] = useState(false)
  const [dialog, setDialog] = useState(false)
  const [runsDialog, setRunsDialog] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const emptyForm = useMemo(() => ({
    name: '', type, script: '', script_type: 'shell',
    interval_sec: type === 'metric' ? 60 : 3600, target_agents: [] as string[], enabled: true, timeout_sec: 60,
  }), [type])
  const [form, setForm] = useState(emptyForm)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      setTasks(await apiList<CollectTask>('/collect-tasks'))
    } catch { toast.error('加载脚本采集任务失败') }
    setLoading(false)
  }, [])

  useEffect(() => { load(); fetchAgents() }, [load, fetchAgents])

  const scoped = useMemo(() => tasks.filter(t => t.type === type), [tasks, type])

  const loadRuns = async (id: string) => {
    try {
      const list = await apiList<CollectRun>(`/collect-tasks/${id}/runs`)
      setRuns(prev => ({ ...prev, [id]: list }))
    } catch { /* 运行记录取不到不影响主流程，静默 */ }
  }

  const handleCreate = async () => {
    if (!form.name.trim()) return toast.error('请输入任务名称')
    if (!form.script.trim()) return toast.error('请输入采集脚本')
    setSaving(true)
    try {
      await apiFetch('/collect-tasks', { method: 'POST', body: JSON.stringify({ ...form, type }) })
      toast.success('采集任务已创建')
      setDialog(false); setForm(emptyForm); load()
    } catch (e: any) { toast.error(e?.message || '创建失败') } finally { setSaving(false) }
  }

  const handleToggle = async (t: CollectTask) => {
    try {
      await apiFetch(`/collect-tasks/${t.id}`, { method: 'PUT', body: JSON.stringify({ ...t, enabled: !t.enabled }) })
      load()
    } catch { toast.error('更新失败') }
  }

  const handleDelete = async () => {
    if (!pendingId) return
    try { await apiFetch(`/collect-tasks/${pendingId}`, { method: 'DELETE' }); toast.success('已删除'); load() }
    catch { toast.error('删除失败') }
    setPendingId(null)
  }

  return (
    <div className="space-y-4">
      <div className={cn('flex items-center gap-2 text-xs p-3 rounded-lg border',
        type === 'cmdb'
          ? 'bg-emerald-50 dark:bg-emerald-500/5 border-emerald-200 dark:border-emerald-500/20'
          : 'bg-blue-50 dark:bg-blue-500/5 border-blue-200 dark:border-blue-500/20')}>
        {type === 'cmdb'
          ? <Database className="h-4 w-4 text-emerald-600 shrink-0" />
          : <Gauge className="h-4 w-4 text-blue-600 shrink-0" />}
        <span>
          {type === 'cmdb'
            ? <><b>资产脚本采集</b> — 脚本输出 JSON 对象（hostname/os/cpu/memory/disk/ips），结果自动回写主机配置项</>
            : <><b>指标脚本采集</b> — 脚本输出 JSON 指标数组 [{'{name,value,unit}'}]，结果落入指标时序</>}
        </span>
      </div>

      <Card>
        <CardHeader className="pb-2 flex-row items-center justify-between space-y-0">
          <CardTitle className="text-base flex items-center gap-2">
            <Activity className="h-4 w-4 text-emerald-500" />脚本采集任务 ({scoped.length})
          </CardTitle>
          <Button size="sm" onClick={() => setDialog(true)}><Plus className="h-4 w-4 mr-1" />新建脚本任务</Button>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="py-10 text-sm text-muted-foreground text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />加载中...</p>
          ) : scoped.length === 0 ? (
            <p className="py-10 text-sm text-muted-foreground text-center">暂无脚本采集任务，点击右上角新建</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 px-4 font-medium">名称</th>
                  <th className="pb-2 px-4 font-medium">脚本</th>
                  <th className="pb-2 px-4 font-medium">周期</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">目标</th>
                  <th className="pb-2 px-4 font-medium">最近运行</th>
                  <th className="pb-2 px-4 font-medium">状态</th>
                  <th className="pb-2 px-4 font-medium w-32">操作</th>
                </tr></thead>
                <tbody>
                  {scoped.map(t => {
                    const lastRun = (runs[t.id] || [])[0]
                    return (
                      <tr key={t.id} className="border-b last:border-0">
                        <td className="py-2 px-4 font-medium">{t.name}</td>
                        <td className="py-2 px-4"><Badge variant="secondary" className="text-[10px]">{t.script_type || 'shell'}</Badge></td>
                        <td className="py-2 px-4 text-muted-foreground text-xs">{t.interval_sec}s</td>
                        <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{t.target_agents?.length ? `${t.target_agents.length} 台` : '全部在线'}</td>
                        <td className="py-2 px-4 text-xs">
                          {lastRun ? (
                            <span className={cn('flex items-center gap-1', lastRun.success ? 'text-emerald-600' : 'text-red-500')}>
                              {lastRun.success ? '✓' : '✗'} {formatTime(lastRun.finished_at)}
                            </span>
                          ) : <span className="text-muted-foreground">{t.last_run_at ? formatTime(t.last_run_at) : '未运行'}</span>}
                        </td>
                        <td className="py-2 px-4">{t.enabled ? <Badge variant="success" className="text-[10px]">启用</Badge> : <Badge variant="outline" className="text-[10px]">暂停</Badge>}</td>
                        <td className="py-2 px-4">
                          <div className="flex gap-1">
                            <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => { setRunsDialog(t.id); loadRuns(t.id) }}>
                              <History className="h-3 w-3 mr-0.5" />记录
                            </Button>
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => handleToggle(t)} title={t.enabled ? '暂停' : '启用'}>
                              {t.enabled ? <Pause className="h-3.5 w-3.5 text-amber-500" /> : <Play className="h-3.5 w-3.5 text-emerald-500" />}
                            </Button>
                            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setPendingId(t.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
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

      <Dialog open={dialog} onClose={() => setDialog(false)} title="新建脚本采集任务">
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">任务名称 *</label>
              <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder={type === 'cmdb' ? '主机基础信息采集' : '主机 CPU 指标采集'} /></div>
            <div><label className="text-sm font-medium">脚本类型</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                value={form.script_type} onChange={e => setForm(f => ({ ...f, script_type: e.target.value }))}>
                <option value="shell">Shell</option>
                <option value="python">Python</option>
              </select>
            </div>
          </div>
          <div><label className="text-sm font-medium">周期(秒)</label>
            <Input type="number" min={10} value={form.interval_sec}
              onChange={e => setForm(f => ({ ...f, interval_sec: parseInt(e.target.value) || 60 }))} /></div>
          <div>
            <label className="text-sm font-medium">
              采集脚本 * <span className="text-xs text-muted-foreground">({type === 'cmdb' ? '输出 CMDB JSON 对象' : '输出指标 JSON 数组'})</span>
            </label>
            <textarea rows={5} value={form.script} onChange={e => setForm(f => ({ ...f, script: e.target.value }))}
              placeholder={SCRIPT_HINT[type]}
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-xs font-mono resize-none" />
          </div>
          <div>
            <label className="text-sm font-medium mb-1 block">目标 Agent <span className="text-muted-foreground text-xs">(留空 = 全部在线)</span></label>
            <div className="flex gap-2 flex-wrap max-h-24 overflow-y-auto">
              {agents.map(a => (
                <label key={a.id} className={cn('flex items-center gap-1.5 text-xs px-2 py-1 rounded-md border cursor-pointer',
                  form.target_agents.includes(a.id) ? 'border-emerald-300 bg-emerald-50 text-emerald-700' : 'border-gray-200 text-muted-foreground')}>
                  <input type="checkbox" checked={form.target_agents.includes(a.id)}
                    onChange={e => setForm(f => ({ ...f, target_agents: e.target.checked ? [...f.target_agents, a.id] : f.target_agents.filter(x => x !== a.id) }))}
                    className="accent-emerald-500" />
                  {a.name || a.id.slice(0, 8)}
                </label>
              ))}
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialog(false)}>取消</Button>
          <Button onClick={handleCreate} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}创建
          </Button>
        </DialogFooter>
      </Dialog>

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
                {r.metrics?.length ? <p className="mt-1 text-emerald-600">{r.metrics.map(m => `${m.name}=${m.value}${m.unit || ''}`).join(' · ')}</p> : null}
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

      <ConfirmDialog
        open={!!pendingId}
        title="删除采集任务"
        description="删除该采集任务？"
        confirmLabel="删除"
        onClose={() => setPendingId(null)}
        onConfirm={handleDelete}
      />
    </div>
  )
}
