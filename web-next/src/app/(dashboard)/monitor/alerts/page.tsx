'use client'

import { useCallback, useEffect, useMemo, useState } from 'react'
import { useMonitorStore, type AlertEvent, type RCAEntry } from '@/stores/monitor'
import { useAIStore } from '@/stores/ai'
import { renderMarkdown } from '@/lib/ai-render'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, cn } from '@/lib/utils'
import { useWebSocket } from '@/lib/useWebSocket'
import { useCan } from '@/stores/permissions'
import { AlertTriangle, CheckCircle, XCircle, Eye, Search, Loader2, History, Sparkles, Bot, Radar, Layers } from 'lucide-react'

const severityVariant: Record<string, 'destructive' | 'warning' | 'secondary'> = { critical: 'destructive', high: 'destructive', warning: 'warning', info: 'secondary' }
const severityLabel: Record<string, string> = { critical: '严重', high: '严重', warning: '警告', info: '提示' }
const SEVERITIES = ['critical', 'warning', 'info']

export default function MonitorAlertsPage() {
  const store = useMonitorStore()
  const { alerts, alertHistory, loading, rcaEntries } = store
  const canWrite = useCan('monitor:write')
  const [view, setView] = useState<'active' | 'history'>('active')
  const [severity, setSeverity] = useState('')
  const [keyword, setKeyword] = useState('')
  const [detail, setDetail] = useState<AlertEvent | null>(null)
  const [dialog, setDialog] = useState(false)
  // —— RCA 根因分析(P0) ——
  const [rcaOpen, setRcaOpen] = useState(false)
  const [rcaLoading, setRcaLoading] = useState(false)
  // —— AI 告警分析 ——
  const streamChat = useAIStore(s => s.streamChat)
  const [analyzeAlert, setAnalyzeAlert] = useState<AlertEvent | null>(null)
  const [analysisOpen, setAnalysisOpen] = useState(false)
  const [analysisText, setAnalysisText] = useState('')
  const [analysisLoading, setAnalysisLoading] = useState(false)

  const refresh = useCallback(() => {
    store.fetchAlerts()
    store.fetchAlertHistory()
    store.fetchAlertStats()
    store.fetchRCA()
  }, [])

  const openRCA = async () => {
    setRcaOpen(true)
    setRcaLoading(true)
    await store.fetchRCA()
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
      if (severity && a.severity !== severity) return false
      if (keyword) {
        const k = keyword.toLowerCase()
        if (!a.title.toLowerCase().includes(k) && !(a.rule_name || '').toLowerCase().includes(k) && !(a.source_ci_id || '').toLowerCase().includes(k)) return false
      }
      return true
    })
  }, [view, alerts, alertHistory, severity, keyword])

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

  // —— AI 告警分析: 复用 streamChat 全链路(工具循环: 自动查 CMDB/脚本库/执行历史), 只读分析不执行变更 ——
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
      [], // 独立分析会话, 不带历史
      undefined, // 默认 Agent(全量只读工具 + 全局审批策略)
      false, // 快速分析, 不请求 reasoning
      undefined,
      false,
      undefined, // 分析中不弹审批确认; AI 若尝试变更操作将被审批闸门拦截
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div>
        <h1 className="text-2xl font-bold flex items-center gap-2"><AlertTriangle className="h-6 w-6 text-red-500" />告警中心</h1>
        <p className="text-sm text-muted-foreground">告警处理与历史轨迹追踪</p>
      </div>

      {/* 视图 + 筛选 */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex rounded-md border border-gray-200 dark:border-[rgba(255,255,255,0.08)] overflow-hidden">
          <button onClick={() => setView('active')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors', view === 'active' ? 'bg-emerald-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
            活跃告警 ({alerts.length})
          </button>
          <button onClick={() => setView('history')} className={cn('px-3 py-1.5 text-xs font-medium transition-colors flex items-center gap-1', view === 'history' ? 'bg-emerald-500 text-white' : 'text-muted-foreground hover:bg-gray-50 dark:hover:bg-white/[0.03]')}>
            <History className="h-3 w-3" />处理历史 ({alertHistory.length})
          </button>
        </div>
        <div className="relative flex-1 min-w-[180px]">
          <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input placeholder="搜索标题 / 规则 / CI..." value={keyword} onChange={e => setKeyword(e.target.value)} className="pl-8" />
        </div>
        <select value={severity} onChange={e => setSeverity(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm max-w-[120px]">
          <option value="">全部严重度</option>
          {SEVERITIES.map(s => <option key={s} value={s}>{severityLabel[s] || s}</option>)}
        </select>
        <Button variant="outline" size="sm" className="gap-1 text-emerald-600 dark:text-emerald-400 border-emerald-500/30 hover:bg-emerald-500/10" onClick={openRCA}>
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
                      <td className="py-2 px-4"><Badge variant={severityVariant[a.severity] || 'secondary'} className="text-xs">{severityLabel[a.severity] || a.severity}</Badge></td>
                      <td className="py-2 px-4 max-w-[240px] truncate">{a.title}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden md:table-cell">{a.rule_name}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs hidden lg:table-cell">{a.source_ci_id || '-'}</td>
                      <td className="py-2 px-4 text-muted-foreground text-xs whitespace-nowrap">{formatTime(a.triggered_at)}</td>
                      <td className="py-2 px-4 hidden md:table-cell">
                        <div className="flex flex-wrap gap-1">
                          {a.resolved_at ? <Badge variant="success" className="text-[10px]">已解决</Badge> : a.is_suppressed ? <Badge variant="warning" className="text-[10px]">已抑制</Badge> : <Badge variant="destructive" className="text-[10px]">未处理</Badge>}
                          {a.cluster_id && <Badge variant="outline" className="text-[10px] gap-1"><Layers className="h-2.5 w-2.5" />聚类</Badge>}
                        </div>
                      </td>
                      <td className="py-2 px-4">
                        <div className="flex gap-1">
                          <Button variant="outline" size="sm" className="text-xs h-7 text-purple-600 dark:text-purple-400 border-purple-500/30 hover:bg-purple-500/10" onClick={() => handleAnalyze(a)}><Sparkles className="h-3 w-3 mr-0.5" />AI 分析</Button>
                          <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => { setDetail(a); setDialog(true) }}><Eye className="h-3 w-3 mr-0.5" />详情</Button>
                          {!a.resolved_at && (
                            <>
                              <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => handleAck(a)} disabled={!canWrite}><CheckCircle className="h-3 w-3 mr-0.5" />确认</Button>
                              <Button variant="outline" size="sm" className="text-xs h-7" onClick={() => handleResolve(a)} disabled={!canWrite}><XCircle className="h-3 w-3 mr-0.5" />解决</Button>
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

      {/* 详情弹窗 */}
      <Dialog open={dialog} onClose={() => setDialog(false)} title="告警详情">
        {detail && (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <div><span className="text-muted-foreground">标题:</span> {detail.title}</div>
              <div><span className="text-muted-foreground">严重度:</span> <Badge variant={severityVariant[detail.severity] || 'secondary'} className="text-xs">{severityLabel[detail.severity] || detail.severity}</Badge></div>
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialog(false)}>关闭</Button>
          {detail && (
            <Button variant="outline" size="sm" className="text-purple-600 dark:text-purple-400 border-purple-500/30 hover:bg-purple-500/10" onClick={() => handleAnalyze(detail)}>
              <Sparkles className="h-3 w-3 mr-1" />AI 分析
            </Button>
          )}
          {detail && !detail.resolved_at && (
            <>
              <Button variant="outline" size="sm" onClick={() => handleAck(detail)} disabled={!canWrite}><CheckCircle className="h-3 w-3 mr-1" />确认</Button>
              <Button variant="outline" size="sm" onClick={() => handleResolve(detail)} disabled={!canWrite}><XCircle className="h-3 w-3 mr-1" />解决</Button>
            </>
          )}
        </DialogFooter>
      </Dialog>

      {/* RCA 根因分析弹窗(P0 启发式: 拓扑 + 严重度 + 触发时序评分) */}
      <Dialog open={rcaOpen} onClose={() => setRcaOpen(false)} title="根因分析（活跃告警）">
        <div className="space-y-3">
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
                    <Badge variant={r.severity === 'critical' || r.severity === 'high' ? 'destructive' : 'warning'} className="text-[10px]">{severityLabel[r.severity] || r.severity}</Badge>
                    <div className="text-xs font-semibold mt-1 text-emerald-600 dark:text-emerald-400">{r.score.toFixed(2)}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setRcaOpen(false)}>关闭</Button>
        </DialogFooter>
      </Dialog>

      {/* AI 告警分析弹窗 */}
      <Dialog open={analysisOpen} onClose={() => setAnalysisOpen(false)} title="AI 告警分析">
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setAnalysisOpen(false)}>关闭</Button>
          <Button variant="outline" size="sm" className="text-emerald-600 dark:text-emerald-400 border-emerald-500/30 hover:bg-emerald-500/10" onClick={() => { setAnalysisOpen(false); window.location.href = '/ai' }}>
            <Bot className="h-3 w-3 mr-1" />转到 AI 助手处置
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
