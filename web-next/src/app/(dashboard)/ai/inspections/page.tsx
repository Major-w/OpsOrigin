'use client'

import { useEffect, useMemo, useState } from 'react'
import type { ReactNode, TextareaHTMLAttributes } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import {
  useInspections,
  type Inspection,
  type InspectionRun,
  type InspectionSchedule,
  type InspectionTemplate,
} from '@/stores/aiWorkforce'
import { useAIStore } from '@/stores/ai'
import {
  RadarIcon, Plus, RefreshCw, Play, Trash2, Edit, History,
  AlertTriangle, CheckCircle2, XCircle, Clock, FileText, ArrowUpRight, FlaskConical,
} from 'lucide-react'

// ----------------------------- 常量与工具 -----------------------------

const SCHEDULE_OPTIONS: { value: InspectionSchedule; label: string }[] = [
  { value: '@hourly', label: '每小时' },
  { value: '@every_6h', label: '每 6 小时' },
  { value: '@daily', label: '每天' },
  { value: '@weekly', label: '每周' },
]
const SCHEDULE_LABELS: Record<string, string> = {
  '@hourly': '每小时', '@every_6h': '每 6 小时', '@daily': '每天', '@weekly': '每周',
}

const STATUS_META: Record<string, { label: string; variant: 'success' | 'destructive' | 'warning' | 'secondary'; Icon: typeof CheckCircle2 }> = {
  success: { label: '正常', variant: 'success', Icon: CheckCircle2 },
  failed: { label: '失败', variant: 'destructive', Icon: XCircle },
  running: { label: '执行中', variant: 'warning', Icon: Clock },
}

const LEVEL_META: Record<string, { label: string; cls: string }> = {
  high: { label: '高', cls: 'bg-red-500/10 text-red-600 dark:text-red-400 border-red-500/30' },
  medium: { label: '中', cls: 'bg-amber-500/10 text-amber-600 dark:text-amber-400 border-amber-500/30' },
  low: { label: '低', cls: 'bg-sky-500/10 text-sky-600 dark:text-sky-400 border-sky-500/30' },
}

interface Finding { level?: string; title?: string; detail?: string; suggestion?: string }

/** 解析运行记录中的结构化发现（模型输出的 JSON 代码块，可能不合规，需容错） */
function parseFindings(raw?: string): Finding[] {
  if (!raw) return []
  try {
    const v = JSON.parse(raw)
    return Array.isArray(v) ? (v as Finding[]) : []
  } catch {
    return []
  }
}

function fmtTime(s?: string) {
  if (!s) return '—'
  const d = new Date(s)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return '—'
  return d.toLocaleString('zh-CN', { hour12: false })
}

function Textarea({ className, ...props }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return (
    <textarea
      className={cn(
        'flex w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background',
        'placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
        'disabled:cursor-not-allowed disabled:opacity-50 min-h-[96px] resize-y',
        className,
      )}
      {...props}
    />
  )
}

/** 报告需要更宽的阅读区，基础 Dialog 固定 max-w-lg，这里按同一视觉规范派生一个宽版弹层 */
function WideDialog({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  if (!open) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="fixed inset-0 bg-black/50" onClick={onClose} />
      <div className="relative z-50 w-full max-w-3xl rounded-xl border bg-background p-6 shadow-lg">
        <h2 className="text-lg font-semibold mb-4">{title}</h2>
        {children}
      </div>
    </div>
  )
}

// ----------------------------- 表单 -----------------------------

interface FormState {
  id?: string
  name: string
  template_id: string
  agent_id: string
  team_id: string
  prompt: string
  scope: string
  schedule: InspectionSchedule
  enabled: boolean
}

function emptyForm(): FormState {
  return { name: '', template_id: '', agent_id: '', team_id: '', prompt: '', scope: '', schedule: '@daily', enabled: true }
}

// ----------------------------- 页面 -----------------------------

