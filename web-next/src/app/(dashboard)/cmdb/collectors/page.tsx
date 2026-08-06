'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Database, CheckCircle2, CircleDashed } from 'lucide-react'
import { apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

interface CollectorInfo {
  kind: string
  resource_type: string
  registered: boolean
  description: string
}

const kindLabels: Record<string, string> = { cmdb: 'CMDB 采集', monitor: '监控采集' }

export default function CMDBCollectorsPage() {
  const [collectors, setCollectors] = useState<CollectorInfo[]>([])

  useEffect(() => {
    apiList<CollectorInfo>('/cmdb/collectors').then(setCollectors).catch(() => toast.error('加载采集器失败'))
  }, [])

  const byKind = collectors.reduce<Record<string, CollectorInfo[]>>((acc, c) => {
    (acc[c.kind] ||= []).push(c)
    return acc
  }, {})

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2"><Database className="h-6 w-6 text-emerald-500" />采集器</h1>
        <p className="text-sm text-muted-foreground">平台采集能力矩阵：已注册表示该采集器实现已接入并可在采集任务中调度</p>
      </div>

      {Object.entries(byKind).map(([kind, list]) => (
        <div key={kind} className="space-y-2">
          <h2 className="text-sm font-semibold text-muted-foreground">{kindLabels[kind] || kind}</h2>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {list.map(c => (
              <Card key={`${c.kind}:${c.resource_type}`}>
                <CardContent className="p-4 space-y-2">
                  <div className="flex items-center justify-between">
                    <p className="font-medium text-sm">{c.resource_type}</p>
                    {c.registered
                      ? <Badge className="text-[10px] bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"><CheckCircle2 className="h-3 w-3 mr-1" />已注册</Badge>
                      : <Badge variant="outline" className="text-[10px] text-muted-foreground"><CircleDashed className="h-3 w-3 mr-1" />待接入</Badge>}
                  </div>
                  <p className="text-xs text-muted-foreground leading-relaxed">{c.description}</p>
                </CardContent>
              </Card>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}
