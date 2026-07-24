'use client'

import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, apiFetch } from '@/lib/utils'
import { Bug, Plus, RefreshCw, ChevronRight, Download, Trash2, ShieldAlert, Shield, ShieldCheck, AlertTriangle, FileSpreadsheet } from 'lucide-react'

const sevBadge: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = {
  critical: 'destructive', high: 'warning', medium: 'secondary', low: 'success',
}
const sevLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const statusLabels: Record<string, string> = { pending: '排队中', running: '扫描中', completed: '已完成', failed: '失败' }
const typeLabels: Record<string, string> = { full: '全量扫描', quick: '快速扫描' }

interface ScanStats {
  total_scans: number
  total_findings: number
  by_severity: { critical: number; high: number; medium: number; low: number }
  by_status: Record<string, number>
}

interface VulnScan {
  id: string
  tenant_id: string
  name: string
  type: string
  demo_mode: boolean
  status: string
  severity_counts: { critical: number; high: number; medium: number; low: number }
  total_findings: number
  created_at: string
  completed_at: string
}

interface Finding {
  id: string
  scan_id: string
  cve: string
  title: string
  severity: string
  affected_package: string
  installed_version: string
  fixed_version: string
  reference: string
  status: string
  rank: number
  created_at: string
}

export default function VulnScanPage() {
  const [stats, setStats] = useState<ScanStats | null>(null)
  const [scans, setScans] = useState<VulnScan[]>([])
  const [loading, setLoading] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [findings, setFindings] = useState<Finding[]>([])
  const [findingsLoading, setFindingsLoading] = useState(false)

  // New scan dialog
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', type: 'full', demo_mode: true })

  const fetchStats = useCallback(async () => {
    try {
      const data = await apiFetch<ScanStats>('/vuln/stats')
      setStats(data)
    } catch { setStats(null) }
  }, [])

  const fetchScans = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiFetch<VulnScan[]>('/vuln/scans')
      setScans(data || [])
    } catch { setScans([]) }
    setLoading(false)
  }, [])

  const fetchFindings = useCallback(async (scanId: string) => {
    setFindingsLoading(true)
    try {
      const data = await apiFetch<Finding[]>(`/vuln/scans/${scanId}/findings`)
      setFindings(data || [])
    } catch { setFindings([]) }
    setFindingsLoading(false)
  }, [])

  useEffect(() => { fetchStats(); fetchScans() }, [fetchStats, fetchScans])

  const handleToggle = (scan: VulnScan) => {
    if (expanded === scan.id) { setExpanded(null); return }
    setExpanded(scan.id)
    fetchFindings(scan.id)
  }

  const handleCreate = async () => {
    if (!form.name.trim()) return
    try {
      await apiFetch<VulnScan>('/vuln/scans', {
        method: 'POST',
        body: JSON.stringify({ name: form.name.trim(), type: form.type, demo_mode: form.demo_mode }),
      })
      setDialogOpen(false)
      setForm({ name: '', type: 'full', demo_mode: true })
      await fetchScans()
      await fetchStats()
    } catch { /* ignore */ }
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/vuln/scans/${id}`, { method: 'DELETE' })
      if (expanded === id) { setExpanded(null); setFindings([]) }
      await fetchScans()
      await fetchStats()
    } catch { /* ignore */ }
  }

  const handleExportCSV = () => {
    window.open('/api/v1/vuln/scans/export/csv', '_blank')
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">漏洞扫描</h1>
          <p className="text-sm text-muted-foreground">自动化漏洞检测与风险评估（CVE 库匹配 + 修复建议）</p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={handleExportCSV}><FileSpreadsheet className="h-4 w-4 mr-1" />导出 CSV</Button>
          <Button variant="outline" size="sm" onClick={() => { fetchStats(); fetchScans() }} disabled={loading}><RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新</Button>
          <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建扫描</Button>
        </div>
      </div>

      {stats && (
        <div className="grid grid-cols-2 lg:grid-cols-5 gap-3">
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">扫描任务</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total_scans}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldAlert className="w-3.5 h-3.5 text-red-500" />致命漏洞</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats.by_severity.critical}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><AlertTriangle className="w-3.5 h-3.5 text-orange-500" />高危漏洞</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold text-orange-500">{stats.by_severity.high}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><Shield className="w-3.5 h-3.5 text-amber-500" />中危漏洞</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.by_severity.medium}</span></CardContent>
          </Card>
          <Card>
            <CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldCheck className="w-3.5 h-3.5 text-emerald-500" />低危漏洞</CardTitle></CardHeader>
            <CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.by_severity.low}</span></CardContent>
          </Card>
        </div>
      )}

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">扫描任务（点击展开查看漏洞详情）</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p>
          ) : scans.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">暂无扫描任务，点击「新建扫描」开始。</p>
          ) : (
            <div className="flex flex-col">
              {scans.map(scan => (
                <div key={scan.id} className="border-b last:border-0">
                  <div
                    onClick={() => handleToggle(scan)}
                    className={`px-4 py-3 cursor-pointer hover:bg-accent transition-colors flex items-center justify-between ${expanded === scan.id ? 'bg-accent' : ''}`}
                  >
                    <div className="flex items-center gap-3 min-w-0">
                      <Bug className="h-4 w-4 text-orange-500 shrink-0" />
                      <div className="min-w-0">
                        <span className="text-xs font-mono text-orange-500">{scan.name}</span>
                        <div className="flex gap-1.5 mt-1 flex-wrap">
                          <Badge variant="outline" className="text-[10px]">{typeLabels[scan.type] || scan.type}</Badge>
                          {scan.demo_mode && <Badge variant="info" className="text-[10px]">演示数据</Badge>}
                          <Badge variant={scan.status === 'completed' ? 'success' : scan.status === 'failed' ? 'destructive' : 'warning'} className="text-[10px]">
                            {statusLabels[scan.status] || scan.status}
                          </Badge>
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-3 shrink-0">
                      <div className="flex gap-1">
                        {scan.severity_counts.critical > 0 && <Badge variant="destructive" className="text-[10px]">致命 {scan.severity_counts.critical}</Badge>}
                        {scan.severity_counts.high > 0 && <Badge variant="warning" className="text-[10px]">高危 {scan.severity_counts.high}</Badge>}
                        {scan.severity_counts.medium > 0 && <Badge variant="secondary" className="text-[10px]">中危 {scan.severity_counts.medium}</Badge>}
                        {scan.severity_counts.low > 0 && <Badge variant="success" className="text-[10px]">低危 {scan.severity_counts.low}</Badge>}
                      </div>
                      <span className="text-[10px] text-muted-foreground font-mono">{scan.id.slice(0, 8)}</span>
                      <span className="text-xs text-muted-foreground hidden md:inline">{formatTime(scan.created_at)}</span>
                      <Button
                        variant="ghost" size="icon" className="h-7 w-7"
                        onClick={e => { e.stopPropagation(); handleDelete(scan.id) }}
                      >
                        <Trash2 className="h-3 w-3 text-muted-foreground hover:text-destructive" />
                      </Button>
                      <ChevronRight className={`h-3 w-3 text-muted-foreground transition-transform ${expanded === scan.id ? 'rotate-90' : ''}`} />
                    </div>
                  </div>

                  {expanded === scan.id && (
                    <div className="px-4 pb-4 pl-11">
                      {findingsLoading ? (
                        <p className="text-xs text-muted-foreground py-2">加载漏洞详情...</p>
                      ) : findings.length === 0 ? (
                        <p className="text-xs text-muted-foreground py-2">该扫描无漏洞记录。</p>
                      ) : (
                        <div className="space-y-1.5">
                          {findings.map(f => (
                            <div key={f.id} className="flex items-start justify-between p-2.5 rounded-lg bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] border border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                              <div className="flex items-start gap-2 min-w-0">
                                <Badge variant={sevBadge[f.severity] || 'secondary'} className="text-[10px] mt-0.5 shrink-0">{sevLabels[f.severity] || f.severity}</Badge>
                                <div className="min-w-0">
                                  <span className="text-xs font-mono text-gray-900 dark:text-white">{f.cve}</span>
                                  <span className="text-xs text-muted-foreground ml-2">{f.title}</span>
                                  <p className="text-[11px] text-muted-foreground mt-0.5 truncate">
                                    {f.affected_package} {f.installed_version} <span className="text-emerald-500">→ {f.fixed_version}</span>
                                    {f.reference && <> · <a href={f.reference} target="_blank" rel="noreferrer" className="underline hover:text-blue-500">NVD</a></>}
                                  </p>
                                </div>
                              </div>
                              <Badge variant={f.status === 'open' ? 'destructive' : 'success'} className="text-[10px] shrink-0">
                                {f.status === 'open' ? '待处理' : '已修复'}
                              </Badge>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建漏洞扫描">
        <div className="space-y-3">
          <div>
            <label className="text-sm font-medium">扫描名称 *</label>
            <Input
              value={form.name}
              onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
              placeholder="例如：生产环境全量扫描"
              onKeyDown={e => e.key === 'Enter' && handleCreate()}
            />
          </div>
          <div>
            <label className="text-sm font-medium">扫描类型</label>
            <select
              className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={form.type}
              onChange={e => setForm(f => ({ ...f, type: e.target.value }))}
            >
              <option value="full">全量扫描（含低危，覆盖完整 CVE 库）</option>
              <option value="quick">快速扫描（仅致命 / 高危，rank ≥ 3）</option>
            </select>
          </div>
          <div className="flex items-center gap-2">
            <input
              type="checkbox"
              id="demo-mode"
              checked={form.demo_mode}
              onChange={e => setForm(f => ({ ...f, demo_mode: e.target.checked }))}
            />
            <label htmlFor="demo-mode" className="text-sm text-muted-foreground">使用演示数据集（无需真实资产接入）</label>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleCreate} disabled={!form.name.trim()}>开始扫描</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
