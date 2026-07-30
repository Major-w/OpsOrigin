'use client'

import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type ReleaseHistory, type ReleaseStatus } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { DataList, type ViewMode, type Column, type FilterOption } from '@/components/ui/data-list'
import { History, RotateCcw, Clock, CheckCircle, XCircle, AlertCircle, Loader2 } from 'lucide-react'
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

export default function HistoryPage() {
  const { history, fetchHistory, rollbackRelease } = useDeployStore()
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [detail, setDetail] = useState<ReleaseHistory | null>(null)
  const [confirmRollback, setConfirmRollback] = useState<ReleaseHistory | null>(null)

  useEffect(() => { fetchHistory() }, [])

  const stats = useMemo(() => ({
    total: history.length,
    succeeded: history.filter(h => h.status === 'succeeded').length,
    rolledBack: history.filter(h => h.status === 'rolled_back').length,
    prod: history.filter(h => h.env === 'prod').length,
  }), [history])

  const columns: Column<ReleaseHistory>[] = [
    { key: 'app_name', label: '应用', sortable: true, filterable: true },
    { key: 'version', label: '版本', sortable: true },
    { key: 'env', label: '环境', sortable: true, filterable: true },
    { key: 'status', label: '状态', sortable: true, filterable: true },
    { key: 'operator', label: '发布人', filterable: true },
    { key: 'created_at', label: '发布时间', sortable: true },
  ]

  const filters: FilterOption[] = [
    { key: 'env', label: '环境', type: 'select', options: Object.entries(ENV_LABELS).map(([v, l]) => ({ label: l, value: v })) },
    { key: 'status', label: '状态', type: 'select', options: [{ label: '已成功', value: 'succeeded' }, { label: '已回滚', value: 'rolled_back' }, { label: '已失败', value: 'failed' }] },
  ]

  const renderListItem = (h: ReleaseHistory) => {
    const sm = STATUS_META[h.status]
    const SIcon = sm.icon
    return (
      <>
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <SIcon className={cn('h-4 w-4 shrink-0', sm.color.split(' ')[1])} />
          <span className="text-sm font-medium truncate cursor-pointer hover:text-emerald-500" onClick={(e) => { e.stopPropagation(); setDetail(h) }}>{h.app_name}</span>
        </div>
        <div className="w-24 shrink-0 text-xs font-mono text-muted-foreground">{h.version}</div>
        <div className="w-16 shrink-0"><span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[h.env])}>{ENV_LABELS[h.env]}</span></div>
        <div className="w-20 shrink-0"><Badge variant="outline" className={cn("text-[10px] px-1.5 py-0", sm.color)}>{sm.label}</Badge></div>
        <div className="w-20 shrink-0 text-xs text-muted-foreground hidden lg:block">{h.operator}</div>
        <div className="w-32 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(h.created_at)}</div>
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
      <Card key={h.id} className="hover:border-emerald-500/20 transition-colors cursor-pointer" onClick={() => setDetail(h)}>
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
          </div>
          <div className="flex items-center justify-between text-[10px] text-muted-foreground/70 pt-1 border-t">
            <span>{h.operator}</span>
            <span>{formatTime(h.created_at)}</span>
          </div>
          {h.status === 'succeeded' && (
            <Button variant="outline" size="sm" className="w-full h-6 text-xs text-orange-500" onClick={(e) => { e.stopPropagation(); setConfirmRollback(h) }}>
              <RotateCcw className="h-3 w-3 mr-1" />回滚到此版本
            </Button>
          )}
        </CardContent>
      </Card>
    )
  }

  const handleRollback = async () => {
    if (!confirmRollback) return
    if (!confirmRollback.release_id) { toast.error('该历史记录缺少关联发布单，无法回滚'); return }
    const ok = await rollbackRelease(confirmRollback.release_id)
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

      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><History className="h-3 w-3 inline mr-1" />发布记录</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{stats.total}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><CheckCircle className="h-3 w-3 inline mr-1" />成功</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{stats.succeeded}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><RotateCcw className="h-3 w-3 inline mr-1" />已回滚</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-orange-500">{stats.rolledBack}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Clock className="h-3 w-3 inline mr-1" />生产发布</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-red-500">{stats.prod}</span></CardContent></Card>
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

      {/* 详情 Dialog */}
      {detail && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setDetail(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-lg mx-4 p-5 space-y-3 max-h-[85vh] overflow-y-auto" onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-bold">{detail.app_name}</h3>
              <Badge variant="outline" className={cn("text-[10px]", STATUS_META[detail.status].color)}>{STATUS_META[detail.status].label}</Badge>
            </div>
            <div className="grid grid-cols-3 gap-2 text-xs">
              <div><span className="text-muted-foreground">版本</span><div className="font-mono font-medium">{detail.version}</div></div>
              <div><span className="text-muted-foreground">环境</span><div><span className={cn("inline-block px-1.5 py-0.5 rounded border text-[10px]", ENV_COLORS[detail.env])}>{ENV_LABELS[detail.env]}</span></div></div>
              <div><span className="text-muted-foreground">发布人</span><div>{detail.operator}</div></div>
            </div>
            {detail.output && (
              <div>
                <span className="text-xs text-muted-foreground">部署输出</span>
                <pre className="text-[10px] font-mono mt-1 bg-muted/30 rounded p-2 whitespace-pre-wrap max-h-48 overflow-y-auto">{detail.output}</pre>
              </div>
            )}
            <div className="flex justify-end gap-2 pt-2 border-t">
              {detail.status === 'succeeded' && detail.release_id && (
                <Button variant="outline" size="sm" onClick={() => { setConfirmRollback(detail); setDetail(null) }}><RotateCcw className="h-3.5 w-3.5 mr-1" />回滚</Button>
              )}
              <Button variant="outline" size="sm" onClick={() => setDetail(null)}>关闭</Button>
            </div>
          </div>
        </div>
      )}

      {/* 回滚确认 */}
      {confirmRollback && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={() => setConfirmRollback(null)}>
          <div className="bg-background rounded-lg border shadow-lg w-full max-w-md mx-4 p-5 space-y-3" onClick={e => e.stopPropagation()}>
            <h3 className="text-sm font-bold text-orange-500">确认回滚</h3>
            <div className="text-sm space-y-1.5">
              <p>应用: <span className="font-medium">{confirmRollback.app_name}</span></p>
              <p>回滚到版本: <span className="font-mono font-medium text-orange-500">{confirmRollback.version}</span></p>
              <p>环境: <span className={cn("inline-block px-1.5 py-0.5 rounded border text-[10px]", ENV_COLORS[confirmRollback.env])}>{ENV_LABELS[confirmRollback.env]}</span></p>
            </div>
            <p className="text-xs text-destructive">回滚将把该应用此环境的当前版本替换为此历史版本。{confirmRollback.env === 'prod' && '生产环境回滚需谨慎！'}</p>
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
