'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { FileText, Search, AlertTriangle, CheckCircle, Clock, GitBranch, ArrowRight } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ChangeRequest {
  id: string
  title: string
  type: 'infrastructure' | 'application' | 'network' | 'database'
  status: 'draft' | 'assessing' | 'approved' | 'rejected'
  sourceCI: string
  targetCIs: string[]
  riskScore: number
  impactScope: string[]
  processId?: string
  createdAt: string
}

const MOCK_CHANGES: ChangeRequest[] = [
  { id: '1', title: '升级 web-01 操作系统', type: 'infrastructure', status: 'assessing', sourceCI: 'web-01', targetCIs: ['lb-01', 'app-01'], riskScore: 65, impactScope: ['用户登录', '商品浏览'], processId: 'p1', createdAt: '2025-01-15 10:00:00' },
  { id: '2', title: '数据库主从切换', type: 'database', status: 'draft', sourceCI: 'db-master', targetCIs: ['db-slave', 'app-01', 'app-02', 'web-01', 'web-02'], riskScore: 85, impactScope: ['所有业务服务'], createdAt: '2025-01-15 11:00:00' },
  { id: '3', title: '防火墙规则更新', type: 'network', status: 'approved', sourceCI: 'firewall-01', targetCIs: ['web-01', 'web-02'], riskScore: 30, impactScope: ['外部访问'], createdAt: '2025-01-14 09:00:00' },
  { id: '4', title: '应用版本回滚', type: 'application', status: 'rejected', sourceCI: 'app-01', targetCIs: ['web-01'], riskScore: 45, impactScope: ['订单服务'], createdAt: '2025-01-13 14:00:00' },
]

