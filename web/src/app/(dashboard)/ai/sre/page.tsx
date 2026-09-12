
// ============================================================================
// AI SRE 工作台
//
// 后端 AI SRE 能力(验证闭环 / 处置剧本 / 自治分级 / 告警第一道防线 / RCA 基准 /
// 事故复盘 / 执行计划 / 反馈统计)此前只有接口没有界面, 运维侧完全用不上。
// 本页把这些能力收拢到一个工作台: 看得见(列表+统计)、能操作(执行/标注/发布/分级)、
// 并根据 ai:tool:exec 权限对写操作做门控。
// ============================================================================

import { useCallback, useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ErrorState } from '@/components/ui/error-state'
import { Skeleton } from '@/components/ui/skeleton'
import { useCan } from '@/stores/permissions'
import { apiFetch, apiList, cn } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { Modal, Select, Tabs, Tag } from 'antd'
import {
  BookMarked, ShieldCheck, Siren, Target, FileWarning, ListChecks, ThumbsUp,
  Play, RefreshCw, Loader2, Plus, Send, CheckCircle2, Gauge,
} from 'lucide-react'

// ------------------------------ 类型 ------------------------------

interface Runbook {
  id: string
  tenant_id: string
  name: string
  title_prefix: string
  steps: string[]
  rollback?: string
  risk_level: string
  source_run_id: string
  use_count: number
  created_at: string
  updated_at: string
}

interface RCARecord {
  id: string
  incident_id: string
  title: string
  hypothesis: string
  confidence: number
  resolved: boolean
  accurate?: boolean
  note?: string
  created_at: string
  labeled_at?: string
}

interface RCAStats {
  total: number
  labeled: number
  accurate: number
  accuracy_rate: number
  avg_confidence: number
}

interface Postmortem {
  id: string
  incident_id: string
  title: string
  summary: string
  root_cause: string
  impact?: string
  actions: string[]
  status: 'draft' | 'published'
  created_at: string
  updated_at: string
}

interface ExecutionPlan {
  id: string
  ticket_id: string
  agent_id: string
  steps: { id: string; tool: string; input: any; output?: any; status: string; error?: string }[]
  risk_level: string
  status: string
  created_at: string
}

interface FeedbackItem {
  id: string
  conversation_id?: string
  message_id?: string
  rating: 'up' | 'down'
  comment?: string
  created_at?: string
}

interface TriageResult {
  alert_id: string
  title: string
  hypothesis: string
  confidence: number
  severity: string
  suggestions: string[]
  escalate: boolean
  investigation: string
  created_at: string
}

type AutonomyLevel = 'notify' | 'suggest' | 'act_with_approval' | 'autonomous'

const AUTONOMY_META: Record<AutonomyLevel, { label: string; desc: string; color: string }> = {
  notify: { label: '仅通知', desc: 'AI 只描述发现，不生成任何处置动作', color: 'default' },
  suggest: { label: '生成建议', desc: 'AI 给出处置建议但不执行，人工确认后自行操作', color: 'blue' },
  act_with_approval: { label: '审批后执行', desc: '高危动作先弹审批条，批准后执行（推荐默认）', color: 'orange' },
  autonomous: { label: '自主执行', desc: 'AI 自主执行，仅拦截危险指令，事后可审计', color: 'red' },
}

const RISK_TAG: Record<string, string> = { high: 'error', medium: 'warning', low: 'success' }
const RISK_LABEL: Record<string, string> = { high: '高危', medium: '中危', low: '低危' }
const SEV_TAG: Record<string, string> = { critical: 'error', warning: 'warning', info: 'default' }
const SEV_LABEL: Record<string, string> = { critical: '严重', warning: '警告', info: '提示' }

function fmtTime(s?: string): string {
  if (!s) return '-'
  const d = new Date(s)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString()
}

// ------------------------------ 页面 ------------------------------