export default function InspectionsPage() {
  const {
    inspectionTemplates, inspections, loading, runs, running, escalating,
    loadInspectionTemplates, loadInspections,
    createInspection, updateInspection, deleteInspection, runInspection, runExperiment, loadRuns, escalateRun,
  } = useInspections()
  const agents = useAIStore(s => s.agents)
  const fetchAgents = useAIStore(s => s.fetchAgents)
  const teams = useAIStore(s => s.teams)
  const loadTeams = useAIStore(s => s.loadTeams)

  const [formOpen, setFormOpen] = useState(false)
  const [form, setForm] = useState<FormState>(emptyForm())
  const [saving, setSaving] = useState(false)
  const [historyOf, setHistoryOf] = useState<Inspection | null>(null)
  const [report, setReport] = useState<InspectionRun | null>(null)
  const [expTarget, setExpTarget] = useState<Inspection | null>(null)
  const [expPrompt, setExpPrompt] = useState('')
  const [expScope, setExpScope] = useState('')

  useEffect(() => {
    loadInspectionTemplates()
    loadInspections()
    fetchAgents()
    loadTeams()
  }, [loadInspectionTemplates, loadInspections, fetchAgents, loadTeams])

  const agentOptions = useMemo(
    () => [
      { value: '', label: '默认（全部工具）' },
      ...agents.map((a: { id: string; name: string }) => ({ value: a.id, label: a.name })),
    ],
    [agents],
  )

  const templateOptions = useMemo(
    () => [
      { value: '', label: '自定义（不使用模板）' },
      ...inspectionTemplates.map(t => ({ value: t.id, label: `${t.name} · ${t.category}` })),
    ],
    [inspectionTemplates],
  )

  // 概览统计：巡检覆盖情况一眼可见
  const stats = useMemo(() => {
    const enabled = inspections.filter(i => i.enabled).length
    const failed = inspections.filter(i => i.last_status === 'failed').length
    const never = inspections.filter(i => !i.last_run_at).length
    return { total: inspections.length, enabled, failed, never }
  }, [inspections])

  const applyTemplate = (tplID: string) => {
    const tpl = inspectionTemplates.find(t => t.id === tplID)
    setForm(f => ({
      ...f,
      template_id: tplID,
      // 仅在用户尚未手工填写时用模板预填，避免覆盖已输入内容
      name: f.name || (tpl?.name ?? ''),
      prompt: tpl?.prompt ?? f.prompt,
      scope: f.scope || (tpl?.scope_hint ?? ''),
      schedule: (tpl?.default_schedule as InspectionSchedule) || f.schedule,
    }))
  }

  const openCreate = (tpl?: InspectionTemplate) => {
    const base = emptyForm()
    if (tpl) {
      base.template_id = tpl.id
      base.name = tpl.name
      base.prompt = tpl.prompt
      base.scope = tpl.scope_hint
      base.schedule = tpl.default_schedule
    }
    setForm(base)
    setFormOpen(true)
  }

  const openEdit = (i: Inspection) => {
    setForm({
      id: i.id,
      name: i.name,
      template_id: i.template_id || '',
      agent_id: i.agent_id || '',
      team_id: i.team_id || '',
      prompt: i.prompt,
      scope: i.scope,
      schedule: i.schedule,
      enabled: i.enabled,
    })
    setFormOpen(true)
  }

  const save = async () => {
    if (!form.name.trim()) return
    setSaving(true)
    const payload = {
      name: form.name.trim(),
      template_id: form.template_id || undefined,
      agent_id: form.agent_id || undefined,
      team_id: form.team_id || undefined,
      prompt: form.prompt,
      scope: form.scope,
      schedule: form.schedule,
      enabled: form.enabled,
    }
    const ok = form.id
      ? await updateInspection(form.id, payload)
      : await createInspection(payload)
    setSaving(false)
    if (ok) setFormOpen(false)
  }

  const toggleEnabled = async (i: Inspection, enabled: boolean) => {
    await updateInspection(i.id, {
      name: i.name,
      template_id: i.template_id,
      agent_id: i.agent_id,
      prompt: i.prompt,
      scope: i.scope,
      schedule: i.schedule,
      enabled,
    })
  }

  const openHistory = async (i: Inspection) => {
    setHistoryOf(i)
    await loadRuns(i.id)
  }

  // 将当前报告弹层中的发现转 ITSM 工单, 成功后用返回的最新运行记录刷新弹层内容
  const doEscalate = async (severity: 'high' | 'medium') => {
    if (!report) return
    const updated = await escalateRun(report.inspection_id, report.id, severity)
    if (updated) setReport(updated)
  }

  const openExperiment = (i: Inspection) => {
    setExpTarget(i)
    setExpPrompt(i.prompt)
    setExpScope(i.scope)
  }

  const confirmExperiment = async () => {
    if (!expTarget) return
    const meta = {
      prompt_override: expPrompt.trim() || undefined,
      scope_override: expScope.trim() || undefined,
    }
    const run = await runExperiment(expTarget.id, meta)
    if (run) setReport(run)
    setExpTarget(null)
  }

  return (
    <div className="p-6 space-y-5">
      {/* 页头 */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <RadarIcon className="h-6 w-6 text-primary" />
            主动巡检
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            让 Agent 按周期主动体检资产，自动发现风险并输出分级处置建议，而不是等人来问
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { loadInspections(); loadInspectionTemplates() }}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
          </button>
          <Button size="sm" onClick={() => openCreate()} className="gap-1">
            <Plus className="h-4 w-4" />新建巡检
          </Button>
        </div>
      </div>

      {/* 概览 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        {[
          { label: '巡检任务', value: stats.total, Icon: RadarIcon, cls: 'text-primary' },
          { label: '已启用', value: stats.enabled, Icon: CheckCircle2, cls: 'text-emerald-500' },
          { label: '最近失败', value: stats.failed, Icon: XCircle, cls: 'text-red-500' },
          { label: '尚未运行', value: stats.never, Icon: Clock, cls: 'text-amber-500' },
        ].map(s => (
          <Card key={s.label}>
            <CardContent className="p-4 flex items-center justify-between">
              <div>
                <div className="text-xs text-muted-foreground">{s.label}</div>
                <div className="text-2xl font-bold mt-0.5">{s.value}</div>
              </div>
              <s.Icon className={cn('h-8 w-8 opacity-20', s.cls)} />
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 巡检模板快捷入口 */}
      {inspectionTemplates.length > 0 && (
        <div>
          <div className="text-sm font-medium mb-2">从模板快速创建</div>
          <div className="flex flex-wrap gap-2">
            {inspectionTemplates.map(t => (
              <button
                key={t.id}
                onClick={() => openCreate(t)}
                className={cn(
                  'group text-left rounded-lg border border-border bg-card px-3 py-2',
                  'transition-all duration-200 hover:-translate-y-0.5 hover:border-primary/50 hover:shadow-sm',
                )}
              >
                <div className="text-sm font-medium group-hover:text-primary transition-colors">{t.name}</div>
                <div className="text-[11px] text-muted-foreground">{t.category} · {SCHEDULE_LABELS[t.default_schedule]}</div>
              </button>
            ))}
          </div>
        </div>
      )}

      {/* 任务列表 */}
      {inspections.length === 0 ? (
        <Card>
          <CardContent className="py-16 text-center space-y-3">
            <RadarIcon className="h-10 w-10 mx-auto text-muted-foreground/40" />
            <div className="text-muted-foreground">还没有巡检任务，从上方模板一键创建即可开始主动看护</div>
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-3">
          {inspections.map(i => {
            const meta = i.last_status ? STATUS_META[i.last_status] : undefined
            const isRunning = running.includes(i.id)
            return (
              <Card key={i.id} className="transition-all duration-200 hover:shadow-sm">
                <CardContent className="p-4">
                  <div className="flex items-start justify-between gap-4 flex-wrap">
                    <div className="min-w-0 flex-1 space-y-1.5">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-semibold">{i.name}</span>
                        <Badge variant="secondary">{SCHEDULE_LABELS[i.schedule] || i.schedule}</Badge>
                        {meta && (
                          <Badge variant={meta.variant} className="gap-1">
                            <meta.Icon className="h-3 w-3" />{meta.label}
                          </Badge>
                        )}
                        {!i.enabled && <Badge variant="outline">已停用</Badge>}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        范围：{i.scope || '全部受管资产'}
                      </div>
                      <div className="flex items-center gap-4 text-[11px] text-muted-foreground">
                        <span>上次运行：{fmtTime(i.last_run_at)}</span>
                        <span>下次运行：{i.enabled ? fmtTime(i.next_run_at) : '—'}</span>
                      </div>
                    </div>

                    <div className="flex items-center gap-2 shrink-0">
                      <Switch checked={i.enabled} onCheckedChange={v => toggleEnabled(i, v)} />
                      <Button size="sm" variant="outline" className="gap-1" disabled={isRunning} onClick={() => runInspection(i.id)}>
                        <Play className={cn('h-3.5 w-3.5', isRunning && 'animate-pulse')} />
                        {isRunning ? '巡检中…' : '立即巡检'}
                      </Button>
                      <Button size="sm" variant="ghost" className="gap-1 text-xs" title="对比实验(策略优化)" onClick={() => openExperiment(i)}>
                        <FlaskConical className="h-3.5 w-3.5" />实验
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => openHistory(i)} title="运行历史">
                        <History className="h-4 w-4" />
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => openEdit(i)} title="编辑">
                        <Edit className="h-4 w-4" />
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => deleteInspection(i.id)} title="删除">
                        <Trash2 className="h-4 w-4 text-red-500" />
                      </Button>
                    </div>
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}

      {/* 新建 / 编辑 */}
      <Dialog open={formOpen} onClose={() => setFormOpen(false)} title={form.id ? '编辑巡检任务' : '新建巡检任务'}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto pr-1">
          <div>
            <label className="text-xs text-muted-foreground">任务名称</label>
            <Input className="mt-1" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} placeholder="如：核心数据库容量日检" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-muted-foreground">巡检模板</label>
              <Select className="mt-1" options={templateOptions} value={form.template_id} onChange={e => applyTemplate(e.target.value)} />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">执行 Agent</label>
              <Select className="mt-1" options={agentOptions} value={form.agent_id} onChange={e => setForm({ ...form, agent_id: e.target.value })} />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">执行团队（多 Agent 协作）</label>
              <Select
                className="mt-1"
                options={[{ value: '', label: '不使用团队' }, ...teams.map(t => ({ value: t.id, label: `${t.name} (${t.mode})` }))]}
                value={form.team_id}
                onChange={e => setForm({ ...form, team_id: e.target.value })}
              />
              {form.team_id && <p className="text-[11px] text-muted-foreground mt-1">绑定团队后巡检由团队成员分工协作执行</p>}
            </div>
          </div>
          <div>
            <label className="text-xs text-muted-foreground">巡检范围</label>
            <Input className="mt-1" value={form.scope} onChange={e => setForm({ ...form, scope: e.target.value })} placeholder="如：全部主机 / 生产环境数据库" />
          </div>
          <div>
            <label className="text-xs text-muted-foreground">巡检指令（注入 Agent 的执行口径）</label>
            <Textarea className="mt-1" value={form.prompt} onChange={e => setForm({ ...form, prompt: e.target.value })} placeholder="描述这次巡检要检查什么、如何判定风险等级、输出什么" />
          </div>
          <div className="grid grid-cols-2 gap-3 items-end">
            <div>
              <label className="text-xs text-muted-foreground">执行周期</label>
              <Select
                className="mt-1"
                options={SCHEDULE_OPTIONS}
                value={form.schedule}
                onChange={e => setForm({ ...form, schedule: e.target.value as InspectionSchedule })}
              />
            </div>
            <div className="flex items-center gap-2 h-10">
              <Switch checked={form.enabled} onCheckedChange={v => setForm({ ...form, enabled: v })} />
              <span className="text-sm">创建后立即启用</span>
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setFormOpen(false)}>取消</Button>
          <Button onClick={save} disabled={saving || !form.name.trim()}>{saving ? '保存中…' : '保存'}</Button>
        </DialogFooter>
      </Dialog>

      {/* 运行历史 */}
      <WideDialog open={!!historyOf} onClose={() => setHistoryOf(null)} title={historyOf ? `${historyOf.name} · 运行历史` : ''}>
        <div className="max-h-[60vh] overflow-y-auto space-y-2 pr-1">
          {(runs[historyOf?.id || ''] || []).length === 0 ? (
            <div className="py-10 text-center text-sm text-muted-foreground">暂无运行记录</div>
          ) : (
            (runs[historyOf?.id || ''] || []).map(r => {
              const meta = STATUS_META[r.status]
              const findings = parseFindings(r.findings)
              const high = findings.filter(f => f.level === 'high').length
              return (
                <div key={r.id} className="rounded-lg border border-border p-3 space-y-2">
                  <div className="flex items-center justify-between gap-3 flex-wrap">
                    <div className="flex items-center gap-2">
                      {meta && <Badge variant={meta.variant} className="gap-1"><meta.Icon className="h-3 w-3" />{meta.label}</Badge>}
                      <Badge variant="outline">{r.triggered_by === 'manual' ? '手动' : r.triggered_by === 'experiment' ? '实验' : '定时'}</Badge>
                      {r.triggered_by === 'experiment' && (
                        <Badge variant="info" className="gap-1 text-[10px]">
                          <FlaskConical className="h-3 w-3" />A/B
                        </Badge>
                      )}
                      <span className="text-xs text-muted-foreground">{fmtTime(r.started_at)}</span>
                      {high > 0 && (
                        <span className="inline-flex items-center gap-1 text-xs text-red-500">
                          <AlertTriangle className="h-3 w-3" />{high} 项高风险
                        </span>
                      )}
                    </div>
                    <Button size="sm" variant="ghost" className="gap-1" onClick={() => setReport(r)}>
                      <FileText className="h-3.5 w-3.5" />查看报告
                    </Button>
                  </div>
                  {r.error && <div className="text-xs text-red-500 break-all">{r.error}</div>}
                  {r.summary && <div className="text-xs text-muted-foreground line-clamp-2">{r.summary}</div>}
                </div>
              )
            })
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setHistoryOf(null)}>关闭</Button>
        </DialogFooter>
      </WideDialog>

      {/* 巡检报告 */}
      <WideDialog open={!!report} onClose={() => setReport(null)} title="巡检报告">
        {report && (() => {
          const findings = parseFindings(report.findings)
          const escalations = report.escalations || []
          const escalatedIdx = new Set(escalations.map(e => e.finding_idx))
          const pendingCount = findings.filter((f, i) =>
            (f.level === 'high' || f.level === 'medium') && !escalatedIdx.has(i),
          ).length
          const isEscalating = escalating.includes(report.id)
          return (
            <div className="max-h-[65vh] overflow-y-auto space-y-4 pr-1">
              <div className="flex items-center gap-2 flex-wrap text-xs text-muted-foreground">
                <span>开始 {fmtTime(report.started_at)}</span>
                <span>·</span>
                <span>结束 {fmtTime(report.finished_at)}</span>
                {escalations.length > 0 && (
                  <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400">
                    <ArrowUpRight className="h-3 w-3" />已转 {escalations.length} 条工单
                  </span>
                )}
              </div>

              {findings.length > 0 && (
                <div className="space-y-2">
                  <div className="flex items-center justify-between gap-2 flex-wrap">
                    <div className="text-sm font-medium">发现清单</div>
                    {pendingCount > 0 && (
                      <Button
                        size="sm"
                        variant="outline"
                        className="gap-1 h-7 text-xs"
                        disabled={isEscalating}
                        onClick={() => doEscalate('medium')}
                      >
                        <ArrowUpRight className="h-3 w-3" />
                        {isEscalating ? '转单中…' : `转 ${pendingCount} 条高危/中危为工单`}
                      </Button>
                    )}
                  </div>
                  {findings.map((f, idx) => {
                    const lv = LEVEL_META[f.level || 'low'] || LEVEL_META.low
                    const escalated = escalations.find(e => e.finding_idx === idx)
                    return (
                      <div key={idx} className={cn('rounded-lg border p-3 space-y-1', lv.cls)}>
                        <div className="flex items-center gap-2 flex-wrap">
                          <span className="text-[11px] font-semibold px-1.5 py-0.5 rounded border border-current">{lv.label}</span>
                          <span className="text-sm font-medium">{f.title || '未命名发现'}</span>
                          {escalated && (
                            <Badge variant="success" className="gap-1 text-[10px]">
                              <ArrowUpRight className="h-3 w-3" />工单 {escalated.ticket_id.slice(0, 8)}
                            </Badge>
                          )}
                          {(f.level === 'high' || f.level === 'medium') && !escalated && (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="gap-1 h-6 text-xs ml-auto"
                              disabled={isEscalating}
                              onClick={() => doEscalate(f.level === 'high' ? 'high' : 'medium')}
                            >
                              <ArrowUpRight className="h-3 w-3" />转工单
                            </Button>
                          )}
                        </div>
                        {f.detail && <div className="text-xs opacity-90">{f.detail}</div>}
                        {f.suggestion && <div className="text-xs opacity-90">建议：{f.suggestion}</div>}
                      </div>
                    )
                  })}
                </div>
              )}

              <div className="space-y-1">
                <div className="text-sm font-medium">报告正文</div>
                <pre className="text-xs whitespace-pre-wrap bg-muted/50 rounded-md p-3 leading-relaxed">
                  {report.summary || report.error || '无内容'}
                </pre>
              </div>
            </div>
          )
        })()}
        <DialogFooter>
          <Button variant="outline" onClick={() => setReport(null)}>关闭</Button>
        </DialogFooter>
      </WideDialog>

      {/* 对比实验（策略优化 A/B） */}
      <Dialog open={!!expTarget} onClose={() => setExpTarget(null)} title="巡检对比实验">
        {expTarget && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              用覆盖的巡检指令与范围跑一次「{expTarget.name}」，结果带「实验」标记与基线并列，用于对比不同策略的巡检质量。
            </p>
            <div>
              <label className="text-xs text-muted-foreground">实验指令（覆盖巡检 prompt）</label>
              <Textarea className="mt-1" value={expPrompt} onChange={e => setExpPrompt(e.target.value)} placeholder="尝试不同的巡检口径…" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">实验范围（覆盖巡检 scope）</label>
              <Input className="mt-1" value={expScope} onChange={e => setExpScope(e.target.value)} placeholder="留空沿用任务范围" />
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setExpTarget(null)}>取消</Button>
          <Button onClick={confirmExperiment} disabled={running.includes(expTarget?.id || '')}>
            <FlaskConical className="h-4 w-4 mr-1" />运行实验
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
