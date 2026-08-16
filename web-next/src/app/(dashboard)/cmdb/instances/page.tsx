'use client'

import { useEffect, useState, useRef } from 'react'
import { useSearchParams } from 'next/navigation'
import { useCMDBStore, type CIModel, type AttributeChange, type CIRelation } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime, apiFetch, apiList, apiBase, apiFetchRaw } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useCan } from '@/stores/permissions'
import TopologyGraph from '@/components/TopologyGraph'
import { SourceBadge, ProvenanceSummary } from '@/components/cmdb/SourceBadge'
import { Plus, Pencil, Trash2, Network, Search, ChevronLeft, ChevronRight, Link2, Unlink, AlertTriangle, Download, Upload, Archive, ArchiveRestore, Bookmark, GitMerge } from 'lucide-react'

interface TopologyNode { id: string; name: string; type: string }
interface TopologyEdge { source: string; target: string; type: string }
interface SavedQuery { id: string; name: string; type_id?: string; query: { q?: string; status?: string; attrs?: Record<string, string> } }
// 模型字段定义(与后端 AttrDef 对齐, 驱动 ref/enum/unique 渲染)
interface ModelFieldDef {
  name: string
  type: string
  required?: boolean
  description?: string
  options?: string[]
  unique?: boolean
  ref_model?: string
  ref_display?: string
}
interface ModelInfo { id: string; name: string; category: string; parent_id?: string; abstract?: boolean; fields?: ModelFieldDef[] }

const CI_TYPES = ['server', 'web-server', 'database', 'switch', 'loadbalancer', 'dns-server', 'firewall', 'storage']
const PAGE_SIZE = 20

function statusVariant(s: string): 'success' | 'destructive' | 'outline' | 'secondary' {
  if (s === 'active') return 'success'
  if (s === 'inactive') return 'outline'
  if (s === 'archived') return 'secondary'
  return 'destructive'
}

