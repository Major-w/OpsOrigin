
import { useMemo } from 'react'
import {
  ReactFlow, Background, BackgroundVariant, Handle, Position, MarkerType,
  BaseEdge, EdgeLabelRenderer, getSmoothStepPath,
  type Node, type Edge, type EdgeProps, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { PlayCircle, StopCircle, UserCheck, GitBranch, Clock, Bell, Server, FolderTree, CheckCircle2, CircleDot, Circle, XCircle } from 'lucide-react'
import type { ProcessStep, StageView } from '@/stores/itsm/types'

// 只读流程视图：与流程设计器（itsm/process/[id] 画布）保持同一套 BPMN 视觉。
// 铁律：坐标/顺序一律取设计器保存的 position_x/position_y，不做任何自动重排；
// 节点形态、尺寸、连线走线（getSmoothStepPath borderRadius=2）、颜色/标签与设计器逐一对齐，
// 保证「所见即所设计」。仅叠加两个运行态标记：当前节点 outline 高亮、已完成节点打勾。

const META: Record<string, { label: string; icon: any; color: string }> = {
  start:    { label: '开始事件', icon: PlayCircle,  color: '#3B82F6' },
  end:      { label: '结束事件', icon: StopCircle,  color: '#ef4444' },
  user:     { label: '人工任务', icon: UserCheck,   color: '#6366f1' },
  gateway:  { label: '排他网关', icon: GitBranch,   color: '#8b5cf6' },
  timer:    { label: '定时器',   icon: Clock,       color: '#f97316' },
  notify:   { label: '通知',     icon: Bell,        color: '#ef4444' },
  service:  { label: '服务任务', icon: Server,      color: '#14b8a6' },
  parallel: { label: '并行网关', icon: FolderTree,  color: '#06b6d4' },
  subflow:  { label: '子流程',   icon: FolderTree,  color: '#8b5cf6' },
}

// ---- 连线条件（与设计器 edgeVisual/condKind 逐字对齐）----
type EdgeCondKind = 'success' | 'failure' | 'always' | 'expr'

function condKind(c: string): EdgeCondKind {
  if (c === 'success' || c === 'failure' || c === 'always') return c
  return 'expr'
}

function edgeVisual(cond: string): { color: string; dash?: string; label: string } {
  switch (condKind(cond)) {
    case 'failure': return { color: '#ef4444', dash: '5,3', label: '失败' }
    case 'always':  return { color: '#64748b', dash: '2,4', label: '默认' }
    case 'expr':    return { color: '#f59e0b', label: cond.length > 22 ? `${cond.slice(0, 22)}…` : cond }
    default:        return { color: '#3B82F6', label: '' }
  }
}

type FlowData = Record<string, any>
type FlowNodeProps = NodeProps<Node<FlowData>>

// BPMN 规范：连线一律「左进右出」，与设计器同一套句柄样式
const HANDLE_IN = { width: 10, height: 10, borderWidth: 2, borderStyle: 'solid', borderColor: '#fff' } as const
function InHandle({ color, size = 10 }: { color: string; size?: number }) {
  return <Handle type="target" position={Position.Left} style={{ ...HANDLE_IN, width: size, height: size, background: color }} className="!h-2.5 !w-2.5"/>
}
function OutHandle({ color, size = 10 }: { color: string; size?: number }) {
  return <Handle type="source" position={Position.Right} style={{ ...HANDLE_IN, width: size, height: size, background: color }} className="!h-2.5 !w-2.5"/>
}

// 运行态标记：当前节点 outline + 已完成/失败角标（不改变节点本体形态）
function StateBadge({ current, done, failed }: { current: boolean; done: boolean; failed?: boolean }) {
  return <>
    {current && <span className="absolute inset-0 rounded-[inherit] pointer-events-none" style={{ outline: '2px solid #185FA5', outlineOffset: 3 }} />}
    {done && <CheckCircle2 className="absolute -top-2 -right-2 h-4 w-4 bg-background rounded-full" style={{ color: '#3B6D11' }} />}
    {failed && <XCircle className="absolute -top-2 -right-2 h-4 w-4 bg-background rounded-full" style={{ color: '#ef4444' }} />}
  </>
}

function BPMNStart({ data }: FlowNodeProps) {
  return <div className="relative">
    <div className="w-[104px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: META.start.color }}>
      <PlayCircle className="h-5 w-5 mr-1.5" style={{ color: META.start.color }}/>
      <span className="text-xs font-semibold">{data.label}</span>
    </div>
    <StateBadge current={data.current} done={data.done}/>
    <OutHandle color={META.start.color}/>
  </div>
}
function BPMNEnd({ data }: FlowNodeProps) {
  return <div className="relative">
    <InHandle color="#ef4444"/>
    <div className="w-[104px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: '#ef4444' }}>
      <StopCircle className="h-5 w-5 mr-1.5" style={{ color: '#ef4444' }}/>
      <span className="text-xs font-semibold">{data.label}</span>
    </div>
    <StateBadge current={data.current} done={data.done}/>
  </div>
}
function BPMNBox({ color, icon, data, children }: { color: string; icon: React.ReactNode; data: any; children?: React.ReactNode }) {
  return <div className="relative shadow-sm rounded-xl border-2 bg-white dark:bg-gray-800 min-w-[152px]" style={{ borderColor: color }}>
    <InHandle color={color}/>
    <div className="flex items-center gap-2 px-3 py-2 rounded-t-xl text-white font-medium text-xs" style={{ backgroundColor: color }}>
      {icon}{data.label}
    </div>
    {children && <div className="px-3 py-1.5 text-[11px] text-muted-foreground">{children}</div>}
    <StateBadge current={data.current} done={data.done}/>
    <OutHandle color={color}/>
  </div>
}
function BPMNUser({ data }: FlowNodeProps) {
  return <BPMNBox color={META.user.color} icon={<UserCheck className="h-3.5 w-3.5"/>} data={data}>
    {data.approverCount > 0 && <p className="text-[10px]">👥 {data.approverCount} 审批人 · {data.approvalTypeLabel}</p>}
    {data.approverField && <p className="text-[10px] truncate">🔗 取自字段 {data.approverField}</p>}
  </BPMNBox>
}
function BPMNService({ data }: FlowNodeProps) {
  return <BPMNBox color={META.service.color} icon={<Server className="h-3.5 w-3.5"/>} data={data}>
    {data.scriptName && <p className="text-[10px] truncate">{data.scriptName}</p>}
  </BPMNBox>
}
function BPMNNotify({ data }: FlowNodeProps) {
  return <BPMNBox color={META.notify.color} icon={<Bell className="h-3.5 w-3.5"/>} data={data}>
    {data.notifyChannel && <p className="text-[10px] truncate">{data.notifyChannel}</p>}
  </BPMNBox>
}
function BPMNSubflow({ data }: FlowNodeProps) {
  return <BPMNBox color={META.subflow.color} icon={<FolderTree className="h-3.5 w-3.5"/>} data={data}>
    {data.subflowName && <p className="text-[10px] truncate">{data.subflowName}</p>}
  </BPMNBox>
}

/** 分支网关（排他 / 并行）：一进多出，与设计器同款菱形 */
function GatewayDiamond({ data, color, mark }: { data: any; color: string; mark: string }) {
  const out = data.outCount || 0
  return <div className="relative flex items-center justify-center w-24 h-18">
    <InHandle color={color} size={9}/>
    <div className="w-[52px] h-[52px] rotate-45 border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: color }}>
      <span className="-rotate-45 text-base font-bold leading-none" style={{ color }}>{mark}</span>
    </div>
    <div className="absolute -bottom-4 left-1/2 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium">
      {data.label}
      <span className="ml-1 text-muted-foreground">({out} 出口)</span>
    </div>
    <StateBadge current={data.current} done={data.done}/>
    <OutHandle color={color} size={9}/>
  </div>
}
function BPMNGateway({ data }: FlowNodeProps) {
  return <GatewayDiamond data={data} color={META.gateway.color} mark="×"/>
}
function BPMNParallel({ data }: FlowNodeProps) {
  return <GatewayDiamond data={data} color={META.parallel.color} mark="+"/>
}
function BPMNTimer({ data }: FlowNodeProps) {
  return <div className="relative">
    <InHandle color={META.timer.color}/>
    <div className="w-[104px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm gap-2" style={{ borderColor: META.timer.color }}>
      <Clock className="h-5 w-5" style={{ color: META.timer.color }}/>
      <span className="text-[10px] font-medium">{data.label}</span>
    </div>
    <StateBadge current={data.current} done={data.done}/>
    <OutHandle color={META.timer.color}/>
  </div>
}

const nodeTypes: Record<string, any> = {
  start: BPMNStart, end: BPMNEnd, user: BPMNUser,
  gateway: BPMNGateway, timer: BPMNTimer, notify: BPMNNotify,
  service: BPMNService, parallel: BPMNParallel, subflow: BPMNSubflow,
}

// ---- 阶段进度条（阶段管理）：done/current/pending 三态胶囊，hover 展示阶段内节点明细 ----
const STAGE_STATUS_LABEL: Record<string, string> = { done: '已完成', current: '进行中', pending: '未开始', failed: '失败' }

function StageBar({ stages }: { stages: StageView[] }) {
  return (
    <div className="flex items-center gap-1 mb-3 overflow-x-auto pb-1">
      {stages.map((sv, i) => {
        const doneCount = sv.steps.filter(s => s.status === 'done' || s.status === 'failed').length
        const cls = sv.status === 'done'
          ? 'border-emerald-500/60 bg-emerald-50 text-emerald-700 dark:bg-emerald-900/20 dark:text-emerald-400'
          : sv.status === 'current'
            ? 'border-blue-500 bg-blue-50 text-blue-700 dark:bg-blue-900/20 dark:text-blue-400 ring-1 ring-blue-500/40'
            : 'border-border bg-background text-muted-foreground'
        const Icon = sv.status === 'done' ? CheckCircle2 : sv.status === 'current' ? CircleDot : Circle
        return (
          <div key={sv.stage} className="flex items-center gap-1 shrink-0">
            {i > 0 && <span className="h-px w-4 bg-border" aria-hidden />}
            <div
              className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium whitespace-nowrap ${cls}`}
              title={`阶段「${sv.stage}」（${STAGE_STATUS_LABEL[sv.status]}）：${sv.steps.map(s => `${s.name}·${STAGE_STATUS_LABEL[s.status] || s.status}`).join('、')}`}
            >
              <Icon className="h-3.5 w-3.5" />
              <span>{sv.stage}</span>
              <span className="text-[10px] opacity-70">{doneCount}/{sv.steps.length}</span>
            </div>
          </div>
        )
      })}
    </div>
  )
}

// 只读连线：与设计器 FlowEdge 同款走线（borderRadius=2 利落直角拐弯）+ 标签胶囊，无删除按钮
function FlowEdgeView({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, data }: EdgeProps) {
  const d = (data || {}) as Record<string, any>
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 2 })
  return <>
    <BaseEdge id={id} path={path} markerEnd={markerEnd}
      style={{ stroke: d.color, strokeWidth: 2, strokeDasharray: d.dash }}/>
    {d.label && (
      <EdgeLabelRenderer>
        <div
          className="nodrag nopan absolute"
          style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`, pointerEvents: 'none' }}
        >
          <span className="rounded px-1.5 py-0.5 text-[9px] font-medium border bg-background shadow-sm"
            style={{ color: d.color, borderColor: d.color }}>{d.label}</span>
        </div>
      </EdgeLabelRenderer>
    )}
  </>
}

const edgeTypes: Record<string, any> = { flow: FlowEdgeView }

interface Props {
  steps?: ProcessStep[]
  currentStep?: string
  stepResults?: Record<string, any>
  processName?: string
  /** 阶段进度视图（阶段管理）：非空时画布上方渲染阶段进度条 */
  stages?: StageView[]
}

export default function ProcessFlowView({ steps, currentStep, stepResults, processName, stages }: Props) {
  const { nodes, edges } = useMemo(() => {
    const list = steps || []
    // 出口计数（网关「N 出口」角标），与设计器同一算法
    const outCount = new Map<string, number>()
    for (const s of list) for (const d of s.depends_on || []) outCount.set(d.nodeId, (outCount.get(d.nodeId) || 0) + 1)
    const ns: Node[] = list.map((s, i) => ({
      id: s.id,
      type: s.type,
      // 坐标严格取设计器保存值；缺失时兜底公式也与设计器 newStep/BPMNCanvas 一致
      position: { x: s.position_x ?? 96 + (i % 5) * 240, y: s.position_y ?? 200 + Math.floor(i / 5) * 176 },
      data: {
        label: s.name || META[s.type]?.label || s.type,
        approverCount: (s.approvers || []).length,
        approvalTypeLabel: ({ countersign: '会签', orsign: '或签', responded_any: '全员表态', percent: '比例表决', count: '数量表决' } as Record<string, string>)[s.approval_type || 'countersign'] || '会签',
        approverField: s.approver_field,
        scriptName: (s as any).script_name || '',
        notifyChannel: (s as any).notify_channel || '',
        subflowName: (s as any).subflow_id || '',
        outCount: outCount.get(s.id) || 0,
        current: s.id === currentStep && currentStep !== '',
        done: !!(stepResults && stepResults[s.id]) && stepResults[s.id]?.status !== 'failed',
        failed: !!(stepResults && stepResults[s.id] && stepResults[s.id]?.status === 'failed'),
      },
    }))
    const es: Edge[] = []
    const idSet = new Set(list.map(s => s.id))
    for (const s of list) {
      if (!s.depends_on) continue
      for (const d of s.depends_on) {
        if (!idSet.has(d.nodeId)) continue
        const v = edgeVisual(d.condition)
        es.push({
          id: `${d.nodeId}->${s.id}`, source: d.nodeId, target: s.id,
          type: 'flow',
          animated: condKind(d.condition) === 'success',
          markerEnd: { type: MarkerType.ArrowClosed, color: v.color },
          data: { ...v },
        })
      }
    }
    return { nodes: ns, edges: es }
  }, [steps, currentStep, stepResults])

  if (!steps || steps.length === 0) {
    return <p className="text-sm text-muted-foreground">该流程没有节点。</p>
  }

  return (
    <div>
      {processName && <p className="text-xs text-muted-foreground mb-2">流程：{processName}</p>}
      {stages && stages.length > 0 && <StageBar stages={stages} />}
      <div className="border rounded-md overflow-hidden bg-muted/20 h-[480px]">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          defaultEdgeOptions={{ type: 'flow' }}
          fitView
          fitViewOptions={{ padding: 0.12, maxZoom: 1 }}
          minZoom={0.08}
          maxZoom={1.5}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          panOnDrag
          zoomOnScroll
          proOptions={{ hideAttribution: true }}
        >
          <Background variant={BackgroundVariant.Dots} gap={20} size={1} />
        </ReactFlow>
      </div>
      <div className="flex items-center gap-4 mt-2 text-[11px] text-muted-foreground">
        <span className="flex items-center gap-1"><span className="inline-block w-2.5 h-2.5 rounded-full bg-[#185FA5]" />当前步骤</span>
        <span className="flex items-center gap-1"><CheckCircle2 className="h-3 w-3 text-[#3B6D11]" />已完成</span>
        <span className="flex items-center gap-1"><XCircle className="h-3 w-3" style={{ color: '#ef4444' }} />执行失败</span>
        <span className="flex items-center gap-1"><span className="inline-block w-2.5 h-2.5 rounded-sm bg-violet-500" />条件分支(按表单字段)</span>
      </div>
    </div>
  )
}