export default function CMDBChangeAssessPage() {
  const [changes, setChanges] = useState(MOCK_CHANGES)
  const [selected, setSelected] = useState<ChangeRequest | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ title: '', type: 'infrastructure' as const, sourceCI: '', targetCIs: '' })

  const handleAssess = (id: string) => {
    setChanges(prev => prev.map(c => c.id === id ? { ...c, status: 'assessing' } : c))
    setTimeout(() => {
      setChanges(prev => prev.map(c => c.id === id ? { ...c, status: 'approved', riskScore: Math.floor(Math.random() * 60) + 20 } : c))
    }, 1500)
  }

  const handleCreate = () => {
    const newChange: ChangeRequest = {
      id: Date.now().toString(), ...form, status: 'draft', targetCIs: form.targetCIs.split(',').map(s => s.trim()).filter(Boolean),
      riskScore: 0, impactScope: [], createdAt: new Date().toISOString().slice(0, 19).replace('T', ' ')
    }
    setChanges(prev => [...prev, newChange])
    setDialogOpen(false)
    setForm({ title: '', type: 'infrastructure', sourceCI: '', targetCIs: '' })
  }

  const statusConfig: Record<string, { label: string; color: string; icon: any }> = {
    draft: { label: '草稿', color: 'bg-gray-100 text-gray-700 dark:bg-gray-500/20 dark:text-gray-400', icon: FileText },
    assessing: { label: '评估中', color: 'bg-blue-100 text-blue-700 dark:bg-blue-500/20 dark:text-blue-400', icon: Clock },
    approved: { label: '已通过', color: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400', icon: CheckCircle },
    rejected: { label: '已拒绝', color: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400', icon: AlertTriangle },
  }

  const getRiskColor = (score: number) => score >= 80 ? 'text-red-600' : score >= 60 ? 'text-orange-600' : score >= 40 ? 'text-yellow-600' : 'text-green-600'

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">变更影响评估</h1>
          <p className="text-sm text-muted-foreground">变更前基于 CMDB 拓扑关系评估变更影响范围，联动 ITSM 变更流程</p>
        </div>
        <Button onClick={() => setDialogOpen(true)}><FileText className="h-4 w-4" />新建变更</Button>
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
        {/* 左侧变更列表 */}
        <div className="w-96 space-y-2 shrink-0">
          {changes.map(change => {
            const sc = statusConfig[change.status]
            return (
              <Card key={change.id} className={cn("cursor-pointer transition-all hover:shadow-md", selected?.id === change.id && "border-emerald-500")} onClick={() => setSelected(change)}>
                <CardContent className="p-3 space-y-2">
                  <div className="flex items-start justify-between">
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium truncate">{change.title}</p>
                      <p className="text-xs text-muted-foreground mt-0.5">源: {change.sourceCI} · 影响 {change.targetCIs.length} 个 CI</p>
                    </div>
                    <Badge className={cn("text-[10px] ml-2 shrink-0", sc.color)}>{sc.label}</Badge>
                  </div>
                  <div className="flex items-center justify-between">
                    <span className={cn("text-lg font-bold", getRiskColor(change.riskScore))}>{change.riskScore}</span>
                    <span className="text-[10px] text-muted-foreground">{change.createdAt.slice(0, 10)}</span>
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>

        {/* 右侧详情 */}
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
                    <div><p className="text-xs text-muted-foreground">源 CI</p><p className="text-sm font-medium">{selected.sourceCI}</p></div>
                    <div><p className="text-xs text-muted-foreground">风险评分</p><p className={cn("text-2xl font-bold", getRiskColor(selected.riskScore))}>{selected.riskScore}</p></div>
                    <div><p className="text-xs text-muted-foreground">影响 CI 数</p><p className="text-2xl font-bold">{selected.targetCIs.length}</p></div>
                  </div>

                  <div>
                    <p className="text-xs text-muted-foreground mb-2">影响范围</p>
                    <div className="flex flex-wrap gap-1.5">
                      {selected.targetCIs.map(ci => (
                        <Badge key={ci} variant="outline" className="text-xs">{ci}</Badge>
                      ))}
                    </div>
                  </div>

                  {selected.impactScope.length > 0 && (
                    <div>
                      <p className="text-xs text-muted-foreground mb-2">受影响业务</p>
                      <div className="flex flex-wrap gap-1.5">
                        {selected.impactScope.map(s => (
                          <Badge key={s} variant="secondary" className="text-xs">{s}</Badge>
                        ))}
                      </div>
                    </div>
                  )}

                  {/* 影响链路图 */}
                  <div>
                    <p className="text-xs text-muted-foreground mb-2">影响链路</p>
                    <div className="flex items-center gap-2 flex-wrap p-3 rounded-lg bg-gray-50 dark:bg-gray-800/50">
                      <Badge variant="outline" className="text-xs">{selected.sourceCI}</Badge>
                      {selected.targetCIs.map((ci, i) => (
                        <div key={ci} className="flex items-center gap-1">
                          <ArrowRight className="h-3 w-3 text-muted-foreground" />
                          <Badge variant="secondary" className="text-xs">{ci}</Badge>
                        </div>
                      ))}
                    </div>
                  </div>

                  <div className="flex gap-2 pt-2">
                    {selected.status === 'draft' && (
                      <Button onClick={() => handleAssess(selected.id)}><AlertTriangle className="h-4 w-4" />开始评估</Button>
                    )}
                    {selected.status === 'assessing' && (
                      <Button disabled><Clock className="h-4 w-4 animate-spin" />评估中...</Button>
                    )}
                    {selected.status === 'approved' && (
                      <Button variant="outline"><GitBranch className="h-4 w-4" />关联 ITSM 工单</Button>
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
            <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm mt-1" value={form.type} onChange={e => setForm(f => ({ ...f, type: e.target.value as any }))}>
              <option value="infrastructure">基础设施</option><option value="application">应用</option><option value="network">网络</option><option value="database">数据库</option>
            </select>
          </div>
          <div><label className="text-sm font-medium">源 CI *</label><Input value={form.sourceCI} onChange={e => setForm(f => ({ ...f, sourceCI: e.target.value }))} placeholder="受影响的源 CI" /></div>
          <div><label className="text-sm font-medium">目标 CI（逗号分隔）</label><Input value={form.targetCIs} onChange={e => setForm(f => ({ ...f, targetCIs: e.target.value }))} placeholder="web-01, app-01" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.title || !form.sourceCI}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
