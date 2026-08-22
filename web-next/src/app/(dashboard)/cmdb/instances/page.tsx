'use client'

import { useEffect, useState, useRef } from 'react'
import { useSearchParams, useRouter } from 'next/navigation'
import Link from 'next/link'
import { useCMDBStore, type CIModel } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, apiFetch, apiList, apiFetchRaw } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useCan } from '@/stores/permissions'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Plus, Pencil, Trash2, Network, Search, ChevronLeft, ChevronRight, Link2, Unlink, Archive, ArchiveRestore, Bookmark, GitMerge, Radio, Download, Upload } from 'lucide-react'

interface SavedQuery { id: string; name: string; type_id?: string; query: { q?: string; status?: string; attrs?: Record<string, string> } }

const CI_TYPES = ['server', 'web-server', 'database', 'switch', 'loadbalancer', 'dns-server', 'firewall', 'storage']
const PAGE_SIZE = 20

function statusVariant(s: string): 'success' | 'destructive' | 'outline' | 'secondary' {
  if (s === 'active') return 'success'
  if (s === 'inactive') return 'outline'
  if (s === 'archived') return 'secondary'
  return 'destructive'
}

export default function CMDBPage() {
  const { items, loading, fetch, search, remove } = useCMDBStore()
  const canDelete = useCan('cmdb:delete')
  const canWrite = useCan('cmdb:write')
  const searchParams = useSearchParams()
  const router = useRouter()
  const [searchQ, setSearchQ] = useState(() => searchParams.get('q') || '')
  const [typeFilter, setTypeFilter] = useState(() => searchParams.get('type') || '')
  const [archivedView, setArchivedView] = useState(false)
  const [savedQueries, setSavedQueries] = useState<SavedQuery[]>([])
  const [sqDialogOpen, setSqDialogOpen] = useState(false)
  const [sqName, setSqName] = useState('')
  const [page, setPage] = useState(0)
  const [models, setModels] = useState<{ id: string; name: string; category: string; parent_id?: string }[]>([])
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [submitting, setSubmitting] = useState(false)
  const [sqSubmitting, setSqSubmitting] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [importResult, setImportResult] = useState<{ created: number; updated: number; failed: { row: number; error: string }[] } | null>(null)
  const [reconcileOpen, setReconcileOpen] = useState(false)
  const [reconcileLoading, setReconcileLoading] = useState(false)
  const [reconcileGroups, setReconcileGroups] = useState<{
    key: string; reason: string;
    members: { ci_id: string; name: string; type_id: string; source: string; confidence: number; updated_at: string }[]
  }[]>([])
  const [mergeTarget, setMergeTarget] = useState<typeof reconcileGroups[number] | null>(null)
  const [mergeSubmitting, setMergeSubmitting] = useState(false)
  const [reconcileKeep, setReconcileKeep] = useState<Record<string, string>>({})
  const [batchDeleteOpen, setBatchDeleteOpen] = useState(false)
  const [batchDeleteSubmitting, setBatchDeleteSubmitting] = useState(false)

  useEffect(() => {
    apiList<typeof models[number]>('/cmdb/model-type?limit=1000')
      .then(list => setModels(list))
      .catch(() => setModels([]))
  }, [])

  useEffect(() => {
    fetch({ limit: PAGE_SIZE, offset: page * PAGE_SIZE, sort_by: 'updated_at', sort_order: 'desc' })
    loadSavedQueries()
  }, [])

  const loadData = () => {
    const params: any = { limit: PAGE_SIZE, offset: page * PAGE_SIZE, sort_by: 'updated_at', sort_order: 'desc', archived: archivedView ? 'true' : 'false' }
    if (typeFilter) params.type_id = typeFilter
    if (searchQ) search(searchQ, typeFilter, archivedView ? 'true' : 'false')
    else fetch(params)
  }

  useEffect(() => { loadData() }, [page, typeFilter, archivedView])

  const loadSavedQueries = async () => {
    try { setSavedQueries(await apiList<SavedQuery>('/cmdb/saved-queries')) } catch { setSavedQueries([]) }
  }

  const toggleCheck = (id: string) => {
    setChecked(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }
  const checkedList = () => Array.from(checked)

  const openBatchDelete = () => {
    const ids = checkedList()
    if (!ids.length) return
    setBatchDeleteOpen(true)
  }
  const confirmBatchDelete = async () => {
    const ids = checkedList()
    if (!ids.length) return
    setBatchDeleteSubmitting(true)
    try {
      const res = await apiFetch<{ deleted: number; failed: { id: string; error: string }[] }>('/cmdb/ci/batch-delete', { method: 'POST', body: JSON.stringify({ ids }) })
      toast.success(`已删除 ${res.deleted} 个资产${res.failed?.length ? `，${res.failed.length} 个失败` : ''}`)
      setChecked(new Set()); loadData()
      setBatchDeleteOpen(false)
    } catch (e: any) { toast.error('批量删除失败: ' + e.message) } finally { setBatchDeleteSubmitting(false) }
  }

  // 实体调解合并(IRE)
  const openReconcile = async () => {
    setReconcileOpen(true); setReconcileLoading(true)
    try {
      const groups = await apiFetch<typeof reconcileGroups>('/cmdb/reconcile/candidates')
      setReconcileGroups(groups)
      const keep: Record<string, string> = {}
      for (const g of groups) if (g.members.length) keep[g.key] = g.members[0].ci_id
      setReconcileKeep(keep)
    } catch (e: any) { setReconcileGroups([]); toast.error('加载疑似重复失败: ' + e.message) }
    finally { setReconcileLoading(false) }
  }
  const openReconcileMerge = (g: typeof reconcileGroups[number]) => setMergeTarget(g)
  const confirmReconcileMerge = async () => {
    const g = mergeTarget
    if (!g) return
    const keep = reconcileKeep[g.key]
    const mergeIds = g.members.map(m => m.ci_id).filter(id => id !== keep)
    if (!keep || !mergeIds.length) return
    setMergeSubmitting(true)
    try {
      const res = await apiFetch<{ kept_id: string; merged_count: number; updated_fields: string[] }>('/cmdb/reconcile/merge', { method: 'POST', body: JSON.stringify({ keep_id: keep, merge_ids: mergeIds }) })
      toast.success(`已合并 ${res.merged_count} 条，更新字段: ${res.updated_fields.join(', ') || '无'}`)
      setMergeTarget(null); openReconcile(); loadData()
    } catch (e: any) { toast.error('合并失败: ' + e.message) } finally { setMergeSubmitting(false) }
  }

  const handleExport = async () => {
    try {
      const res = await apiFetchRaw(`/cmdb/ci/export?type_id=${encodeURIComponent(typeFilter)}`)
      if (!res.ok) { alert('导出失败: ' + res.status); return }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      const cd = res.headers.get('Content-Disposition')
      a.download = cd?.match(/filename=(.+)/)?.[1] || `cmdb_${typeFilter || 'all'}_export.csv`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e: any) { alert('导出失败: ' + e.message) }
  }

  const handleImportFile = async (e: any) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const fd = new FormData()
      fd.append('file', file)
      const res = await apiFetchRaw(`/cmdb/ci/import?type_id=${encodeURIComponent(typeFilter)}`, { method: 'POST', body: fd })
      const data = await res.json()
      setImportResult(data)
      setImportOpen(true)
      if ((data.created || 0) + (data.updated || 0) > 0) loadData()
    } catch (e: any) {
      setImportResult({ created: 0, updated: 0, failed: [{ row: 0, error: e.message }] })
      setImportOpen(true)
    } finally {
      e.target.value = ''
    }
  }

  const handleSaveQuery = async () => {
    if (sqSubmitting || !sqName.trim()) { if (!sqName.trim()) toast.error('请输入查询名称'); return }
    setSqSubmitting(true)
    try {
      await apiFetch('/cmdb/saved-queries', {
        method: 'POST',
        body: JSON.stringify({ name: sqName.trim(), type_id: typeFilter || undefined, query: { q: searchQ || undefined } }),
      })
      toast.success('已保存查询策略')
      setSqDialogOpen(false); setSqName('')
      loadSavedQueries()
    } catch (e: any) { toast.error('保存失败: ' + e.message) } finally { setSqSubmitting(false) }
  }

  const handleApplySavedQuery = (sq: SavedQuery) => {
    setTypeFilter(sq.type_id || '')
    setSearchQ(sq.query?.q || '')
    setPage(0)
    loadData()
  }

  const syncUrl = (q: string, t: string) => {
    const p = new URLSearchParams()
    if (q) p.set('q', q)
    if (t) p.set('type', t)
    router.replace(`/cmdb/instances?${p.toString()}`, { scroll: false })
  }

  const handleSearch = () => {
    if (searchQ) search(searchQ, typeFilter, archivedView ? 'true' : 'false')
    else loadData()
    syncUrl(searchQ, typeFilter)
  }

  const openCI = (item: CIModel) => router.push(`/cmdb/instances/${item.id}`)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">配置项管理</h1><p className="text-sm text-muted-foreground">按模型结构浏览配置项实例，点击实例查看详情、编辑与更新</p></div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={handleExport}><Download className="h-4 w-4" />导出 CSV</Button>
          <Button variant="outline" onClick={() => fileRef.current?.click()}><Upload className="h-4 w-4" />导入 CSV</Button>
          <Button variant="outline" onClick={() => setSqDialogOpen(true)}><Bookmark className="h-4 w-4" />保存查询</Button>
          <Button variant="outline" onClick={openReconcile}><GitMerge className="h-4 w-4" />疑似重复</Button>
          <Link href="/cmdb/auto-collect"><Button variant="outline"><Radio className="h-4 w-4" />配置自动采集</Button></Link>
          <Link href="/cmdb/instances/new"><Button><Plus className="h-4 w-4" />新建配置项</Button></Link>
        </div>
      </div>

      <div className="flex gap-2 items-center">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input className="pl-8" placeholder="搜索资产名称..." value={searchQ} onChange={e => setSearchQ(e.target.value)} onKeyDown={e => e.key === 'Enter' && handleSearch()} />
        </div>
        <select className="h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value={typeFilter} onChange={e => { setTypeFilter(e.target.value); setPage(0); syncUrl(searchQ, e.target.value) }}>
          <option value="">所有类型</option>
          {CI_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        <Button variant="outline" onClick={handleSearch}>搜索</Button>
        <Button variant={archivedView ? 'default' : 'outline'} onClick={() => { setArchivedView(v => !v); setPage(0) }}>
          <Archive className="h-4 w-4" />{archivedView ? '归档视图' : '正常视图'}
        </Button>
        {savedQueries.length > 0 && (
          <select className="h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value="" onChange={e => { const sq = savedQueries.find(s => s.id === e.target.value); if (sq) handleApplySavedQuery(sq); e.target.value = '' }}>
            <option value="">应用保存的查询…</option>
            {savedQueries.map(sq => <option key={sq.id} value={sq.id}>{sq.name}</option>)}
          </select>
        )}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-4 gap-4">
        {/* 左栏: 模型结构（分类 → 模型 → 子模型） */}
        <div className="lg:col-span-1 space-y-4">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">模型结构</CardTitle></CardHeader>
            <CardContent className="p-0 max-h-[70vh] overflow-y-auto">
              {(() => {
                const roots = models.filter(m => !m.parent_id)
                const childrenOf = (id: string) => models.filter(m => m.parent_id === id)
                const byCat = new Map<string, typeof models>()
                for (const m of roots) {
                  const arr = byCat.get(m.category) || []
                  arr.push(m); byCat.set(m.category, arr)
                }
                const catOrder = ['基础设施资源', '数据库资源', '中间件资源', '应用资源', '业务资源']
                const cats = catOrder.filter(c => byCat.has(c)).concat(Array.from(byCat.keys()).filter(c => !catOrder.includes(c)))
                return (
                  <div className="text-xs">
                    {cats.map(cat => (
                      <div key={cat}>
                        <p className="px-3 py-1.5 text-[10px] font-semibold text-muted-foreground uppercase tracking-wide bg-muted/40">{cat}</p>
                        {(byCat.get(cat) || []).map(m => (
                          <div key={m.id}>
                            <button onClick={() => { setTypeFilter(m.id); setPage(0) }}
                              className={`w-full text-left px-3 py-1.5 flex items-center gap-1.5 hover:bg-accent ${typeFilter === m.id ? 'bg-accent text-emerald-600 font-medium' : ''}`}>
                              <ChevronLeft className="h-3 w-3 rotate-180 text-muted-foreground shrink-0" />
                              <span className="truncate">{m.name}</span>
                              <span className="text-[9px] text-muted-foreground ml-auto">{m.id}</span>
                            </button>
                            {childrenOf(m.id).map(c => (
                              <button key={c.id} onClick={() => { setTypeFilter(c.id); setPage(0) }}
                                className={`w-full text-left pl-7 pr-3 py-1 flex items-center gap-1.5 hover:bg-accent ${typeFilter === c.id ? 'bg-accent text-emerald-600 font-medium' : ''}`}>
                                <span className="text-muted-foreground shrink-0">└</span>
                                <span className="truncate">{c.name}</span>
                              </button>
                            ))}
                          </div>
                        ))}
                      </div>
                    ))}
                  </div>
                )
              })()}
            </CardContent>
          </Card>
        </div>

        {/* 右栏: 实例列表 */}
        <div className="lg:col-span-3 space-y-4">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">实例列表 ({items.length})</CardTitle></CardHeader>
            <CardContent className="p-0">
              {loading && items.length === 0 ? <p className="px-4 text-sm text-muted-foreground">加载中...</p> : items.length === 0 ? (
                <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
                  <p className="text-sm text-muted-foreground">
                    {typeFilter ? '当前筛选条件下' : '当前'}暂无配置项实例。
                  </p>
                  <p className="text-xs text-muted-foreground/70">
                    配置项（CI）是 CMDB 的最小管理单元，可手动新建或从「自动发现」接入。
                  </p>
                  {canWrite && (
                    <Link href={typeFilter ? `/cmdb/instances/new?type_id=${encodeURIComponent(typeFilter)}` : '/cmdb/instances/new'}>
                      <Button className="mt-2" size="sm"><Plus className="h-4 w-4 mr-1" />新建配置项</Button>
                    </Link>
                  )}
                </div>
              ) : (
                <div className="flex flex-col">
                  <div className="flex items-center gap-1.5 px-3 py-2 border-b bg-muted/30">
                    <input type="checkbox" className="rounded" checked={checked.size > 0 && checked.size === items.length}
                      onChange={e => setChecked(e.target.checked ? new Set(items.map(i => i.id)) : new Set())} title="全选本页" />
                    <span className="text-xs text-muted-foreground shrink-0">{checked.size > 0 ? `已选 ${checked.size} 项` : '勾选批量操作'}</span>
                    {checked.size > 0 && (
                      <div className="flex gap-1 ml-auto">
                        <Button variant="outline" size="sm" className="h-6 text-xs text-destructive" onClick={openBatchDelete}><Trash2 className="h-3 w-3 mr-1" />删除</Button>
                      </div>
                    )}
                  </div>
                  {items.map(item => (
                    <div key={item.id} onClick={() => openCI(item)}
                      className="px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 min-w-0">
                          <input type="checkbox" className="rounded shrink-0" checked={checked.has(item.id)}
                            onClick={e => e.stopPropagation()}
                            onChange={() => toggleCheck(item.id)} title="选择以批量操作" />
                          <span className="font-medium text-sm truncate">{item.name}</span>
                        </div>
                        <div className="flex gap-1 shrink-0">
                          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); openCI(item) }}><Pencil className="h-3 w-3" /></Button>
                          {item.status === 'archived'
                            ? <Button variant="ghost" size="icon" className="h-6 w-6" title="恢复" onClick={e => { e.stopPropagation(); (async () => { await apiFetch(`/cmdb/ci/${item.id}/restore`, { method: 'POST' }); toast.success('已恢复'); loadData() })() }}><ArchiveRestore className="h-3 w-3" /></Button>
                            : <Button variant="ghost" size="icon" className="h-6 w-6" title="归档" onClick={e => { e.stopPropagation(); (async () => { await apiFetch(`/cmdb/ci/${item.id}/archive`, { method: 'POST' }); toast.success('已归档'); loadData() })() }}><Archive className="h-3 w-3" /></Button>}
                          {canDelete && <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); remove(item.id); setChecked(prev => { const n = new Set(prev); n.delete(item.id); return n }) }}><Trash2 className="h-3 w-3" /></Button>}
                        </div>
                      </div>
                      <div className="flex gap-2 mt-1">
                        <Badge variant="secondary" className="text-xs">{item.type_id}</Badge>
                        <Badge variant={statusVariant(item.status)} className="text-xs">{item.status}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">{formatTime(item.updated_at)}</p>
                    </div>
                  ))}
                  <div className="flex items-center justify-between px-4 py-2 border-t">
                    <Button variant="ghost" size="sm" disabled={page === 0} onClick={() => setPage(p => p - 1)}><ChevronLeft className="h-4 w-4" /></Button>
                    <span className="text-xs text-muted-foreground">第 {page + 1} 页</span>
                    <Button variant="ghost" size="sm" disabled={items.length < PAGE_SIZE} onClick={() => setPage(p => p + 1)}><ChevronRight className="h-4 w-4" /></Button>
                  </div>
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>

      <input ref={fileRef} type="file" accept=".csv" className="hidden" onChange={handleImportFile} />

      <Dialog open={reconcileOpen} onClose={() => setReconcileOpen(false)} title="实体调解（疑似重复）">
        <div className="space-y-4 max-h-[60vh] overflow-y-auto">
          {reconcileLoading && <p className="text-sm text-muted-foreground">扫描中...</p>}
          {!reconcileLoading && reconcileGroups.length === 0 && (
            <p className="text-sm text-muted-foreground py-6 text-center">未发现疑似重复的实体 🎉</p>
          )}
          {reconcileGroups.map(g => (
            <div key={g.key} className="border rounded-md p-3 space-y-2">
              <div className="flex items-center justify-between">
                <p className="text-sm font-medium">{g.reason}</p>
                <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => openReconcileMerge(g)}><GitMerge className="h-3 w-3 mr-1" />合并</Button>
              </div>
              <div className="space-y-1">
                {g.members.map(m => (
                  <label key={m.ci_id} className="flex items-center gap-2 text-xs rounded px-2 py-1 hover:bg-accent cursor-pointer">
                    <input type="radio" name={`keep-${g.key}`} className="rounded-full" checked={reconcileKeep[g.key] === m.ci_id}
                      onChange={() => setReconcileKeep(k => ({ ...k, [g.key]: m.ci_id }))} />
                    <span className="font-medium">{m.name}</span>
                    <Badge variant="outline" className="text-[9px]">{m.type_id}</Badge>
                    <span className="text-muted-foreground ml-auto flex items-center gap-2">
                      <Badge variant="secondary" className="text-[9px]">{m.source}</Badge>
                      <span className={m.confidence > 0.7 ? 'text-emerald-600' : m.confidence > 0.4 ? 'text-amber-600' : 'text-red-600'}>{(m.confidence * 100).toFixed(0)}%</span>
                      <span>{m.updated_at}</span>
                    </span>
                  </label>
                ))}
              </div>
              <p className="text-[10px] text-muted-foreground">选择要保留的主体（默认最高置信度），其余按渠道置信度合并后删除。</p>
            </div>
          ))}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setReconcileOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      <Dialog open={sqDialogOpen} onClose={() => setSqDialogOpen(false)} title="保存当前查询策略">
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">将当前筛选条件（类型：{typeFilter || '全部'}{searchQ ? `，关键字：${searchQ}` : ''}）存为可复用查询（"我想查"）。</p>
          <div><label className="text-sm font-medium">查询名称 *</label><Input value={sqName} onChange={e => setSqName(e.target.value)} placeholder="例如：华东区 Web 服务器" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setSqDialogOpen(false)} disabled={sqSubmitting}>取消</Button><Button onClick={handleSaveQuery} disabled={!sqName.trim() || sqSubmitting}><Bookmark className="h-4 w-4 mr-1" />保存{sqSubmitting ? '…' : ''}</Button></DialogFooter>
      </Dialog>

      <Dialog open={importOpen} onClose={() => setImportOpen(false)} title="导入结果">
        <div className="space-y-2">
          {importResult ? (
            <>
              <p className="text-sm">新建 <strong>{importResult.created}</strong> 条，更新 <strong>{importResult.updated}</strong> 条。</p>
              {importResult.failed.length > 0 ? (
                <div className="max-h-60 overflow-auto border rounded">
                  <table className="w-full text-sm">
                    <thead><tr className="bg-muted"><th className="px-2 py-1 text-left text-xs">行</th><th className="px-2 py-1 text-left text-xs">错误</th></tr></thead>
                    <tbody>
                      {importResult.failed.map((f, i) => (
                        <tr key={i} className="border-t"><td className="px-2 py-1 text-xs">{f.row}</td><td className="px-2 py-1 text-xs text-destructive">{f.error}</td></tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : <p className="text-sm text-muted-foreground">无失败行。</p>}
            </>
          ) : <p className="text-sm text-muted-foreground">无数据。</p>}
        </div>
        <DialogFooter><Button onClick={() => setImportOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      <ConfirmDialog
        open={batchDeleteOpen}
        onClose={() => setBatchDeleteOpen(false)}
        onConfirm={confirmBatchDelete}
        title="确认批量删除"
        description={`将永久删除选中的 ${checkedList().length} 个配置项，此操作不可撤销。`}
        confirmLabel="删除"
        loading={batchDeleteSubmitting}
      />

      <ConfirmDialog
        open={!!mergeTarget}
        onClose={() => setMergeTarget(null)}
        onConfirm={confirmReconcileMerge}
        title="确认实体调解合并"
        description={mergeTarget ? `合并「${mergeTarget.reason}」：保留 ${mergeTarget.members.find(m => m.ci_id === (reconcileKeep[mergeTarget.key]))?.name}，将其余 ${mergeTarget.members.filter(m => m.ci_id !== (reconcileKeep[mergeTarget.key])).length} 条合并为一条（被合并的 CI 将删除）。` : ''}
        confirmLabel="合并"
        loading={mergeSubmitting}
      />
    </div>
  )
}
