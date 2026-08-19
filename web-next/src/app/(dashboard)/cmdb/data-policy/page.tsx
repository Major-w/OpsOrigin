'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Plus, Trash2, ShieldCheck } from 'lucide-react'

interface PolicyCondition { attr: string; op: string; value: string }
interface DataPolicy { id: string; role: string; model_type_id: string; condition: PolicyCondition[]; action: string; created_at: string }
interface ModelInfo { id: string; name: string }

export default function CMDBDataPolicyPage() {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [policies, setPolicies] = useState<DataPolicy[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<{ role: string; model_type_id: string; action: string; condition: PolicyCondition[] }>({
    role: '', model_type_id: '', action: 'query', condition: [{ attr: '', op: 'eq', value: '' }],
  })

  const load = async () => {
    try { setPolicies(await apiList<DataPolicy>('/cmdb/data-policies')) } catch { setPolicies([]) }
  }
  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000').then(setModels).catch(() => setModels([]))
    load()
  }, [])

  const save = async () => {
    if (!form.role.trim() || !form.model_type_id) { toast.error('角色与模型必填'); return }
    const condition = form.condition.filter(c => c.attr.trim())
    try {
      await apiFetch('/cmdb/data-policies', { method: 'POST', body: JSON.stringify({ ...form, condition }) })
      toast.success('数据权限已保存')
      setDialogOpen(false); load()
    } catch (e: any) { toast.error('保存失败: ' + e.message) }
  }

  const [delTarget, setDelTarget] = useState<DataPolicy | null>(null)
  const [delSubmitting, setDelSubmitting] = useState(false)

  const confirmDel = async () => {
    if (!delTarget) return
    setDelSubmitting(true)
    try {
      await apiFetch(`/cmdb/data-policies/${delTarget.id}`, { method: 'DELETE' }); toast.success('已删除'); load(); setDelTarget(null)
    } catch (e: any) { toast.error('删除失败: ' + e.message) } finally { setDelSubmitting(false) }
  }

  const condText = (c: PolicyCondition[]) => c.map(x => `${x.attr} ${x.op === 'in' ? '∈' : '='} ${x.value}`).join(' 且 ') || '全部数据'

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><ShieldCheck className="h-6 w-6" />数据权限</h1>
          <p className="text-sm text-muted-foreground">行级数据权限：角色对模型类型仅可见/可操作满足属性条件的 CI（与租户隔离、字段规则平行）</p>
        </div>
        <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建规则</Button>
      </div>

      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">规则列表（{policies.length}）</CardTitle></CardHeader>
        <CardContent className="p-0">
          {policies.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无数据权限规则。默认所有角色可见全部数据（仅租户隔离）。</p> : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 font-medium px-4 py-2">角色</th>
                  <th className="pb-2 font-medium px-4 py-2">模型</th>
                  <th className="pb-2 font-medium px-4 py-2">动作</th>
                  <th className="pb-2 font-medium px-4 py-2">条件</th>
                  <th className="pb-2 font-medium px-4 py-2">操作</th>
                </tr></thead>
                <tbody>
                  {policies.map(p => (
                    <tr key={p.id} className="border-b last:border-0">
                      <td className="px-4 py-2"><Badge variant="secondary" className="text-xs">{p.role}</Badge></td>
                      <td className="px-4 py-2 text-xs">{p.model_type_id}</td>
                      <td className="px-4 py-2"><Badge variant="outline" className="text-[10px]">{p.action}</Badge></td>
                      <td className="px-4 py-2 text-xs">{condText(p.condition)}</td>
                      <td className="px-4 py-2"><Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setDelTarget(p)}><Trash2 className="h-3 w-3 text-destructive" /></Button></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建数据权限规则">
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">角色 *</label><Input value={form.role} onChange={e => setForm(f => ({ ...f, role: e.target.value }))} placeholder="operator / viewer" /></div>
            <div><label className="text-sm font-medium">模型 *</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.model_type_id} onChange={e => setForm(f => ({ ...f, model_type_id: e.target.value }))}>
                <option value="">选择模型...</option>
                {models.map(m => <option key={m.id} value={m.id}>{m.name}（{m.id}）</option>)}
              </select>
            </div>
          </div>
          <div><label className="text-sm font-medium">动作</label>
            <div className="flex gap-1 mt-1">
              {['query', 'update', 'delete'].map(a => (
                <button key={a} className={`px-3 py-1 rounded text-xs border ${form.action === a ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-500/10 text-emerald-600' : 'border-gray-200 dark:border-gray-700'}`} onClick={() => setForm(f => ({ ...f, action: a }))}>{a}</button>
              ))}
            </div>
          </div>
          <div><label className="text-sm font-medium">条件（AND，满足才可见/可操作）</label>
            <div className="space-y-1.5 mt-1">
              {form.condition.map((c, i) => (
                <div key={i} className="flex gap-1.5 items-center">
                  <Input className="flex-1 h-8 text-xs" value={c.attr} onChange={e => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, attr: e.target.value } : x) }))} placeholder="属性名，如 env" />
                  <select className="h-8 rounded-md border border-input bg-background px-1 text-xs" value={c.op} onChange={e => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, op: e.target.value } : x) }))}>
                    <option value="eq">=</option>
                    <option value="in">∈</option>
                  </select>
                  <Input className="flex-1 h-8 text-xs" value={c.value} onChange={e => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, value: e.target.value } : x) }))} placeholder="值，in 用逗号分隔" />
                  <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0" onClick={() => setForm(f => ({ ...f, condition: f.condition.filter((_, j) => j !== i) }))}><Trash2 className="h-3 w-3" /></Button>
                </div>
              ))}
              <Button variant="outline" size="sm" onClick={() => setForm(f => ({ ...f, condition: [...f.condition, { attr: '', op: 'eq', value: '' }] }))}><Plus className="h-3 w-3 mr-1" />添加条件</Button>
            </div>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={save}>保存</Button></DialogFooter>
      </Dialog>

      <ConfirmDialog
        open={!!delTarget}
        onClose={() => setDelTarget(null)}
        onConfirm={confirmDel}
        title="确认删除数据权限"
        description={delTarget ? `删除角色 ${delTarget.role} 对 ${delTarget.model_type_id} 的数据权限？` : ''}
        confirmLabel="删除"
        loading={delSubmitting}
      />
    </div>
  )
}
