'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2, GitBranch, ArrowRight, Link } from 'lucide-react'
import { useThemeStore } from '@/stores/theme'
import { useCan } from '@/stores/permissions'
import { cn } from '@/lib/utils'

interface CIRelation {
  id: string
  source_id: string
  source_name: string
  target_id: string
  target_name: string
  relation_type: string
  properties: Record<string, any>
  created_at: string
}

const RELATION_TYPES = [
  { value: 'depends_on', label: '依赖于', color: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400' },
  { value: 'connects_to', label: '连接到', color: 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-400' },
  { value: 'contains', label: '包含', color: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400' },
  { value: 'backup_of', label: '备份', color: 'bg-purple-100 text-purple-700 dark:bg-purple-500/20 dark:text-purple-400' },
  { value: 'deployed_on', label: '部署于', color: 'bg-orange-100 text-orange-700 dark:bg-orange-500/20 dark:text-orange-400' },
  { value: 'calls', label: '调用', color: 'bg-cyan-100 text-cyan-700 dark:bg-cyan-500/20 dark:text-cyan-400' },
  { value: 'belongs_to', label: '隶属于', color: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-500/20 dark:text-yellow-400' },
  { value: 'part_of', label: '组成部分', color: 'bg-pink-100 text-pink-700 dark:bg-pink-500/20 dark:text-pink-400' },
]

export default function CMDBRelationsPage() {
  const [relations, setRelations] = useState<CIRelation[]>([])
  const [ciList, setCiList] = useState<{ id: string; name: string; type_id: string }[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [filter, setFilter] = useState('')
  const [form, setForm] = useState({ source_id: '', target_id: '', relation_type: 'depends_on', properties: '{}' })
  const { theme } = useThemeStore()
  const canDelete = useCan('cmdb:delete')

  useEffect(() => {
    Promise.all([
      apiList<any>('/cmdb/relation').catch(() => []),
      apiList<any>('/cmdb/ci?limit=500').catch(() => []),
    ]).then(([rels, cis]) => {
      const ciMap = new Map(cis.map((c: any) => [c.id, c]))
      setRelations(rels.map((r: any) => ({
        ...r,
        source_name: ciMap.get(r.source_id)?.name || r.source_id,
        target_name: ciMap.get(r.target_id)?.name || r.target_id,
      })))
      setCiList(cis.map((c: any) => ({ id: c.id, name: c.name, type_id: c.type_id })))
    })
  }, [])

  const handleCreate = async () => {
    try {
      const props = JSON.parse(form.properties)
      await apiFetch('/cmdb/relation', {
        method: 'POST',
        body: JSON.stringify({ source_id: form.source_id, target_id: form.target_id, relation_type: form.relation_type, properties: props })
      })
      const src = ciList.find(c => c.id === form.source_id)
      const tgt = ciList.find(c => c.id === form.target_id)
      setRelations(prev => [...prev, {
        id: Date.now().toString(), ...form,
        source_name: src?.name || form.source_id,
        target_name: tgt?.name || form.target_id,
        properties: props, created_at: new Date().toISOString()
      }])
    } catch (e) { console.error(e) }
    setDialogOpen(false)
    setForm({ source_id: '', target_id: '', relation_type: 'depends_on', properties: '{}' })
  }

  const handleDelete = async (id: string) => {
    if (!confirm('确定删除该关系？')) return
    await apiFetch(`/cmdb/relation/${id}`, { method: 'DELETE' }).catch(() => {})
    setRelations(prev => prev.filter(r => r.id !== id))
  }

  const filtered = relations.filter(r =>
    !filter || r.source_name.toLowerCase().includes(filter.toLowerCase()) ||
    r.target_name.toLowerCase().includes(filter.toLowerCase()) ||
    r.relation_type.toLowerCase().includes(filter.toLowerCase())
  )

  const stats = RELATION_TYPES.map(rt => ({
    ...rt, count: relations.filter(r => r.relation_type === rt.value).length
  }))

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CI 关系管理</h1>
          <p className="text-sm text-muted-foreground">定义和管理 CI 之间的关系类型与属性</p>
        </div>
        <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建关系</Button>
      </div>

      <div className="flex gap-2 flex-wrap">
        {stats.map(s => (
          <div key={s.value} className={cn("flex items-center gap-1.5 px-3 py-1.5 rounded-full text-xs", s.color)}>
            <span>{s.label}</span>
            <span className="font-bold">{s.count}</span>
          </div>
        ))}
      </div>

      <div className="flex gap-3">
        <Input placeholder="搜索关系..." value={filter} onChange={e => setFilter(e.target.value)} className="max-w-sm" />
      </div>

      <Card>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 font-medium px-4 py-2">源 CI</th>
                  <th className="pb-2 font-medium px-4 py-2">关系类型</th>
                  <th className="pb-2 font-medium px-4 py-2">目标 CI</th>
                  <th className="pb-2 font-medium px-4 py-2">属性</th>
                  <th className="pb-2 font-medium px-4 py-2">创建时间</th>
                  <th className="pb-2 font-medium px-4 py-2">操作</th>
                </tr>
              </thead>
              <tbody>
                {filtered.length === 0 ? (
                  <tr><td colSpan={6} className="text-center py-8 text-muted-foreground">暂无关系数据</td></tr>
                ) : filtered.map(r => {
                  const rt = RELATION_TYPES.find(rt => rt.value === r.relation_type)
                  return (
                    <tr key={r.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-gray-800/50">
                      <td className="px-4 py-3 font-medium">{r.source_name}</td>
                      <td className="px-4 py-3">
                        <Badge variant="secondary" className={cn("text-[10px]", rt?.color)}>{rt?.label || r.relation_type}</Badge>
                      </td>
                      <td className="px-4 py-3 font-medium">{r.target_name}</td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">
                        {Object.keys(r.properties || {}).length > 0
                          ? Object.entries(r.properties).map(([k, v]) => `${k}=${v}`).join(', ')
                          : '-'}
                      </td>
                      <td className="px-4 py-3 text-xs text-muted-foreground">{r.created_at?.slice(0, 10)}</td>
                      <td className="px-4 py-3">
                        {canDelete && (
                          <Button variant="ghost" size="sm" className="h-6 w-6 p-0 text-destructive" onClick={() => handleDelete(r.id)}>
                            <Trash2 className="h-3 w-3" />
                          </Button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建关系">
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">源 CI *</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.source_id} onChange={e => setForm(f => ({ ...f, source_id: e.target.value }))}>
              <option value="">选择源 CI</option>
              {ciList.map(c => <option key={c.id} value={c.id}>{c.name} ({c.type_id})</option>)}
            </select>
          </div>
          <div>
            <label className="text-sm font-medium">关系类型 *</label>
            <div className="flex gap-1 flex-wrap mt-1">
              {RELATION_TYPES.map(rt => (
                <button key={rt.value} className={cn("px-2 py-1 rounded text-xs border", form.relation_type === rt.value ? rt.color + " border-current" : "border-gray-200 dark:border-gray-700")} onClick={() => setForm(f => ({ ...f, relation_type: rt.value }))}>{rt.label}</button>
              ))}
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">目标 CI *</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.target_id} onChange={e => setForm(f => ({ ...f, target_id: e.target.value }))}>
              <option value="">选择目标 CI</option>
              {ciList.map(c => <option key={c.id} value={c.id}>{c.name} ({c.type_id})</option>)}
            </select>
          </div>
          <div>
            <label className="text-sm font-medium">关系属性 (JSON)</label>
            <textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono mt-1" value={form.properties} onChange={e => setForm(f => ({ ...f, properties: e.target.value }))} placeholder='{"bandwidth": "1Gbps", "weight": 1}' />
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.source_id || !form.target_id}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
