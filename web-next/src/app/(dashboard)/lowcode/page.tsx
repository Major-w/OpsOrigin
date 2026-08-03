'use client'

import { useEffect, useState, useCallback } from 'react'
import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { apiFetch, apiList } from '@/lib/utils'
import { Database, Layout, Table2, Plus, Pencil, Trash2, Box, ArrowRight } from 'lucide-react'

interface Model { id: string; name: string; description: string; fields?: any[] }

interface Instance { id: string; model_id: string; data: Record<string, any>; created_at?: string }

export default function LowcodeIndexPage() {
  const router = useRouter()
  const [models, setModels] = useState<Model[]>([])
  const [instances, setInstances] = useState<Instance[]>([])
  const [selectedModelId, setSelectedModelId] = useState('')
  const [dialog, setDialog] = useState<'create' | 'edit' | null>(null)
  const [editInstance, setEditInstance] = useState<Instance | null>(null)
  const [formValues, setFormValues] = useState<Record<string, string>>({})

  const fetchModels = useCallback(() => {
    apiList<Model>('/lowcode/models').then(setModels).catch(() => setModels([]))
  }, [])

  const fetchInstances = useCallback(() => {
    apiList<Instance>('/lowcode/instances').then(setInstances).catch(() => setInstances([]))
  }, [])

  useEffect(() => { fetchModels(); fetchInstances() }, [fetchModels, fetchInstances])

  const selectedModel = models.find(m => m.id === selectedModelId)
  const filteredInstances = instances.filter(i => i.model_id === selectedModelId)

  const openCreate = () => {
    setEditInstance(null)
    setFormValues({})
    setDialog('create')
  }

  const openEdit = (inst: Instance) => {
    setEditInstance(inst)
    setFormValues({ ...inst.data })
    setDialog('edit')
  }

  const handleSave = async () => {
    if (!selectedModelId) return
    try {
      if (dialog === 'create') {
        await apiFetch('/lowcode/instances', { method: 'POST', body: JSON.stringify({ model_id: selectedModelId, data: formValues }) })
      } else if (editInstance) {
        await apiFetch(`/lowcode/instances/${editInstance.id}`, { method: 'PUT', body: JSON.stringify({ data: formValues }) })
      }
      fetchInstances()
    } catch {}
    setDialog(null)
  }

  const handleDelete = async (id: string) => {
    if (!confirm('确定删除该实例?')) return
    try { await apiFetch(`/lowcode/instances/${id}`, { method: 'DELETE' }); fetchInstances() } catch {}
  }

  const fields = selectedModel?.fields || []

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">低代码编排平台</h1>
          <p className="text-sm text-muted-foreground">模型定义、页面搭建与数据管理</p>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Card className="group cursor-pointer hover:border-[rgba(255,255,255,0.15)] transition-colors" onClick={() => router.push('/lowcode/models')}>
          <CardContent className="flex items-center gap-3 py-4">
            <div className="p-2 rounded-lg bg-emerald-500/10"><Database className="h-5 w-5 text-emerald-400" /></div>
            <div className="flex-1"><p className="font-medium text-sm">数据模型</p><p className="text-xs text-muted-foreground">定义数据结构与字段</p></div>
            <ArrowRight className="h-4 w-4 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
          </CardContent>
        </Card>
        <Card className="group cursor-pointer hover:border-[rgba(255,255,255,0.15)] transition-colors" onClick={() => router.push('/lowcode/pages')}>
          <CardContent className="flex items-center gap-3 py-4">
            <div className="p-2 rounded-lg bg-blue-500/10"><Layout className="h-5 w-5 text-blue-400" /></div>
            <div className="flex-1"><p className="font-medium text-sm">页面搭建</p><p className="text-xs text-muted-foreground">可视化构建表单与列表</p></div>
            <ArrowRight className="h-4 w-4 text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity" />
          </CardContent>
        </Card>
        <Card className="group cursor-pointer hover:border-emerald-500/30 transition-colors border-emerald-500/20">
          <CardContent className="flex items-center gap-3 py-4">
            <div className="p-2 rounded-lg bg-violet-500/10"><Table2 className="h-5 w-5 text-violet-400" /></div>
            <div className="flex-1"><p className="font-medium text-sm">数据管理</p><p className="text-xs text-muted-foreground">管理模型实例数据</p></div>
            <Badge variant="info" className="text-xs">当前</Badge>
          </CardContent>
        </Card>
      </div>

      <Tabs defaultValue="data">
        <TabsList>
          <TabsTrigger value="data" onClick={() => router.push('/lowcode/models')}><Database className="w-3 h-3 mr-1" />数据模型</TabsTrigger>
          <TabsTrigger value="pages" onClick={() => router.push('/lowcode/pages')}><Layout className="w-3 h-3 mr-1" />页面搭建</TabsTrigger>
          <TabsTrigger value="instances"><Table2 className="w-3 h-3 mr-1" />数据管理</TabsTrigger>
        </TabsList>

        <TabsContent value="instances" className="mt-4 space-y-4">
          <div className="flex items-center gap-3">
            <label className="text-sm font-medium whitespace-nowrap">选择模型</label>
            <select
              className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm min-w-[200px]"
              value={selectedModelId}
              onChange={e => setSelectedModelId(e.target.value)}
            >
              <option value="">-- 请选择模型 --</option>
              {models.map(m => (
                <option key={m.id} value={m.id}>{m.name}{m.description ? ` (${m.description})` : ''}</option>
              ))}
            </select>
            {selectedModelId && (
              <>
                <Badge variant="secondary">{filteredInstances.length} 条数据</Badge>
                <div className="flex-1" />
                <Button size="sm" onClick={openCreate}><Plus className="h-3 w-3 mr-1" />新增数据</Button>
              </>
            )}
          </div>

          {selectedModelId ? (
            <Card>
              <CardContent className="p-0">
                {fields.length === 0 ? (
                  <p className="px-4 py-8 text-sm text-muted-foreground text-center">该模型暂无字段定义，请先在数据模型中添加字段。</p>
                ) : filteredInstances.length === 0 ? (
                  <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无数据，点击上方按钮新增。</p>
                ) : (
                  <div className="overflow-auto max-h-[60vh]">
                    <table className="w-full text-sm">
                      <thead>
                        <tr className="border-b text-left text-muted-foreground sticky top-0 bg-background">
                          {fields.map((f: any) => (
                            <th key={f.name} className="pb-2 px-4 font-medium whitespace-nowrap">
                              {f.label || f.name}
                              {f.required && <span className="text-destructive ml-0.5">*</span>}
                            </th>
                          ))}
                          <th className="pb-2 px-4 font-medium w-20"></th>
                        </tr>
                      </thead>
                      <tbody>
                        {filteredInstances.map(inst => (
                          <tr key={inst.id} className="border-b last:border-0 hover:bg-[rgba(255,255,255,0.02)]">
                            {fields.map((f: any) => (
                              <td key={f.name} className="py-2 px-4 text-muted-foreground max-w-[200px] truncate">
                                {inst.data?.[f.name] ?? <span className="text-xs italic">空</span>}
                              </td>
                            ))}
                            <td className="py-2 px-4">
                              <div className="flex gap-1">
                                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => openEdit(inst)}><Pencil className="h-3 w-3" /></Button>
                                <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => handleDelete(inst.id)}><Trash2 className="h-3 w-3 text-red-400" /></Button>
                              </div>
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </CardContent>
            </Card>
          ) : (
            <Card>
              <CardContent className="flex flex-col items-center justify-center py-16 text-muted-foreground">
                <Box className="h-12 w-12 mb-3 opacity-30" />
                <p className="text-sm">请先选择一个模型来管理数据</p>
              </CardContent>
            </Card>
          )}
        </TabsContent>
      </Tabs>

      <Dialog open={!!dialog} onClose={() => setDialog(null)} title={dialog === 'create' ? '新增数据' : '编辑数据'}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto pr-1">
          {fields.map((f: any) => (
            <div key={f.name}>
              <label className="text-sm font-medium">
                {f.label || f.name}
                {f.required && <span className="text-destructive ml-0.5">*</span>}
              </label>
              {f.type === 'boolean' ? (
                <select
                  className="mt-1 flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                  value={formValues[f.name] ?? ''}
                  onChange={e => setFormValues(v => ({ ...v, [f.name]: e.target.value }))}
                >
                  <option value="">--</option>
                  <option value="true">是</option>
                  <option value="false">否</option>
                </select>
              ) : f.options?.length ? (
                <select
                  className="mt-1 flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
                  value={formValues[f.name] ?? ''}
                  onChange={e => setFormValues(v => ({ ...v, [f.name]: e.target.value }))}
                >
                  <option value="">--</option>
                  {f.options.map((opt: string) => <option key={opt} value={opt}>{opt}</option>)}
                </select>
              ) : (
                <Input
                  className="mt-1"
                  placeholder={f.placeholder || ''}
                  value={formValues[f.name] ?? ''}
                  onChange={e => setFormValues(v => ({ ...v, [f.name]: e.target.value }))}
                />
              )}
            </div>
          ))}
          {fields.length === 0 && (
            <p className="text-sm text-muted-foreground text-center py-4">该模型暂无字段，请先定义字段。</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialog(null)}>取消</Button>
          <Button onClick={handleSave} disabled={fields.length === 0}>{dialog === 'create' ? '创建' : '保存'}</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
