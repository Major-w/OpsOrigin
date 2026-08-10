'use client'

// 服务目录定义 —— 管理员视角：
// 目录项在此定义并「上线」后，才会出现在员工「服务门户」中；
// 服务对象(用户/用户组)决定哪些人能在门户看到它，为空 = 全员可见。
// 本页只做定义与生命周期管理，发起工单统一收敛到服务门户，避免两处入口语义重叠。
import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Package, Plus, Trash2, Pencil, Search, Users, ArrowUpCircle, ArrowDownCircle, Globe, FolderTree, FileText, GripVertical, Star, ChevronRight } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useITSMStore } from '@/stores/itsm'
import { useIAMStore } from '@/stores/iam'
import { useUserStore } from '@/stores/user'

interface Audience { user_ids?: string[]; group_ids?: string[] }
interface CatalogUsage { catalog_id: string; ticket_count: number; avg_satisfaction: number; rated_count: number }
interface ServiceItem {
  id: string
  name: string
  description: string
  category: string
  parent_id?: string
  level?: number
  sort_order?: number
  process_id?: string
  form_scope?: string
  enabled: boolean
  audience?: Audience
}

const CATEGORIES = [
  { value: 'change', label: '变更管理' },
  { value: 'incident', label: '事件管理' },
  { value: 'request', label: '服务请求' },
  { value: 'problem', label: '问题管理' },
  { value: 'release', label: '发布管理' },
]
const catLabel = (v: string) => CATEGORIES.find(c => c.value === v)?.label || v

const emptyDraft = (): ServiceItem => ({
  id: '', name: '', description: '', category: 'change', parent_id: '', level: 1, sort_order: 0, process_id: '', enabled: false,
  audience: { user_ids: [], group_ids: [] },
})

