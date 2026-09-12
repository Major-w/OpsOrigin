import { useEffect, useState, useCallback, useMemo } from 'react'
import { useAgentStore } from '@/stores/agent'
import { useCan } from '@/stores/permissions'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Modal, Select, Tag, Popconfirm, Segmented, Tooltip, Checkbox } from 'antd'
import { formatTime, apiFetch, apiList, apiFetchRaw } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import {
  Bug, Plus, RefreshCw, Download, Trash2, ShieldAlert, Shield, ShieldCheck, AlertTriangle,
  FileSpreadsheet, Database, Clock, CalendarClock, Info, HelpCircle, ChevronDown, ListFilter,
  CircleCheckBig, CircleSlash, Ban, RotateCcw, ServerCog, Search,
} from 'lucide-react'

const sevColor: Record<string, string> = { critical: 'red', high: 'orange', medium: 'gold', low: 'green', unknown: 'default' }
const sevLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危', unknown: '未知' }

// 扫描状态:completed 只代表「链路跑通」,与是否发现漏洞无关;partial/failed 必须显式可见,
// 否则「没扫成」会被读成「0 漏洞」。
const scanStatusMeta: Record<string, { label: string; color: string; hint: string }> = {
  running: { label: '扫描中', color: 'processing', hint: '正在执行' },
  completed: { label: '已完成', color: 'success', hint: '扫描链路成功执行完毕' },
  partial: { label: '部分失败', color: 'warning', hint: '部分目标失败,结论覆盖率不完整' },
  failed: { label: '失败', color: 'error', hint: '未成功扫描(结果不可用,不等于无漏洞)' },
}

// 漏洞处置状态(来自后端真实字段,不再由前端臆测)。
const findingStatusMeta: Record<string, { label: string; color: string }> = {
  open: { label: '待处理', color: 'red' },
  fixed: { label: '已修复', color: 'green' },
  ignored: { label: '已忽略', color: 'default' },
  false_positive: { label: '误报', color: 'blue' },
}

const scanTypeLabels: Record<string, string> = {
  full: '内置规则·全量', quick: '内置规则·快速', cve: '内置规则·CVE',
  agent: '主机 Agent(trivy fs)', image: '容器镜像(trivy image)',
}
const scheduleLabels: Record<string, string> = { once: '单次', daily: '每天', weekly: '每周' }

interface ScanStats {
  total_scans: number
  total_findings: number
  open_findings: number
  by_severity: Record<string, number>
  by_status: Record<string, number>
  by_finding_status: Record<string, number>
  open_by_severity: Record<string, number>
  scanned_targets: number
  failed_targets: number
}
interface VulnScan {
  id: string; tenant_id: string; name: string; type: string; schedule: string
  demo_mode: boolean; status: string
  severity_counts: Record<string, number>
  total_findings: number; targets: number; failed_targets: number
  error_detail?: string; agent_id?: string; db_updated_at?: string
  created_at: string; next_run_at?: string; last_run_at?: string; completed_at?: string
}
interface Finding {
  id: string; scan_id: string; cve: string; title: string; description: string
  severity: string; cvss: number; affected_package: string; installed_version: string
  fixed_version: string; reference: string; remediation: string; rank: number
  host: string; status: string; note: string; ignore_until?: string
  occurrences: number; first_seen?: string; last_seen?: string
}

const IMAGE_PLACEHOLDER = 'registry.example.com/team/app:1.2.3\n每行一个镜像全引用'

