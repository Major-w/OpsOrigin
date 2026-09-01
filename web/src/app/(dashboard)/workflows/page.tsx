
import { useEffect, useState, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { useWorkflowStore, type Workflow, type WorkflowStep, type StepType, type StepApproval } from '@/stores/workflow'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { DataList, type ViewMode, type Column, type FilterOption, type BulkAction } from '@/components/ui/data-list'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { Plus, Trash2, GitBranch, Code, CheckSquare, Split, Clock, Workflow as WorkflowIcon, FileText, Check, Ban, Flag, Loader2, Pencil, Play } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { Button, Tag, message } from 'antd'
const NODE_META: Record<string, { label: string; icon: any; color: string }> = {
  script:    { label: '脚本节点',  icon: Code,      color: 'text-blue-500' },
  approval:  { label: '人工审批',  icon: CheckSquare, color: 'text-amber-500' },
  condition: { label: '分支网关',  icon: GitBranch, color: 'text-purple-500' },
  parallel:  { label: '并行网关',  icon: Split,     color: 'text-cyan-500' },
  wait:      { label: '等待节点',  icon: Clock,     color: 'text-slate-500' },
}

export default function WorkflowsPage() {
  const navigate = useNavigate()
  const {
    workflows, approvals, loading,
    fetch, remove, fetchApprovals, approve, reject,
  } = useWorkflowStore()
  const [viewMode, setViewMode] = useState<ViewMode>('list')
  const [confirmDelete, setConfirmDelete] = useState<Workflow | null>(null)
  // 审批面板：列表页内联显示待审批任务
  const [showApprovals, setShowApprovals] = useState(false)
  const [approvalComments, setApprovalComments] = useState<Record<string, string>>({})

  useEffect(() => {
    fetch()
    fetchApprovals()
  }, [])

  // 统计卡片
  const stats = useMemo(() => {
    const total = workflows.length
    const scriptCount = workflows.reduce((acc, w) => acc + (w.steps || []).filter(s => s.type === 'script').length, 0)
    const approvalCount = workflows.reduce((acc, w) => acc + (w.steps || []).filter(s => s.type === 'approval').length, 0)
    const pendingApprovals = approvals.filter(a => a.status === 'pending').length
    const stepsTotal = workflows.reduce((acc, w) => acc + (w.steps || []).length, 0)
    return { total, scriptCount, approvalCount, pendingApprovals, stepsTotal }
  }, [workflows, approvals])

  const pendingApprovals = approvals.filter(a => a.status === 'pending')

  // DataList 列定义
  const columns: Column<Workflow>[] = [
    { key: 'name', label: '流程名称', sortable: true, filterable: true },
    { key: 'steps', label: '步骤数', sortable: true },
    { key: 'description', label: '描述', filterable: true },
    { key: 'created_at', label: '创建时间', sortable: true },
    { key: 'updated_at', label: '更新时间', sortable: true },
  ]

  // 筛选
  const filters: FilterOption[] = [
    {
      key: 'name', label: '名称', type: 'text',
    },
  ]

  // 批量操作
  const bulkActions: BulkAction[] = [
    {
      key: 'delete',
      label: '批量删除',
      icon: <Trash2 className="h-3 w-3 mr-1" />,
      variant: 'destructive',
      confirmMessage: '确认删除选中的流程？相关执行历史将一并删除。',
      onClick: async (items) => {
        for (const item of items) { await remove((item as Workflow).id) }
      },
    },
  ]

  // 行渲染
  const renderListItem = (wf: Workflow) => {
    const steps = wf.steps || []
    const scriptNodes = steps.filter(s => s.type === 'script').length
    const approvalNodes = steps.filter(s => s.type === 'approval').length
    return (
      <>
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <WorkflowIcon className="h-4 w-4 text-blue-400 shrink-0" />
          <span
            className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
            onClick={(e) => { e.stopPropagation(); navigate(`/workflows/${wf.id}`) }}
          >
            {wf.name}
          </span>
        </div>
        <div className="w-20 shrink-0 flex items-center gap-1">
          <Tag color="default">{steps.length} 步</Tag>
        </div>
        <div className="w-32 shrink-0 hidden lg:flex items-center gap-1.5">
          {scriptNodes > 0 && (
            <span className="text-[10px] text-muted-foreground flex items-center gap-0.5" title="脚本节点">
              <Code className="h-2.5 w-2.5 text-blue-500" />{scriptNodes}
            </span>
          )}
          {approvalNodes > 0 && (
            <span className="text-[10px] text-muted-foreground flex items-center gap-0.5" title="审批节点">
              <CheckSquare className="h-2.5 w-2.5 text-amber-500" />{approvalNodes}
            </span>
          )}
          {steps.length === 0 && (
            <span className="text-[10px] text-muted-foreground/60 italic">未编排</span>
          )}
        </div>
        <div className="flex-1 min-w-0 truncate text-xs text-muted-foreground hidden md:block">
          {wf.description || '-'}
        </div>
        <div className="w-28 shrink-0 text-xs text-muted-foreground/70 hidden md:block">{formatTime(wf.updated_at)}</div>
        <div className="w-28 shrink-0 flex justify-end gap-1">
          <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={(e) => { e.stopPropagation(); navigate(`/workflows/${wf.id}`) }} title="编辑">
            <Pencil className="h-3 w-3" />
          </Button>
          <Button type="text" size="middle" className="h-6 w-6 text-destructive" onClick={(e) => { e.stopPropagation(); setConfirmDelete(wf) }} title="删除">
            <Trash2 className="h-3 w-3" />
          </Button>
        </div>
      </>
    )
  }

  // 网格视图
  const renderGridItem = (wf: Workflow) => {
    const steps = wf.steps || []
    const scriptNodes = steps.filter(s => s.type === 'script').length
    const approvalNodes = steps.filter(s => s.type === 'approval').length
    return (
      <Card key={wf.id} className="hover:border-blue-500/20 transition-colors cursor-pointer" onClick={() => navigate(`/workflows/${wf.id}`)}>
        <CardHeader className="py-3 pb-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <WorkflowIcon className="h-4 w-4 text-blue-400 shrink-0" />
              <span className="text-sm font-medium truncate">{wf.name}</span>
            </div>
            <div className="flex gap-0.5 shrink-0" onClick={(e) => e.stopPropagation()}>
               <Button type="text" size="middle" className="h-6 w-6 text-destructive" onClick={() => setConfirmDelete(wf)} title="删除">
                <Trash2 className="h-3 w-3" />
              </Button>
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-2 pt-0">
          <div className="flex items-center gap-2">
            <Tag color="default">{steps.length} 步骤</Tag>
            {scriptNodes > 0 && (
              <Tag color="blue">
                <Code className="h-2.5 w-2.5 mr-0.5" />{scriptNodes}
              </Tag>
            )}
            {approvalNodes > 0 && (
              <Tag color="orange">
                <CheckSquare className="h-2.5 w-2.5 mr-0.5" />{approvalNodes}
              </Tag>
            )}
          </div>
          {wf.description && <p className="text-xs text-muted-foreground line-clamp-2">{wf.description}</p>}
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground/70 pt-1 border-t">
            <span>更新 {formatTime(wf.updated_at)}</span>
          </div>
        </CardContent>
      </Card>
    )
  }

  // 确认删除
  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await remove(confirmDelete.id)
    if (ok) {
      setConfirmDelete(null)
    }
  }

  // 审批操作
  const handleApprove = async (id: string) => {
    const ok = await approve(id, approvalComments[id] || '')
    if (ok) setApprovalComments(prev => ({ ...prev, [id]: '' }))
  }

  const handleReject = async (id: string) => {
    const ok = await reject(id, approvalComments[id] || '')
    if (ok) setApprovalComments(prev => ({ ...prev, [id]: '' }))
  }

  return (
    <div className="p-6 space-y-4">
      {/* 标题行 */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">作业流程</h1>
          <p className="text-sm text-muted-foreground">可视化编排多脚本与审批流程</p>
        </div>
      </div>

      {/* 统计卡片 */}
      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="流程总数" value={stats.total} icon={<WorkflowIcon className="h-3 w-3" />} />
        <StatisticCard title="脚本节点" value={stats.scriptCount} icon={<Code className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="审批节点" value={stats.approvalCount} icon={<CheckSquare className="h-3 w-3" />} className="text-amber-500" />
        <StatisticCard title="总步骤数" value={stats.stepsTotal} icon={<GitBranch className="h-3 w-3" />} />
        <StatisticCard title="待审批" value={stats.pendingApprovals} icon={<Flag className="h-3 w-3" />} className={cn(stats.pendingApprovals > 0 ? "text-red-500" : "")} />
      </StatisticCardGroup>

      {/* 列表 */}
      <DataList
        data={workflows}
        loading={loading}
        columns={columns}
        viewMode={viewMode}
        filters={filters}
        bulkActions={bulkActions}
        listView={renderListItem}
        gridView={renderGridItem}
        emptyMessage="暂无流程，点击「新建流程」创建"
        searchPlaceholder="搜索流程名称..."
        onRefresh={() => { fetch(); fetchApprovals() }}
        customHeader={
          <div className="flex items-center gap-2">
            {pendingApprovals.length > 0 && (
              <Button
                type="default"
                size="middle"
                onClick={() => setShowApprovals(!showApprovals)}
                className={cn(showApprovals && "border-amber-500 text-amber-600")}
              >
                <CheckSquare className="h-4 w-4 mr-1" />
                待审批 ({pendingApprovals.length})
              </Button>
            )}
            <Button size="middle" onClick={() => navigate('/workflows/new')}>
              <Plus className="h-4 w-4 mr-1" />新建流程
            </Button>
          </div>
        }
      />

      {/* 待审批面板 */}
      {showApprovals && pendingApprovals.length > 0 && (
        <Card className="border-amber-500/30">
          <CardHeader className="py-3">
            <div className="flex items-center justify-between">
              <CardTitle className="text-sm flex items-center gap-2">
                <Flag className="h-4 w-4 text-amber-500" />
                待审批任务 ({pendingApprovals.length})
              </CardTitle>
              <Button type="text" size="middle" onClick={() => setShowApprovals(false)}>收起</Button>
            </div>
          </CardHeader>
          <CardContent className="space-y-3">
            {pendingApprovals.map(a => (
              <div key={a.id} className="border rounded-md p-3 bg-amber-50/50 dark:bg-amber-950/10">
                <div className="flex items-start justify-between">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <Flag className="h-3 w-3 text-amber-600" />
                      <span className="text-sm font-medium">{a.step_name}</span>
                       <Tag color="default">待审批</Tag>
                    </div>
                    <p className="text-xs text-muted-foreground mt-1">
                      审批人: {(a.approvers || []).join(', ') || '无'} ·
                      创建于 {formatTime(a.created_at)}
                    </p>
                    {a.message && <p className="text-sm mt-2">{a.message}</p>}
                  </div>
                  <span className="text-xs text-muted-foreground font-mono shrink-0">{a.id.slice(0, 8)}</span>
                </div>
                <div className="mt-2">
                  <input
                    className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
                    placeholder="审批意见 (可选)"
                    value={approvalComments[a.id] || ''}
                    onChange={e => setApprovalComments({ ...approvalComments, [a.id]: e.target.value })}
                  />
                </div>
                <div className="flex gap-2 mt-2 justify-end">
                   <Button size="middle" type="default" onClick={() => handleReject(a.id)}>
                     <Ban className="h-3 w-3 mr-1" />驳回
                   </Button>
                   <Button size="middle" onClick={() => handleApprove(a.id)}>
                    <Check className="h-3 w-3 mr-1" />通过
                  </Button>
                </div>
              </div>
            ))}
          </CardContent>
        </Card>
      )}

      {/* 确认删除对话框 */}
      {confirmDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setConfirmDelete(null)} />
          <div className="relative z-50 w-full max-w-md bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
            <h3 className="text-lg font-semibold mb-2 flex items-center gap-2">
              <Trash2 className="h-5 w-5 text-destructive" />确认删除流程
            </h3>
            <p className="text-sm text-muted-foreground mb-4">
              确认删除流程 <span className="font-medium text-foreground">{confirmDelete.name}</span>? 此操作不可恢复，相关执行历史和审批记录将一并删除。
            </p>
            <div className="flex justify-end gap-2">
              <Button type="default" onClick={() => setConfirmDelete(null)}>取消</Button>
              <Button danger onClick={handleConfirmDelete}>
                <Trash2 className="h-3 w-3 mr-1" />确认删除
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