export default function CMDBPage() {
  const { items, relations, loading, fetch, search, create, update, remove, getAudit, fetchRelations, createRelation, deleteRelation } = useCMDBStore()
  const canDelete = useCan('cmdb:delete')
  const searchParams = useSearchParams()
  // CMDB 概览页/搜索结果通过 ?ci=<id> 深链进来，需要直接展开对应配置项详情
  const deepLinkCI = searchParams.get('ci')
  const deepLinkDone = useRef<string | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState<{ name: string; type_id: string; description: string; attributes: Record<string, any>; labels: Record<string, string> }>({ name: '', type_id: 'server', description: '', attributes: {}, labels: {} })
  const [searchQ, setSearchQ] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [archivedView, setArchivedView] = useState(false)
  const [savedQueries, setSavedQueries] = useState<SavedQuery[]>([])
  const [sqDialogOpen, setSqDialogOpen] = useState(false)
  const [sqName, setSqName] = useState('')
  const [page, setPage] = useState(0)
  const [selectedCI, setSelectedCI] = useState<CIModel | null>(null)
  const [detailTab, setDetailTab] = useState('attrs')
  const [auditLog, setAuditLog] = useState<AttributeChange[]>([])
  const [ciRelations, setCiRelations] = useState<CIRelation[]>([])
  const [topology, setTopology] = useState<{ nodes: TopologyNode[]; edges: TopologyEdge[] }>({ nodes: [], edges: [] })
  const [topoLoading, setTopoLoading] = useState(false)
  const [relDialogOpen, setRelDialogOpen] = useState(false)
  const [relForm, setRelForm] = useState({ source_id: '', target_id: '', relation_type: 'depends_on' })
  const [impactResult, setImpactResult] = useState<{ source_ci: string; affected_ci: string[]; has_cycle: boolean } | null>(null)
  // 配置漂移
  const [driftList, setDriftList] = useState<{ id: string; field: string; expected: string; actual: string; created_at: string }[]>([])
  const handleDrift = async (ciID: string) => {
    try { setDriftList(await apiList<{ id: string; field: string; expected: string; actual: string; created_at: string }>(`/cmdb/drift?ci_id=${ciID}`)) }
    catch { setDriftList([]) }
  }
  const resolveDrift = async (id: string) => {
    try {
      await apiFetch(`/cmdb/drift/${id}/resolve`, { method: 'POST' })
      toast.success('已标记解决')
      if (selectedCI) handleDrift(selectedCI.id)
    } catch (e: any) { toast.error('操作失败: ' + e.message) }
  }
  const [editAttrsOpen, setEditAttrsOpen] = useState(false)
  const [attrForm, setAttrForm] = useState<Record<string, string>>({})
  const fileRef = useRef<HTMLInputElement>(null)
  const [importOpen, setImportOpen] = useState(false)
  const [importResult, setImportResult] = useState<{ created: number; updated: number; failed: { row: number; error: string }[] } | null>(null)

  // 模型列表(类型下拉与 schema 驱动渲染), 替代硬编码 CI_TYPES
  const [models, setModels] = useState<ModelInfo[]>([])
  // 引用型字段的目标模型实例缓存: ref_model -> CI 列表
  const [refOptions, setRefOptions] = useState<Record<string, { id: string; name: string; attributes?: Record<string, any> }[]>>({})

  // 批量操作: 勾选的 CI ID 集合 + 批量改属性/批量建关系弹窗状态
  const [checked, setChecked] = useState<Set<string>>(new Set())
  const [batchAttrOpen, setBatchAttrOpen] = useState(false)
  const [batchAttrs, setBatchAttrs] = useState<Record<string, string>>({})
  const [batchRelOpen, setBatchRelOpen] = useState(false)
  const [batchRelForm, setBatchRelForm] = useState({ target_id: '', relation_type: 'depends_on' })

  const toggleCheck = (id: string) => {
    setChecked(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  const checkedList = () => Array.from(checked)

  // 批量删除(二次确认)
  const handleBatchDelete = async () => {
    const ids = checkedList()
    if (!ids.length) return
    if (!confirm(`确认删除选中的 ${ids.length} 个资产？此操作不可撤销。`)) return
    try {
      const res = await apiFetch<{ deleted: number; failed: { id: string; error: string }[] }>('/cmdb/ci/batch-delete', { method: 'POST', body: JSON.stringify({ ids }) })
      toast.success(`已删除 ${res.deleted} 个资产${res.failed?.length ? `，${res.failed.length} 个失败` : ''}`)
      setChecked(new Set()); loadData()
    } catch (e: any) { toast.error('批量删除失败: ' + e.message) }
  }

  // 批量改属性
  const handleBatchUpdate = async () => {
    const ids = checkedList()
    if (!ids.length) return
    const attrs: Record<string, any> = {}
    for (const [k, v] of Object.entries(batchAttrs)) {
      if (!k.trim()) continue
      try { attrs[k.trim()] = JSON.parse(v) } catch { attrs[k.trim()] = v }
    }
    if (!Object.keys(attrs).length) { toast.error('请至少填写一个属性'); return }
    try {
      const res = await apiFetch<{ updated: number; failed: { id: string; error: string }[] }>('/cmdb/ci/batch-update', { method: 'POST', body: JSON.stringify({ ids, attrs }) })
      toast.success(`已更新 ${res.updated} 个资产${res.failed?.length ? `，${res.failed.length} 个失败` : ''}`)
      setBatchAttrs({}); setBatchAttrOpen(false); setChecked(new Set()); loadData()
    } catch (e: any) { toast.error('批量更新失败: ' + e.message) }
  }

  // 批量建关系(源 = 勾选集合)
  const handleBatchCreateRel = async () => {
    const ids = checkedList()
    if (!ids.length || !batchRelForm.target_id) return
    const relations = ids.map(source_id => ({ source_id, target_id: batchRelForm.target_id, relation_type: batchRelForm.relation_type }))
    try {
      const res = await apiFetch<{ created: number; failed: { source_id: string; target_id: string; error: string }[] }>('/cmdb/relation/batch', { method: 'POST', body: JSON.stringify({ relations }) })
      toast.success(`已创建 ${res.created} 条关系${res.failed?.length ? `，${res.failed.length} 条失败(可能成环)` : ''}`)
      setBatchRelOpen(false); fetchRelations(); loadData()
    } catch (e: any) { toast.error('批量建关系失败: ' + e.message) }
  }

  // 实体调解合并(IRE): 疑似重复候选组 + 合并
  const [reconcileOpen, setReconcileOpen] = useState(false)
  const [reconcileLoading, setReconcileLoading] = useState(false)
  const [reconcileGroups, setReconcileGroups] = useState<{
    key: string; reason: string;
    members: { ci_id: string; name: string; type_id: string; source: string; confidence: number; updated_at: string }[]
  }[]>([])
  // 每组选择的保留主体 ci_id
  const [reconcileKeep, setReconcileKeep] = useState<Record<string, string>>({})

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

  const handleReconcileMerge = async (g: typeof reconcileGroups[number]) => {
    const keep = reconcileKeep[g.key]
    const mergeIds = g.members.map(m => m.ci_id).filter(id => id !== keep)
    if (!keep || !mergeIds.length) return
    if (!confirm(`合并「${g.reason}」：保留 ${g.members.find(m => m.ci_id === keep)?.name}，合并 ${mergeIds.length} 条为一条？（被合并的 CI 将删除）`)) return
    try {
      const res = await apiFetch<{ kept_id: string; merged_count: number; updated_fields: string[] }>('/cmdb/reconcile/merge', { method: 'POST', body: JSON.stringify({ keep_id: keep, merge_ids: mergeIds }) })
      toast.success(`已合并 ${res.merged_count} 条，更新字段: ${res.updated_fields.join(', ') || '无'}`)
      openReconcile(); loadData()
    } catch (e: any) { toast.error('合并失败: ' + e.message) }
  }

  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000')
      .then(list => setModels(list))
      .catch(() => setModels([]))
  }, [])

  useEffect(() => {
    fetch({ limit: PAGE_SIZE, offset: page * PAGE_SIZE, sort_by: 'updated_at', sort_order: 'desc' })
    fetchRelations()
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

  // 引用型字段: 按需加载目标模型实例作为下拉选项(缓存, 避免重复请求)
  const ensureRefOptions = async (refModel: string) => {
    if (!refModel || refOptions[refModel]) return
    try {
      const list = await apiList<{ id: string; name: string; attributes?: Record<string, any> }>(`/cmdb/ci?type_id=${refModel}&limit=200`)
      setRefOptions(s => ({ ...s, [refModel]: list }))
    } catch { /* 忽略加载失败, 下拉保持空 */ }
  }

  const handleArchive = async (id: string) => {
    try {
      await apiFetch(`/cmdb/ci/${id}/archive`, { method: 'POST' })
      toast.success('已归档')
      loadData()
    } catch (e: any) { toast.error('归档失败: ' + e.message) }
  }

  const handleRestore = async (id: string) => {
    try {
      await apiFetch(`/cmdb/ci/${id}/restore`, { method: 'POST' })
      toast.success('已恢复')
      loadData()
    } catch (e: any) { toast.error('恢复失败: ' + e.message) }
  }

  const handleSaveQuery = async () => {
    if (!sqName.trim()) { toast.error('请输入查询名称'); return }
    try {
      await apiFetch('/cmdb/saved-queries', {
        method: 'POST',
        body: JSON.stringify({ name: sqName.trim(), type_id: typeFilter || undefined, query: { q: searchQ || undefined } }),
      })
      toast.success('已保存查询策略')
      setSqDialogOpen(false); setSqName('')
      loadSavedQueries()
    } catch (e: any) { toast.error('保存失败: ' + e.message) }
  }

  const handleApplySavedQuery = (sq: SavedQuery) => {
    setTypeFilter(sq.type_id || '')
    setSearchQ(sq.query?.q || '')
    setPage(0)
    loadData()
  }

  const handleSearch = () => {
    if (searchQ) search(searchQ, typeFilter, archivedView ? 'true' : 'false')
    else loadData()
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

  const handleSubmit = async () => {
    if (editId) await update(editId, form)
    else await create(form)
    setDialogOpen(false); setEditId(null); setForm({ name: '', type_id: 'server', description: '', attributes: {}, labels: {} })
    loadData()
  }

  const handleEdit = (item: CIModel) => {
    setEditId(item.id)
    setForm({ name: item.name, type_id: item.type_id, description: item.description || '', attributes: item.attributes || {}, labels: item.labels || {} })
    setDialogOpen(true)
  }

  const handleSelectCI = async (item: CIModel) => {
    setSelectedCI(item)
    setDetailTab('attrs')
    setImpactResult(null)
    try {
      // 拉详情(含聚合字段 aggregates)合并到选中项
      const [audit, rels, detail] = await Promise.all([
        getAudit(item.id),
        apiList<CIRelation>('/cmdb/relation'),
        apiFetch<CIModel>('/cmdb/ci/' + item.id).catch(() => null),
      ])
      if (detail) setSelectedCI(detail)
      setAuditLog(audit || [])
      setCiRelations((rels || []).filter((r: CIRelation) => r.source_id === item.id || r.target_id === item.id))
    } catch { setAuditLog([]); setCiRelations([]) }
  }

  // 深链选中：列表加载完成后按 id 命中一次即可，避免用户手动切换后被反复拉回
  useEffect(() => {
    if (!deepLinkCI || deepLinkDone.current === deepLinkCI) return
    const hit = items.find(i => i.id === deepLinkCI)
    if (!hit) return
    deepLinkDone.current = deepLinkCI
    handleSelectCI(hit)
  }, [deepLinkCI, items])

  const handleTopology = async (ciId: string) => {
    setTopoLoading(true); setDetailTab('topo')
    try {
      const data = await apiFetch<any>(`/cmdb/topology/${ciId}`)
      if (data?.nodes) setTopology(data)
      else {
        const relNodes = new Set<string>()
        const relEdges: TopologyEdge[] = []
        ciRelations.forEach(r => { relNodes.add(r.source_id); relNodes.add(r.target_id); relEdges.push({ source: r.source_id, target: r.target_id, type: r.relation_type }) })
        setTopology({
          nodes: Array.from(relNodes).map(id => {
            const ci = items.find(i => i.id === id)
            return { id, name: ci?.name || id.slice(0, 8), type: ci?.type_id || 'host' }
          }),
          edges: relEdges,
        })
      }
    } catch {
      setTopology({ nodes: [{ id: ciId, name: selectedCI?.name || ciId, type: selectedCI?.type_id || 'host' }], edges: [] })
    } finally { setTopoLoading(false) }
  }

  const handleImpact = async (ciId: string) => {
    setDetailTab('impact'); setTopoLoading(true)
    try {
      const result = await apiFetch<any>(`/cmdb/impact/${ciId}`)
      setImpactResult(result)
    } catch { setImpactResult(null) } finally { setTopoLoading(false) }
  }

  const handleCreateRel = async () => {
    await createRelation(relForm)
    setRelDialogOpen(false)
    fetchRelations()
    if (selectedCI) handleSelectCI(selectedCI)
  }

  const handleDeleteRel = async (id: string) => {
    await deleteRelation(id)
    fetchRelations()
    if (selectedCI) handleSelectCI(selectedCI)
  }

  const handleEditAttrs = () => {
    if (!selectedCI) return
    const attrs: Record<string, string> = {}
    for (const [k, v] of Object.entries(selectedCI.attributes || {})) {
      attrs[k] = typeof v === 'string' ? v : JSON.stringify(v)
    }
    setAttrForm(attrs); setEditAttrsOpen(true)
  }

  const handleSaveAttrs = async () => {
    if (!selectedCI) return
    const parsed: Record<string, any> = {}
    for (const [k, v] of Object.entries(attrForm)) {
      try { parsed[k] = JSON.parse(v) } catch { parsed[k] = v }
    }
    await update(selectedCI.id, { ...selectedCI, name: selectedCI.name, description: selectedCI.description, attributes: parsed, labels: selectedCI.labels, channel: 100 })
    setEditAttrsOpen(false)
    const items = await apiList<CIModel>('/cmdb/ci')
    const updated = items.find(i => i.id === selectedCI.id)
    if (updated) setSelectedCI(updated)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">配置项管理</h1><p className="text-sm text-muted-foreground">按模型树浏览配置项实例，点击模型查看其下实例</p></div>
        <div className="flex gap-2">
          <Button variant="outline" onClick={handleExport}><Download className="h-4 w-4" />导出 CSV</Button>
          <Button variant="outline" onClick={() => fileRef.current?.click()}><Upload className="h-4 w-4" />导入 CSV</Button>
          <Button variant="outline" onClick={() => setSqDialogOpen(true)}><Bookmark className="h-4 w-4" />保存查询</Button>
          <Button variant="outline" onClick={openReconcile}><GitMerge className="h-4 w-4" />疑似重复</Button>
          <Button onClick={() => { setEditId(null); setForm({ name: '', type_id: 'server', description: '', attributes: {}, labels: {} }); setDialogOpen(true) }}><Plus className="h-4 w-4" />新建配置项</Button>
        </div>
      </div>

      <div className="flex gap-2 items-center">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
          <Input className="pl-8" placeholder="搜索资产名称..." value={searchQ} onChange={e => setSearchQ(e.target.value)} onKeyDown={e => e.key === 'Enter' && handleSearch()} />
        </div>
        <select className="h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value={typeFilter} onChange={e => { setTypeFilter(e.target.value); setPage(0) }}>
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
        {/* 左栏: 模型树(分类 → 模型 → 子模型) */}
        <div className="lg:col-span-1 space-y-4">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">模型结构</CardTitle></CardHeader>
            <CardContent className="p-0 max-h-[70vh] overflow-y-auto">
              {(() => {
                const roots = models.filter(m => !m.parent_id)
                const childrenOf = (id: string) => models.filter(m => m.parent_id === id)
                const byCat = new Map<string, ModelInfo[]>()
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
                              <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
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

        {/* 中间栏: 所选模型的实例列表 */}
        <div className="lg:col-span-1 space-y-4">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">实例列表 ({items.length})</CardTitle></CardHeader>
            <CardContent className="p-0">
              {loading && items.length === 0 ? <p className="px-4 text-sm text-muted-foreground">加载中...</p> : items.length === 0 ? <p className="px-4 text-sm text-muted-foreground">暂无实例。</p> : (
                <div className="flex flex-col">
                  {/* 批量操作栏 */}
                  <div className="flex items-center gap-1.5 px-3 py-2 border-b bg-muted/30">
                    <input type="checkbox" className="rounded" checked={checked.size > 0 && checked.size === items.length}
                      onChange={e => setChecked(e.target.checked ? new Set(items.map(i => i.id)) : new Set())} title="全选本页" />
                    <span className="text-xs text-muted-foreground shrink-0">{checked.size > 0 ? `已选 ${checked.size} 项` : '勾选批量操作'}</span>
                    {checked.size > 0 && (
                      <div className="flex gap-1 ml-auto">
                        <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => setBatchAttrOpen(true)}><Pencil className="h-3 w-3 mr-1" />改属性</Button>
                        <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => setBatchRelOpen(true)}><Link2 className="h-3 w-3 mr-1" />建关系</Button>
                        <Button variant="outline" size="sm" className="h-6 text-xs text-destructive" onClick={handleBatchDelete}><Trash2 className="h-3 w-3 mr-1" />删除</Button>
                      </div>
                    )}
                  </div>
                  {items.map(item => (
                    <div key={item.id} onClick={() => handleSelectCI(item)}
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selectedCI?.id === item.id ? 'bg-accent border-l-2 border-l-emerald-500' : ''}`}>
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2 min-w-0">
                          <input type="checkbox" className="rounded shrink-0" checked={checked.has(item.id)}
                            onClick={e => e.stopPropagation()}
                            onChange={() => toggleCheck(item.id)} title="选择以批量操作" />
                          <span className="font-medium text-sm truncate">{item.name}</span>
                        </div>
                        <div className="flex gap-1 shrink-0">
                          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); handleEdit(item) }}><Pencil className="h-3 w-3" /></Button>
                          {item.status === 'archived'
                            ? <Button variant="ghost" size="icon" className="h-6 w-6" title="恢复" onClick={e => { e.stopPropagation(); handleRestore(item.id) }}><ArchiveRestore className="h-3 w-3" /></Button>
                            : <Button variant="ghost" size="icon" className="h-6 w-6" title="归档" onClick={e => { e.stopPropagation(); handleArchive(item.id) }}><Archive className="h-3 w-3" /></Button>}
                          {canDelete && <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); remove(item.id); setSelectedCI(null) }}><Trash2 className="h-3 w-3" /></Button>}
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

        <div className="lg:col-span-2">
          {selectedCI ? (
            <Card>
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-lg">{selectedCI.name}</CardTitle>
                    <p className="text-xs text-muted-foreground">ID: {selectedCI.id.slice(0, 12)}... · {selectedCI.type_id} · {selectedCI.status}</p>
                  </div>
                  <div className="flex gap-1">
                    <Button variant="outline" size="sm" onClick={handleEditAttrs}>编辑属性</Button>
                    {selectedCI.status === 'archived'
                      ? <Button variant="outline" size="sm" onClick={() => handleRestore(selectedCI.id)}><ArchiveRestore className="h-3 w-3" />恢复</Button>
                      : <Button variant="outline" size="sm" onClick={() => handleArchive(selectedCI.id)}><Archive className="h-3 w-3" />归档</Button>}
                    <Button variant="outline" size="sm" onClick={e => { e.stopPropagation(); handleEdit(selectedCI) }}><Pencil className="h-3 w-3" /></Button>
                    {canDelete && <Button variant="outline" size="sm" onClick={() => { remove(selectedCI.id); setSelectedCI(null) }}><Trash2 className="h-3 w-3" /></Button>}
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <Tabs value={detailTab} onValueChange={setDetailTab}>
                  <TabsList>
                    <TabsTrigger value="attrs">属性</TabsTrigger>
                    <TabsTrigger value="rels">关系 ({ciRelations.length})</TabsTrigger>
                    <TabsTrigger value="audit">审计 ({auditLog.length})</TabsTrigger>
                    <TabsTrigger value="topo" onClick={() => handleTopology(selectedCI.id)}>拓扑</TabsTrigger>
                    <TabsTrigger value="impact" onClick={() => handleImpact(selectedCI.id)}><AlertTriangle className="w-3 h-3 mr-1" />影响</TabsTrigger>
                    <TabsTrigger value="drift" onClick={() => handleDrift(selectedCI.id)}>漂移 ({driftList.length})</TabsTrigger>
                  </TabsList>

                  <TabsContent value="attrs" className="mt-4 space-y-3">
                    <div className="grid grid-cols-2 gap-2 text-sm">
                      <div><span className="text-muted-foreground">名称:</span> {selectedCI.name}</div>
                      <div><span className="text-muted-foreground">类型:</span> <Badge variant="secondary">{selectedCI.type_id}</Badge></div>
                      <div><span className="text-muted-foreground">状态:</span> <Badge variant={statusVariant(selectedCI.status)}>{selectedCI.status}</Badge></div>
                      <div><span className="text-muted-foreground">信道:</span> {selectedCI.channel}</div>
                      <div className="col-span-2"><span className="text-muted-foreground">描述:</span> {selectedCI.description || '无'}</div>
                      <div className="col-span-2"><span className="text-muted-foreground">创建:</span> {formatTime(selectedCI.created_at)}</div>
                      <div className="col-span-2"><span className="text-muted-foreground">更新:</span> {formatTime(selectedCI.updated_at)}</div>
                    </div>
                    {(selectedCI as any)?.aggregates && Object.keys((selectedCI as any).aggregates).length > 0 && (
                      <div className="border rounded-md p-3 bg-muted/20 space-y-1">
                        <p className="text-xs font-medium text-muted-foreground">聚合统计（自动计算）</p>
                        <div className="flex flex-wrap gap-2">
                          {Object.entries((selectedCI as any).aggregates).map(([k, v]) => (
                            <span key={k} className="text-xs"><span className="text-muted-foreground">{k}:</span> <strong>{String(v)}</strong></span>
                          ))}
                        </div>
                      </div>
                    )}
                    <ProvenanceSummary meta={selectedCI.attribute_meta} />
                    {Object.keys(selectedCI.attributes || {}).length > 0 && (
                      <div>
                        <h4 className="text-sm font-medium mb-2">自定义属性</h4>
                        <table className="w-full text-sm border">
                          <thead><tr className="bg-muted"><th className="px-2 py-1 text-left text-xs">属性</th><th className="px-2 py-1 text-left text-xs">值</th><th className="px-2 py-1 text-left text-xs">来源</th><th className="px-2 py-1 text-left text-xs">访问</th></tr></thead>
                          <tbody>
                            {Object.entries(selectedCI.attributes || {}).map(([k, v]) => {
                              const meta = selectedCI.attribute_meta?.[k]
                              const low = meta ? meta.confidence < 0.5 : false
                              const access = (selectedCI as any).field_access?.[k]
                              return (
                              <tr key={k} className={`border-t ${low ? 'bg-amber-50 dark:bg-amber-500/5' : ''}`}><td className="px-2 py-1 text-xs font-medium">{k}</td><td className="px-2 py-1 text-xs">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</td><td className="px-2 py-1"><SourceBadge meta={meta} /></td><td className="px-2 py-1">{access === 'readonly' && <Badge variant="outline" className="text-[9px] text-amber-500">只读</Badge>}</td></tr>
                              )
                            })}
                          </tbody>
                        </table>
                      </div>
                    )}
                    {Object.keys(selectedCI.labels || {}).length > 0 && (
                      <div>
                        <h4 className="text-sm font-medium mb-2">标签</h4>
                        <div className="flex gap-1 flex-wrap">
                          {Object.entries(selectedCI.labels || {}).map(([k, v]) => (
                            <Badge key={k} variant="outline" className="text-xs">{k}={v}</Badge>
                          ))}
                        </div>
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent value="rels" className="mt-4">
                    {ciRelations.length === 0 ? <p className="text-sm text-muted-foreground">无关联关系。</p> : (
                      <table className="w-full text-sm">
                        <thead><tr className="border-b text-muted-foreground"><th className="pb-2 text-left text-xs">源</th><th className="pb-2 text-left text-xs">类型</th><th className="pb-2 text-left text-xs">目标</th><th className="pb-2 text-left text-xs">操作</th></tr></thead>
                        <tbody>
                          {ciRelations.map(rel => {
                            const src = items.find(i => i.id === rel.source_id)
                            const tgt = items.find(i => i.id === rel.target_id)
                            return (
                              <tr key={rel.id} className="border-b last:border-0">
                                <td className="py-1 text-xs">{src?.name || rel.source_id.slice(0, 8)}</td>
                                <td className="py-1 text-xs"><Badge variant="outline" className="text-[10px]">{rel.relation_type}</Badge></td>
                                <td className="py-1 text-xs">{tgt?.name || rel.target_id.slice(0, 8)}</td>
                                <td className="py-1">{canDelete && <Button variant="ghost" size="icon" className="h-5 w-5" onClick={() => handleDeleteRel(rel.id)}><Unlink className="h-3 w-3" /></Button>}</td>
                              </tr>
                            )
                          })}
                        </tbody>
                      </table>
                    )}
                  </TabsContent>

                  <TabsContent value="audit" className="mt-4">
                    {auditLog.length === 0 ? <p className="text-sm text-muted-foreground">无变更记录。</p> : (
                      <table className="w-full text-sm">
                        <thead><tr className="border-b text-muted-foreground"><th className="pb-2 text-left text-xs">属性</th><th className="pb-2 text-left text-xs">旧值</th><th className="pb-2 text-left text-xs">新值</th><th className="pb-2 text-left text-xs">信道</th><th className="pb-2 text-left text-xs">时间</th></tr></thead>
                        <tbody>
                          {auditLog.map((ch, i) => (
                            <tr key={i} className="border-b last:border-0">
                              <td className="py-1 text-xs font-medium">{ch.attr_name}</td>
                              <td className="py-1 text-xs text-muted-foreground">{String(ch.old_value || '').slice(0, 40)}</td>
                              <td className="py-1 text-xs">{String(ch.new_value || '').slice(0, 40)}</td>
                              <td className="py-1 text-xs"><Badge variant="outline" className="text-[10px]">{ch.channel}</Badge></td>
                              <td className="py-1 text-xs text-muted-foreground">{formatTime(ch.changed_at)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    )}
                  </TabsContent>

                  <TabsContent value="topo" className="mt-4">
                    {topoLoading ? <p className="text-sm text-muted-foreground">加载中...</p> : topology.nodes.length === 0 ? <p className="text-sm text-muted-foreground">无拓扑数据。</p> : (
                      <div className="border rounded-lg bg-muted/20" style={{ minHeight: 300 }}>
                        <TopologyGraph data={topology} width={600} height={300} />
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent value="impact" className="mt-4">
                    {topoLoading ? <p className="text-sm text-muted-foreground">分析中...</p> : !impactResult ? <p className="text-sm text-muted-foreground">无法分析影响范围。</p> : (
                      <div className="space-y-3">
                        <div className="flex items-center gap-2">
                          <AlertTriangle className="h-4 w-4 text-amber-400" />
                          <span className="text-sm font-medium">影响分析: {selectedCI.name}</span>
                          {impactResult.has_cycle && <Badge variant="destructive" className="text-xs">检测到拓扑环</Badge>}
                        </div>
                        <p className="text-sm">深度影响 <strong>{impactResult.affected_ci?.length || 0}</strong> 个资产:</p>
                        <div className="flex flex-wrap gap-1">
                          {(impactResult.affected_ci || []).map(ciId => {
                            const ci = items.find(i => i.id === ciId)
                            return <Badge key={ciId} variant="outline" className="text-xs">{ci?.name || ciId.slice(0, 8)}</Badge>
                          })}
                        </div>
                      </div>
                    )}
                  </TabsContent>

                  <TabsContent value="drift" className="mt-4">
                    {driftList.length === 0 ? <p className="text-sm text-muted-foreground">无配置漂移（发现值均与 CMDB 一致）</p> : (
                      <div className="space-y-2">
                        {driftList.map(d => (
                          <div key={d.id} className="border rounded-md p-3 text-xs flex items-center gap-3">
                            <div className="flex-1 min-w-0">
                              <p className="font-medium">{d.field}</p>
                              <p className="text-muted-foreground truncate">CMDB: <span className="text-emerald-600">{d.expected}</span> → 发现: <span className="text-amber-600">{d.actual}</span></p>
                              <p className="text-[10px] text-muted-foreground">{d.created_at?.slice(0, 19)?.replace('T', ' ')}</p>
                            </div>
                            <Button variant="outline" size="sm" className="h-6 text-xs shrink-0" onClick={() => resolveDrift(d.id)}>已处理</Button>
                          </div>
                        ))}
                      </div>
                    )}
                  </TabsContent>
                </Tabs>
              </CardContent>
            </Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]">
              <CardContent className="text-center text-muted-foreground">
                <p>选择一个资产查看详情</p>
              </CardContent>
            </Card>
          )}
        </div>
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editId ? '编辑资产' : '新建配置项'}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div><label className="text-sm font-medium">名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">类型</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.type_id} onChange={e => setForm(f => ({ ...f, type_id: e.target.value }))}>
              {(models.length ? models : CI_TYPES.map(t => ({ id: t, name: t }))).map(m => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">描述</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>

          {/* schema 驱动字段渲染: ref → 引用选择器 / enum → 选项下拉 / unique → 唯一提示 */}
          {(() => {
            const schema = models.find(m => m.id === form.type_id)?.fields || []
            if (schema.length === 0) return null
            return (
              <div className="space-y-2 border rounded-md p-3 bg-muted/20">
                <p className="text-xs font-medium text-muted-foreground">模型字段（{form.type_id}）</p>
                {schema.map(f => {
                  const setAttr = (v: any) => setForm(fm => ({ ...fm, attributes: { ...fm.attributes, [f.name]: v } }))
                  if (f.type === 'ref' && f.ref_model) {
                    const opts = refOptions[f.ref_model] || []
                    return (
                      <div key={f.name}>
                        <label className="text-xs font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一）'}</label>
                        <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1"
                          value={(form.attributes[f.name] as string) || ''}
                          onChange={e => {
                            setAttr(e.target.value)
                            if (f.ref_model) ensureRefOptions(f.ref_model)
                            // 自动填充: 选中目标实例后, 把声明的目标属性值填入本 CI
                            if ((f as any).autofill?.length && f.ref_model) {
                              const target = refOptions[f.ref_model]?.find(o => o.id === e.target.value)
                              if (target?.attributes) {
                                setForm(fm => {
                                  const attrs = { ...fm.attributes }
                                  const tattrs = target.attributes || {}
                                  for (const k of (f as any).autofill as string[]) {
                                    if (tattrs[k] !== undefined) attrs[k] = tattrs[k]
                                  }
                                  return { ...fm, attributes: attrs }
                                })
                              }
                            }
                          }}>
                          <option value="">选择 {f.ref_model} 实例...</option>
                          {opts.map(o => <option key={o.id} value={o.id}>{o.name || o.id}</option>)}
                        </select>
                        <p className="text-[10px] text-muted-foreground mt-0.5">引用 {f.ref_model} 模型实例（{f.description || ''}）</p>
                      </div>
                    )
                  }
                  if (f.type === 'enum' && f.options?.length) {
                    return (
                      <div key={f.name}>
                        <label className="text-xs font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一）'}</label>
                        <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1"
                          value={(form.attributes[f.name] as string) || ''}
                          onChange={e => setAttr(e.target.value)}>
                          <option value="">选择...</option>
                          {(f.options || []).map(o => <option key={o} value={o}>{o}</option>)}
                        </select>
                      </div>
                    )
                  }
                  return (
                    <div key={f.name}>
                      <label className="text-xs font-medium">{f.name}{f.required && ' *'}{f.unique && '（唯一，不可重复）'}</label>
                      <Input className="mt-1 h-9 text-xs" value={(form.attributes[f.name] as string) || ''}
                        onChange={e => setAttr(f.type === 'number' ? Number(e.target.value) : e.target.value)}
                        placeholder={f.description || ''} />
                    </div>
                  )
                })}
              </div>
            )
          })()}

          <div>
            <label className="text-sm font-medium">自定义属性 (JSON)</label>
            <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
              value={JSON.stringify(form.attributes, null, 2)}
              onChange={e => { try { setForm(f => ({ ...f, attributes: JSON.parse(e.target.value) })) } catch {}}}
              placeholder='{"ip": "10.0.0.1", "os": "Ubuntu 22.04"}' />
          </div>
          <div>
            <label className="text-sm font-medium">标签 (key=value, 逗号分隔)</label>
            <Input value={Object.entries(form.labels || {}).map(([k,v]) => `${k}=${v}`).join(',')}
              onChange={e => {
                const labels: Record<string, string> = {}
                e.target.value.split(',').forEach(pair => {
                  const [k, v] = pair.split('=')
                  if (k && v) labels[k.trim()] = v.trim()
                })
                setForm(f => ({ ...f, labels }))
              }}
              placeholder="env=prod,region=us-east-1" />
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSubmit} disabled={!form.name}>{editId ? '保存' : '创建'}</Button></DialogFooter>
      </Dialog>

      <Dialog open={relDialogOpen} onClose={() => setRelDialogOpen(false)} title="新建关联">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">源资产</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={relForm.source_id} onChange={e => setRelForm(f => ({ ...f, source_id: e.target.value }))}>
              <option value="">选择资产</option>
              {items.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">目标资产</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={relForm.target_id} onChange={e => setRelForm(f => ({ ...f, target_id: e.target.value }))}>
              <option value="">选择资产</option>
              {items.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">关联类型</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={relForm.relation_type} onChange={e => setRelForm(f => ({ ...f, relation_type: e.target.value }))}>
              <option value="depends_on">depends_on</option>
              <option value="connects_to">connects_to</option>
              <option value="contains">contains</option>
              <option value="backup_of">backup_of</option>
            </select>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRelDialogOpen(false)}>取消</Button><Button onClick={handleCreateRel} disabled={!relForm.source_id || !relForm.target_id}>创建</Button></DialogFooter>
      </Dialog>

      <Dialog open={editAttrsOpen} onClose={() => setEditAttrsOpen(false)} title="编辑自定义属性">        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          {Object.entries(attrForm).map(([k, v]) => {
            const readonly = (selectedCI as any)?.field_access?.[k] === 'readonly'
            return (
            <div key={k} className="flex gap-2 items-center">
              <Input className="flex-1 text-xs" value={k} disabled={readonly} onChange={e => {
                const newAttrs = { ...attrForm }
                delete newAttrs[k]
                newAttrs[e.target.value] = v
                setAttrForm(newAttrs)
              }} placeholder="属性名" />
              <Input className="flex-1 text-xs" value={v} disabled={readonly} onChange={e => setAttrForm(f => ({ ...f, [k]: e.target.value }))} placeholder="值" />
              {readonly ? <span className="w-8 shrink-0 text-[9px] text-amber-500 text-center">只读</span> : <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={() => {
                const newAttrs = { ...attrForm }; delete newAttrs[k]; setAttrForm(newAttrs)
              }}><Trash2 className="h-3 w-3" /></Button>}
            </div>
            )
          })}
          <Button variant="outline" size="sm" onClick={() => setAttrForm(f => ({ ...f, '': '' }))}><Plus className="h-3 w-3 mr-1" />添加属性</Button>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setEditAttrsOpen(false)}>取消</Button><Button onClick={handleSaveAttrs}>保存</Button></DialogFooter>
      </Dialog>

      {/* 批量改属性 */}
      <Dialog open={batchAttrOpen} onClose={() => setBatchAttrOpen(false)} title={`批量修改属性（${checked.size} 个资产）`}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <p className="text-xs text-muted-foreground">填写要覆盖的属性（key=value），未填写的属性保持不变；支持 JSON 值。</p>
          {Object.entries(batchAttrs).map(([k, v]) => (
            <div key={k} className="flex gap-2 items-center">
              <Input className="flex-1 text-xs" value={k} disabled={!k} onChange={e => {
                const next = { ...batchAttrs }; delete next[k]; next[e.target.value] = v; setBatchAttrs(next)
              }} placeholder="属性名" />
              <Input className="flex-1 text-xs" value={v} onChange={e => setBatchAttrs(f => ({ ...f, [k]: e.target.value }))} placeholder="值" />
              <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={() => { const next = { ...batchAttrs }; delete next[k]; setBatchAttrs(next) }}><Trash2 className="h-3 w-3" /></Button>
            </div>
          ))}
          <Button variant="outline" size="sm" onClick={() => setBatchAttrs(f => ({ ...f, '': '' }))}><Plus className="h-3 w-3 mr-1" />添加属性</Button>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setBatchAttrOpen(false)}>取消</Button><Button onClick={handleBatchUpdate}>批量更新</Button></DialogFooter>
      </Dialog>

      {/* 批量建关系 */}
      <Dialog open={batchRelOpen} onClose={() => setBatchRelOpen(false)} title={`批量建关系（${checked.size} 个源资产 → 1 个目标）`}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">目标资产</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={batchRelForm.target_id} onChange={e => setBatchRelForm(f => ({ ...f, target_id: e.target.value }))}>
              <option value="">选择目标资产</option>
              {items.map(i => <option key={i.id} value={i.id}>{i.name}</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">关联类型</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={batchRelForm.relation_type} onChange={e => setBatchRelForm(f => ({ ...f, relation_type: e.target.value }))}>
              <option value="depends_on">depends_on</option>
              <option value="hosts">hosts</option>
              <option value="connects">connects</option>
            </select>
          </div>
          <p className="text-xs text-muted-foreground">将为每个勾选的资产创建一条指向该目标的关系（逐条环路检测，成环的会失败并跳过）。</p>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setBatchRelOpen(false)}>取消</Button><Button onClick={handleBatchCreateRel} disabled={!batchRelForm.target_id}>批量创建</Button></DialogFooter>
      </Dialog>

      <input ref={fileRef} type="file" accept=".csv" className="hidden" onChange={handleImportFile} />

      {/* 实体调解合并: 疑似重复候选组 */}
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
                <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => handleReconcileMerge(g)}><GitMerge className="h-3 w-3 mr-1" />合并</Button>
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
        <DialogFooter><Button variant="outline" onClick={() => setSqDialogOpen(false)}>取消</Button><Button onClick={handleSaveQuery} disabled={!sqName.trim()}><Bookmark className="h-4 w-4 mr-1" />保存</Button></DialogFooter>
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
    </div>
  )
}