export default function ITSMCatalogPage() {
  const { processes, fetchProcesses } = useITSMStore()
  const { groups, fetchGroups } = useIAMStore()
  const { users, fetch: fetchUsers } = useUserStore()

  const [items, setItems] = useState<ServiceItem[]>([])
  const [keyword, setKeyword] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [draft, setDraft] = useState<ServiceItem>(emptyDraft())
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ServiceItem | null>(null)
  const [audTab, setAudTab] = useState<'group' | 'user'>('group')
  const [audFilter, setAudFilter] = useState('')
  const [usageMap, setUsageMap] = useState<Record<string, CatalogUsage>>({})
  const [dragId, setDragId] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string>('')

  const load = () => {
    apiList<ServiceItem>('/itsm/catalog').then(list => setItems(list || [])).catch(() => toast.error('加载失败'))
    apiList<CatalogUsage>('/itsm/catalog/usage').then(list => {
      const m: Record<string, CatalogUsage> = {}
      ;(list || []).forEach(u => { m[u.catalog_id] = u })
      setUsageMap(m)
    }).catch(() => {})
  }

  useEffect(() => { load(); fetchProcesses(); fetchGroups(); fetchUsers() }, [])

  const isEdit = !!draft.id
  const openCreate = () => { setDraft(emptyDraft()); setAudFilter(''); setAudTab('group'); setDialogOpen(true) }
  const openEdit = (it: ServiceItem) => {
    setDraft({ ...it, parent_id: it.parent_id || '', level: it.level || 1, sort_order: it.sort_order || 0, audience: { user_ids: it.audience?.user_ids || [], group_ids: it.audience?.group_ids || [] } })
    setAudFilter(''); setAudTab('group'); setDialogOpen(true)
  }

  // 选上级分类：自动继承层级与一级分类类型（从 ITIL 5 大类派生）
  const onParentChange = (pid: string) => {
    if (!pid) {
      setDraft(d => ({ ...d, parent_id: '', level: 1 }))
      return
    }
    const parent = items.find(i => i.id === pid)
    setDraft(d => ({ ...d, parent_id: pid, level: (parent?.level || 1) + 1, category: parent?.category || d.category }))
  }

  const save = async () => {
    if (!draft.name.trim()) { toast.error('请填写服务名称'); return }
    setSaving(true)
    try {
      const payload = {
        name: draft.name.trim(),
        description: draft.description || '',
        category: draft.category,
        parent_id: draft.parent_id || '',
        level: draft.level || 1,
        sort_order: draft.sort_order || 0,
        process_id: draft.process_id || '',
        form_scope: draft.form_scope || '',
        enabled: draft.enabled,
        audience: { user_ids: draft.audience?.user_ids || [], group_ids: draft.audience?.group_ids || [] },
      }
      if (isEdit) {
        await apiFetch(`/itsm/catalog/${draft.id}`, { method: 'PUT', body: JSON.stringify(payload) })
        toast.success('服务目录已更新')
      } else {
        await apiFetch('/itsm/catalog', { method: 'POST', body: JSON.stringify(payload) })
        toast.success('服务目录已创建')
      }
      setDialogOpen(false)
      load()
    } catch (e: any) {
      toast.error(`保存失败: ${e?.message || e}`)
    }
    setSaving(false)
  }

  // 上线 / 下线：整体 PUT 回写(后端 Update 为全量覆盖)，保持其余字段不丢
  const toggleEnabled = async (it: ServiceItem) => {
    const next = !it.enabled
    setItems(prev => prev.map(x => x.id === it.id ? { ...x, enabled: next } : x)) // 乐观更新
    try {
      await apiFetch(`/itsm/catalog/${it.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: it.name, description: it.description, category: it.category,
          parent_id: it.parent_id || '', level: it.level || 1, sort_order: it.sort_order || 0,
          process_id: it.process_id || '', form_scope: it.form_scope || '', enabled: next,
          audience: { user_ids: it.audience?.user_ids || [], group_ids: it.audience?.group_ids || [] },
        }),
      })
      toast.success(next ? `「${it.name}」已上线，员工可在服务门户申请` : `「${it.name}」已下线`)
    } catch (e: any) {
      setItems(prev => prev.map(x => x.id === it.id ? { ...x, enabled: !next } : x)) // 回滚
      toast.error(`操作失败: ${e?.message || e}`)
    }
  }

  const remove = async (it: ServiceItem) => {
    if (hasChildren(it.id)) {
      toast.error(`「${it.name}」下还有子项，请先移除其子项或调整归属后再删除`)
      return
    }
    setDeleteTarget(it)
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await apiFetch(`/itsm/catalog/${deleteTarget.id}`, { method: 'DELETE' })
      toast.success('已删除'); load()
      if (deleteTarget.id === selectedId) setSelectedId('')
    } catch (e: any) { toast.error(`删除失败: ${e?.message || e}`) }
    setDeleteTarget(null)
  }

  // 同级拖拽排序：仅允许同一 parent_id 的目录项之间重排，按 sort_order 持久化。
  const reorder = async (fromId: string, toId: string) => {
    if (!fromId || !toId || fromId === toId) return
    const from = items.find(i => i.id === fromId)
    const to = items.find(i => i.id === toId)
    if (!from || !to) return
    if ((from.parent_id || '') !== (to.parent_id || '')) {
      toast.error('仅支持同一分类下的同级目录项之间拖拽排序')
      return
    }
    const siblings = items
      .filter(i => (i.parent_id || '') === (to.parent_id || ''))
      .sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0) || a.name.localeCompare(b.name))
    const fromIdx = siblings.findIndex(i => i.id === fromId)
    const toIdx = siblings.findIndex(i => i.id === toId)
    if (fromIdx < 0 || toIdx < 0) return
    siblings.splice(fromIdx, 1)
    siblings.splice(toIdx, 0, from)
    const ordered = siblings.map((s, idx) => ({ id: s.id, sort_order: idx + 1 }))
    const newOrder = new Map(ordered.map(o => [o.id, o.sort_order]))
    setItems(prev => prev.map(x => newOrder.has(x.id) ? { ...x, sort_order: newOrder.get(x.id)! } : x)) // 乐观更新
    try {
      await apiFetch('/itsm/catalog/reorder', { method: 'POST', body: JSON.stringify({ items: ordered }) })
    } catch (e: any) {
      toast.error(`排序保存失败: ${e?.message || e}`); load()
    }
  }

  const toggleAud = (kind: 'user_ids' | 'group_ids', id: string) => {
    setDraft(d => {
      const cur = new Set(d.audience?.[kind] || [])
      cur.has(id) ? cur.delete(id) : cur.add(id)
      return { ...d, audience: { ...d.audience, [kind]: Array.from(cur) } }
    })
  }

  const filtered = useMemo(() => {
    const k = keyword.trim().toLowerCase()
    if (!k) return items
    return items.filter(i => i.name.toLowerCase().includes(k) || (i.description || '').toLowerCase().includes(k))
  }, [items, keyword])

  const online = items.filter(i => i.enabled).length
  const groupName = (id: string) => groups.find(g => g.id === id)?.name || id.slice(0, 8)
  const userName = (id: string) => users.find(u => u.id === id)?.username || id.slice(0, 8)

  const audienceSummary = (it: ServiceItem) => {
    const u = it.audience?.user_ids?.length || 0
    const g = it.audience?.group_ids?.length || 0
    if (!u && !g) return null
    return `${g ? `${g} 个用户组` : ''}${g && u ? ' · ' : ''}${u ? `${u} 人` : ''}`
  }

  const audList = audTab === 'group'
    ? groups.filter(g => !audFilter || g.name.toLowerCase().includes(audFilter.toLowerCase()))
    : users.filter(u => !audFilter || u.username.toLowerCase().includes(audFilter.toLowerCase()))

  // —— 多级分类：把扁平列表构造成树 ——
  const byID = useMemo(() => new Map(items.map(i => [i.id, i])), [items])
  const hasChildren = (id: string) => items.some(i => i.parent_id === id)
  const depthOf = (it: ServiceItem): number => {
    let d = 0; let cur = it.parent_id
    while (cur) { d++; const p = byID.get(cur); if (!p) break; cur = p.parent_id }
    return d
  }
  interface TreeNode { item: ServiceItem; children: TreeNode[]; depth: number }
  const buildTree = (src: ServiceItem[]): TreeNode[] => {
    const map = new Map<string, TreeNode>()
    src.forEach(it => map.set(it.id, { item: it, children: [], depth: 0 }))
    const roots: TreeNode[] = []
    src.forEach(it => {
      const node = map.get(it.id)!
      if (it.parent_id && map.has(it.parent_id)) map.get(it.parent_id)!.children.push(node)
      else roots.push(node)
    })
    const sortNodes = (ns: TreeNode[]) => {
      ns.sort((a, b) => (a.item.sort_order || 0) - (b.item.sort_order || 0) || a.item.name.localeCompare(b.item.name))
      ns.forEach(n => { n.depth = depthOf(n.item); sortNodes(n.children) })
    }
    sortNodes(roots)
    return roots
  }
  const tree = useMemo(() => buildTree(items), [items])
  // 祖先路径（用于右侧面包屑）
  const pathOf = (it: ServiceItem): ServiceItem[] => {
    const arr: ServiceItem[] = []
    let cur = it.parent_id
    while (cur) {
      const p = byID.get(cur)
      if (!p) break
      arr.unshift(p)
      cur = p.parent_id
    }
    return arr
  }
  // 选上级分类时，排除「自己 + 自己的所有后代」，避免成环
  const descendantIDs = (rootID: string): Set<string> => {
    const set = new Set<string>(); const walk = (id: string) => {
      items.filter(i => i.parent_id === id).forEach(c => { set.add(c.id); walk(c.id) })
    }; walk(rootID); return set
  }
  const parentOptions = useMemo(() => {
    const banned = draft.id ? descendantIDs(draft.id) : new Set<string>()
    if (draft.id) banned.add(draft.id)
    return items.filter(i => !banned.has(i.id))
      .sort((a, b) => (a.level || 1) - (b.level || 1) || (a.sort_order || 0) - (b.sort_order || 0) || a.name.localeCompare(b.name))
      .map(i => ({ id: i.id, name: i.name, indent: ' '.repeat(depthOf(i) * 2) + (depthOf(i) > 0 ? '└ ' : '') }))
  }, [items, draft.id])

  // 左侧树项：可点击选中 + 可拖拽排序，不在此处放操作按钮（操作收敛到右侧定义面板）
  const renderTreeItem = (it: ServiceItem, depth: number) => {
    const isFolder = hasChildren(it.id)
    const selected = selectedId === it.id
    return <div key={it.id}
      draggable
      onDragStart={(e) => { e.stopPropagation(); setDragId(it.id); e.dataTransfer.effectAllowed = 'move' }}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => { e.preventDefault(); e.stopPropagation(); if (dragId) reorder(dragId, it.id); setDragId(null) }}
      onDragEnd={() => setDragId(null)}
      onClick={() => setSelectedId(it.id)}
      className={`group flex items-center gap-2 rounded-lg border px-2 py-2 cursor-pointer transition-all ${selected ? 'border-primary bg-primary/5 ring-1 ring-primary/30' : 'hover:shadow-sm'} ${it.enabled ? 'bg-background' : 'bg-muted/40 opacity-70'} ${dragId === it.id ? 'ring-2 ring-primary opacity-60' : ''}`}
      style={{ marginLeft: `${depth * 18}px` }}>
      <GripVertical className="h-4 w-4 text-muted-foreground/40 cursor-grab shrink-0" />
      <div className={`h-7 w-7 rounded-md flex items-center justify-center shrink-0 ${isFolder ? 'bg-primary/10' : it.enabled ? 'bg-emerald-500/10' : 'bg-muted'}`}>
        {isFolder
          ? <FolderTree className="h-3.5 w-3.5 text-primary" />
          : <Package className={`h-3.5 w-3.5 ${it.enabled ? 'text-emerald-500' : 'text-muted-foreground'}`} />}
      </div>
      <span className={`flex-1 min-w-0 truncate text-sm ${selected ? 'font-semibold' : 'font-medium'}`}>{it.name}</span>
      <Badge variant={it.enabled ? 'success' : 'outline'} className="text-[10px] shrink-0">{it.enabled ? '已上线' : '未上线'}</Badge>
    </div>
  }
  const renderTreeNode = (node: TreeNode): JSX.Element => (
    <div key={node.item.id}>
      {renderTreeItem(node.item, node.depth)}
      {node.children.map(c => renderTreeNode(c))}
    </div>
  )

  const selected = useMemo(() => items.find(i => i.id === selectedId) || null, [items, selectedId])

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div>
        <h1 className="text-xl font-bold">服务目录定义</h1>
        <p className="text-xs text-muted-foreground">定义标准化服务，上线后员工可在「服务门户」按服务对象范围申请 · 共 {items.length} 项，已上线 {online} 项 · 左侧拖拽 ⠿ 可调整同级顺序，点击节点查看右侧定义</p>
      </div>
    </div>

    <div className="flex gap-4 items-start">
      {/* 左：多级目录树 */}
      <Card className="w-80 shrink-0">
        <CardContent className="p-3 space-y-2">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input className="h-8 pl-8 text-xs" placeholder="搜索服务名称/描述" value={keyword} onChange={e => setKeyword(e.target.value)} />
            </div>
            <Button size="sm" onClick={openCreate} className="shrink-0"><Plus className="h-4 w-4 mr-1" />新增</Button>
          </div>
          <div className="space-y-1 max-h-[70vh] overflow-y-auto pr-1">
            {items.length === 0 ? (
              <p className="text-xs text-muted-foreground text-center py-8">暂无服务项，点击「新增」开始定义（默认已就绪 ITIL 5 大类一级分类）</p>
            ) : keyword.trim() ? (
              filtered.length === 0
                ? <p className="text-xs text-muted-foreground text-center py-6">没有匹配的服务项</p>
                : filtered.map(it => renderTreeItem(it, depthOf(it)))
            ) : (
              tree.map(n => renderTreeNode(n))
            )}
          </div>
        </CardContent>
      </Card>

      {/* 右：目录定义 */}
      <Card className="flex-1 min-w-0">
        <CardContent className="p-5">
          {!selected ? (
            <div className="flex flex-col items-center justify-center text-center text-muted-foreground py-20">
              <FolderTree className="h-10 w-10 mb-3 opacity-30" />
              <p className="text-sm">从左侧目录树选择一个节点，查看其定义与配置</p>
            </div>
          ) : (
            (() => {
              const isFolder = hasChildren(selected.id)
              const aud = audienceSummary(selected)
              const usage = usageMap[selected.id]
              const path = pathOf(selected)
              return <>
                {/* 面包屑路径 */}
                {path.length > 0 && (
                  <div className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground mb-3">
                    {path.map(p => <span key={p.id} className="flex items-center gap-1">{p.name}<ChevronRight className="h-3 w-3" /></span>)}
                    <span className="font-medium text-foreground">{selected.name}</span>
                  </div>
                )}
                {/* 标题 + 操作 */}
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className={`h-10 w-10 rounded-lg flex items-center justify-center shrink-0 ${isFolder ? 'bg-primary/10' : selected.enabled ? 'bg-emerald-500/10' : 'bg-muted'}`}>
                      {isFolder ? <FolderTree className="h-5 w-5 text-primary" /> : <Package className={`h-5 w-5 ${selected.enabled ? 'text-emerald-500' : 'text-muted-foreground'}`} />}
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <h2 className="text-lg font-semibold truncate">{selected.name}</h2>
                        {isFolder && <Badge variant="outline" className="text-[10px]">分类</Badge>}
                        <Badge variant={selected.enabled ? 'success' : 'outline'} className="text-[10px]">{selected.enabled ? '已上线' : '未上线'}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5">{selected.description || '无描述'}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <Button variant="outline" size="sm" onClick={() => toggleEnabled(selected)}>
                      {selected.enabled ? <><ArrowDownCircle className="h-3.5 w-3.5 mr-1 text-amber-500" />下线</> : <><ArrowUpCircle className="h-3.5 w-3.5 mr-1 text-emerald-500" />上线</>}
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => openEdit(selected)}><Pencil className="h-3.5 w-3.5 mr-1" />编辑</Button>
                    <Button variant="outline" size="sm" className="text-red-500" onClick={() => remove(selected)}><Trash2 className="h-3.5 w-3.5 mr-1" />删除</Button>
                  </div>
                </div>

                <div className="grid grid-cols-2 gap-4 mt-5">
                  <div className="space-y-3">
                    <div>
                      <div className="text-[11px] text-muted-foreground mb-1">服务分类</div>
                      <Badge variant="outline" className="text-xs">{catLabel(selected.category)}</Badge>
                    </div>
                    <div>
                      <div className="text-[11px] text-muted-foreground mb-1">关联流程模板</div>
                      {selected.process_id
                        ? <Badge variant="secondary" className="text-xs">{processes.find(p => p.id === selected.process_id)?.name || '关联流程'}</Badge>
                        : <Badge variant="outline" className="text-xs text-muted-foreground">无流程模板（直接生成工单）</Badge>}
                    </div>
                    <div>
                      <div className="text-[11px] text-muted-foreground mb-1">层级 / 排序</div>
                      <div className="flex items-center gap-2 text-xs">
                        <Badge variant="outline" className="text-[10px]">第 {selected.level || 1} 级</Badge>
                        <span className="text-muted-foreground">排序 {selected.sort_order || 0}</span>
                      </div>
                    </div>
                  </div>
                  <div className="space-y-3">
                    <div>
                      <div className="text-[11px] text-muted-foreground mb-1">服务对象（可见范围）</div>
                      <div className="flex flex-wrap items-center gap-1.5">
                        {aud ? (
                          <>
                            <Badge variant="outline" className="text-[10px] gap-1"><Users className="h-2.5 w-2.5" />{aud}</Badge>
                            {(selected.audience?.group_ids || []).map(id => <Badge key={id} variant="secondary" className="text-[10px]">组·{groupName(id)}</Badge>)}
                            {(selected.audience?.user_ids || []).map(id => <Badge key={id} variant="outline" className="text-[10px]">{userName(id)}</Badge>)}
                          </>
                        ) : (
                          <Badge variant="outline" className="text-[10px] gap-1"><Globe className="h-2.5 w-2.5" />全员可见</Badge>
                        )}
                      </div>
                    </div>
                    <div>
                      <div className="text-[11px] text-muted-foreground mb-1">使用统计</div>
                      {usage && (usage.ticket_count > 0 || usage.rated_count > 0) ? (
                        <div className="flex items-center gap-3 text-xs">
                          <span className="flex items-center gap-1"><FileText className="h-3.5 w-3.5 text-muted-foreground" />申请 {usage.ticket_count} 次</span>
                          {usage.rated_count > 0 && (
                            <span className="flex items-center gap-1 text-amber-500"><Star className="h-3.5 w-3.5" />满意度 {usage.avg_satisfaction.toFixed(1)}（{usage.rated_count} 人评）</span>
                          )}
                        </div>
                      ) : (
                        <span className="text-xs text-muted-foreground">暂无使用记录</span>
                      )}
                    </div>
                  </div>
                </div>
              </>
            })()
          )}
        </CardContent>
      </Card>
    </div>

    {/* 新增 / 编辑弹窗 */}
    <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={isEdit ? '编辑服务目录' : '新增服务目录'} className="max-w-2xl">
      <div className="space-y-4">
        <div className="space-y-1.5">
          <label className="text-xs font-medium">服务名称 <span className="text-red-500">*</span></label>
          <Input placeholder="如：账号权限申请" value={draft.name} onChange={e => setDraft({ ...draft, name: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <label className="text-xs font-medium">描述</label>
          <Input placeholder="向员工说明该服务的用途" value={draft.description} onChange={e => setDraft({ ...draft, description: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <label className="text-xs font-medium">上级分类</label>
          <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.parent_id || ''} onChange={e => onParentChange(e.target.value)}>
            <option value="">（无）作为一级分类</option>
            {parentOptions.map(o => <option key={o.id} value={o.id}>{o.indent}{o.name}</option>)}
          </select>
          <p className="text-[10px] text-muted-foreground">默认一级分类为 ITIL 5 大类；留空即新建一级分类，可选已有分类作为上级形成多级。</p>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <label className="text-xs font-medium">服务分类</label>
            <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.category} onChange={e => setDraft({ ...draft, category: e.target.value })}>
              {CATEGORIES.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
            </select>
          </div>
          <div className="space-y-1.5">
            <label className="text-xs font-medium">关联流程模板</label>
            <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.process_id || ''} onChange={e => setDraft({ ...draft, process_id: e.target.value })}>
              <option value="">无流程模板（直接生成工单）</option>
              {processes.filter(p => p.enabled).map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </div>
        </div>

        {/* 服务对象 */}
        <div className="space-y-2">
          <div className="flex items-center justify-between">
            <label className="text-xs font-medium flex items-center gap-1"><Users className="h-3 w-3" />服务对象（可见范围）</label>
            <span className="text-[10px] text-muted-foreground">
              {(draft.audience?.group_ids?.length || 0) + (draft.audience?.user_ids?.length || 0) === 0
                ? '未选择 = 全员可见' : `已选 ${draft.audience?.group_ids?.length || 0} 组 / ${draft.audience?.user_ids?.length || 0} 人`}
            </span>
          </div>
          <div className="border rounded-lg overflow-hidden">
            <div className="flex items-center gap-1 border-b bg-muted/30 px-2 py-1.5">
              {(['group', 'user'] as const).map(t => (
                <button key={t} type="button"
                  className={`px-2.5 py-1 text-xs rounded transition-colors ${audTab === t ? 'bg-background shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'}`}
                  onClick={() => { setAudTab(t); setAudFilter('') }}>
                  {t === 'group' ? `用户组 (${groups.length})` : `用户 (${users.length})`}
                </button>
              ))}
              <Input className="h-7 ml-auto w-40 text-xs" placeholder="搜索…" value={audFilter} onChange={e => setAudFilter(e.target.value)} />
            </div>
            <div className="max-h-44 overflow-y-auto p-2 grid grid-cols-2 gap-1">
              {audList.length === 0 ? <p className="col-span-2 text-xs text-muted-foreground text-center py-4">无可选项</p> :
                audList.map((o: any) => {
                  const kind = audTab === 'group' ? 'group_ids' : 'user_ids'
                  const checked = (draft.audience?.[kind] || []).includes(o.id)
                  return <label key={o.id} className={`flex items-center gap-2 px-2 py-1.5 rounded text-xs cursor-pointer transition-colors ${checked ? 'bg-primary/10' : 'hover:bg-accent'}`}>
                    <input type="checkbox" className="h-3 w-3" checked={checked} onChange={() => toggleAud(kind, o.id)} />
                    <span className="truncate">{audTab === 'group' ? o.name : o.username}</span>
                    {audTab === 'group' && <span className="text-[10px] text-muted-foreground ml-auto shrink-0">{o.user_ids?.length || 0} 人</span>}
                  </label>
                })}
            </div>
          </div>
          {((draft.audience?.group_ids?.length || 0) + (draft.audience?.user_ids?.length || 0)) > 0 && (
            <div className="flex flex-wrap gap-1">
              {(draft.audience?.group_ids || []).map(id => <Badge key={id} variant="secondary" className="text-[10px] cursor-pointer" onClick={() => toggleAud('group_ids', id)}>组·{groupName(id)} ×</Badge>)}
              {(draft.audience?.user_ids || []).map(id => <Badge key={id} variant="outline" className="text-[10px] cursor-pointer" onClick={() => toggleAud('user_ids', id)}>{userName(id)} ×</Badge>)}
            </div>
          )}
        </div>

        <label className="flex items-center gap-2 text-xs cursor-pointer">
          <input type="checkbox" className="h-3.5 w-3.5" checked={draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />
          <span>立即上线（员工可在服务门户看到并申请）</span>
        </label>
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
        <Button onClick={save} disabled={saving || !draft.name.trim()}>{saving ? '保存中…' : isEdit ? '保存修改' : '创建'}</Button>
      </DialogFooter>
    </Dialog>
    <ConfirmDialog open={!!deleteTarget} onClose={() => setDeleteTarget(null)} onConfirm={confirmDelete} title="确认删除" description={`确定删除服务目录「${deleteTarget?.name || ''}」？`} />
  </div>
}
