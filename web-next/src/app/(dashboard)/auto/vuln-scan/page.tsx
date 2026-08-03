'use client'

import { useEffect, useState, useCallback } from 'react'
import { useAgentStore } from '@/stores/agent'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { Bug, Plus, RefreshCw, Download, Trash2, ShieldAlert, Shield, ShieldCheck, AlertTriangle, FileSpreadsheet, Database, Clock, Server, Calendar, Info, HelpCircle, ChevronDown } from 'lucide-react'

const sevBadge: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = { critical: 'destructive', high: 'warning', medium: 'secondary', low: 'success' }
const sevLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const statusLabels: Record<string, string> = { pending: '排队中', running: '扫描中', completed: '已完成', failed: '失败' }

interface ScanStats { total_scans: number; total_findings: number; by_severity: { critical: number; high: number; medium: number; low: number }; by_status: Record<string, number> }
interface VulnScan { id: string; tenant_id: string; name: string; type: string; schedule?: string; target_agent?: string; scan_scope?: string; demo_mode: boolean; status: string; severity_counts: { critical: number; high: number; medium: number; low: number }; total_findings: number; created_at: string; completed_at: string }
interface Finding { id: string; scan_id: string; cve: string; title: string; severity: string; affected_package: string; installed_version: string; fixed_version: string; reference: string; status: string; rank: number }

