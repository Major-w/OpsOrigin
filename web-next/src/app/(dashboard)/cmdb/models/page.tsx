'use client'

import { useEffect, useRef, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, GripVertical, Edit2, Download, Upload, Settings2, Shield, History } from 'lucide-react'
import { useCan } from '@/stores/permissions'
import { cn } from '@/lib/utils'
import { toast } from '@/stores/toast'

interface SchemaField {
  name: string
  type: 'string' | 'number' | 'boolean' | 'enum' | 'date' | 'json' | 'ref'
  required: boolean
  description: string
  options?: string[]
  unique?: boolean
  ref_model?: string
  ref_display?: string
}

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
] as const

// 自定义分类的兜底样式
const CUSTOM_CAT_STYLE = { emoji: '🏷️', color: 'bg-cyan-500' }

function catStyle(name: string) {
  return DEFAULT_CATEGORIES.find(c => c.name === name) || CUSTOM_CAT_STYLE
}

const ICONS = ['🖥️','🌐','🗄️','🔌','⚖️','📡','🛡️','💾','☸️','📱','⚙️','🔗','📦','🏢','🔧','📊']

function schemaToFields(schema: Record<string, any> = {}): SchemaField[] {
  return Object.entries(schema).map(([name, def]) => ({
    name,
    type: (def?.type || 'string') as SchemaField['type'],
    required: !!def?.required,
    description: def?.description || '',
    options: def?.options,
    unique: !!def?.unique,
    ref_model: def?.ref_model,
    ref_display: def?.ref_display,
  }))
}

function fieldsToSchema(fields: SchemaField[]) {
  return fields.reduce((acc, f) => ({
    ...acc,
    [f.name]: {
      type: f.type, required: f.required, description: f.description,
      ...(f.options ? { options: f.options } : {}),
      ...(f.unique ? { unique: true } : {}),
      ...(f.ref_model ? { ref_model: f.ref_model } : {}),
      ...(f.ref_display ? { ref_display: f.ref_display } : {}),
    },
  }), {} as Record<string, any>)
}

