'use client'

import { useEffect, useState, useRef, useCallback } from 'react'
import { useRouter } from 'next/navigation'
import { useITSMStore, type Ticket, type TicketEvent, type ITSMStats } from '@/stores/itsm'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime, apiFetch } from '@/lib/utils'
import { Plus, Check, X, Play, RotateCcw, Clock, FileText, Activity, ChevronRight, Paperclip, MessageSquare, Upload, Download, Trash2, FileUp, FileDown, BookOpen } from 'lucide-react'

const riskBadge: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = { critical: 'destructive', high: 'warning', medium: 'secondary', low: 'success' }
const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const statusLabels: Record<string, string> = {
  draft: '草稿', pending_review: '待审批', approved: '已审批', rejected: '已驳回',
  executing: '执行中', succeeded: '已完成', failed: '已失败', rolled_back: '已回滚',
}
const SLA_BY_RISK: Record<string, string> = { critical: '4小时', high: '24小时', medium: '48小时', low: '72小时' }

interface Comment {
  id: number
  ticket_id: string
  tenant_id: string
  author: string
  content: string
  created_at: string
}

interface Attachment {
  id: number
  ticket_id: string
  tenant_id: string
  file_name: string
  file_size: number
  file_type: string
  uploaded_by: string
  created_at: string
}

function statusVariant(s: string): 'success' | 'destructive' | 'secondary' | 'outline' {
  if (s === 'approved' || s === 'succeeded') return 'success'
  if (s === 'rejected' || s === 'failed') return 'destructive'
  if (s === 'executing') return 'secondary'
  return 'outline'
}

