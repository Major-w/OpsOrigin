'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Bug, Play, RefreshCw, Plus, AlertTriangle, Shield, ShieldAlert, ShieldCheck } from 'lucide-react'

export default function VulnScanPage() {
  const [tasks] = useState([
    { id: 'VUL-001', name: '全量漏洞扫描', targets: 45, lastRun: '2026-07-14 02:00', status: 'success', critical: 2, high: 5, medium: 12, low: 23 },
    { id: 'VUL-002', name: '高危漏洞专项扫描', targets: 15, lastRun: '2026-07-15 03:00', status: 'success', critical: 1, high: 3, medium: 4, low: 8 },
    { id: 'VUL-003', name: '新CVE紧急扫描', targets: 45, lastRun: '2026-07-15 06:00', status: 'running', critical: 0, high: 0, medium: 0, low: 0 },
  ])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">漏洞扫描</h1>
          <p className="text-sm text-muted-foreground">自动化漏洞检测与风险评估</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm"><RefreshCw className="h-3 w-3 mr-1" />刷新</Button>
          <Button size="sm"><Plus className="h-4 w-4 mr-1" />新建扫描</Button>
        </div>
      </div>

      <div className="grid gap-4 grid-cols-2 lg:grid-cols-4">
        <Card>
          <CardContent className="p-4">
            <div className="flex items-center gap-2">
              <ShieldAlert className="w-5 h-5 text-red-500" />
              <p className="text-xs text-muted-foreground">严重漏洞</p>
            </div>
            <p className="metric-value text-2xl text-red-500 mt-1">3</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <div className="flex items-center gap-2">
              <AlertTriangle className="w-5 h-5 text-orange-500" />
              <p className="text-xs text-muted-foreground">高危漏洞</p>
            </div>
            <p className="metric-value text-2xl text-orange-500 mt-1">8</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <div className="flex items-center gap-2">
              <Shield className="w-5 h-5 text-amber-500" />
              <p className="text-xs text-muted-foreground">中危漏洞</p>
            </div>
            <p className="metric-value text-2xl text-amber-500 mt-1">16</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="p-4">
            <div className="flex items-center gap-2">
              <ShieldCheck className="w-5 h-5 text-emerald-500" />
              <p className="text-xs text-muted-foreground">低危漏洞</p>
            </div>
            <p className="metric-value text-2xl text-emerald-500 mt-1">31</p>
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">扫描任务</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {tasks.map((t) => (
            <div key={t.id} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
              <div className="flex items-center gap-3">
                <Bug className="h-4 w-4 text-orange-500" />
                <div>
                  <span className="text-xs font-mono text-orange-500">{t.id}</span>
                  <span className="text-xs text-gray-900 dark:text-white ml-2">{t.name}</span>
                </div>
              </div>
              <div className="flex items-center gap-4 text-xs">
                <span className="text-muted-foreground">{t.targets} 台</span>
                {t.critical > 0 && <Badge variant="destructive" className="text-[10px]">严重 {t.critical}</Badge>}
                {t.high > 0 && <Badge variant="warning" className="text-[10px]">高危 {t.high}</Badge>}
                <Badge variant={t.status === 'success' ? 'success' : t.status === 'running' ? 'warning' : 'secondary'}>
                  {t.status === 'success' ? '完成' : '扫描中'}
                </Badge>
                <span className="font-mono text-[10px] text-muted-foreground">{t.lastRun}</span>
                <Button variant="ghost" size="icon" className="h-7 w-7"><Play className="h-3 w-3" /></Button>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">漏洞详情示例</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {[
            { cve: 'CVE-2026-1234', severity: 'critical', name: 'OpenSSL 远程代码执行', affected: 'prod-web-01, prod-web-02', fix: '升级 OpenSSL 至 3.2.1' },
            { cve: 'CVE-2026-5678', severity: 'high', name: 'Linux Kernel 权限提升', affected: 'prod-db-01', fix: '更新内核至 6.1.96' },
            { cve: 'CVE-2026-9012', severity: 'medium', name: 'Nginx 信息泄露', affected: 'api-gateway-01~03', fix: '升级 Nginx 至 1.25.4' },
          ].map((v, i) => (
            <div key={i} className="flex items-center justify-between p-3 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
              <div className="flex items-center gap-3">
                <Badge variant={v.severity === 'critical' ? 'destructive' : v.severity === 'high' ? 'warning' : 'secondary'}>{v.severity}</Badge>
                <div>
                  <span className="text-xs font-mono text-gray-900 dark:text-white">{v.cve}</span>
                  <span className="text-xs text-muted-foreground ml-2">{v.name}</span>
                </div>
              </div>
              <div className="flex items-center gap-4 text-xs text-muted-foreground">
                <span>{v.affected}</span>
                <span className="text-emerald-500">{v.fix}</span>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
