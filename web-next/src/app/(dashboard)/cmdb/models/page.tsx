'use client'

import { useEffect, useRef, useState } from 'react'
import Link from 'next/link'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, GripVertical, Edit2, Download, Upload, Settings2, Shield, History, Gauge, Search, ChevronDown, ChevronRight } from 'lucide-react'
import { useCan } from '@/stores/permissions'
import { cn } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import TopologyGraph from '@/components/TopologyGraph'

type FieldType =
  | 'string' | 'longtext' | 'number' | 'float'
  | 'enum' | 'multi-enum' | 'boolean'
  | 'ref' | 'user' | 'date' | 'datetime' | 'json' | 'list'

interface SchemaField {
  name: string
  type: FieldType
  required: boolean
  description: string
  options?: string[]
  multi?: boolean
  unique?: boolean
  ref_model?: string
  ref_display?: string
  autofill?: string[]
  pattern?: string
  maxLength?: number
  min?: number
  max?: number
}

// 模型级关系定义（与属性字段并列，落库到模型类型的 relations）。
type Cardinality = 'one_to_one' | 'one_to_many' | 'many_to_many'
interface RelationDef {
  name: string
  relation_type: string
  target_model: string
  cardinality: Cardinality
  reverse_name?: string
  description?: string
}
const cardinalityLabel = (c: string) =>
  c === 'one_to_one' ? '1:1 一对一' : c === 'one_to_many' ? '1:N 一对多' : 'M:N 多对多'

// 字段类型元数据：下拉分组 + 中文标签 + 该类型专属的配置项(选中类型后才显示)。
type FieldConfigKey = 'options' | 'multi' | 'ref' | 'autofill' | 'pattern' | 'maxLength' | 'minmax'
interface FieldTypeMeta {
  value: FieldType
  label: string
  group: string
  desc: string
  configs: FieldConfigKey[]
}
const FIELD_TYPES: FieldTypeMeta[] = [
  { value: 'string', label: '短文本', group: '文本', desc: '单行文本，如名称、IP、版本号', configs: ['pattern', 'maxLength'] },
  { value: 'longtext', label: '长文本', group: '文本', desc: '多行文本，如安装路径、备注说明', configs: ['pattern', 'maxLength'] },
  { value: 'number', label: '整数', group: '数值', desc: '整数，如端口、CPU 核数、连接数', configs: ['minmax'] },
  { value: 'float', label: '浮点数', group: '数值', desc: '带小数，如内存容量(GB)、使用率(%)', configs: ['minmax'] },
  { value: 'enum', label: '枚举(单选)', group: '选择', desc: '固定选项下拉，如环境(prod/dev/test)', configs: ['options'] },
  { value: 'multi-enum', label: '枚举(多选)', group: '选择', desc: '可多选的枚举，实例值存为数组', configs: ['options', 'multi'] },
  { value: 'boolean', label: '布尔', group: '选择', desc: '是/否开关，如是否开启 binlog', configs: [] },
  { value: 'user', label: '用户', group: '选择', desc: '从平台用户列表选择（存用户名），如负责人、审批人', configs: [] },
  { value: 'date', label: '日期', group: '日期时间', desc: '年-月-日', configs: [] },
  { value: 'datetime', label: '日期时间', group: '日期时间', desc: '含时分秒的完整时间', configs: [] },
  { value: 'json', label: 'JSON', group: '特殊', desc: '结构化数据，如配置片段', configs: [] },
  { value: 'list', label: '列表', group: '特殊', desc: '同构值数组，如标签集合', configs: [] },
]
const FIELD_GROUPS = ['文本', '数值', '选择', '日期时间', '特殊']
const fieldTypeMeta = (t: FieldType) => FIELD_TYPES.find(m => m.value === t)

interface CIModel {
  id: string
  name: string
  description: string
  icon: string
  category: string
  sub_category?: string
  show_in_menu: boolean
  parent_id?: string
  builtin?: boolean
  author?: string
  fields: SchemaField[]
  relations: RelationDef[]
  ciCount: number
}

interface Category {
  id: string
  name: string
  builtin: boolean
}

// 内置分类兜底（API 不可用或首次加载时使用），与后端 seed 一致
const DEFAULT_CATEGORIES = [
  { name: '业务资源', emoji: '🏢', color: 'bg-emerald-500' },
  { name: '应用资源', emoji: '📱', color: 'bg-blue-500' },
  { name: '中间件资源', emoji: '🔌', color: 'bg-violet-500' },
  { name: '数据库资源', emoji: '🗄️', color: 'bg-amber-500' },
  { name: '基础设施资源', emoji: '🖥️', color: 'bg-slate-500' },
  { name: '容器资源', emoji: '☸️', color: 'bg-teal-500' },
  { name: '云资源', emoji: '☁️', color: 'bg-cyan-500' },
] as const

// 自定义分类的兜底样式
const CUSTOM_CAT_STYLE = { emoji: '🏷️', color: 'bg-cyan-500' }

function catStyle(name: string) {
  return DEFAULT_CATEGORIES.find(c => c.name === name) || CUSTOM_CAT_STYLE
}

const ICONS = ['🖥️','🌐','🗄️','🔌','⚖️','📡','🛡️','💾','☸️','📱','⚙️','🔗','📦','🏢','🔧','📊']

// 内置模型 icon 字段存 lucide 图标名，映射为语义 emoji 展示（自定义模型直接选 emoji，不受影响）
const LUCIDE_EMOJI: Record<string, string> = {
  server: '🖥️',
  cpu: '🖥️',
  network: '🌐',
  database: '🗄️',
  layers: '📦',
  warehouse: '🏭',
  shield: '🛡️',
  scale: '⚖️',
  rows: '🗃️',
  route: '🛣️',
  'hard-drive': '💾',
  building: '🏢',
  briefcase: '💼',
  'app-window': '📱',
}

// 模型图标渲染：emoji 直接显示；lucide 名走映射；未知兜底 📦
function modelIcon(icon: string) {
  return ICONS.includes(icon) ? icon : LUCIDE_EMOJI[icon] || '📦'
}

function schemaToFields(schema: Record<string, any> = {}): SchemaField[] {
  return Object.entries(schema).map(([name, def]) => ({
    name,
    type: (def?.type || 'string') as FieldType,
    required: !!def?.required,
    description: def?.description || '',
    options: def?.options,
    multi: !!def?.multi,
    unique: !!def?.unique,
    ref_model: def?.ref_model,
    ref_display: def?.ref_display,
    autofill: def?.autofill,
    pattern: def?.pattern,
    maxLength: def?.max_length,
    min: def?.min,
    max: def?.max,
  }))
}

