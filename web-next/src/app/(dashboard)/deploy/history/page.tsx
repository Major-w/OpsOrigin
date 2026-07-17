'use client'

import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type ReleaseHistory, type ReleaseStatus } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DataList, type ViewMode, type Column, type FilterOption } from '@/components/ui/data-list'
import { History, RotateCcw, GitBranch, Clock, CheckCircle, XCircle, AlertCircle, Loader2 } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'

const STATUS_META: Record<ReleaseStatus, { label: string; color: string; icon: any }> = {
  draft: { label: '草稿', color: 'bg-slate-500/10 text-slate-600 border-slate-500/20', icon: History },
  pending_approval: { label: '待审批', color: 'bg-amber-500/10 text-amber-600 border-amber-500/20', icon: Clock },
  approved: { label: '已审批', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
  deploying: { label: '发布中', color: 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20', icon: Loader2 },
  succeeded: { label: '已成功', color: 'bg-emerald-500/10 text-emerald-600 border-emerald-500/20', icon: CheckCircle },
  failed: { label: '已失败', color: 'bg-red-500/10 text-red-600 border-red-500/20', icon: XCircle },
  rolled_back: { label: '已回滚', color: 'bg-orange-500/10 text-orange-600 border-orange-500/20', icon: RotateCcw },
  rejected: { label: '已驳回', color: 'bg-red-500/10 text-red-600 border-red-500/20', icon: XCircle },
}

const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }
const ENV_COLORS: Record<string, string> = {
  dev: 'bg-blue-500/10 text-blue-600 border-blue-500/20',
  test: 'bg-amber-500/10 text-amber-600 border-amber-500/20',
  staging: 'bg-purple-500/10 text-purple-600 border-purple-500/20',
  prod: 'bg-red-500/10 text-red-600 border-red-500/20',
}

const STRATEGY_LABELS: Record<string, string> = {
  blue_green: '蓝绿', canary: '金丝雀', rolling: '滚动', full: '全量',
}

function formatDuration(ms: number): string {
  if (!ms) return '-'
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`
  const m = Math.floor(ms / 60000)
  const s = Math.floor((ms % 60000) / 1000)
  return `${m}m${s}s`
}

export default function HistoryPage() {
  const { history, fetchHistory, rollbackHistory } = useDeployStore()
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [confirmRollback, setConfirmRollback] = useState<ReleaseHistory | null>(null)

  useEffect(() => { fetchHistory() }, [])

  const stats = useMemo(() => ({
    total: history.length,
    succeeded: history.filter(h => h.status === 'succeeded').length,
    rolledBack: history.filter(h => h.status === 'rolled_back').length,
    prod: history.filter(h => h.env === 'prod').length,
    avgDuration: history.length > 0 ? Math.round(history.reduce((s, h) => s + h.duration_ms, 0) / history.length) : 0,
  }), [history])

  const columns: Column<ReleaseHistory>[] = [
    { key: 'app_name', label: '应用', sortable: true, filterable: true },
    { key: 'version', label: '版本', sortable: true },
    { key: 'env', label: '环境', sortable: true, filterable: true },
    { key: 'strategy', label: '策略', sortable: true },
    { key: 'status', label: '状态', sortable: true, filterable: true },
    { key: 'deployed_by', label: '发布人', filterable: true },
    { key: 'started_at', label: '发布时间', sortable: true },
    { key: 'duration', label: '耗时', sortable: true },
  ]

  const filters: FilterOption[] = [
    { key: 'env', label: '环境', type: 'select', options: Object.entries(ENV_LABELS).map(([v, l]) => ({ label: l, value: v })) },
    { key: 'status', label: '状态', type: 'select', options: [{ label: '已成功', value: 'succeeded' }, { label: '已回滚', value: 'rolled_back' }] },
  ]

  const renderListItem = (h: ReleaseHistory) => {
    const sm = STATUS_META[h.status]
    const SIcon = sm.icon
    return (
      <>
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <SIcon className={cn('h-4 w-4 shrink-0', sm.color.split(' ')[1])} />
          <span className="text-sm font-medium truncate">{h.app_name}</span>
          <span className="text-[10px] text-muted-foreground/60 font-mono">{h.app_code}</span>
        </div>
        <div className="w-24 shrink-0 text-xs font-mono text-muted-foreground">{h.version}</div>
        <div className="w-16 shrink-0"><span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[h.env])}>{ENV_LABELS[h.env]}</span></div>
        <div className="w-16 shrink-0 text-xs text-muted-foreground hidden md:block">{STRATEGY_LABELS[h.strategy]}</div>
        <div className="w-20 shrink-0"><Badge variant="outline" className={cn("text-[10px] px-1.5 py-0", sm.color)}>{sm.label}</Badge></div>
        <div className="w-20 shrink-0 text-xs text-muted-foreground hidden lg:block">{h.deployed_by}</div>
        <div className="w-32 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(h.started_at)}</div>
        <div className="w-16 shrink-0 text-xs font-mono text-muted-foreground">{formatDuration(h.duration_ms)}</div>
        <div className="w-20 shrink-0 flex justify-end gap-1">
          {h.status === 'succeeded' && (
            <Button variant="ghost" size="icon" className="h-6 w-6 text-orange-500" onClick={(e) => { e.stopPropagation(); setConfirmRollback(h) }} title="回滚">
              <RotateCcw className="h-3 w-3" />
            </Button>
          )}
        </div>
      </>
    )
  }

  const renderGridItem = (h: ReleaseHistory) => {
    const sm = STATUS_META[h.status]
    const SIcon = sm.icon
    return (
      <Card key={h.id} className="hover:border-emerald-500/20 transition-colors">
        <CardHeader className="py-3 pb-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <SIcon className={cn('h-4 w-4 shrink-0', sm.color.split(' ')[1])} />
              <span className="text-sm font-medium truncate">{h.app_name}</span>
            </div>
            <Badge variant="outline" className={cn("text-[10px]", sm.color)}>{sm.label}</Badge>
          </div>
        </CardHeader>
        <CardContent className="space-y-2 pt-0">
          <div className="flex items-center gap-2 text-xs">
            <span className="font-mono text-muted-foreground">{h.version}</span>
            <span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[h.env])}>{ENV_LABELS[h.env]}</span>
            <span className="text-[10px] text-muted-foreground">{STRATEGY_LABELS[h.strategy]}</span>
          </div>
          {h.commit_msg && <p className="text-[10px] text-muted-foreground line-clamp-1 font-mono">{h.commit_msg}</p>}
          <div className="flex items-center justify-between text-[10px] text-muted-foreground/70 pt-1 border-t">
            <span>{h.deployed_by}</span>
            <span>{formatTime(h.started_at)}</span>
            <span className="font-mono">{formatDuration(h.duration_ms)}</span>
          </div>
          {h.status === 'succeeded' && (
            <Button variant="outline" size="sm" className="w-full h-6 text-xs text-orange-500" onClick={() => setConfirmRollback(h)}>
              <RotateCcw className="h-3 w-3 mr-1" />回滚到此版本
            </Button>
          )}
          {h.rolled_back_by && (
            <div className="text-[10px] text-orange-500 flex items-center gap-1">
              <RotateCcw className="h-2.5 w-2.5" />由 {h.rolled_back_by} 于 {formatTime(h.rolled_back_at)} 回滚
            </div>
          )}
        </CardContent>
      </Card>
    )
  }

  const handleRollback = async () => {
    if (!confirmRollback) return
    const ok = await rollbackHistory(confirmRollback.id)
    if (ok) setConfirmRollback(null)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">发布历史</h1>
          <p className="text-sm text-muted-foreground">所有发布记录的审计与回滚入口</p>
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><History className="h-3 w-3 inline mr-1" />发布记录</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><CheckCircle className="h-3 w-3 inline mr-1" />成功</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.succeeded}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><RotateCcw className="h-3 w-3 inline mr-1" />已回滚</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-orange-500">{stats.rolledBack}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><GitBranch className="h-3 w-3 inline mr-1" />生产发布</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats.prod}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Clock className="h-3 w-3 inline mr-1" />平均耗时</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-cyan-500">{formatDuration(stats.avgDuration)}</span></CardContent></Card>
      </div>

      <DataList
        data={history}
        columns={columns}
        viewMode={viewMode}
        filters={filters}
        listView={renderListItem}
        gridView={renderGridItem}
        emptyMessage="暂无发布历史"
        searchPlaceholder="搜索应用/版本/发布人..."
        onRefresh={() => fetchHistory()}
      />

      {/* 回滚确认 */}
      {confirmRollback && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setConfirmRollback(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-md mx-4 p-5 space-y-3" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold text-orange-500">确认回滚</h3>
            <div className="text-sm space-y-1.5">
              <p>应用: <span className="font-medium">{confirmRollback.app_name}</span></p>
              <p>回滚到版本: <span className="font-mono font-medium text-orange-500">{confirmRollback.version}</span></p>
              <p>环境: <span className={cn("inline-block px-1.5 py-0.5 rounded border text-[10px]", ENV_COLORS[confirmRollback.env])}>{ENV_LABELS[confirmRollback.env]}</span></p>
              <p>原 Commit: <span className="font-mono text-xs">{confirmRollback.commit_id}</span></p>
            </div>
            <p className="text-xs text-destructive">回滚将把当前版本替换为此历史版本。{confirmRollback.env === 'prod' && '生产环境回滚需谨慎!'}</p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" size="sm" onClick={() => setConfirmRollback(null)}>取消</Button>
              <Button variant="outline" size="sm" className="text-orange-500 border-orange-500/30 hover:bg-orange-500/10" onClick={handleRollback}><RotateCcw className="h-3.5 w-3.5 mr-1" />确认回滚</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
