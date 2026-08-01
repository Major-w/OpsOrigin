'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Switch, SwitchField } from '@/components/ui/switch'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { useCollectionStore } from '@/stores/collection'
import { useCan } from '@/stores/permissions'
import {
  CHANNEL_LABELS,
  DEFAULT_PLATFORM_POLICY,
  FREQUENCY_PRESETS,
  KIND_LABELS,
  RESOURCE_TYPE_LABELS,
  STATUS_LABELS,
  validateFrequency,
  type CollectionKind,
  type CollectionResourceType,
  type CollectionTask,
  type PlatformPolicy,
} from '@/lib/collection'
import { cn } from '@/lib/utils'
import {
  Database, Activity, Save, RefreshCw, Play, Trash2, Plus, Server, Cloud, Box,
  Loader2, CheckCircle2, XCircle, Radio, Settings2,
} from 'lucide-react'

const FREQ_OPTIONS = FREQUENCY_PRESETS.map(f => ({ value: f.value, label: f.label }))

const RESOURCE_ICONS: Record<CollectionResourceType, typeof Cloud> = {
  cloud: Cloud,
  host: Server,
  container: Box,
}

export default function CollectionPolicyPage() {
  const { policy, tasks, loading, saving, fetchPolicy, savePolicy, fetchTasks, updateTask, deleteTask, runTask, createTask } =
    useCollectionStore()

  const [draft, setDraft] = useState<PlatformPolicy>(DEFAULT_PLATFORM_POLICY)
  const canDelete = useCan('cmdb:delete')
  const [kindFilter, setKindFilter] = useState<'all' | CollectionKind>('all')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<{
    name: string; resource_type: CollectionResourceType; target: string; kind: CollectionKind; frequency: string; enabled: boolean
  }>({ name: '', resource_type: 'cloud', target: '', kind: 'cmdb', frequency: '1h', enabled: true })

  useEffect(() => {
    fetchPolicy(true)
    fetchTasks()
  }, [fetchPolicy, fetchTasks])

  useEffect(() => { setDraft(policy) }, [policy])

  const dirty = useMemo(() =>
    draft.default_cmdb_enabled !== policy.default_cmdb_enabled ||
    draft.default_monitor_enabled !== policy.default_monitor_enabled ||
    draft.cmdb_default_frequency !== policy.cmdb_default_frequency ||
    draft.monitor_default_frequency !== policy.monitor_default_frequency,
  [draft, policy])

  const cmdbFreqErr = validateFrequency(draft.cmdb_default_frequency)
  const monitorFreqErr = validateFrequency(draft.monitor_default_frequency)

  const filtered = kindFilter === 'all' ? tasks : tasks.filter(t => t.kind === kindFilter)
  const stats = useMemo(() => ({
    total: tasks.length,
    enabled: tasks.filter(t => t.enabled).length,
    cmdb: tasks.filter(t => t.kind === 'cmdb').length,
    monitor: tasks.filter(t => t.kind === 'monitor').length,
    failed: tasks.filter(t => t.status === 'failed').length,
  }), [tasks])

  const handleSave = async () => {
    if (cmdbFreqErr || monitorFreqErr) return
    await savePolicy(draft)
  }

  const handleCreate = async () => {
    const ok = await createTask({
      name: form.name || undefined,
      resource_type: form.resource_type,
      target: form.target.trim(),
      kind: form.kind,
      frequency: form.frequency,
      enabled: form.enabled,
    })
    if (ok) {
      setDialogOpen(false)
      setForm({ name: '', resource_type: 'cloud', target: '', kind: 'cmdb', frequency: '1h', enabled: true })
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
          <h1 className="text-2xl font-bold">采集策略</h1>
          <p className="text-sm text-muted-foreground">
            平台全局默认采集行为 + 全量采集任务调度总览。CMDB 是唯一调度者，纳管时可逐资源覆盖默认值。
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={() => { fetchPolicy(true); fetchTasks() }} disabled={loading}>
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

      {/* 全局默认策略 */}
      <Card>
        <CardHeader>
          <CardTitle className="text-sm flex items-center gap-2"><Settings2 className="h-4 w-4" />全局默认策略</CardTitle>
          <CardDescription>
            新纳管资源的默认采集行为。资源纳管表单的勾选框会预填这里的值，可针对单个资源覆盖。
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <div className="space-y-3">
              <SwitchField
                label="默认开启 CMDB 采集"
                description="CMDB 是平台基石，建议保持开启：新资源纳管后自动创建 cmdb 类采集任务，周期性写入配置项。"
                checked={draft.default_cmdb_enabled}
                onCheckedChange={v => setDraft(d => ({ ...d, default_cmdb_enabled: v }))}
              />
              <div>
                <label className="text-xs font-medium text-muted-foreground">CMDB 采集默认频率</label>
                <div className="flex gap-2 mt-1">
                  <Input
                    value={draft.cmdb_default_frequency}
                    onChange={e => setDraft(d => ({ ...d, cmdb_default_frequency: e.target.value }))}
                    placeholder="1h"
                    className={cn('font-mono', cmdbFreqErr && 'border-red-500')}
                  />
                  <Select
                    className="w-32"
                    value=""
                    options={[{ value: '', label: '预设' }, ...FREQ_OPTIONS]}
                    onChange={e => e.target.value && setDraft(d => ({ ...d, cmdb_default_frequency: e.target.value }))}
                  />
                </div>
                {cmdbFreqErr && <p className="text-xs text-red-500 mt-1">{cmdbFreqErr}</p>}
              </div>
            </div>

            <div className="space-y-3">
              <SwitchField
                label="默认开启监控采集"
                description="默认关闭：监控采集频率高、资源开销大，建议按需为关键资源单独开启。"
                checked={draft.default_monitor_enabled}
                onCheckedChange={v => setDraft(d => ({ ...d, default_monitor_enabled: v }))}
              />
              <div>
                <label className="text-xs font-medium text-muted-foreground">监控采集默认频率</label>
                <div className="flex gap-2 mt-1">
                  <Input
                    value={draft.monitor_default_frequency}
                    onChange={e => setDraft(d => ({ ...d, monitor_default_frequency: e.target.value }))}
                    placeholder="5m"
                    className={cn('font-mono', monitorFreqErr && 'border-red-500')}
                  />
                  <Select
                    className="w-32"
                    value=""
                    options={[{ value: '', label: '预设' }, ...FREQ_OPTIONS]}
                    onChange={e => e.target.value && setDraft(d => ({ ...d, monitor_default_frequency: e.target.value }))}
                  />
                </div>
                {monitorFreqErr && <p className="text-xs text-red-500 mt-1">{monitorFreqErr}</p>}
              </div>
            </div>
          </div>

          <div className="flex items-center justify-between pt-1 border-t border-gray-100 dark:border-gray-800">
            <p className="text-xs text-muted-foreground pt-3">
              {dirty ? '有未保存的更改' : '策略已是最新'}
              {policy.updated_at && !dirty && ` · 更新于 ${policy.updated_at.slice(0, 19).replace('T', ' ')}`}
            </p>
            <div className="flex gap-2 pt-3">
              {dirty && <Button variant="outline" onClick={() => setDraft(policy)}>撤销</Button>}
              <Button onClick={handleSave} disabled={!dirty || saving || !!cmdbFreqErr || !!monitorFreqErr}>
                {saving ? <Loader2 className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}保存策略
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

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
                        <td className="px-4 py-3 text-xs font-mono text-muted-foreground">{t.target}</td>
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
                  frequency: e.target.value === 'monitor' ? policy.monitor_default_frequency : policy.cmdb_default_frequency,
                }))}
              />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">采集目标 *</label>
            <Input
              value={form.target}
              onChange={e => setForm(f => ({ ...f, target: e.target.value }))}
              placeholder="cloud:aliyun / host:&lt;agent-id&gt;"
              className="font-mono"
            />
            <p className="text-xs text-muted-foreground mt-1">指向某个已纳管资源，格式 <code>&lt;类型&gt;:&lt;标识&gt;</code></p>
          </div>
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
          <SwitchField
            label="创建后立即启用"
            checked={form.enabled}
            onCheckedChange={v => setForm(f => ({ ...f, enabled: v }))}
          />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleCreate} disabled={!form.target.trim() || !!validateFrequency(form.frequency)}>创建</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