export default function AISREWorkbenchPage() {
  const canExec = useCan('ai:tool:exec')

  // —— 通用加载态 ——
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // —— 自治分级 ——
  const [autonomy, setAutonomy] = useState<AutonomyLevel>('act_with_approval')
  const [autonomySaving, setAutonomySaving] = useState(false)

  // —— 剧本 ——
  const [runbooks, setRunbooks] = useState<Runbook[]>([])
  const [rbDetail, setRbDetail] = useState<Runbook | null>(null)
  const [rbTarget, setRbTarget] = useState<Runbook | null>(null)
  const [rbRunning, setRbRunning] = useState(false)

  // —— 告警分级 ——
  const [triageForm, setTriageForm] = useState({ title: '', summary: '', severity: 'warning', source: '', target_ci: '' })
  const [triaging, setTriaging] = useState(false)
  const [triageResult, setTriageResult] = useState<TriageResult | null>(null)

  // —— RCA 基准 ——
  const [rcaRecords, setRcaRecords] = useState<RCARecord[]>([])
  const [rcaStats, setRcaStats] = useState<RCAStats | null>(null)
  const [labelTarget, setLabelTarget] = useState<RCARecord | null>(null)
  const [labelForm, setLabelForm] = useState({ accurate: true, note: '' })
  const [labelSaving, setLabelSaving] = useState(false)

  // —— 复盘 ——
  const [postmortems, setPostmortems] = useState<Postmortem[]>([])
  const [pmOpen, setPmOpen] = useState(false)
  const [pmForm, setPmForm] = useState({ title: '', incident_id: '', summary: '', root_cause: '', impact: '', actions: '' })
  const [pmSaving, setPmSaving] = useState(false)
  const [pmTarget, setPmTarget] = useState<Postmortem | null>(null)
  const [pmPublishing, setPmPublishing] = useState(false)

  // —— 执行计划 ——
  const [plans, setPlans] = useState<ExecutionPlan[]>([])

  // —— 反馈 ——
  const [feedbacks, setFeedbacks] = useState<FeedbackItem[]>([])
  const [fbStats, setFbStats] = useState<{ up: number; down: number; total: number } | null>(null)

  const loadAll = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const [auto, rbs, rca, pms, pls] = await Promise.all([
        apiFetch<{ autonomy: AutonomyLevel }>('/ai/autonomy').catch(() => null),
        apiList<Runbook>('/ai/runbooks').catch(() => []),
        apiFetch<{ records: RCARecord[]; stats: RCAStats }>('/ai/rca-records').catch(() => null),
        apiList<Postmortem>('/ai/postmortems').catch(() => []),
        apiList<ExecutionPlan>('/ai/plans').catch(() => []),
      ])
      if (auto?.autonomy) setAutonomy(auto.autonomy)
      setRunbooks(Array.isArray(rbs) ? rbs : [])
      setRcaRecords(Array.isArray(rca?.records) ? rca.records : [])
      setRcaStats(rca?.stats ?? null)
      setPostmortems(Array.isArray(pms) ? pms : [])
      setPlans(Array.isArray(pls) ? pls : [])
    } catch (e: any) {
      setError(`加载 AI SRE 数据失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
    // 反馈接口可能未配置 store(返回 503), 单独失败不阻断整页
    try {
      const fb = await apiFetch<{ feedbacks: FeedbackItem[] }>('/ai/feedback')
      setFeedbacks(Array.isArray(fb?.feedbacks) ? fb.feedbacks : [])
    } catch { setFeedbacks([]) }
    try {
      const st = await apiFetch<{ up: number; down: number; total: number }>('/ai/feedback/stats')
      setFbStats(st)
    } catch { setFbStats(null) }
  }, [])

  useEffect(() => { loadAll() }, [loadAll])

  // ---------------------- 自治分级 ----------------------
  const saveAutonomy = async (level: AutonomyLevel) => {
    setAutonomySaving(true)
    try {
      await apiFetch('/ai/autonomy', { method: 'PUT', body: JSON.stringify({ autonomy: level }) })
      setAutonomy(level)
      getMessage().success(`自治档位已切换为「${AUTONOMY_META[level].label}」`)
    } catch (e: any) {
      getMessage().error(`切换自治档位失败: ${e.message}`)
    } finally {
      setAutonomySaving(false)
    }
  }

  // ---------------------- 剧本 ----------------------
  const executeRunbook = async () => {
    if (!rbTarget) return
    setRbRunning(true)
    try {
      await apiFetch(`/ai/runbooks/${rbTarget.id}/execute`, { method: 'POST', body: '{}' })
      getMessage().success(`已按剧本「${rbTarget.name}」执行处置`)
      setRbTarget(null)
      await loadAll()
    } catch (e: any) {
      getMessage().error(`执行剧本失败: ${e.message}`)
    } finally {
      setRbRunning(false)
    }
  }

  const openRunbook = async (rb: Runbook) => {
    try {
      const full = await apiFetch<Runbook>(`/ai/runbooks/${rb.id}`)
      setRbDetail(full)
    } catch {
      setRbDetail(rb)
    }
  }

  // ---------------------- 告警分级 ----------------------
  const runTriage = async () => {
    if (!triageForm.title.trim()) return getMessage().error('请填写告警标题')
    setTriaging(true)
    setTriageResult(null)
    try {
      const res = await apiFetch<TriageResult>('/ai/alerts/triage', {
        method: 'POST',
        body: JSON.stringify({
          title: triageForm.title.trim(),
          summary: triageForm.summary || undefined,
          severity: triageForm.severity,
          source: triageForm.source || undefined,
          target_ci: triageForm.target_ci || undefined,
        }),
      })
      setTriageResult(res)
      if (res?.escalate) getMessage().warning('AI 判定需要升级人工处理')
    } catch (e: any) {
      getMessage().error(`告警分级失败: ${e.message}`)
    } finally {
      setTriaging(false)
    }
  }

  // ---------------------- RCA 标注 ----------------------
  const submitLabel = async () => {
    if (!labelTarget) return
    setLabelSaving(true)
    try {
      await apiFetch(`/ai/rca-records/${labelTarget.id}/label`, {
        method: 'POST',
        body: JSON.stringify({ accurate: labelForm.accurate, note: labelForm.note || undefined }),
      })
      getMessage().success('标注已保存')
      setLabelTarget(null)
      await loadAll()
    } catch (e: any) {
      getMessage().error(`标注失败: ${e.message}`)
    } finally {
      setLabelSaving(false)
    }
  }

  // ---------------------- 复盘 ----------------------
  const createPostmortem = async () => {
    if (!pmForm.title.trim()) return getMessage().error('请填写复盘标题')
    setPmSaving(true)
    try {
      await apiFetch('/ai/postmortems', {
        method: 'POST',
        body: JSON.stringify({
          title: pmForm.title.trim(),
          incident_id: pmForm.incident_id || undefined,
          summary: pmForm.summary,
          root_cause: pmForm.root_cause,
          impact: pmForm.impact || undefined,
          actions: pmForm.actions.split('\n').map(s => s.trim()).filter(Boolean),
        }),
      })
      getMessage().success('复盘记录已创建（草稿）')
      setPmOpen(false)
      setPmForm({ title: '', incident_id: '', summary: '', root_cause: '', impact: '', actions: '' })
      await loadAll()
    } catch (e: any) {
      getMessage().error(`创建复盘失败: ${e.message}`)
    } finally {
      setPmSaving(false)
    }
  }

  const publishPostmortem = async () => {
    if (!pmTarget) return
    setPmPublishing(true)
    try {
      await apiFetch(`/ai/postmortems/${pmTarget.id}/publish`, {
        method: 'POST',
        body: JSON.stringify({ with_memory: true }),
      })
      getMessage().success('复盘已发布，并回灌到 AI 记忆')
      setPmTarget(null)
      await loadAll()
    } catch (e: any) {
      getMessage().error(`发布复盘失败: ${e.message}`)
    } finally {
      setPmPublishing(false)
    }
  }

  // ---------------------- 渲染 ----------------------

  if (loading && runbooks.length === 0 && plans.length === 0 && postmortems.length === 0) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-6">
        <ErrorState message={error} onRetry={loadAll} />
      </div>
    )
  }

  const tabItems = [
    {
      key: 'runbooks',
      label: <span className="inline-flex items-center gap-1.5"><BookMarked className="h-3.5 w-3.5" />处置剧本（{runbooks.length}）</span>,
      children: (
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            AI 每次成功处置后自动沉淀的可复用剧本，命中同类 finding 时直接复用，省一次 LLM 调用且可审计。
          </p>
          {runbooks.length === 0 ? (
            <p className="py-8 text-sm text-muted-foreground text-center">暂无剧本。完成一次巡检修复处置后会自动沉淀。</p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {runbooks.map(rb => (
                <Card key={rb.id} className="hover:border-primary/40 transition-colors">
                  <CardHeader className="pb-2">
                    <div className="flex items-center justify-between gap-2">
                      <CardTitle className="text-sm truncate">{rb.name}</CardTitle>
                      <Tag color={RISK_TAG[rb.risk_level] || 'default'}>{RISK_LABEL[rb.risk_level] || rb.risk_level}</Tag>
                    </div>
                  </CardHeader>
                  <CardContent className="space-y-2">
                    <div className="text-[11px] text-muted-foreground">匹配键：{rb.title_prefix || '-'} · 复用 {rb.use_count} 次 · {fmtTime(rb.updated_at)}</div>
                    <ol className="text-xs text-muted-foreground space-y-0.5 list-decimal list-inside">
                      {(rb.steps || []).slice(0, 4).map((s, i) => <li key={i} className="truncate">{s}</li>)}
                      {(rb.steps || []).length > 4 && <li className="list-none text-muted-foreground">…共 {rb.steps.length} 步</li>}
                    </ol>
                    <div className="flex gap-2 pt-1">
                      <Button size="sm" variant="outline" onClick={() => openRunbook(rb)}>详情</Button>
                      <Button size="sm" onClick={() => setRbTarget(rb)} disabled={!canExec} title={canExec ? '' : '需要 ai:tool:exec 权限'}>
                        <Play className="h-3.5 w-3.5" />执行
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      ),
    },
    {
      key: 'triage',
      label: <span className="inline-flex items-center gap-1.5"><Siren className="h-3.5 w-3.5" />告警分级</span>,
      children: (
        <div className="space-y-4">
          <p className="text-xs text-muted-foreground">
            告警第一道防线：把一条告警交给 AI，输出根因假设、分级结论与建议动作；判定需升级时会标记 escalate。
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">告警标题 *</label>
              <Input value={triageForm.title} onChange={e => setTriageForm(f => ({ ...f, title: e.target.value }))} placeholder="如：web-01 /data 分区使用率 98%" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">告警级别</label>
              <Select className="w-full" value={triageForm.severity} onChange={v => setTriageForm(f => ({ ...f, severity: v }))}>
                <Select.Option value="critical">严重</Select.Option>
                <Select.Option value="warning">警告</Select.Option>
                <Select.Option value="info">提示</Select.Option>
              </Select>
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">来源（可选）</label>
              <Input value={triageForm.source} onChange={e => setTriageForm(f => ({ ...f, source: e.target.value }))} placeholder="prometheus / zabbix" />
            </div>
            <div className="space-y-1">
              <label className="text-xs text-muted-foreground">目标 CI（可选）</label>
              <Input value={triageForm.target_ci} onChange={e => setTriageForm(f => ({ ...f, target_ci: e.target.value }))} placeholder="host-01" />
            </div>
            <div className="space-y-1 md:col-span-2">
              <label className="text-xs text-muted-foreground">告警详情（可选）</label>
              <Input value={triageForm.summary} onChange={e => setTriageForm(f => ({ ...f, summary: e.target.value }))} placeholder="补充指标、时间线等上下文，结论更准" />
            </div>
          </div>
          <Button onClick={runTriage} disabled={triaging || !canExec} title={canExec ? '' : '需要 ai:tool:exec 权限'}>
            {triaging ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}AI 分级
          </Button>

          {triageResult && (
            <Card>
              <CardHeader className="pb-2">
                <div className="flex items-center justify-between gap-2">
                  <CardTitle className="text-sm">{triageResult.title}</CardTitle>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <Tag color={SEV_TAG[triageResult.severity] || 'default'}>{SEV_LABEL[triageResult.severity] || triageResult.severity}</Tag>
                    <Tag color="blue">置信度 {Math.round((triageResult.confidence || 0) * 100)}%</Tag>
                    {triageResult.escalate && <Tag color="red">需升级人工</Tag>}
                  </div>
                </div>
              </CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div><span className="text-muted-foreground">根因假设：</span>{triageResult.hypothesis || '-'}</div>
                {triageResult.investigation && <div className="text-xs text-muted-foreground">调查过程：{triageResult.investigation}</div>}
                {!!triageResult.suggestions?.length && (
                  <div>
                    <div className="text-muted-foreground text-xs mb-1">建议动作</div>
                    <ol className="list-decimal list-inside text-xs space-y-0.5">
                      {triageResult.suggestions.map((s, i) => <li key={i}>{s}</li>)}
                    </ol>
                  </div>
                )}
              </CardContent>
            </Card>
          )}
        </div>
      ),
    },
    {
      key: 'rca',
      label: <span className="inline-flex items-center gap-1.5"><Target className="h-3.5 w-3.5" />RCA 基准（{rcaRecords.length}）</span>,
      children: (
        <div className="space-y-3">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <Card><CardContent className="py-3"><div className="text-xs text-muted-foreground">已记录</div><div className="text-xl font-semibold">{rcaStats?.total ?? 0}</div></CardContent></Card>
            <Card><CardContent className="py-3"><div className="text-xs text-muted-foreground">已标注</div><div className="text-xl font-semibold">{rcaStats?.labeled ?? 0}</div></CardContent></Card>
            <Card><CardContent className="py-3"><div className="text-xs text-muted-foreground">准确率</div><div className="text-xl font-semibold">{Math.round((rcaStats?.accuracy_rate || 0) * 100)}%</div></CardContent></Card>
            <Card><CardContent className="py-3"><div className="text-xs text-muted-foreground">平均置信度</div><div className="text-xl font-semibold">{Math.round((rcaStats?.avg_confidence || 0) * 100)}%</div></CardContent></Card>
          </div>
          {rcaRecords.length === 0 ? (
            <p className="py-8 text-sm text-muted-foreground text-center">暂无 RCA 记录。告警分级/根因分析产出后会自动留痕。</p>
          ) : (
            <div className="divide-y border rounded-lg">
              {rcaRecords.map(r => (
                <div key={r.id} className="px-4 py-3 flex items-start gap-3">
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-sm font-medium truncate">{r.title}</span>
                      <Tag color="blue">置信度 {Math.round((r.confidence || 0) * 100)}%</Tag>
                      {r.accurate === undefined
                        ? <Tag>待标注</Tag>
                        : <Tag color={r.accurate ? 'success' : 'error'}>{r.accurate ? '准确' : '不准确'}</Tag>}
                    </div>
                    <div className="text-xs text-muted-foreground mt-0.5">根因假设：{r.hypothesis || '-'}</div>
                    <div className="text-[11px] text-muted-foreground mt-0.5">{fmtTime(r.created_at)}{r.note ? ` · 备注：${r.note}` : ''}</div>
                  </div>
                  <Button size="sm" variant="outline" disabled={!canExec} onClick={() => { setLabelTarget(r); setLabelForm({ accurate: r.accurate ?? true, note: r.note || '' }) }}>
                    {r.accurate === undefined ? '标注' : '改标'}
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      ),
    },
    {
      key: 'postmortems',
      label: <span className="inline-flex items-center gap-1.5"><FileWarning className="h-3.5 w-3.5" />事故复盘（{postmortems.length}）</span>,
      children: (
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <p className="text-xs text-muted-foreground">复盘沉淀后可回灌 AI 记忆，让同类事故下次自动给出更准的根因假设。</p>
            <Button size="sm" onClick={() => setPmOpen(true)} disabled={!canExec}><Plus className="h-3.5 w-3.5" />新建复盘</Button>
          </div>
          {postmortems.length === 0 ? (
            <p className="py-8 text-sm text-muted-foreground text-center">暂无复盘记录。</p>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {postmortems.map(pm => (
                <Card key={pm.id}>
                  <CardHeader className="pb-2">
                    <div className="flex items-center justify-between gap-2">
                      <CardTitle className="text-sm truncate">{pm.title}</CardTitle>
                      <Tag color={pm.status === 'published' ? 'success' : 'default'}>{pm.status === 'published' ? '已发布' : '草稿'}</Tag>
                    </div>
                  </CardHeader>
                  <CardContent className="space-y-2">
                    <div className="text-xs text-muted-foreground line-clamp-2">{pm.summary || '（无摘要）'}</div>
                    <div className="text-xs"><span className="text-muted-foreground">根因：</span>{pm.root_cause || '-'}</div>
                    {!!pm.actions?.length && (
                      <ol className="text-[11px] text-muted-foreground list-decimal list-inside space-y-0.5">
                        {pm.actions.map((a, i) => <li key={i} className="truncate">{a}</li>)}
                      </ol>
                    )}
                    <div className="flex items-center justify-between pt-1">
                      <span className="text-[11px] text-muted-foreground">{fmtTime(pm.created_at)}</span>
                      {pm.status !== 'published' && (
                        <Button size="sm" variant="outline" disabled={!canExec} onClick={() => setPmTarget(pm)}>发布并回灌</Button>
                      )}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      ),
    },
    {
      key: 'plans',
      label: <span className="inline-flex items-center gap-1.5"><ListChecks className="h-3.5 w-3.5" />执行计划（{plans.length}）</span>,
      children: (
        <div className="space-y-3">
          {plans.length === 0 ? (
            <p className="py-8 text-sm text-muted-foreground text-center">暂无执行计划。在 AI 对话中下达多步目标后会自动生成。</p>
          ) : (
            <div className="divide-y border rounded-lg">
              {plans.map(p => (
                <div key={p.id} className="px-4 py-3">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-medium">{p.ticket_id ? `工单 ${p.ticket_id}` : `计划 ${p.id.slice(0, 8)}`}</span>
                    <Tag color={RISK_TAG[p.risk_level] || 'default'}>{RISK_LABEL[p.risk_level] || p.risk_level}</Tag>
                    <Tag color={p.status === 'completed' ? 'success' : p.status === 'failed' ? 'error' : 'blue'}>{p.status}</Tag>
                    <span className="text-[11px] text-muted-foreground">{fmtTime(p.created_at)}</span>
                  </div>
                  <ol className="mt-1.5 text-xs text-muted-foreground list-decimal list-inside space-y-0.5">
                    {(p.steps || []).map(s => (
                      <li key={s.id} className="truncate">
                        {s.tool}
                        {s.status && <span className={cn('ml-1', s.status === 'failed' ? 'text-red-500' : s.status === 'done' ? 'text-green-600' : '')}>({s.status})</span>}
                        {s.error && <span className="ml-1 text-red-500">{s.error}</span>}
                      </li>
                    ))}
                  </ol>
                </div>
              ))}
            </div>
          )}
        </div>
      ),
    },
    {
      key: 'feedback',
      label: <span className="inline-flex items-center gap-1.5"><ThumbsUp className="h-3.5 w-3.5" />反馈（{fbStats?.total ?? feedbacks.length}）</span>,
      children: (
        <div className="space-y-3">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
            <Card><CardContent className="py-3 flex items-center gap-2"><ThumbsUp className="h-4 w-4 text-green-600" /><div><div className="text-xs text-muted-foreground">点赞</div><div className="text-xl font-semibold">{fbStats?.up ?? 0}</div></div></CardContent></Card>
            <Card><CardContent className="py-3 flex items-center gap-2"><ThumbsUp className="h-4 w-4 rotate-180 text-red-500" /><div><div className="text-xs text-muted-foreground">点踩</div><div className="text-xl font-semibold">{fbStats?.down ?? 0}</div></div></CardContent></Card>
            <Card><CardContent className="py-3 flex items-center gap-2"><Gauge className="h-4 w-4 text-blue-500" /><div><div className="text-xs text-muted-foreground">好评率</div><div className="text-xl font-semibold">{fbStats && fbStats.total > 0 ? `${Math.round((fbStats.up / fbStats.total) * 100)}%` : '-'}</div></div></CardContent></Card>
          </div>
          {feedbacks.length === 0 ? (
            <p className="py-8 text-sm text-muted-foreground text-center">暂无反馈记录。用户在对话中点赞/点踩后会出现在这里。</p>
          ) : (
            <div className="divide-y border rounded-lg max-h-[26rem] overflow-y-auto">
              {feedbacks.map(f => (
                <div key={f.id} className="px-4 py-2.5 flex items-center gap-3">
                  <Tag color={f.rating === 'up' ? 'success' : 'error'} className="shrink-0">{f.rating === 'up' ? '赞' : '踩'}</Tag>
                  <div className="flex-1 min-w-0">
                    <div className="text-xs text-muted-foreground truncate">{f.comment || '（无评论）'}</div>
                    <div className="text-[11px] text-muted-foreground">{fmtTime(f.created_at)}{f.conversation_id ? ` · ${f.conversation_id.slice(0, 8)}` : ''}</div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      ),
    },
  ]

  return (
    <div className="space-y-4 p-6">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-lg font-semibold flex items-center gap-2">
            <ShieldCheck className="h-5 w-5 text-blue-600 dark:text-blue-400" />AI SRE 工作台
          </h1>
          <p className="text-xs text-muted-foreground mt-0.5">
            处置剧本 · 自治分级 · 告警第一道防线 · RCA 基准 · 事故复盘 · 执行计划 · 反馈质量
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={loadAll} disabled={loading}>
          <RefreshCw className={cn('h-3.5 w-3.5', loading && 'animate-spin')} />刷新
        </Button>
      </div>

      {/* 自治分级: 唯一的全局风险闸门, 放在顶部常显 */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-sm flex items-center gap-2">
            <ShieldCheck className="h-4 w-4" />自治分级
            <Tag color={AUTONOMY_META[autonomy].color}>{AUTONOMY_META[autonomy].label}</Tag>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap items-end gap-3">
          <div className="flex-1 min-w-[220px]">
            <div className="text-xs text-muted-foreground mb-1">{AUTONOMY_META[autonomy].desc}</div>
            <Select
              className="w-full"
              value={autonomy}
              onChange={v => setAutonomy(v)}
              disabled={!canExec || autonomySaving}
              options={Object.entries(AUTONOMY_META).map(([v, m]) => ({ value: v as AutonomyLevel, label: m.label }))}
            />
          </div>
          <Button size="sm" onClick={() => saveAutonomy(autonomy)} disabled={!canExec || autonomySaving} title={canExec ? '' : '需要 ai:tool:exec 权限'}>
            {autonomySaving ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}保存档位
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-4">
          <Tabs items={tabItems} />
        </CardContent>
      </Card>

      {/* 剧本详情 */}
      <Modal open={!!rbDetail} onCancel={() => setRbDetail(null)} footer={null} title={rbDetail?.name || '剧本详情'} width={640}>
        {rbDetail && (
          <div className="space-y-3 text-sm">
            <div className="flex items-center gap-2">
              <Tag color={RISK_TAG[rbDetail.risk_level] || 'default'}>{RISK_LABEL[rbDetail.risk_level] || rbDetail.risk_level}</Tag>
              <span className="text-xs text-muted-foreground">复用 {rbDetail.use_count} 次 · 更新于 {fmtTime(rbDetail.updated_at)}</span>
            </div>
            {rbDetail.title_prefix && <div className="text-xs text-muted-foreground">匹配键：{rbDetail.title_prefix}</div>}
            <div>
              <div className="text-xs text-muted-foreground mb-1">处置步骤</div>
              <ol className="list-decimal list-inside space-y-1 text-sm">
                {(rbDetail.steps || []).map((s, i) => <li key={i}>{s}</li>)}
              </ol>
            </div>
            {rbDetail.rollback && (
              <div><div className="text-xs text-muted-foreground mb-1">回滚说明</div><div className="text-sm">{rbDetail.rollback}</div></div>
            )}
            {rbDetail.source_run_id && <div className="text-[11px] text-muted-foreground">沉淀来源：{rbDetail.source_run_id}</div>}
          </div>
        )}
      </Modal>

      {/* 执行剧本二次确认 */}
      <ConfirmDialog
        open={!!rbTarget}
        onClose={() => setRbTarget(null)}
        onConfirm={executeRunbook}
        loading={rbRunning}
        title={`按剧本「${rbTarget?.name || ''}」执行处置？`}
        description={`风险等级：${RISK_LABEL[rbTarget?.risk_level || ''] || rbTarget?.risk_level || '-'}。执行将走审批闸门与验证闭环，全过程可在审计中追溯。`}
        confirmLabel="执行"
      />

      {/* RCA 标注 */}
      <Modal open={!!labelTarget} onCancel={() => setLabelTarget(null)} onOk={submitLabel} confirmLoading={labelSaving} title="标注根因准确性" okText="保存">
        <div className="space-y-2">
          <div className="text-xs text-muted-foreground">「{labelTarget?.title || ''}」</div>
          <div className="text-xs">AI 假设：{labelTarget?.hypothesis || '-'}</div>
          <Select
            className="w-full"
            value={labelForm.accurate}
            onChange={v => setLabelForm(f => ({ ...f, accurate: v }))}
            options={[{ value: true, label: '根因准确' }, { value: false, label: '根因不准确' }]}
          />
          <Input value={labelForm.note} onChange={e => setLabelForm(f => ({ ...f, note: e.target.value }))} placeholder="备注（可选）：实际根因/偏差原因" />
        </div>
      </Modal>

      {/* 新建复盘 */}
      <Modal open={pmOpen} onCancel={() => setPmOpen(false)} onOk={createPostmortem} confirmLoading={pmSaving} title="新建事故复盘" okText="创建草稿" width={640}>
        <div className="space-y-2">
          <Input value={pmForm.title} onChange={e => setPmForm(f => ({ ...f, title: e.target.value }))} placeholder="复盘标题 *" />
          <Input value={pmForm.incident_id} onChange={e => setPmForm(f => ({ ...f, incident_id: e.target.value }))} placeholder="关联事件 ID（可选）" />
          <Input value={pmForm.summary} onChange={e => setPmForm(f => ({ ...f, summary: e.target.value }))} placeholder="影响与过程摘要" />
          <Input value={pmForm.root_cause} onChange={e => setPmForm(f => ({ ...f, root_cause: e.target.value }))} placeholder="根因" />
          <Input value={pmForm.impact} onChange={e => setPmForm(f => ({ ...f, impact: e.target.value }))} placeholder="影响范围（可选）" />
          <div>
            <label className="text-xs text-muted-foreground">改进措施（每行一条）</label>
            <textarea
              value={pmForm.actions}
              onChange={e => setPmForm(f => ({ ...f, actions: e.target.value }))}
              rows={3}
              className="mt-1 w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              placeholder={"补齐磁盘水位巡检\n接入日志轮转告警"}
            />
          </div>
        </div>
      </Modal>

      {/* 发布复盘二次确认 */}
      <ConfirmDialog
        open={!!pmTarget}
        onClose={() => setPmTarget(null)}
        onConfirm={publishPostmortem}
        loading={pmPublishing}
        title={`发布复盘「${pmTarget?.title || ''}」？`}
        description="发布后将回灌 AI 记忆，同类事故下 AI 会参考本次根因与改进措施。"
        confirmLabel="发布并回灌"
      />
    </div>
  )
}
