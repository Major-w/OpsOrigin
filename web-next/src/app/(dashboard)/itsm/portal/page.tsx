'use client'

// 员工自助门户 —— 三门户的「员工自助」形态：
// 服务目录浏览(分类卡片) + 一键申请(跳工单页自动套用目录 scope) + 我的工单(进度查询)。
import { useEffect, useState, useCallback } from 'react'
import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { apiList, formatTime } from '@/lib/utils'
import { TicketCheck, Package, ChevronRight, Loader2 } from 'lucide-react'

interface ServiceItem {
  id: string
  name: string
  description?: string
  category: string
  process_id?: string
  enabled: boolean
}

interface PortalTicket {
  id: string
  title: string
  status: string
  risk_level: string
  created_at: string
  assignee?: string
}

const statusLabels: Record<string, string> = {
  draft: '草稿', pending_review: '待审批', approved: '已审批', rejected: '已驳回',
  executing: '执行中', succeeded: '已完成', failed: '已失败', rolled_back: '已回滚',
  closed: '已关闭', withdrawn: '已撤回', reopened: '已重开',
}
const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const riskColor: Record<string, string> = { critical: 'bg-red-500', high: 'bg-amber-500', medium: 'bg-blue-500', low: 'bg-emerald-500' }
const categoryLabels: Record<string, string> = { change: '变更申请', incident: '故障报修', problem: '问题反馈', request: '服务请求' }

export default function ITSMPortalPage() {
  const router = useRouter()
  const [services, setServices] = useState<ServiceItem[]>([])
  const [tickets, setTickets] = useState<PortalTicket[]>([])
  const [loading, setLoading] = useState(true)

  // 门户只拉「已上线 且 当前用户在服务对象范围内」的目录项：
  // 过滤在服务端完成(catalog/visible)，前端拿不到越权目录，避免可见范围靠前端自觉。
  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const [svc, tks] = await Promise.all([
        apiList<ServiceItem>('/itsm/catalog/visible'),
        apiList<PortalTicket>('/itsm/tickets'),
      ])
      setServices(svc || [])
      setTickets((tks || []).slice(0, 20))
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetchAll() }, [fetchAll])

  const apply = (item: ServiceItem) => {
    router.push(`/itsm/tickets?catalog=${item.id}`)
  }

  // 分类分组
  const groups = new Map<string, ServiceItem[]>()
  for (const s of services) {
    const key = s.category || 'request'
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key)!.push(s)
  }

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">自助服务门户</h1>
          <p className="text-sm text-muted-foreground">浏览服务目录发起申请，随时查看我的工单进度</p>
        </div>
      </div>

      {/* 服务目录 */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-1.5"><Package className="h-3.5 w-3.5" />服务目录</CardTitle></CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-10 text-muted-foreground text-sm"><Loader2 className="h-4 w-4 animate-spin mr-2" />加载中…</div>
          ) : services.length === 0 ? (
            <p className="text-sm text-muted-foreground py-6 text-center">暂无可用服务，请联系管理员在「服务目录定义」中配置。</p>
          ) : (
            <div className="space-y-4">
              {Array.from(groups.entries()).map(([cat, items]) => (
                <div key={cat}>
                  <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-2">{categoryLabels[cat] || cat}</h4>
                  <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                    {items.map(item => (
                      <div key={item.id}
                        role="button" tabIndex={0}
                        onClick={() => apply(item)}
                        onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); apply(item) } }}
                        className="border rounded-lg p-4 cursor-pointer hover:border-primary/60 hover:shadow-md hover:-translate-y-0.5 transition-all duration-200 group focus:outline-none focus:ring-2 focus:ring-primary/40">
                        <div className="flex items-start justify-between">
                          <span className={`mt-1 h-2 w-2 rounded-full ${riskColor[item.category === 'change' ? 'medium' : 'low']}`} />
                          <Badge variant="outline" className="text-[10px]">{item.process_id ? '含流程' : '直通'}</Badge>
                        </div>
                        <h5 className="font-medium text-sm mt-1 group-hover:text-primary transition-colors">{item.name}</h5>
                        {item.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{item.description}</p>}
                        <Button size="sm" variant="outline" className="mt-3 w-full pointer-events-none group-hover:border-primary/60 group-hover:text-primary">
                          {item.process_id ? '发起流程' : '发起申请'} <ChevronRight className="h-3 w-3 group-hover:translate-x-0.5 transition-transform" />
                        </Button>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 我的工单 */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-1.5"><TicketCheck className="h-3.5 w-3.5" />我的工单（最近 20 条）</CardTitle></CardHeader>
        <CardContent>
          {tickets.length === 0 ? (
            <p className="text-sm text-muted-foreground py-6 text-center">暂无工单，从上方服务目录发起第一个申请吧。</p>
          ) : (
            <div className="divide-y">
              {tickets.map(t => (
                <div key={t.id} className="flex items-center justify-between py-2.5 hover:bg-accent/50 rounded px-2 cursor-pointer" onClick={() => router.push(`/itsm/tickets?focus=${t.id}`)}>
                  <div className="min-w-0">
                    <div className="text-sm truncate">{t.title}</div>
                    <div className="text-xs text-muted-foreground mt-0.5">{formatTime(t.created_at)} · {t.id.slice(0, 8)}</div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <Badge variant="outline" className="text-xs">{riskLabels[t.risk_level] || t.risk_level}</Badge>
                    <Badge variant={t.status === 'closed' || t.status === 'succeeded' ? 'success' : t.status === 'executing' ? 'secondary' : 'outline'} className="text-xs">
                      {statusLabels[t.status] || t.status}
                    </Badge>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
