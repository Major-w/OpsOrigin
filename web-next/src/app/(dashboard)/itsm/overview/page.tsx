'use client'

import { useEffect, useState } from 'react'
import { useITSMStore } from '@/stores/itsm'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime } from '@/lib/utils'
import { TicketCheck, Clock, CheckCircle, AlertTriangle } from 'lucide-react'

const statusLabels: Record<string, string> = { draft: '草稿', pending_review: '待审批', approved: '已审批', rejected: '已驳回', executing: '执行中', succeeded: '已完成', failed: '已失败', rolled_back: '已回滚' }

export default function ITSMOverviewPage() {
  const { tickets, stats, fetch, fetchStats } = useITSMStore()
  useEffect(() => { fetch(); fetchStats() }, [])

  return (
    <div className="p-6 space-y-4">
      <div><h1 className="text-2xl font-bold">ITSM 总览</h1><p className="text-sm text-muted-foreground">工单统计与状态概览</p></div>
      {stats && (
        <div className="grid grid-cols-4 gap-3">
          <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground"><TicketCheck className="h-4 w-4 inline mr-1" />总工单</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground"><CheckCircle className="h-4 w-4 inline mr-1" />审批通过率</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{(stats.approval_rate * 100).toFixed(1)}%</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground"><Clock className="h-4 w-4 inline mr-1" />平均审批</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_approval_ms > 0 ? `${(stats.avg_approval_ms / 1000).toFixed(1)}s` : '-'}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground"><AlertTriangle className="h-4 w-4 inline mr-1" />平均执行</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.avg_execution_ms > 0 ? `${(stats.avg_execution_ms / 1000).toFixed(1)}s` : '-'}</span></CardContent></Card>
        </div>
      )}
      <div className="grid grid-cols-2 gap-4">
        <Card><CardHeader className="py-3"><CardTitle className="text-sm">状态分布</CardTitle></CardHeader><CardContent>
          {stats && (<div className="space-y-1">{Object.entries(stats.by_status || {}).filter(([,v]) => v > 0).map(([k,v]) => (<div key={k} className="flex items-center justify-between text-sm"><Badge variant="outline" className="text-xs">{statusLabels[k] || k}</Badge><span className="font-medium">{v}</span></div>))}</div>)}
        </CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-sm">风险分布</CardTitle></CardHeader><CardContent>
          {stats && (<div className="space-y-1">{Object.entries(stats.by_risk || {}).filter(([,v]) => v > 0).map(([k,v]) => (<div key={k} className="flex items-center justify-between text-sm"><Badge variant={(k==='critical'?'destructive':k==='high'?'warning':'secondary')} className="text-xs">{k}</Badge><span className="font-medium">{v}</span></div>))}</div>)}
        </CardContent></Card>
      </div>
    </div>
  )
}
