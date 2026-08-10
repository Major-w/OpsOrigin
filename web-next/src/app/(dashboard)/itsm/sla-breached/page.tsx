'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { AlertTriangle, Search, Clock, ExternalLink } from 'lucide-react'
import { apiList, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { useRouter } from 'next/navigation'
import { RISK_LABELS, STATUS_LABELS, RISK_BADGE_VARIANT } from '@/lib/itsm-constants'

interface Ticket {
  id: string
  title: string
  risk_level: string
  status: string
  created_by: string
  created_at: string
  deadline_at?: string
  sla_breached: boolean
  sla_breached_at?: string
  assignee?: string
}

export default function SLABreachedPage() {
  const router = useRouter()
  const [tickets, setTickets] = useState<Ticket[]>([])
  const [loading, setLoading] = useState(false)
  const [keyword, setKeyword] = useState('')

  const load = async () => {
    setLoading(true)
    try {
      const data = await apiList<Ticket>('/itsm/sla/breached')
      setTickets(data || [])
    } catch {
      toast.error('加载 SLA 违规列表失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const filtered = tickets.filter(t =>
    !keyword || t.title.toLowerCase().includes(keyword.toLowerCase()) ||
    t.created_by.toLowerCase().includes(keyword.toLowerCase())
  )

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">SLA 违规工单</h1>
          <p className="text-sm text-muted-foreground mt-1">已超过 SLA 截止时间的工单列表</p>
        </div>
        <Badge variant="destructive" className="text-lg px-3 py-1">
          <AlertTriangle className="w-4 h-4 mr-1" />
          {tickets.length} 个违规
        </Badge>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input
            placeholder="搜索工单标题或创建人..."
            value={keyword}
            onChange={e => setKeyword(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button variant="outline" onClick={load} disabled={loading}>
          刷新
        </Button>
      </div>

      {loading ? (
        <div className="animate-pulse space-y-4">{[1,2,3].map(i=><div key={i} className="h-24 bg-muted rounded"/>)}</div>
      ) : filtered.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Clock className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
            <p className="text-muted-foreground">暂无 SLA 违规工单</p>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {filtered.map(ticket => (
            <Card key={ticket.id} className="hover:shadow-md transition-shadow">
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 mb-1">
                      <Badge variant={RISK_BADGE_VARIANT[ticket.risk_level] || 'secondary'}>
                        {RISK_LABELS[ticket.risk_level] || ticket.risk_level}
                      </Badge>
                      <Badge variant="destructive">SLA 违规</Badge>
                    </div>
                    <h3 className="font-medium truncate">{ticket.title}</h3>
                    <div className="flex items-center gap-4 mt-2 text-sm text-muted-foreground">
                      <span>创建人: {ticket.created_by}</span>
                      <span>创建时间: {formatTime(ticket.created_at)}</span>
                      {ticket.deadline_at && (
                        <span className="text-red-500">
                          截止时间: {formatTime(ticket.deadline_at)}
                        </span>
                      )}
                      {ticket.sla_breached_at && (
                        <span className="text-red-500">
                          违规时间: {formatTime(ticket.sla_breached_at)}
                        </span>
                      )}
                    </div>
                    {ticket.assignee && (
                      <p className="text-sm text-muted-foreground mt-1">负责人: {ticket.assignee}</p>
                    )}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => router.push(`/itsm/tickets`)}
                  >
                    <ExternalLink className="w-4 h-4" />
                  </Button>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
