// 动作库管理：既支持「复制为快照」（配置期预设），也支持流程节点「运行期引用」。
// 保存只改草稿；「发布」追加不可变版本，version=0 的流程引用立即切到新版本。
// 协议对齐 internal/itsm/assets：action_type 必须落在内置动作白名单内。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Plus, RefreshCw, Search, Trash2, Pencil, Zap, Link2, Rocket, History } from 'lucide-react'
import { Button, Input, Modal, Select, Switch, Tag, Tooltip } from 'antd'
import { apiFetch, apiList, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { formatDateTime } from '@/lib/itsm-constants'

interface ActionTemplate {
  id: string
  name: string
  description?: string
  action_type: string
  inputs: Record<string, any>
  enabled: boolean
  status?: 'draft' | 'published'
  version?: number
  used_by?: string[]
  created_at?: string
  updated_at?: string
}

interface ActionTemplateVersion {
  id: string
  version: number
  action_type: string
  inputs: Record<string, any>
  note?: string
  created_by?: string
  created_at?: string
}

/** 与流程设计器 ACTION_DEFS / 后端 assets.BuiltinActionTypes 一一对齐 */
const ACTION_TYPES: { value: string; label: string; hint: string; keys: string[] }[] = [
  { value: 'http', label: 'HTTP 请求', hint: 'url / body / headers 支持 ${...} 变量；输出 status_code、body', keys: ['method', 'url', 'body', 'headers'] },
  { value: 'notify', label: '通知分发', hint: '按事件订阅策略分发；title / content 支持 ${...}', keys: ['event', 'title', 'content'] },
  { value: 'create_ticket', label: '创建工单', hint: '保留字段之外的额外参数写入新工单 custom_fields', keys: ['title', 'description', 'risk_level', 'change_plan'] },
  { value: 'script', label: '执行脚本', hint: '走动作库 script 通道；除 script_id 外的参数作为脚本入参', keys: ['script_id'] },
  { value: 'cmdb_update', label: '更新 CMDB 配置项', hint: 'fields 为属性键值对，值支持 ${...}', keys: ['ci_id', 'fields'] },
]
const actionLabel = (t: string) => ACTION_TYPES.find(a => a.value === t)?.label || t

/** 值以 { 开头时按 JSON 对象解析（headers / fields 这类嵌套参数）；解析失败按字符串保存。 */
function toStoredValue(raw: string): any {
  const v = raw.trim()
  if (v.startsWith('{')) {
    try { return JSON.parse(v) } catch { /* 按字符串保存 */ }
  }
  return v
}
function toEditableValue(v: any): string {
  if (v && typeof v === 'object') return JSON.stringify(v)
  return v == null ? '' : String(v)
}
const emptyKV = (): [string, string] => ['', '']

export default function ActionTemplatesPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [keyword, setKeyword] = useState(searchParams.get('q') || '')
  const [typeFilter, setTypeFilter] = useState(searchParams.get('type') || '')
  const [items, setItems] = useState<ActionTemplate[]>([])
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<{
    name: string; description: string; action_type: string; rows: [string, string][]; enabled: boolean
  }>({ name: '', description: '', action_type: 'http', rows: [emptyKV()], enabled: true })
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ActionTemplate | null>(null)
  const [publishTarget, setPublishTarget] = useState<ActionTemplate | null>(null)
  const [versionsFor, setVersionsFor] = useState<ActionTemplate | null>(null)
  const [versions, setVersions] = useState<ActionTemplateVersion[]>([])
  const [versionsLoading, setVersionsLoading] = useState(false)
  const [busy, setBusy] = useState(false)

  const updateParams = (patch: Record<string, string>) => {
    const next = new URLSearchParams(searchParams)
    for (const [k, v] of Object.entries(patch)) {
      if (v) next.set(k, v)
      else next.delete(k)
    }
    setSearchParams(next, { replace: true })
  }

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      setItems((await apiList<ActionTemplate>('/itsm/action-templates')) || [])
    } catch (e: any) {
      setLoadError(e.message || '加载动作模板失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const filtered = useMemo(() => items.filter(t => {
    if (typeFilter && t.action_type !== typeFilter) return false
    return !keyword ||
      t.name?.toLowerCase().includes(keyword.toLowerCase()) ||
      t.description?.toLowerCase().includes(keyword.toLowerCase())
  }), [items, keyword, typeFilter])

  const openCreate = () => {
    setEditingId(null)
    setForm({ name: '', description: '', action_type: 'http', rows: [emptyKV()], enabled: true })
    setDialogOpen(true)
  }

  const openEdit = (t: ActionTemplate) => {
    setEditingId(t.id)
    const rows: [string, string][] = Object.entries(t.inputs || {}).map(([k, v]) => [k, toEditableValue(v)])
    setForm({
      name: t.name || '', description: t.description || '',
      action_type: t.action_type || 'http',
      rows: rows.length ? rows : [emptyKV()], enabled: t.enabled !== false,
    })
    setDialogOpen(true)
  }

  const patchRow = (i: number, patch: Partial<{ key: string; value: string }>) => {
    setForm(f => ({
      ...f,
      rows: f.rows.map((r, j) => j === i
        ? [patch.key !== undefined ? patch.key : r[0], patch.value !== undefined ? patch.value : r[1]] as [string, string]
        : r),
    }))
  }

  const applyKeyHints = () => {
    const def = ACTION_TYPES.find(a => a.value === form.action_type)
    if (!def) return
    const existing = new Set(form.rows.map(r => r[0]))
    const additions = def.keys.filter(k => !existing.has(k)).map(k => [k, ''] as [string, string])
    setForm(f => ({ ...f, rows: [...f.rows.filter(r => r[0].trim()), ...additions] }))
  }

  const save = async () => {
    if (!form.name.trim()) { getMessage().error('请填写模板名称'); return }
    if (!ACTION_TYPES.some(a => a.value === form.action_type)) { getMessage().error('请选择内置动作类型'); return }
    const seen = new Set<string>()
    const inputs: Record<string, any> = {}
    for (const [k, v] of form.rows) {
      const key = k.trim()
      if (!key) continue
      if (seen.has(key)) { getMessage().error(`参数名「${key}」重复`); return }
      seen.add(key)
      inputs[key] = toStoredValue(v)
    }
    setSaving(true)
    try {
      await apiFetch('/itsm/action-templates', {
        method: 'POST',
        body: JSON.stringify({
          id: editingId || undefined,
          name: form.name.trim(),
          description: form.description.trim(),
          action_type: form.action_type,
          inputs,
          enabled: form.enabled,
        }),
      })
      getMessage().success(editingId ? '更新成功' : '创建成功')
      setDialogOpen(false)
      load()
    } catch (e: any) {
      getMessage().error(`保存失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const toggleEnabled = async (t: ActionTemplate, enabled: boolean) => {
    setBusy(true)
    try {
      await apiFetch('/itsm/action-templates', {
        method: 'POST',
        body: JSON.stringify({
          id: t.id, name: t.name, description: t.description || '',
          action_type: t.action_type, inputs: t.inputs || {}, enabled,
        }),
      })
      getMessage().success(enabled ? '已启用' : '已停用')
      load()
    } catch (e: any) {
      getMessage().error(`操作失败: ${e.message}`)
    } finally {
      setBusy(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    setBusy(true)
    try {
      await apiFetch(`/itsm/action-templates/${deleteTarget.id}`, { method: 'DELETE' })
      getMessage().success('删除成功')
      load()
    } catch (e: any) {
      getMessage().error(`删除失败: ${e.message}`)
    } finally {
      setBusy(false)
      setDeleteTarget(null)
    }
  }

  const confirmPublish = async () => {
    if (!publishTarget) return
    setBusy(true)
    try {
      await apiFetch(`/itsm/action-templates/${publishTarget.id}/publish`, {
        method: 'POST', body: JSON.stringify({ note: '' }),
      })
      getMessage().success(`已发布 v${(publishTarget.version || 0) + 1}，跟随最新版的流程立即生效`)
      load()
    } catch (e: any) {
      getMessage().error(`发布失败: ${e.message}`)
    } finally {
      setBusy(false)
      setPublishTarget(null)
    }
  }

  const openVersions = async (t: ActionTemplate) => {
    setVersionsFor(t)
    setVersionsLoading(true)
    try {
      setVersions((await apiList<ActionTemplateVersion>(`/itsm/action-templates/${t.id}/versions`)) || [])
    } catch (e: any) {
      getMessage().error(`加载版本历史失败: ${e.message}`)
    } finally {
      setVersionsLoading(false)
    }
  }

  const rollback = async (v: ActionTemplateVersion) => {
    if (!versionsFor) return
    setBusy(true)
    try {
      await apiFetch(`/itsm/action-templates/${versionsFor.id}/rollback`, {
        method: 'POST', body: JSON.stringify({ version: v.version }),
      })
      getMessage().success(`已用 v${v.version} 的内容发布新版本`)
      load()
    } catch (e: any) {
      getMessage().error(`回滚失败: ${e.message}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold">动作库</h1>
          <p className="text-sm text-muted-foreground mt-1">
            流程服务任务的参数预设。发布后可在设计器「引用模板」（运行期跟随版本）或「复制为快照」（配置期解耦）
          </p>
        </div>
        <Button type="primary" onClick={openCreate}><Plus className="w-4 h-4 mr-1" />新建模板</Button>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input placeholder="搜索模板名称或描述..." value={keyword} className="pl-9"
            onChange={e => { setKeyword(e.target.value); updateParams({ q: e.target.value }) }} />
        </div>
        <Select allowClear placeholder="全部动作类型" style={{ width: 180 }} value={typeFilter || undefined}
          onChange={v => { const next = v || ''; setTypeFilter(next); updateParams({ type: next }) }}
          options={ACTION_TYPES.map(a => ({ value: a.value, label: a.label }))} />
        <Button onClick={load} disabled={loading}>
          <RefreshCw className={cn('w-4 h-4 mr-1', loading && 'animate-spin')} />刷新
        </Button>
      </div>

      {loadError ? (
        <ErrorState message={`加载动作模板失败: ${loadError}`} onRetry={load} />
      ) : loading ? (
        <div className="text-center py-12 text-muted-foreground">加载中...</div>
      ) : filtered.length === 0 ? (
        <div className="border rounded-lg py-12 text-center">
          <Zap className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
          <p className="text-muted-foreground">{(keyword || typeFilter) ? '无匹配的模板' : '暂无动作模板，新建后可在流程设计器中载入'}</p>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map(t => (
            <div key={t.id} className="border rounded-lg p-4 bg-card hover:shadow-md transition-shadow">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <h3 className="text-base font-semibold truncate">{t.name}</h3>
                    <Tag color="geekblue">{actionLabel(t.action_type)}</Tag>
                    {t.status === 'published'
                      ? <Tag color="green">v{t.version || 1}</Tag>
                      : <Tag color="orange">草稿·未发布</Tag>}
                    {!t.enabled && <Tag>已停用</Tag>}
                  </div>
                  {t.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-1">{t.description}</p>}
                </div>
                <div className="flex items-center gap-0.5 shrink-0">
                  <Tooltip title={t.enabled ? '停用' : '启用'}>
                    <Switch size="small" checked={t.enabled !== false} disabled={busy}
                      onChange={v => toggleEnabled(t, v)} />
                  </Tooltip>
                  <Tooltip title="发布新版本"><Button type="text" size="small" disabled={busy || !t.enabled}
                    onClick={() => setPublishTarget(t)} aria-label="发布">
                    <Rocket className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="版本历史"><Button type="text" size="small" disabled={busy}
                    onClick={() => openVersions(t)} aria-label="版本历史">
                    <History className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="编辑"><Button type="text" size="small" onClick={() => openEdit(t)} aria-label="编辑">
                    <Pencil className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="删除"><Button type="text" size="small" danger
                    onClick={() => setDeleteTarget(t)} aria-label="删除">
                    <Trash2 className="w-3.5 h-3.5" /></Button></Tooltip>
                </div>
              </div>
              <div className="mt-3 space-y-1">
                {Object.entries(t.inputs || {}).slice(0, 3).map(([k, v]) => (
                  <div key={k} className="flex items-center gap-1.5 text-xs">
                    <Tag className="text-[10px]" color="default">{k}</Tag>
                    <span className="truncate text-muted-foreground font-mono">{toEditableValue(v) || '—'}</span>
                  </div>
                ))}
                {Object.keys(t.inputs || {}).length > 3 && (
                  <p className="text-xs text-muted-foreground">+{Object.keys(t.inputs || {}).length - 3} 个参数</p>
                )}
                {Object.keys(t.inputs || {}).length === 0 && <p className="text-xs text-muted-foreground">未预设参数</p>}
              </div>
              {(t.used_by?.length || 0) > 0 && (
                <Tooltip title={`被引用：${t.used_by!.join('、')}`}>
                  <Tag color="blue" className="mt-2">被 {t.used_by!.length} 处流程步骤引用</Tag>
                </Tooltip>
              )}
              {t.updated_at && <p className="text-[11px] text-muted-foreground mt-2">{formatDateTime(t.updated_at)}</p>}
            </div>
          ))}
        </div>
      )}

      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} footer={null}
        title={editingId ? '编辑动作模板' : '新建动作模板'} width={720}>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">模板名称 <span className="text-red-500">*</span></label>
              <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="如：变更后自动建单" />
            </div>
            <div>
              <label className="text-sm font-medium">描述</label>
              <Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                placeholder="用途说明（可选）" />
            </div>
          </div>

          <div>
            <label className="text-sm font-medium">动作类型 <span className="text-red-500">*</span></label>
            <Select className="w-full mt-1" value={form.action_type}
              onChange={v => setForm(f => ({ ...f, action_type: v }))}
              options={ACTION_TYPES.map(a => ({ value: a.value, label: a.label }))} />
            <p className="text-[11px] text-muted-foreground mt-1">
              {ACTION_TYPES.find(a => a.value === form.action_type)?.hint}
            </p>
          </div>

          <div className="flex items-center gap-2">
            <label className="text-sm font-medium">启用</label>
            <Switch size="small" checked={form.enabled} onChange={v => setForm(f => ({ ...f, enabled: v }))} />
            <span className="text-[11px] text-muted-foreground">停用后不出现在流程设计器的「从模板载入」列表</span>
          </div>

          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">预设参数（写入 step.inputs）</label>
              <div className="flex gap-2">
                <Button size="small" onClick={applyKeyHints}><Link2 className="w-3 h-3 mr-1" />插入常用参数名</Button>
                <Button size="small" onClick={() => setForm(f => ({ ...f, rows: [...f.rows, emptyKV()] }))}>
                  <Plus className="w-3.5 h-3.5 mr-1" />添加参数
                </Button>
              </div>
            </div>
            <p className="text-[11px] text-muted-foreground mb-2">
              值支持 {'${ticket.number}'} / {'${steps.<节点ID>.result.body}'} 等变量；headers、fields 等嵌套对象可直接填 JSON（以 {'{'} 开头）。
            </p>
            <div className="space-y-2">
              {form.rows.map(([k, v], i) => (
                <div key={i} className="flex items-center gap-2">
                  <Input className="w-40" size="small" placeholder="参数名" value={k}
                    onChange={e => patchRow(i, { key: e.target.value })} />
                  <Input className="flex-1 font-mono" size="small" placeholder="值或 JSON 对象" value={v}
                    onChange={e => patchRow(i, { value: e.target.value })} />
                  <Button type="text" size="small" danger disabled={form.rows.length <= 1}
                    onClick={() => setForm(f => ({ ...f, rows: f.rows.filter((_, j) => j !== i) }))}
                    aria-label="删除参数"><Trash2 className="w-3.5 h-3.5" /></Button>
                </div>
              ))}
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={save} disabled={saving}>
              {saving ? '保存中...' : '保存'}
            </Button>
          </div>
        </div>
      </Modal>

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={confirmDelete}
        title={`删除模板「${deleteTarget?.name || ''}」`}
        description={deleteTarget?.used_by?.length
          ? `该模板正被 ${deleteTarget.used_by.length} 处流程步骤引用（${deleteTarget.used_by.join('、')}），后端已阻止删除，请先移除引用。已「复制为快照」的节点不受影响。`
          : '删除后不可恢复，已发布版本快照一并移除；已「复制为快照」的流程节点不受影响。'}
        confirmLabel="删除"
      />

      <ConfirmDialog
        open={!!publishTarget}
        onClose={() => setPublishTarget(null)}
        onConfirm={confirmPublish}
        title={`发布「${publishTarget?.name || ''}」新版本`}
        description="以当前草稿内容追加一个不可变版本。所有 version=0（跟随最新版）的流程引用会立即切到新版本；锁定版本的引用不受影响。"
        confirmLabel="发布"
      />

      <Modal open={!!versionsFor} onCancel={() => setVersionsFor(null)} footer={null}
        title={`版本历史 · ${versionsFor?.name || ''}`} width={640}>
        {versionsLoading ? (
          <div className="py-8 text-center text-muted-foreground">加载中...</div>
        ) : versions.length === 0 ? (
          <div className="py-8 text-center text-muted-foreground">尚未发布任何版本</div>
        ) : (
          <div className="space-y-2 max-h-[60vh] overflow-auto">
            {versions.map(v => (
              <div key={v.id} className="border rounded-md p-3 flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <Tag color="blue">v{v.version}</Tag>
                    <span className="text-xs text-muted-foreground">{actionLabel(v.action_type)}</span>
                    {v.created_at && <span className="text-[11px] text-muted-foreground">{formatDateTime(v.created_at)}</span>}
                  </div>
                  <p className="text-xs text-muted-foreground mt-1 font-mono truncate">
                    {Object.keys(v.inputs || {}).join(', ') || '无预设参数'}
                  </p>
                  {v.note && <p className="text-[11px] text-muted-foreground mt-1">{v.note}</p>}
                </div>
                <Button size="small" disabled={busy} onClick={() => rollback(v)}>回滚至此</Button>
              </div>
            ))}
          </div>
        )}
      </Modal>
    </div>
  )
}
