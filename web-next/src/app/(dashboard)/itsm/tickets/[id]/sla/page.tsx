'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { useITSMStore, type Ticket } from '@/stores/itsm'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatTime } from '@/lib/utils'
import { Clock, AlertTriangle, CheckCircle2, ArrowLeft, Timer, User, Calendar } from 'lucide-react'
import { RISK_LABELS, RISK_BADGE_VARIANT } from '@/lib/itsm-constants'

const SLA_BY_RISK: Record<string, string> = { critical: '4小时', high: '24小时', medium: '48小时', low: '72小时' }

function formatDuration(ms: number): string {
  if (ms < 0) return '已逾期'
  const totalSec = Math.floor(ms / 1000)
  const hours = Math.floor(totalSec / 3600)
  const minutes = Math.floor((totalSec % 3600) / 60)
  const seconds = totalSec % 60
  if (hours > 0) return `${hours}小时${minutes}分钟`
  if (minutes > 0) return `${minutes}分钟${seconds}秒`
  return `${seconds}秒`
}

export default function TicketSLADetailPage() {
  const { id } = useParams<{ id: string }>()
  const router = useRouter()
  const { getTicket } = useITSMStore()
  const [ticket, setTicket] = useState<Ticket | null>(null)
  const [loading, setLoading] = useState(true)
  const [now, setNow] = useState(Date.now())

  useEffect(() => {
    if (!id) return
    setLoading(true)
    getTicket(id)
      .then(setTicket)
      .catch(() => setTicket(null))
      .finally(() => setLoading(false))
  }, [id, getTicket])

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])

  if (loading) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => router.back()}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <h1 className="text-2xl font-bold">SLA 详情</h1>
        </div>
        <div className="animate-pulse space-y-4">{[1,2,3].map(i=><div key={i} className="h-24 bg-muted rounded"/>)}</div>
      </div>
    )
  }

  if (!ticket) {
    return (
      <div className="p-6 space-y-4">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => router.back()}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <h1 className="text-2xl font-bold">SLA 详情</h1>
        </div>
        <p className="text-sm text-muted-foreground">工单不存在或加载失败。</p>
      </div>
    )
  }

  const deadline = ticket.deadline_at ? new Date(ticket.deadline_at).getTime() : null
  const createdAt = new Date(ticket.created_at).getTime()
  const remainingMs = deadline ? deadline - now : null
  const isOverdue = remainingMs !== null && remainingMs < 0
  const isCompleted = ticket.status === 'succeeded' || ticket.status === 'closed'

  // Response time progress: from created_at to deadline_at
  const responseProgress = (() => {
    if (!deadline) return null
    const total = deadline - createdAt
    if (total <= 0) return 100
    const elapsed = now - createdAt
    return Math.min(100, Math.max(0, (elapsed / total) * 100))
  })()

  // Resolution time progress: from created_at to deadline_at (or completed_at if done)
  const resolutionProgress = (() => {
    const end = isCompleted && ticket.completed_at ? new Date(ticket.completed_at).getTime() : deadline
    if (!end) return null
    const total = end - createdAt
    if (total <= 0) return 100
    const elapsed = (isCompleted && ticket.completed_at ? new Date(ticket.completed_at).getTime() : now) - createdAt
    return Math.min(100, Math.max(0, (elapsed / total) * 100))
  })()

  const timelineItems = [
    { label: '创建时间', value: ticket.created_at, icon: Calendar },
    { label: '审批时间', value: ticket.approved_at, icon: CheckCircle2 },
    { label: '执行时间', value: ticket.executed_at, icon: Timer },
    { label: '完成时间', value: ticket.completed_at, icon: CheckCircle2 },
    { label: '截止时间', value: ticket.deadline_at, icon: Clock },
  ].filter(item => item.value)

  const slaStatus = ticket.sla_breached
    ? { label: '已违规', variant: 'destructive' as const, icon: AlertTriangle }
    : ticket.sla_warning_notified
      ? { label: '即将到期', variant: 'warning' as const, icon: Clock }
      : { label: '正常', variant: 'success' as const, icon: CheckCircle2 }

  return (
    <div className="p-6 space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" onClick={() => router.back()}>
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <div>
            <h1 className="text-2xl font-bold">SLA 详情</h1>
            <p className="text-sm text-muted-foreground">工单 ID: {ticket.id}</p>
          </div>
        </div>
        <Badge variant={slaStatus.variant} className="text-sm gap-1">
          <slaStatus.icon className="h-3.5 w-3.5" />
          {slaStatus.label}
        </Badge>
      </div>

      {/* Risk & SLA Target */}
      <div className="grid grid-cols-2 gap-4">
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">风险等级</CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <div className="flex items-center gap-2">
              <Badge variant={RISK_BADGE_VARIANT[ticket.risk_level]} className="text-sm">
                {RISK_LABELS[ticket.risk_level] || ticket.risk_level}
              </Badge>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">SLA 目标</CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <span className="text-lg font-medium">{SLA_BY_RISK[ticket.risk_level] || '-'}</span>
          </CardContent>
        </Card>
      </div>

      {/* Response Time Progress */}
      {responseProgress !== null && (
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">响应时间进度</CardTitle>
          </CardHeader>
          <CardContent className="pt-0 space-y-2">
            <div className="w-full h-2 bg-muted rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full transition-all ${isOverdue ? 'bg-destructive' : responseProgress > 80 ? 'bg-yellow-500' : 'bg-emerald-500'}`}
                style={{ width: `${Math.min(100, responseProgress)}%` }}
              />
            </div>
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>已用 {formatDuration(now - createdAt)}</span>
              <span>目标 {SLA_BY_RISK[ticket.risk_level] || '-'}</span>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Resolution Time Progress */}
      {resolutionProgress !== null && (
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">解决时间进度</CardTitle>
          </CardHeader>
          <CardContent className="pt-0 space-y-2">
            <div className="w-full h-2 bg-muted rounded-full overflow-hidden">
              <div
                className={`h-full rounded-full transition-all ${isOverdue ? 'bg-destructive' : resolutionProgress > 80 ? 'bg-yellow-500' : 'bg-emerald-500'}`}
                style={{ width: `${Math.min(100, resolutionProgress)}%` }}
              />
            </div>
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>
                {isCompleted && ticket.completed_at
                  ? `完成于 ${formatTime(ticket.completed_at)}`
                  : `已用 ${formatDuration(now - createdAt)}`}
              </span>
              <span>目标 {SLA_BY_RISK[ticket.risk_level] || '-'}</span>
            </div>
          </CardContent>
        </Card>
      )}

      {/* Timeline */}
      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm text-muted-foreground">时间线</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          <div className="relative space-y-3">
            <div className="absolute left-3 top-2 bottom-2 w-px bg-border" />
            {timelineItems.map((item, i) => (
              <div key={i} className="relative flex items-start gap-3">
                <div className={`z-10 flex items-center justify-center w-6 h-6 rounded-full border-2 bg-background ${
                  item.label === '截止时间' && isOverdue ? 'border-destructive' : 'border-border'
                }`}>
                  <item.icon className="h-3 w-3 text-muted-foreground" />
                </div>
                <div className="flex-1 pt-0.5">
                  <p className="text-xs text-muted-foreground">{item.label}</p>
                  <p className={`text-sm ${item.label === '截止时间' && isOverdue ? 'text-destructive font-medium' : ''}`}>
                    {formatTime(item.value)}
                  </p>
                </div>
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* Hold Information */}
      {(ticket.on_hold || (ticket.hold_seconds || 0) > 0) && (
        <Card className="border-yellow-500/50 dark:border-yellow-400/30">
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-yellow-700 dark:text-yellow-400 flex items-center gap-2">
              <Clock className="h-4 w-4" />
              挂起信息
            </CardTitle>
          </CardHeader>
          <CardContent className="pt-0 space-y-2">
            {ticket.on_hold && (
              <div className="flex items-center gap-2">
                <Badge variant="warning">已挂起</Badge>
                {ticket.hold_reason && (
                  <span className="text-sm text-muted-foreground">{ticket.hold_reason}</span>
                )}
              </div>
            )}
            {ticket.hold_at && (
              <p className="text-xs text-muted-foreground">
                挂起时间: {formatTime(ticket.hold_at)}
              </p>
            )}
            {ticket.hold_until_at && (
              <p className="text-xs text-muted-foreground">
                预计恢复: {formatTime(ticket.hold_until_at)}
              </p>
            )}
            {(ticket.hold_seconds || 0) > 0 && (
              <p className="text-xs text-muted-foreground">
                累计挂起: {Math.floor((ticket.hold_seconds || 0) / 3600)}小时 {Math.floor(((ticket.hold_seconds || 0) % 3600) / 60)}分钟
              </p>
            )}
          </CardContent>
        </Card>
      )}

      {/* SLA Status & Escalation */}
      <div className="grid grid-cols-2 gap-4">
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">SLA 状态</CardTitle>
          </CardHeader>
          <CardContent className="pt-0 space-y-2">
            <Badge variant={slaStatus.variant} className="text-sm gap-1">
              <slaStatus.icon className="h-3.5 w-3.5" />
              {slaStatus.label}
            </Badge>
            {ticket.sla_breached && ticket.sla_breached_at && (
              <p className="text-xs text-destructive">违规时间: {formatTime(ticket.sla_breached_at)}</p>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm text-muted-foreground">升级级别</CardTitle>
          </CardHeader>
          <CardContent className="pt-0">
            <span className="text-lg font-medium">
              Level {ticket.sla_escalation_level || 0}
            </span>
          </CardContent>
        </Card>
      </div>

      {/* Remaining Time */}
      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm text-muted-foreground">剩余时间</CardTitle>
        </CardHeader>
        <CardContent className="pt-0">
          {!deadline ? (
            <span className="text-sm text-muted-foreground">未设置截止时间</span>
          ) : isCompleted ? (
            <div className="flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 text-emerald-500" />
              <span className="text-sm">已完成</span>
            </div>
          ) : isOverdue ? (
            <div className="flex items-center gap-2">
              <AlertTriangle className="h-4 w-4 text-destructive" />
              <span className="text-sm text-destructive font-medium">已逾期 {formatDuration(Math.abs(remainingMs!))}</span>
            </div>
          ) : (
            <div className="space-y-1">
              <span className="text-2xl font-bold">{formatDuration(remainingMs!)}</span>
              <p className="text-xs text-muted-foreground">
                截止于 {formatTime(ticket.deadline_at)}
              </p>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
