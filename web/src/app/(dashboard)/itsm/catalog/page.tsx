
// 服务目录定义 —— 管理员视角，左右分离布局：
//  · 左侧 = 纯目录树（仅 kind=folder 节点，可多级嵌套，只做组织分类，不承载流程/上线语义）
//  · 右侧 = 选中某目录后，展示「该目录下的服务项编辑区」；在 A 目录下新建服务项，它就归属 A 目录(parent_id=A)
// 服务对象(用户/用户组)决定哪些人能在门户看到该项，为空 = 全员可见。
import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Popconfirm } from 'antd'
import { FolderTree, FolderPlus, Package, Plus, Trash2, Pencil, Search, Users, ArrowUpCircle, ArrowDownCircle, Globe, FileText, Star, ChevronRight, CornerDownRight, Layers } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { useProcessStore } from '@/stores/itsm/processes'
import { useIAMStore } from '@/stores/iam'
import { useUserStore } from '@/stores/user'
import { message } from 'antd'

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
  kind?: string // folder=纯目录 / service=服务项
  process_id?: string
  form_scope?: string
  enabled: boolean
  audience?: Audience
}
type DraftKind = 'folder' | 'service'

const CATEGORIES = [
  { value: 'change', label: '变更管理' },
  { value: 'incident', label: '事件管理' },
  { value: 'request', label: '服务请求' },
  { value: 'problem', label: '问题管理' },
  { value: 'release', label: '发布管理' },
]
const catLabel = (v: string) => CATEGORIES.find(c => c.value === v)?.label || v

const emptyFolder = (parent?: ServiceItem): Partial<ServiceItem> => ({
  id: '', name: '', description: '', kind: 'folder',
  parent_id: parent?.id || '', level: (parent?.level || 0) + 1,
  category: parent?.category || 'change', enabled: false, audience: { user_ids: [], group_ids: [] },
})
const emptyService = (parent?: ServiceItem): Partial<ServiceItem> => ({
  id: '', name: '', description: '', kind: 'service',
  parent_id: parent?.id || '', level: (parent?.level || 0) + 1,
  category: parent?.category || 'change', process_id: '', enabled: false, audience: { user_ids: [], group_ids: [] },
})
const isFolder = (it?: ServiceItem) => (it?.kind || 'service') === 'folder'

