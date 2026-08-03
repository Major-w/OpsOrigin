'use client'

import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { validateName, sanitizeName, NAME_RULE_TEXT } from '@/lib/validate'
import {
  Plus, Trash2, Database, ChevronDown, ChevronRight,
  Pencil, Search, X, Hash, CheckSquare, Calendar, Mail,
  Phone, Link, FileText, Code, Layers, List,
} from 'lucide-react'

const FIELD_TYPES = [
  { value: 'text', label: 'Text' }, { value: 'number', label: 'Number' },
  { value: 'boolean', label: 'Boolean' }, { value: 'date', label: 'Date' },
  { value: 'datetime', label: 'DateTime' }, { value: 'select', label: 'Select' },
  { value: 'multi_select', label: 'Multi Select' }, { value: 'email', label: 'Email' },
  { value: 'phone', label: 'Phone' }, { value: 'url', label: 'URL' },
  { value: 'file', label: 'File' }, { value: 'rich_text', label: 'Rich Text' },
  { value: 'json', label: 'JSON' }, { value: 'relation', label: 'Relation' },
]

const TYPE_ICONS: Record<string, any> = {
  text: Hash, number: Hash, boolean: CheckSquare, date: Calendar,
  datetime: Calendar, select: List, multi_select: List, email: Mail,
  phone: Phone, url: Link, file: FileText, rich_text: FileText,
  json: Code, relation: Layers,
}

interface Field {
  name: string; label: string; type: string; required: boolean; unique: boolean
  default_value: string; placeholder: string; description: string; options: string[]
}

interface Model {
  id: string; name: string; description: string; table_name: string; fields: Field[]
}

const emptyField: Field = { name: '', label: '', type: 'text', required: false, unique: false, default_value: '', placeholder: '', description: '', options: [] }

