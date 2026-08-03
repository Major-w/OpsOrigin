'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { useCollectionStore } from '@/stores/collection'
import { useCan } from '@/stores/permissions'
import {
  CHANNEL_LABELS,
  FREQUENCY_PRESETS,
  KIND_LABELS,
  RESOURCE_TYPE_LABELS,
  STATUS_LABELS,
  validateFrequency,
  type CollectionKind,
  type CollectionResourceType,
  type CollectionTask,
  type CreateCollectionTaskRequest,
} from '@/lib/collection'
import { cn, apiList } from '@/lib/utils'
import {
  Database, Activity, RefreshCw, Play, Trash2, Plus, Server, Cloud, Box,
  Loader2, CheckCircle2, XCircle, Radio, Settings2, Bookmark,
} from 'lucide-react'

const FREQ_OPTIONS = FREQUENCY_PRESETS.map(f => ({ value: f.value, label: f.label }))

const RESOURCE_ICONS: Record<CollectionResourceType, typeof Cloud> = {
  cloud: Cloud,
  host: Server,
  container: Box,
}

export default function CollectionPolicyPage() {
  const { tasks, loading, fetchTasks, updateTask, deleteTask, runTask, createTask } =
    useCollectionStore()

  const canDelete = useCan('cmdb:delete')
  const [kindFilter, setKindFilter] = useState<'all' | CollectionKind>('all')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [savedQueries, setSavedQueries] = useState<{ id: string; name: string }[]>([])
  const [form, setForm] = useState<{
    name: string; resource_type: CollectionResourceType; target: string; kind: CollectionKind; frequency: string; enabled: boolean; target_mode: 'static' | 'query'; target_query: string
  }>({ name: '', resource_type: 'cloud', target: '', kind: 'cmdb', frequency: '1h', enabled: true, target_mode: 'static', target_query: '' })

  useEffect(() => {
    fetchTasks()
    loadSavedQueries()
  }, [fetchTasks])

  const loadSavedQueries = async () => {
    try { setSavedQueries(await apiList<{ id: string; name: string }>('/cmdb/saved-queries')) } catch { setSavedQueries([]) }
  }

  const filtered = kindFilter === 'all' ? tasks : tasks.filter(t => t.kind === kindFilter)
  const stats = useMemo(() => ({
    total: tasks.length,
    enabled: tasks.filter(t => t.enabled).length,
    cmdb: tasks.filter(t => t.kind === 'cmdb').length,
    monitor: tasks.filter(t => t.kind === 'monitor').length,
    failed: tasks.filter(t => t.status === 'failed').length,
  }), [tasks])

  const handleCreate = async () => {
    const req: CreateCollectionTaskRequest = {
      name: form.name || undefined,
      resource_type: form.resource_type,
      target: form.target.trim(),
      kind: form.kind,
      frequency: form.frequency,
      enabled: form.enabled,
    }
    if (form.target_mode === 'query') {
      req.target_mode = 'query'
      req.target_query = form.target_query
    }
    const ok = await createTask(req)
    if (ok) {
      setDialogOpen(false)
      setForm({ name: '', resource_type: 'cloud', target: '', kind: 'cmdb', frequency: '1h', enabled: true, target_mode: 'static', target_query: '' })
    }
  }

  const statusBadge = (t: CollectionTask) => {
    const map: Record<string, { v: any; icon?: any }> = {
      success: { v: 'success', icon: CheckCircle2 },
      failed: { v: 'destructive', icon: XCircle },
      running: { v: 'info', icon: Loader2 },
      pending: { v: 'warning' },
      idle: { v: 'outline' },
    }
    const cfg = map[t.status] || { v: 'outline' }
    const Icon = cfg.icon
    return (
      <Badge variant={cfg.v} title={t.error || undefined}>
        {Icon && <Icon className={cn('h-3 w-3', t.status === 'running' && 'animate-spin')} />}
        {STATUS_LABELS[t.status] || t.status}
      </Badge>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">采集任务</h1>
          <p className="text-sm text-muted-foreground">
            全量采集任务调度总览。CMDB 是唯一调度者；全局默认采集策略（资源接入默认值）在「资源纳管」页配置，纳管时可逐资源覆盖。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => fetchTasks()} disabled={loading}>
            <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
          </Button>
          <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建采集任务</Button>
        </div>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[
          { n: stats.total, label: '采集任务总数', icon: Settings2, color: 'emerald' },
          { n: stats.enabled, label: '已启用', icon: Radio, color: 'blue' },
          { n: stats.cmdb, label: 'CMDB 采集', icon: Database, color: 'violet' },
          { n: stats.failed, label: '最近失败', icon: XCircle, color: 'red' },
        ].map(s => (
          <Card key={s.label}><CardContent className="p-4 flex items-center gap-3">
            <div className={cn('h-10 w-10 rounded-lg flex items-center justify-center',
              s.color === 'emerald' && 'bg-emerald-100 dark:bg-emerald-500/20',
              s.color === 'blue' && 'bg-blue-100 dark:bg-blue-500/20',
              s.color === 'violet' && 'bg-violet-100 dark:bg-violet-500/20',
              s.color === 'red' && 'bg-red-100 dark:bg-red-500/20')}>
              <s.icon className={cn('h-5 w-5',
                s.color === 'emerald' && 'text-emerald-600 dark:text-emerald-400',
                s.color === 'blue' && 'text-blue-600 dark:text-blue-400',
                s.color === 'violet' && 'text-violet-600 dark:text-violet-400',
                s.color === 'red' && 'text-red-600 dark:text-red-400')} />
            </div>
            <div><p className="text-2xl font-bold">{s.n}</p><p className="text-xs text-muted-foreground">{s.label}</p></div>
          </CardContent></Card>
        ))}
      </div>

      {/* 采集任务总览 */}
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle className="text-sm flex items-center gap-2"><Database className="h-4 w-4" />采集任务总览 ({filtered.length})</CardTitle>
            <CardDescription>纳管时自动创建，也可手动新增。channel=agent 的任务会自动投影到对应 Agent 执行。</CardDescription>
          </div>
          <div className="flex gap-1">
            {(['all', 'cmdb', 'monitor'] as const).map(k => (
              <button
                key={k}
                onClick={() => setKindFilter(k)}
                className={cn('px-3 py-1 text-xs rounded-md border transition-colors',
                  kindFilter === k
                    ? 'border-emerald-500 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400'
                    : 'border-gray-200 dark:border-gray-700 text-muted-foreground hover:border-gray-300')}
              >
                {k === 'all' ? `全部 ${stats.total}` : k === 'cmdb' ? `CMDB ${stats.cmdb}` : `监控 ${stats.monitor}`}
              </button>
            ))}
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <div className="text-center py-10 text-muted-foreground text-xs"><Loader2 className="h-5 w-5 animate-spin mx-auto mb-2" />加载中...</div>
          ) : filtered.length === 0 ? (
            <div className="text-center py-12 text-muted-foreground">
              <Database className="h-12 w-12 mx-auto mb-3 opacity-30" />
              <p className="text-sm">暂无采集任务</p>
              <p className="text-xs mt-1">在「资源纳管」中勾选采集开关，或点击右上角手动新建</p>
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left text-muted-foreground">
                    <th className="font-medium px-4 py-2">任务名称</th>
                    <th className="font-medium px-4 py-2">资源类型</th>
                    <th className="font-medium px-4 py-2">采集目标</th>
                    <th className="font-medium px-4 py-2">类别</th>
                    <th className="font-medium px-4 py-2">通道</th>
                    <th className="font-medium px-4 py-2">频率</th>
                    <th className="font-medium px-4 py-2">上次执行</th>
                    <th className="font-medium px-4 py-2">状态</th>
                    <th className="font-medium px-4 py-2">启用</th>
                    <th className="font-medium px-4 py-2 text-right">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(t => {
                    const Icon = RESOURCE_ICONS[t.resource_type] || Server
                    return (
                      <tr key={t.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-gray-800/50">
                        <td className="px-4 py-3 font-medium">{t.name}</td>
                        <td className="px-4 py-3">
                          <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
                            <Icon className="h-3.5 w-3.5" />{RESOURCE_TYPE_LABELS[t.resource_type] || t.resource_type}
                          </span>
                        </td>
                        <td className="px-4 py-3 text-xs">
                          {t.target_mode === 'query' ? (
                            <span className="inline-flex items-center gap-1 text-violet-600 dark:text-violet-400">
                              <Bookmark className="h-3 w-3" />
                              {savedQueries.find(s => s.id === t.target_query)?.name || '查询策略(已删除)'}
                            </span>
                          ) : (
                            <span className="font-mono text-muted-foreground">{t.target}</span>
                          )}
                        </td>
                        <td className="px-4 py-3">
                          <Badge variant={t.kind === 'cmdb' ? 'info' : 'warning'}>
                            {t.kind === 'cmdb' ? <Database className="h-3 w-3" /> : <Activity className="h-3 w-3" />}
                            {KIND_LABELS[t.kind] || t.kind}
                          </Badge>
                        </td>
                        <td className="px-4 py-3">
                          <Badge variant="secondary">{CHANNEL_LABELS[t.channel] || t.channel || 'server'}</Badge>
                        </td>
                        <td className="px-4 py-3 text-xs font-mono">{t.frequency}</td>
                        <td className="px-4 py-3 text-xs text-muted-foreground">
                          {t.last_run_at ? t.last_run_at.slice(0, 19).replace('T', ' ') : '从未执行'}
                        </td>
                        <td className="px-4 py-3">{statusBadge(t)}</td>
                        <td className="px-4 py-3">
                          <Switch size="sm" checked={t.enabled} onCheckedChange={v => updateTask(t.id, { enabled: v })} />
                        </td>
                        <td className="px-4 py-3">
                          <div className="flex items-center justify-end gap-1">
                            <Button
                              variant="ghost" size="sm" className="h-7 w-7 p-0"
                              title="立即执行一次"
                              onClick={() => runTask(t.id)}
                              disabled={t.status === 'running'}
                            >
                              {t.status === 'running'
                                ? <Loader2 className="h-3.5 w-3.5 animate-spin" />
                                : <Play className="h-3.5 w-3.5" />}
                            </Button>
                            {canDelete && (
                              <Button
                                variant="ghost" size="sm" className="h-7 w-7 p-0 text-red-500 hover:text-red-600"
                                title="删除任务"
                                onClick={() => { if (confirm(`确认删除采集任务「${t.name}」？`)) deleteTask(t.id) }}
                              >
                                <Trash2 className="h-3.5 w-3.5" />
                              </Button>
                            )}
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

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建采集任务">
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">任务名称</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="留空则自动生成" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">资源类型 *</label>
              <Select
                className="mt-1"
                value={form.resource_type}
                options={(Object.keys(RESOURCE_TYPE_LABELS) as CollectionResourceType[]).map(k => ({ value: k, label: RESOURCE_TYPE_LABELS[k] }))}
                onChange={e => setForm(f => ({ ...f, resource_type: e.target.value as CollectionResourceType }))}
              />
            </div>
            <div>
              <label className="text-sm font-medium">采集类别 *</label>
              <Select
                className="mt-1"
                value={form.kind}
                options={(Object.keys(KIND_LABELS) as CollectionKind[]).map(k => ({ value: k, label: KIND_LABELS[k] }))}
                onChange={e => setForm(f => ({
                  ...f,
                  kind: e.target.value as CollectionKind,
                  frequency: e.target.value === 'monitor' ? '5m' : '1h',
                }))}
              />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">执行目标模式</label>
            <div className="flex gap-2 mt-1">
              <button
                type="button"
                onClick={() => setForm(f => ({ ...f, target_mode: 'static', target_query: '' }))}
                className={cn('flex-1 px-3 py-2 text-xs rounded-md border transition-colors',
                  form.target_mode === 'static'
                    ? 'border-emerald-500 bg-emerald-50 text-emerald-700 dark:bg-emerald-500/10 dark:text-emerald-400'
                    : 'border-gray-200 dark:border-gray-700 text-muted-foreground hover:border-gray-300')}
              >
                静态目标
              </button>
              <button
                type="button"
                onClick={() => setForm(f => ({ ...f, target_mode: 'query' }))}
                className={cn('flex-1 px-3 py-2 text-xs rounded-md border transition-colors',
                  form.target_mode === 'query'
                    ? 'border-violet-500 bg-violet-50 text-violet-700 dark:bg-violet-500/10 dark:text-violet-400'
                    : 'border-gray-200 dark:border-gray-700 text-muted-foreground hover:border-gray-300')}
              >
                查询策略（动态目标）
              </button>
            </div>
            <p className="text-xs text-muted-foreground mt-1">
              {form.target_mode === 'query'
                ? '按保存的查询（"我想查"）实时解析命中的资产集合，调度时自动对每个命中资产下发采集。'
                : '指向某个已纳管资源，格式 <code>&lt;类型&gt;:&lt;标识&gt;</code>，如 cloud:aliyun / host:&lt;agent-id&gt;。'}
            </p>
          </div>
          {form.target_mode === 'query' ? (
            <div>
              <label className="text-sm font-medium">选择查询策略 *</label>
              <Select
                className="mt-1"
                value={form.target_query}
                options={savedQueries.map(s => ({ value: s.id, label: s.name }))}
                onChange={e => setForm(f => ({ ...f, target_query: e.target.value }))}
              />
              {savedQueries.length === 0 && (
                <p className="text-xs text-amber-600 mt-1">暂无保存的查询策略，请先在「CMDB 资产」页保存查询后回来刷新。</p>
              )}
            </div>
          ) : (
            <div>
              <label className="text-sm font-medium">采集目标 *</label>
              <Input
                value={form.target}
                onChange={e => setForm(f => ({ ...f, target: e.target.value }))}
                placeholder="cloud:aliyun / host:&lt;agent-id&gt;"
                className="font-mono"
              />
            </div>
          )}
          <div>
            <label className="text-sm font-medium">采集频率 *</label>
            <div className="flex gap-2 mt-1">
              <Input value={form.frequency} onChange={e => setForm(f => ({ ...f, frequency: e.target.value }))} className="font-mono" />
              <Select
                className="w-32" value=""
                options={[{ value: '', label: '预设' }, ...FREQ_OPTIONS]}
                onChange={e => e.target.value && setForm(f => ({ ...f, frequency: e.target.value }))}
              />
            </div>
          </div>
          <label className="text-sm font-medium flex items-center gap-2">
            <input type="checkbox" checked={form.enabled} onChange={e => setForm(f => ({ ...f, enabled: e.target.checked }))} className="rounded" />
            创建后立即启用
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button
            onClick={handleCreate}
            disabled={
              !!validateFrequency(form.frequency) ||
              (form.target_mode === 'query' ? !form.target_query : !form.target.trim())
            }
          >创建</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
