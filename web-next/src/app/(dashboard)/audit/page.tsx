'use client'

import { useEffect } from 'react'
import { useAuditStore } from '@/stores/audit'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { formatTime } from '@/lib/utils'

export default function AuditPage() {
  const { events, loading, fetch } = useAuditStore()

  useEffect(() => { fetch() }, [fetch])

  return (
    <div className="p-6 space-y-4">
      <div><h1 className="text-2xl font-bold">审计日志</h1><p className="text-sm text-muted-foreground">全链路操作追踪与安全审计</p></div>

      <Card>
        <CardHeader><CardTitle>事件历史</CardTitle></CardHeader>
        <CardContent>
          {loading ? <p className="text-sm text-muted-foreground">加载中...</p> : events.length === 0 ? <p className="text-sm text-muted-foreground">暂无审计事件。</p> : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground"><th className="pb-2 font-medium">Trace ID</th><th className="pb-2 font-medium">事件类型</th><th className="pb-2 font-medium">操作主体</th><th className="pb-2 font-medium">目标</th><th className="pb-2 font-medium">动作</th><th className="pb-2 font-medium">结果</th><th className="pb-2 font-medium">时间</th></tr></thead>
                <tbody>{events.map(e => (
                  <tr key={e.id} className="border-b last:border-0">
                    <td className="py-2 text-xs text-muted-foreground font-mono">{e.trace_id?.slice(0, 12)}</td>
                    <td className="py-2"><Badge variant="secondary">{e.event_type}</Badge></td>
                    <td className="py-2"><Badge variant={e.actor?.type === 'HUMAN' ? 'default' : 'outline'}>{e.actor?.type}</Badge> {e.actor?.id}</td>
                    <td className="py-2 text-muted-foreground">{e.target?.type}/{e.target?.id?.slice(0, 8)}</td>
                    <td className="py-2 font-mono text-xs">{e.action}</td>
                    <td className="py-2"><Badge variant={e.success ? 'success' : 'destructive'}>{e.success ? '成功' : '失败'}</Badge></td>
                    <td className="py-2 text-muted-foreground">{formatTime(e.timestamp)}</td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
