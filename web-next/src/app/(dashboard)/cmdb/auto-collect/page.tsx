'use client'

/**
 * CMDB · 自动采集
 *
 * 把原先分散的「自动发现」「采集任务」「采集器」三个入口融合为单一页面：
 *   - 调度采集：CMDB 统一调度器持有的 kind=cmdb 周期任务（唯一真相源）
 *   - 自动发现：探针 / SNMP 主动扫描未知资产，确认后同步为配置项
 *   - 脚本采集：下发自定义脚本由 Agent 执行，回写资产字段
 *   - 采集能力：(资源类型 × 类别) 采集器注册矩阵，只读
 *
 * 监控类采集（kind=monitor / type=metric）已迁至「监控中心 → 监控采集」，两侧共用同一调度底座。
 */

import { useState } from 'react'
import Link from 'next/link'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ScheduledTaskPanel } from '@/components/collection/scheduled-task-panel'
import { ScriptCollectPanel } from '@/components/collection/script-collect-panel'
import { DiscoveryPanel } from '@/components/collection/discovery-panel'
import { CollectorMatrix } from '@/components/collection/collector-matrix'
import { Activity, Cpu, Radar, Radio, Terminal } from 'lucide-react'

export default function CMDBAutoCollectPage() {
  const [tab, setTab] = useState('scheduled')

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2">
          <Radio className="h-6 w-6 text-emerald-500" />自动采集
        </h1>
        <p className="text-sm text-muted-foreground">
          配置项数据的统一入口：周期调度、主动发现、脚本回写与采集能力矩阵。
          监控指标采集请前往
          <Link href="/monitor/collection" className="ml-1 text-emerald-600 dark:text-emerald-400 hover:underline">
            监控中心 · 监控采集
          </Link>
          。
        </p>
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="scheduled"><Activity className="h-3.5 w-3.5 mr-1.5" />调度采集</TabsTrigger>
          <TabsTrigger value="discovery"><Radar className="h-3.5 w-3.5 mr-1.5" />自动发现</TabsTrigger>
          <TabsTrigger value="script"><Terminal className="h-3.5 w-3.5 mr-1.5" />脚本采集</TabsTrigger>
          <TabsTrigger value="collectors"><Cpu className="h-3.5 w-3.5 mr-1.5" />采集能力</TabsTrigger>
        </TabsList>

        <TabsContent value="scheduled" className="mt-4"><ScheduledTaskPanel kind="cmdb" /></TabsContent>
        <TabsContent value="discovery" className="mt-4"><DiscoveryPanel /></TabsContent>
        <TabsContent value="script" className="mt-4"><ScriptCollectPanel type="cmdb" /></TabsContent>
        <TabsContent value="collectors" className="mt-4"><CollectorMatrix kind="cmdb" /></TabsContent>
      </Tabs>
    </div>
  )
}
