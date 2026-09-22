
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useMonitorStore, type AlertEvent, type RCAEntry, type MaintenanceWindow } from '@/stores/monitor'
import { useAIStore } from '@/stores/ai'
import { renderMarkdown } from '@/lib/ai-render'
import { getMessage } from '@/lib/message'
import { apiFetch } from '@/lib/utils'

interface RootCausePath {
  root_ci_id: string
  root_title: string
  root_severity: string
  path: string[]
  affected_count: number
  earliest_triggered: string
  confidence: number
}
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Modal, Select, Tag, Space } from 'antd'
import { formatTime, cn } from '@/lib/utils'
import { useWebSocket } from '@/lib/useWebSocket'
import { useCan } from '@/stores/permissions'
import { AlertTriangle, CheckCircle, XCircle, Eye, Search, Loader2, History, Sparkles, Bot, Radar, Layers, Bell } from 'lucide-react'

const severityColor: Record<string, string> = { critical: 'red', high: 'red', warning: 'orange', info: 'default' }
const severityLabel: Record<string, string> = { critical: '严重', high: '严重', warning: '警告', info: '提示' }
const SEVERITIES = ['critical', 'warning', 'info']

export default function MonitorAlertsPage() {
  const store = useMonitorStore()
  const { alerts, alertHistory, loading, rcaEntries } = store
  const canWrite = useCan('monitor:write')
  const navigate = useNavigate()
  const [view, setView] = useState<'active' | 'history'>('active')
  const [severity, setSeverity] = useState('')
  const [keyword, setKeyword] = useState('')
  // 从 CMDB 拓扑跳转时携带 ci_id, 仅展示该 CI 的告警(告警 × CMDB 联动下钻)
  const [searchParams, setSearchParams] = useSearchParams()
  const ciFilter = searchParams.get('ci_id') || ''
  const [detail, setDetail] = useState<AlertEvent | null>(null)
  const [dialog, setDialog] = useState(false)
  const [rcaOpen, setRcaOpen] = useState(false)
  const [rcaLoading, setRcaLoading] = useState(false)
  const [rcaPaths, setRcaPaths] = useState<RootCausePath[]>([])
  const streamChat = useAIStore(s => s.streamChat)
  const [analyzeAlert, setAnalyzeAlert] = useState<AlertEvent | null>(null)
  const [analysisOpen, setAnalysisOpen] = useState(false)
  const [analysisText, setAnalysisText] = useState('')
  const [analysisLoading, setAnalysisLoading] = useState(false)
  const [maintOpen, setMaintOpen] = useState(false)
  const [maintForm, setMaintForm] = useState<{ name: string; start: string; end: string; instances: string; ci_ids: string; sources: string; severities: string }>({ name: '', start: '', end: '', instances: '', ci_ids: '', sources: '', severities: '' })
  const [webhookUrl, setWebhookUrl] = useState('')
  const [emailForm, setEmailForm] = useState<{ host: string; port: number; user: string; pass: string; from: string; to: string; tls: boolean }>({ host: '', port: 25, user: '', pass: '', from: '', to: '', tls: false })

  const toISO = (v: string) => (v ? new Date(v).toISOString() : '')
  const maintStatus = (w: MaintenanceWindow) => {
    if (!w.enabled) return { text: '已禁用', color: 'default' as const }
    const s = new Date(w.start_at).getTime()
    const e = new Date(w.end_at).getTime()
    const now = Date.now()
    if (now < s) return { text: '未开始', color: 'default' as const }
    if (now > e) return { text: '已结束', color: 'default' as const }
    return { text: '生效中', color: 'green' as const }
  }

  const refresh = useCallback(() => {
    store.fetchAlerts()
    store.fetchAlertHistory()
    store.fetchAlertStats()
    store.fetchRCA()
    store.fetchWebhook()
    store.fetchEmail()
    store.fetchMaintenances()
  }, [])

  const submitMaint = async () => {
    if (!maintForm.name || !maintForm.start || !maintForm.end) {
      getMessage().error('请填写名称、开始与结束时间')
      return
    }
    const match: Record<string, string[]> = {}
    const push = (k: string, v: string) => { const arr = v.split(',').map(s => s.trim()).filter(Boolean); if (arr.length) match[k] = arr }
    push('instances', maintForm.instances)
    push('ci_ids', maintForm.ci_ids)
    push('sources', maintForm.sources)
    push('severities', maintForm.severities)
    const ok = await store.createMaintenance({
      name: maintForm.name,
      start_at: toISO(maintForm.start),
      end_at: toISO(maintForm.end),
      match: Object.keys(match).length ? match : undefined,
    })
    if (ok) setMaintOpen(false)
  }

  const openRCA = async () => {
    setRcaOpen(true)
    setRcaLoading(true)
    await store.fetchRCA()
    try {
      setRcaPaths(await apiFetch<RootCausePath[]>('/monitor/alerts/rca/paths'))
    } catch { setRcaPaths([]) }
    setRcaLoading(false)
  }

  useEffect(() => { refresh() }, [refresh])

  useWebSocket({
    onEvent: (data: any) => {
      if (data.type === 'alert') refresh()
    },
  })

  const filtered = useMemo(() => {
    const list = view === 'active' ? alerts : alertHistory
    return list.filter(a => {
      if (ciFilter && a.source_ci_id !== ciFilter) return false
      if (severity && a.severity !== severity) return false
      if (keyword) {
        const k = keyword.toLowerCase()
        if (!a.title.toLowerCase().includes(k) && !(a.rule_name || '').toLowerCase().includes(k) && !(a.source_ci_id || '').toLowerCase().includes(k)) return false
      }
      return true
    })
  }, [view, alerts, alertHistory, severity, keyword, ciFilter])

  const handleAck = async (a: AlertEvent) => {
    await store.ackAlert(a.id)
    setDialog(false)
    refresh()
  }
  const handleResolve = async (a: AlertEvent) => {
    await store.resolveAlert(a.id)
    setDialog(false)
    refresh()
  }

  const handleAnalyze = async (a: AlertEvent) => {
    setDialog(false)
    setAnalyzeAlert(a)
    setAnalysisText('')
    setAnalysisOpen(true)
    setAnalysisLoading(true)

    const prompt = `请作为运维专家分析以下告警，给出根因假设与处置建议。

【告警信息】
标题: ${a.title}
严重度: ${a.severity}
触发规则: ${a.rule_name || '未知'}
源 CI: ${a.source_ci_id || '未知'}
描述: ${a.description || '无'}
触发时间: ${formatTime(a.triggered_at)}

【要求】
1. 先结合 CMDB(cmdb_query) 与脚本库(script_list / script_detail / script_executions) 分析可能根因，引用你实际查到的数据；
2. 若存在适用的已批准脚本，说明推荐哪个脚本及执行参数（不要执行）；
3. 给出分步处置建议（保持只读分析，不要执行任何变更操作）；
4. 用简洁中文输出，结构清晰。`

    let acc = ''
    await streamChat(
      prompt,
      (t) => {
        acc += t
        setAnalysisText(acc)
      },
      () => setAnalysisLoading(false),
      [],
      undefined,
      false,
      undefined,
      false,
      undefined,
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2"><AlertTriangle className="h-6 w-6 text-red-500" />告警中心</h1>
        <p className="text-sm text-muted-foreground">告警处理与历史轨迹追踪</p>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <div className="flex rounded-md border border-gray-200 dark:border-[rgba(255,255,255,0.08)] overflow-hidden">
          <button onClick={() => setView('active')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors', view === 'active' ? 'bg-blue-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
            活跃告警 ({alerts.length})
          </button>
          <button onClick={() => setView('history')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors flex items-center gap-1', view === 'history' ? 'bg-blue-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
            <History className="h-3 w-3" />处理历史 ({alertHistory.length})
          </button>
        </div>
        <div className="relative flex-1 min-w-[180px]">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input placeholder="搜索标题 / 规则 / CI..." value={keyword} onChange={e => setKeyword(e.target.value)} className="pl-8" />
        </div>
        <Select value={severity || undefined} onChange={v => setSeverity(v || '')} allowClear placeholder="全部严重度" className="max-w-[120px]" options={SEVERITIES.map(s => ({ label: severityLabel[s] || s, value: s }))} />
        {ciFilter && (
          <Tag closable color="red" className="text-xs" onClose={() => { const p = new URLSearchParams(searchParams); p.delete('ci_id'); setSearchParams(p) }}>
            仅看 CI: {ciFilter}
          </Tag>
        )}
        <Button className="gap-1 text-blue-600 dark:text-blue-400 border-blue-500/30 hover:bg-blue-500/10" onClick={openRCA}>
          <Radar className="h-3.5 w-3.5" />根因分析
        </Button>
      </div>

      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-base">{view === 'active' ? '活跃告警' : '处理历史（已确认 / 已解决）'}</CardTitle></CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="py-10 text-sm text-muted-foreground text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />加载中...</p>
          ) : filtered.length === 0 ? (
            <p className="py-10 text-sm text-muted-foreground text-center">{view === 'active' ? '暂无活跃告警 🎉' : '暂无处理历史'}</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead><tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 px-4 font-medium">严重度</th>
                  <th className="pb-2 px-4 font-medium">标题</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">规则</th>
                  <th className="pb-2 px-4 font-medium hidden lg:table-cell">源 CI</th>
                  <th className="pb-2 px-4 font-medium">触发时间</th>
                  <th className="pb-2 px-4 font-medium hidden md:table-cell">状态</th>
                  <th className="pb-2 px-4 font-medium w-36">操作</th>
                </tr></thead>
                <tbody>
                  {filtered.map(a => (
                    <tr key={a.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-white/[0.02]">
                      <td className="py-2 px-4"><Tag color={severityColor[a.severity] || 'default'} className="text-xs">{severityLabel[a.severity] || a.severity}</Tag></td>
                      <td className="py-2 px-4 max-w-[240px] truncate">{a.title}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{a.rule_name}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden lg:table-cell">{a.source_ci_id || '-'}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs whitespace-nowrap">{formatTime(a.triggered_at)}</td>
                      <td className="py-2 px-4 hidden md:table-cell">
                        <div className="flex flex-wrap gap-1">
                          {a.resolved_at ? <Tag color="green" className="text-[10px]">已解决</Tag> : a.is_suppressed ? <Tag color="orange" className="text-[10px]">已抑制</Tag> : <Tag color="red" className="text-[10px]">未处理</Tag>}
                          {a.cluster_id && <Tag className="text-[10px] gap-1"><Layers className="h-2.5 w-2.5" />聚类</Tag>}
                        </div>
                      </td>
                      <td className="py-2 px-4">
                        <div className="flex gap-1">
                          <Button size="middle" className="text-xs h-7 text-purple-600 dark:text-purple-400 border-purple-500/30 hover:bg-purple-500/10" onClick={() => handleAnalyze(a)}><Sparkles className="h-3 w-3 mr-0.5" />AI 分析</Button>
                          <Button size="middle" className="text-xs h-7" onClick={() => { setDetail(a); setDialog(true) }}><Eye className="h-3 w-3 mr-0.5" />详情</Button>
                          {!a.resolved_at && (
                            <>
                              <Button size="middle" className="text-xs h-7" onClick={() => handleAck(a)} disabled={!canWrite}><CheckCircle className="h-3 w-3 mr-0.5" />确认</Button>
                              <Button size="middle" className="text-xs h-7" onClick={() => handleResolve(a)} disabled={!canWrite}><XCircle className="h-3 w-3 mr-0.5" />解决</Button>
                            </>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 通知渠道：新告警触发时异步投递到外部（Webhook / 邮件）。与 WS 实时广播并存。 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base flex items-center gap-2"><Bell className="h-4 w-4 text-amber-500" />通知渠道</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          {/* Webhook 投递 */}
          <div className="rounded-lg border border-border p-3 space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">Webhook 推送</span>
              <Tag color={store.webhook.configured ? 'green' : 'default'} className="text-[10px]">{store.webhook.configured ? (store.webhook.format === 'wecom' ? '企业微信' : store.webhook.format === 'dingtalk' ? '钉钉' : '已启用') : '未配置'}</Tag>
            </div>
            <div className="flex gap-2">
              <Input size="small" placeholder="机器人地址(支持企微/钉钉自动识别)" value={webhookUrl || store.webhook.url} onChange={e => setWebhookUrl(e.target.value)} />
              <Button size="small" onClick={() => store.setWebhook(webhookUrl)} disabled={!canWrite}>保存</Button>
              <Button size="small" onClick={() => store.testWebhook()} disabled={!store.webhook.configured}>测试</Button>
            </div>
          </div>
          {/* 邮件(SMTP)投递 */}
          <div className="rounded-lg border border-border p-3 space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">邮件(SMTP)推送</span>
              <Tag color={store.email.configured ? 'green' : 'default'} className="text-[10px]">{store.email.configured ? '已启用' : '未配置'}</Tag>
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Input size="small" placeholder="SMTP 主机" value={emailForm.host || store.email.host} onChange={e => setEmailForm(f => ({ ...f, host: e.target.value }))} />
              <Input size="small" type="number" placeholder="端口" value={emailForm.port || store.email.port} onChange={e => setEmailForm(f => ({ ...f, port: Number(e.target.value) }))} />
              <Input size="small" placeholder="发件人" value={emailForm.from || store.email.from} onChange={e => setEmailForm(f => ({ ...f, from: e.target.value }))} />
              <Input size="small" placeholder="收件人(逗号分隔)" value={emailForm.to || store.email.to} onChange={e => setEmailForm(f => ({ ...f, to: e.target.value }))} />
              <Input size="small" placeholder="用户名(可选)" value={emailForm.user || store.email.user} onChange={e => setEmailForm(f => ({ ...f, user: e.target.value }))} />
              <Input size="small" type="password" placeholder={store.email.pass_set ? '留空不改' : '密码(可选)'} value={emailForm.pass} onChange={e => setEmailForm(f => ({ ...f, pass: e.target.value }))} />
            </div>
            <div className="flex items-center gap-3">
              <label className="flex items-center gap-1 text-xs text-muted-foreground"><input type="checkbox" checked={emailForm.tls || store.email.tls} onChange={e => setEmailForm(f => ({ ...f, tls: e.target.checked }))} /> 使用 TLS/SSL</label>
              <Button size="small" onClick={() => store.setEmail(emailForm)} disabled={!canWrite}>保存</Button>
              <Button size="small" onClick={() => store.testEmail()} disabled={!store.email.configured}>测试</Button>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 维护期静默：变更窗口内抑制误报告警（不投递通知，仍保留记录） */}
      <Card>
        <CardHeader className="pb-2 flex flex-row items-center justify-between">
          <CardTitle className="text-base flex items-center gap-2"><Bell className="h-4 w-4 text-amber-500" />维护期静默</CardTitle>
          <Button size="middle" className="text-xs h-7 text-blue-600 dark:text-blue-400 border-blue-500/30 hover:bg-blue-500/10" onClick={() => { setMaintForm({ name: '', start: '', end: '', instances: '', ci_ids: '', sources: '', severities: '' }); setMaintOpen(true) }}>
            <Bell className="h-3 w-3 mr-0.5" />新建
          </Button>
        </CardHeader>
        <CardContent className="space-y-2">
          <p className="text-xs text-muted-foreground">变更窗口内抑制误报告警（不投递通知，仍保留活跃与历史记录）。可指定匹配维度（instance / CI / source / severity），留空表示全部匹配。</p>
          {store.maintenances.length === 0 ? (
            <p className="py-6 text-sm text-muted-foreground text-center">暂无维护期</p>
          ) : (
            store.maintenances.map(w => {
              const st = maintStatus(w)
              const chips: string[] = []
              ;(w.match?.instances || []).forEach(v => chips.push('instance=' + v))
              ;(w.match?.ci_ids || []).forEach(v => chips.push('ci=' + v))
              ;(w.match?.sources || []).forEach(v => chips.push('source=' + v))
              ;(w.match?.severities || []).forEach(v => chips.push('severity=' + v))
              return (
                <div key={w.id} className="flex items-center gap-3 rounded-lg border border-border p-2.5">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium">{w.name}</span>
                      <Tag color={st.color} className="text-[10px]">{st.text}</Tag>
                    </div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{formatTime(w.start_at)} ~ {formatTime(w.end_at)}</div>
                    {chips.length > 0 && (
                      <div className="flex flex-wrap gap-1 mt-1">
                        {chips.map((c, i) => <Tag key={i} className="text-[10px]">{c}</Tag>)}
                      </div>
                    )}
                  </div>
                  <Button size="middle" className="text-xs h-7" onClick={() => store.deleteMaintenance(w.id)} disabled={!canWrite}><XCircle className="h-3 w-3 mr-0.5" />删除</Button>
                </div>
              )
            })
          )}
        </CardContent>
      </Card>

      <Modal title="告警详情" open={dialog} onCancel={() => setDialog(false)} footer={null}>
        {detail && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <div><span className="text-muted-foreground">标题:</span> {detail.title}</div>
              <div><span className="text-muted-foreground">严重度:</span> <Tag color={severityColor[detail.severity] || 'default'} className="text-xs">{severityLabel[detail.severity] || detail.severity}</Tag></div>
              <div><span className="text-muted-foreground">规则:</span> {detail.rule_name}</div>
              <div><span className="text-muted-foreground">源 CI:</span> {detail.source_ci_id || '未知'}</div>
              <div className="col-span-2"><span className="text-muted-foreground">描述:</span> {detail.description || '无'}</div>
              <div><span className="text-muted-foreground">触发:</span> {formatTime(detail.triggered_at)}</div>
              <div><span className="text-muted-foreground">抑制:</span> {detail.is_suppressed ? '是' : '否'}</div>
              {detail.root_cause_ci && <div className="col-span-2"><span className="text-muted-foreground">根因 CI:</span> {detail.root_cause_ci}</div>}
              {detail.resolved_at && <div className="col-span-2"><span className="text-muted-foreground">解决于:</span> {formatTime(detail.resolved_at)}</div>}
            </div>
          </div>
        )}
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setDialog(false)}>关闭</Button>
          {detail && (
            <Button size="middle" className="text-purple-600 dark:text-purple-400 border-purple-500/30 hover:bg-purple-500/10" onClick={() => handleAnalyze(detail)}>
              <Sparkles className="h-3 w-3 mr-1" />AI 分析
            </Button>
          )}
          {detail && !detail.resolved_at && (
            <>
              <Button size="middle" onClick={() => handleAck(detail)} disabled={!canWrite}><CheckCircle className="h-3 w-3 mr-1" />确认</Button>
              <Button size="middle" onClick={() => handleResolve(detail)} disabled={!canWrite}><XCircle className="h-3 w-3 mr-1" />解决</Button>
            </>
          )}
        </div>
      </Modal>

      <Modal title="根因分析（活跃告警）" open={rcaOpen} onCancel={() => setRcaOpen(false)} footer={null}>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            因果链：按拓扑祖先 + 触发时序把告警串成传播路径（根因 → 受影响）。下方为按严重度/时序评分的根因候选。
          </p>
          {!rcaLoading && rcaPaths.length > 0 && (
            <div className="space-y-2">
              <div className="text-xs font-medium text-muted-foreground">因果传播路径</div>
              {rcaPaths.map((p, i) => (
                <div key={i} className="rounded-lg border border-border p-2.5 space-y-1.5">
                  <div className="flex items-center gap-2 flex-wrap">
                    <Tag color={p.root_severity === 'critical' || p.root_severity === 'high' ? 'red' : 'orange'} className="text-[10px]">根因</Tag>
                    <span className="text-sm font-medium">{p.root_title}</span>
                    <span className="text-[11px] text-muted-foreground">{p.root_ci_id}</span>
                    <span className="ml-auto text-[11px] text-muted-foreground">影响 {p.affected_count} 项 · 置信度 {(p.confidence * 100).toFixed(0)}%</span>
                  </div>
                  <div className="flex items-center gap-1 flex-wrap text-[11px]">
                    {p.path.map((ci, j) => (
                      <span key={j} className="inline-flex items-center gap-1">
                        {j > 0 && <span className="text-muted-foreground">→</span>}
                        <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{ci}</code>
                      </span>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
          <p className="text-xs text-muted-foreground">
            基于拓扑关系 + 严重度权重 + 触发时序的启发式评分。分数越高越可能是根因源头（祖先节点、更早触发、更严重优先）。
          </p>
          {rcaLoading ? (
            <p className="py-6 text-sm text-muted-foreground text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />分析中...</p>
          ) : rcaEntries.length === 0 ? (
            <p className="py-6 text-sm text-muted-foreground text-center">暂无活跃告警，无需根因分析。</p>
          ) : (
            <div className="space-y-2">
              {rcaEntries.map((r, i) => (
                <div key={r.ci_id + i} className="flex items-center gap-3 rounded-lg border border-border p-2.5">
                  <div className={cn('w-6 h-6 rounded-full flex items-center justify-center text-xs font-semibold shrink-0', i === 0 ? 'bg-red-500/15 text-red-500' : 'bg-muted text-muted-foreground')}>
                    {i + 1}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="text-sm font-medium truncate">{r.title}</div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      CI: {r.ci_id} · 规则: {r.rule_name} · 触发: {formatTime(r.triggered_at)}
                    </div>
                  </div>
                  <div className="text-right shrink-0">
                    <Tag color={r.severity === 'critical' || r.severity === 'high' ? 'red' : 'orange'} className="text-[10px]">{severityLabel[r.severity] || r.severity}</Tag>
                    <div className="text-xs font-semibold mt-1 text-blue-600 dark:text-blue-400">{r.score.toFixed(2)}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setRcaOpen(false)}>关闭</Button>
        </div>
      </Modal>

      <Modal title="AI 告警分析" open={analysisOpen} onCancel={() => setAnalysisOpen(false)} footer={null}>
        {analyzeAlert && (
          <div className="space-y-3">
            <div className="rounded-lg bg-gray-50 dark:bg-white/[0.03] border border-gray-200 dark:border-gray-800 p-3 text-xs text-muted-foreground space-y-1">
              <p className="font-medium text-foreground flex items-center gap-1.5">
                <AlertTriangle className={cn('h-3.5 w-3.5', analyzeAlert.severity === 'critical' || analyzeAlert.severity === 'high' ? 'text-red-500' : 'text-amber-500')} />
                {analyzeAlert.title}
              </p>
              <p>严重度: {severityLabel[analyzeAlert.severity] || analyzeAlert.severity} · 规则: {analyzeAlert.rule_name || '未知'} · 源 CI: {analyzeAlert.source_ci_id || '未知'} · 触发: {formatTime(analyzeAlert.triggered_at)}</p>
            </div>

            {analysisLoading && !analysisText ? (
              <div className="flex items-center gap-2 text-sm text-muted-foreground py-4">
                <Loader2 className="h-4 w-4 animate-spin" />
                <span><Bot className="h-3.5 w-3.5 inline mr-1" />AI 正在分析（会查询 CMDB 与脚本库）…</span>
              </div>
            ) : (
              <div
                className="text-sm leading-relaxed max-h-[50vh] overflow-y-auto pr-1"
                dangerouslySetInnerHTML={{ __html: renderMarkdown(analysisText || '（分析失败或无输出，请稍后重试）') }}
              />
            )}
          </div>
        )}
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setAnalysisOpen(false)}>关闭</Button>
          <Button size="middle" className="text-blue-600 dark:text-blue-400 border-blue-500/30 hover:bg-blue-500/10" onClick={() => { setAnalysisOpen(false); navigate('/ai') }}>
            <Bot className="h-3 w-3 mr-1" />转到 AI 助手处置
          </Button>
        </div>
      </Modal>

      <Modal title="新建维护期" open={maintOpen} onCancel={() => setMaintOpen(false)} onOk={submitMaint} okText="创建">
        <div className="space-y-3">
          <div>
            <label className="text-xs text-muted-foreground">名称</label>
            <Input value={maintForm.name} onChange={e => setMaintForm({ ...maintForm, name: e.target.value })} placeholder="如：周末变更窗口" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-muted-foreground">开始时间</label>
              <Input type="datetime-local" value={maintForm.start} onChange={e => setMaintForm({ ...maintForm, start: e.target.value })} />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">结束时间</label>
              <Input type="datetime-local" value={maintForm.end} onChange={e => setMaintForm({ ...maintForm, end: e.target.value })} />
            </div>
          </div>
          <div>
            <label className="text-xs text-muted-foreground">匹配条件（可选，逗号分隔；留空=全部匹配）</label>
            <div className="grid grid-cols-2 gap-2 mt-1">
              <Input size="small" value={maintForm.instances} onChange={e => setMaintForm({ ...maintForm, instances: e.target.value })} placeholder="instance: host-a,host-b" />
              <Input size="small" value={maintForm.ci_ids} onChange={e => setMaintForm({ ...maintForm, ci_ids: e.target.value })} placeholder="ci_id: ci-1,ci-2" />
              <Input size="small" value={maintForm.sources} onChange={e => setMaintForm({ ...maintForm, sources: e.target.value })} placeholder="source: zabbix" />
              <Input size="small" value={maintForm.severities} onChange={e => setMaintForm({ ...maintForm, severities: e.target.value })} placeholder="severity: warning,info" />
            </div>
          </div>
        </div>
      </Modal>
    </div>
  )
}
