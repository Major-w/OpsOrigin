
// 服务门户 —— 员工侧的服务消费入口：
// 左侧 = 固定的 5 大 ITIL 分类(发布/服务请求/变更/问题/事件)作为一级目录(+ 其子分类文件夹)，
// 右侧 = 当前选中分类下的「可申请服务」卡片；门户内直接发起申请(抽屉表单，不跳工单页)。
// 申请走 POST /itsm/catalog/{id}/fulfill，由后端套用目录项绑定的流程与表单作用域。
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag, Button, Input, Tabs } from 'antd'
import { apiList, apiFetch } from '@/lib/utils'
import {
  Package, ChevronRight, ChevronDown, Loader2, FolderTree, Search,
  Layers, X, Send, CheckCircle2, FileText,
} from 'lucide-react'
import { buildTree, parseFormSchema, type TreeNode } from '@/lib/itsm-constants'
import { useProcessStore } from '@/stores/itsm/processes'
import type { Process } from '@/stores/itsm/types'
import { useAuthStore } from '@/stores/auth'
import { DynamicForm, withBuiltinFields, splitBuiltinValues, type FormSchema } from '@/components/itsm/DynamicForm'
import ProcessFlowView from '@/components/itsm/ProcessFlowView'
import { message } from 'antd'

interface ServiceItem {
  id: string
  name: string
  description?: string
  category: string
  parent_id?: string
  level?: number
  sort_order?: number
  process_id?: string
  form_scope?: string
  enabled: boolean
}

// 服务门户左侧一级目录：5 大 ITIL 分类(顺序与产品口径一致)
const CATEGORIES = [
  { value: 'release', label: '发布管理' },
  { value: 'request', label: '服务请求' },
  { value: 'change', label: '变更管理' },
  { value: 'problem', label: '问题管理' },
  { value: 'incident', label: '事件管理' },
]
const catLabel = (v: string) => CATEGORIES.find(c => c.value === v)?.label || v

const ROOT_KEY = '__all__'

interface PortalNode { item: ServiceItem; children: PortalNode[] }

