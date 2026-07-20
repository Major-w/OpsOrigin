'use client'

import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, apiFetch } from '@/lib/utils'
import { Plus, Check, X, Play, ChevronRight, Shield } from 'lucide-react'

interface CABApproval {
  member: string
  approved: boolean
  comment?: string
  voted_at: string
}

interface ChangeRequest {
  id: string
  tenant_id: string
  ticket_id: string
  title: string
  description: string
  risk_level: string
  status: string
  change_type: string
  impact: string
  urgency: string
  cab_members: string[]
  approvals: CABApproval[]
  plan: string
  rollback_plan: string
  created_by: string
  created_at: string
  updated_at: string
}

const riskBadge: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = { critical: 'destructive', high: 'warning', medium: 'secondary', low: 'success' }
const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const statusLabels: Record<string, string> = { pending: '待审批', approved: '已批准', rejected: '已拒绝', executed: '已执行' }

function statusVariant(s: string): 'success' | 'destructive' | 'secondary' | 'outline' {
  if (s === 'approved') return 'success'
  if (s === 'rejected') return 'destructive'
  if (s === 'executed') return 'secondary'
  return 'outline'
}

export default function ChangeManagementPage() {
  const [changes, setChanges] = useState<ChangeRequest[]>([])
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<ChangeRequest | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [form, setForm] = useState({
    title: '', description: '', risk_level: 'medium', change_type: 'normal',
    impact: '', urgency: '', cab_members: '', plan: '', rollback_plan: '', created_by: 'admin', ticket_id: '',
  })
  const [voteDialog, setVoteDialog] = useState(false)
  const [voteMember, setVoteMember] = useState('admin')
  const [voteComment, setVoteComment] = useState('')

  const fetchChanges = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiFetch<ChangeRequest[]>('/itsm/changes')
      setChanges(data || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetchChanges() }, [fetchChanges])

  const handleCreate = async () => {
    const cabMembers = form.cab_members.split(',').map(s => s.trim()).filter(Boolean)
    await apiFetch('/itsm/changes', {
      method: 'POST',
      body: JSON.stringify({ ...form, cab_members: cabMembers }),
    })
    setCreateOpen(false)
    setForm({ title: '', description: '', risk_level: 'medium', change_type: 'normal', impact: '', urgency: '', cab_members: '', plan: '', rollback_plan: '', created_by: 'admin', ticket_id: '' })
    fetchChanges()
  }

  const handleVote = async (approved: boolean) => {
    if (!selected) return
    const updated = await apiFetch<ChangeRequest>(`/itsm/changes/${selected.id}/vote`, {
      method: 'POST',
      body: JSON.stringify({ member: voteMember, approved, comment: voteComment }),
    })
    setSelected(updated)
    setVoteDialog(false)
    setVoteComment('')
    fetchChanges()
  }

  const handleExecute = async () => {
    if (!selected) return
    const updated = await apiFetch<ChangeRequest>(`/itsm/changes/${selected.id}/execute`, {
      method: 'POST',
    })
    setSelected(updated)
    fetchChanges()
  }

  const allApproved = selected && selected.cab_members.length > 0 &&
    selected.cab_members.every(m => selected.approvals.some(a => a.member === m && a.approved))

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">变更管理</h1>
          <p className="text-sm text-muted-foreground">CAB 审批与变更控制</p>
        </div>
        <Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />新建变更</Button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-1">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">变更列表</CardTitle></CardHeader>
            <CardContent className="p-0">
              {loading ? <p className="px-4 text-sm text-muted-foreground">加载中...</p> : changes.length === 0 ? (
                <p className="px-4 text-sm text-muted-foreground">暂无变更请求。</p>
              ) : (
                <div className="flex flex-col">
                  {changes.map(c => (
                    <div key={c.id} onClick={() => setSelected(c)}
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selected?.id === c.id ? 'bg-accent border-l-2 border-l-emerald-500' : ''}`}>
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-sm truncate">{c.title}</span>
                        <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                      </div>
                      <div className="flex gap-1.5 mt-1.5 flex-wrap">
                        <Badge variant={riskBadge[c.risk_level] || 'secondary'} className="text-xs">{riskLabels[c.risk_level] || c.risk_level}</Badge>
                        <Badge variant={statusVariant(c.status)} className="text-xs">{statusLabels[c.status] || c.status}</Badge>
                        <Badge variant="outline" className="text-xs">{c.change_type}</Badge>
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">{c.created_by} · {formatTime(c.created_at)}</p>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        <div className="lg:col-span-2">
          {selected ? (
            <Card>
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-lg">{selected.title}</CardTitle>
                    <CardDescription className="text-xs">ID: {selected.id.slice(0, 12)}... · {selected.created_by} · {formatTime(selected.created_at)}</CardDescription>
                  </div>
                  <div className="flex gap-1">
                    {selected.status === 'pending' && (
                      <Button variant="outline" size="sm" onClick={() => { setVoteDialog(true); setVoteComment('') }}>
                        <Shield className="h-3 w-3" />投票
                      </Button>
                    )}
                    {selected.status === 'approved' && (
                      <Button variant="outline" size="sm" onClick={handleExecute}><Play className="h-3 w-3" />执行</Button>
                    )}
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-2 text-sm mb-4">
                  <div><span className="text-muted-foreground">风险:</span> <Badge variant={riskBadge[selected.risk_level]} className="text-xs">{riskLabels[selected.risk_level]}</Badge></div>
                  <div><span className="text-muted-foreground">状态:</span> <Badge variant={statusVariant(selected.status)} className="text-xs">{statusLabels[selected.status]}</Badge></div>
                  <div><span className="text-muted-foreground">类型:</span> {selected.change_type}</div>
                  {selected.impact && <div><span className="text-muted-foreground">影响:</span> {selected.impact}</div>}
                  {selected.urgency && <div><span className="text-muted-foreground">紧急度:</span> {selected.urgency}</div>}
                  {selected.ticket_id && <div><span className="text-muted-foreground">关联工单:</span> {selected.ticket_id.slice(0, 12)}...</div>}
                </div>

                {selected.description && (
                  <div className="mb-4">
                    <h4 className="text-sm font-medium mb-1">描述</h4>
                    <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selected.description}</p>
                  </div>
                )}

                {selected.plan && (
                  <div className="mb-4">
                    <h4 className="text-sm font-medium mb-1">实施计划</h4>
                    <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selected.plan}</p>
                  </div>
                )}

                {selected.rollback_plan && (
                  <div className="mb-4">
                    <h4 className="text-sm font-medium mb-1">回滚计划</h4>
                    <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selected.rollback_plan}</p>
                  </div>
                )}

                <div>
                  <h4 className="text-sm font-medium mb-2">CAB 投票 ({selected.approvals.length}/{selected.cab_members.length})</h4>
                  {selected.cab_members.length === 0 ? (
                    <p className="text-sm text-muted-foreground">未指定 CAB 成员</p>
                  ) : (
                    <div className="space-y-1">
                      {selected.cab_members.map(m => {
                        const approval = selected.approvals.find(a => a.member === m)
                        return (
                          <div key={m} className="flex items-center justify-between border rounded-md px-3 py-2 text-sm">
                            <span>{m}</span>
                            <div className="flex items-center gap-2">
                              {approval ? (
                                <>
                                  <Badge variant={approval.approved ? 'success' : 'destructive'} className="text-xs">
                                    {approval.approved ? '已批准' : '已拒绝'}
                                  </Badge>
                                  {approval.comment && <span className="text-xs text-muted-foreground">{approval.comment}</span>}
                                </>
                              ) : (
                                <Badge variant="outline" className="text-xs">待投票</Badge>
                              )}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}
                  {allApproved && selected.status === 'pending' && (
                    <p className="text-sm text-emerald-600 mt-2">所有成员已批准，可执行变更</p>
                  )}
                </div>
              </CardContent>
            </Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]">
              <CardContent className="text-center text-muted-foreground">
                <p>选择一个变更请求查看详情</p>
              </CardContent>
            </Card>
          )}
        </div>
      </div>

      <Dialog open={createOpen} onClose={() => setCreateOpen(false)} title="新建变更请求">
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div><label className="text-sm font-medium">标题 *</label><Input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))} placeholder="变更标题" /></div>
          <div><label className="text-sm font-medium">描述</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">风险等级</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.risk_level} onChange={e => setForm(f => ({ ...f, risk_level: e.target.value }))}>
                <option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">变更类型</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.change_type} onChange={e => setForm(f => ({ ...f, change_type: e.target.value }))}>
                <option value="standard">标准</option><option value="normal">普通</option><option value="emergency">紧急</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">影响</label><Input value={form.impact} onChange={e => setForm(f => ({ ...f, impact: e.target.value }))} placeholder="影响范围" /></div>
            <div><label className="text-sm font-medium">紧急度</label><Input value={form.urgency} onChange={e => setForm(f => ({ ...f, urgency: e.target.value }))} placeholder="紧急程度" /></div>
          </div>
          <div><label className="text-sm font-medium">CAB 成员 (逗号分隔)</label><Input value={form.cab_members} onChange={e => setForm(f => ({ ...f, cab_members: e.target.value }))} placeholder="user1, user2, user3" /></div>
          <div><label className="text-sm font-medium">实施计划</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.plan} onChange={e => setForm(f => ({ ...f, plan: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">回滚计划</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.rollback_plan} onChange={e => setForm(f => ({ ...f, rollback_plan: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">创建人</label><Input value={form.created_by} onChange={e => setForm(f => ({ ...f, created_by: e.target.value }))} /></div>
            <div><label className="text-sm font-medium">关联工单 ID (可选)</label><Input value={form.ticket_id} onChange={e => setForm(f => ({ ...f, ticket_id: e.target.value }))} placeholder="留空则不关联" /></div>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setCreateOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.title}>提交</Button></DialogFooter>
      </Dialog>

      <Dialog open={voteDialog} onClose={() => setVoteDialog(false)} title="CAB 投票">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">投票人</label><Input value={voteMember} onChange={e => setVoteMember(e.target.value)} /></div>
          <div><label className="text-sm font-medium">备注</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={voteComment} onChange={e => setVoteComment(e.target.value)} placeholder="投票备注（可选）" /></div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setVoteDialog(false)}>取消</Button>
          <Button variant="destructive" onClick={() => handleVote(false)}>拒绝</Button>
          <Button onClick={() => handleVote(true)}>批准</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