function formatFileSize(bytes: number): string {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

export default function ITSMPage() {
  const router = useRouter()
  const { tickets, stats, loading, fetch, create, approve, reject, execute, complete, rollback, getTicket, getEvents, fetchStats, processes, fetchProcesses } = useITSMStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: 'admin', process_id: '' })
  const [knowledgeRecs, setKnowledgeRecs] = useState<any[]>([])
  const [selectedTicket, setSelectedTicket] = useState<Ticket | null>(null)
  const [ticketEvents, setTicketEvents] = useState<TicketEvent[]>([])
  const [detailTab, setDetailTab] = useState('detail')
  const [approveDialog, setApproveDialog] = useState(false)
  const [approveComment, setApproveComment] = useState('')
  const [rejectDialog, setRejectDialog] = useState(false)
  const [rejectReason, setRejectReason] = useState('')
  const [completeDialog, setCompleteDialog] = useState(false)
  const [completeComment, setCompleteComment] = useState('')
  const [rollbackDialog, setRollbackDialog] = useState(false)
  const [rollbackReason, setRollbackReason] = useState('')
  const [ciIdInput, setCiIdInput] = useState('')
  const [ciIds, setCiIds] = useState<string[]>([])
  const [ticketFormOpen, setTicketFormOpen] = useState(false)

  // Comments
  const [comments, setComments] = useState<Comment[]>([])
  const [commentAuthor, setCommentAuthor] = useState('admin')
  const [commentContent, setCommentContent] = useState('')
  const [commentLoading, setCommentLoading] = useState(false)

  // Attachments
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [uploading, setUploading] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Import
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<{ total: number; success: number; errors: { row: number; message: string }[] } | null>(null)
  const [importDialog, setImportDialog] = useState(false)
  const importFileRef = useRef<HTMLInputElement>(null)

  const fetchComments = useCallback(async (ticketId: string) => {
    try {
      const data = await apiFetch<Comment[]>(`/itsm/tickets/${ticketId}/comments`)
      setComments(data || [])
    } catch { setComments([]) }
  }, [])

  const fetchAttachments = useCallback(async (ticketId: string) => {
    try {
      const data = await apiFetch<Attachment[]>(`/itsm/tickets/${ticketId}/attachments`)
      setAttachments(data || [])
    } catch { setAttachments([]) }
  }, [])

  useEffect(() => { fetch(); fetchStats(); fetchProcesses() }, [])

  // 知识推荐：监听标题变化, 匹配知识库文章
  useEffect(() => {
    if (!form.title || form.title.length < 2) { setKnowledgeRecs([]); return }
    const t = setTimeout(async () => {
      try {
        const data = await apiFetch<any[]>('/knowledge?search=' + encodeURIComponent(form.title))
        setKnowledgeRecs((data || []).slice(0, 3))
      } catch { setKnowledgeRecs([]) }
    }, 300)
    return () => clearTimeout(t)
  }, [form.title])

  const handleCreate = async () => {
    await create({ ...form, ci_ids: ciIds })
    setDialogOpen(false); setCiIds([])
    setForm({ title: '', description: '', risk_level: 'low', change_plan: '', rollback_plan: '', created_by: 'admin', process_id: '' })
    fetchStats()
  }

  const handleSelectTicket = async (ticket: Ticket) => {
    setSelectedTicket(ticket)
    setDetailTab('detail')
    try {
      const events = await getEvents(ticket.id)
      setTicketEvents(events || [])
    } catch { setTicketEvents([]) }
    fetchComments(ticket.id)
    fetchAttachments(ticket.id)
  }

  const handleApprove = async () => {
    if (!selectedTicket) return
    await approve(selectedTicket.id, approveComment)
    setApproveDialog(false); setApproveComment('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleReject = async () => {
    if (!selectedTicket) return
    await reject(selectedTicket.id, rejectReason)
    setRejectDialog(false); setRejectReason('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleExecute = async () => {
    if (!selectedTicket) return
    await execute(selectedTicket.id)
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleComplete = async () => {
    if (!selectedTicket) return
    await complete(selectedTicket.id, completeComment)
    setCompleteDialog(false); setCompleteComment('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleRollback = async () => {
    if (!selectedTicket) return
    await rollback(selectedTicket.id, rollbackReason)
    setRollbackDialog(false); setRollbackReason('')
    await handleSelectTicket(await getTicket(selectedTicket.id))
    fetchStats()
  }

  const handleConvertToKnowledge = async (ticket: Ticket) => {
    const content = [
      '## 问题描述', ticket.description || '',
      ticket.change_plan ? '## 变更方案\n' + ticket.change_plan : '',
      ticket.rollback_plan ? '## 回滚方案\n' + ticket.rollback_plan : '',
      '## 处理结果',
      '- 风险等级: ' + ticket.risk_level,
      '- 处理人: ' + (ticket.completed_by || ticket.created_by),
      ticket.completed_at ? '- 完成时间: ' + formatTime(ticket.completed_at) : '',
    ].filter(Boolean).join('\n\n')
    try {
      await apiFetch('/knowledge', { method: 'POST', body: JSON.stringify({ title: ticket.title + ' - 处理总结', content, category: '故障处理', tags: [ticket.risk_level, '工单转换'], author: 'admin' }) })
      router.push('/knowledge')
    } catch (e: any) { alert('转换失败: ' + e.message) }
  }

  const handleAddComment = async () => {
    if (!selectedTicket || !commentContent.trim()) return
    setCommentLoading(true)
    try {
      await apiFetch<Comment>(`/itsm/tickets/${selectedTicket.id}/comments`, {
        method: 'POST',
        body: JSON.stringify({ author: commentAuthor, content: commentContent }),
      })
      setCommentContent('')
      fetchComments(selectedTicket.id)
    } catch { /* ignore */ }
    setCommentLoading(false)
  }

  const handleDeleteComment = async (cid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/comments/${cid}`, { method: 'DELETE' })
      fetchComments(selectedTicket.id)
    } catch { /* ignore */ }
  }

  const handleUploadFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file || !selectedTicket) return
    setUploading(true)
    try {
      const formData = new FormData()
      formData.append('file', file)
      formData.append('uploaded_by', commentAuthor)
      await apiFetch<Attachment>(`/itsm/tickets/${selectedTicket.id}/attachments`, {
        method: 'POST',
        headers: {},
        body: formData,
      })
      fetchAttachments(selectedTicket.id)
    } catch { /* ignore */ }
    setUploading(false)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleDeleteAttachment = async (aid: number) => {
    if (!selectedTicket) return
    try {
      await apiFetch(`/itsm/tickets/${selectedTicket.id}/attachments/${aid}`, { method: 'DELETE' })
      fetchAttachments(selectedTicket.id)
    } catch { /* ignore */ }
  }

  const addCiId = () => {
    if (ciIdInput.trim() && !ciIds.includes(ciIdInput.trim())) {
      setCiIds([...ciIds, ciIdInput.trim()]); setCiIdInput('')
    }
  }

  const handleImportCSV = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImporting(true)
    try {
      const formData = new FormData()
      formData.append('file', file)
      const result = await apiFetch<{ total: number; success: number; errors: { row: number; message: string }[] }>('/itsm/tickets/import', {
        method: 'POST',
        headers: {},
        body: formData,
      })
      setImportResult(result)
      setImportDialog(true)
      fetch()
      fetchStats()
    } catch { /* ignore */ }
    setImporting(false)
    if (importFileRef.current) importFileRef.current.value = ''
  }

  const handleExportTickets = () => {
    window.open('/api/v1/itsm/tickets/export/csv', '_blank')
  }

  const handleExportStats = () => {
    window.open('/api/v1/itsm/stats/export/csv', '_blank')
  }

  const isOverdue = (ticket: Ticket) => {
    if (!ticket.deadline_at || ticket.status === 'succeeded' || ticket.status === 'rejected' || ticket.status === 'rolled_back') return false
    return new Date(ticket.deadline_at) < new Date()
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">ITSM 工单</h1><p className="text-sm text-muted-foreground">变更审批与风险控制</p></div>
        <div className="flex gap-2">
          <input ref={importFileRef} type="file" accept=".csv" className="hidden" onChange={handleImportCSV} />
          <Button variant="outline" onClick={() => importFileRef.current?.click()} disabled={importing}><FileUp className="h-4 w-4" />{importing ? '导入中...' : '批量导入'}</Button>
          <Button variant="outline" onClick={handleExportTickets}><FileDown className="h-4 w-4" />导出工单</Button>
          <Button variant="outline" onClick={handleExportStats}><FileDown className="h-4 w-4" />导出统计</Button>
          <Button onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4" />新建工单</Button>
        </div>
      </div>

      {stats && (
        <div className="grid grid-cols-6 gap-3">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">总工单</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">审批通过率</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{(stats.approval_rate * 100).toFixed(1)}%</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">平均审批</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_approval_ms > 0 ? `${(stats.avg_approval_ms / 1000).toFixed(1)}秒` : '-'}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">平均执行</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_execution_ms > 0 ? `${(stats.avg_execution_ms / 1000).toFixed(1)}秒` : '-'}</span></CardContent>
          </Card>
          <Card className="col-span-2">
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">状态分布</CardTitle></CardHeader>
            <CardContent className="pt-0">
              <div className="flex gap-2 flex-wrap">
                {Object.entries(stats.by_status).filter(([,v]) => v > 0).map(([k, v]) => (
                  <Badge key={k} variant={statusVariant(k)} className="text-xs">{statusLabels[k] || k}: {v}</Badge>
                ))}
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-1">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm">工单列表</CardTitle></CardHeader>
            <CardContent className="p-0">
              {loading ? <p className="px-4 text-sm text-muted-foreground">加载中...</p> : tickets.length === 0 ? <p className="px-4 text-sm text-muted-foreground">暂无工单。</p> : (
                <div className="flex flex-col">
                  {tickets.map(t => (
                    <div key={t.id} onClick={() => handleSelectTicket(t)}
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent transition-colors ${selectedTicket?.id === t.id ? 'bg-accent border-l-2 border-l-emerald-500' : ''} ${isOverdue(t) ? 'border-l-2 border-l-destructive' : ''}`}>
                      <div className="flex items-center justify-between">
                        <span className="font-medium text-sm truncate">{t.title}</span>
                        <ChevronRight className="h-3 w-3 text-muted-foreground shrink-0" />
                      </div>
                      <div className="flex gap-1.5 mt-1.5 flex-wrap">
                        <Badge variant={riskBadge[t.risk_level] || 'secondary'} className="text-xs">{riskLabels[t.risk_level] || t.risk_level}</Badge>
                        <Badge variant={statusVariant(t.status)} className="text-xs">{statusLabels[t.status] || t.status}</Badge>
                        {isOverdue(t) && <Badge variant="destructive" className="text-xs"><Clock className="h-3 w-3 mr-0.5" />SLA 逾期</Badge>}
                      </div>
                      <p className="text-xs text-muted-foreground mt-1">{t.created_by} · {formatTime(t.created_at)}</p>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>

        <div className="lg:col-span-2">
          {selectedTicket ? (
            <Card>
              <CardHeader className="py-3">
                <div className="flex items-center justify-between">
                  <div>
                    <CardTitle className="text-lg">{selectedTicket.title}</CardTitle>
                    <CardDescription className="text-xs">ID: {selectedTicket.id.slice(0, 12)}... · {selectedTicket.created_by} · {formatTime(selectedTicket.created_at)}</CardDescription>
                  </div>
                  <div className="flex gap-1">
                    {selectedTicket.status === 'pending_review' && (
                      <><Button variant="outline" size="sm" onClick={() => { setApproveDialog(true); setApproveComment('') }}><Check className="h-3 w-3" />通过</Button>
                        <Button variant="outline" size="sm" onClick={() => { setRejectDialog(true); setRejectReason('') }}><X className="h-3 w-3" />驳回</Button></>
                    )}
                    {selectedTicket.status === 'approved' && (
                      <Button variant="outline" size="sm" onClick={handleExecute}><Play className="h-3 w-3" />执行</Button>
                    )}
                    {selectedTicket.status === 'executing' && (
                      <Button variant="outline" size="sm" onClick={() => { setCompleteDialog(true); setCompleteComment('') }}>完成</Button>
                    )}
                    {(selectedTicket.status === 'failed' || selectedTicket.status === 'succeeded') && (
                      <Button variant="outline" size="sm" onClick={() => { setRollbackDialog(true); setRollbackReason('') }}><RotateCcw className="h-3 w-3" />回滚</Button>
                    )}
                    {selectedTicket.status === 'succeeded' && (
                      <Button variant="outline" size="sm" onClick={() => handleConvertToKnowledge(selectedTicket)}><BookOpen className="h-3 w-3 mr-1"/>转知识</Button>
                    )}
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-3 gap-2 text-sm mb-3">
                  <div><span className="text-muted-foreground">风险:</span> <Badge variant={riskBadge[selectedTicket.risk_level]} className="text-xs">{riskLabels[selectedTicket.risk_level]}</Badge></div>
                  <div><span className="text-muted-foreground">状态:</span> <Badge variant={statusVariant(selectedTicket.status)} className="text-xs">{statusLabels[selectedTicket.status]}</Badge></div>
                  <div><span className="text-muted-foreground">流程:</span> {selectedTicket.process_id ? <Badge variant="outline" className="text-xs">{processes.find(p => p.id === selectedTicket.process_id)?.name || selectedTicket.process_id.slice(0, 8)}</Badge> : '无'}</div>
                  <div><span className="text-muted-foreground">SLA:</span> <span className={isOverdue(selectedTicket) ? 'text-destructive font-medium' : ''}>{SLA_BY_RISK[selectedTicket.risk_level] || '72小时'}{isOverdue(selectedTicket) && ' 已逾期'}</span></div>
                  {selectedTicket.deadline_at && <div className="col-span-3"><span className="text-muted-foreground">截止:</span> <Clock className="h-3 w-3 inline mr-1" />{formatTime(selectedTicket.deadline_at)}</div>}
                  {selectedTicket.scheduled_at && <div className="col-span-3"><span className="text-muted-foreground">计划执行:</span> {formatTime(selectedTicket.scheduled_at)}</div>}
                  {selectedTicket.approved_by && <div><span className="text-muted-foreground">审批人:</span> {selectedTicket.approved_by}</div>}
                  {selectedTicket.approved_at && <div><span className="text-muted-foreground">审批时间:</span> {formatTime(selectedTicket.approved_at)}</div>}
                  {selectedTicket.reject_reason && <div className="col-span-3"><span className="text-muted-foreground">驳回原因:</span> <span className="text-destructive">{selectedTicket.reject_reason}</span></div>}
                  {selectedTicket.completed_by && <div><span className="text-muted-foreground">完成人:</span> {selectedTicket.completed_by}</div>}
                  {selectedTicket.completed_at && <div><span className="text-muted-foreground">完成时间:</span> {formatTime(selectedTicket.completed_at)}</div>}
                  {selectedTicket.rolled_back_by && <div><span className="text-muted-foreground">回滚人:</span> {selectedTicket.rolled_back_by}</div>}
                  {selectedTicket.rollback_reason && <div className="col-span-3"><span className="text-muted-foreground">回滚原因:</span> <span className="text-amber-400">{selectedTicket.rollback_reason}</span></div>}
                </div>

                <Tabs value={detailTab} onValueChange={setDetailTab}>
                  <TabsList>
                    <TabsTrigger value="detail"><FileText className="w-3 h-3 mr-1" />详情</TabsTrigger>
                    <TabsTrigger value="events"><Activity className="w-3 h-3 mr-1" />事件 ({ticketEvents.length})</TabsTrigger>
                    <TabsTrigger value="comments"><MessageSquare className="w-3 h-3 mr-1" />评论 ({comments.length})</TabsTrigger>
                    <TabsTrigger value="attachments"><Paperclip className="w-3 h-3 mr-1" />附件 ({attachments.length})</TabsTrigger>
                  </TabsList>
                  <TabsContent value="detail" className="mt-3 space-y-3">
                    {selectedTicket.description && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">描述</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.description}</p>
                      </div>
                    )}
                    {selectedTicket.change_plan && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">变更计划</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.change_plan}</p>
                      </div>
                    )}
                    {selectedTicket.rollback_plan && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">回滚计划</h4>
                        <p className="text-sm text-muted-foreground whitespace-pre-wrap border rounded-md p-3 bg-muted/30">{selectedTicket.rollback_plan}</p>
                      </div>
                    )}
                    {(selectedTicket.ci_ids || []).length > 0 && (
                      <div>
                        <h4 className="text-sm font-medium mb-1">关联资产</h4>
                        <div className="flex gap-1 flex-wrap">
                          {(selectedTicket.ci_ids || []).map(ciId => (
                            <Badge key={ciId} variant="outline" className="text-xs">{ciId.slice(0, 12)}...</Badge>
                          ))}
                        </div>
                      </div>
                    )}
                  </TabsContent>
                  <TabsContent value="events" className="mt-3">
                    {ticketEvents.length === 0 ? <p className="text-sm text-muted-foreground">无变更事件。</p> : (
                      <div className="space-y-0">
                        {ticketEvents.map((ev, i) => (
                          <div key={ev.id || i} className="flex items-start gap-3 py-2 border-b last:border-0 text-sm">
                            <div className="flex flex-col items-center shrink-0">
                              <div className={`w-2 h-2 rounded-full ${i === ticketEvents.length - 1 ? 'bg-emerald-500' : 'bg-muted-foreground/30'}`} />
                              {i < ticketEvents.length - 1 && <div className="w-px h-full bg-muted-foreground/20 mt-1" />}
                            </div>
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <Badge variant={statusVariant(ev.to_status)} className="text-xs">{statusLabels[ev.to_status] || ev.to_status}</Badge>
                                {ev.from_status && <span className="text-xs text-muted-foreground">从 {statusLabels[ev.from_status] || ev.from_status}</span>}
                              </div>
                              <p className="text-xs text-muted-foreground mt-0.5">{ev.changed_by} · {formatTime(ev.created_at)}</p>
                              {ev.comment && <p className="text-xs mt-1 text-muted-foreground italic">{ev.comment}</p>}
                            </div>
                          </div>
                        ))}
                      </div>
                    )}
                  </TabsContent>
                  <TabsContent value="comments" className="mt-3">
                    <div className="space-y-3">
                      <div className="border rounded-md p-3 bg-muted/30 space-y-2">
                        <div className="flex gap-2 items-center">
                          <label className="text-xs text-muted-foreground shrink-0">用户:</label>
                          <Input className="h-8 text-xs" value={commentAuthor} onChange={e => setCommentAuthor(e.target.value)} placeholder="用户名" />
                        </div>
                        <textarea
                          className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none"
                          value={commentContent}
                          onChange={e => setCommentContent(e.target.value)}
                          placeholder="输入评论内容..."
                          onKeyDown={e => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) handleAddComment() }}
                        />
                        <div className="flex justify-end">
                          <Button size="sm" onClick={handleAddComment} disabled={!commentContent.trim() || commentLoading}>
                            {commentLoading ? '发送中...' : '发送评论'}
                          </Button>
                        </div>
                      </div>
                      {comments.length === 0 ? (
                        <p className="text-sm text-muted-foreground text-center py-4">暂无评论</p>
                      ) : (
                        <div className="space-y-2">
                          {comments.map(c => (
                            <div key={c.id} className="border rounded-md p-3 text-sm group">
                              <div className="flex items-center justify-between">
                                <div className="flex items-center gap-2">
                                  <Badge variant="secondary" className="text-xs">{c.author}</Badge>
                                  <span className="text-xs text-muted-foreground">{formatTime(c.created_at)}</span>
                                </div>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="h-6 w-6 p-0 opacity-0 group-hover:opacity-100 transition-opacity"
                                  onClick={() => handleDeleteComment(c.id)}
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                              <p className="mt-1 text-sm whitespace-pre-wrap">{c.content}</p>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </TabsContent>
                  <TabsContent value="attachments" className="mt-3">
                    <div className="space-y-3">
                      <div className="flex items-center gap-2">
                        <input
                          ref={fileInputRef}
                          type="file"
                          className="hidden"
                          onChange={handleUploadFile}
                        />
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => fileInputRef.current?.click()}
                          disabled={uploading}
                        >
                          <Upload className="h-3 w-3 mr-1" />
                          {uploading ? '上传中...' : '上传附件'}
                        </Button>
                      </div>
                      {attachments.length === 0 ? (
                        <p className="text-sm text-muted-foreground text-center py-4">暂无附件</p>
                      ) : (
                        <div className="space-y-1">
                          {attachments.map(a => (
                            <div key={a.id} className="flex items-center justify-between border rounded-md px-3 py-2 text-sm group">
                              <div className="flex items-center gap-2 min-w-0">
                                <Paperclip className="h-3 w-3 text-muted-foreground shrink-0" />
                                <span className="truncate">{a.file_name}</span>
                                <span className="text-xs text-muted-foreground shrink-0">({formatFileSize(a.file_size)})</span>
                                <span className="text-xs text-muted-foreground shrink-0">{a.uploaded_by}</span>
                              </div>
                              <div className="flex items-center gap-1 shrink-0">
                                <a
                                  href={`/api/v1/itsm/tickets/${selectedTicket.id}/attachments/${a.id}`}
                                  download={a.file_name}
                                  className="inline-flex items-center justify-center h-7 w-7 rounded-md hover:bg-accent transition-colors"
                                >
                                  <Download className="h-3 w-3" />
                                </a>
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  className="h-7 w-7 p-0 opacity-0 group-hover:opacity-100 transition-opacity"
                                  onClick={() => handleDeleteAttachment(a.id)}
                                >
                                  <Trash2 className="h-3 w-3" />
                                </Button>
                              </div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </TabsContent>
                </Tabs>
              </CardContent>
            </Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]">
              <CardContent className="text-center text-muted-foreground">
                <p>选择一个工单查看详情</p>
              </CardContent>
            </Card>
          )}
        </div>
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建工单">
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          <div><label className="text-sm font-medium">标题 *</label><Input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))} placeholder="变更描述" /></div>
          <div><label className="text-sm font-medium">描述</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">风险等级</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.risk_level} onChange={e => setForm(f => ({ ...f, risk_level: e.target.value }))}>
                <option value="low">Low - 数据只读</option><option value="medium">Medium - 单机无状态</option><option value="high">High - 核心配置修改</option><option value="critical">Critical - 数据库变更</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">流程模板</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.process_id} onChange={e => setForm(f => ({ ...f, process_id: e.target.value }))}>
                <option value="">无流程模板</option>
                {processes.filter(p => p.enabled).map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
              </select>
            </div>
          </div>
          <div><label className="text-sm font-medium">变更计划</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.change_plan} onChange={e => setForm(f => ({ ...f, change_plan: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">回滚计划</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.rollback_plan} onChange={e => setForm(f => ({ ...f, rollback_plan: e.target.value }))} /></div>
          <div>
            <label className="text-sm font-medium">关联资产 CI</label>
            <div className="flex gap-1 flex-wrap mb-1">
              {ciIds.map(id => <Badge key={id} variant="outline" className="text-xs cursor-pointer" onClick={() => setCiIds(ciIds.filter(c => c !== id))}>{id.slice(0, 12)}... ×</Badge>)}
            </div>
            <div className="flex gap-2">
              <Input className="text-xs" value={ciIdInput} onChange={e => setCiIdInput(e.target.value)} placeholder="输入 CI ID" onKeyDown={e => e.key === 'Enter' && addCiId()} />
              <Button variant="outline" size="sm" onClick={addCiId}>添加</Button>
            </div>
          </div>
          {knowledgeRecs.length > 0 && <div className="mt-2 p-2 bg-amber-50 dark:bg-amber-950/20 rounded-md">
            <p className="text-xs font-medium text-amber-700 dark:text-amber-400 mb-1">相关文章推荐</p>
            {knowledgeRecs.map(a => <div key={a.id} className="text-[11px] text-muted-foreground hover:text-foreground cursor-pointer truncate" onClick={e => { e.stopPropagation(); window.open('/knowledge/' + a.id, '_blank') }}>· {a.title}</div>)}
          </div>}
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleCreate} disabled={!form.title}>提交</Button></DialogFooter>
      </Dialog>

      <Dialog open={approveDialog} onClose={() => setApproveDialog(false)} title="审批通过">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">审批意见</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={approveComment} onChange={e => setApproveComment(e.target.value)} placeholder="审批意见（可选）" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setApproveDialog(false)}>取消</Button><Button onClick={handleApprove}>通过</Button></DialogFooter>
      </Dialog>

      <Dialog open={rejectDialog} onClose={() => setRejectDialog(false)} title="驳回工单">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">驳回原因 *</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={rejectReason} onChange={e => setRejectReason(e.target.value)} placeholder="请填写驳回原因" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRejectDialog(false)}>取消</Button><Button onClick={handleReject} disabled={!rejectReason}>驳回</Button></DialogFooter>
      </Dialog>

      <Dialog open={completeDialog} onClose={() => setCompleteDialog(false)} title="完成执行">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">完成备注</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={completeComment} onChange={e => setCompleteComment(e.target.value)} placeholder="完成备注（可选）" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setCompleteDialog(false)}>取消</Button><Button onClick={handleComplete}>确认完成</Button></DialogFooter>
      </Dialog>

      <Dialog open={rollbackDialog} onClose={() => setRollbackDialog(false)} title="回滚工单">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">回滚原因</label><textarea className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={rollbackReason} onChange={e => setRollbackReason(e.target.value)} placeholder="请填写回滚原因（可选）" /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRollbackDialog(false)}>取消</Button><Button onClick={handleRollback}>确认回滚</Button></DialogFooter>
      </Dialog>

      <Dialog open={importDialog} onClose={() => setImportDialog(false)} title="导入结果">
        {importResult && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><span className="text-muted-foreground">总行数:</span> {importResult.total}</div>
              <div><span className="text-muted-foreground">成功:</span> <span className="text-emerald-600 font-medium">{importResult.success}</span></div>
            </div>
            {importResult.errors.length > 0 && (
              <div>
                <h4 className="text-sm font-medium text-destructive mb-1">失败记录 ({importResult.errors.length})</h4>
                <div className="max-h-40 overflow-y-auto space-y-1">
                  {importResult.errors.map((err, i) => (
                    <div key={i} className="text-xs border rounded px-2 py-1">
                      <span className="text-muted-foreground">行 {err.row}:</span> {err.message}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
        <DialogFooter><Button onClick={() => setImportDialog(false)}>关闭</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
