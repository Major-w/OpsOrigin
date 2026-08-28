
import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button, Tag, Input, Modal } from 'antd'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { Plus, Check, X, Play, ChevronRight, Shield, ChevronLeft, CalendarDays } from 'lucide-react'

/** 获取当前登录用户名 */
const getCurrentUser = () => useAuthStore.getState().user?.username || 'anonymous'

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
  scheduled_at?: string
  window_start?: string
  window_end?: string
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
    impact: '', urgency: '', cab_members: '', plan: '', rollback_plan: '', created_by: '', ticket_id: '',
    scheduled_at: '', window_start: '', window_end: '',
  })
  const [voteDialog, setVoteDialog] = useState(false)
  const [voteMember, setVoteMember] = useState('')
  const [voteComment, setVoteComment] = useState('')
  const [calYear, setCalYear] = useState(() => new Date().getFullYear())
  const [calMonth, setCalMonth] = useState(() => new Date().getMonth())

  const fetchChanges = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiList<ChangeRequest>('/itsm/changes')
      setChanges(data || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetchChanges() }, [fetchChanges])

  const handleCreate = async () => {
    const cabMembers = form.cab_members.split(',').map(s => s.trim()).filter(Boolean)
    await apiFetch('/itsm/changes', {
      method: 'POST',
      body: JSON.stringify({ ...form, created_by: getCurrentUser(), cab_members: cabMembers }),
    })
    setCreateOpen(false)
    setForm({ title: '', description: '', risk_level: 'medium', change_type: 'normal', impact: '', urgency: '', cab_members: '', plan: '', rollback_plan: '', created_by: '', ticket_id: '', scheduled_at: '', window_start: '', window_end: '' })
    fetchChanges()
  }

  const handleVote = async (approved: boolean) => {
    if (!selected) return
    const updated = await apiFetch<ChangeRequest>(`/itsm/changes/${selected.id}/vote`, {
      method: 'POST',
      body: JSON.stringify({ member: getCurrentUser(), approved, comment: voteComment }),
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

      <Card>
        <CardHeader className="py-3">
          <div className="flex items-center justify-between">
            <CardTitle className="text-sm flex items-center gap-1.5"><CalendarDays className="h-3.5 w-3.5" />变更日历（{calYear} 年 {calMonth + 1} 月）</CardTitle>
            <div className="flex gap-1">
              <Button type="default" size="middle" className="h-7 w-7 p-0" onClick={() => { if (calMonth === 0) { setCalMonth(11); setCalYear(y => y - 1) } else setCalMonth(m => m - 1) }}><ChevronLeft className="h-3 w-3" /></Button>
              <Button type="default" size="middle" className="h-7 w-7 p-0" onClick={() => { if (calMonth === 11) { setCalMonth(0); setCalYear(y => y + 1) } else setCalMonth(m => m + 1) }}><ChevronRight className="h-3 w-3" /></Button>
            </div>
          </div>
        </CardHeader>
        <CardContent>
          <CalendarGrid year={calYear} month={calMonth} changes={changes} onSelect={setSelected} />
        </CardContent>
      </Card>

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
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selected?.id === c.id ? 'bg-accent border-l-2 border-l-blue-500' : ''}`}>
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-sm truncate">{c.title}</span>
                        <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                      </div>
                      <div className="flex gap-1.5 mt-1.5 flex-wrap">
                        <Tag color={riskBadge[c.risk_level] === 'destructive' ? 'error' : riskBadge[c.risk_level] === 'warning' ? 'warning' : riskBadge[c.risk_level] === 'success' ? 'success' : 'default'} className="text-xs">{riskLabels[c.risk_level] || c.risk_level}</Tag>
                        <Tag color={statusVariant(c.status) === 'success' ? 'success' : statusVariant(c.status) === 'destructive' ? 'error' : statusVariant(c.status) === 'secondary' ? 'default' : 'default'} className="text-xs">{statusLabels[c.status] || c.status}</Tag>
                        <Tag color="default" className="text-xs">{c.change_type}</Tag>
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
                      <Button type="default" size="middle" onClick={() => { setVoteDialog(true); setVoteComment('') }}>
                        <Shield className="h-3 w-3" />投票
                      </Button>
                    )}
                    {selected.status === 'approved' && (
                      <Button type="default" size="middle" onClick={handleExecute}><Play className="h-3 w-3" />执行</Button>
                    )}
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-2 text-sm mb-4">
                  <div><span className="text-muted-foreground">风险:</span> <Tag color={riskBadge[selected.risk_level] === 'destructive' ? 'error' : riskBadge[selected.risk_level] === 'warning' ? 'warning' : riskBadge[selected.risk_level] === 'success' ? 'success' : 'default'} className="text-xs">{riskLabels[selected.risk_level]}</Tag></div>
                  <div><span className="text-muted-foreground">状态:</span> <Tag color={statusVariant(selected.status) === 'success' ? 'success' : statusVariant(selected.status) === 'destructive' ? 'error' : 'default'} className="text-xs">{statusLabels[selected.status]}</Tag></div>
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
                                  <Tag color={approval.approved ? 'success' : 'error'} className="text-xs">
                                    {approval.approved ? '已批准' : '已拒绝'}
                                  </Tag>
                                  {approval.comment && <span className="text-xs text-muted-foreground">{approval.comment}</span>}
                                </>
                              ) : (
                                <Tag color="default" className="text-xs">待投票</Tag>
                              )}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )}
                  {allApproved && selected.status === 'pending' && (
                    <p className="text-sm text-blue-600 mt-2">所有成员已批准，可执行变更</p>
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

      <Modal open={createOpen} onCancel={() => setCreateOpen(false)} title="新建变更请求" footer={null}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div><label className="text-sm font-medium">标题 *</label><Input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))} placeholder="变更标题" /></div>
          <div><label className="text-sm font-medium">描述</label><Input.TextArea rows={3} value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
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
          <div><label className="text-sm font-medium">实施计划</label><Input.TextArea rows={3} value={form.plan} onChange={e => setForm(f => ({ ...f, plan: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">回滚计划</label><Input.TextArea rows={3} value={form.rollback_plan} onChange={e => setForm(f => ({ ...f, rollback_plan: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">创建人</label><Input value={form.created_by} onChange={e => setForm(f => ({ ...f, created_by: e.target.value }))} /></div>
            <div><label className="text-sm font-medium">关联工单 ID (可选)</label><Input value={form.ticket_id} onChange={e => setForm(f => ({ ...f, ticket_id: e.target.value }))} placeholder="留空则不关联" /></div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">计划开始时间</label><input type="datetime-local" className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.scheduled_at} onChange={e => setForm(f => ({ ...f, scheduled_at: e.target.value }))} /></div>
            <div><label className="text-sm font-medium">变更窗口</label><input type="datetime-local" className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.window_start} onChange={e => setForm(f => ({ ...f, window_start: e.target.value }))} placeholder="窗口开始" /></div>
          </div>
          <div><label className="text-sm font-medium">窗口结束</label><input type="datetime-local" className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.window_end} onChange={e => setForm(f => ({ ...f, window_end: e.target.value }))} /></div>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button onClick={() => setCreateOpen(false)}>取消</Button><Button type="primary" onClick={handleCreate} disabled={!form.title}>提交</Button></div>
      </Modal>

      <Modal open={voteDialog} onCancel={() => setVoteDialog(false)} title="CAB 投票" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">投票人</label><Input value={voteMember} onChange={e => setVoteMember(e.target.value)} /></div>
          <div><label className="text-sm font-medium">备注</label><Input.TextArea rows={3} value={voteComment} onChange={e => setVoteComment(e.target.value)} placeholder="投票备注（可选）" /></div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setVoteDialog(false)}>取消</Button>
          <Button danger onClick={() => handleVote(false)}>拒绝</Button>
          <Button type="primary" onClick={() => handleVote(true)}>批准</Button>
        </div>
      </Modal>
    </div>
  )
}

// CalendarGrid —— 变更日历: 按月的 7 列网格, 把带排期(scheduled_at/window_start)的变更块渲染到对应日期。
function CalendarGrid({
  year,
  month,
  changes,
  onSelect,
}: {
  year: number
  month: number
  changes: ChangeRequest[]
  onSelect: (c: ChangeRequest) => void
}) {
  const today = new Date()
  const firstDay = new Date(year, month, 1).getDay()
  const daysInMonth = new Date(year, month + 1, 0).getDate()
  const weekdays = ['日', '一', '二', '三', '四', '五', '六']

  const dayOf = (iso?: string) => {
    if (!iso) return null
    const d = new Date(iso)
    if (isNaN(d.getTime())) return null
    return { date: d.getDate(), month: d.getMonth(), year: d.getFullYear(), label: `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}` }
  }

  const cells: (number | null)[] = [
    ...Array.from({ length: firstDay }, () => null),
    ...Array.from({ length: daysInMonth }, (_, i) => i + 1),
  ]
  const riskColor: Record<string, string> = { critical: 'bg-red-500', high: 'bg-amber-500', medium: 'bg-blue-500', low: 'bg-blue-500' }

  return (
    <div>
      <div className="grid grid-cols-7 gap-1 mb-1">
        {weekdays.map(w => <div key={w} className="text-center text-[11px] text-muted-foreground py-1">{w}</div>)}
      </div>
      <div className="grid grid-cols-7 gap-1">
        {cells.map((d, i) => {
          const dayChanges = d === null ? [] : changes.filter(c => {
            const dt = dayOf(c.scheduled_at) || dayOf(c.window_start)
            return dt !== null && dt.date === d && dt.month === month && dt.year === year
          })
          const isToday = d === today.getDate() && month === today.getMonth() && year === today.getFullYear()
          return (
            <div key={i} className={`min-h-[64px] rounded-md border p-1 text-xs ${isToday ? 'border-primary' : 'border-border'} ${d === null ? 'bg-muted/30' : ''}`}>
              {d !== null && (
                <>
                  <div className="text-right text-muted-foreground">{d}</div>
                  <div className="space-y-0.5">
                    {dayChanges.slice(0, 3).map(c => {
                      const dt = dayOf(c.scheduled_at) || dayOf(c.window_start)
                      return (
                        <button
                          key={c.id}
                          type="button"
                          onClick={() => onSelect(c)}
                          className={`block w-full text-left truncate rounded px-1 py-0.5 text-[10px] text-white ${riskColor[c.risk_level] || 'bg-gray-500'}`}
                        >
                          {dt ? dt.label : ''} {c.title}
                        </button>
                      )
                    })}
                    {dayChanges.length > 3 && <div className="text-[10px] text-muted-foreground">+{dayChanges.length - 3} 项</div>}
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>
      {changes.filter(c => c.scheduled_at || c.window_start).length === 0 && (
        <p className="text-xs text-muted-foreground mt-2">暂无排期变更，可在新建变更时填写计划开始时间/变更窗口。</p>
      )}
    </div>
  )
}