export default function VulnScanPage() {
  const { agents, fetch: fetchAgents } = useAgentStore()
  const [stats, setStats] = useState<ScanStats | null>(null)
  const [scans, setScans] = useState<VulnScan[]>([])
  const [findings, setFindings] = useState<Finding[]>([])
  const [findingsLoading, setFindingsLoading] = useState(false)
  const [loading, setLoading] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [dbInfoOpen, setDbInfoOpen] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ name: '', type: 'full', target_agent: '', scan_scope: 'host', schedule: 'once', demo_mode: true })

  const fetchStats = useCallback(async () => { try { setStats(await apiFetch<ScanStats>('/vuln/stats')) } catch {} }, [])
  const fetchScans = useCallback(async () => { setLoading(true); try { setScans(await apiList<VulnScan>('/vuln/scans') || []) } catch {}; setLoading(false) }, [])
  const fetchFindings = useCallback(async (scanId: string) => { setFindingsLoading(true); try { setFindings(await apiList<Finding>(`/vuln/scans/${scanId}/findings`) || []) } catch {}; setFindingsLoading(false) }, [])

  useEffect(() => { fetchStats(); fetchScans(); fetchAgents('') }, [fetchStats, fetchScans, fetchAgents])

  const handleCreate = async () => {
    if (!form.name.trim()) return
    try {
      await apiFetch('/vuln/scans', { method: 'POST', body: JSON.stringify({ name: form.name.trim(), type: form.type, schedule: form.schedule, target_agent: form.target_agent, scan_scope: form.scan_scope, demo_mode: form.demo_mode }) })
      setDialogOpen(false); setForm({ name: '', type: 'full', target_agent: '', scan_scope: 'host', schedule: 'once', demo_mode: true })
      await fetchScans(); await fetchStats()
    } catch {}
  }

  const handleDelete = async (id: string) => { try { await apiFetch(`/vuln/scans/${id}`, { method: 'DELETE' }); if (expanded === id) { setExpanded(null); setFindings([]) }; await fetchScans(); await fetchStats() } catch {} }

  const onlineAgents = agents.filter(a => a.type === 'agent' && a.status === 'online')

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">漏洞扫描</h1><p className="text-sm text-muted-foreground">基于 Trivy CVE 库的主机/容器漏洞检测与风险评估</p></div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => window.open('/api/v1/vuln/scans/export/csv', '_blank')}><FileSpreadsheet className="h-4 w-4 mr-1" />导出 CSV</Button>
          <Button variant="outline" size="sm" onClick={() => { fetchStats(); fetchScans() }} disabled={loading}><RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新</Button>
          <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建扫描</Button>
        </div>
      </div>

      {/* 漏洞库说明:默认收起,点击问号/箭头展开 */}
      <div className="rounded-lg border border-blue-200 bg-blue-50/30 overflow-hidden">
        <button
          type="button"
          onClick={() => setDbInfoOpen(v => !v)}
          aria-expanded={dbInfoOpen}
          className="w-full flex items-center gap-2 px-4 py-3 text-left hover:bg-blue-100/40 transition-colors"
        >
          <Database className="h-4 w-4 text-blue-600 shrink-0" />
          <span className="text-sm font-medium text-blue-900">漏洞库信息</span>
          <HelpCircle className="h-4 w-4 text-blue-500 shrink-0" aria-label="什么是漏洞库" />
          <span className="ml-auto text-xs text-muted-foreground">{dbInfoOpen ? '收起' : '展开'}</span>
          <ChevronDown className={`h-4 w-4 text-blue-500 transition-transform duration-300 ${dbInfoOpen ? 'rotate-180' : ''}`} />
        </button>
        <div className={`grid transition-all duration-300 ease-in-out ${dbInfoOpen ? 'grid-rows-[1fr] opacity-100' : 'grid-rows-[0fr] opacity-0'}`}>
          <div className="overflow-hidden">
            <dl className="px-4 pb-4 pt-1 text-xs text-muted-foreground space-y-1 border-t border-blue-200/60">
              <p>漏洞数据源：<strong>Trivy DB</strong>（从 GitHub Container Registry 自动同步，每 6 小时更新一次）</p>
              <p>覆盖范围：操作系统包（Alpine/Ubuntu/Debian/CentOS/RHEL）、语言依赖（Go/Node/Python/Java）、容器镜像层</p>
              <p>漏洞库维护：Agent 端 trivy 二进制自动拉取最新 DB，Core 端不存储漏洞数据，扫描结果实时匹配</p>
            </dl>
          </div>
        </div>
      </div>

      {/* 统计 */}
      {stats && (
        <div className="grid grid-cols-5 gap-3">
          <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground">扫描任务</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total_scans}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldAlert className="w-3.5 h-3.5 text-red-500" />致命</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats.by_severity.critical}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><AlertTriangle className="w-3.5 h-3.5 text-orange-500" />高危</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-orange-500">{stats.by_severity.high}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><Shield className="w-3.5 h-3.5 text-amber-500" />中危</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.by_severity.medium}</span></CardContent></Card>
          <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldCheck className="w-3.5 h-3.5 text-emerald-500" />低危</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.by_severity.low}</span></CardContent></Card>
        </div>
      )}

      {/* 扫描列表 */}
      <Card><CardHeader className="py-3"><CardTitle className="text-sm">扫描任务（点击展开查看漏洞详情）</CardTitle></CardHeader>
        <CardContent className="p-0">
          {loading ? <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p> :
           scans.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground">暂无扫描任务。</p> :
           scans.map(scan => (
            <div key={scan.id} className="border-b last:border-0">
              <div onClick={() => { setExpanded(expanded === scan.id ? null : scan.id); if (expanded !== scan.id) fetchFindings(scan.id) }} className="flex items-center gap-3 px-4 py-3 cursor-pointer hover:bg-muted/30">
                <Bug className="h-4 w-4 text-muted-foreground shrink-0" />
                <div className="flex-1 min-w-0"><p className="text-sm font-medium truncate">{scan.name}{scan.schedule && scan.schedule !== 'once' && <Badge variant="outline" className="text-[10px] ml-2"><Clock className="h-2.5 w-2.5 mr-0.5" />{scan.schedule === 'daily' ? '每天' : '每周'}</Badge>}</p></div>
                <div className="flex items-center gap-2 shrink-0">
                  {scan.severity_counts && <><Badge variant={sevBadge.critical} className="text-[10px]">{scan.severity_counts.critical}</Badge><Badge variant={sevBadge.high} className="text-[10px]">{scan.severity_counts.high}</Badge><Badge variant={sevBadge.medium} className="text-[10px]">{scan.severity_counts.medium}</Badge></>}
                  <Badge variant="outline" className="text-[10px]">{statusLabels[scan.status] || scan.status}</Badge>
                  <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); handleDelete(scan.id) }}><Trash2 className="h-3 w-3" /></Button>
                </div>
              </div>
              {expanded === scan.id && (findingsLoading ? <p className="px-4 py-3 text-sm text-muted-foreground">加载漏洞列表...</p> :
                findings.length === 0 ? <p className="px-4 py-3 text-sm text-muted-foreground">暂无漏洞发现。</p> :
                <div className="px-4 pb-3 space-y-1">{findings.map(f => (
                  <div key={f.id} className="flex items-center gap-3 py-2 border-t text-xs">
                    <Badge variant={sevBadge[f.severity] || 'secondary'} className="text-[10px] shrink-0">{sevLabels[f.severity] || f.severity}</Badge>
                    <div className="flex-1 min-w-0"><p className="font-medium truncate">{f.title}</p><p className="text-muted-foreground">{f.affected_package} {f.installed_version} <span className="text-emerald-500">→ {f.fixed_version}</span>{f.reference && <> · <a href={f.reference} target="_blank" rel="noreferrer" className="underline">NVD</a></>}</p></div>
                    <Badge variant={f.status === 'open' ? 'destructive' : 'success'} className="text-[10px] shrink-0">{f.status === 'open' ? '待处理' : '已修复'}</Badge>
                  </div>
                ))}</div>
              )}
            </div>
          ))}
        </CardContent>
      </Card>

      {/* 新建扫描对话框 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title="新建漏洞扫描">
        <div className="space-y-4">
          <div><label className="text-sm font-medium">扫描名称 *</label><Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="例如：生产环境全量扫描" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">扫描目标</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.target_agent} onChange={e => setForm(f => ({ ...f, target_agent: e.target.value }))}>
                <option value="">全部主机</option>
                {onlineAgents.map(a => <option key={a.id} value={a.id}>{a.name || a.id} · {a.address}</option>)}
              </select>
            </div>
            <div><label className="text-sm font-medium">扫描范围</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.scan_scope} onChange={e => setForm(f => ({ ...f, scan_scope: e.target.value }))}>
                <option value="host">主机软件包</option><option value="image">容器镜像</option><option value="both">全部</option>
              </select>
            </div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">扫描策略</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.type} onChange={e => setForm(f => ({ ...f, type: e.target.value }))}>
                <option value="full">全量扫描（所有级别）</option><option value="quick">快速扫描（仅致命/高危）</option>
              </select>
            </div>
            <div><label className="text-sm font-medium">执行频率</label>
              <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.schedule} onChange={e => setForm(f => ({ ...f, schedule: e.target.value }))}>
                <option value="once">单次执行</option><option value="daily">每天</option><option value="weekly">每周</option>
              </select>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <input type="checkbox" id="demo-mode" checked={form.demo_mode} onChange={e => setForm(f => ({ ...f, demo_mode: e.target.checked }))} />
            <label htmlFor="demo-mode" className="text-sm text-muted-foreground">演示模式（不选真实主机时自动使用演示数据）</label>
          </div>
          {!form.demo_mode && onlineAgents.length === 0 && <p className="text-xs text-amber-600 flex items-center gap-1"><Info className="h-3 w-3" />无在线 Agent，请先接入主机或开启演示模式。</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleCreate} disabled={!form.name.trim()}>开始扫描</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
