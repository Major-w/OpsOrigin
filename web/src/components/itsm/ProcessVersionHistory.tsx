
// ProcessVersionHistory —— 流程历史版本列表 + 版本间差异对比。
// 复用后端 /itsm/processes/{id}/versions（含当前生效版本，ID="current"）。
// 列表一屏可看全部版本号；点选两个版本做结构性 diff：按 step id 对齐，
// 标记「新增 / 删除 / 修改 / 不变」，不用 hack 第三方 diff 库。

import { useEffect, useMemo, useState } from 'react'
import { useProcessStore } from '@/stores/itsm/processes'
import type { ProcessStep, ProcessVersion } from '@/stores/itsm/types'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { formatTime } from '@/lib/utils'
import { History, GitCompare, Loader2, ArrowRight } from 'lucide-react'

const TYPE_LABEL: Record<string, string> = {
  start: '开始', end: '结束', user: '人工', gateway: '网关',
  timer: '定时', notify: '通知', service: '服务', parallel: '并行', subflow: '子流程',
}

type DiffStatus = 'added' | 'removed' | 'changed' | 'same'

interface AlignedRow {
  id: string
  a?: ProcessStep
  b?: ProcessStep
  status: DiffStatus
}

/** 按 step id 对齐两版步骤，得出逐节点变更状态。 */
function alignSteps(a: ProcessStep[], b: ProcessStep[]): AlignedRow[] {
  const aMap = new Map(a.map(s => [s.id, s]))
  const bMap = new Map(b.map(s => [s.id, s]))
  const ids: string[] = []
  const seen = new Set<string>()
  for (const s of b) { ids.push(s.id); seen.add(s.id) }
  for (const s of a) if (!seen.has(s.id)) { ids.push(s.id); seen.add(s.id) }
  return ids.map(id => {
    const av = aMap.get(id)
    const bv = bMap.get(id)
    let status: DiffStatus
    if (av && !bv) status = 'removed'
    else if (!av && bv) status = 'added'
    else {
      const changed =
        av!.name !== bv!.name ||
        av!.type !== bv!.type ||
        JSON.stringify(av!.depends_on || []) !== JSON.stringify(bv!.depends_on || [])
      status = changed ? 'changed' : 'same'
    }
    return { id, a: av, b: bv, status }
  })
}

