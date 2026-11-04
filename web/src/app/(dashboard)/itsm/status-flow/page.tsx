import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { STATUS_LABELS } from '@/lib/itsm-constants'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ArrowRightLeft, FileText, RotateCcw, Save } from 'lucide-react'

// 状态流转规则配置页（P2-⑯ 状态管理自定义）：
// 显示名覆盖 + 内置状态边裁剪（只收紧不放大：可禁用内置允许的流转、可要求备注，
// 内置禁止的边不提供放行入口，保护流程引擎对状态机的不变量假设）。

type TransitionRule = { from: string; to: string; allowed: boolean; require_comment?: boolean }

type FlowView = {
  catalog_id: string
  builtin: Record<string, Record<string, boolean>>
  overrides: TransitionRule[]
  status_labels: Record<string, string>
  enabled: boolean
  statuses: string[]
  builtin_labels: Record<string, string>
}

const edgeKey = (from: string, to: string) => `${from}>${to}`
const STATUS_ORDER = ['draft', 'pending_review', 'approved', 'rejected', 'executing', 'succeeded', 'failed', 'rolled_back', 'closed', 'withdrawn', 'reopened']

export default function ITSMStatusFlowPage() {
  const [catalogId, setCatalogId] = useState('')
  const [scopeInput, setScopeInput] = useState('')
  const [view, setView] = useState<FlowView | null>(null)
  const [labels, setLabels] = useState<Record<string, string>>({})
  // 边覆盖: key=from>to。未出现 = 内置默认(允许/无备注要求)
  const [edges, setEdges] = useState<Record<string, { allowed: boolean; require_comment: boolean }>>({})
  const [saving, setSaving] = useState(false)

  const load = useCallback(async (scope: string) => {
    try {
      const d = await apiFetch<FlowView>(`/itsm/status-flows${scope ? `?catalog_id=${encodeURIComponent(scope)}` : ''}`)
      setView(d)
      setLabels({ ...(d.status_labels || {}) })
      const e: Record<string, { allowed: boolean; require_comment: boolean }> = {}
      for (const r of d.overrides || []) {
        e[edgeKey(r.from, r.to)] = { allowed: r.allowed, require_comment: !!r.require_comment }
      }
      setEdges(e)
    } catch {
      getMessage().error('加载状态流转配置失败')
    }
  }, [])

  useEffect(() => { load(catalogId) }, [catalogId, load])

  const statusName = (s: string) => labels[s] || view?.builtin_labels?.[s] || STATUS_LABELS[s] || s

  const toggleEdge = (from: string, to: string) => {
    const k = edgeKey(from, to)
    const cur = edges[k] || { allowed: true, require_comment: false }
    setEdges({ ...edges, [k]: { ...cur, allowed: !cur.allowed } })
  }
  const toggleComment = (from: string, to: string) => {
    const k = edgeKey(from, to)
    const cur = edges[k] || { allowed: true, require_comment: false }
    setEdges({ ...edges, [k]: { ...cur, require_comment: !cur.require_comment } })
  }

  const save = async () => {
    setSaving(true)
    try {
      const transitions: TransitionRule[] = []
      for (const [from, tos] of Object.entries(view?.builtin || {})) {
        for (const to of Object.keys(tos)) {
          const o = edges[edgeKey(from, to)]
          // 只保存与内置默认不同的边(禁用/要求备注), 内置禁止的边永不出现在配置里
          if (o && (!o.allowed || o.require_comment)) {
            transitions.push({ from, to, allowed: o.allowed, require_comment: o.require_comment || undefined })
          }
        }
      }
      // 显示名只保存非空项(空 = 回退内置)
      const cleanLabels: Record<string, string> = {}
      for (const [k, v] of Object.entries(labels)) if (v.trim()) cleanLabels[k] = v.trim()
      await apiFetch('/itsm/status-flows', {
        method: 'PUT',
        body: JSON.stringify({ catalog_id: catalogId, transitions, status_labels: cleanLabels, enabled: true }),
      })
      getMessage().success('状态流转配置已保存')
      load(catalogId)
    } catch {
      getMessage().error('保存失败')
    } finally {
      setSaving(false)
    }
  }

  const reset = async () => {
    try {
      await apiFetch(`/itsm/status-flows${catalogId ? `?catalog_id=${encodeURIComponent(catalogId)}` : ''}`, { method: 'DELETE' })
      getMessage().success('已恢复内置状态机')
      load(catalogId)
    } catch {
      getMessage().info('当前无配置')
    }
  }

  return (
    <div className="p-6 space-y-4 max-w-5xl mx-auto">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2"><ArrowRightLeft className="h-5 w-5" />状态流转规则</h1>
          <p className="text-xs text-muted-foreground mt-1">
            自定义状态显示名与流转规则。规则只收紧不放大：可禁用内置允许的流转、可要求填写备注；内置禁止的流转不可配置放行。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Input value={scopeInput} onChange={e => setScopeInput(e.target.value)} placeholder="服务目录 ID（留空=全局）" className="h-8 w-56 text-xs" />
          <Button size="sm" variant="outline" className="h-8" onClick={() => setCatalogId(scopeInput.trim())}>切换作用域</Button>
          <Button size="sm" variant="outline" className="h-8" onClick={reset}><RotateCcw className="h-3.5 w-3.5 mr-1" />恢复内置</Button>
          <Button size="sm" className="h-8" disabled={saving} onClick={save}><Save className="h-3.5 w-3.5 mr-1" />{saving ? '保存中…' : '保存'}</Button>
        </div>
      </div>

      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">状态显示名</CardTitle></CardHeader>
        <CardContent className="grid grid-cols-2 md:grid-cols-3 gap-3">
          {STATUS_ORDER.map(s => (
            <div key={s}>
              <label className="text-[10px] text-muted-foreground font-mono">{s}</label>
              <Input
                className="h-8 mt-0.5 text-xs"
                value={labels[s] ?? ''}
                placeholder={view?.builtin_labels?.[s] || STATUS_LABELS[s] || s}
                onChange={e => setLabels({ ...labels, [s]: e.target.value })}
              />
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">流转规则</CardTitle>
          <p className="text-[10px] text-muted-foreground font-normal">
            点击状态徽章禁用/恢复该流转；点击徽章内的 📝 切换「必须填写备注」。
            {catalogId && <> 当前作用域：<b>目录 {catalogId}</b>（未覆盖的边回退全局配置与内置状态机）</>}
          </p>
        </CardHeader>
        <CardContent className="space-y-2">
          {view && STATUS_ORDER.filter(s => view.builtin[s] && Object.keys(view.builtin[s]).length > 0).map(from => (
            <div key={from} className="flex items-start gap-2 text-xs">
              <span className="w-20 shrink-0 pt-1 font-medium">{statusName(from)}</span>
              <span className="text-muted-foreground pt-1">→</span>
              <div className="flex flex-wrap gap-1.5">
                {Object.keys(view.builtin[from]).map(to => {
                  const o = edges[edgeKey(from, to)] || { allowed: true, require_comment: false }
                  const cls = !o.allowed
                    ? 'border-border bg-muted/40 text-muted-foreground line-through'
                    : o.require_comment
                      ? 'border-amber-500/60 bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-400'
                      : 'border-emerald-500/50 bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'
                  return (
                    <span key={to} className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 cursor-pointer select-none transition-colors ${cls}`}
                      title={!o.allowed ? '已禁用（点击恢复）' : o.require_comment ? '需备注（点击切换）' : '允许（点击禁用）'}
                      onClick={() => toggleEdge(from, to)}>
                      {statusName(to)}
                      <span
                        role="button"
                        className={`text-[10px] leading-none px-0.5 rounded ${o.require_comment ? 'opacity-100' : 'opacity-40 hover:opacity-80'}`}
                        onClick={e => { e.stopPropagation(); toggleComment(from, to) }}
                        title="切换：必须填写备注"
                      >
                        <FileText className="inline h-3 w-3" />
                      </span>
                    </span>
                  )
                })}
              </div>
            </div>
          ))}
          {view && STATUS_ORDER.every(s => !view.builtin[s] || Object.keys(view.builtin[s]).length === 0) && (
            <p className="text-sm text-muted-foreground">内置状态机为空。</p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
