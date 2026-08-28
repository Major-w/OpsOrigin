
/**
 * 监控中心 · 监控采集
 *
 * 从原「自动化运维 → 数据采集」拆分而来：监控类采集不再与 CMDB 采集混放。
 *   - 调度采集：kind=monitor 的周期采集任务（仍由 CMDB 调度器统一驱动，避免两套调度器）
 *   - 脚本采集：type=metric 的 Agent 脚本采集，输出指标数组
 *   - 指标流水：最近上报的指标点，用于快速验证采集链路是否通
 *   - 采集能力：monitor 类采集器注册矩阵
 */

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tabs } from 'antd'
import { ScheduledTaskPanel } from '@/components/collection/scheduled-task-panel'
import { ScriptCollectPanel } from '@/components/collection/script-collect-panel'
import { CollectorMatrix } from '@/components/collection/collector-matrix'
import { apiList, cn, formatTime } from '@/lib/utils'
import { Activity, Cpu, Gauge, RefreshCw, Terminal } from 'lucide-react'

interface CollectMetric {
  name: string
  value: number
  unit?: string
  agent_id?: string
  collected_at: string
}

function MetricStreamPanel() {
  const [metrics, setMetrics] = useState<CollectMetric[]>([])
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try { setMetrics(await apiList<CollectMetric>('/collect-metrics?limit=200')) } catch { setMetrics([]) }
    setLoading(false)
  }

  useEffect(() => { load() }, [])

  return (
    <Card>
      <CardHeader className="pb-2 flex-row items-center justify-between space-y-0">
        <CardTitle className="text-base flex items-center gap-2">
          <Gauge className="h-4 w-4 text-blue-500" />指标流水（最近 {metrics.length} 条）
        </CardTitle>
        <Button onClick={load} disabled={loading}>
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        {metrics.length === 0 ? (
          <p className="py-10 text-sm text-muted-foreground text-center">暂无指标数据，等待 metric 类型采集任务执行</p>
        ) : (
          <div className="max-h-[480px] overflow-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b text-left text-muted-foreground sticky top-0 bg-background">
                <th className="pb-2 px-4 font-medium">指标名</th>
                <th className="pb-2 px-4 font-medium">值</th>
                <th className="pb-2 px-4 font-medium">单位</th>
                <th className="pb-2 px-4 font-medium">Agent</th>
                <th className="pb-2 px-4 font-medium">采集时间</th>
              </tr></thead>
              <tbody>
                {metrics.map((m, i) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="py-2 px-4 font-mono text-xs">{m.name}</td>
                    <td className="py-2 px-4 font-medium">{m.value}</td>
                    <td className="py-2 px-4 text-muted-foreground text-xs">{m.unit || ''}</td>
                    <td className="py-2 px-4 text-muted-foreground text-xs">{m.agent_id?.slice(0, 12)}</td>
                    <td className="py-2 px-4 text-muted-foreground text-xs">{formatTime(m.collected_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}

export default function MonitorCollectionPage() {
  const [tab, setTab] = useState('scheduled')

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Activity className="h-6 w-6 text-blue-500" />监控采集
        </h1>
        <p className="text-sm text-muted-foreground">
          监控指标的采集入口：周期调度、脚本上报与指标流水。配置项数据采集请前往
          <Link to="/cmdb/auto-collect" className="ml-1 text-blue-600 dark:text-blue-400 hover:underline">
            CMDB · 自动采集
          </Link>
          。
        </p>
      </div>

      <Tabs activeKey={tab} onChange={setTab} items={[
        { key: 'scheduled', label: <span><Activity className="h-3.5 w-3.5 mr-1.5 inline" />调度采集</span>, children: <div className="mt-4"><ScheduledTaskPanel kind="monitor" /></div> },
        { key: 'script', label: <span><Terminal className="h-3.5 w-3.5 mr-1.5 inline" />脚本采集</span>, children: <div className="mt-4"><ScriptCollectPanel type="metric" /></div> },
        { key: 'metrics', label: <span><Gauge className="h-3.5 w-3.5 mr-1.5 inline" />指标流水</span>, children: <div className="mt-4"><MetricStreamPanel /></div> },
        { key: 'collectors', label: <span><Cpu className="h-3.5 w-3.5 mr-1.5 inline" />采集能力</span>, children: <div className="mt-4"><CollectorMatrix kind="monitor" /></div> },
      ]} />
    </div>
  )
}
