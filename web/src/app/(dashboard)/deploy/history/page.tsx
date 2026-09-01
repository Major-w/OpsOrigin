
import { useEffect, useState, useMemo } from 'react'
import { useDeployStore, type ReleaseHistory, type ReleaseStatus } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Badge, Modal, Tag, Popconfirm, message } from 'antd'
import { DataList, type ViewMode, type Column, type FilterOption } from '@/components/ui/data-list'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { History, RotateCcw, Clock, CheckCircle, XCircle, AlertCircle, Loader2 } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { getMessage } from '@/lib/message'
const STATUS_META: Record<ReleaseStatus, { label: string; color: string; icon: any }> = {
  draft: { label: '草稿', color: 'bg-slate-500/10 text-slate-600 border-slate-500/20', icon: History },
  pending_approval: { label: '待审批', color: 'bg-amber-500/10 text-amber-600 border-amber-500/20', icon: Clock },
  approved: { label: '已审批', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
  deploying: { label: '发布中', color: 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20', icon: Loader2 },
  succeeded: { label: '已成功', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
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
  const { history, loading, fetchHistory, rollbackRelease } = useDeployStore()
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
          <span className="text-sm font-medium truncate cursor-pointer hover:text-blue-500" onClick={(e) => { e.stopPropagation(); setDetail(h) }}>{h.app_name}</span>
        </div>
        <div className="w-24 shrink-0 text-xs font-mono text-muted-foreground">{h.version}</div>
        <div className="w-16 shrink-0"><span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[h.env])}>{ENV_LABELS[h.env]}</span></div>
        <div className="w-20 shrink-0"><Tag className={cn("text-[10px] px-1.5 py-0", sm.color)}>{sm.label}</Tag></div>
        <div className="w-20 shrink-0 text-xs text-muted-foreground hidden lg:block">{h.operator}</div>
        <div className="w-32 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(h.created_at)}</div>
        <div className="w-20 shrink-0 flex justify-end gap-1">
          {h.status === 'succeeded' && (
            <Button type="text" size="middle" className="text-orange-500" onClick={(e) => { e.stopPropagation(); setConfirmRollback(h) }} title="回滚">
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
      <Card key={h.id} className="hover:border-blue-500/20 transition-colors cursor-pointer" onClick={() => setDetail(h)}>
        <CardHeader className="py-3 pb-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <SIcon className={cn('h-4 w-4 shrink-0', sm.color.split(' ')[1])} />
              <span className="text-sm font-medium truncate">{h.app_name}</span>
            </div>
            <Tag className={cn("text-[10px]", sm.color)}>{sm.label}</Tag>
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
            <Button size="middle" className="w-full h-6 text-xs text-orange-500" onClick={(e) => { e.stopPropagation(); setConfirmRollback(h) }}>
              <RotateCcw className="h-3 w-3 mr-1" />回滚到此版本
            </Button>
          )}
        </CardContent>
      </Card>
    )
  }

  const handleRollback = async () => {
    if (!confirmRollback) return
    if (!confirmRollback.release_id) { getMessage().error('该历史记录缺少关联发布单，无法回滚'); return }
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

      <StatisticCardGroup loading={loading} columns={4}>
        <StatisticCard title="发布记录" value={stats.total} icon={<History className="h-3 w-3" />} />
        <StatisticCard title="成功" value={stats.succeeded} icon={<CheckCircle className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="已回滚" value={stats.rolledBack} icon={<RotateCcw className="h-3 w-3" />} className="text-orange-500" />
        <StatisticCard title="生产发布" value={stats.prod} icon={<Clock className="h-3 w-3" />} className="text-red-500" />
      </StatisticCardGroup>

      <DataList
        data={history}
        loading={loading}
        columns={columns}
        viewMode={viewMode}
        filters={filters}
        listView={renderListItem}
        gridView={renderGridItem}
        emptyMessage="暂无发布历史"
        searchPlaceholder="搜索应用/版本/发布人..."
        onRefresh={() => fetchHistory()}
      />

      <Modal title={detail?.app_name} open={!!detail} onCancel={() => setDetail(null)} footer={null} width={640}>
        {detail && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Tag className={cn("text-[10px]", STATUS_META[detail.status].color)}>{STATUS_META[detail.status].label}</Tag>
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
                <Button size="middle" onClick={() => { setConfirmRollback(detail); setDetail(null) }}><RotateCcw className="h-3.5 w-3.5 mr-1" />回滚</Button>
              )}
              <Button size="middle" onClick={() => setDetail(null)}>关闭</Button>
            </div>
          </div>
        )}
      </Modal>

      <Modal title="确认回滚" open={!!confirmRollback} onCancel={() => setConfirmRollback(null)} footer={null}>
        {confirmRollback && (
          <div className="space-y-3">
            <div className="text-sm space-y-1.5">
              <p>应用: <span className="font-medium">{confirmRollback.app_name}</span></p>
              <p>回滚到版本: <span className="font-mono font-medium text-orange-500">{confirmRollback.version}</span></p>
              <p>环境: <span className={cn("inline-block px-1.5 py-0.5 rounded border text-[10px]", ENV_COLORS[confirmRollback.env])}>{ENV_LABELS[confirmRollback.env]}</span></p>
            </div>
            <p className="text-xs text-destructive">回滚将把该应用此环境的当前版本替换为此历史版本。{confirmRollback.env === 'prod' && '生产环境回滚需谨慎！'}</p>
            <div className="flex justify-end gap-2">
              <Button onClick={() => setConfirmRollback(null)}>取消</Button>
              <Button size="middle" className="text-orange-500 border-orange-500/30 hover:bg-orange-500/10" onClick={handleRollback}><RotateCcw className="h-3.5 w-3.5 mr-1" />确认回滚</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