export default function ITSMCatalogPage() {
  const { processes, fetchProcesses } = useProcessStore()
  const { groups, fetchGroups } = useIAMStore()
  const { users, fetch: fetchUsers } = useUserStore()

  const [items, setItems] = useState<ServiceItem[]>([])
  const [keyword, setKeyword] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [draftKind, setDraftKind] = useState<DraftKind>('service')
  const [draft, setDraft] = useState<Partial<ServiceItem>>(emptyService())
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<ServiceItem | null>(null)
  const [audTab, setAudTab] = useState<'group' | 'user'>('group')
  const [audFilter, setAudFilter] = useState('')
  const [usageMap, setUsageMap] = useState<Record<string, CatalogUsage>>({})
  const [selectedFolderId, setSelectedFolderId] = useState<string>('')

  const load = () => {
    apiList<ServiceItem>('/itsm/catalog').then(list => setItems(list || [])).catch(() => message.error('加载失败'))
    apiList<CatalogUsage>('/itsm/catalog/usage').then(list => {
      const m: Record<string, CatalogUsage> = {}
      ;(list || []).forEach(u => { m[u.catalog_id] = u })
      setUsageMap(m)
    }).catch(() => {})
  }
  useEffect(() => { load(); fetchProcesses(); fetchGroups(); fetchUsers() }, [])

  const folders = useMemo(() => items.filter(i => isFolder(i)), [items])
  const services = useMemo(() => items.filter(i => !isFolder(i)), [items])
  const selectedFolder = useMemo(() => folders.find(f => f.id === selectedFolderId) || null, [folders, selectedFolderId])
  const servicesUnder = useMemo(() => {
    const m = new Map<string, ServiceItem[]>()
    services.forEach(s => {
      const pid = s.parent_id || ''
      const arr = m.get(pid) || []
      arr.push(s)
      m.set(pid, arr)
    })
    return m
  }, [services])
  const subfoldersUnder = useMemo(() => {
    const m = new Map<string, ServiceItem[]>()
    folders.forEach(f => {
      const pid = f.parent_id || ''
      const arr = m.get(pid) || []
      arr.push(f)
      m.set(pid, arr)
    })
    return m
  }, [folders])

  // —— 左侧纯目录树 ——
  const byID = useMemo(() => new Map(items.map(i => [i.id, i])), [items])
  const depthOf = (it: ServiceItem): number => {
    let d = 0; let cur = it.parent_id
    while (cur) { d++; const p = byID.get(cur); if (!p) break; cur = p.parent_id }
    return d
  }
  const buildTree = (src: ServiceItem[]): ServiceItem[] => {
    const map = new Map<string, ServiceItem[]>()
    src.forEach(it => {
      const pid = it.parent_id || ''
      const arr = map.get(pid) || []
      arr.push(it)
      map.set(pid, arr)
    })
    const sortNs = (ns: ServiceItem[]) => {
      ns.sort((a, b) => (a.sort_order || 0) - (b.sort_order || 0) || a.name.localeCompare(b.name))
      ns.forEach(n => sortNs(map.get(n.id) || []))
    }
    const roots = (map.get('') || []).slice()
    sortNs(roots)
    return roots
  }
  const folderTree = useMemo(() => buildTree(folders), [folders])
  const pathOf = (it: ServiceItem): ServiceItem[] => {
    const arr: ServiceItem[] = []
    let cur = it.parent_id
    while (cur) { const p = byID.get(cur); if (!p) break; arr.unshift(p); cur = p.parent_id }
    return arr
  }
  // 后代目录(含自身)，用于目录选择时排除成环
  const descendantFolderIDs = (rootID: string): Set<string> => {
    const set = new Set<string>(); const walk = (id: string) => {
      folders.filter(i => (i.parent_id || '') === id).forEach(c => { set.add(c.id); walk(c.id) })
    }; walk(rootID); return set
  }

  // —— 弹窗（区分 folder / service）——
  const isEdit = !!draft.id
  const openCreateFolder = () => { setDraftKind('folder'); setDraft({ ...emptyFolder(selectedFolder || undefined), enabled: true }); setAudFilter(''); setAudTab('group'); setDialogOpen(true) }
  const openCreateService = () => {
    if (!selectedFolder) { message.error('请先在左侧选择一个目录，再在其中新增服务项'); return }
    setDraftKind('service'); setDraft({ ...emptyService(selectedFolder), enabled: true }); setAudFilter(''); setAudTab('group'); setDialogOpen(true)
  }
  const openEdit = (it: ServiceItem) => {
    setDraftKind(it.kind === 'folder' ? 'folder' : 'service')
    setDraft({ ...it, parent_id: it.parent_id || '', level: it.level || 1, sort_order: it.sort_order || 0, audience: { user_ids: it.audience?.user_ids || [], group_ids: it.audience?.group_ids || [] } })
    setAudFilter(''); setAudTab('group'); setDialogOpen(true)
  }
  const parentOptions = useMemo(() => {
    const banned = new Set<string>()
    if (draftKind === 'folder' && draft.id) { banned.add(draft.id); descendantFolderIDs(draft.id).forEach(id => banned.add(id)) }
    return folders.filter(f => !banned.has(f.id))
      .sort((a, b) => (a.level || 1) - (b.level || 1) || (a.sort_order || 0) - (b.sort_order || 0) || a.name.localeCompare(b.name))
      .map(f => ({ id: f.id, name: f.name, indent: ' '.repeat(depthOf(f) * 2) + (depthOf(f) > 0 ? '└ ' : '') }))
  }, [folders, draftKind, draft.id])

  const save = async () => {
    if (!draft.name?.trim()) { message.error('请填写名称'); return }
    const folderMode = draftKind === 'folder'
    if (!folderMode && !draft.process_id) { message.error('请选择关联流程模板：服务目录必须绑定流程才能上线'); return }
    setSaving(true)
    try {
      const payload = {
        name: draft.name.trim(),
        description: draft.description || '',
        category: draft.category || 'change',
        parent_id: draft.parent_id || '',
        level: draft.level || 1,
        sort_order: draft.sort_order || 0,
        kind: draftKind,
        process_id: folderMode ? '' : (draft.process_id || ''),
        form_scope: folderMode ? '' : (draft.form_scope || ''),
        enabled: draft.enabled && !folderMode, // 目录本身无上线语义
        audience: folderMode ? { user_ids: [], group_ids: [] } : { user_ids: draft.audience?.user_ids || [], group_ids: draft.audience?.group_ids || [] },
      }
      if (isEdit) {
        await apiFetch(`/itsm/catalog/${draft.id}`, { method: 'PUT', body: JSON.stringify(payload) })
        message.success(folderMode ? '目录已更新' : '服务目录已更新')
      } else {
        await apiFetch('/itsm/catalog', { method: 'POST', body: JSON.stringify(payload) })
        message.success(folderMode ? '目录已创建' : '服务目录已创建')
      }
      setDialogOpen(false)
      load()
    } catch (e: any) {
      message.error(`保存失败: ${e?.message || e}`)
    }
    setSaving(false)
  }

  // 服务项上线 / 下线（目录无此语义）
  const toggleEnabled = async (it: ServiceItem) => {
    if (!it.enabled && !it.process_id) {
      message.error('该服务目录未关联流程模板，无法上线（请先编辑并关联流程）')
      return
    }
    const next = !it.enabled
    setItems(prev => prev.map(x => x.id === it.id ? { ...x, enabled: next } : x))
    try {
      await apiFetch(`/itsm/catalog/${it.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: it.name, description: it.description, category: it.category,
          parent_id: it.parent_id || '', level: it.level || 1, sort_order: it.sort_order || 0,
          kind: it.kind, process_id: it.process_id || '', form_scope: it.form_scope || '', enabled: next,
          audience: { user_ids: it.audience?.user_ids || [], group_ids: it.audience?.group_ids || [] },
        }),
      })
      message.success(next ? `「${it.name}」已上线，员工可在服务门户申请` : `「${it.name}」已下线`)
    } catch (e: any) {
      setItems(prev => prev.map(x => x.id === it.id ? { ...x, enabled: !next } : x))
      message.error(`操作失败: ${e?.message || e}`)
    }
  }

  const remove = async (it: ServiceItem) => {
    const hasKids = (subfoldersUnder.get(it.id)?.length || 0) + (servicesUnder.get(it.id)?.length || 0) > 0
    if (hasKids) {
      message.error(isFolder(it) ? '该目录下还有子目录或服务项，请先移除其下内容后再删除' : '该服务项下还有子项，请先移除后再删除')
      return
    }
    setDeleteTarget(it)
  }
  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await apiFetch(`/itsm/catalog/${deleteTarget.id}`, { method: 'DELETE' })
      message.success('已删除'); load()
      if (deleteTarget.id === selectedFolderId) setSelectedFolderId('')
    } catch (e: any) { message.error(`删除失败: ${e?.message || e}`) }
    setDeleteTarget(null)
  }

  const toggleAud = (kind: 'user_ids' | 'group_ids', id: string) => {
    setDraft(d => {
      const cur = new Set(d.audience?.[kind] || [])
      cur.has(id) ? cur.delete(id) : cur.add(id)
      return { ...d, audience: { ...d.audience, [kind]: Array.from(cur) } }
    })
  }

  const folderMode = draftKind === 'folder'
  const groupName = (id: string) => groups.find(g => g.id === id)?.name || id.slice(0, 8)
  const userName = (id: string) => users.find(u => u.id === id)?.username || id.slice(0, 8)
  const audienceSummary = (it: ServiceItem) => {
    const u = it.audience?.user_ids?.length || 0
    const g = it.audience?.group_ids?.length || 0
    if (!u && !g) return null
    return `${g ? `${g} 个用户组` : ''}${g && u ? ' · ' : ''}${u ? `${u} 人` : ''}`
  }
  // 流程 -> 引用它的服务项列表（本地由 services 计算，供「选流程看引用」展示）
  const refsByProcess = useMemo(() => {
    const m = new Map<string, ServiceItem[]>()
    services.forEach(it => { if (it.process_id) { const arr = m.get(it.process_id) || []; arr.push(it); m.set(it.process_id, arr) } })
    return m
  }, [services])
  const audList = audTab === 'group'
    ? groups.filter(g => !audFilter || g.name.toLowerCase().includes(audFilter.toLowerCase()))
    : users.filter(u => !audFilter || u.username.toLowerCase().includes(audFilter.toLowerCase()))

  // —— 左侧目录树节点 ——
  const renderFolder = (f: ServiceItem, depth: number) => {
    const childCount = (subfoldersUnder.get(f.id)?.length || 0) + (servicesUnder.get(f.id)?.length || 0)
    const selected = selectedFolderId === f.id
    return <div key={f.id}>
      <div
        onClick={() => setSelectedFolderId(f.id)}
        className={`group flex items-center gap-2 rounded-lg border px-2 py-2 cursor-pointer transition-all ${selected ? 'border-primary bg-primary/5 ring-1 ring-primary/30' : 'hover:shadow-sm border-transparent hover:bg-muted/40'}`}
        style={{ marginLeft: `${depth * 18}px` }}>
        <div className="h-7 w-7 rounded-md flex items-center justify-center shrink-0 bg-primary/10">
          <FolderTree className="h-3.5 w-3.5 text-primary" />
        </div>
        <span className={`flex-1 min-w-0 truncate text-sm ${selected ? 'font-semibold' : 'font-medium'}`}>{f.name}</span>
        {childCount > 0 && <span className="text-[10px] text-muted-foreground bg-muted rounded px-1.5 py-0.5 shrink-0">{childCount}</span>}
        <button title="新增子目录" onClick={(e) => { e.stopPropagation(); setSelectedFolderId(f.id); openCreateFolder() }}
          className="opacity-0 group-hover:opacity-100 h-6 w-6 rounded flex items-center justify-center text-muted-foreground hover:text-primary hover:bg-primary/10 shrink-0 transition">
          <FolderPlus className="h-3.5 w-3.5" />
        </button>
      </div>
      {(subfoldersUnder.get(f.id) || []).map(c => renderFolder(c, depth + 1))}
    </div>
  }

  const selectedPath = selectedFolder ? pathOf(selectedFolder) : []

  return <div className="p-6 space-y-4">
    <div>
      <h1 className="text-2xl font-bold">服务目录定义</h1>
      <p className="text-xs text-muted-foreground">左侧为纯目录树（多级组织分类）；选中目录后，右侧即为该目录下的服务项编辑区 —— 在哪个目录下新建，服务项就归属哪个目录 · 共 {folders.length} 个目录 / {services.length} 个服务项</p>
    </div>

    <div className="flex gap-4 items-start">
      {/* 左：纯目录树 */}
      <Card className="w-80 shrink-0">
        <CardContent className="p-3 space-y-2">
          <div className="flex items-center gap-2">
            <div className="relative flex-1">
              <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
              <Input className="h-8 pl-8 text-xs" placeholder="搜索目录名称" value={keyword} onChange={e => setKeyword(e.target.value)} />
            </div>
            <Button size="middle" onClick={openCreateFolder} className="shrink-0" title="新增目录"><Plus className="h-4 w-4 mr-1" />目录</Button>
          </div>
          <div className="space-y-1 max-h-[70vh] overflow-y-auto pr-1">
            {folders.length === 0 ? (
              <p className="text-xs text-muted-foreground text-center py-8">暂无目录，点击「目录」开始创建（默认已就绪 ITIL 5 大类一级目录）</p>
            ) : keyword.trim() ? (
              (() => {
                const k = keyword.trim().toLowerCase()
                const hit = folders.filter(f => f.name.toLowerCase().includes(k))
                return hit.length === 0 ? <p className="text-xs text-muted-foreground text-center py-6">没有匹配的目录</p> : hit.map(f => renderFolder(f, depthOf(f)))
              })()
            ) : (
              folderTree.map(f => renderFolder(f, 0))
            )}
          </div>
        </CardContent>
      </Card>

      {/* 右：选中目录下的服务项编辑区 */}
      <Card className="flex-1 min-w-0">
        <CardContent className="p-5">
          {!selectedFolder ? (
            <div className="flex flex-col items-center justify-center text-center text-muted-foreground py-20">
              <Layers className="h-10 w-10 mb-3 opacity-30" />
              <p className="text-sm">从左侧目录树选择一个目录，右侧将展示该目录下的服务项编辑区</p>
            </div>
          ) : (
            (() => {
              const subs = servicesUnder.get(selectedFolder.id) || []
              const childFolders = subfoldersUnder.get(selectedFolder.id) || []
              return <>
                {selectedPath.length > 0 && (
                  <div className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground mb-2">
                    {selectedPath.map(p => <span key={p.id} className="flex items-center gap-1">{p.name}<ChevronRight className="h-3 w-3" /></span>)}
                  </div>
                )}
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="h-10 w-10 rounded-lg flex items-center justify-center shrink-0 bg-primary/10">
                      <FolderTree className="h-5 w-5 text-primary" />
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <h2 className="text-lg font-semibold truncate">{selectedFolder.name}</h2>
                        <Tag color="default" className="text-[10px]">目录</Tag>
                        {childFolders.length + subs.length > 0 && <Tag color="default" className="text-[10px]">{childFolders.length} 子目录 · {subs.length} 服务项</Tag>}
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5">{selectedFolder.description || '无描述'}</p>
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <Button type="default" size="middle" onClick={openCreateService}><Plus className="h-3.5 w-3.5 mr-1" />新增服务项</Button>
                    <Button type="default" size="middle" onClick={() => { setSelectedFolderId(selectedFolder.id); openCreateFolder() }}><FolderPlus className="h-3.5 w-3.5 mr-1" />子目录</Button>
                    <Button type="default" size="middle" onClick={() => openEdit(selectedFolder)}><Pencil className="h-3.5 w-3.5 mr-1" />重命名</Button>
                    <Button type="default" size="middle" danger onClick={() => remove(selectedFolder)}><Trash2 className="h-3.5 w-3.5 mr-1" />删除目录</Button>
                  </div>
                </div>

                <div className="mt-5">
                  <div className="flex items-center gap-2 mb-2">
                    <CornerDownRight className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm font-semibold">该目录下的服务项</span>
                    <span className="text-xs text-muted-foreground">（新建服务项将自动归属此目录）</span>
                  </div>
                  {subs.length === 0 ? (
                    <div className="border border-dashed rounded-lg py-10 flex flex-col items-center justify-center text-center text-muted-foreground">
                      <Package className="h-8 w-8 mb-2 opacity-30" />
                      <p className="text-sm">该目录下暂无服务项</p>
                      <Button type="default" size="middle" className="mt-3" onClick={openCreateService}><Plus className="h-3.5 w-3.5 mr-1" />新增服务项</Button>
                    </div>
                  ) : (
                    <div className="space-y-2">
                      {subs.map(s => {
                        const aud = audienceSummary(s)
                        const usage = usageMap[s.id]
                        const proc = processes.find(p => p.id === s.process_id)
                        return <div key={s.id} className="border rounded-lg p-3 flex items-center gap-3 hover:shadow-sm transition">
                          <div className={`h-8 w-8 rounded-md flex items-center justify-center shrink-0 ${s.enabled ? 'bg-blue-500/10' : 'bg-muted'}`}>
                            <Package className={`h-4 w-4 ${s.enabled ? 'text-blue-500' : 'text-muted-foreground'}`} />
                          </div>
                          <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                              <span className="text-sm font-medium truncate">{s.name}</span>
                              <Tag color={s.enabled ? 'success' : 'default'} className="text-[10px]">{s.enabled ? '已上线' : '未上线'}</Tag>
                            </div>
                            <div className="flex flex-wrap items-center gap-1.5 mt-1">
                              {proc ? <Tag color="default" className="text-[10px]">{proc.name}</Tag>
                                : <Tag color="warning" className="text-[10px]">未关联流程</Tag>}
                              {aud ? <Tag color="default" className="text-[10px] gap-1"><Users className="h-2.5 w-2.5" />{aud}</Tag>
                                : <Tag color="default" className="text-[10px] gap-1"><Globe className="h-2.5 w-2.5" />全员可见</Tag>}
                              {usage && (usage.ticket_count > 0 || usage.rated_count > 0) && (
                                <span className="text-[10px] text-muted-foreground flex items-center gap-1">
                                  <FileText className="h-2.5 w-2.5" />{usage.ticket_count} 次申请
                                  {usage.rated_count > 0 && <><Star className="h-2.5 w-2.5 text-amber-500" />{usage.avg_satisfaction.toFixed(1)}</>}
                                </span>
                              )}
                            </div>
                          </div>
                          <div className="flex items-center gap-1.5 shrink-0">
                            <Button type="default" size="middle" onClick={() => toggleEnabled(s)} disabled={!s.enabled && !s.process_id} title={!s.enabled && !s.process_id ? '未关联流程模板，无法上线' : ''}>
                              {s.enabled ? <><ArrowDownCircle className="h-3.5 w-3.5 mr-1 text-amber-500" />下线</> : <><ArrowUpCircle className="h-3.5 w-3.5 mr-1 text-blue-500" />上线</>}
                            </Button>
                            <Button type="default" size="middle" onClick={() => openEdit(s)}><Pencil className="h-3.5 w-3.5 mr-1" />编辑</Button>
                            <Button type="default" size="middle" danger onClick={() => remove(s)}><Trash2 className="h-3.5 w-3.5 mr-1" />删除</Button>
                          </div>
                        </div>
                      })}
                    </div>
                  )}
                </div>
              </>
            })()
          )}
        </CardContent>
      </Card>
    </div>

    {/* 新增 / 编辑弹窗（folder 与 service 共用，按 draftKind 切换字段） */}
    <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={isEdit ? (folderMode ? '编辑目录' : '编辑服务目录') : (folderMode ? '新增目录' : '新增服务目录')} className="max-w-2xl" footer={null}>
      <div className="space-y-4">
        <div className="space-y-1.5">
          <label className="text-xs font-medium">{folderMode ? '目录名称' : '服务名称'} <span className="text-red-500">*</span></label>
          <Input placeholder={folderMode ? '如：发布管理' : '如：账号权限申请'} value={draft.name || ''} onChange={e => setDraft({ ...draft, name: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <label className="text-xs font-medium">描述</label>
          <Input placeholder={folderMode ? '目录用途说明（可选）' : '向员工说明该服务的用途'} value={draft.description || ''} onChange={e => setDraft({ ...draft, description: e.target.value })} />
        </div>
        <div className="space-y-1.5">
          <label className="text-xs font-medium">上级目录</label>
          <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.parent_id || ''} onChange={e => {
            const pid = e.target.value
            const p = folders.find(f => f.id === pid)
            setDraft(d => ({ ...d, parent_id: pid, level: (p?.level || 0) + 1, category: p?.category || d.category }))
          }}>
            <option value="">（无）作为一级目录</option>
            {parentOptions.map(o => <option key={o.id} value={o.id}>{o.indent}{o.name}</option>)}
          </select>
          <p className="text-[10px] text-muted-foreground">目录可多级嵌套；服务项将归属于其所在目录。</p>
        </div>

        {!folderMode && (
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <label className="text-xs font-medium">服务分类</label>
              <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.category || 'change'} onChange={e => setDraft({ ...draft, category: e.target.value })}>
                {CATEGORIES.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
              </select>
            </div>
            <div className="space-y-1.5">
              <label className="text-xs font-medium">关联流程模板 <span className="text-red-500">*</span></label>
              <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs" value={draft.process_id || ''} onChange={e => setDraft({ ...draft, process_id: e.target.value })}>
                <option value="" disabled>请选择流程模板 *</option>
                {processes.filter(p => p.enabled).map(p => {
                  const cnt = refsByProcess.get(p.id)?.length || 0
                  return <option key={p.id} value={p.id}>{p.name}{cnt ? `（被 ${cnt} 个目录引用）` : ''}</option>
                })}
              </select>
              <p className="text-[10px] text-muted-foreground">服务目录必须绑定流程模板；未关联流程无法上线。</p>
              {draft.process_id && (() => {
                const refs = (refsByProcess.get(draft.process_id) || []).filter(r => r.id !== draft.id)
                return <p className="text-[10px] text-muted-foreground mt-1">该流程已被以下服务目录引用：{refs.length ? refs.map(r => r.name).join('、') : '（当前编辑项，暂无其他目录）'}</p>
              })()}
            </div>
          </div>
        )}

        {!folderMode && (
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
                {(draft.audience?.group_ids || []).map(id => <Tag key={id} color="default" className="text-[10px] cursor-pointer" onClick={() => toggleAud('group_ids', id)}>组·{groupName(id)} ×</Tag>)}
                {(draft.audience?.user_ids || []).map(id => <Tag key={id} color="default" className="text-[10px] cursor-pointer" onClick={() => toggleAud('user_ids', id)}>{userName(id)} ×</Tag>)}
              </div>
            )}
          </div>
        )}

        {!folderMode && (
          <label className="flex items-center gap-2 text-xs cursor-pointer">
            <input type="checkbox" className="h-3.5 w-3.5" checked={!!draft.enabled} onChange={e => setDraft({ ...draft, enabled: e.target.checked })} />
            <span>立即上线（员工可在服务门户看到并申请）</span>
          </label>
        )}
      </div>
      <div className="flex justify-end gap-2">
        <Button onClick={() => setDialogOpen(false)}>取消</Button>
        <Button type="primary" onClick={save} disabled={saving || !(draft.name || '').trim() || (!folderMode && !draft.process_id)}>{saving ? '保存中…' : isEdit ? '保存修改' : '创建'}</Button>
      </div>
    </Modal>
    <Popconfirm title="确认删除" description={`确定删除${deleteTarget && isFolder(deleteTarget) ? '目录' : '服务目录'}「${deleteTarget?.name || ''}」？`} onConfirm={confirmDelete} okText="确认" cancelText="取消">
      <span style={{ display: 'none' }} />
    </Popconfirm>
  </div>
}
