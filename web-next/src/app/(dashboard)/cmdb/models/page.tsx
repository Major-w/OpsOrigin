'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch } from '@/lib/utils'
import { Plus, Trash2, Package, GripVertical, Edit2 } from 'lucide-react'
import { useThemeStore } from '@/stores/theme'
import { useCan } from '@/stores/permissions'
import { cn } from '@/lib/utils'

interface SchemaField {
  name: string
  type: 'string' | 'number' | 'boolean' | 'enum' | 'date' | 'json'
  required: boolean
  description: string
  options?: string[]
}

interface CIModel {
  id: string
  name: string
  description: string
  icon: string
  color: string
  fields: SchemaField[]
  ciCount: number
}

const TYPE_ICONS: Record<string, string> = {
  server: '🖥️', 'web-server': '🌐', database: '🗄️', switch: '🔌',
  loadbalancer: '⚖️', 'dns-server': '📡', firewall: '🛡️', storage: '💾',
  kubernetes: '☸️', application: '📱', service: '⚙️', network: '🔗',
}

const TYPE_COLORS: Record<string, string> = {
  server: 'bg-blue-500', 'web-server': 'bg-cyan-500', database: 'bg-purple-500',
  switch: 'bg-orange-500', loadbalancer: 'bg-yellow-500', 'dns-server': 'bg-green-500',
  firewall: 'bg-red-500', storage: 'bg-pink-500', kubernetes: 'bg-indigo-500',
  application: 'bg-teal-500', service: 'bg-gray-500', network: 'bg-amber-500',
}

