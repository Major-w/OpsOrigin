
import { useEffect, useState, useMemo, useRef } from 'react'
import { useDeployStore, type ReleaseTicket, type ReleaseStatus, type DeployEnv, type DeployEnvironment, type DeployMode } from '@/stores/deploy'
import { ProTable, type ProColumns, type ActionType } from '@ant-design/pro-components'
import type { ColumnsState } from '@ant-design/pro-components'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { Button, Modal, Input, Select, Tag, message } from 'antd'
import { FileCheck2, Trash2, Check, Ban, Play, RotateCcw, GitBranch, Clock, AlertCircle, CheckCircle, XCircle, Loader2, Rocket, Box, Server, Container } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'

const STATUS_META: Record<ReleaseStatus, { label: string; color: string; icon: any }> = {
  draft: { label: '草稿', color: 'bg-slate-500/10 text-slate-600 border-slate-500/20', icon: FileCheck2 },
  pending_approval: { label: '待审批', color: 'bg-amber-500/10 text-amber-600 border-amber-500/20', icon: Clock },
  approved: { label: '已审批', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
  deploying: { label: '发布中', color: 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20', icon: Loader2 },
  succeeded: { label: '已成功', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20', icon: CheckCircle },
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
  const { releases, environments, apps, loading, fetchReleases, fetchEnvs, approveRelease, rejectRelease, deployRelease, rollbackRelease, createRelease } = useDeployStore()
  const actionRef = useRef<ActionType>()
  const [detail, setDetail] = useState<ReleaseTicket | null>(null)
  const [rejectTarget, setRejectTarget] = useState<ReleaseTicket | null>(null)
  const [rejectReason, setRejectReason] = useState('')
  const [createOpen, setCreateOpen] = useState(false)
  const [searchValues, setSearchValues] = useState<Record<string, any>>({})
  const [columnsState, setColumnsState] = useState<Record<string, ColumnsState>>({})


  useEffect(() => { fetchReleases(); fetchEnvs() }, [])

  const filteredReleases = useMemo(() => {
    let result = releases
    if (searchValues._q) {
      const q = searchValues._q.toLowerCase()
      result = result.filter(r =>
        r.title?.toLowerCase().includes(q) ||
        r.app_name?.toLowerCase().includes(q) ||
        r.version?.toLowerCase().includes(q) ||
        r.env?.toLowerCase().includes(q) ||
        r.status?.toLowerCase().includes(q) ||
        r.strategy?.toLowerCase().includes(q) ||
        r.created_at?.toLowerCase().includes(q)
      )
    }
    if (searchValues.title) {
      result = result.filter(r => r.title.toLowerCase().includes(searchValues.title.toLowerCase()))
    }
    if (searchValues.app_name) {
      result = result.filter(r => r.app_name.toLowerCase().includes(searchValues.app_name.toLowerCase()))
    }
    if (searchValues.env) {
      result = result.filter(r => r.env === searchValues.env)
    }
    if (searchValues.status) {
      result = result.filter(r => r.status === searchValues.status)
    }
    return result
  }, [releases, searchValues])

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

  const handleReject = async () => {
    if (!rejectTarget) return
    const ok = await rejectRelease(rejectTarget.id, rejectReason || '未填写原因')
    if (ok) { setRejectTarget(null); setRejectReason('') }
  }

  const columns: ProColumns<ReleaseTicket>[] = [
    {
      title: '模糊搜索',
      dataIndex: '_q',
      hideInTable: true,
      hideInSetting: true,
      fieldProps: { placeholder: '搜索发布标题、应用、版本、环境、状态、策略...' },
    },
    {
      title: '发布标题',
      dataIndex: 'title',
      fixed: 'left',
      width: 220,
      render: (_, rel) => {
        const sm = STATUS_META[rel.status]
        const SIcon = sm.icon
        return (
          <div className="flex items-center gap-2 min-w-0">
            <SIcon className={cn('h-4 w-4 shrink-0', rel.status === 'deploying' && 'animate-spin', sm.color.split(' ')[1])} />
            <span className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
              onClick={(e) => { e.stopPropagation(); setDetail(rel) }}>
              {rel.title}
            </span>
          </div>
        )
      },
      fieldProps: { placeholder: '搜索发布标题' },
    },
    {
      title: '应用',
      dataIndex: 'app_name',
      width: 140,
      fieldProps: { placeholder: '搜索应用名' },
      render: (_, rel) => <span className="text-xs text-muted-foreground truncate hidden lg:block">{rel.app_name}</span>,
    },
    {
      title: '版本',
      dataIndex: 'version',
      width: 100,
      fieldProps: { placeholder: '搜索版本' },
      render: (_, rel) => <span className="text-xs font-mono text-muted-foreground">{rel.version}</span>,
    },
    {
      title: '环境',
      dataIndex: 'env',
      width: 80,
      filters: Object.entries(ENV_LABELS).map(([v, l]) => ({ text: l, value: v })),
      onFilter: (value, record) => record.env === value,
      render: (_, rel) => <span className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[rel.env])}>{ENV_LABELS[rel.env]}</span>,
    },
    {
      title: '策略',
      dataIndex: 'strategy',
      width: 100,
      fieldProps: { placeholder: '搜索策略' },
      render: (_, rel) => <span className="text-xs text-muted-foreground hidden md:block">{STRATEGY_LABELS[rel.strategy]}</span>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      width: 100,
      filters: Object.entries(STATUS_META).map(([v, m]) => ({ text: m.label, value: v })),
      onFilter: (value, record) => record.status === value,
      render: (_, rel) => {
        const sm = STATUS_META[rel.status]
        return <Tag className={cn("text-[10px] px-1.5 py-0", sm.color)}>{sm.label}</Tag>
      },
    },
    {
      title: '创建时间',
      dataIndex: 'created_at',
      width: 160,
      fieldProps: { placeholder: '搜索创建时间' },
      sorter: (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      render: (_, rel) => <span className="text-xs text-muted-foreground/70 hidden md:block">{formatTime(rel.created_at)}</span>,
    },
    {
      title: '操作',
      valueType: 'option',
      fixed: 'right',
      width: 120,
      render: (_, rel) => [
        rel.status === 'pending_approval' && (
          <button key="approve" className="h-6 w-6 inline-flex items-center justify-center rounded text-blue-500 hover:bg-blue-500/10 cursor-pointer"
            onClick={(e) => { e.stopPropagation(); approveRelease(rel.id) }} title="审批通过">
            <Check className="h-3 w-3" />
          </button>
        ),
        rel.status === 'pending_approval' && (
          <button key="reject" className="h-6 w-6 inline-flex items-center justify-center rounded text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10 cursor-pointer"
            onClick={(e) => { e.stopPropagation(); setRejectTarget(rel); setRejectReason('') }} title="驳回">
            <Ban className="h-3 w-3" />
          </button>
        ),
        rel.status === 'approved' && (
          <button key="deploy" className="h-6 w-6 inline-flex items-center justify-center rounded text-cyan-500 hover:bg-cyan-500/10 cursor-pointer"
            onClick={(e) => { e.stopPropagation(); deployRelease(rel.id) }} title="执行发布">
            <Play className="h-3 w-3" />
          </button>
        ),
        (rel.status === 'succeeded' || rel.status === 'rolled_back') && (
          <button key="rollback" className="h-6 w-6 inline-flex items-center justify-center rounded text-orange-500 hover:bg-orange-500/10 cursor-pointer"
            onClick={(e) => { e.stopPropagation(); rollbackRelease(rel.id) }} title="回滚">
            <RotateCcw className="h-3 w-3" />
          </button>
        ),
        <button key="detail" className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); setDetail(rel) }} title="详情">
          <FileCheck2 className="h-3 w-3" />
        </button>,
      ],
    },
  ]

  const searchColumns = useMemo(() => {
    return columns.map(col => {
      const key = (col.dataIndex || col.key) as string
      if (!key) return col
      const state = columnsState[key]
      if (state && state.show === false) return { ...col, hideInSearch: true }
      return col
    })
  }, [columns, columnsState])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">发布单</h1>
          <p className="text-sm text-muted-foreground">应用版本发布流程，含审批、执行、回滚；支持主机环境与容器环境</p>
        </div>
      </div>

      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="发布单总数" value={stats.total} icon={<FileCheck2 className="h-3 w-3" />} />
        <StatisticCard title="待审批" value={stats.pending} icon={<Clock className="h-3 w-3" />} className="text-amber-500" />
        <StatisticCard title="发布中" value={stats.deploying} icon={<Loader2 className="h-3 w-3" />} className="text-cyan-500" />
        <StatisticCard title="已成功" value={stats.succeeded} icon={<CheckCircle className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="失败/回滚" value={stats.failed} icon={<XCircle className="h-3 w-3" />} className="text-red-500" />
      </StatisticCardGroup>

      <ProTable<ReleaseTicket>
        actionRef={actionRef}
        columns={searchColumns}
        dataSource={filteredReleases}
        loading={loading}
        rowKey="id"
        columnsState={{ value: columnsState, onChange: setColumnsState }}
        search={{ labelWidth: 'auto', defaultCollapsed: true, span: 6 }}
        onSubmit={(values) => setSearchValues(values)}
        onReset={() => setSearchValues({})}
        pagination={{ defaultPageSize: 20, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }}
        toolBarRender={() => [
          <Button key="new" type="primary" size="middle" onClick={() => setCreateOpen(true)}>
            <Rocket className="h-3.5 w-3.5 mr-1" />新建发布单
          </Button>,
        ]}
        options={{ density: true, fullScreen: true, reload: () => { fetchReleases(); fetchEnvs() }, setting: true }}
        headerTitle={false}
      />

      <Modal title={detail?.title} open={!!detail} onCancel={() => setDetail(null)} footer={null} width={640}>
        {detail && (
          <div className="space-y-3">
            <div className="flex items-center gap-2">
              <Tag className={cn("text-[10px]", STATUS_META[detail.status].color)}>{STATUS_META[detail.status].label}</Tag>
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
            <div>
              <span className="text-xs font-medium">部署步骤</span>
              <div className="mt-2 space-y-1.5">
                {(detail.steps || []).map(step => {
                  const StepIcon = STEP_ICONS[step.id] || FileCheck2
                  const stepColor = step.status === 'succeeded' ? 'text-blue-500' : step.status === 'failed' ? 'text-red-500' : step.status === 'running' ? 'text-cyan-500' : 'text-muted-foreground'
                  return (
                    <div key={step.id} className="flex items-center gap-2 text-xs">
                      <StepIcon className={cn('h-3 w-3', stepColor, step.status === 'running' && 'animate-spin')} />
                      <span className="flex-1">{step.name}</span>
                      <span className={cn(stepColor)}>{step.status === 'succeeded' ? '成功' : step.status === 'failed' ? '失败' : step.status === 'running' ? '进行中' : '等待'}</span>
                    </div>
                  )
                })}
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2 border-t">
              {detail.status === 'pending_approval' && (
                <>
                  <Button danger size="middle" onClick={() => { setRejectTarget(detail); setRejectReason(''); setDetail(null) }}><Ban className="h-3.5 w-3.5 mr-1" />驳回</Button>
                  <Button type="primary" size="middle" onClick={() => { approveRelease(detail.id); setDetail(null) }}><Check className="h-3.5 w-3.5 mr-1" />审批通过</Button>
                </>
              )}
              {detail.status === 'approved' && (
                <Button type="primary" size="middle" onClick={() => { deployRelease(detail.id); setDetail(null) }}><Play className="h-3.5 w-3.5 mr-1" />执行发布</Button>
              )}
              {(detail.status === 'succeeded' || detail.status === 'rolled_back') && (
                <Button size="middle" onClick={() => { rollbackRelease(detail.id); setDetail(null) }}><RotateCcw className="h-3.5 w-3.5 mr-1" />回滚</Button>
              )}
              <Button size="middle" onClick={() => setDetail(null)}>关闭</Button>
            </div>
          </div>
        )}
      </Modal>

      <Modal title="驳回发布单" open={!!rejectTarget} onCancel={() => setRejectTarget(null)} footer={null}>
        {rejectTarget && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">驳回 <span className="font-medium text-foreground">{rejectTarget.title}</span></p>
            <div><label className="text-xs font-medium">驳回原因</label><Input.TextArea className="mt-1" rows={3} value={rejectReason} onChange={e => setRejectReason(e.target.value)} placeholder="请填写驳回原因" /></div>
            <div className="flex justify-end gap-2">
              <Button onClick={() => setRejectTarget(null)}>取消</Button>
              <Button danger size="middle" onClick={handleReject}><Ban className="h-3.5 w-3.5 mr-1" />确认驳回</Button>
            </div>
          </div>
        )}
      </Modal>

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
    <Modal title="新建发布单" open onCancel={onClose} footer={null}>
      <div className="space-y-3">
        <div className="grid grid-cols-2 gap-3">
          <div className="col-span-2">
            <label className="text-xs font-medium">应用 *</label>
            <Select className="mt-1 w-full" value={form.app_id || undefined} onChange={v => setForm(f => ({ ...f, app_id: v }))} placeholder="请选择应用" options={apps.map(a => ({ label: `${a.name}（${a.code}）`, value: a.id }))} />
          </div>
          <div>
            <label className="text-xs font-medium">版本 *</label>
            <Input className="mt-1 font-mono" value={form.version} onChange={e => setForm(f => ({ ...f, version: e.target.value }))} placeholder="v1.2.0" />
          </div>
          <div>
            <label className="text-xs font-medium">环境 *</label>
            <Select className="mt-1 w-full" value={form.env} onChange={v => setForm(f => ({ ...f, env: v }))} options={Object.entries(ENV_LABELS).map(([v, l]) => ({ label: l, value: v }))} />
          </div>
          <div>
            <label className="text-xs font-medium">发布策略</label>
            <Select className="mt-1 w-full" value={form.strategy} onChange={v => setForm(f => ({ ...f, strategy: v }))} options={Object.entries(STRATEGY_LABELS).map(([v, l]) => ({ label: l, value: v }))} />
          </div>
          <div>
            <label className="text-xs font-medium">审批人（逗号分隔）</label>
            <Input className="mt-1" value={form.approvers} onChange={e => setForm(f => ({ ...f, approvers: e.target.value }))} placeholder="alice, bob" />
          </div>
          <div className="col-span-2">
            <label className="text-xs font-medium">变更日志</label>
            <Input.TextArea className="mt-1" rows={3} value={form.changelog} onChange={e => setForm(f => ({ ...f, changelog: e.target.value }))} placeholder="本次变更内容..." />
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
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" disabled={!form.app_id || !form.version} onClick={() => onSubmit({
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
    </Modal>
  )
}
