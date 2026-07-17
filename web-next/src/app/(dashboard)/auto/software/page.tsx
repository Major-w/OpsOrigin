'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { PackagePlus, Play, RefreshCw, Plus, Package, CheckCircle, Clock } from 'lucide-react'

export default function SoftwareInstallPage() {
  const [tasks] = useState([
    { id: 'SW-001', name: 'Nginx 批量部署', targets: 12, completed: 12, status: 'success', software: 'nginx 1.25.4', lastRun: '2026-07-15 10:00' },
    { id: 'SW-002', name: 'Redis 集群安装', targets: 6, completed: 4, status: 'partial', software: 'redis 7.2.4', lastRun: '2026-07-14 15:00' },
    { id: 'SW-003', name: 'MySQL 主从部署', targets: 3, completed: 0, status: 'pending', software: 'mysql 8.0.36', lastRun: '待执行' },
  ])

  const [catalog] = useState([
    { name: 'Nginx', version: '1.25.4', icon: '🌐', category: 'Web服务器', installed: 12 },
    { name: 'Redis', version: '7.2.4', icon: '💾', category: '缓存', installed: 6 },
    { name: 'MySQL', version: '8.0.36', icon: '🗃️', category: '数据库', installed: 3 },
    { name: 'PostgreSQL', version: '16.2', icon: '🗃️', category: '数据库', installed: 2 },
    { name: 'Kafka', version: '3.7.0', icon: '📨', category: '消息队列', installed: 0 },
    { name: 'Elasticsearch', version: '8.12.0', icon: '🔍', category: '搜索引擎', installed: 0 },
  ])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">软件安装</h1>
          <p className="text-sm text-muted-foreground">标准化软件分发与批量部署</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm"><RefreshCw className="h-3 w-3 mr-1" />刷新</Button>
          <Button size="sm"><Plus className="h-4 w-4 mr-1" />新建任务</Button>
        </div>
      </div>

      <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
        {[
          { label: '软件目录', value: catalog.length, color: 'text-blue-500' },
          { label: '部署任务', value: tasks.length, color: 'text-violet-500' },
          { label: '已安装主机', value: 23, color: 'text-emerald-500' },
          { label: '部署成功率', value: '95%', color: 'text-green-500' },
        ].map((item, i) => (
          <Card key={i}>
            <CardContent className="p-4">
              <p className="text-xs text-muted-foreground">{item.label}</p>
              <p className={`metric-value text-2xl ${item.color} mt-1`}>{item.value}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm">部署任务</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {tasks.map((t) => (
              <div key={t.id} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                <div className="flex items-center gap-3">
                  <Package className="h-4 w-4 text-blue-500" />
                  <div>
                    <span className="text-xs font-mono text-blue-500">{t.id}</span>
                    <span className="text-xs text-gray-900 dark:text-white ml-2">{t.name}</span>
                  </div>
                </div>
                <div className="flex items-center gap-3 text-xs text-muted-foreground">
                  <Badge variant="outline" className="text-[10px]">{t.software}</Badge>
                  <span>{t.completed}/{t.targets}</span>
                  <Badge variant={t.status === 'success' ? 'success' : t.status === 'partial' ? 'warning' : 'secondary'}>
                    {t.status === 'success' ? '完成' : t.status === 'partial' ? '部分完成' : '待执行'}
                  </Badge>
                  <span className="font-mono text-[10px]">{t.lastRun}</span>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="py-3">
            <CardTitle className="text-sm">软件目录</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {catalog.map((c, i) => (
              <div key={i} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                <div className="flex items-center gap-3">
                  <span className="text-lg">{c.icon}</span>
                  <div>
                    <span className="text-xs font-medium text-gray-900 dark:text-white">{c.name}</span>
                    <span className="text-[10px] text-muted-foreground ml-2">v{c.version}</span>
                  </div>
                  <Badge variant="secondary" className="text-[10px]">{c.category}</Badge>
                </div>
                <div className="flex items-center gap-3 text-xs text-muted-foreground">
                  <span>{c.installed} 台已安装</span>
                  <Button variant="outline" size="sm"><Play className="h-3 w-3 mr-1" />部署</Button>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
