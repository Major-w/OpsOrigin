// 决策表资产：把原本只能内嵌在单张 FormSchema 的决策表提升为可跨表单复用的独立资产。
// 协议对齐 internal/itsm/assets：草稿 Table → publish 生成不可变版本快照 → 表单按 asset_ref 引用。
// 交互契约：破坏性操作走 ConfirmDialog、submitting 防重、toast + 刷新、四态 + ErrorState、筛选 URL 参数化。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import {
  ArrowDown, ArrowUp, ChevronDown, ChevronRight, Copy, History, Play, Plus,
  RefreshCw, Search, Table2, Trash2, Pencil, Rocket, RotateCcw,
} from 'lucide-react'
import { Button, Input, Modal, Tag, Tooltip } from 'antd'
import { apiFetch, apiList, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { formatDateTime } from '@/lib/itsm-constants'
import type { DecisionRow, DecisionTable } from '@/components/itsm/DynamicForm'

interface DecisionTableAsset {
  id: string
  name: string
  description?: string
  status: string
  version: number
  table: DecisionTable
  created_at?: string
  updated_at?: string
  used_by?: string[]
}

interface DecisionTableVersion {
  id: string
  asset_id: string
  version: number
  table: DecisionTable
  note?: string
  created_by?: string
  created_at?: string
}

const emptyRow = (): DecisionRow => ({ cells: {}, outputs: {} })
const emptyTable = (): DecisionTable => ({
  name: '', condition_keys: [], output_keys: [], rows: [emptyRow()],
})

/** 列编辑器：资产不绑定具体表单，列 key 直接输入（支持逗号批量）。 */
function KeyListEditor({ label, keys, onChange, tone }: {
  label: string
  keys: string[]
  onChange: (next: string[]) => void
  tone: 'default' | 'blue'
}) {
  const [draft, setDraft] = useState('')
  const add = () => {
    const next = draft.split(/[,，\s]+/).map(s => s.trim()).filter(Boolean)
    if (!next.length) return
    onChange(Array.from(new Set([...keys, ...next])))
    setDraft('')
  }
  return (
    <div className="flex items-start gap-2">
      <span className="text-[11px] text-muted-foreground w-14 shrink-0 pt-1.5">{label}</span>
      <div className="flex-1 flex flex-wrap items-center gap-1">
        {keys.map(k => (
          <Tag key={k} color={tone === 'blue' ? 'blue' : undefined} closable
            onClose={() => onChange(keys.filter(x => x !== k))}>{k}</Tag>
        ))}
        <Input size="small" className="w-40 h-6 text-[11px]" placeholder="输入列名后回车"
          value={draft} onChange={e => setDraft(e.target.value)}
          onPressEnter={add} onBlur={add} />
        <span className="text-[10px] text-muted-foreground">多个用逗号分隔</span>
      </div>
    </div>
  )
}

/** 决策矩阵编辑器：条件列 × 输出列，行序决定首个命中。 */
function MatrixEditor({ value, onChange }: { value: DecisionTable; onChange: (t: DecisionTable) => void }) {
  const ckeys = value.condition_keys || []
  const okeys = value.output_keys || []
  const rows = value.rows || []
  const patch = (p: Partial<DecisionTable>) => onChange({ ...value, ...p })
  const patchRow = (i: number, p: Partial<DecisionRow>) =>
    patch({ rows: rows.map((r, j) => (j === i ? { ...r, ...p } : r)) })
  const move = (i: number, to: number) => {
    if (to < 0 || to >= rows.length) return
    const next = [...rows]
    const [r] = next.splice(i, 1)
    next.splice(to, 0, r)
    patch({ rows: next })
  }
  const changeKeys = (kind: 'condition_keys' | 'output_keys', next: string[]) => {
    // 删除列时同步清理所有行的孤儿单元格，避免后端校验「列未声明」误判。
    const valid = new Set(next)
    const cleaned = rows.map(r => kind === 'condition_keys'
      ? { ...r, cells: Object.fromEntries(Object.entries(r.cells || {}).filter(([k]) => valid.has(k))) }
      : { ...r, outputs: Object.fromEntries(Object.entries(r.outputs || {}).filter(([k]) => valid.has(k))) })
    patch({ [kind]: next, rows: cleaned } as Partial<DecisionTable>)
  }
  return (
    <div className="space-y-3">
      <KeyListEditor label="条件列" keys={ckeys} tone="default" onChange={v => changeKeys('condition_keys', v)} />
      <KeyListEditor label="输出列" keys={okeys} tone="blue" onChange={v => changeKeys('output_keys', v)} />
      <div className="overflow-x-auto border rounded-md bg-background">
        <table className="w-full text-xs">
          <thead>
            <tr className="border-b bg-muted/40 text-muted-foreground">
              {ckeys.map(k => <th key={k} className="px-2 py-1.5 text-left font-medium whitespace-nowrap">{k}</th>)}
              {okeys.map(k => <th key={k} className="px-2 py-1.5 text-left font-medium whitespace-nowrap text-blue-600 dark:text-blue-400">{k}</th>)}
              <th className="w-28 px-1 py-1.5 text-center font-medium whitespace-nowrap">行操作</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row, ri) => {
              const enabled = row.enabled !== false
              return (
                <tr key={row.id || ri} className={`border-b last:border-b-0 ${enabled ? '' : 'opacity-40'}`}>
                  {ckeys.map(k => (
                    <td key={k} className="px-1 py-1">
                      <Input size="small" variant="borderless" className="h-7 text-[11px]" placeholder="不限"
                        value={row.cells?.[k] || ''}
                        onChange={e => patchRow(ri, { cells: { ...(row.cells || {}), [k]: e.target.value } })} />
                    </td>
                  ))}
                  {okeys.map(k => (
                    <td key={k} className="px-1 py-1">
                      <Input size="small" variant="borderless" className="h-7 text-[11px]" placeholder="—"
                        value={row.outputs?.[k] || ''}
                        onChange={e => patchRow(ri, { outputs: { ...(row.outputs || {}), [k]: e.target.value } })} />
                    </td>
                  ))}
                  <td className="px-1 text-center whitespace-nowrap">
                    <div className="flex items-center justify-center gap-1">
                      <button type="button" title="上移（首个命中行优先）" disabled={ri === 0}
                        className="text-muted-foreground hover:text-primary disabled:opacity-30" onClick={() => move(ri, ri - 1)}>
                        <ArrowUp className="h-3 w-3" /></button>
                      <button type="button" title="下移" disabled={ri === rows.length - 1}
                        className="text-muted-foreground hover:text-primary disabled:opacity-30" onClick={() => move(ri, ri + 1)}>
                        <ArrowDown className="h-3 w-3" /></button>
                      <button type="button" title="复制该行"
                        className="text-muted-foreground hover:text-primary"
                        onClick={() => {
                          const next = [...rows]
                          next.splice(ri + 1, 0, { cells: { ...(row.cells || {}) }, outputs: { ...(row.outputs || {}) } })
                          patch({ rows: next })
                        }}><Copy className="h-3 w-3" /></button>
                      <button type="button" title={enabled ? '停用该行' : '启用该行'}
                        className={enabled ? 'text-xs text-blue-600 dark:text-blue-400' : 'text-xs text-muted-foreground'}
                        onClick={() => patchRow(ri, { enabled: !enabled })}>{enabled ? '✓' : '✕'}</button>
                      <button type="button" title="删除本行" className="text-muted-foreground hover:text-destructive"
                        onClick={() => patch({ rows: rows.filter((_, j) => j !== ri) })}><Trash2 className="h-3 w-3" /></button>
                    </div>
                  </td>
                </tr>
              )
            })}
            {rows.length === 0 && (
              <tr><td colSpan={ckeys.length + okeys.length + 1} className="px-2 py-3 text-center text-muted-foreground italic">暂无行</td></tr>
            )}
          </tbody>
        </table>
      </div>
      <div className="flex gap-2">
        <Button size="small" type="dashed" className="h-6 text-[11px]" onClick={() => patch({ rows: [...rows, emptyRow()] })}>
          <Plus className="h-3 w-3 mr-0.5" />加行</Button>
        {rows.length > 0 && (
          <Button size="small" type="dashed" className="h-6 text-[11px]" onClick={() => patch({ rows: rows.slice(0, -1) })}>
            <Trash2 className="h-3 w-3 mr-0.5" />删末行</Button>
        )}
      </div>
    </div>
  )
}