export default function CMDBModelsPage() {
  const [models, setModels] = useState<CIModel[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editModel, setEditModel] = useState<CIModel | null>(null)
  const [form, setForm] = useState({ name: '', description: '', icon: '🖥️', color: 'bg-blue-500' })
  const [fields, setFields] = useState<SchemaField[]>([])
  const [fieldDialogOpen, setFieldDialogOpen] = useState(false)
  const [editFieldIdx, setEditFieldIdx] = useState<number | null>(null)
  const [fieldForm, setFieldForm] = useState<SchemaField>({ name: '', type: 'string', required: false, description: '' })
  const { theme } = useThemeStore()
  const canAdmin = useCan('cmdb:admin')

  useEffect(() => {
    apiFetch<any[]>('/cmdb/ci?limit=500').then(items => {
      const typeMap = new Map<string, CIModel>()
      for (const ci of items) {
        if (!typeMap.has(ci.type_id)) {
          typeMap.set(ci.type_id, {
            id: ci.type_id, name: ci.type_id, description: '', icon: TYPE_ICONS[ci.type_id] || '📦',
            color: TYPE_COLORS[ci.type_id] || 'bg-gray-500', fields: [], ciCount: 0
          })
        }
        typeMap.get(ci.type_id)!.ciCount++
        // 从 CI attributes 提取字段定义
        if (ci.attributes && typeof ci.attributes === 'object') {
          for (const [k, v] of Object.entries(ci.attributes)) {
            if (!typeMap.get(ci.type_id)!.fields.find(f => f.name === k)) {
              typeMap.get(ci.type_id)!.fields.push({
                name: k,
                type: typeof v === 'number' ? 'number' : typeof v === 'boolean' ? 'boolean' : 'string',
                required: false, description: ''
              })
            }
          }
        }
      }
      setModels(Array.from(typeMap.values()))
    }).catch(() => setModels([]))
  }, [])

  const openCreate = () => {
    setEditModel(null)
    setForm({ name: '', description: '', icon: '🖥️', color: 'bg-blue-500' })
    setFields([])
    setDialogOpen(true)
  }

  const openEdit = (model: CIModel) => {
    setEditModel(model)
    setForm({ name: model.name, description: model.description, icon: model.icon, color: model.color })
    setFields([...model.fields])
    setDialogOpen(true)
  }

  const handleSave = async () => {
    try {
      if (editModel) {
        setModels(prev => prev.map(m => m.id === editModel.id ? { ...m, ...form, fields } : m))
      } else {
        const newModel: CIModel = {
          id: form.name, ...form, fields, ciCount: 0
        }
        setModels(prev => [...prev, newModel])
        await apiFetch('/cmdb/model-type', { method: 'POST', body: JSON.stringify({ name: form.name, description: form.description, schema: fields.reduce((acc, f) => ({ ...acc, [f.name]: { type: f.type, required: f.required, description: f.description } }), {}) }) })
      }
    } catch (e) { console.error(e) }
    setDialogOpen(false)
  }

  const handleDeleteField = (idx: number) => {
    setFields(prev => prev.filter((_, i) => i !== idx))
  }

  const openFieldDialog = (idx?: number) => {
    if (idx !== undefined) {
      setEditFieldIdx(idx)
      setFieldForm({ ...fields[idx] })
    } else {
      setEditFieldIdx(null)
      setFieldForm({ name: '', type: 'string', required: false, description: '' })
    }
    setFieldDialogOpen(true)
  }

  const saveField = () => {
    if (!fieldForm.name) return
    if (editFieldIdx !== null) {
      setFields(prev => prev.map((f, i) => i === editFieldIdx ? { ...fieldForm } : f))
    } else {
      setFields(prev => [...prev, { ...fieldForm }])
    }
    setFieldDialogOpen(false)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CI 模型管理</h1>
          <p className="text-sm text-muted-foreground">定义 CI 类型及其属性字段 Schema</p>
        </div>
        {canAdmin && <Button onClick={openCreate}><Plus className="h-4 w-4" />新建模型</Button>}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
        {models.map(model => (
          <Card key={model.id} className="group hover:shadow-md transition-shadow">
            <CardHeader className="py-3">
              <div className="flex items-center justify-between">
                <CardTitle className="text-sm flex items-center gap-2">
                  <span className="text-lg">{model.icon}</span>
                  {model.name}
                </CardTitle>
                {canAdmin && (
                  <Button variant="ghost" size="sm" className="opacity-0 group-hover:opacity-100 h-7 w-7 p-0" onClick={() => openEdit(model)}>
                    <Edit2 className="h-3.5 w-3.5" />
                  </Button>
                )}
              </div>
            </CardHeader>
            <CardContent className="space-y-2">
              {model.description && <p className="text-xs text-muted-foreground">{model.description}</p>}
              <div className="flex items-center gap-2">
                <Badge variant="secondary" className="text-[10px]">{model.ciCount} 个实例</Badge>
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
        ))}
      </div>

      {/* 模型编辑对话框 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editModel ? '编辑模型' : '新建模型'}>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="server" disabled={!!editModel} /></div>
            <div><label className="text-sm font-medium">图标</label>
              <div className="flex gap-1 flex-wrap mt-1">
                {['🖥️','🌐','🗄️','🔌','⚖️','📡','🛡️','💾','☸️','📱','⚙️','🔗'].map(icon => (
                  <button key={icon} className={cn("w-8 h-8 rounded border flex items-center justify-center text-sm", form.icon === icon ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10" : "border-gray-200 dark:border-gray-700")} onClick={() => setForm(f => ({ ...f, icon }))}>{icon}</button>
                ))}
              </div>
            </div>
          </div>
          <div><label className="text-sm font-medium">描述</label><Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>

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
            <div className="flex gap-1 mt-1">
              {(['string', 'number', 'boolean', 'enum', 'date', 'json'] as const).map(t => (
                <button key={t} className={cn("px-2 py-1 rounded text-xs border", fieldForm.type === t ? "border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600" : "border-gray-200 dark:border-gray-700")} onClick={() => setFieldForm(f => ({ ...f, type: t }))}>{t}</button>
              ))}
            </div>
          </div>
          <div className="flex items-center gap-2"><input type="checkbox" id="field-required" checked={fieldForm.required} onChange={e => setFieldForm(f => ({ ...f, required: e.target.checked }))} className="rounded" /><label htmlFor="field-required" className="text-sm font-medium">必填</label></div>
          <div><label className="text-sm font-medium">描述</label><Input value={fieldForm.description} onChange={e => setFieldForm(f => ({ ...f, description: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setFieldDialogOpen(false)}>取消</Button><Button onClick={saveField} disabled={!fieldForm.name}>保存</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