export default function LowcodeModelsPage() {
  const [models, setModels] = useState<Model[]>([])
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [search, setSearch] = useState('')
  const [modelDialog, setModelDialog] = useState<'create' | 'edit' | null>(null)
  const [editModel, setEditModel] = useState<Model | null>(null)
  const [modelForm, setModelForm] = useState({ name: '', description: '', table_name: '' })
  const [nameError, setNameError] = useState<string | null>(null)
  const [fieldDialog, setFieldDialog] = useState<'create' | 'edit' | null>(null)
  const [fieldModelId, setFieldModelId] = useState<string>('')
  const [editFieldIdx, setEditFieldIdx] = useState<number>(-1)
  const [fieldForm, setFieldForm] = useState<Field>({ ...emptyField })
  const [optionsInput, setOptionsInput] = useState('')

  const fetchModels = useCallback(() => {
    apiList<Model>('/lowcode/models').then(setModels).catch(() => setModels([]))
  }, [])

  useEffect(() => { fetchModels() }, [fetchModels])

  const toggleExpand = (id: string) => {
    setExpanded(prev => { const s = new Set(prev); s.has(id) ? s.delete(id) : s.add(id); return s })
  }

  const filtered = models.filter(m =>
    m.name.toLowerCase().includes(search.toLowerCase()) ||
    m.description?.toLowerCase().includes(search.toLowerCase()) ||
    m.table_name?.toLowerCase().includes(search.toLowerCase())
  )

  const openCreateModel = () => { setModelForm({ name: '', description: '', table_name: '' }); setModelDialog('create') }
  const openEditModel = (m: Model) => { setEditModel(m); setModelForm({ name: m.name, description: m.description, table_name: m.table_name }); setModelDialog('edit') }

  const handleSaveModel = async () => {
    const err = validateName(modelForm.name, '模型名称')
    if (err) { alert(err); return }
    try {
      if (modelDialog === 'create') {
        await apiFetch('/lowcode/models', { method: 'POST', body: JSON.stringify(modelForm) })
      } else if (editModel) {
        await apiFetch(`/lowcode/models/${editModel.id}`, { method: 'PUT', body: JSON.stringify(modelForm) })
      }
      fetchModels()
    } catch {}
    setModelDialog(null)
  }

  const handleDeleteModel = async (id: string) => {
    if (!confirm('确定删除该模型?')) return
    try { await apiFetch(`/lowcode/models/${id}`, { method: 'DELETE' }); fetchModels() } catch {}
  }

  const openCreateField = (modelId: string) => {
    setFieldModelId(modelId); setEditFieldIdx(-1); setFieldForm({ ...emptyField }); setOptionsInput(''); setFieldDialog('create')
  }
  const openEditField = (modelId: string, idx: number, f: Field) => {
    setFieldModelId(modelId); setEditFieldIdx(idx); setFieldForm({ ...f }); setOptionsInput(f.options?.join('\n') || ''); setFieldDialog('edit')
  }

  const handleSaveField = async () => {
    if (!fieldForm.name || !fieldModelId) return
    const model = models.find(m => m.id === fieldModelId)
    if (!model) return
    const options = (fieldForm.type === 'select' || fieldForm.type === 'multi_select')
      ? optionsInput.split('\n').map(s => s.trim()).filter(Boolean) : []
    const newField = { ...fieldForm, options }
    const fields = [...(model.fields || [])]
    if (editFieldIdx >= 0) fields[editFieldIdx] = newField; else fields.push(newField)
    try {
      await apiFetch(`/lowcode/models/${fieldModelId}`, { method: 'PUT', body: JSON.stringify({ ...model, fields }) })
      fetchModels()
    } catch {}
    setFieldDialog(null)
  }

  const handleDeleteField = async (modelId: string, idx: number) => {
    const model = models.find(m => m.id === modelId)
    if (!model) return
    const fields = model.fields.filter((_, i) => i !== idx)
    try {
      await apiFetch(`/lowcode/models/${modelId}`, { method: 'PUT', body: JSON.stringify({ ...model, fields }) })
      fetchModels()
    } catch {}
  }

  const showOptions = fieldForm.type === 'select' || fieldForm.type === 'multi_select'

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">数据模型</h1>
          <p className="text-sm text-muted-foreground">管理低代码平台的数据模型与字段定义</p>
        </div>
        <Button onClick={openCreateModel}><Plus className="h-4 w-4" />新建模型</Button>
      </div>

      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
        <Input className="pl-9" placeholder="搜索模型名称、描述、表名..." value={search} onChange={e => setSearch(e.target.value)} />
      </div>

      <div className="space-y-3">
        {filtered.map(m => (
          <Card key={m.id}>
            <CardHeader className="py-3 cursor-pointer select-none" onClick={() => toggleExpand(m.id)}>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  {expanded.has(m.id) ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                  <Database className="h-4 w-4 text-emerald-400" />
                  <span className="font-medium text-sm">{m.name}</span>
                  {m.table_name && <Badge variant="secondary">{m.table_name}</Badge>}
                  <Badge variant="info">{m.fields?.length || 0} 字段</Badge>
                </div>
                <div className="flex items-center gap-1" onClick={e => e.stopPropagation()}>
                  <Button variant="ghost" size="sm" onClick={() => openEditModel(m)}><Pencil className="h-3.5 w-3.5" /></Button>
                  <Button variant="ghost" size="sm" onClick={() => handleDeleteModel(m.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                </div>
              </div>
              {m.description && <p className="text-xs text-muted-foreground mt-1 ml-6">{m.description}</p>}
            </CardHeader>
            {expanded.has(m.id) && (
              <CardContent>
                <div className="flex items-center justify-between mb-2">
                  <span className="text-xs font-medium text-muted-foreground">字段列表</span>
                  <Button variant="outline" size="sm" onClick={() => openCreateField(m.id)}><Plus className="h-3 w-3" />添加字段</Button>
                </div>
                {m.fields?.length ? (
                  <div className="rounded-lg border border-[rgba(255,255,255,0.06)] divide-y divide-[rgba(255,255,255,0.06)]">
                    {m.fields.map((f, i) => {
                      const Icon = TYPE_ICONS[f.type] || Hash
                      return (
                        <div key={i} className="flex items-center justify-between px-3 py-2 text-xs">
                          <div className="flex items-center gap-2 min-w-0">
                            <Icon className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                            <span className="font-medium">{f.name}</span>
                            <span className="text-muted-foreground">{f.label}</span>
                            <Badge variant="outline">{f.type}</Badge>
                            {f.required && <Badge variant="destructive">必填</Badge>}
                            {f.unique && <Badge variant="warning">唯一</Badge>}
                          </div>
                          <div className="flex items-center gap-1 shrink-0">
                            <Button variant="ghost" size="sm" onClick={() => openEditField(m.id, i, f)}><Pencil className="h-3 w-3" /></Button>
                            <Button variant="ghost" size="sm" onClick={() => handleDeleteField(m.id, i)}><Trash2 className="h-3 w-3 text-red-400" /></Button>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                ) : (
                  <p className="text-xs text-muted-foreground">暂无字段定义</p>
                )}
              </CardContent>
            )}
          </Card>
        ))}
        {filtered.length === 0 && (
          <div className="text-center py-12 text-muted-foreground text-sm">{search ? '没有匹配的模型' : '暂无数据模型，点击上方按钮创建'}</div>
        )}
      </div>

      <Dialog open={!!modelDialog} onClose={() => setModelDialog(null)} title={modelDialog === 'create' ? '新建模型' : '编辑模型'}>
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">名称 *</label>
            <Input className="mt-1" value={modelForm.name} onChange={e => { const v = sanitizeName(e.target.value); setModelForm(f => ({ ...f, name: v })); setNameError(validateName(v, '模型名称')) }} placeholder="user 或 user_model" />
            {nameError ? <p className="text-[11px] text-destructive mt-1">{nameError}</p> : <p className="text-[11px] text-muted-foreground mt-1">{NAME_RULE_TEXT}</p>}
          </div>
          <div>
            <label className="text-sm font-medium">表名</label>
            <Input className="mt-1" value={modelForm.table_name} onChange={e => setModelForm(f => ({ ...f, table_name: e.target.value }))} placeholder="t_user (可选)" />
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input className="mt-1" value={modelForm.description} onChange={e => setModelForm(f => ({ ...f, description: e.target.value }))} placeholder="用户模型" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setModelDialog(null)}>取消</Button>
          <Button onClick={handleSaveModel} disabled={!modelForm.name}>{modelDialog === 'create' ? '创建' : '保存'}</Button>
        </DialogFooter>
      </Dialog>

      <Dialog open={!!fieldDialog} onClose={() => setFieldDialog(null)} title={fieldDialog === 'create' ? '添加字段' : '编辑字段'}>
        <div className="space-y-3 max-h-[70vh] overflow-y-auto pr-1">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">字段名 *</label>
              <Input className="mt-1" value={fieldForm.name} onChange={e => setFieldForm(f => ({ ...f, name: sanitizeName(e.target.value) }))} placeholder="username" />
            </div>
            <div>
              <label className="text-sm font-medium">显示名</label>
              <Input className="mt-1" value={fieldForm.label} onChange={e => setFieldForm(f => ({ ...f, label: e.target.value }))} placeholder="用户名" />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">类型</label>
            <Select className="mt-1" options={FIELD_TYPES} value={fieldForm.type} onChange={e => setFieldForm(f => ({ ...f, type: e.target.value }))} />
          </div>
          {showOptions && (
            <div>
              <label className="text-sm font-medium">选项 (每行一个)</label>
              <textarea
                className="mt-1 flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm min-h-[80px]"
                value={optionsInput}
                onChange={e => setOptionsInput(e.target.value)}
                placeholder={"选项A\n选项B\n选项C"}
              />
            </div>
          )}
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">默认值</label>
              <Input className="mt-1" value={fieldForm.default_value} onChange={e => setFieldForm(f => ({ ...f, default_value: e.target.value }))} />
            </div>
            <div>
              <label className="text-sm font-medium">占位符</label>
              <Input className="mt-1" value={fieldForm.placeholder} onChange={e => setFieldForm(f => ({ ...f, placeholder: e.target.value }))} />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input className="mt-1" value={fieldForm.description} onChange={e => setFieldForm(f => ({ ...f, description: e.target.value }))} />
          </div>
          <div className="flex items-center gap-4">
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" checked={fieldForm.required} onChange={e => setFieldForm(f => ({ ...f, required: e.target.checked }))} className="rounded" />必填
            </label>
            <label className="flex items-center gap-2 text-sm cursor-pointer">
              <input type="checkbox" checked={fieldForm.unique} onChange={e => setFieldForm(f => ({ ...f, unique: e.target.checked }))} className="rounded" />唯一
            </label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setFieldDialog(null)}>取消</Button>
          <Button onClick={handleSaveField} disabled={!fieldForm.name}>{fieldDialog === 'create' ? '添加' : '保存'}</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
