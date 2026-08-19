'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { FileText, Search, AlertTriangle, CheckCircle, Clock, GitBranch, ArrowRight, Link2 } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { toast } from '@/stores/toast'
import { cn } from '@/lib/utils'

interface CI { id: string; name: string; type_id?: string }
interface ImpactResult { source_ci: string; affected_ci: string[]; depth: number; has_cycle: boolean }
interface ChangeRequest {
  id: string; title: string; type: string; status: string
  source_ci: string; risk_score: number; impact_scope: string; process_id?: string; created_at: string
}

function computeRisk(impact?: ImpactResult): number {
  if (!impact) return 0
  const base = 20 + impact.affected_ci.length * 8 + impact.depth * 5 + (impact.has_cycle ? 20 : 0)
  return Math.min(100, base)
}

export default function CMDBChangeAssessPage() {
  const canRead = useCan('cmdb:read')
  const canWrite = useCan('cmdb:write')
  const [changes, setChanges] = useState<ChangeRequest[]>([])
  const [cis, setCis] = useState<CI[]>([])
  const [ciNames, setCiNames] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<ChangeRequest | null>(null)
  const [impact, setImpact] = useState<ImpactResult | null>(null)
  const [assessing, setAssessing] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ title: '', type: 'infrastructure', source_ci: '' })

  useEffect(() => {
    apiList<ChangeRequest>('/cmdb/change-requests').then(setChanges).catch(() => toast.error('加载变更请求失败'))
    apiList<CI>('/cmdb/ci?limit=500').then(setCis).catch(() => {})
  }, [])

  const loadImpact = async (sourceCI: string) => {
    if (!sourceCI) { setImpact(null); return }
    try {
      const res = await apiFetch<ImpactResult>(`/cmdb/impact/${sourceCI}`)
      setImpact(res)
      // 解析受影响 CI 名称
      const ids = [sourceCI, ...res.affected_ci]
      const missing = ids.filter(id => !ciNames[id])
      await Promise.all(missing.map(async id => {
        try {
          const ci = await apiFetch<CI>(`/cmdb/ci/${id}`)
          setCiNames(prev => ({ ...prev, [id]: ci.name }))
        } catch { setCiNames(prev => ({ ...prev, [id]: id })) }
      }))
    } catch (e: any) {
      toast.error('影响分析失败: ' + e.message)
      setImpact(null)
    }
  }

  const handleSelect = (c: ChangeRequest) => {
    setSelected(c)
    setImpact(null)
    if (c.status !== 'draft') loadImpact(c.source_ci)
  }

  const handleAssess = async (id: string) => {
    const c = changes.find(x => x.id === id)
    if (!c) return
    setAssessing(true)
    try {
      const res = await apiFetch<ImpactResult>(`/cmdb/impact/${c.source_ci}`)
      const score = computeRisk(res)
      const updated = await apiFetch<ChangeRequest>(`/cmdb/change-requests/${id}`, {
        method: 'PUT', body: JSON.stringify({ ...c, status: 'approved', risk_score: score, impact_scope: JSON.stringify(res.affected_ci) })
      })
      setChanges(prev => prev.map(x => x.id === id ? updated : x))
      setSelected(updated)
      setImpact(res)
      toast.success('评估完成')
    } catch (e: any) { toast.error('评估失败: ' + e.message) }
    finally { setAssessing(false) }
  }

  const handleCreate = async () => {
    if (!form.title || !form.source_ci) { toast.error('请填写标题与源 CI'); return }
    try {
      const created = await apiFetch<ChangeRequest>('/cmdb/change-requests', { method: 'POST', body: JSON.stringify(form) })
      setChanges(prev => [created, ...prev])
      setDialogOpen(false)
      setForm({ title: '', type: 'infrastructure', source_ci: '' })
      toast.success('变更请求已创建')
    } catch (e: any) { toast.error('创建失败: ' + e.message) }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/cmdb/change-requests/${id}`, { method: 'DELETE' })
      setChanges(prev => prev.filter(x => x.id !== id))
      if (selected?.id === id) setSelected(null)
      toast.success('已删除')
    } catch (e: any) { toast.error('删除失败: ' + e.message) }
  }

  const statusConfig: Record<string, { label: string; color: string; icon: any }> = {
    draft: { label: '草稿', color: 'bg-gray-100 text-gray-700 dark:bg-gray-500/20 dark:text-gray-400', icon: FileText },
    assessing: { label: '评估中', color: 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-400', icon: Clock },
    approved: { label: '已通过', color: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400', icon: CheckCircle },
    rejected: { label: '已拒绝', color: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400', icon: AlertTriangle },
  }
  const getRiskColor = (score: number) => score >= 80 ? 'text-red-600' : score >= 60 ? 'text-orange-600' : score >= 40 ? 'text-yellow-600' : 'text-green-600'

  if (!canRead) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <FileText className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「查看 CMDB 资源（cmdb:read）」权限，无法使用变更影响评估。</p>
        </CardContent></Card>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">变更影响评估</h1>
          <p className="text-sm text-muted-foreground">基于 CMDB 真实拓扑关系评估变更影响范围，联动 ITSM 变更流程</p>
        </div>
        {canWrite && <Button onClick={() => setDialogOpen(true)}><FileText className="h-4 w-4" />新建变更</Button>}
      </div>

      <div className="grid grid-cols-4 gap-4">
        {Object.entries(statusConfig).map(([key, cfg]) => (
          <Card key={key}>
            <CardContent className="p-4 flex items-center gap-3">
              <div className={cn("h-10 w-10 rounded-lg flex items-center justify-center", cfg.color)}>
                <cfg.icon className="h-5 w-5" />
              </div>
              <div>
                <p className="text-2xl font-bold">{changes.filter(c => c.status === key).length}</p>
                <p className="text-xs text-muted-foreground">{cfg.label}</p>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="flex gap-4">
        <div className="w-96 space-y-2 shrink-0">
          {changes.map(change => {
            const sc = statusConfig[change.status]
            return (
              <Card key={change.id} className={cn("cursor-pointer transition-all hover:shadow-md", selected?.id === change.id && "border-emerald-500")}>
                <CardContent className="p-3 space-y-2">
                  <div className="flex items-start justify-between">
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">{change.title}</p>
                      <p className="text-xs text-muted-foreground mt-0.5">源: {ciNames[change.source_ci] || change.source_ci} · 影响 {change.impact_scope ? (JSON.parse(change.impact_scope).length || 0) : 0} 个 CI</p>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <Badge className={cn("text-[10px] mr-1", sc.color)}>{sc.label}</Badge>
                      {canWrite && <button className="text-red-400 hover:text-red-600" onClick={(e) => { e.stopPropagation(); handleDelete(change.id) }}><AlertTriangle className="h-3 w-3" /></button>}
                    </div>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className={cn("text-lg font-bold", getRiskColor(change.risk_score))}>{change.risk_score}</span>
                    <span className="text-[10px] text-muted-foreground">{change.created_at.slice(0, 10)}</span>
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>

        <div className="flex-1">
          {selected ? (
            <div className="space-y-4">
              <Card>
                <CardHeader className="py-3">
                  <CardTitle className="text-sm flex items-center justify-between">
                    <span>{selected.title}</span>
                    <Badge className={cn("text-[10px]", statusConfig[selected.status].color)}>{statusConfig[selected.status].label}</Badge>
                  </CardTitle>
                </CardHeader>
                <CardContent className="space-y-4">
                  <div className="grid grid-cols-3 gap-4">
                    <div><p className="text-xs text-muted-foreground">源 CI</p><p className="text-sm font-medium">{ciNames[selected.source_ci] || selected.source_ci}</p></div>
                    <div><p className="text-xs text-muted-foreground">风险评分</p><p className={cn("text-2xl font-bold", getRiskColor(selected.risk_score))}>{selected.risk_score}</p></div>
                    <div><p className="text-xs text-muted-foreground">影响 CI 数</p><p className="text-2xl font-bold">{(() => { try { return JSON.parse(selected.impact_scope || '[]').length } catch { return 0 } })()}</p></div>
                  </div>

                  {impact && (
                    <div>
                      <p className="text-xs text-muted-foreground mb-2">影响链路（基于 CMDB 关系图 BFS，深度 {impact.depth}）{impact.has_cycle && <span className="ml-2 text-red-500">检测到环路</span>}</p>
                      <div className="flex items-center gap-2 flex-wrap p-3 rounded-lg bg-gray-50 dark:bg-gray-800/50">
                        <Badge variant="outline" className="text-xs">{ciNames[impact.source_ci] || impact.source_ci}</Badge>
                        {impact.affected_ci.map((ci, i) => (
                          <div key={ci} className="flex items-center gap-1">
                            <ArrowRight className="h-3 w-3 text-muted-foreground" />
                            <Badge variant="secondary" className="text-xs">{ciNames[ci] || ci}</Badge>
                          </div>
                        ))}
                        {impact.affected_ci.length === 0 && <span className="text-xs text-muted-foreground">无下游影响</span>}
                      </div>
                    </div>
                  )}

                  <div className="flex gap-2 pt-2">
                    {canWrite && selected.status === 'draft' && (
                      <Button onClick={() => handleAssess(selected.id)} disabled={assessing}><AlertTriangle className="h-4 w-4" />{assessing ? '评估中...' : '开始评估'}</Button>
                    )}
                    {selected.status === 'approved' && (
                      <Button variant="outline" asChild><a href="/itsm/tickets"><Link2 className="h-4 w-4" />关联 ITSM 工单</a></Button>
                    )}
                  </div>
                </CardContent>
              </Card>
            </div>
          ) : (
            <Card className="h-[400px] flex items-center justify-center">
              <div className="text-center text-muted-foreground">
                <FileText className="h-12 w-12 mx-auto mb-3 opacity-30" />
                <p>请在左侧选择一个变更请求查看详情</p>
              </div>
            </Card>
          )}
        </div>
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建变更请求">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">标题 *</label><Input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))} placeholder="变更标题" /></div>
          <div><label className="text-sm font-medium">变更类型</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.type} onChange={e => setForm(f => ({ ...f, type: e.target.value }))}>
              <option value="infrastructure">基础设施</option><option value="application">应用</option><option value="network">网络</option><option value="database">数据库</option>
            </select>
          </div>
          <div><label className="text-sm font-medium">源 CI *</label>
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.source_ci} onChange={e => setForm(f => ({ ...f, source_ci: e.target.value }))}>
              <option value="">选择受变更的源 CI</option>
              {cis.map(ci => <option key={ci.id} value={ci.id}>{ci.name}</option>)}
            </select>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.title || !form.source_ci || !canWrite}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