export default function ITSMPortalPage() {
  const navigate = useNavigate()
  const { getProcess, getFormSchema, getFormOptions } = useProcessStore()
  const me = useAuthStore(s => s.user)

  const [services, setServices] = useState<ServiceItem[]>([])
  const [loading, setLoading] = useState(true)
  const [keyword, setKeyword] = useState('')
  const [selectedId, setSelectedId] = useState<string>(ROOT_KEY)
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  // 门户只拉「已上线 且 当前用户在服务对象范围内」的目录项：
  // 过滤在服务端完成(catalog/visible)，前端拿不到越权目录，避免可见范围靠前端自觉。
  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const svc = await apiList<ServiceItem>('/itsm/catalog/visible')
      setServices(svc || [])
    } catch { message.error('加载服务目录失败') }
    setLoading(false)
  }, [])

  useEffect(() => { fetchAll() }, [fetchAll])

  const toPortal = (ns: TreeNode<ServiceItem>[]): PortalNode[] =>
    ns.map(n => ({ item: n.item, children: toPortal(n.children) }))

  // 每个分类各自的子树(以该分类下 parent_id 关系构成；叶子=可申请服务，非叶子=子分类文件夹)
  const categoryTrees = useMemo(() => {
    const m = new Map<string, PortalNode[]>()
    for (const c of CATEGORIES) m.set(c.value, toPortal(buildTree(services.filter(s => s.category === c.value))))
    return m
  }, [services])

  // 左侧展示树：5 个分类作为一级节点；其子节点只放「子分类文件夹」，叶子服务不进左树(改在右侧卡片展示)
  const displayTree = useMemo<PortalNode[]>(() => {
    return CATEGORIES.map(c => {
      const sub = categoryTrees.get(c.value) || []
      const folderChildren = sub.filter(n => n.children.length > 0)
      return { item: { id: c.value, name: c.label, category: c.value, enabled: true } as ServiceItem, children: folderChildren }
    })
  }, [categoryTrees])

  const nodeById = useMemo(() => {
    const m = new Map<string, PortalNode>()
    const walk = (ns: PortalNode[]) => ns.forEach(n => { m.set(n.item.id, n); walk(n.children) })
    walk(displayTree)
    return m
  }, [displayTree])

  /** 递归收集某节点下所有「可申请服务」（叶子节点），带所属分类路径 */
  const collectLeaves = useCallback((nodes: PortalNode[], path: string[] = []): { item: ServiceItem; path: string[] }[] => {
    const out: { item: ServiceItem; path: string[] }[] = []
    for (const n of nodes) {
      if (n.children.length === 0) out.push({ item: n.item, path })
      else out.push(...collectLeaves(n.children, [...path, n.item.name]))
    }
    return out
  }, [])

  // 全量叶子服务(带分类路径)，搜索与「全部服务」共用
  const allLeaves = useMemo(() => {
    const out: { item: ServiceItem; path: string[] }[] = []
    for (const c of CATEGORIES) out.push(...collectLeaves(categoryTrees.get(c.value) || [], [c.label]))
    return out
  }, [categoryTrees, collectLeaves])

  // 右侧展示集合：搜索优先；否则取当前选中分类/子分类下的全部叶子服务
  const visibleLeaves = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (kw) {
      return allLeaves.filter(({ item }) =>
        item.name.toLowerCase().includes(kw) || (item.description || '').toLowerCase().includes(kw))
    }
    if (selectedId === ROOT_KEY) return allLeaves
    // 选中某个 ITIL 分类：展示该分类下全部叶子服务
    if (CATEGORIES.some(c => c.value === selectedId)) {
      const cat = CATEGORIES.find(c => c.value === selectedId)!
      return collectLeaves(categoryTrees.get(selectedId) || [], [cat.label])
    }
    // 选中某个子分类文件夹：展示其下叶子服务
    const node = nodeById.get(selectedId)
    if (!node) return allLeaves
    if (node.children.length === 0) return [{ item: node.item, path: [] }]
    const cat = CATEGORIES.find(c => c.value === node.item.category)
    return collectLeaves(node.children, cat ? [cat.label, node.item.name] : [node.item.name])
  }, [keyword, selectedId, categoryTrees, nodeById, allLeaves, collectLeaves])

  // 按所属分类分组，右侧展示时保留层级语义（避免一大片扁平卡片）
  const grouped = useMemo(() => {
    const g = new Map<string, ServiceItem[]>()
    for (const { item, path } of visibleLeaves) {
      const key = path.join(' / ') || '未分类'
      if (!g.has(key)) g.set(key, [])
      g.get(key)!.push(item)
    }
    return Array.from(g.entries())
  }, [visibleLeaves])

  const selectedTitle = selectedId === ROOT_KEY ? '全部服务'
    : CATEGORIES.some(c => c.value === selectedId) ? CATEGORIES.find(c => c.value === selectedId)!.label
    : (nodeById.get(selectedId)?.item.name || '全部服务')

  const toggle = (id: string) => setExpanded(prev => {
    const next = new Set(prev)
    next.has(id) ? next.delete(id) : next.add(id)
    return next
  })

  // 默认展开含子分类的分类，让用户进来就看到目录层级
  useEffect(() => {
    if (!displayTree.length) return
    setExpanded(prev => (prev.size ? prev : new Set(displayTree.filter(n => n.children.length).map(n => n.item.id))))
  }, [displayTree])

  // ---------------- 申请抽屉（门户内提交，不跳工单页） ----------------
  const [applyItem, setApplyItem] = useState<ServiceItem | null>(null)
  const [proc, setProc] = useState<Process | null>(null)
  const [schema, setSchema] = useState<FormSchema | null>(null)
  const [schemaLoading, setSchemaLoading] = useState(false)
  // 表单值（含内置字段 title/description/risk_level + 自定义字段），提交时由 splitBuiltinValues 拆回一等列与 custom_fields
  const [formValues, setFormValues] = useState<Record<string, unknown>>({})
  const [submitting, setSubmitting] = useState(false)
  const [createdId, setCreatedId] = useState<string | null>(null)

  const openApply = async (item: ServiceItem) => {
    setApplyItem(item)
    setCreatedId(null)
    setSchema(null)
    setProc(null)
    setSchemaLoading(true)
    setFormValues({})
    try {
      // 表单来源优先级：绑定流程的主表单（流程设计第一步产出） > 目录项自定义作用域表单
      let s: FormSchema | null = null
      let p: Process | null = null
      if (item.process_id) {
        p = await getProcess(item.process_id)
        const parsed = parseFormSchema(p?.form)
        if (parsed.fields.length) s = parsed
      }
      setProc(p)
      if (!s) s = await getFormSchema(item.form_scope || `catalog:${item.id}`)
      setSchema(s)
      // 预填默认值：内置字段(title=申请：服务名, risk_level=low) + 自定义字段默认
      const merged = withBuiltinFields(s)
      const defaults: Record<string, unknown> = { risk_level: 'low' }
      for (const f of merged.fields || []) if (f.default !== undefined) defaults[f.key] = f.default
      if (!('title' in defaults) || !defaults.title) defaults.title = `申请：${item.name}`
      setFormValues(defaults)
    } catch { setSchema(null); setFormValues({}) }
    setSchemaLoading(false)
  }

  const closeApply = () => { setApplyItem(null); setProc(null); setSchema(null); setCreatedId(null) }

  const submitApply = async () => {
    if (!applyItem) return
    // 必填校验前置到前端：内置字段与自定义字段一并校验
    const merged = withBuiltinFields(schema)
    const missing = merged.fields.filter(f => f.required && !f.readonly).find(f => {
      const v = formValues[f.key]
      return v === undefined || v === null || v === '' || (Array.isArray(v) && v.length === 0)
    })
    if (missing) { message.error(`请填写必填项：${missing.label}`); return }
    setSubmitting(true)
    try {
      const { builtin, custom } = splitBuiltinValues(formValues)
      const tk = await apiFetch<{ id: string }>(`/itsm/catalog/${applyItem.id}/fulfill`, {
        method: 'POST',
        body: JSON.stringify({
          title: String(builtin.title ?? '').trim(),
          description: builtin.description ?? '',
          risk_level: builtin.risk_level ?? 'low',
          custom_fields: custom,
          created_by: me?.username || 'portal',
        }),
      })
      setCreatedId(tk?.id || '')
      message.success('申请已提交')
    } catch (e: any) {
      message.error(`提交失败: ${e.message}`)
    }
    setSubmitting(false)
  }

  // ---------------- 渲染 ----------------
  const renderTreeNode = (node: PortalNode, depth = 0): JSX.Element => {
    const it = node.item
    const isCategory = CATEGORIES.some(c => c.value === it.id)
    const isFolder = node.children.length > 0
    const expandable = isCategory || isFolder
    const open = expanded.has(it.id)
    const active = selectedId === it.id
    const count = isCategory
      ? collectLeaves(categoryTrees.get(it.id) || []).length
      : (isFolder ? collectLeaves(node.children).length : 0)
    return (
      <div key={it.id}>
        <div
          role="button"
          tabIndex={0}
          onClick={() => { setSelectedId(it.id); if (expandable) toggle(it.id) }}
          onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setSelectedId(it.id); if (expandable) toggle(it.id) } }}
          style={{ paddingLeft: `${8 + depth * 14}px` }}
          className={`group flex items-center gap-1.5 pr-2 py-1.5 rounded-md cursor-pointer text-sm transition-colors
            ${active ? 'bg-primary/10 text-primary font-medium' : 'hover:bg-muted/60'}`}
        >
          {expandable
            ? (open ? <ChevronDown className="h-3.5 w-3.5 shrink-0 opacity-60" /> : <ChevronRight className="h-3.5 w-3.5 shrink-0 opacity-60" />)
            : <span className="w-3.5 shrink-0" />}
          <FolderTree className={`h-3.5 w-3.5 shrink-0 ${active ? 'text-primary' : 'text-muted-foreground'}`} />
          <span className="truncate flex-1">{it.name}</span>
          {expandable && <span className="text-[10px] text-muted-foreground shrink-0">{count}</span>}
        </div>
        {expandable && open && <div>{node.children.map(c => renderTreeNode(c, depth + 1))}</div>}
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">服务门户</h1>
          <p className="text-sm text-muted-foreground">按目录浏览并直接发起申请（申请进度请在「工单列表」页用「我的工单」筛选查看）</p>
        </div>
        <div className="relative w-64">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input value={keyword} onChange={e => setKeyword(e.target.value)} placeholder="搜索服务…" className="pl-8 h-9" />
        </div>
      </div>

      <div className="flex gap-4 items-start">
        {/* 左：5 大分类作为一级目录(+ 子分类文件夹) */}
        <Card className="w-64 shrink-0">
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-1.5"><Layers className="h-3.5 w-3.5" />服务分类</CardTitle>
          </CardHeader>
          <CardContent className="pb-3 max-h-[calc(100vh-220px)] overflow-y-auto">
            {loading ? (
              <div className="animate-pulse space-y-2">{[1, 2, 3, 4].map(i => <div key={i} className="h-7 bg-muted rounded" />)}</div>
            ) : (
              <div className="space-y-0.5">
                <div
                  role="button" tabIndex={0}
                  onClick={() => setSelectedId(ROOT_KEY)}
                  onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setSelectedId(ROOT_KEY) } }}
                  className={`flex items-center gap-1.5 px-2 py-1.5 rounded-md cursor-pointer text-sm transition-colors
                    ${selectedId === ROOT_KEY ? 'bg-primary/10 text-primary font-medium' : 'hover:bg-muted/60'}`}
                >
                  <Package className="h-3.5 w-3.5" />
                  <span className="flex-1">全部服务</span>
                  <span className="text-[10px] text-muted-foreground">{allLeaves.length}</span>
                </div>
                {displayTree.map(n => renderTreeNode(n))}
                {allLeaves.length === 0 && <p className="text-xs text-muted-foreground py-4 text-center">暂无已上线服务</p>}
              </div>
            )}
          </CardContent>
        </Card>

        {/* 右：选中分类下的可申请服务卡片 */}
        <Card className="flex-1 min-w-0">
          <CardHeader className="py-3">
            <CardTitle className="text-sm flex items-center gap-1.5">
              <Package className="h-3.5 w-3.5" />
              {keyword.trim() ? `搜索结果「${keyword.trim()}」` : selectedTitle}
              <span className="text-[10px] font-normal text-muted-foreground ml-1">{visibleLeaves.length} 项服务</span>
            </CardTitle>
          </CardHeader>
          <CardContent>
            {loading ? (
              <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3 animate-pulse">
                {[1, 2, 3, 4, 5, 6].map(i => <div key={i} className="h-28 bg-muted rounded" />)}
              </div>
            ) : visibleLeaves.length === 0 ? (
              <p className="text-sm text-muted-foreground py-10 text-center">
                {keyword.trim() ? '没有匹配的服务，换个关键词试试。' : '该分类下暂无已上线服务，请联系管理员在「服务目录定义」中配置。'}
              </p>
            ) : (
              <div className="space-y-5">
                {grouped.map(([groupName, items]) => (
                  <div key={groupName}>
                    {grouped.length > 1 && (
                      <div className="flex items-center gap-1.5 mb-2 text-xs text-muted-foreground">
                        <FolderTree className="h-3 w-3" />{groupName}
                      </div>
                    )}
                    <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-3">
                      {items.map(it => (
                        <div key={it.id}
                          role="button" tabIndex={0}
                          onClick={() => openApply(it)}
                          onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); openApply(it) } }}
                          className="border rounded-lg p-4 cursor-pointer hover:border-primary/60 hover:shadow-md hover:-translate-y-0.5 transition-all duration-200 group focus:outline-none focus:ring-2 focus:ring-primary/40">
                          <div className="flex items-start justify-between">
                            <Tag color="default" className="text-[10px]">{catLabel(it.category)}</Tag>
                            <Tag color={it.process_id ? 'default' : 'warning'} className="text-[10px]">{it.process_id ? '含流程' : '未关联流程'}</Tag>
                          </div>
                          <h5 className="font-medium text-sm mt-2 group-hover:text-primary transition-colors">{it.name}</h5>
                          {it.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{it.description}</p>}
                          <Button size="middle" type="default" className="mt-3 w-full pointer-events-none group-hover:border-primary/60 group-hover:text-primary">
                            {it.process_id ? '发起流程' : '发起申请'} <ChevronRight className="h-3 w-3 group-hover:translate-x-0.5 transition-transform" />
                          </Button>
                        </div>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* 申请抽屉：门户内提交，不离开当前页面 */}
      {applyItem && (
        <div className="fixed inset-0 z-50 flex justify-end">
          <div className="absolute inset-0 bg-black/40 backdrop-blur-[1px]" onClick={closeApply} />
          <div className="relative w-full sm:w-[680px] max-w-[95vw] h-full bg-background border-l shadow-2xl flex flex-col animate-in slide-in-from-right duration-200">
            <div className="flex items-start justify-between px-5 py-4 border-b shrink-0">
              <div className="min-w-0">
                <h3 className="font-semibold truncate">{applyItem.name}</h3>
                <p className="text-xs text-muted-foreground mt-0.5 line-clamp-2">
                  {applyItem.description || (proc ? `提交后将进入「${proc.name}」流程审批` : '提交后直接生成工单')}
                </p>
              </div>
              <button onClick={closeApply} className="p-1.5 rounded-md hover:bg-muted transition-colors shrink-0" aria-label="关闭">
                <X className="h-4 w-4" />
              </button>
            </div>

            <div className="flex-1 overflow-y-auto px-5 py-4 space-y-4">
              {createdId !== null ? (
                <div className="flex flex-col items-center justify-center py-14 text-center space-y-3">
                  <CheckCircle2 className="h-12 w-12 text-blue-500" />
                  <div>
                    <p className="font-medium">申请已提交</p>
                    <p className="text-xs text-muted-foreground mt-1">
                      {applyItem.process_id ? '已按目录绑定的流程创建工单并进入流转' : '已创建工单，等待处理'}
                    </p>
                  </div>
                  <div className="flex gap-2 pt-2">
                    <Button type="default" size="middle" onClick={closeApply}>继续浏览</Button>
                    {createdId && <Button type="primary" size="middle" onClick={() => navigate(`/itsm/tickets?id=${createdId}`)}>查看工单</Button>}
                  </div>
                </div>
              ) : (
                <Tabs defaultActiveKey="form" className="space-y-4" items={[
                  {
                    key: 'form',
                    label: '填写申请',
                    children: <div className="space-y-4 mt-2">
                      {schemaLoading ? (
                        <div className="flex items-center gap-2 text-xs text-muted-foreground py-4">
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />加载表单…
                        </div>
                      ) : schema ? (
                        <DynamicForm
                          schema={withBuiltinFields(schema)}
                          values={formValues}
                          context={formValues}
                          onChange={(k, v) => setFormValues(prev => ({ ...prev, [k]: v }))}
                          loadOptions={getFormOptions}
                        />
                      ) : (
                        <p className="text-xs text-muted-foreground italic py-2">该服务未配置表单，填写上方内容即可提交。</p>
                      )}
                    </div>,
                  },
                  {
                    key: 'flow',
                    label: '流程过程',
                    children: <div className="mt-2">
                      {proc && proc.steps && proc.steps.length > 0 ? (
                        <ProcessFlowView steps={proc.steps} processName={proc.name} />
                      ) : (
                        <p className="text-xs text-muted-foreground italic py-4 text-center">该服务未关联流程，无流程图可展示。</p>
                      )}
                    </div>,
                  },
                ]} />
              )}
            </div>

            {createdId === null && (
              <div className="px-5 py-3 border-t flex justify-end gap-2 shrink-0">
                <Button type="default" onClick={closeApply} disabled={submitting}>取消</Button>
                <Button type="primary" onClick={submitApply} disabled={submitting}>
                  {submitting ? <Loader2 className="h-4 w-4 mr-1.5 animate-spin" /> : <Send className="h-4 w-4 mr-1.5" />}
                  提交申请
                </Button>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
