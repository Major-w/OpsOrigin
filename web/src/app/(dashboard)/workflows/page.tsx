
import { useEffect, useState, useMemo, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useWorkflowStore, type Workflow, type StepApproval } from '@/stores/workflow'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { ProTable, type ProColumns, type ActionType } from '@ant-design/pro-components'
import type { ColumnsState } from '@ant-design/pro-components'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { Plus, Trash2, GitBranch, Code, CheckSquare, Split, Clock, Workflow as WorkflowIcon, Check, Ban, Flag, Pencil } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { Button, Tag, Modal, Popconfirm, message } from 'antd'

export default function WorkflowsPage() {
  const navigate = useNavigate()
  const {
    workflows, approvals, executions, loading,
    fetch, remove, fetchApprovals, approve, reject,
  } = useWorkflowStore()
  const actionRef = useRef<ActionType>()
  const [confirmDelete, setConfirmDelete] = useState<Workflow | null>(null)
  const [showApprovals, setShowApprovals] = useState(false)
  const [approvalComments, setApprovalComments] = useState<Record<string, string>>({})
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([])

  useEffect(() => { fetch(); fetchApprovals() }, [])

  const stats = useMemo(() => {
    const total = workflows.length
    const scriptCount = workflows.reduce((acc, w) => acc + (w.steps || []).filter(s => s.type === 'script').length, 0)
    const approvalCount = workflows.reduce((acc, w) => acc + (w.steps || []).filter(s => s.type === 'approval').length, 0)
    const pendingApprovals = approvals.filter(a => a.status === 'pending').length
    const stepsTotal = workflows.reduce((acc, w) => acc + (w.steps || []).length, 0)
    return { total, scriptCount, approvalCount, pendingApprovals, stepsTotal }
  }, [workflows, approvals])

  const pendingApprovals = approvals.filter(a => a.status === 'pending')

  const [workflowFilter, setWorkflowFilter] = useState<string | null>(null)
  const [workflowSearchValues, setWorkflowSearchValues] = useState<Record<string, any>>({})
  const [workflowColumnsState, setWorkflowColumnsState] = useState<Record<string, ColumnsState>>({})

  const filteredWorkflows = useMemo(() => {
    let result = workflows
    if (workflowFilter) {
      if (workflowFilter === 'hasScript') result = result.filter(w => (w.steps || []).some(s => s.type === 'script'))
      else if (workflowFilter === 'hasApproval') result = result.filter(w => (w.steps || []).some(s => s.type === 'approval'))
      else if (workflowFilter === 'pendingApproval') {
        const pendingExecIds = new Set(approvals.filter(a => a.status === 'pending').map(a => a.execution_id))
        const pendingWfIds = new Set(executions.filter(e => pendingExecIds.has(e.id)).map(e => e.workflow_id))
        result = result.filter(w => pendingWfIds.has(w.id))
      }
    }
    if (workflowSearchValues._q) {
      const q = workflowSearchValues._q.toLowerCase()
      result = result.filter(w =>
        w.name?.toLowerCase().includes(q) ||
        w.description?.toLowerCase().includes(q) ||
        String(w.steps?.length).includes(q) ||
        w.created_at?.toLowerCase().includes(q) ||
        w.updated_at?.toLowerCase().includes(q)
      )
    }
    if (workflowSearchValues.name) {
      result = result.filter(w => w.name.toLowerCase().includes(workflowSearchValues.name.toLowerCase()))
    }
    return result
  }, [workflows, workflowFilter, approvals, executions, workflowSearchValues])

  const handleWorkflowFilterClick = (filter: string | null) => {
    setWorkflowFilter(prev => prev === filter ? null : filter)
  }

  const handleApprove = async (id: string) => {
    const ok = await approve(id, approvalComments[id] || '')
    if (ok) setApprovalComments(prev => ({ ...prev, [id]: '' }))
  }

  const handleReject = async (id: string) => {
    const ok = await reject(id, approvalComments[id] || '')
    if (ok) setApprovalComments(prev => ({ ...prev, [id]: '' }))
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await remove(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  const columns: ProColumns<Workflow>[] = [
    {
      title: '模糊搜索',
      dataIndex: '_q',
      hideInTable: true,
      hideInSetting: true,
      fieldProps: { placeholder: '搜索流程名称、描述、步骤数...' },
    },
    {
      title: '流程名称',
      dataIndex: 'name',
      fixed: 'left',
      width: 240,
      render: (_, wf) => (
        <div className="flex items-center gap-2 min-w-0">
          <WorkflowIcon className="h-4 w-4 text-blue-400 shrink-0" />
          <span
            className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
            onClick={(e) => { e.stopPropagation(); navigate(`/workflows/${wf.id}`) }}
          >
            {wf.name}
          </span>
        </div>
      ),
      fieldProps: { placeholder: '搜索流程名称' },
    },
    {
      title: '步骤数',
      dataIndex: 'steps',
      width: 100,
      fieldProps: { placeholder: '搜索步骤数' },
      sorter: (a, b) => (a.steps?.length || 0) - (b.steps?.length || 0),
      render: (_, wf) => {
        const steps = wf.steps || []
        const scriptNodes = steps.filter(s => s.type === 'script').length
        const approvalNodes = steps.filter(s => s.type === 'approval').length
        return (
          <div className="flex items-center gap-1.5">
            <Tag color="default">{steps.length} 步</Tag>
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
        )
      },
    },
    {
      title: '描述',
      dataIndex: 'description',
      ellipsis: true,
      fieldProps: { placeholder: '搜索描述' },
      render: (_, wf) => (
        <span className="text-xs text-muted-foreground">{wf.description || '-'}</span>
      ),
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      fieldProps: { placeholder: '搜索创建时间' },
      sorter: (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      render: (_, wf) => (
        <span className="text-xs text-muted-foreground/70 hidden md:block">{formatTime(wf.created_at)}</span>
      ),
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      fieldProps: { placeholder: '搜索更新时间' },
      sorter: (a, b) => new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime(),
      render: (_, wf) => (
        <span className="text-xs text-muted-foreground/70 hidden md:block">{formatTime(wf.updated_at)}</span>
      ),
    },
    {
      title: '操作',
      valueType: 'option',
      fixed: 'right',
      width: 80,
      render: (_, wf) => [
        <button
          key="edit"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); navigate(`/workflows/${wf.id}`) }}
          title="编辑"
        >
          <Pencil className="h-3 w-3" />
        </button>,
        <button
          key="delete"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); setConfirmDelete(wf) }}
          title="删除"
        >
          <Trash2 className="h-3 w-3" />
        </button>,
      ],
    },
  ]

  const searchWorkflowColumns = useMemo(() => {
    return columns.map(col => {
      const key = (col.dataIndex || col.key) as string
      if (!key) return col
      const state = workflowColumnsState[key]
      if (state && state.show === false) return { ...col, hideInSearch: true }
      return col
    })
  }, [columns, workflowColumnsState])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">作业流程</h1>
          <p className="text-sm text-muted-foreground">可视化编排多脚本与审批流程</p>
        </div>
      </div>

      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="流程总数" value={stats.total} icon={<WorkflowIcon className="h-3 w-3" />} onClick={() => handleWorkflowFilterClick(null)} active={workflowFilter === null} />
        <StatisticCard title="脚本节点" value={stats.scriptCount} icon={<Code className="h-3 w-3" />} className="text-blue-500" onClick={() => handleWorkflowFilterClick('hasScript')} active={workflowFilter === 'hasScript'} />
        <StatisticCard title="审批节点" value={stats.approvalCount} icon={<CheckSquare className="h-3 w-3" />} className="text-amber-500" onClick={() => handleWorkflowFilterClick('hasApproval')} active={workflowFilter === 'hasApproval'} />
        <StatisticCard title="总步骤数" value={stats.stepsTotal} icon={<GitBranch className="h-3 w-3" />} />
        <StatisticCard title="待审批" value={stats.pendingApprovals} icon={<Flag className="h-3 w-3" />} className={cn(stats.pendingApprovals > 0 ? "text-red-500" : "")} onClick={() => handleWorkflowFilterClick('pendingApproval')} active={workflowFilter === 'pendingApproval'} />
      </StatisticCardGroup>

      <ProTable<Workflow>
        actionRef={actionRef}
        columns={searchWorkflowColumns}
        dataSource={filteredWorkflows}
        loading={loading}
        rowKey="id"
        columnsState={{ value: workflowColumnsState, onChange: setWorkflowColumnsState }}
        search={{ labelWidth: 'auto', defaultCollapsed: true, span: 6 }}
        onSubmit={(values) => setWorkflowSearchValues(values)}
        onReset={() => setWorkflowSearchValues({})}
        pagination={{ defaultPageSize: 20, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }}
        rowSelection={{ selectedRowKeys, onChange: setSelectedRowKeys }}
        toolBarRender={() => [
          selectedRowKeys.length > 0 && (
            <Popconfirm
              key="bulk-delete"
              title={`确认删除选中的 ${selectedRowKeys.length} 个流程？`}
              onConfirm={async () => {
                for (const key of selectedRowKeys) { await remove(key as string) }
                setSelectedRowKeys([])
              }}
              okText="确认"
              cancelText="取消"
            >
              <Button danger size="middle">
                <Trash2 className="h-3.5 w-3.5 mr-1" />批量删除 ({selectedRowKeys.length})
              </Button>
            </Popconfirm>
          ),
          pendingApprovals.length > 0 && (
            <Button
              key="approvals"
              type="default"
              size="middle"
              onClick={() => setShowApprovals(!showApprovals)}
              className={cn(showApprovals && "border-amber-500 text-amber-600")}
            >
              <CheckSquare className="h-4 w-4 mr-1" />
              待审批 ({pendingApprovals.length})
            </Button>
          ),
          <Button key="new" type="primary" size="middle" onClick={() => navigate('/workflows/new')}>
            <Plus className="h-4 w-4 mr-1" />新建流程
          </Button>,
        ].filter(Boolean)}
        options={{ density: true, fullScreen: true, reload: () => { fetch(); fetchApprovals() }, setting: true }}
        headerTitle={false}
      />

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
                      审批人: {(a.approvers || []).join(', ') || '无'} · 创建于 {formatTime(a.created_at)}
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

      <Modal
        open={!!confirmDelete}
        onCancel={() => setConfirmDelete(null)}
        onOk={handleConfirmDelete}
        okText="确认删除"
        okButtonProps={{ danger: true }}
        cancelText="取消"
        title={<span className="flex items-center gap-2"><Trash2 className="h-5 w-5 text-red-500" />确认删除流程</span>}
      >
        <p className="text-sm text-muted-foreground">
          确认删除流程 <span className="font-medium text-foreground">{confirmDelete?.name}</span>? 此操作不可恢复，相关执行历史和审批记录将一并删除。
        </p>
      </Modal>
    </div>
  )
}
