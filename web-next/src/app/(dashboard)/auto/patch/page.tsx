'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Download, Play, RefreshCw, Plus, CheckCircle, Clock, AlertCircle } from 'lucide-react'

export default function PatchInstallPage() {
  const [tasks] = useState([
    { id: 'PAT-001', name: '安全补丁批量安装', targets: 28, completed: 28, failed: 0, lastRun: '2026-07-15 04:00', status: 'success', patches: 12 },
    { id: 'PAT-002', name: '内核补丁紧急安装', targets: 5, completed: 3, failed: 1, lastRun: '2026-07-14 23:00', status: 'partial', patches: 2 },
    { id: 'PAT-003', name: 'OpenSSL 安全补丁', targets: 45, completed: 0, failed: 0, lastRun: '待执行', status: 'pending', patches: 1 },
  ])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">补丁安装</h1>
          <p className="text-sm text-muted-foreground">自动化补丁分发与安装</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm"><RefreshCw className="h-3 w-3 mr-1" />刷新</Button>
          <Button size="sm"><Plus className="h-4 w-4 mr-1" />新建任务</Button>
        </div>
      </div>

      <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
        {[
          { label: '待安装补丁', value: 8, color: 'text-amber-500' },
          { label: '安装成功率', value: '96%', color: 'text-emerald-500' },
          { label: '已覆盖主机', value: 120, color: 'text-blue-500' },
          { label: '待重启主机', value: 5, color: 'text-orange-500' },
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
          <CardTitle className="text-sm">补丁安装任务</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {tasks.map((t) => (
            <div key={t.id} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
              <div className="flex items-center gap-3">
                <Download className="h-4 w-4 text-blue-500" />
                <div>
                  <span className="text-xs font-mono text-blue-500">{t.id}</span>
                  <span className="text-xs text-gray-900 dark:text-white ml-2">{t.name}</span>
                </div>
              </div>
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>{t.patches} 个补丁</span>
                <div className="flex items-center gap-1">
                  {t.completed > 0 && <Badge variant="success" className="text-[10px]">成功 {t.completed}</Badge>}
                  {t.failed > 0 && <Badge variant="destructive" className="text-[10px]">失败 {t.failed}</Badge>}
                  {t.status === 'pending' && <Badge variant="secondary" className="text-[10px]">待执行</Badge>}
                </div>
                <span>{t.targets} 台</span>
                <span className="font-mono text-[10px]">{t.lastRun}</span>
                {t.status === 'pending' ? (
                  <Button variant="outline" size="sm"><Play className="h-3 w-3 mr-1" />执行</Button>
                ) : (
                  <Button variant="ghost" size="icon" className="h-7 w-7"><Play className="h-3 w-3" /></Button>
                )}
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">可用补丁列表</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {[
            { name: 'openssl-3.2.1-1.el8.x86_64', severity: 'critical', size: '1.2MB', date: '2026-07-14' },
            { name: 'kernel-6.1.96-1.el8.x86_64', severity: 'high', size: '45MB', date: '2026-07-13' },
            { name: 'nginx-1.25.4-1.el8.x86_64', severity: 'medium', size: '2.8MB', date: '2026-07-12' },
            { name: 'bash-5.2.21-1.el8.x86_64', severity: 'low', size: '780KB', date: '2026-07-10' },
          ].map((p, i) => (
            <div key={i} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
              <div className="flex items-center gap-3">
                <Badge variant={p.severity === 'critical' ? 'destructive' : p.severity === 'high' ? 'warning' : p.severity === 'medium' ? 'secondary' : 'outline'}>{p.severity}</Badge>
                <span className="text-xs font-mono text-gray-900 dark:text-white">{p.name}</span>
              </div>
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>{p.size}</span>
                <span>{p.date}</span>
                <Button variant="outline" size="sm"><Download className="h-3 w-3 mr-1" />安装</Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
