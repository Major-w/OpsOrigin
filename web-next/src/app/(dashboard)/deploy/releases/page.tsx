'use client'

import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type ReleaseTicket, type ReleaseStatus, type DeployEnv, type ReleaseStrategy, type DeployEnvironment, type DeployMode } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DataList, type ViewMode, type Column, type FilterOption, type BulkAction } from '@/components/ui/data-list'
import { FileCheck2, Trash2, Check, Ban, Play, RotateCcw, GitBranch, Clock, AlertCircle, CheckCircle, XCircle, Loader2, Rocket, Box, Server, Container } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'

const STATUS_META: Record<ReleaseStatus, { label: string; color: string; icon: any }> = {
  draft: { label: '草稿', color: 'bg-slate-500/10 text-slate-600 border-slate-500/20', icon: FileCheck2 },
  pending_approval: { label: '待审批', color: 'bg-amber-500/10 text-amber-600 border-amber-500/20', icon: Clock },
  approved: { label: '已审批', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
  deploying: { label: '发布中', color: 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20', icon: Loader2 },
  succeeded: { label: '已成功', color: 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20', icon: CheckCircle },
  failed: { label: '已失败', color: 'bg-red-500/10 text-red-600 border-red-500/20', icon: XCircle },
  rolled_back: { label: '已回滚', color: 'bg-orange-500/10 text-orange-600 border-orange-500/20', icon: RotateCcw },
  rejected: { label: '已驳回', color: 'bg-red-500/10 text-red-600 border-red-500/20', icon: Ban },
}

const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }
const ENV_COLORS: Record<string, string> = {
  dev: 'bg-blue-500/10 text-blue-600 border-blue-500/20',
  test: 'bg-amber-500/10 text-amber-600 border-amber-500/20',
  staging: 'bg-purple-500/10 text-purple-600 border-purple-500/20',
  prod: 'bg-red-500/10 text-red-600 border-red-500/20',
}

const STRATEGY_LABELS: Record<string, string> = {
  blue_green: '蓝绿发布', canary: '金丝雀', rolling: '滚动更新', full: '全量发布',
}

const MODE_META: Record<DeployMode, { label: string; icon: any; color: string }> = {
  host: { label: '主机', icon: Server, color: 'bg-slate-500/10 text-slate-600 border-slate-500/20' },
  container: { label: '容器', icon: Container, color: 'bg-violet-500/10 text-violet-600 border-violet-500/20' },
}

const STEP_ICONS: Record<string, any> = {
  deploy: Play, health_check: CheckCircle, package: Box, notify: AlertCircle,
}

export default function ReleasesPage() {
  const { releases, environments, fetchReleases, fetchEnvs, approveRelease, rejectRelease, deployRelease, rollbackRelease, createRelease } = useDeployStore()
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [detail, setDetail] = useState<ReleaseTicket | null>(null)
  const [rejectTarget, setRejectTarget] = useState<ReleaseTicket | null>(null)
  const [rejectReason, setRejectReason] = useState('')
  const [createOpen, setCreateOpen] = useState(false)

  useEffect(() => { fetchReleases(); fetchEnvs() }, [])

  // 环境 type -> mode 映射（用于在发布单上标识主机/容器）
  const envModeOf = useMemo(() => {
    const m: Record<string, DeployMode> = {}
    environments.forEach(e => { m[e.type] = e.mode === 'container' ? 'container' : 'host' })
    return m
  }, [environments])

  const stats = useMemo(() => ({
    total: releases.length,
    pending: releases.filter(r => r.status === 'pending_approval').length,
    deploying: releases.filter(r => r.status === 'deploying').length,
    succeeded: releases.filter(r => r.status === 'succeeded').length,
    failed: releases.filter(r => r.status === 'failed' || r.status === 'rolled_back').length,
  }), [releases])

  const columns: Column<ReleaseTicket>[] = [
    { key: 'title', label: '发布标题', sortable: true, filterable: true },
    { key: 'app_name', label: '应用', sortable: true, filterable: true },
    { key: 'version', label: '版本', sortable: true },
    { key: 'env', label: '环境', sortable: true, filterable: true },
    { key: 'strategy', label: '策略', sortable: true },
    { key: 'status', label: '状态', sortable: true, filterable: true },
    { key: 'created_at', label: '创建时间', sortable: true },
  ]

  const filters: FilterOption[] = [
    { key: 'env', label: '环境', type: 'select', options: Object.entries(ENV_LABELS).map(([v, l]) => ({ label: l, value: v })) },
    { key: 'status', label: '状态', type: 'select', options: Object.entries(STATUS_META).map(([v, m]) => ({ label: m.label, value: v })) },
  ]

  const bulkActions: BulkAction[] = []

  const renderListItem = (rel: ReleaseTicket) => {
    const sm = STATUS_META[rel.status]
    const SIcon = sm.icon
    const mode = envModeOf[rel.env]
    const MIcon = mode ? MODE_META[mode].icon : Server
    return (
      <>
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <SIcon className={cn('h-4 w-4 shrink-0', rel.status === 'deploying' && 'animate-spin', sm.color.split(' ')[1])} />
          <span className="text-sm font-medium truncate cursor-pointer hover:text-emerald-500"
            onClick={(e) => { e.stopPropagation(); setDetail(rel) }}>
            {rel.title}
          </span>
        </div>
        <div className="w-28 shrink-0 text-xs text-muted-foreground truncate hidden lg:block">{rel.app_name}</div>
        <div className="w-20 shrink-0 text-xs font-mono text-muted-foreground">{rel.version}</div>
        <div className="w-16 shrink-0"><span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[rel.env])}>{ENV_LABELS[rel.env]}</span></div>
        {mode && <div className="w-14 shrink-0"><MIcon className={cn("h-3 w-3", MODE_META[mode].color.split(' ')[1])} /></div>}
        <div className="w-24 shrink-0 text-xs text-muted-foreground hidden md:block">{STRATEGY_LABELS[rel.strategy]}</div>
        <div className="w-20 shrink-0"><Badge variant="outline" className={cn("text-[10px] px-1.5 py-0", sm.color)}>{sm.label}</Badge></div>
        <div className="w-28 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(rel.created_at)}</div>
        <div className="w-32 shrink-0 flex justify-end gap-1">
          {rel.status === 'pending_approval' && (
            <>
              <Button variant="ghost" size="icon" className="h-6 w-6 text-emerald-500" onClick={(e) => { e.stopPropagation(); approveRelease(rel.id) }} title="审批通过">
                <Check className="h-3 w-3" />
              </Button>
              <Button variant="ghost" size="icon" className="h-6 w-6 text-destructive" onClick={(e) => { e.stopPropagation(); setRejectTarget(rel); setRejectReason('') }} title="驳回">
                <Ban className="h-3 w-3" />
              </Button>
            </>
          )}
          {rel.status === 'approved' && (
            <Button variant="ghost" size="icon" className="h-6 w-6 text-cyan-500" onClick={(e) => { e.stopPropagation(); deployRelease(rel.id) }} title="执行发布">
              <Play className="h-3 w-3" />
            </Button>
          )}
          {(rel.status === 'succeeded' || rel.status === 'rolled_back') && (
            <Button variant="ghost" size="icon" className="h-6 w-6 text-orange-500" onClick={(e) => { e.stopPropagation(); rollbackRelease(rel.id) }} title="回滚">
              <RotateCcw className="h-3 w-3" />
            </Button>
          )}
          <Button variant="ghost" size="icon" className="h-6 w-6" onClick={(e) => { e.stopPropagation(); setDetail(rel) }} title="详情">
            <FileCheck2 className="h-3 w-3" />
          </Button>
        </div>
      </>
    )
  }

  const renderGridItem = (rel: ReleaseTicket) => {
    const sm = STATUS_META[rel.status]
    const SIcon = sm.icon
    const mode = envModeOf[rel.env]
    const MIcon = mode ? MODE_META[mode].icon : Server
    return (
      <Card key={rel.id} className="hover:border-emerald-500/20 transition-colors cursor-pointer" onClick={() => setDetail(rel)}>
        <CardHeader className="py-3 pb-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <SIcon className={cn('h-4 w-4 shrink-0', rel.status === 'deploying' && 'animate-spin', sm.color.split(' ')[1])} />
              <span className="text-sm font-medium truncate">{rel.title}</span>
            </div>
            <Badge variant="outline" className={cn("text-[10px]", sm.color)}>{sm.label}</Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-2 pt-0">
          <div className="flex items-center gap-2 text-xs">
            <span className="text-muted-foreground">{rel.app_name}</span>
            <span className="font-mono text-muted-foreground/70">{rel.version}</span>
            {mode && <MIcon className={cn("h-3 w-3", MODE_META[mode].color.split(' ')[1])} />}
          </div>
          <div className="flex items-center gap-2">
            <span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[rel.env])}>{ENV_LABELS[rel.env]}</span>
            <span className="text-[10px] text-muted-foreground">{STRATEGY_LABELS[rel.strategy]}</span>
          </div>
          {rel.commit_msg && <p className="text-[10px] text-muted-foreground line-clamp-1 font-mono">{rel.commit_msg}</p>}
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground/70 pt-1 border-t">
            <span>{rel.operator}</span>
            <span>·</span>
            <span>{formatTime(rel.created_at)}</span>
          </div>
        </CardContent>
      </Card>
    )
  }

  const handleReject = async () => {
    if (!rejectTarget) return
    const ok = await rejectRelease(rejectTarget.id, rejectReason || '未填写原因')
    if (ok) { setRejectTarget(null); setRejectReason('') }
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">发布单</h1>
          <p className="text-sm text-muted-foreground">应用版本发布流程，含审批、执行、回滚；支持主机环境与容器环境</p>
        </div>
        <Button size="sm" onClick={() => setCreateOpen(true)}><Rocket className="h-3.5 w-3.5 mr-1" />新建发布单</Button>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><FileCheck2 className="h-3 w-3 inline mr-1" />发布单总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Clock className="h-3 w-3 inline mr-1" />待审批</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-amber-500">{stats.pending}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Loader2 className="h-3 w-3 inline mr-1" />发布中</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-cyan-500">{stats.deploying}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><CheckCircle className="h-3 w-3 inline mr-1" />已成功</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.succeeded}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><XCircle className="h-3 w-3 inline mr-1" />失败/回滚</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats.failed}</span></CardContent></Card>
      </div>

      <DataList
        data={releases}
        columns={columns}
        viewMode={viewMode}
        filters={filters}
        bulkActions={bulkActions}
        listView={renderListItem}
        gridView={renderGridItem}
        emptyMessage="暂无发布单"
        searchPlaceholder="搜索发布标题/应用/版本..."
        onRefresh={() => { fetchReleases(); fetchEnvs() }}
      />

      {/* 详情 Dialog */}
      {detail && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setDetail(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-2xl mx-4 p-5 space-y-3 max-h-[85vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-bold">{detail.title}</h3>
              <Badge variant="outline" className={cn("text-[10px]", STATUS_META[detail.status].color)}>{STATUS_META[detail.status].label}</Badge>
            </div>
            <div className="grid grid-cols-3 gap-2 text-xs">
              <div><span className="text-muted-foreground">应用</span><div className="font-medium">{detail.app_name}</div></div>
              <div><span className="text-muted-foreground">版本</span><div className="font-mono font-medium">{detail.version}</div></div>
              <div><span className="text-muted-foreground">环境</span><div><span className={cn("inline-block px-1.5 py-0.5 rounded border text-[10px]", ENV_COLORS[detail.env])}>{ENV_LABELS[detail.env]}</span></div></div>
              <div><span className="text-muted-foreground">策略</span><div>{STRATEGY_LABELS[detail.strategy]}</div></div>
              <div><span className="text-muted-foreground">Commit</span><div className="font-mono">{detail.commit_id}</div></div>
              <div><span className="text-muted-foreground">发起人</span><div>{detail.operator}</div></div>
            </div>
            {detail.commit_msg && <div><span className="text-xs text-muted-foreground">提交信息</span><p className="text-xs font-mono mt-1 bg-muted/30 rounded p-2">{detail.commit_msg}</p></div>}
            {detail.changelog && <div><span className="text-xs text-muted-foreground">变更日志</span><pre className="text-xs mt-1 bg-muted/30 rounded p-2 whitespace-pre-wrap">{detail.changelog}</pre></div>}
            {detail.approved_by && <div className="text-xs text-muted-foreground">审批人: <span className="font-medium text-foreground">{detail.approved_by}</span> · {formatTime(detail.approved_at || '')}</div>}
            {detail.reject_reason && <div className="text-xs text-destructive">驳回原因: {detail.reject_reason}</div>}

            {/* 部署步骤 */}
            <div>
              <span className="text-xs font-medium">部署步骤</span>
              <div className="mt-2 space-y-1.5">
                {(detail.steps || []).map(step => {
                  const StepIcon = STEP_ICONS[step.id] || FileCheck2
                  const stepColor = step.status === 'succeeded' ? 'text-emerald-500' : step.status === 'failed' ? 'text-red-500' : step.status === 'running' ? 'text-cyan-500' : step.status === 'skipped' ? 'text-muted-foreground' : 'text-muted-foreground'
                  return (
                    <div key={step.id} className="flex items-center gap-2 text-xs">
                      <StepIcon className={cn('h-3 w-3', stepColor, step.status === 'running' && 'animate-spin')} />
                      <span className="flex-1">{step.name}</span>
                      <span className={cn(stepColor)}>{step.status === 'succeeded' ? '成功' : step.status === 'failed' ? '失败' : step.status === 'running' ? '进行中' : step.status === 'skipped' ? '跳过' : '等待'}</span>
                    </div>
                  )
                })}
              </div>
              {(detail.steps || []).some(s => s.output) && (
                <div className="mt-2 space-y-1">
                  {(detail.steps || []).filter(s => s.output).map(s => (
                    <div key={s.id} className="text-[10px] font-mono bg-muted/30 rounded p-1.5">
                      <span className="text-muted-foreground">{s.name}: </span>{s.output}
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="flex justify-end gap-2 pt-2 border-t">
              {detail.status === 'pending_approval' && (
                <>
                  <Button variant="destructive" size="sm" onClick={() => { setRejectTarget(detail); setRejectReason(''); setDetail(null) }}><Ban className="h-3.5 w-3.5 mr-1" />驳回</Button>
                  <Button size="sm" onClick={() => { approveRelease(detail.id); setDetail(null) }}><Check className="h-3.5 w-3.5 mr-1" />审批通过</Button>
                </>
              )}
              {detail.status === 'approved' && (
                <Button size="sm" onClick={() => { deployRelease(detail.id); setDetail(null) }}><Play className="h-3.5 w-3.5 mr-1" />执行发布</Button>
              )}
              {(detail.status === 'succeeded' || detail.status === 'rolled_back') && (
                <Button variant="outline" size="sm" onClick={() => { rollbackRelease(detail.id); setDetail(null) }}><RotateCcw className="h-3.5 w-3.5 mr-1" />回滚</Button>
              )}
              <Button variant="outline" size="sm" onClick={() => setDetail(null)}>关闭</Button>
            </div>
          </div>
        </div>
      )}

      {/* 驳回 Dialog */}
      {rejectTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setRejectTarget(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-sm mx-4 p-5 space-y-3" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold text-destructive">驳回发布单</h3>
            <p className="text-sm text-muted-foreground">驳回 <span className="font-medium text-foreground">{rejectTarget.title}</span></p>
            <div><label className="text-xs font-medium">驳回原因</label><textarea className="mt-1 flex w-full rounded-md border border-input bg-background px-3 py-1 text-sm" rows={3} value={rejectReason} onChange={e => setRejectReason(e.target.value)} placeholder="请填写驳回原因" /></div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setRejectTarget(null)}>取消</Button>
              <Button variant="destructive" size="sm" onClick={handleReject}><Ban className="h-3.5 w-3.5 mr-1" />确认驳回</Button>
            </div>
          </div>
        </div>
      )}

      {/* 新建发布单 Dialog */}
      {createOpen && (
        <CreateReleaseDialog onClose={() => setCreateOpen(false)} onSubmit={async (d) => {
          const ok = await createRelease(d)
          if (ok) setCreateOpen(false)
        }} />
      )}
    </div>
  )
}

function CreateReleaseDialog({ onClose, onSubmit }: { onClose: () => void; onSubmit: (d: any) => Promise<void> }) {
  const { apps, environments } = useDeployStore()
  const [form, setForm] = useState({
    app_id: '', version: '', env: 'dev', strategy: 'rolling',
    changelog: '', approvers: '',
  })
  const app = apps.find(a => a.id === form.app_id)

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onClose}>
      <div className="bg-background rounded-lg border shadow-lg w-full max-w-lg mx-4 p-5 space-y-3" onClick={e => e.stopPropagation()}>
        <h3 className="text-sm font-bold">新建发布单</h3>
        <div className="grid grid-cols-2 gap-3">
          <div className="col-span-2">
            <label className="text-xs font-medium">应用 *</label>
            <select className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.app_id} onChange={e => setForm(f => ({ ...f, app_id: e.target.value }))}>
              <option value="">请选择应用</option>
              {apps.map(a => <option key={a.id} value={a.id}>{a.name}（{a.code}）</option>)}
            </select>
          </div>
          <div>
            <label className="text-xs font-medium">版本 *</label>
            <input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm font-mono" value={form.version} onChange={e => setForm(f => ({ ...f, version: e.target.value }))} placeholder="v1.2.0" />
          </div>
          <div>
            <label className="text-xs font-medium">环境 *</label>
            <select className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.env} onChange={e => setForm(f => ({ ...f, env: e.target.value }))}>
              {Object.entries(ENV_LABELS).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
            </select>
          </div>
          <div>
            <label className="text-xs font-medium">发布策略</label>
            <select className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.strategy} onChange={e => setForm(f => ({ ...f, strategy: e.target.value }))}>
              {Object.entries(STRATEGY_LABELS).map(([v, l]) => <option key={v} value={v}>{l}</option>)}
            </select>
          </div>
          <div>
            <label className="text-xs font-medium">审批人（逗号分隔）</label>
            <input className="mt-1 flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm" value={form.approvers} onChange={e => setForm(f => ({ ...f, approvers: e.target.value }))} placeholder="alice, bob" />
          </div>
          <div className="col-span-2">
            <label className="text-xs font-medium">变更日志</label>
            <textarea className="mt-1 flex w-full rounded-md border border-input bg-background px-3 py-1 text-sm" rows={3} value={form.changelog} onChange={e => setForm(f => ({ ...f, changelog: e.target.value }))} placeholder="本次变更内容..." />
          </div>
        </div>
        {app && (
          <div className="text-[10px] text-muted-foreground bg-muted/30 rounded p-2">
            部署方式：
            {environments.find(e => e.type === form.env)?.mode === 'container'
              ? '容器模式（使用应用镜像配置经 K8s 网关 Agent 下发 kubectl）'
              : '主机模式（经绑定 Agent 下发部署脚本）'}
          </div>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={onClose}>取消</Button>
          <Button size="sm" disabled={!form.app_id || !form.version} onClick={() => onSubmit({
            app_id: form.app_id,
            title: `${app?.name || ''} ${form.version}`,
            version: form.version,
            env: form.env,
            strategy: form.strategy,
            changelog: form.changelog,
            approvers: form.approvers.split(',').map(s => s.trim()).filter(Boolean),
          })}><Rocket className="h-3.5 w-3.5 mr-1" />创建</Button>
        </div>
      </div>
    </div>
  )
}
