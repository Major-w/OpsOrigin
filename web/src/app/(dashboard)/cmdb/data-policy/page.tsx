
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Select, Popconfirm, message } from 'antd'
import { apiFetch, apiList } from '@/lib/utils'
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
    if (!form.role.trim() || !form.model_type_id) { message.error('角色与模型必填'); return }
    const condition = form.condition.filter(c => c.attr.trim())
    try {
      await apiFetch('/cmdb/data-policies', { method: 'POST', body: JSON.stringify({ ...form, condition }) })
      message.success('数据权限已保存')
      setDialogOpen(false); load()
    } catch (e: any) { message.error('保存失败: ' + e.message) }
  }

  const [delTarget, setDelTarget] = useState<DataPolicy | null>(null)
  const [delSubmitting, setDelSubmitting] = useState(false)

  const confirmDel = async () => {
    if (!delTarget) return
    setDelSubmitting(true)
    try {
      await apiFetch(`/cmdb/data-policies/${delTarget.id}`, { method: 'DELETE' }); message.success('已删除'); load(); setDelTarget(null)
    } catch (e: any) { message.error('删除失败: ' + e.message) } finally { setDelSubmitting(false) }
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
                      <td className="px-4 py-2"><Tag>{p.role}</Tag></td>
                      <td className="px-4 py-2 text-xs">{p.model_type_id}</td>
                      <td className="px-4 py-2"><Tag className="text-[10px]">{p.action}</Tag></td>
                      <td className="px-4 py-2 text-xs">{condText(p.condition)}</td>
                      <td className="px-4 py-2"><Button type="text" icon={<Trash2 className="h-3 w-3 text-red-500" />} onClick={() => setDelTarget(p)} /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 新建数据权限规则 */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title="新建数据权限规则" footer={null}>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">角色 *</label><Input value={form.role} onChange={e => setForm(f => ({ ...f, role: e.target.value }))} placeholder="operator / viewer" /></div>
            <div><label className="text-sm font-medium">模型 *</label>
              <Select value={form.model_type_id || undefined} onChange={v => setForm(f => ({ ...f, model_type_id: v || '' }))} className="w-full mt-1" options={[{ value: '', label: '选择模型...' }, ...models.map(m => ({ value: m.id, label: `${m.name}（${m.id}）` }))]} />
            </div>
          </div>
          <div><label className="text-sm font-medium">动作</label>
            <div className="flex gap-1 mt-1">
              {['query', 'update', 'delete'].map(a => (
                <button key={a} className={`px-3 py-1 rounded text-xs border ${form.action === a ? 'border-blue-500 bg-blue-50 dark:bg-blue-500/10 text-blue-600' : 'border-gray-200 dark:border-gray-700'}`} onClick={() => setForm(f => ({ ...f, action: a }))}>{a}</button>
              ))}
            </div>
          </div>
          <div><label className="text-sm font-medium">条件（AND，满足才可见/可操作）</label>
            <div className="space-y-1.5 mt-1">
              {form.condition.map((c, i) => (
                <div key={i} className="flex gap-1.5 items-center">
                  <Input className="flex-1 h-8 text-xs" value={c.attr} onChange={e => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, attr: e.target.value } : x) }))} placeholder="属性名，如 env" />
                  <Select className="w-16 h-8" value={c.op} onChange={v => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, op: v } : x) }))} options={[{ value: 'eq', label: '=' }, { value: 'in', label: '∈' }]} />
                  <Input className="flex-1 h-8 text-xs" value={c.value} onChange={e => setForm(f => ({ ...f, condition: f.condition.map((x, j) => j === i ? { ...x, value: e.target.value } : x) }))} placeholder="值，in 用逗号分隔" />
                  <Button type="text" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" icon={<Trash2 className="h-3 w-3" />} onClick={() => setForm(f => ({ ...f, condition: f.condition.filter((_, j) => j !== i) }))} />
                </div>
              ))}
              <Button type="default" size="middle" onClick={() => setForm(f => ({ ...f, condition: [...f.condition, { attr: '', op: 'eq', value: '' }] }))}><Plus className="h-3 w-3 mr-1" />添加条件</Button>
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button type="default" onClick={() => setDialogOpen(false)}>取消</Button><Button type="primary" onClick={save}>保存</Button></div>
      </Modal>

      <Popconfirm
        title="确认删除数据权限"
        description={delTarget ? `删除角色 ${delTarget.role} 对 ${delTarget.model_type_id} 的数据权限？` : ''}
        onConfirm={confirmDel}
        open={!!delTarget}
        onCancel={() => setDelTarget(null)}
        okButtonProps={{ loading: delSubmitting }}
      />
    </div>
  )
}
