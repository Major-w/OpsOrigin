
import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useTicketStore } from '@/stores/itsm/tickets'
import type { TicketEvent } from '@/stores/itsm/types'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag, Button } from 'antd'
import { formatTime } from '@/lib/utils'
import {
  ArrowLeft,
  Activity,
  Clock,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  RotateCcw,
} from 'lucide-react'
import { STATUS_LABELS } from '@/lib/itsm-constants'

const positiveStatuses = new Set(['executing', 'succeeded', 'closed', 'approved'])
const negativeStatuses = new Set(['failed', 'rejected', 'rolled_back'])
const warningKeywords = ['sla', 'breach', 'warning', 'timeout', 'escalat']

function getEventSeverity(e: TicketEvent): 'positive' | 'negative' | 'warning' {
  const comment = (e.comment || '').toLowerCase()
  if (warningKeywords.some((k) => comment.includes(k))) return 'warning'
  if (negativeStatuses.has(e.to_status)) return 'negative'
  if (positiveStatuses.has(e.to_status)) return 'positive'
  return 'positive'
}

function EventIcon({ severity }: { severity: 'positive' | 'negative' | 'warning' }) {
  if (severity === 'positive')
    return <CheckCircle2 className="h-5 w-5 text-blue-500" />
  if (severity === 'negative')
    return <XCircle className="h-5 w-5 text-red-500" />
  return <AlertTriangle className="h-5 w-5 text-yellow-500" />
}

function lineColor(severity: 'positive' | 'negative' | 'warning') {
  if (severity === 'positive') return 'bg-blue-500'
  if (severity === 'negative') return 'bg-red-500'
  return 'bg-yellow-500'
}

function dotColor(severity: 'positive' | 'negative' | 'warning') {
  if (severity === 'positive') return 'bg-blue-500'
  if (severity === 'negative') return 'bg-red-500'
  return 'bg-yellow-500'
}

export default function SLALogPage() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const getEvents = useTicketStore((s) => s.getEvents)
  const getTicket = useTicketStore((s) => s.getTicket)

  const [events, setEvents] = useState<TicketEvent[]>([])
  const [ticketTitle, setTicketTitle] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (!id) return
    setLoading(true)
    Promise.all([getEvents(id), getTicket(id)])
      .then(([evts, ticket]) => {
        const filtered = evts.filter(
          (e) =>
            e.comment?.toLowerCase().includes('sla') ||
            e.from_status !== e.to_status
        )
        setEvents(filtered)
        if (ticket) setTicketTitle(ticket.title || id)
      })
      .finally(() => setLoading(false))
  }, [id, getEvents, getTicket])

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center gap-4">
        <Button
          type="text"
          onClick={() => navigate(-1)}
          className="shrink-0"
        >
          <ArrowLeft className="h-5 w-5" />
        </Button>
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Activity className="h-6 w-6 text-blue-500" />
            SLA 事件日志
          </h1>
          {ticketTitle && (
            <p className="text-muted-foreground text-sm mt-1">{ticketTitle}</p>
          )}
        </div>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            <Clock className="h-4 w-4" />
            事件时间线
            <Tag color="default" className="ml-auto">
              {events.length} 条记录
            </Tag>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-12 text-muted-foreground">
              <RotateCcw className="h-5 w-5 animate-spin mr-2" />
              加载中...
            </div>
          ) : events.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
              <Activity className="h-12 w-12 mb-4 opacity-30" />
              <p className="text-lg font-medium">暂无 SLA 事件</p>
              <p className="text-sm mt-1">该工单当前没有 SLA 相关的状态变更记录</p>
            </div>
          ) : (
            <div className="relative">
              <div className="absolute left-[19px] top-0 bottom-0 w-px bg-border" />

              <div className="space-y-0">
                {events.map((event) => {
                  const severity = getEventSeverity(event)
                  return (
                    <div key={event.id} className="relative flex gap-4 group">
                      <div
                        className={`relative z-10 flex h-10 w-10 shrink-0 items-center justify-center rounded-full border-2 border-background ${dotColor(severity)}`}
                      >
                        <EventIcon severity={severity} />
                      </div>

                      <div className="flex-1 pb-8 pt-1">
                        <div className="flex flex-col sm:flex-row sm:items-center gap-1 sm:gap-3">
                          <span className="font-mono text-sm text-muted-foreground">
                            {formatTime(event.created_at)}
                          </span>
                          <div className="flex items-center gap-1.5 text-sm">
                            <Tag color="default" className="text-xs">
                              {STATUS_LABELS[event.from_status] || event.from_status}
                            </Tag>
                            <span className="text-muted-foreground">→</span>
                            <Tag
                              color={
                                severity === 'negative'
                                  ? 'error'
                                  : severity === 'warning'
                                    ? 'warning'
                                    : 'success'
                              }
                              className="text-xs"
                            >
                              {STATUS_LABELS[event.to_status] || event.to_status}
                            </Tag>
                          </div>
                        </div>

                        <div className="mt-1.5 flex items-center gap-2 text-xs text-muted-foreground">
                          <span>操作人: {event.changed_by}</span>
                        </div>

                        {event.comment && (
                          <div className="mt-2 rounded-md bg-muted/50 px-3 py-2 text-sm text-muted-foreground">
                            {event.comment}
                          </div>
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}