export default function CMDBModelsPage() {
  const [models, setModels] = useState<CIModel[]>([])
  const [categories, setCategories] = useState<Category[]>(DEFAULT_CATEGORIES.map((c, i) => ({ id: c.name, name: c.name, builtin: true })))
  const [ciCounts, setCiCounts] = useState<Record<string, number>>({})
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editModel, setEditModel] = useState<CIModel | null>(null)
  const [categoryFilter, setCategoryFilter] = useState<string>('all')
  const [form, setForm] = useState({
    name: '', description: '', icon: '🖥️', category: '基础设施资源',
    sub_category: '', show_in_menu: true, parent_id: '',
  })
  const [fields, setFields] = useState<SchemaField[]>([])
  const [fieldDialogOpen, setFieldDialogOpen] = useState(false)
  const [editFieldIdx, setEditFieldIdx] = useState<number | null>(null)
  const [fieldForm, setFieldForm] = useState<SchemaField>({ name: '', type: 'string', required: false, description: '' })
  const [catDialogOpen, setCatDialogOpen] = useState(false)
  const [newCatName, setNewCatName] = useState('')

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

  const handleRollback = async (v: { version: number }) => {
    if (!verModel) return
    if (!confirm(`回滚「${verModel.name}」到 v${v.version}？当前 schema 会留痕为下一个版本。`)) return
    try {
      await apiFetch(`/cmdb/model-type/${verModel.id}/rollback/${v.version}`, { method: 'POST' })
      toast.success(`已回滚到 v${v.version}`)
      setVerDialogOpen(false); loadModels()
    } catch (e: any) { toast.error('回滚失败: ' + e.message) }
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
        fields: schemaToFields(m.schema), ciCount: 0,
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
    setDialogOpen(true)
  }

  const openEdit = (model: CIModel) => {
    setEditModel(model)
    setForm({
      name: model.name, description: model.description, icon: model.icon, category: model.category,
      sub_category: model.sub_category || '', show_in_menu: model.show_in_menu, parent_id: model.parent_id || '',
    })
    setFields([...model.fields])
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!form.name.trim()) return
    const payload: any = {
      name: form.name, description: form.description,
      icon: form.icon, category: form.category, sub_category: form.sub_category,
      show_in_menu: form.show_in_menu, parent_id: form.parent_id || null,
      schema: fieldsToSchema(fields),
    }
    try {
      if (editModel) {
        await apiFetch(`/cmdb/model-type/${editModel.id}`, { method: 'PUT', body: JSON.stringify(payload) })
      } else {
        await apiFetch('/cmdb/model-type', { method: 'POST', body: JSON.stringify(payload) })
      }
      toast.success(editModel ? '模型已更新' : `模型「${form.name}」已创建`)
      setDialogOpen(false)
      loadModels()
    } catch (e: any) {
      toast.error(`保存模型失败: ${e.message}`)
    }
  }

  const handleDelete = async (model: CIModel) => {
    if (model.builtin) return
    if (!confirm(`确认删除模型「${model.name}」？`)) return
    try {
      await apiFetch(`/cmdb/model-type/${model.id}`, { method: 'DELETE' })
      toast.success('模型已删除')
      loadModels()
    } catch (e: any) { toast.error(`删除模型失败: ${e.message}`) }
  }

  const handleDeleteField = (idx: number) => setFields(prev => prev.filter((_, i) => i !== idx))

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
    if (!confirm(`确认删除分类「${c.name}」？`)) return
    try {
      await apiFetch(`/cmdb/model-categories/${c.id}`, { method: 'DELETE' })
      toast.success('分类已删除')
      loadCategories()
      if (categoryFilter === c.name) setCategoryFilter('all')
    } catch (e: any) { toast.error(e.message) }
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

  const visible = models.filter(m => categoryFilter === 'all' || m.category === categoryFilter)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CI 模型管理</h1>
          <p className="text-sm text-muted-foreground">定义 CI 类型、分类、继承关系及属性 Schema</p>
        </div>
        <div className="flex gap-2">
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
              <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => handleRollback(v)}>回滚</Button>
            </div>
          ))}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setVerDialogOpen(false)}>关闭</Button></DialogFooter>
      </Dialog>

      {/* 分类过滤（动态，含自定义） */}
      <div className="flex gap-2 flex-wrap items-center">
        <button onClick={() => setCategoryFilter('all')} className={cn("px-3 py-1 rounded-full text-xs border", categoryFilter === 'all' ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600" : "border-gray-200 dark:border-gray-700")}>全部 {models.length}</button>
        {categories.map(c => {
          const style = catStyle(c.name)
          return (
            <button key={c.id} onClick={() => setCategoryFilter(c.name)} className={cn("px-3 py-1 rounded-full text-xs border flex items-center gap-1", categoryFilter === c.name ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600" : "border-gray-200 dark:border-gray-700")}>
              <span>{style.emoji}</span>{c.name}
              {!c.builtin && <span className="text-[9px] text-violet-500">自定义</span>}
            </button>
          )
        })}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {visible.map(model => {
          const style = catStyle(model.category)
          return (
            <Card key={model.id} className="group hover:shadow-md transition-shadow">
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <CardTitle className="text-sm flex items-center gap-2">
                    <span className="text-lg">{model.icon}</span>
                    {model.name}
                  </CardTitle>
                  {canAdmin && (
                    <div className="flex">
                      <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title="字段权限" onClick={() => openFieldRules(model)}><Shield className="h-3.5 w-3.5" /></Button>
                      <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" title="版本历史" onClick={() => openVersions(model)}><History className="h-3.5 w-3.5" /></Button>
                      <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" onClick={() => openEdit(model)}><Edit2 className="h-3.5 w-3.5" /></Button>
                      {!model.builtin && (
                        <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0 text-destructive" onClick={() => handleDelete(model)}><Trash2 className="h-3.5 w-3.5" /></Button>
                      )}
                    </div>
                  )}
                </div>
              </CardHeader>
              <CardContent className="space-y-2">
                {model.description && <p className="text-xs text-muted-foreground">{model.description}</p>}
                <div className="flex items-center gap-2 flex-wrap">
                  <Badge className={cn("text-[10px] text-white", style.color)}>{style.emoji} {model.category}</Badge>
                  {model.sub_category && <Badge variant="outline" className="text-[10px]">{model.sub_category}</Badge>}
                  {model.parent_id && <Badge variant="secondary" className="text-[10px]">继承</Badge>}
                  {model.builtin && <Badge variant="secondary" className="text-[10px]">内置</Badge>}
                </div>
                <div className="flex items-center gap-2">
                  <Badge variant="secondary" className="text-[10px]">{(ciCounts[model.id] || 0)} 个实例</Badge>
                  <Badge variant="outline" className="text-[10px]">{model.fields.length} 个字段</Badge>
                </div>
                {model.fields.length > 0 && (
                  <div className="space-y-0.5 max-h-32 overflow-y-auto">
                    {model.fields.slice(0, 5).map(f => (
                      <div key={f.name} className="flex items-center gap-1.5 text-[11px] py-0.5">
                        <span className="font-medium truncate">{f.name}</span>
                        <Badge variant="outline" className="text-[9px] px-1">{f.type}</Badge>
                        {f.required && <span className="text-destructive text-[10px]">*</span>}
                      </div>
                    ))}
                    {model.fields.length > 5 && <p className="text-[10px] text-muted-foreground">+{model.fields.length - 5} 更多...</p>}
                  </div>
                )}
              </CardContent>
            </Card>
          )
        })}
      </div>

      {visible.length === 0 && (
        <Card className="text-center py-16">
          <CardContent>
            <p className="text-muted-foreground text-sm">
              {models.length === 0 ? '还没有任何模型，点击右上角「新建模型」创建第一个 CI 模型。' : '当前分类下没有模型。'}
            </p>
          </CardContent>
        </Card>
      )}

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

          {/* 字段列表 */}
          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">属性字段 ({fields.length})</label>
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
                    {f.description && <span className="text-muted-foreground truncate flex-1">{f.description}</span>}
                    {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0" onClick={() => openFieldDialog(idx)}><Edit2 className="h-3 w-3" /></Button>}
                    {canAdmin && <Button variant="ghost" size="sm" className="h-5 w-5 p-0 text-destructive" onClick={() => handleDeleteField(idx)}><Trash2 className="h-3 w-3" /></Button>}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.name}>保存</Button></DialogFooter>
      </Dialog>

      {/* 字段编辑对话框 */}
      <Dialog open={fieldDialogOpen} onClose={() => setFieldDialogOpen(false)} title={editFieldIdx !== null ? '编辑字段' : '添加字段'}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">字段名 *</label><Input value={fieldForm.name} onChange={e => setFieldForm(f => ({ ...f, name: e.target.value }))} placeholder="ip_address" /></div>
          <div><label className="text-sm font-medium">类型</label>
            <div className="flex gap-1 mt-1 flex-wrap">
              {(['string', 'number', 'boolean', 'enum', 'date', 'json', 'ref'] as const).map(t => (
                <button key={t} className={cn("px-2 py-1 rounded text-xs border", fieldForm.type === t ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600" : "border-gray-200 dark:border-gray-700")} onClick={() => setFieldForm(f => ({ ...f, type: t }))}>{t}</button>
              ))}
            </div>
          </div>
          {fieldForm.type === 'ref' && (
            <div><label className="text-sm font-medium">引用目标模型 *</label>
              <select value={fieldForm.ref_model || ''} onChange={e => setFieldForm(f => ({ ...f, ref_model: e.target.value }))} className="w-full h-8 text-xs border rounded-md bg-background px-2">
                <option value="">选择目标模型...</option>
                {models.map(m => <option key={m.id} value={m.id}>{m.name}（{m.id}）</option>)}
              </select>
              <p className="text-[10px] text-muted-foreground mt-1">引用型字段的值取自目标模型的 CI 实例，写入时校验存在性</p>
            </div>
          )}
          {fieldForm.type === 'enum' && (
            <div><label className="text-sm font-medium">枚举选项（逗号分隔）</label><Input value={(fieldForm.options || []).join(',')} onChange={e => setFieldForm(f => ({ ...f, options: e.target.value.split(',').map(s => s.trim()).filter(Boolean) }))} placeholder="prod,dev,test" /></div>
          )}
          <div className="flex items-center gap-2"><input type="checkbox" id="field-required" checked={fieldForm.required} onChange={e => setFieldForm(f => ({ ...f, required: e.target.checked }))} className="rounded" /><label htmlFor="field-required" className="text-sm font-medium">必填</label></div>
          <div className="flex items-center gap-2"><input type="checkbox" id="field-unique" checked={!!fieldForm.unique} onChange={e => setFieldForm(f => ({ ...f, unique: e.target.checked }))} className="rounded" /><label htmlFor="field-unique" className="text-sm font-medium">唯一（同类型内该字段值不可重复，写入时拦截）</label></div>
          <div><label className="text-sm font-medium">描述</label><Input value={fieldForm.description} onChange={e => setFieldForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setFieldDialogOpen(false)}>取消</Button><Button onClick={saveField} disabled={!fieldForm.name}>保存</Button></DialogFooter>
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
    </div>
  )
}
