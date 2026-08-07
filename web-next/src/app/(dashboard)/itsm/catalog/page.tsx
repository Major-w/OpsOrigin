'use client'

// 服务目录定义 —— 管理员视角：
// 目录项在此定义并「上线」后，才会出现在员工「自助服务门户」中；
// 服务对象(用户/用户组)决定哪些人能在门户看到它，为空 = 全员可见。
// 本页只做定义与生命周期管理，发起工单统一收敛到自助服务门户，避免两处入口语义重叠。
import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Package, Plus, Trash2, Pencil, Search, Users, ArrowUpCircle, ArrowDownCircle, Globe, FolderTree } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useITSMStore } from '@/stores/itsm'
import { useIAMStore } from '@/stores/iam'
import { useUserStore } from '@/stores/user'

interface Audience { user_ids?: string[]; group_ids?: string[] }
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
  const [audTab, setAudTab] = useState<'group' | 'user'>('group')
  const [audFilter, setAudFilter] = useState('')

  const load = () => apiList<ServiceItem>('/itsm/catalog').then(list => setItems(list || [])).catch(() => toast.error('加载失败'))

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
      toast.success(next ? `「${it.name}」已上线，员工可在自助服务门户申请` : `「${it.name}」已下线`)
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
    if (!confirm(`确认删除服务目录「${it.name}」？`)) return
    try {
      await apiFetch(`/itsm/catalog/${it.id}`, { method: 'DELETE' })
      toast.success('已删除'); load()
    } catch (e: any) { toast.error(`删除失败: ${e?.message || e}`) }
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
      .map(i => ({ id: i.id, name: i.name, indent: ' '.repeat(depthOf(i) * 2) + (depthOf(i) > 0 ? '└ ' : '') }))
  }, [items, draft.id])

  const renderRow = (it: ServiceItem, depth: number) => {
    const isFolder = hasChildren(it.id)
    const aud = audienceSummary(it)
    return <div key={it.id}
      className={`group flex items-center gap-3 rounded-lg border px-3 py-2.5 transition-all hover:shadow-sm ${it.enabled ? 'bg-background' : 'bg-muted/40 opacity-70'}`}
      style={{ marginLeft: `${depth * 22}px` }}>
      <div className={`h-8 w-8 rounded-md flex items-center justify-center shrink-0 ${isFolder ? 'bg-primary/10' : it.enabled ? 'bg-emerald-500/10' : 'bg-muted'}`}>
        {isFolder
          ? <FolderTree className="h-4 w-4 text-primary" />
          : <Package className={`h-4 w-4 ${it.enabled ? 'text-emerald-500' : 'text-muted-foreground'}`} />}
      </div>
      <div className="flex-1 min-w-0">
        <div className="flex items-center gap-2">
          <span className="font-medium text-sm truncate">{it.name}</span>
          {isFolder && <Badge variant="outline" className="text-[10px]">分类</Badge>}
          <Badge variant={it.enabled ? 'success' : 'outline'} className="text-[10px] shrink-0">{it.enabled ? '已上线' : '未上线'}</Badge>
        </div>
        <p className="text-xs text-muted-foreground truncate mt-0.5">{it.description || '无描述'}</p>
        <div className="flex flex-wrap gap-1.5 mt-1.5">
          <Badge variant="outline" className="text-[10px]">{catLabel(it.category)}</Badge>
          {it.process_id
            ? <Badge variant="secondary" className="text-[10px]">{processes.find(p => p.id === it.process_id)?.name || '关联流程'}</Badge>
            : <Badge variant="outline" className="text-[10px] text-muted-foreground">无流程模板</Badge>}
          <Badge variant="outline" className="text-[10px] gap-1">
            {aud ? <><Users className="h-2.5 w-2.5" />{aud}</> : <><Globe className="h-2.5 w-2.5" />全员可见</>}
          </Badge>
        </div>
      </div>
      <div className="flex flex-col gap-1 shrink-0">
        <Button variant="ghost" size="icon" className="h-7 w-7" title={it.enabled ? '下线' : '上线'} onClick={() => toggleEnabled(it)}>
          {it.enabled ? <ArrowDownCircle className="h-3.5 w-3.5 text-amber-500" /> : <ArrowUpCircle className="h-3.5 w-3.5 text-emerald-500" />}
        </Button>
        <Button variant="ghost" size="icon" className="h-7 w-7" title="编辑" onClick={() => openEdit(it)}>
          <Pencil className="h-3.5 w-3.5 text-blue-500" />
        </Button>
        <Button variant="ghost" size="icon" className="h-7 w-7" title="删除" onClick={() => remove(it)}>
          <Trash2 className="h-3.5 w-3.5 text-red-400" />
        </Button>
      </div>
    </div>
  }
  const renderNode = (node: TreeNode): JSX.Element => (
    <div key={node.item.id}>
      {renderRow(node.item, node.depth)}
      {node.children.map(c => renderNode(c))}
    </div>
  )

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div>
        <h1 className="text-xl font-bold">服务目录定义</h1>
        <p className="text-xs text-muted-foreground">定义标准化服务，上线后员工可在「自助服务门户」按服务对象范围申请 · 共 {items.length} 项，已上线 {online} 项</p>
      </div>
      <div className="flex items-center gap-2">
        <div className="relative">
          <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 w-52 pl-8 text-xs" placeholder="搜索服务名称/描述" value={keyword} onChange={e => setKeyword(e.target.value)} />
        </div>
        <Button size="sm" onClick={openCreate}><Plus className="h-4 w-4 mr-1" />新增服务</Button>
      </div>
    </div>

    <div className="space-y-2">
      {items.length === 0 ? (
        <Card><CardContent className="py-12 text-center text-muted-foreground">
          <Package className="h-10 w-10 mx-auto mb-2 opacity-30" />
          <p className="text-sm">暂无服务项，点击右上角「新增服务」开始定义（默认已就绪 ITIL 5 大类一级分类）</p>
        </CardContent></Card>
      ) : keyword.trim() ? (
        filtered.length === 0
          ? <p className="text-sm text-muted-foreground px-1 py-6">没有匹配的服务项</p>
          : filtered.map(it => renderRow(it, depthOf(it)))
      ) : (
        tree.map(n => renderNode(n))
      )}
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
              <option value="">无流程模板（直通工单）</option>
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
          <span>立即上线（员工可在自助服务门户看到并申请）</span>
        </label>
      </div>
      <DialogFooter>
        <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
        <Button onClick={save} disabled={saving || !draft.name.trim()}>{saving ? '保存中…' : isEdit ? '保存修改' : '创建'}</Button>
      </DialogFooter>
    </Dialog>
  </div>
}
