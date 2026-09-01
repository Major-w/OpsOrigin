
/**
 * 采集器能力矩阵：展示 (资源类型 × 采集类别) 维度上已注册的采集器实现。
 *
 * 数据来自后端注册表 /cmdb/collectors —— 采集器可插拔，cloud/agent 等模块在装配期
 * 注册各自实现，本组件只做只读透出，按 kind 切片供 CMDB / 监控两侧分别展示。
 */

import { useEffect, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { CheckCircle2, CircleDashed } from 'lucide-react'
import { apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
interface CollectorInfo {
  kind: string
  resource_type: string
  registered: boolean
  description: string
}

const RESOURCE_LABELS: Record<string, string> = {
  cloud: '云资源', host: '主机', container: '容器',
}

interface Props {
  /** 只展示该类别的采集器；不传则展示全部。 */
  kind?: 'cmdb' | 'monitor'
}

export function CollectorMatrix({ kind }: Props) {
  const [collectors, setCollectors] = useState<CollectorInfo[]>([])
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    apiList<CollectorInfo>('/cmdb/collectors')
      .then(setCollectors)
      .catch(() => getMessage().error('加载采集器失败'))
      .finally(() => setLoaded(true))
  }, [])

  const list = kind ? collectors.filter(c => c.kind === kind) : collectors

  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">
        采集能力矩阵：「已注册」表示该采集器实现已接入，可在采集任务中被调度；「待接入」为预留槽位。
      </p>
      {loaded && list.length === 0 ? (
        <Card><CardContent className="p-8 text-center text-sm text-muted-foreground">暂无已声明的采集器</CardContent></Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {list.map(c => (
            <Card key={`${c.kind}:${c.resource_type}`}>
              <CardContent className="p-4 space-y-2">
                <div className="flex items-center justify-between">
                  <p className="font-medium text-sm">{RESOURCE_LABELS[c.resource_type] || c.resource_type}</p>
                  {c.registered
                    ? <Badge className="text-[10px] bg-blue-500/15 text-blue-600 dark:text-blue-400"><CheckCircle2 className="h-3 w-3 mr-1" />已注册</Badge>
                    : <Badge variant="outline" className="text-[10px] text-muted-foreground"><CircleDashed className="h-3 w-3 mr-1" />待接入</Badge>}
                </div>
                <p className="text-xs text-muted-foreground leading-relaxed">{c.description}</p>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