export default function DecisionTablesPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [keyword, setKeyword] = useState(searchParams.get('q') || '')
  const [assets, setAssets] = useState<DecisionTableAsset[]>([])
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<{ name: string; description: string; table: DecisionTable }>(
    { name: '', description: '', table: emptyTable() })
  const [saving, setSaving] = useState(false)

  const [versionFor, setVersionFor] = useState<DecisionTableAsset | null>(null)
  const [versions, setVersions] = useState<DecisionTableVersion[]>([])
  const [versionLoading, setVersionLoading] = useState(false)
  const [busy, setBusy] = useState(false)

  const [confirmAction, setConfirmAction] = useState<{
    kind: 'delete' | 'publish' | 'rollback'
    asset: DecisionTableAsset
    version?: number
  } | null>(null)

  const [previewOpen, setPreviewOpen] = useState(false)
  const [previewValues, setPreviewValues] = useState<Record<string, string>>({})
  const [previewResult, setPreviewResult] = useState<{ values: Record<string, unknown>; fired: string[]; warnings: string[] } | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    setLoadError(null)
    try {
      const data = await apiList<DecisionTableAsset>('/itsm/decision-tables')
      setAssets(data || [])
    } catch (e: any) {
      setLoadError(e.message || '加载决策表资产失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const filtered = useMemo(() => assets.filter(a =>
    !keyword ||
    a.name?.toLowerCase().includes(keyword.toLowerCase()) ||
    a.description?.toLowerCase().includes(keyword.toLowerCase())
  ), [assets, keyword])

  const setKeywordParam = (v: string) => {
    setKeyword(v)
    const next = new URLSearchParams(searchParams)
    if (v) next.set('q', v)
    else next.delete('q')
    setSearchParams(next, { replace: true })
  }

  const openCreate = () => {
    setEditingId(null)
    setForm({ name: '', description: '', table: emptyTable() })
    setPreviewResult(null)
    setPreviewOpen(false)
    setDialogOpen(true)
  }

  const openEdit = (a: DecisionTableAsset) => {
    setEditingId(a.id)
    setForm({
      name: a.name || '',
      description: a.description || '',
      table: { ...emptyTable(), ...(a.table || {}), rows: (a.table?.rows?.length ? a.table.rows : [emptyRow()]) },
    })
    setPreviewResult(null)
    setPreviewOpen(false)
    setDialogOpen(true)
  }

  const validate = (): string | null => {
    if (!form.name.trim()) return '请填写资产名称'
    const t = form.table
    if (!t.condition_keys?.length) return '至少声明一个条件列'
    if (!t.output_keys?.length) return '至少声明一个输出列'
    if (!t.rows?.length) return '至少配置一行规则'
    if (new Set([...t.condition_keys, ...t.output_keys]).size !== t.condition_keys.length + t.output_keys.length) {
      return '条件列与输出列存在重复列名'
    }
    for (let i = 0; i < t.rows.length; i++) {
      const r = t.rows[i]
      for (const [k] of Object.entries(r.cells || {})) {
        if (!t.condition_keys.includes(k)) return `第 ${i + 1} 行引用了未声明的条件列「${k}」`
      }
      for (const [k] of Object.entries(r.outputs || {})) {
        if (!t.output_keys.includes(k)) return `第 ${i + 1} 行引用了未声明的输出列「${k}」`
      }
    }
    return null
  }

  const save = async () => {
    const err = validate()
    if (err) { getMessage().error(err); return }
    setSaving(true)
    try {
      const res = await apiFetch<{ asset: DecisionTableAsset; warnings?: string[] }>('/itsm/decision-tables', {
        method: 'POST',
        body: JSON.stringify({
          id: editingId || undefined,
          name: form.name.trim(),
          description: form.description.trim(),
          table: { ...form.table, name: form.name.trim() },
        }),
      })
      getMessage().success(editingId ? '草稿已保存' : '资产已创建（草稿，发布后表单引用才生效）')
      if (res?.warnings?.length) res.warnings.forEach(w => getMessage().warning(w))
      setDialogOpen(false)
      load()
    } catch (e: any) {
      getMessage().error(`保存失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const openVersions = async (a: DecisionTableAsset) => {
    setVersionFor(a)
    setVersions([])
    setVersionLoading(true)
    try {
      setVersions((await apiList<DecisionTableVersion>(`/itsm/decision-tables/${a.id}/versions`)) || [])
    } catch (e: any) {
      getMessage().error(`加载版本失败: ${e.message}`)
    } finally {
      setVersionLoading(false)
    }
  }

  const runConfirm = async () => {
    if (!confirmAction) return
    const { kind, asset, version } = confirmAction
    setBusy(true)
    try {
      if (kind === 'delete') {
        await apiFetch(`/itsm/decision-tables/${asset.id}`, { method: 'DELETE' })
        getMessage().success('删除成功')
      } else if (kind === 'publish') {
        await apiFetch(`/itsm/decision-tables/${asset.id}/publish`, {
          method: 'POST', body: JSON.stringify({ note: '发布新版本' }),
        })
        getMessage().success('已发布新版本，跟随最新版的表单立即生效')
        if (versionFor?.id === asset.id) await openVersions({ ...asset, version: asset.version + 1 })
      } else {
        await apiFetch(`/itsm/decision-tables/${asset.id}/rollback`, {
          method: 'POST', body: JSON.stringify({ version, note: `回滚至版本 v${version}` }),
        })
        getMessage().success(`已回滚到 v${version}（生成新版本，历史不改）`)
        if (versionFor?.id === asset.id) await openVersions({ ...asset, version: asset.version + 1 })
      }
      load()
    } catch (e: any) {
      getMessage().error(`${kind === 'delete' ? '删除' : kind === 'publish' ? '发布' : '回滚'}失败: ${e.message}`)
    } finally {
      setBusy(false)
      setConfirmAction(null)
    }
  }

  const runPreview = async () => {
    setPreviewLoading(true)
    try {
      // 编辑态直接送草稿 table（后端优先采用，不必先保存）；新建态 id 用占位 draft。
      const res = await apiFetch<{ values: Record<string, unknown>; fired: string[]; warnings: string[] }>(
        `/itsm/decision-tables/${editingId || 'draft'}/preview`, {
          method: 'POST',
          body: JSON.stringify({ table: { ...form.table, name: form.name.trim() }, values: previewValues }),
        })
      setPreviewResult({
        values: res?.values || {}, fired: res?.fired || [], warnings: res?.warnings || [],
      })
    } catch (e: any) {
      setPreviewResult({ values: {}, fired: [], warnings: [`试算失败: ${e.message}`] })
    } finally {
      setPreviewLoading(false)
    }
  }

  const confirmCopy = (() => {
    if (!confirmAction) return { title: '', description: '', label: '确定' }
    const { kind, asset, version } = confirmAction
    if (kind === 'delete') {
      const used = asset.used_by?.length || 0
      return {
        title: `删除决策表「${asset.name}」`,
        description: used > 0
          ? `该资产正被 ${used} 处配置引用（${asset.used_by!.join('、')}），后端已阻止删除，请先移除引用。`
          : '删除后不可恢复，且已发布版本快照一并移除。',
        label: '删除',
      }
    }
    if (kind === 'publish') {
      return {
        title: `发布「${asset.name}」的新版本`,
        description: '以当前草稿内容生成不可变版本快照；所有「跟随最新版」的表单引用将立即切换到新版本。',
        label: '发布',
      }
    }
    return {
      title: `回滚「${asset.name}」至 v${version}`,
      description: '历史版本永不改写，回滚会以该快照生成一个新版本；跟随最新版的表单立即生效。',
      label: '回滚',
    }
  })()

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold">决策表资产</h1>
          <p className="text-sm text-muted-foreground mt-1">
            跨表单复用的规则矩阵；草稿编辑 → 发布生成不可变版本 → 表单设计器按「跟随最新版 / 锁定版本」引用
          </p>
        </div>
        <Button type="primary" onClick={openCreate}><Plus className="w-4 h-4 mr-1" />新建资产</Button>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input placeholder="搜索资产名称或描述..." value={keyword}
            onChange={e => setKeywordParam(e.target.value)} className="pl-9" />
        </div>
        <Button onClick={load} disabled={loading}>
          <RefreshCw className={cn('w-4 h-4 mr-1', loading && 'animate-spin')} />刷新
        </Button>
      </div>

      {loadError ? (
        <ErrorState message={`加载决策表资产失败: ${loadError}`} onRetry={load} />
      ) : loading ? (
        <div className="text-center py-12 text-muted-foreground">加载中...</div>
      ) : filtered.length === 0 ? (
        <div className="border rounded-lg py-12 text-center">
          <Table2 className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
          <p className="text-muted-foreground">{keyword ? '无匹配的资产' : '暂无决策表资产，新建后可在表单设计器中引用'}</p>
        </div>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map(a => (
            <div key={a.id} className="border rounded-lg p-4 bg-card hover:shadow-md transition-shadow">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <h3 className="text-base font-semibold truncate">{a.name}</h3>
                    <Tag color={a.status === 'published' ? 'green' : 'default'}>
                      {a.status === 'published' ? `已发布 v${a.version}` : '草稿'}
                    </Tag>
                    {(a.used_by?.length || 0) > 0 && (
                      <Tooltip title={`被引用：${a.used_by!.join('、')}`}>
                        <Tag color="blue">被 {a.used_by!.length} 处配置引用</Tag>
                      </Tooltip>
                    )}
                  </div>
                  {a.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-1">{a.description}</p>}
                </div>
                <div className="flex gap-0.5 shrink-0">
                  <Tooltip title="编辑草稿"><Button type="text" size="small" onClick={() => openEdit(a)} aria-label="编辑">
                    <Pencil className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="版本与发布"><Button type="text" size="small" onClick={() => openVersions(a)} aria-label="版本">
                    <History className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="发布新版本"><Button type="text" size="small"
                    onClick={() => setConfirmAction({ kind: 'publish', asset: a })} aria-label="发布">
                    <Rocket className="w-3.5 h-3.5" /></Button></Tooltip>
                  <Tooltip title="删除"><Button type="text" size="small" danger
                    onClick={() => setConfirmAction({ kind: 'delete', asset: a })} aria-label="删除">
                    <Trash2 className="w-3.5 h-3.5" /></Button></Tooltip>
                </div>
              </div>
              <div className="mt-3 flex items-center gap-3 text-xs text-muted-foreground">
                <span>条件列 {a.table?.condition_keys?.length || 0}</span>
                <span>输出列 {a.table?.output_keys?.length || 0}</span>
                <span>规则行 {a.table?.rows?.length || 0}</span>
                {a.updated_at && <span className="ml-auto">{formatDateTime(a.updated_at)}</span>}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 编辑草稿 */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)}
        title={editingId ? '编辑决策表草稿' : '新建决策表资产'} width={900} footer={null}>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">资产名称 <span className="text-red-500">*</span></label>
              <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="如：事件分级矩阵" />
            </div>
            <div>
              <label className="text-sm font-medium">描述</label>
              <Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                placeholder="用途说明（可选）" />
            </div>
          </div>
          <p className="text-[11px] text-muted-foreground">
            单元格留空表示不限；多个候选值用英文逗号分隔。自上而下首个命中且启用的行生效，输出列支持 {'${字段名}'} 插值。
            保存只更新草稿，需「发布」后表单引用才生效。
          </p>
          <MatrixEditor value={form.table} onChange={t => setForm(f => ({ ...f, table: t }))} />

          {/* 试算：草稿态直送后端同一推导通道 */}
          <div className="rounded-md border bg-muted/20 p-2.5 space-y-2">
            <button type="button" className="flex items-center gap-1.5 text-[11px] font-medium"
              onClick={() => setPreviewOpen(v => !v)}>
              {previewOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
              试算（用当前草稿验证命中行与输出）
            </button>
            {previewOpen && (
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2">
                  {(form.table.condition_keys || []).length === 0 &&
                    <span className="text-[11px] text-muted-foreground">还没有条件列</span>}
                  {(form.table.condition_keys || []).map(k => (
                    <label key={k} className="flex items-center gap-1 text-[11px]">
                      <span className="text-muted-foreground">{k}</span>
                      <Input size="small" className="h-6 w-32 text-[11px]" placeholder="样例值"
                        value={previewValues[k] || ''}
                        onChange={e => setPreviewValues(v => ({ ...v, [k]: e.target.value }))} />
                    </label>
                  ))}
                  <Button size="small" type="primary" className="h-6 text-[11px]" loading={previewLoading} onClick={runPreview}>
                    <Play className="h-3 w-3 mr-0.5" />运行试算
                  </Button>
                </div>
                {previewResult && (
                  <div className="space-y-1 text-[11px]">
                    <div className="text-muted-foreground">触发规则（含命中行号）：</div>
                    {previewResult.fired.length
                      ? previewResult.fired.map((f, n) => <div key={n} className="text-blue-600 dark:text-blue-400">· {f}</div>)
                      : <div className="text-muted-foreground">无规则命中</div>}
                    {previewResult.warnings.length > 0 && (
                      <div className="text-amber-600 dark:text-amber-400">
                        {previewResult.warnings.map((w, n) => <div key={n}>· {w}</div>)}
                      </div>
                    )}
                    <div className="text-muted-foreground">推导后的输出列：</div>
                    <div className="rounded border bg-background p-1.5 font-mono">
                      {(form.table.output_keys || []).map(k => (
                        <div key={k}>{k} = {String(previewResult.values[k] ?? '（未赋值）')}</div>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>

          <div className="flex justify-end gap-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={save} disabled={saving}>
              {saving ? '保存中...' : '保存草稿'}
            </Button>
          </div>
        </div>
      </Modal>

      {/* 版本历史 */}
      <Modal open={!!versionFor} onCancel={() => setVersionFor(null)} footer={null}
        title={`版本历史 — ${versionFor?.name || ''}`} width={640}>
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <span className="text-xs text-muted-foreground">
              当前状态：{versionFor?.status === 'published' ? `已发布 v${versionFor.version}` : '草稿（未发布）'}
            </span>
            <Button size="small" type="primary" disabled={busy}
              onClick={() => versionFor && setConfirmAction({ kind: 'publish', asset: versionFor })}>
              <Rocket className="w-3 h-3 mr-1" />发布当前草稿
            </Button>
          </div>
          {versionLoading ? (
            <div className="text-center py-8 text-muted-foreground">加载中...</div>
          ) : versions.length === 0 ? (
            <div className="border rounded-md py-8 text-center text-sm text-muted-foreground">暂无已发布版本</div>
          ) : (
            <div className="space-y-2 max-h-80 overflow-y-auto">
              {versions.map(v => (
                <div key={v.id} className="border rounded-md p-3 flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <Tag color="blue">v{v.version}</Tag>
                      <span className="text-xs text-muted-foreground">{formatDateTime(v.created_at || '')}</span>
                      {v.created_by && <span className="text-xs text-muted-foreground">by {v.created_by}</span>}
                    </div>
                    <p className="text-xs mt-1 truncate">{v.note || '—'}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5">
                      {v.table?.condition_keys?.length || 0} 条件列 / {v.table?.output_keys?.length || 0} 输出列 / {v.table?.rows?.length || 0} 行
                    </p>
                  </div>
                  <Button size="small" disabled={busy}
                    onClick={() => versionFor && setConfirmAction({ kind: 'rollback', asset: versionFor, version: v.version })}>
                    <RotateCcw className="w-3 h-3 mr-1" />回滚
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      </Modal>

      <ConfirmDialog
        open={!!confirmAction}
        onClose={() => setConfirmAction(null)}
        onConfirm={runConfirm}
        title={confirmCopy.title}
        description={confirmCopy.description}
        confirmLabel={confirmCopy.label}
      />
    </div>
  )
}