function fieldsToSchema(fields: SchemaField[]) {
  return fields.reduce((acc, f) => ({
    ...acc,
    [f.name]: {
      type: f.type, required: f.required, description: f.description,
      ...(f.options?.length ? { options: f.options } : {}),
      ...(f.multi ? { multi: true } : {}),
      ...(f.unique ? { unique: true } : {}),
      ...(f.ref_model ? { ref_model: f.ref_model } : {}),
      ...(f.ref_display ? { ref_display: f.ref_display } : {}),
      ...(f.autofill?.length ? { autofill: f.autofill } : {}),
      ...(f.pattern ? { pattern: f.pattern } : {}),
      ...(f.maxLength ? { max_length: f.maxLength } : {}),
      ...(f.min != null ? { min: f.min } : {}),
      ...(f.max != null ? { max: f.max } : {}),
    },
  }), {} as Record<string, any>)
}

export default function CMDBModelsPage() {
  const [models, setModels] = useState<CIModel[]>([])
  const [categories, setCategories] = useState<Category[]>(DEFAULT_CATEGORIES.map((c, i) => ({ id: c.name, name: c.name, builtin: true })))
  const [ciCounts, setCiCounts] = useState<Record<string, number>>({})
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editModel, setEditModel] = useState<CIModel | null>(null)
  // 父模型集合：被其他模型继承的模型（受保护：不可删除/修改，醒目标识）
  const parentIds = new Set(models.filter(m => m.parent_id).map(m => m.parent_id as string))
  // 模型搜索(按 ID/名称模糊) + 分类折叠(默认收起)
  const [searchQuery, setSearchQuery] = useState('')
  const [expandedCats, setExpandedCats] = useState<Set<string>>(new Set())
  const toggleCat = (name: string) => setExpandedCats(s => {
    const n = new Set(s); if (n.has(name)) n.delete(name); else n.add(name); return n
  })
  const [form, setForm] = useState({
    name: '', description: '', icon: '🖥️', category: '基础设施资源',
    sub_category: '', show_in_menu: true, parent_id: '',
  })
  const [fields, setFields] = useState<SchemaField[]>([])
  const [fieldDialogOpen, setFieldDialogOpen] = useState(false)
  const [editFieldIdx, setEditFieldIdx] = useState<number | null>(null)
  const [fieldForm, setFieldForm] = useState<SchemaField>({ name: '', type: 'string', required: false, description: '' })
  // 模型级关系定义（与属性字段并列）
  const [modelTab, setModelTab] = useState<'attrs' | 'rels'>('attrs')
  const [relations, setRelations] = useState<RelationDef[]>([])
  const [relDialogOpen, setRelDialogOpen] = useState(false)
  const [editRelIdx, setEditRelIdx] = useState<number | null>(null)
  const [relForm, setRelForm] = useState<RelationDef>({ name: '', relation_type: '', target_model: '', cardinality: 'one_to_many' })
  const [catDialogOpen, setCatDialogOpen] = useState(false)
  const [viewMode, setViewMode] = useState<'list' | 'graph'>('list')
  const [newCatName, setNewCatName] = useState('')
  // 新建模型成功后，引导用户去录入该模型的实例（互链：建模→填充）
  const [createdModel, setCreatedModel] = useState<{ id: string; name: string } | null>(null)

  // 模型版本历史(快照/回滚)
  const [verDialogOpen, setVerDialogOpen] = useState(false)
  const [verModel, setVerModel] = useState<CIModel | null>(null)
  const [versions, setVersions] = useState<{ version: number; name: string; created_at: string; schema: Record<string, any> }[]>([])

  const openVersions = async (model: CIModel) => {
    setVerModel(model); setVerDialogOpen(true)
    try {
      setVersions(await apiList<{ version: number; name: string; created_at: string; schema: Record<string, any> }>(`/cmdb/model-type/${model.id}/versions`))
    } catch (e: any) { setVersions([]); toast.error('加载版本失败: ' + e.message) }
  }

  // 字段消费度(MVD): 保存查询/表单引用统计
  const [usageOpen, setUsageOpen] = useState(false)
  const [usageModel, setUsageModel] = useState<CIModel | null>(null)
  const [fieldUsage, setFieldUsage] = useState<{ field: string; type: string; required: boolean; saved_queries: number; itsm_forms: number; total: number; unused: boolean }[]>([])
  const openUsage = async (model: CIModel) => {
    setUsageModel(model); setUsageOpen(true)
    try {
      const data = await apiFetch<{ fields: typeof fieldUsage; unused_count: number }>(`/cmdb/model-type/${model.id}/field-usage`)
      setFieldUsage(data.fields)
    } catch (e: any) { setFieldUsage([]); toast.error('加载字段消费度失败: ' + e.message) }
  }

  // 危险操作二次确认（回滚/删模型/删分类统一 ConfirmDialog）
  const [confirmTarget, setConfirmTarget] = useState<{ type: 'rollback'; version: number } | { type: 'model'; model: CIModel } | { type: 'category'; cat: Category } | null>(null)
  const [confirmSubmitting, setConfirmSubmitting] = useState(false)

  const confirmAction = async () => {
    const t = confirmTarget
    if (!t) return
    setConfirmSubmitting(true)
    try {
      if (t.type === 'rollback') {
        if (!verModel) return
        await apiFetch(`/cmdb/model-type/${verModel.id}/rollback/${t.version}`, { method: 'POST' })
        toast.success(`已回滚到 v${t.version}`)
        setVerDialogOpen(false); loadModels()
      } else if (t.type === 'model') {
        if (t.model.builtin) return
        await apiFetch(`/cmdb/model-type/${t.model.id}`, { method: 'DELETE' })
        toast.success('模型已删除'); loadModels()
      } else {
        if (t.cat.builtin) return
        await apiFetch(`/cmdb/model-categories/${t.cat.id}`, { method: 'DELETE' })
        toast.success('分类已删除'); loadCategories()
      }
      setConfirmTarget(null)
    } catch (e: any) { toast.error(e.message || '操作失败') } finally { setConfirmSubmitting(false) }
  }
  // P2-4 字段级规则权限
  const [ruleDialogOpen, setRuleDialogOpen] = useState(false)
  const [ruleModel, setRuleModel] = useState<CIModel | null>(null)
  const [rules, setRules] = useState<{ id: string; field: string; permission: string; action: 'hidden' | 'readonly' }[]>([])
  const [ruleForm, setRuleForm] = useState({ field: '', permission: 'cmdb:admin', action: 'hidden' as 'hidden' | 'readonly' })
  const canAdmin = useCan('cmdb:admin')
  const fileRef = useRef<HTMLInputElement>(null)

  const loadCategories = () => {
    apiList<Category>('/cmdb/model-categories')
      .then(list => {
        if (list.length > 0) setCategories(list)
      })
      .catch(() => {})
  }

  const loadModels = () => {
    apiList<any>('/cmdb/model-type?limit=1000').then(items => {
      setModels(items.map((m: any) => ({
        id: m.id, name: m.name, description: m.description || '',
        icon: m.icon || '📦', category: m.category || '基础设施资源',
        sub_category: m.sub_category || '', show_in_menu: m.show_in_menu !== false,
        parent_id: m.parent_id || '', builtin: !!m.builtin, author: m.author,
        fields: schemaToFields(m.schema), relations: (m.relations || []) as RelationDef[], ciCount: 0,
      })))
    }).catch((e: any) => { toast.error(`加载模型失败: ${e.message}`); setModels([]) })
    // 实例计数（单请求按 type_id 汇总）
    apiList<any>('/cmdb/ci?limit=2000').then(items => {
      const counts: Record<string, number> = {}
      for (const ci of items) counts[ci.type_id] = (counts[ci.type_id] || 0) + 1
      setCiCounts(counts)
    }).catch(() => {})
  }

  useEffect(() => { loadModels(); loadCategories() }, [])

  const openCreate = () => {
    setEditModel(null)
    setForm({ name: '', description: '', icon: '🖥️', category: categories[0]?.name || '基础设施资源', sub_category: '', show_in_menu: true, parent_id: '' })
    setFields([])
    setRelations([])
    setModelTab('attrs')
    setDialogOpen(true)
  }

  const openEdit = (model: CIModel) => {
    if (parentIds.has(model.id)) {
      toast.error('父模型受保护，不可修改')
      return
    }
    setEditModel(model)
    setForm({
      name: model.name, description: model.description, icon: model.icon, category: model.category,
      sub_category: model.sub_category || '', show_in_menu: model.show_in_menu, parent_id: model.parent_id || '',
    })
    setFields([...model.fields])
    setRelations([...model.relations])
    setModelTab('attrs')
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!form.name.trim()) return
    const payload: any = {
      name: form.name, description: form.description,
      icon: form.icon, category: form.category, sub_category: form.sub_category,
      show_in_menu: form.show_in_menu, parent_id: form.parent_id || null,
      schema: fieldsToSchema(fields),
      relations,
    }
    try {
      if (editModel) {
        await apiFetch(`/cmdb/model-type/${editModel.id}`, { method: 'PUT', body: JSON.stringify(payload) })
        toast.success('模型已更新')
      } else {
        const created = await apiFetch<{ id?: string }>('/cmdb/model-type', { method: 'POST', body: JSON.stringify(payload) })
        toast.success(`模型「${form.name}」已创建`)
        if (created?.id) setCreatedModel({ id: created.id, name: form.name })
      }
      setDialogOpen(false)
      loadModels()
    } catch (e: any) {
      toast.error(`保存模型失败: ${e.message}`)
    }
  }

  const handleDelete = async (model: CIModel) => {
    if (model.builtin) return
    const ciCount = ciCounts[model.id] || 0
    if (ciCount > 0) {
      toast.error(`该模型下还有 ${ciCount} 条实例记录，请先删除这些实例后再删除模型`)
      return
    }
    if (parentIds.has(model.id)) {
      const kids = models.filter(m => m.parent_id === model.id).map(m => m.name)
      toast.error(`该模型为父模型，还有 ${kids.length} 个子模型「${kids.join('」「')}」在使用该模型，请先删除这些子模型`)
      return
    }
    setConfirmTarget({ type: 'model', model })
  }

  const handleDeleteField = (idx: number) => setFields(prev => prev.filter((_, i) => i !== idx))

  const openRelDialog = (idx?: number) => {
    if (idx !== undefined) { setEditRelIdx(idx); setRelForm({ ...relations[idx] }) }
    else { setEditRelIdx(null); setRelForm({ name: '', relation_type: '', target_model: '', cardinality: 'one_to_many' }) }
    setRelDialogOpen(true)
  }

  const saveRel = () => {
    if (!relForm.name || !relForm.relation_type || !relForm.target_model) return
    if (editRelIdx !== null) setRelations(prev => prev.map((r, i) => i === editRelIdx ? { ...relForm } : r))
    else setRelations(prev => [...prev, { ...relForm }])
    setRelDialogOpen(false)
  }

  const handleDeleteRelationDef = (idx: number) => setRelations(prev => prev.filter((_, i) => i !== idx))

  const openFieldDialog = (idx?: number) => {
    if (idx !== undefined) { setEditFieldIdx(idx); setFieldForm({ ...fields[idx] }) }
    else { setEditFieldIdx(null); setFieldForm({ name: '', type: 'string', required: false, description: '' }) }
    setFieldDialogOpen(true)
  }

  const saveField = () => {
    if (!fieldForm.name) return
    if (editFieldIdx !== null) setFields(prev => prev.map((f, i) => i === editFieldIdx ? { ...fieldForm } : f))
    else setFields(prev => [...prev, { ...fieldForm }])
    setFieldDialogOpen(false)
  }

  const handleAddCategory = async () => {
    const name = newCatName.trim()
    if (!name) { toast.error('请输入分类名称'); return }
    try {
      await apiFetch('/cmdb/model-categories', { method: 'POST', body: JSON.stringify({ name }) })
      toast.success(`分类「${name}」已创建`)
      setNewCatName('')
      loadCategories()
    } catch (e: any) { toast.error(e.message) }
  }

  const handleDeleteCategory = async (c: Category) => {
    if (c.builtin) { toast.error('内置分类不可删除'); return }
    setConfirmTarget({ type: 'category', cat: c })
  }

  // P2-4 字段级规则权限
  const openFieldRules = (model: CIModel) => {
    setRuleModel(model)
    setRuleForm({ field: model.fields[0]?.name || '', permission: 'cmdb:admin', action: 'hidden' })
    apiList<{ id: string; field: string; permission: string; action: 'hidden' | 'readonly' }>(`/cmdb/field-rules?model_type_id=${model.id}`)
      .then(setRules).catch(() => setRules([]))
    setRuleDialogOpen(true)
  }

  const handleAddFieldRule = async () => {
    if (!ruleModel || !ruleForm.field) return
    try {
      await apiFetch('/cmdb/field-rules', { method: 'POST', body: JSON.stringify({ model_type_id: ruleModel.id, ...ruleForm }) })
      toast.success('字段规则已添加')
      apiList<{ id: string; field: string; permission: string; action: 'hidden' | 'readonly' }>(`/cmdb/field-rules?model_type_id=${ruleModel.id}`).then(setRules)
    } catch (e: any) { toast.error(e.message) }
  }

  const handleDeleteFieldRule = async (id: string) => {
    try { await apiFetch(`/cmdb/field-rules/${id}`, { method: 'DELETE' }); setRules(prev => prev.filter(r => r.id !== id)) }
    catch (e: any) { toast.error(e.message) }
  }

  const handleExport = async () => {
    try {
      const data = await apiFetch('/cmdb/model-type/export')
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url; a.download = 'cmdb-model-types.json'; a.click()
      URL.revokeObjectURL(url)
    } catch (e) { console.error(e) }
  }

  const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    try {
      const text = await file.text()
      await apiFetch('/cmdb/model-type/import', { method: 'POST', body: text })
      loadModels()
    } catch (err) { console.error(err) }
    e.target.value = ''
  }

  return (
    <div className="p-6 space-y-4">
      {createdModel && (
        <div className="flex items-center justify-between gap-3 rounded-lg border border-emerald-200 dark:border-emerald-500/30 bg-emerald-50 dark:bg-emerald-500/10 px-4 py-3">
          <p className="text-sm text-emerald-800 dark:text-emerald-200">模型「{createdModel.name}」已创建，现在去录入它的实例吧。</p>
          <div className="flex items-center gap-2 shrink-0">
            <Link href={`/cmdb/instances?type=${encodeURIComponent(createdModel.id)}`}>
              <Button size="sm">去录入实例 →</Button>
            </Link>
            <Button variant="ghost" size="sm" onClick={() => setCreatedModel(null)}>稍后</Button>
          </div>
        </div>
      )}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CI 模型管理</h1>
          <p className="text-sm text-muted-foreground">定义 CI 类型、分类、继承关系及属性 Schema</p>
        </div>
        <div className="flex gap-2">
          <Button variant={viewMode === 'list' ? 'default' : 'outline'} size="sm" onClick={() => setViewMode('list')}>列表</Button>
          <Button variant={viewMode === 'graph' ? 'default' : 'outline'} size="sm" onClick={() => setViewMode('graph')}>模型关系图</Button>
          <Button variant="outline" onClick={handleExport}><Download className="h-4 w-4" />导出</Button>
          <Button variant="outline" onClick={() => fileRef.current?.click()}><Upload className="h-4 w-4" />导入</Button>
          {canAdmin && <Button variant="outline" onClick={() => setCatDialogOpen(true)}><Settings2 className="h-4 w-4" />管理分类</Button>}
          {canAdmin && <Button onClick={openCreate}><Plus className="h-4 w-4" />新建模型</Button>}
        </div>
      </div>

      {/* 模型版本历史 */}
      <Dialog open={verDialogOpen} onClose={() => setVerDialogOpen(false)} title={`版本历史：${verModel?.name || ''}`}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          {versions.length === 0 && <p className="text-sm text-muted-foreground py-4 text-center">暂无版本快照（模型更新时会自动留痕）</p>}
          {versions.map(v => (
            <div key={v.version} className="border rounded-md p-3 flex items-center justify-between">
              <div>
                <p className="text-sm font-medium">v{v.version} · {v.name}</p>
                <p className="text-xs text-muted-foreground">{v.created_at?.slice(0, 19)?.replace('T', ' ') || ''} · {Object.keys(v.schema || {}).length} 个字段</p>
              </div>
              <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => setConfirmTarget({ type: 'rollback', version: v.version })}>回滚</Button>
            </div>
          ))}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setVerDialogOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      {/* 字段消费度(MVD) */}
      <Dialog open={usageOpen} onClose={() => setUsageOpen(false)} title={`字段消费度：${usageModel?.name || ''}`}>
        <div className="space-y-2 max-h-[60vh] overflow-y-auto">
          <p className="text-xs text-muted-foreground">扫描保存查询与 ITSM 表单对该模型字段的引用（无消费 = 僵尸字段，可考虑下线）</p>
          {fieldUsage.map(f => (
            <div key={f.field} className={cn("border rounded-md px-3 py-2 text-xs flex items-center gap-2", f.unused && "opacity-50")}>
              <span className="font-medium w-32 truncate">{f.field}</span>
              <Badge variant="outline" className="text-[9px]">{f.type}</Badge>
              {f.unused
                ? <Badge variant="destructive" className="text-[9px]">无消费</Badge>
                : <span className="text-muted-foreground flex items-center gap-2 ml-auto">查询 {f.saved_queries} · 表单 {f.itsm_forms}</span>}
            </div>
          ))}
          {fieldUsage.length === 0 && <p className="text-sm text-muted-foreground py-4 text-center">无字段数据</p>}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setUsageOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      {viewMode === 'graph' && (
        <Card>
          <CardHeader className="py-3"><CardTitle className="text-sm">模型关系图（继承边 = 父类→子类；引用边 = ref 字段 → 目标模型）</CardTitle></CardHeader>
          <CardContent>
            {models.length === 0 ? <p className="text-sm text-muted-foreground py-8 text-center">暂无模型数据</p> : (
              <TopologyGraph
                data={{
                  nodes: models.map(m => ({ id: m.id, name: m.name, type: m.category || 'other' })),
                  edges: [
                    ...models.filter(m => m.parent_id).map(m => ({ source: m.parent_id!, target: m.id, type: '继承' })),
                    ...models.flatMap(m =>
                      (m.fields || []).filter(f => f.type === 'ref' && f.ref_model)
                        .map(f => ({ source: m.id, target: f.ref_model!, type: '引用' }))
                    ),
                  ],
                }}
                width={720} height={420}
              />
            )}
          </CardContent>
        </Card>
      )}

      {/* 模型搜索 */}
      <div className="relative max-w-sm">
        <Search className="h-3.5 w-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={searchQuery}
          onChange={e => setSearchQuery(e.target.value)}
          placeholder="搜索模型 ID / 名称（模糊）…"
          className="pl-8 h-9 text-sm"
        />
      </div>

      {/* 按分类分组展示：分类标题(默认收起) + 模型卡片 */}
      {models.length === 0 ? (
        <Card className="text-center py-16">
          <CardContent>
            <p className="text-muted-foreground text-sm">还没有任何模型，点击右上角「新建模型」创建第一个 CI 模型。</p>
          </CardContent>
        </Card>
      ) : (() => {
        const q = searchQuery.trim().toLowerCase()
        const isSearch = q !== ''
        const filtered = isSearch ? models.filter(m => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q)) : models
        // 一级分类分组；分类内再按二级分类 sub_category 分子组（云资源按厂商分组）
        const groups: { name: string; emoji: string; color: string; subs: { name: string; list: typeof models }[] }[] = []
        const seen = new Set<string>()
        for (const c of categories) {
          const list = filtered.filter(m => m.category === c.name)
          if (list.length === 0) continue
          const st = catStyle(c.name)
          const subMap = new Map<string, typeof models>()
          for (const m of list) {
            const key = m.sub_category || ''
            if (!subMap.has(key)) subMap.set(key, [])
            subMap.get(key)!.push(m)
          }
          const subs = [...subMap.entries()].map(([name, l]) => ({ name, list: l }))
          groups.push({ name: c.name, emoji: st.emoji, color: st.color, subs })
          seen.add(c.name)
        }
        const rest = filtered.filter(m => !seen.has(m.category))
        if (rest.length > 0) groups.push({ name: '未分类', emoji: '🏷️', color: 'bg-gray-500', subs: [{ name: '', list: rest }] })
        if (groups.length === 0) {
          return (
            <Card className="text-center py-16">
              <CardContent><p className="text-muted-foreground text-sm">未找到与「{searchQuery}」匹配的模型。</p></CardContent>
            </Card>
          )
        }
        return (
          <div className="space-y-6">
            {groups.map(g => {
              const open = isSearch || expandedCats.has(g.name)
              const total = g.subs.reduce((a, s) => a + s.list.length, 0)
              return (
                <section key={g.name} className="space-y-3">
                  <div className="flex items-center gap-2 cursor-pointer select-none" onClick={() => toggleCat(g.name)} title={open ? '收起该分类' : '展开该分类'}>
                    {open ? <ChevronDown className="h-3.5 w-3.5 text-muted-foreground shrink-0" /> : <ChevronRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />}
                    <span className="text-base">{g.emoji}</span>
                    <h2 className="text-sm font-semibold">{g.name}</h2>
                    <span className="text-xs text-muted-foreground">{total} 个模型</span>
                    <div className="flex-1 border-t border-border/60" />
                  </div>
                  {open && (
                    <div className="space-y-4">
                      {g.subs.map(sub => (
                        <div key={sub.name || 'default'} className="space-y-2">
                          {sub.name && (
                            <div className="flex items-center gap-2">
                              <span className="text-xs font-medium text-muted-foreground">{sub.name}</span>
                              <span className="text-[10px] text-muted-foreground">{sub.list.length} 个模型</span>
                              <div className="flex-1 border-t border-border/40" />
                            </div>
                          )}
                          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                            {sub.list.map(model => {
                              const isParent = parentIds.has(model.id)
                              return (
                              <Card key={model.id} className={`group hover:shadow-md transition-shadow ${isParent ? 'ring-1 ring-amber-400/60 dark:ring-amber-500/40' : ''}`}>
                                <CardHeader className="py-3">
                                  <div className="flex items-start justify-between gap-2">
                                    <CardTitle className="text-sm min-w-0 flex-1">
                                      <div className="flex items-center gap-2 flex-wrap">
                                        <span className="text-lg shrink-0">{modelIcon(model.icon)}</span>
                                        <span className="font-medium break-words">{model.name}</span>
                                        {isParent && <Badge className="bg-amber-500 text-white text-[10px] shrink-0">父模型</Badge>}
                                      </div>
                                      <p className="text-[10px] text-muted-foreground font-normal mt-0.5 break-all">{model.id}</p>
                                    </CardTitle>
                                    {canAdmin && (
                                      <div className="flex shrink-0 pt-0.5">
                                        <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title="字段权限" onClick={() => openFieldRules(model)}><Shield className="h-3.5 w-3.5" /></Button>
                                        <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title="版本历史" onClick={() => openVersions(model)}><History className="h-3.5 w-3.5" /></Button>
                                        <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title="字段消费度" onClick={() => openUsage(model)}><Gauge className="h-3.5 w-3.5" /></Button>
                                        <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title={isParent ? '父模型受保护，不可修改' : '编辑'} disabled={isParent} onClick={() => openEdit(model)}><Edit2 className="h-3.5 w-3.5" /></Button>
                                        {!model.builtin && (
                                          <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0 text-destructive" title="删除" onClick={() => handleDelete(model)}><Trash2 className="h-3.5 w-3.5" /></Button>
                                        )}
                                      </div>
                                    )}
                                  </div>
                                </CardHeader>
                                <CardContent className="space-y-2">
                                  {model.description && <p className="text-xs text-muted-foreground line-clamp-2">{model.description}</p>}
                                  <div className="flex items-center gap-2 flex-wrap">
                                    <Badge className={cn("text-[10px] text-white", g.color)}>{g.emoji} {model.category}</Badge>
                                    {model.sub_category && <Badge variant="outline" className="text-[10px]">{model.sub_category}</Badge>}
                                    {model.parent_id && <Badge variant="secondary" className="text-[10px]">继承</Badge>}
                                    {model.builtin && <Badge variant="secondary" className="text-[10px]">内置</Badge>}
                                  </div>
                                  <div className="flex items-center gap-2">
                                    <Badge variant="secondary" className="text-[10px]">{(ciCounts[model.id] || 0)} 个实例</Badge>
                                    <Badge variant="outline" className="text-[10px]">{model.fields.length} 个字段</Badge>
                                  </div>
                                </CardContent>
                              </Card>
                            )
                            })}
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </section>
              )
            })}
          </div>
        )
      })()}

      {/* 模型编辑对话框 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editModel ? `编辑模型：${editModel.name}` : '新建模型'}>
        <div className="space-y-4">
          {editModel?.builtin && (
            <p className="text-[11px] text-amber-600 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/20 rounded-md px-2 py-1.5">
              内置模型：可修改描述/分类与增补字段；不可删除，内置字段建议保留（仅可加属性）
            </p>
          )}
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="server" disabled={!!editModel} /></div>
            <div><label className="text-sm font-medium">图标</label>
              <div className="flex gap-1 flex-wrap mt-1">
                {ICONS.map(icon => (
                  <button key={icon} className={cn("w-8 h-8 rounded border flex items-center justify-center text-sm", form.icon === icon ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10" : "border-gray-200 dark:border-gray-700")} onClick={() => setForm(f => ({ ...f, icon }))}>{icon}</button>
                ))}
              </div>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">分类</label>
              <select value={form.category} onChange={e => setForm(f => ({ ...f, category: e.target.value }))} className="w-full mt-1 rounded-md border border-gray-200 dark:border-gray-700 bg-background px-2 py-1.5 text-sm">
                {categories.map(c => <option key={c.id} value={c.name}>{catStyle(c.name).emoji} {c.name}</option>)}
              </select>
            </div>
            <div><label className="text-sm font-medium">二级分类</label><Input value={form.sub_category} onChange={e => setForm(f => ({ ...f, sub_category: e.target.value }))} placeholder="可选，自定义" /></div>
          </div>
          <div><label className="text-sm font-medium">描述</label><Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">继承父模型</label>
              <select value={form.parent_id} onChange={e => setForm(f => ({ ...f, parent_id: e.target.value }))} className="w-full mt-1 rounded-md border border-gray-200 dark:border-gray-700 bg-background px-2 py-1.5 text-sm">
                <option value="">（无）</option>
                {models.filter(m => m.id !== editModel?.id).map(m => <option key={m.id} value={m.id}>{m.name}</option>)}
              </select>
            </div>
            <div className="flex items-end gap-2">
              <input type="checkbox" id="show-in-menu" checked={form.show_in_menu} onChange={e => setForm(f => ({ ...f, show_in_menu: e.target.checked }))} className="rounded" />
              <label htmlFor="show-in-menu" className="text-sm font-medium">在菜单显示</label>
            </div>
          </div>

          {/* 字段列表 / 关系定义：两个子页签 */}
          <Tabs value={modelTab} onValueChange={(v) => setModelTab(v as 'attrs' | 'rels')}>
            <TabsList>
              <TabsTrigger value="attrs">属性字段 ({fields.length})</TabsTrigger>
              <TabsTrigger value="rels">关系 ({relations.length})</TabsTrigger>
            </TabsList>
            <TabsContent value="attrs" className="mt-3">
              <div className="flex items-center justify-between mb-2">
                <label className="text-sm font-medium">属性字段</label>
                {canAdmin && <Button variant="outline" size="sm" onClick={() => openFieldDialog()}><Plus className="h-3 w-3" />添加字段</Button>}
              </div>
              {fields.length === 0 ? (
                <div className="text-center py-4 border border-dashed rounded-md text-xs text-muted-foreground">暂无字段，点击上方按钮添加</div>
              ) : (
                <div className="border rounded-md divide-y max-h-48 overflow-y-auto">
                  {fields.map((f, idx) => (
                    <div key={idx} className="flex items-center gap-2 px-3 py-2 text-xs">
                      <GripVertical className="h-3 w-3 text-muted-foreground/30" />
                      <span className="font-medium min-w-[80px]">{f.name}</span>
                      <Badge variant="outline" className="text-[9px]">{f.type}</Badge>
                      {f.required && <Badge variant="destructive" className="text-[9px]">必填</Badge>}
                      {f.description && <span className="text-muted-foreground truncate flex-1">{f.description ? f.description : ''}</span>}
                      {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0" onClick={() => openFieldDialog(idx)}><Edit2 className="h-3 w-3" /></Button>}
                      {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0 text-destructive" onClick={() => handleDeleteField(idx)}><Trash2 className="h-3 w-3" /></Button>}
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
            <TabsContent value="rels" className="mt-3">
              <div className="flex items-center justify-between mb-2">
                <label className="text-sm font-medium">关系定义（声明关联类型 + 目标模型 + 基数）</label>
                {canAdmin && <Button variant="outline" size="sm" onClick={() => openRelDialog()}><Plus className="h-3 w-3" />添加关系</Button>}
              </div>
              {relations.length === 0 ? (
                <div className="text-center py-4 border border-dashed rounded-md text-xs text-muted-foreground">暂无关系定义；建立关系时将作为"未建模"自由关系处理</div>
              ) : (
                <div className="border rounded-md divide-y max-h-48 overflow-y-auto">
                  {relations.map((r, idx) => (
                    <div key={idx} className="flex items-center gap-2 px-3 py-2 text-xs">
                      <span className="font-medium min-w-[80px]">{r.name}</span>
                      <Badge variant="outline" className="text-[9px]">{r.relation_type}</Badge>
                      <span className="text-muted-foreground truncate">→ {r.target_model}</span>
                      <Badge variant="secondary" className="text-[9px]">{cardinalityLabel(r.cardinality)}</Badge>
                      {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0" onClick={() => openRelDialog(idx)}><Edit2 className="h-3 w-3" /></Button>}
                      {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0 text-destructive" onClick={() => handleDeleteRelationDef(idx)}><Trash2 className="h-3 w-3" /></Button>}
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.name}>保存</Button></DialogFooter>
      </Dialog>

      {/* 字段编辑对话框 */}
      <Dialog open={fieldDialogOpen} onClose={() => setFieldDialogOpen(false)} title={editFieldIdx !== null ? '编辑字段' : '添加字段'}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">字段名 *</label><Input value={fieldForm.name} onChange={e => setFieldForm(f => ({ ...f, name: e.target.value }))} placeholder="ip_address" /></div>
          <div>
            <label className="text-sm font-medium">类型</label>
            <select
              value={fieldForm.type}
              onChange={e => {
                const t = e.target.value as FieldType
                // 切换类型时重置专属配置，避免脏数据残留
                setFieldForm(f => ({ ...f, type: t, options: undefined, multi: false, ref_model: '', ref_display: '', autofill: [], pattern: '', maxLength: undefined, min: undefined, max: undefined }))
              }}
              className="w-full h-9 mt-1 text-sm border rounded-md bg-background px-2"
            >
              {!FIELD_TYPES.some(t => t.value === fieldForm.type) && (
                <option value={fieldForm.type} disabled>{fieldForm.type}（旧类型，兼容展示，保存后请改为新类型）</option>
              )}
              {FIELD_GROUPS.map(g => (
                <optgroup key={g} label={g}>
                  {FIELD_TYPES.filter(t => t.group === g).map(t => (
                    <option key={t.value} value={t.value}>{t.label}（{t.value}）</option>
                  ))}
                </optgroup>
              ))}
            </select>
            <p className="text-[10px] text-muted-foreground mt-1">{fieldTypeMeta(fieldForm.type)?.desc}</p>
          </div>
          {/* 动态配置面板：依据所选类型的 configs 显示对应输入方式（原平铺选中改为下拉+按类型展示） */}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('options') && (
            <div><label className="text-sm font-medium">枚举选项（逗号分隔）</label><Input value={(fieldForm.options || []).join(',')} onChange={e => setFieldForm(f => ({ ...f, options: e.target.value.split(',').map(s => s.trim()).filter(Boolean) }))} placeholder="prod,dev,test" /></div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('multi') && (
            <div className="flex items-center gap-2"><input type="checkbox" id="field-multi" checked={!!fieldForm.multi} onChange={e => setFieldForm(f => ({ ...f, multi: e.target.checked }))} className="rounded" /><label htmlFor="field-multi" className="text-sm font-medium">允许多选（实例值存为数组）</label></div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('ref') && (
            <div><label className="text-sm font-medium">引用目标模型 *</label>
              <select value={fieldForm.ref_model || ''} onChange={e => setFieldForm(f => ({ ...f, ref_model: e.target.value }))} className="w-full h-8 text-xs border rounded-md bg-background px-2">
                <option value="">选择目标模型...</option>
                {models.map(m => <option key={m.id} value={m.id}>{m.name}（{m.id}）</option>)}
              </select>
              <p className="text-[10px] text-muted-foreground mt-1">引用型字段的值取自目标模型的 CI 实例，写入时校验存在性</p>
            </div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('autofill') && (
            <div><label className="text-sm font-medium">自动填充（目标属性，逗号分隔）</label>
              <Input value={(fieldForm.autofill || []).join(',')} onChange={e => setFieldForm(f => ({ ...f, autofill: e.target.value.split(',').map(x => x.trim()).filter(Boolean) }))} placeholder="room_id,data_center_id" />
              <p className="text-[10px] text-muted-foreground mt-1">选中目标实例后，自动把目标 CI 的这些属性值填入本 CI（如选机柜自动带出机房/数据中心）</p>
            </div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('pattern') && (
            <div><label className="text-sm font-medium">格式校验（正则）</label><Input value={fieldForm.pattern || ''} onChange={e => setFieldForm(f => ({ ...f, pattern: e.target.value }))} placeholder="^\d{1,3}(\.\d{1,3}){3}$" /><p className="text-[10px] text-muted-foreground mt-1">录入时按正则校验，常用于邮箱/手机/IP，如 IP：{'^\d{1,3}(\.\d{1,3}){3}$'}</p></div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('maxLength') && (
            <div><label className="text-sm font-medium">最大长度</label><Input type="number" value={fieldForm.maxLength ?? ''} onChange={e => setFieldForm(f => ({ ...f, maxLength: e.target.value ? parseInt(e.target.value) : undefined }))} placeholder="短文本 256 / 长文本 2000" /></div>
          )}
          {fieldTypeMeta(fieldForm.type)?.configs.includes('minmax') && (
            <div className="flex gap-2">
              <div className="flex-1"><label className="text-sm font-medium">最小值</label><Input type="number" value={fieldForm.min ?? ''} onChange={e => setFieldForm(f => ({ ...f, min: e.target.value ? parseFloat(e.target.value) : undefined }))} /></div>
              <div className="flex-1"><label className="text-sm font-medium">最大值</label><Input type="number" value={fieldForm.max ?? ''} onChange={e => setFieldForm(f => ({ ...f, max: e.target.value ? parseFloat(e.target.value) : undefined }))} /></div>
            </div>
          )}
          <div className="flex items-center gap-2"><input type="checkbox" id="field-required" checked={fieldForm.required} onChange={e => setFieldForm(f => ({ ...f, required: e.target.checked }))} className="rounded" /><label htmlFor="field-required" className="text-sm font-medium">必填</label></div>
          <div className="flex items-center gap-2"><input type="checkbox" id="field-unique" checked={!!fieldForm.unique} onChange={e => setFieldForm(f => ({ ...f, unique: e.target.checked }))} className="rounded" /><label htmlFor="field-unique" className="text-sm font-medium">唯一（同类型内该字段值不可重复，写入时拦截）</label></div>
          <div><label className="text-sm font-medium">描述</label><Input value={fieldForm.description} onChange={e => setFieldForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setFieldDialogOpen(false)}>取消</Button><Button onClick={saveField} disabled={!fieldForm.name}>保存</Button></DialogFooter>
      </Dialog>

      {/* 关系定义编辑对话框 */}
      <Dialog open={relDialogOpen} onClose={() => setRelDialogOpen(false)} title={editRelIdx !== null ? '编辑关系' : '添加关系'}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">关系名称 *</label><Input value={relForm.name} onChange={e => setRelForm(f => ({ ...f, name: e.target.value }))} placeholder="部署于" /></div>
          <div><label className="text-sm font-medium">关系类型键 *</label><Input value={relForm.relation_type} onChange={e => setRelForm(f => ({ ...f, relation_type: e.target.value }))} placeholder="hosts（自由字符串，建议复用 depends_on / hosts / connects）" /></div>
          <div><label className="text-sm font-medium">目标模型 *</label>
            <select value={relForm.target_model} onChange={e => setRelForm(f => ({ ...f, target_model: e.target.value }))} className="w-full h-9 text-sm border rounded-md bg-background px-2">
              <option value="">选择目标模型...</option>
              {models.map(m => <option key={m.id} value={m.id}>{m.name}（{m.id}）</option>)}
            </select>
          </div>
          <div><label className="text-sm font-medium">基数 *</label>
            <select value={relForm.cardinality} onChange={e => setRelForm(f => ({ ...f, cardinality: e.target.value as Cardinality }))} className="w-full h-9 text-sm border rounded-md bg-background px-2">
              <option value="one_to_one">一对一 (1:1)</option>
              <option value="one_to_many">一对多 (1:N)</option>
              <option value="many_to_many">多对多 (M:N)</option>
            </select>
            <p className="text-[10px] text-muted-foreground mt-1">1:1 源/目标均唯一；1:N 目标唯一归属一个源；M:N 无约束。建关系时后端按此校验。</p>
          </div>
          <div><label className="text-sm font-medium">反向名称（对端显示，可选）</label><Input value={relForm.reverse_name || ''} onChange={e => setRelForm(f => ({ ...f, reverse_name: e.target.value }))} placeholder="被部署于" /></div>
          <div><label className="text-sm font-medium">说明（可选）</label><Input value={relForm.description || ''} onChange={e => setRelForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRelDialogOpen(false)}>取消</Button><Button onClick={saveRel} disabled={!relForm.name || !relForm.relation_type || !relForm.target_model}>保存</Button></DialogFooter>
      </Dialog>

      {/* 字段级规则权限对话框 */}
      <Dialog open={ruleDialogOpen} onClose={() => setRuleDialogOpen(false)} title={`字段权限 · ${ruleModel?.name || ''}`}>
        <div className="space-y-3">
          <div className="flex gap-2">
            <select value={ruleForm.field} onChange={e => setRuleForm(f => ({ ...f, field: e.target.value }))} className="flex-1 h-9 rounded-md border border-input bg-background px-2 py-1 text-sm">
              {(ruleModel?.fields || []).map(f => <option key={f.name} value={f.name}>{f.name}</option>)}
              {!(ruleModel?.fields || []).some(f => f.name === ruleForm.field) && ruleForm.field && <option value={ruleForm.field}>{ruleForm.field}</option>}
            </select>
            <select value={ruleForm.permission} onChange={e => setRuleForm(f => ({ ...f, permission: e.target.value }))} className="h-9 rounded-md border border-input bg-background px-2 py-1 text-sm">
              <option value="cmdb:admin">cmdb:admin</option>
              <option value="cmdb:write">cmdb:write</option>
              <option value="cmdb:delete">cmdb:delete</option>
              <option value="cmdb:read">cmdb:read</option>
            </select>
            <select value={ruleForm.action} onChange={e => setRuleForm(f => ({ ...f, action: e.target.value as 'hidden' | 'readonly' }))} className="h-9 rounded-md border border-input bg-background px-2 py-1 text-sm">
              <option value="hidden">隐藏</option>
              <option value="readonly">只读</option>
            </select>
            <Button onClick={handleAddFieldRule} disabled={!ruleForm.field}><Plus className="h-3 w-3" />添加</Button>
          </div>
          <p className="text-xs text-muted-foreground">规则语义：<strong>拥有该权限的用户</strong>对该字段隐藏/只读；不拥有该权限的用户不受影响。同一字段多条规则取最严格（隐藏 &gt; 只读）。</p>
          <div className="border rounded-md divide-y max-h-64 overflow-y-auto">
            {rules.length === 0 ? <p className="px-3 py-4 text-xs text-muted-foreground text-center">暂无规则，默认全部字段可见可编辑</p> : rules.map(r => (
              <div key={r.id} className="flex items-center justify-between px-3 py-2 text-sm">
                <span className="flex items-center gap-2">
                  <code className="text-xs font-mono">{r.field}</code>
                  <Badge variant={r.action === 'hidden' ? 'destructive' : 'warning'} className="text-[9px]">{r.action === 'hidden' ? '隐藏' : '只读'}</Badge>
                  <span className="text-xs text-muted-foreground">约束权限：{r.permission}</span>
                </span>
                <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-destructive" onClick={() => handleDeleteFieldRule(r.id)}><Trash2 className="h-3 w-3" /></Button>
              </div>
            ))}
          </div>
        </div>
        <DialogFooter><Button onClick={() => setRuleDialogOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      {/* 分类管理对话框（可扩展：新增/删除自定义分类） */}
      <Dialog open={catDialogOpen} onClose={() => setCatDialogOpen(false)} title="管理模型分类">
        <div className="space-y-3">
          <div className="flex gap-2">
            <Input value={newCatName} onChange={e => setNewCatName(e.target.value)} placeholder="新增分类名称，如：安全资源" onKeyDown={e => e.key === 'Enter' && handleAddCategory()} />
            <Button onClick={handleAddCategory} disabled={!newCatName.trim()}><Plus className="h-4 w-4" />新增</Button>
          </div>
          <div className="border rounded-md divide-y max-h-64 overflow-y-auto">
            {categories.map(c => (
              <div key={c.id} className="flex items-center justify-between px-3 py-2 text-sm">
                <span className="flex items-center gap-2">
                  <span>{catStyle(c.name).emoji}</span>{c.name}
                  {c.builtin ? <Badge variant="secondary" className="text-[9px]">内置</Badge> : <Badge className="text-[9px] bg-violet-500">自定义</Badge>}
                </span>
                {!c.builtin && <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-destructive" onClick={() => handleDeleteCategory(c)}><Trash2 className="h-3 w-3" /></Button>}
              </div>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">内置 5 类不可删除；有模型引用的分类不可删除。</p>
        </div>
        <DialogFooter><Button onClick={() => setCatDialogOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      <input ref={fileRef} type="file" accept="application/json" className="hidden" onChange={handleImportFile} />

      <ConfirmDialog
        open={!!confirmTarget}
        onClose={() => setConfirmTarget(null)}
        onConfirm={confirmAction}
        title={confirmTarget?.type === 'rollback' ? '确认回滚' : confirmTarget?.type === 'model' ? '确认删除模型' : '确认删除分类'}
        description={confirmTarget?.type === 'rollback' && verModel ? `回滚「${verModel.name}」到 v${confirmTarget.version}？当前 schema 会留痕为下一个版本。` : confirmTarget?.type === 'model' ? `确认删除模型「${confirmTarget.model.name}」？此操作不可撤销，子模型与实例将一并受限。` : confirmTarget?.type === 'category' ? `确认删除分类「${confirmTarget.cat.name}」？` : ''}
        requireText={confirmTarget?.type === 'model' ? confirmTarget.model.name : undefined}
        requireLabel="模型名称"
        confirmLabel="确定"
        loading={confirmSubmitting}
      />
    </div>
  )
}