export default function VulnScanPage() {
  const { agents, fetch: fetchAgents } = useAgentStore()
  const canWrite = useCan('vuln:write')

  const [tab, setTab] = useState<'scans' | 'findings'>('scans')
  const [stats, setStats] = useState<ScanStats | null>(null)
  const [scans, setScans] = useState<VulnScan[]>([])
  const [loading, setLoading] = useState(false)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [scanFindings, setScanFindings] = useState<Finding[]>([])
  const [scanFindingsLoading, setScanFindingsLoading] = useState(false)

  // 漏洞闭环工作台(跨扫描总表)
  const [findings, setFindings] = useState<Finding[]>([])
  const [findingsTotal, setFindingsTotal] = useState(0)
  const [findingsPage, setFindingsPage] = useState(1)
  const [findingsLoading, setFindingsLoading] = useState(false)
  const [fltSeverity, setFltSeverity] = useState<string>('')
  const [fltStatus, setFltStatus] = useState<string>('open')
  const [fltSearch, setFltSearch] = useState('')

  const [dbInfoOpen, setDbInfoOpen] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [form, setForm] = useState({
    name: '', type: 'full', schedule: 'once', demo_mode: true,
    agent_id: '', images: '', registry_auto: false,
    registry_url: '', registry_user: '', registry_password: '',
  })

  // 处置弹窗
  const [dispOpen, setDispOpen] = useState(false)
  const [dispTarget, setDispTarget] = useState<Finding | null>(null)
  const [dispStatus, setDispStatus] = useState<string>('ignored')
  const [dispDays, setDispDays] = useState<number>(30)
  const [dispNote, setDispNote] = useState('')
  const [dispSubmitting, setDispSubmitting] = useState(false)

  const fetchStats = useCallback(async () => {
    try { setStats(await apiFetch<ScanStats>('/vuln/stats')) } catch { /* 失败保留上次数据 */ }
  }, [])
  const fetchScans = useCallback(async () => {
    setLoading(true)
    try { setScans(await apiList<VulnScan>('/vuln/scans?limit=200') || []) } catch { /* 保持空态 */ }
    setLoading(false)
  }, [])

  const PAGE_SIZE = 20
  const fetchFindings = useCallback(async (page = findingsPage) => {
    setFindingsLoading(true)
    try {
      const qs = new URLSearchParams()
      if (fltSeverity) qs.set('severity', fltSeverity)
      if (fltStatus) qs.set('status', fltStatus)
      if (fltSearch.trim()) qs.set('search', fltSearch.trim())
      qs.set('limit', String(PAGE_SIZE))
      qs.set('offset', String((page - 1) * PAGE_SIZE))
      const res = await apiFetch<{ items: Finding[]; total: number }>(`/vuln/findings?${qs.toString()}`)
      setFindings(res.items || [])
      setFindingsTotal(res.total || 0)
    } catch {
      setFindings([]); setFindingsTotal(0)
    }
    setFindingsLoading(false)
  }, [fltSeverity, fltStatus, fltSearch, findingsPage])

  const loadScanFindings = useCallback(async (scanId: string) => {
    setScanFindingsLoading(true)
    try { setScanFindings(await apiList<Finding>(`/vuln/scans/${scanId}/findings?limit=200`) || []) } catch { setScanFindings([]) }
    setScanFindingsLoading(false)
  }, [])

  useEffect(() => { fetchStats(); fetchScans(); fetchAgents('') }, [fetchStats, fetchScans, fetchAgents])
  useEffect(() => { if (tab === 'findings') fetchFindings(findingsPage) }, [tab, fetchFindings, findingsPage])

  const onlineAgents = useMemo(
    () => agents.filter(a => a.type === 'agent' && a.status === 'online'),
    [agents],
  )
  const needAgent = form.type === 'agent' || form.type === 'image'

  const resetForm = () => setForm({
    name: '', type: 'full', schedule: 'once', demo_mode: true,
    agent_id: '', images: '', registry_auto: false,
    registry_url: '', registry_user: '', registry_password: '',
  })

  const refreshAll = useCallback(async () => {
    await Promise.all([fetchScans(), fetchStats()])
    if (tab === 'findings') await fetchFindings(findingsPage)
  }, [fetchScans, fetchStats, fetchFindings, tab, findingsPage])

  const handleCreate = async () => {
    if (!form.name.trim() || submitting) return
    setSubmitting(true)
    try {
      const payload: Record<string, unknown> = {
        name: form.name.trim(),
        type: form.type,
        schedule: form.schedule,
      }
      if (form.type === 'agent' || form.type === 'image') {
        payload.demo_mode = false
        if (form.agent_id) payload.agent_id = form.agent_id
      } else {
        payload.demo_mode = form.demo_mode
      }
      if (form.type === 'image') {
        if (form.registry_auto) {
          payload.registry_auto = true
          payload.registry = {
            url: form.registry_url.trim(),
            username: form.registry_user.trim(),
            password: form.registry_password,
          }
        } else {
          payload.images = form.images.split('\n').map(s => s.trim()).filter(Boolean)
        }
      }
      await apiFetch('/vuln/scans', { method: 'POST', body: JSON.stringify(payload) })
      getMessage().success('扫描已下发')
      setDialogOpen(false)
      resetForm()
      await refreshAll()
    } catch (e: any) {
      getMessage().error(`下发失败: ${e?.message || '未知错误'}`)
    }
    setSubmitting(false)
  }

  const handleDelete = async (id: string) => {
    try {
      await apiFetch(`/vuln/scans/${id}`, { method: 'DELETE' })
      if (expanded === id) { setExpanded(null); setScanFindings([]) }
      getMessage().success('已删除')
      await refreshAll()
    } catch (e: any) {
      getMessage().error(`删除失败: ${e?.message || '未知错误'}`)
    }
  }

  // 处置漏洞:改状态 / 写说明 / 设忽略到期。
  const openDisposition = (f: Finding, status: string) => {
    setDispTarget(f)
    setDispStatus(status)
    setDispDays(30)
    setDispNote(f.note || '')
    setDispOpen(true)
  }

  const submitDisposition = async () => {
    if (!dispTarget || dispSubmitting) return
    setDispSubmitting(true)
    try {
      const body: Record<string, unknown> = { status: dispStatus, note: dispNote }
      if (dispStatus === 'ignored' || dispStatus === 'false_positive') {
        body.ignore_until = dispDays > 0
          ? new Date(Date.now() + dispDays * 86400000).toISOString()
          : 'null' // 空/null 表示清除到期时间 = 永久抑制
      } else {
        body.ignore_until = 'null'
      }
      await apiFetch(`/vuln/findings/${dispTarget.id}`, { method: 'PATCH', body: JSON.stringify(body) })
      getMessage().success('处置已保存')
      setDispOpen(false)
      setDispTarget(null)
      await Promise.all([
        tab === 'findings' ? fetchFindings(findingsPage) : Promise.resolve(),
        expanded ? loadScanFindings(expanded) : Promise.resolve(),
        fetchStats(),
      ])
    } catch (e: any) {
      getMessage().error(`保存失败: ${e?.message || '未知错误'}`)
    }
    setDispSubmitting(false)
  }

  // CSV 导出:必须走带鉴权头的 fetch —— window.open 不带 Authorization,后端会 401。
  const handleExport = async () => {
    try {
      const res = await apiFetchRaw('/vuln/scans/export/csv', {}, { redirectOn401: false })
      if (!res.ok) {
        getMessage().error(`导出失败(${res.status})`)
        return
      }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `vuln_findings_${new Date().toISOString().slice(0, 10)}.csv`
      document.body.appendChild(a)
      a.click()
      a.remove()
      URL.revokeObjectURL(url)
      getMessage().success('已导出 CSV')
    } catch {
      getMessage().error('导出失败')
    }
  }

  const totalPages = Math.max(1, Math.ceil(findingsTotal / PAGE_SIZE))

  const renderFindingRow = (f: Finding, showScan = false) => (
    <div key={f.id} className="flex items-start gap-3 py-2.5 border-t text-xs">
      <Tag color={sevColor[f.severity] || 'default'} className="text-[10px] shrink-0 mt-0.5">
        {sevLabels[f.severity] || f.severity}
      </Tag>
      <div className="flex-1 min-w-0">
        <p className="font-medium truncate" title={f.title}>
          {f.cve && <span className="font-mono text-[11px] text-muted-foreground mr-2">{f.cve}</span>}
          {f.title}
        </p>
        <p className="text-muted-foreground truncate">
          {f.host && <><ServerCog className="inline h-3 w-3 mr-0.5 -mt-0.5" />{f.host} · </>}
          {f.affected_package} {f.installed_version}
          {f.fixed_version && <span className="text-blue-500"> → {f.fixed_version}</span>}
          {f.cvss > 0 && <> · CVSS {f.cvss.toFixed(1)}</>}
          {f.occurrences > 1 && <> · 命中 {f.occurrences} 次</>}
          {f.reference && <> · <a href={f.reference} target="_blank" rel="noreferrer" className="underline">NVD</a></>}
        </p>
        {f.remediation && <p className="text-[11px] text-muted-foreground/80 mt-0.5">修复建议：{f.remediation}</p>}
        {f.note && <p className="text-[11px] text-muted-foreground/80 mt-0.5">处置说明：{f.note}</p>}
        {f.ignore_until && <p className="text-[11px] text-amber-600 mt-0.5">忽略至 {formatTime(f.ignore_until)}</p>}
        {showScan && f.scan_id && <p className="text-[11px] text-muted-foreground/70 mt-0.5">扫描任务 {f.scan_id.slice(0, 8)}</p>}
      </div>
      <Tag color={findingStatusMeta[f.status]?.color || 'default'} className="text-[10px] shrink-0 mt-0.5">
        {findingStatusMeta[f.status]?.label || f.status}
      </Tag>
      {canWrite && (
        <div className="flex items-center gap-1 shrink-0">
          {f.status === 'open' ? (
            <>
              <Tooltip title="标记已修复">
                <Button type="text" size="small" onClick={() => openDisposition(f, 'fixed')}><CircleCheckBig className="h-3.5 w-3.5 text-emerald-600" /></Button>
              </Tooltip>
              <Tooltip title="忽略(可设到期,避免噪音)">
                <Button type="text" size="small" onClick={() => openDisposition(f, 'ignored')}><CircleSlash className="h-3.5 w-3.5 text-slate-500" /></Button>
              </Tooltip>
              <Tooltip title="标记误报">
                <Button type="text" size="small" onClick={() => openDisposition(f, 'false_positive')}><Ban className="h-3.5 w-3.5 text-slate-500" /></Button>
              </Tooltip>
            </>
          ) : (
            <Tooltip title="重新打开">
              <Button type="text" size="small" onClick={() => openDisposition(f, 'open')}><RotateCcw className="h-3.5 w-3.5 text-sky-600" /></Button>
            </Tooltip>
          )}
        </div>
      )}
    </div>
  )

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-bold">漏洞扫描</h1>
          <p className="text-sm text-muted-foreground">基于 Trivy 的主机/容器漏洞检测、覆盖率诊断与处置闭环</p>
        </div>
        <div className="flex gap-2">
          <Button onClick={handleExport}><FileSpreadsheet className="h-4 w-4 mr-1" />导出 CSV</Button>
          <Button onClick={refreshAll} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新
          </Button>
          {canWrite && (
            <Button type="primary" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建扫描</Button>
          )}
        </div>
      </div>

      {/* 覆盖率告警:最近一轮有失败目标时,结论需打折 */}
      {stats && stats.failed_targets > 0 && (
        <div className="rounded-lg border border-amber-300 bg-amber-50/50 px-4 py-2.5 text-xs text-amber-900 flex items-start gap-2">
          <AlertTriangle className="h-4 w-4 shrink-0 mt-0.5 text-amber-600" />
          <span>
            最近一轮扫描有 <strong>{stats.failed_targets}</strong> 个目标失败(共 {stats.scanned_targets} 个),
            结果覆盖率不完整 —— 未扫到的资产不代表无漏洞。
          </span>
        </div>
      )}

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
              <p>漏洞数据源：<strong>Trivy DB</strong>（Agent 端从 GitHub Container Registry 拉取）</p>
              <p>更新策略：Agent 端漏洞库<strong>超过 6 小时</strong>即自动刷新后再扫描；离线导致刷新失败时降级使用本地库继续扫描（页面上会标注实际库时间）</p>
              <p>失败语义：trivy 未成功执行（二进制缺失 / 库不可用 / 网络失败 / 输出异常）时扫描会被标记为<strong>失败或部分失败</strong>，不会伪报成「0 漏洞」</p>
              <p>覆盖范围：操作系统包（Alpine/Ubuntu/Debian/CentOS/RHEL）、语言依赖（Go/Node/Python/Java）、容器镜像层</p>
            </dl>
          </div>
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-6 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground">扫描任务</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{stats?.total_scans ?? 0}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground">未关闭漏洞</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold">{stats?.open_findings ?? 0}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldAlert className="w-3.5 h-3.5 text-red-500" />未关闭致命</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats?.open_by_severity?.critical ?? 0}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><AlertTriangle className="w-3.5 h-3.5 text-orange-500" />未关闭高危</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-orange-500">{stats?.open_by_severity?.high ?? 0}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><Shield className="w-3.5 h-3.5 text-amber-500" />未关闭中危</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats?.open_by_severity?.medium ?? 0}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground flex items-center gap-1"><ShieldCheck className="w-3.5 h-3.5 text-blue-500" />未关闭低危</CardTitle></CardHeader>
          <CardContent className="pt-0"><span className="text-2xl font-bold text-blue-500">{stats?.open_by_severity?.low ?? 0}</span></CardContent></Card>
      </div>

      <Segmented
        value={tab}
        onChange={v => setTab(v as 'scans' | 'findings')}
        options={[
          { label: '扫描任务', value: 'scans', icon: <Bug className="h-3.5 w-3.5" /> },
          { label: '漏洞闭环', value: 'findings', icon: <ListFilter className="h-3.5 w-3.5" /> },
        ]}
      />

      {tab === 'scans' && (
        <Card><CardHeader className="py-3"><CardTitle className="text-sm">扫描任务（点击展开本轮漏洞）</CardTitle></CardHeader>
          <CardContent className="p-0">
            {loading ? <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p> :
              scans.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground">暂无扫描任务。</p> :
                scans.map(scan => (
                  <div key={scan.id} className="border-b last:border-0">
                    <div
                      onClick={() => { const next = expanded === scan.id ? null : scan.id; setExpanded(next); if (next) loadScanFindings(next) }}
                      className="flex items-center gap-3 px-4 py-3 cursor-pointer hover:bg-muted/30"
                    >
                      <Bug className="h-4 w-4 text-muted-foreground shrink-0" />
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium truncate flex items-center gap-2 flex-wrap">
                          {scan.name}
                          {scan.schedule && scan.schedule !== 'once' && (
                            <Tag className="text-[10px]" icon={<CalendarClock className="h-2.5 w-2.5" />}>
                              {scheduleLabels[scan.schedule]}
                              {scan.next_run_at ? ` · 下次 ${formatTime(scan.next_run_at)}` : ''}
                            </Tag>
                          )}
                          <Tag className="text-[10px]">{scanTypeLabels[scan.type] || scan.type}</Tag>
                          {scan.demo_mode && <Tag className="text-[10px]" color="purple">演示数据</Tag>}
                        </p>
                        <p className="text-[11px] text-muted-foreground truncate mt-0.5">
                          覆盖 {scan.targets} 个目标
                          {scan.failed_targets > 0 && <span className="text-amber-600">（{scan.failed_targets} 个失败）</span>}
                          {scan.db_updated_at && <> · 漏洞库 {formatTime(scan.db_updated_at)}</>}
                          {scan.completed_at && <> · 完成 {formatTime(scan.completed_at)}</>}
                        </p>
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        {scan.severity_counts && (
                          <>
                            <Tag color="red" className="text-[10px]">{scan.severity_counts.critical || 0}</Tag>
                            <Tag color="orange" className="text-[10px]">{scan.severity_counts.high || 0}</Tag>
                            <Tag color="gold" className="text-[10px]">{scan.severity_counts.medium || 0}</Tag>
                          </>
                        )}
                        <Tooltip title={scanStatusMeta[scan.status]?.hint}>
                          <Tag color={scanStatusMeta[scan.status]?.color || 'default'} className="text-[10px]">
                            {scanStatusMeta[scan.status]?.label || scan.status}
                          </Tag>
                        </Tooltip>
                        {canWrite && (
                          <Popconfirm title="确认删除该扫描任务？" onConfirm={() => handleDelete(scan.id)}>
                            <Button type="text" danger size="middle" className="text-red-400 hover:text-red-600"
                              onClick={e => e.stopPropagation()}><Trash2 className="h-3 w-3" /></Button>
                          </Popconfirm>
                        )}
                      </div>
                    </div>
                    {scan.error_detail && (
                      <div className="px-4 pb-2 text-[11px] text-amber-700 flex items-start gap-1">
                        <Info className="h-3 w-3 shrink-0 mt-0.5" />
                        <span className="break-all">{scan.error_detail}</span>
                      </div>
                    )}
                    {expanded === scan.id && (
                      scanFindingsLoading ? <p className="px-4 py-3 text-sm text-muted-foreground">加载漏洞列表...</p> :
                        scanFindings.length === 0 ? (
                          <p className="px-4 py-3 text-sm text-muted-foreground">
                            {scan.status === 'failed'
                              ? '本轮未产出结果 —— 扫描失败,不代表无漏洞。'
                              : scan.targets === 0
                                ? '本轮未覆盖任何资产,结果不可用于判断风险。'
                                : '本轮未发现漏洞。'}
                          </p>
                        ) :
                          <div className="px-4 pb-3">{scanFindings.map(f => renderFindingRow(f))}</div>
                    )}
                  </div>
                ))}
          </CardContent>
        </Card>
      )}

      {tab === 'findings' && (
        <Card>
          <CardHeader className="py-3">
            <div className="flex items-center gap-2 flex-wrap">
              <CardTitle className="text-sm mr-auto">漏洞闭环（共 {findingsTotal} 条）</CardTitle>
              <Input
                allowClear
                prefix={<Search className="h-3.5 w-3.5 text-muted-foreground" />}
                placeholder="搜索 CVE / 包名 / 标题"
                value={fltSearch}
                onChange={e => { setFltSearch(e.target.value); setFindingsPage(1) }}
                onPressEnter={() => { setFindingsPage(1); fetchFindings(1) }}
                className="w-56"
                size="small"
              />
              <Select
                size="small"
                value={fltSeverity || 'all'}
                onChange={v => { setFltSeverity(v === 'all' ? '' : v); setFindingsPage(1) }}
                className="w-28"
                options={[
                  { label: '全部级别', value: 'all' },
                  { label: '致命', value: 'critical' },
                  { label: '高危', value: 'high' },
                  { label: '中危', value: 'medium' },
                  { label: '低危', value: 'low' },
                ]}
              />
              <Select
                size="small"
                value={fltStatus || 'all'}
                onChange={v => { setFltStatus(v === 'all' ? '' : v); setFindingsPage(1) }}
                className="w-28"
                options={[
                  { label: '全部状态', value: 'all' },
                  { label: '待处理', value: 'open' },
                  { label: '已修复', value: 'fixed' },
                  { label: '已忽略', value: 'ignored' },
                  { label: '误报', value: 'false_positive' },
                ]}
              />
            </div>
          </CardHeader>
          <CardContent className="p-0">
            {findingsLoading ? <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p> :
              findings.length === 0 ? <p className="px-4 py-6 text-sm text-muted-foreground">没有符合条件的漏洞。</p> :
                <div className="px-4 pb-2">{findings.map(f => renderFindingRow(f, true))}</div>}
            {findingsTotal > PAGE_SIZE && (
              <div className="flex items-center justify-end gap-2 px-4 py-3 border-t">
                <Button size="small" disabled={findingsPage <= 1} onClick={() => setFindingsPage(p => Math.max(1, p - 1))}>上一页</Button>
                <span className="text-xs text-muted-foreground">{findingsPage} / {totalPages}</span>
                <Button size="small" disabled={findingsPage >= totalPages} onClick={() => setFindingsPage(p => Math.min(totalPages, p + 1))}>下一页</Button>
              </div>
            )}
          </CardContent>
        </Card>
      )}

      <Modal title="新建漏洞扫描" open={dialogOpen} onCancel={() => setDialogOpen(false)} footer={null} destroyOnHidden>
        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium">扫描名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="例如：生产环境全量扫描" />
          </div>

          <div>
            <label className="text-sm font-medium">扫描方式</label>
            <Select
              value={form.type}
              onChange={v => setForm(f => ({ ...f, type: v }))}
              className="w-full"
              options={[
                { label: '内置规则库（全量，含中低危）', value: 'full' },
                { label: '内置规则库（快速，仅致命/高危）', value: 'quick' },
                { label: '主机 Agent 扫描（trivy fs）', value: 'agent' },
                { label: '容器镜像扫描（trivy image）', value: 'image' },
              ]}
            />
            <p className="text-[11px] text-muted-foreground mt-1">
              {form.type === 'full' || form.type === 'quick'
                ? '使用内置 CVE 规则匹配 CMDB 上报的软件清单；勾选演示模式可在无真实资产时查看效果。'
                : form.type === 'agent'
                  ? '经 Agent 通道在已绑定 Agent 的资产上执行 trivy fs，产出真实 CVE 清单。'
                  : '经 Agent 通道在指定主机上执行 trivy image，扫描给定镜像或整个仓库。'}
            </p>
          </div>

          {needAgent && (
            <div>
              <label className="text-sm font-medium">
                {form.type === 'image' ? '执行主机（Agent）*' : '目标主机（留空 = 全部已绑定 Agent 的资产）'}
              </label>
              <Select
                value={form.agent_id || undefined}
                onChange={v => setForm(f => ({ ...f, agent_id: v || '' }))}
                placeholder="选择在线 Agent"
                className="w-full"
                options={onlineAgents.map(a => ({ label: `${a.name || a.id} · ${a.address}`, value: a.id }))}
              />
              {onlineAgents.length === 0 && (
                <p className="text-[11px] text-amber-600 mt-1 flex items-center gap-1">
                  <Info className="h-3 w-3" />当前无在线 Agent，请先接入主机。
                </p>
              )}
            </div>
          )}

          {form.type === 'image' && (
            <>
              <div className="flex items-center gap-2">
                <Checkbox checked={form.registry_auto} onChange={e => setForm(f => ({ ...f, registry_auto: e.target.checked }))}>
                  自动枚举整个仓库（trivy registry）
                </Checkbox>
              </div>
              {form.registry_auto ? (
                <div className="space-y-2 rounded-lg border p-3">
                  <div>
                    <label className="text-xs font-medium">仓库地址 *</label>
                    <Input value={form.registry_url} onChange={e => setForm(f => ({ ...f, registry_url: e.target.value }))} placeholder="registry.example.com" />
                  </div>
                  <div className="grid grid-cols-2 gap-2">
                    <div>
                      <label className="text-xs font-medium">用户名</label>
                      <Input value={form.registry_user} onChange={e => setForm(f => ({ ...f, registry_user: e.target.value }))} />
                    </div>
                    <div>
                      <label className="text-xs font-medium">密码</label>
                      <Input.Password value={form.registry_password} onChange={e => setForm(f => ({ ...f, registry_password: e.target.value }))} />
                    </div>
                  </div>
                </div>
              ) : (
                <div>
                  <label className="text-sm font-medium">镜像清单 *</label>
                  <Input.TextArea
                    rows={4}
                    value={form.images}
                    onChange={e => setForm(f => ({ ...f, images: e.target.value }))}
                    placeholder={IMAGE_PLACEHOLDER}
                  />
                </div>
              )}
            </>
          )}

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">执行频率</label>
              <Select
                value={form.schedule}
                onChange={v => setForm(f => ({ ...f, schedule: v }))}
                className="w-full"
                options={[
                  { label: '单次执行', value: 'once' },
                  { label: '每天', value: 'daily' },
                  { label: '每周', value: 'weekly' },
                ]}
              />
            </div>
            {(form.type === 'full' || form.type === 'quick') && (
              <div className="flex items-end pb-1">
                <Checkbox checked={form.demo_mode} onChange={e => setForm(f => ({ ...f, demo_mode: e.target.checked }))}>
                  演示模式（使用样例资产与内置规则）
                </Checkbox>
              </div>
            )}
          </div>

          {form.schedule !== 'once' && form.type === 'image' && (form.registry_password !== '') && (
            <p className="text-[11px] text-amber-600 flex items-center gap-1">
              <Info className="h-3 w-3" />含仓库凭据的镜像扫描不支持定时执行（凭据不落库），请改为单次执行。
            </p>
          )}

          {form.schedule !== 'once' && (
            <p className="text-[11px] text-muted-foreground flex items-center gap-1">
              <Clock className="h-3 w-3" />计划任务将复用同一条记录并按原参数重跑；每轮结果会更新漏洞生命周期（首末次出现、命中次数）。
            </p>
          )}

          <div className="flex justify-end gap-2 mt-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" loading={submitting} onClick={handleCreate} disabled={!form.name.trim()}>
              开始扫描
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        title="处置漏洞"
        open={dispOpen}
        onCancel={() => setDispOpen(false)}
        onOk={submitDisposition}
        confirmLoading={dispSubmitting}
        okText="保存"
        cancelText="取消"
        destroyOnHidden
      >
        {dispTarget && (
          <div className="space-y-4">
            <div className="rounded-lg border p-3 text-xs space-y-1 bg-muted/30">
              <p className="font-medium">{dispTarget.cve || '未知编号'} · {dispTarget.title}</p>
              <p className="text-muted-foreground">
                {dispTarget.host && <>{dispTarget.host} · </>}
                {dispTarget.affected_package} {dispTarget.installed_version}
                {dispTarget.fixed_version && <> → {dispTarget.fixed_version}</>}
              </p>
              {dispTarget.remediation && <p className="text-muted-foreground">建议：{dispTarget.remediation}</p>}
            </div>
            <div>
              <label className="text-sm font-medium">处置状态</label>
              <Select
                value={dispStatus}
                onChange={setDispStatus}
                className="w-full"
                options={[
                  { label: '待处理（open）', value: 'open' },
                  { label: '已修复（fixed）', value: 'fixed' },
                  { label: '已忽略（ignored）', value: 'ignored' },
                  { label: '误报（false_positive）', value: 'false_positive' },
                ]}
              />
            </div>
            {(dispStatus === 'ignored' || dispStatus === 'false_positive') && (
              <div>
                <label className="text-sm font-medium">抑制有效期</label>
                <Select
                  value={dispDays}
                  onChange={setDispDays}
                  className="w-full"
                  options={[
                    { label: '7 天后重新提醒', value: 7 },
                    { label: '30 天后重新提醒', value: 30 },
                    { label: '90 天后重新提醒', value: 90 },
                    { label: '永久抑制（不再次提醒）', value: 0 },
                  ]}
                />
                <p className="text-[11px] text-muted-foreground mt-1">
                  到期后若漏洞仍存在，会自动回到「待处理」，避免忽略项被永久遗忘。
                </p>
              </div>
            )}
            <div>
              <label className="text-sm font-medium">处置说明</label>
              <Input.TextArea
                rows={3}
                value={dispNote}
                onChange={e => setDispNote(e.target.value)}
                placeholder="记录依据，例如：已完成升级到 2.17.1 / 该服务已下线 / 业务侧确认可接受风险"
              />
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
