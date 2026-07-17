'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Scan, Play, Clock, CheckCircle, XCircle, RefreshCw, Plus } from 'lucide-react'

export default function AutoInspectionPage() {
  const [tasks] = useState([
    { id: 'INS-001', name: '每日系统巡检', targets: 45, lastRun: '2026-07-15 08:00', status: 'success', duration: '12m' },
    { id: 'INS-002', name: '安全合规巡检', targets: 32, lastRun: '2026-07-14 22:00', status: 'success', duration: '18m' },
    { id: 'INS-003', name: '性能基线巡检', targets: 28, lastRun: '2026-07-14 06:00', status: 'partial', duration: '15m' },
    { id: 'INS-004', name: '网络连通性巡检', targets: 50, lastRun: '2026-07-15 07:30', status: 'success', duration: '5m' },
  ])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">自动化巡检</h1>
          <p className="text-sm text-muted-foreground">定时自动巡检主机健康状态</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm"><RefreshCw className="h-3 w-3 mr-1" />刷新</Button>
          <Button size="sm"><Plus className="h-4 w-4 mr-1" />新建巡检</Button>
        </div>
      </div>

      <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
        {[
          { label: '巡检任务', value: tasks.length, color: 'text-blue-500' },
          { label: '覆盖主机', value: 155, color: 'text-emerald-500' },
          { label: '今日完成', value: 8, color: 'text-violet-500' },
          { label: '异常发现', value: 3, color: 'text-amber-500' },
        ].map((item, i) => (
          <Card key={i}>
            <CardContent className="p-4">
              <p className="text-xs text-muted-foreground">{item.label}</p>
              <p className={`metric-value text-2xl ${item.color} mt-1`}>{item.value}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">巡检任务列表</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {tasks.map((t) => (
            <div key={t.id} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
              <div className="flex items-center gap-3">
                <Scan className="h-4 w-4 text-blue-500" />
                <div>
                  <span className="text-xs font-mono text-blue-500">{t.id}</span>
                  <span className="text-xs text-gray-900 dark:text-white ml-2">{t.name}</span>
                </div>
              </div>
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>{t.targets} 台主机</span>
                <span>{t.duration}</span>
                <Badge variant={t.status === 'success' ? 'success' : t.status === 'partial' ? 'warning' : 'destructive'}>
                  {t.status === 'success' ? '成功' : t.status === 'partial' ? '部分通过' : '失败'}
                </Badge>
                <span className="font-mono text-[10px]">{t.lastRun}</span>
                <Button variant="ghost" size="icon" className="h-7 w-7"><Play className="h-3 w-3" /></Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