const STATUS_META: Record<DiffStatus, { label: string; cls: string }> = {
  added:   { label: '新增', cls: 'bg-blue-500/15 text-blue-600 dark:text-blue-400' },
  removed: { label: '删除', cls: 'bg-red-500/15 text-red-600 dark:text-red-400' },
  changed: { label: '修改', cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
  same:    { label: '不变', cls: 'bg-muted text-muted-foreground' },
}

export function ProcessVersionHistory({ processId, open, onClose }: {
  processId: string
  open: boolean
  onClose: () => void
}) {
  const { listProcessVersions } = useProcessStore()
  const [versions, setVersions] = useState<ProcessVersion[]>([])
  const [loading, setLoading] = useState(false)
  const [aId, setAId] = useState('current')
  const [bId, setBId] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setLoading(true)
    listProcessVersions(processId).then(v => {
      setVersions(v)
      const cur = v.find(x => x.id === 'current')
      const firstHist = v.find(x => x.id !== 'current')
      setAId(cur?.id || v[0]?.id || 'current')
      setBId(firstHist?.id || null)
      setLoading(false)
    }).catch(() => setLoading(false))
  }, [open, processId, listProcessVersions])

  const aVer = versions.find(v => v.id === aId) || versions.find(v => v.id === 'current')
  const bVer = bId ? versions.find(v => v.id === bId) : undefined

  const rows = useMemo(
    () => (aVer && bVer ? alignSteps(aVer.steps, bVer.steps) : []),
    [aVer, bVer],
  )
  const summary = useMemo(() => {
    const s = { added: 0, removed: 0, changed: 0, same: 0 }
    for (const r of rows) s[r.status]++
    return s
  }, [rows])

  return (
    <Dialog open={open} onClose={onClose} title="流程历史版本" className="max-w-4xl">
      <div className="grid grid-cols-[260px_1fr] gap-3" style={{ height: '70vh' }}>
        {/* 左：版本列表 */}
        <div className="overflow-auto rounded-md border border-input">
          <div className="flex items-center gap-1.5 border-b border-input px-2 py-1.5 text-xs font-semibold">
            <History className="h-3.5 w-3.5" /> 版本列表（{versions.length}）
          </div>
          {loading ? (
            <div className="flex justify-center py-10"><Loader2 className="h-5 w-5 animate-spin text-muted-foreground" /></div>
          ) : versions.length === 0 ? (
            <p className="px-3 py-8 text-center text-xs text-muted-foreground">暂无历史版本<br />（每次保存都会归档一份快照）</p>
          ) : (
            <div className="p-1.5 space-y-1">
              {versions.map(v => {
                const isA = v.id === aId
                const isB = v.id === bId
                return (
                  <div
                    key={v.id}
                    className={`rounded-md border p-2 transition-colors ${isB ? 'border-sky-500/60 bg-sky-500/5' : isA ? 'border-blue-500/60 bg-blue-500/5' : 'border-input hover:bg-accent/40'}`}
                  >
                    <div className="flex items-center justify-between gap-1">
                      <span className="flex items-center gap-1 text-xs font-semibold">
                        v{v.version}
                        {v.id === 'current' && <Badge variant="success" className="text-[10px] px-1 py-0">当前</Badge>}
                      </span>
                      <Badge variant={v.enabled ? 'success' : 'outline'} className="text-[10px] px-1 py-0">{v.enabled ? '启用' : '禁用'}</Badge>
                    </div>
                    <p className="mt-0.5 truncate text-[10px] text-muted-foreground">
                      {v.created_by || '未知'} · {formatTime(v.created_at)} · {v.steps?.length || 0} 节点
                    </p>
                    <div className="mt-1 flex gap-1">
                      <button
                        onClick={() => setAId(v.id)}
                        className={`flex-1 rounded px-1 py-0.5 text-[10px] ${isA ? 'bg-blue-500 text-white' : 'bg-muted text-muted-foreground hover:bg-blue-500/15'}`}
                      >设为基准</button>
                      <button
                        onClick={() => setBId(v.id)}
                        className={`flex-1 rounded px-1 py-0.5 text-[10px] ${isB ? 'bg-sky-500 text-white' : 'bg-muted text-muted-foreground hover:bg-sky-500/15'}`}
                      >对比</button>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* 右：差异对比 */}
        <div className="flex flex-col overflow-hidden rounded-md border border-input">
          <div className="flex items-center gap-2 border-b border-input px-3 py-2 text-xs">
            <GitCompare className="h-3.5 w-3.5 shrink-0" />
            {aVer && bVer ? (
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="font-semibold">v{aVer.version}</span>
                <ArrowRight className="h-3 w-3 text-muted-foreground" />
                <span className="font-semibold">v{bVer.version}</span>
                <span className="text-muted-foreground">·</span>
                <Badge className={`text-[10px] px-1 py-0 ${STATUS_META.added.cls}`}>+{summary.added}</Badge>
                <Badge className={`text-[10px] px-1 py-0 ${STATUS_META.removed.cls}`}>-{summary.removed}</Badge>
                <Badge className={`text-[10px] px-1 py-0 ${STATUS_META.changed.cls}`}>~{summary.changed}</Badge>
              </div>
            ) : (
              <span className="text-muted-foreground">在左侧选择一个版本点「对比」</span>
            )}
          </div>

          <div className="flex-1 overflow-auto">
            {aVer && bVer ? (
              <table className="w-full text-xs">
                <thead className="sticky top-0 bg-muted/60 text-[10px] text-muted-foreground">
                  <tr>
                    <th className="px-2 py-1.5 text-left font-medium w-12">变更</th>
                    <th className="px-2 py-1.5 text-left font-medium">v{aVer.version}（基准）</th>
                    <th className="px-2 py-1.5 text-left font-medium">v{bVer.version}（对比）</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map(r => {
                    const m = STATUS_META[r.status]
                    return (
                      <tr key={r.id} className="border-t border-border/60 align-top">
                        <td className="px-2 py-1.5"><Badge className={`text-[10px] px-1 py-0 ${m.cls}`}>{m.label}</Badge></td>
                        <td className="px-2 py-1.5">
                          {r.a ? (
                            <span className={r.status === 'removed' ? 'line-through opacity-60' : ''}>
                              <span className="font-medium">{r.a.name}</span>
                              <span className="ml-1 text-[10px] text-muted-foreground">{TYPE_LABEL[r.a.type] || r.a.type}</span>
                            </span>
                          ) : <span className="text-muted-foreground">—</span>}
                        </td>
                        <td className="px-2 py-1.5">
                          {r.b ? (
                            <span className={r.status === 'added' ? '' : ''}>
                              <span className="font-medium">{r.b.name}</span>
                              <span className="ml-1 text-[10px] text-muted-foreground">{TYPE_LABEL[r.b.type] || r.b.type}</span>
                            </span>
                          ) : <span className="text-muted-foreground">—</span>}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            ) : (
              <div className="flex h-full items-center justify-center text-xs text-muted-foreground">选择两个版本以查看差异</div>
            )}
          </div>
        </div>
      </div>
    </Dialog>
  )
}
