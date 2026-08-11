'use client'

import { useEffect, useState, useMemo, useCallback, useRef } from 'react'
import { useParams, useRouter } from 'next/navigation'
import {
  ReactFlow, Controls, Background, BackgroundVariant, MiniMap,
  type Node, type Edge, type Connection, type NodeProps, type EdgeProps,
  Handle, Position, ReactFlowProvider,
  MarkerType, BaseEdge, EdgeLabelRenderer, getSmoothStepPath,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useITSMStore, type Process, type ProcessStep } from '@/stores/itsm'
import { useAgentStore } from '@/stores/agent'
import { useScriptStore } from '@/stores/script'
import { useNotificationStore } from '@/stores/notification'
import { useUserStore } from '@/stores/user'
import { useIAMStore } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Dialog } from '@/components/ui/dialog'
import { formatTime } from '@/lib/utils'
import { apiList, apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { NOTIFY_CHANNEL_LABELS } from '@/lib/itsm-constants'
import { FormDesigner } from '@/components/itsm/FormDesigner'
import { type FormSchema } from '@/components/itsm/DynamicForm'
import { parseFormSchema, type StepFieldPerm } from '@/lib/itsm-constants'
import {
  Plus, Trash2, Save, X, Pencil, ChevronLeft, Loader2,
  PlayCircle, StopCircle, UserCheck, GitBranch,
  Clock, Bell, Server, FolderTree, FileText, Settings2, ChevronRight, ChevronLeft as CL,
  Grid3x3, Undo2, Redo2, Users, Search, Route, AlertTriangle,
} from 'lucide-react'

// ---- types & meta ----
type ItStepType = ProcessStep['type']

const WIZARD_STEPS = ['表单设计', '流程编排', '高级设置'] as const

const META: Record<ItStepType, { label: string; icon: any; color: string }> = {
  start:    { label: '开始事件', icon: PlayCircle, color: '#10b981' },
  end:      { label: '结束事件', icon: StopCircle, color: '#ef4444' },
  user:     { label: '人工任务', icon: UserCheck, color: '#6366f1' },
  gateway:  { label: '排他网关', icon: GitBranch, color: '#8b5cf6' },
  timer:    { label: '定时器',   icon: Clock,     color: '#f97316' },
  notify:   { label: '通知',     icon: Bell,      color: '#ef4444' },
  service:  { label: '服务任务', icon: Server,    color: '#14b8a6' },
  parallel: { label: '并行网关', icon: FolderTree, color: '#06b6d4' },
  subflow:  { label: '子流程',   icon: FolderTree, color: '#8b5cf6' },
}

// 工单可操作按钮全集（key → 中文标签）。流程设计时在「人工任务」节点勾选本节点允许展示的按钮。
const ACTION_OPTIONS: { key: string; label: string }[] = [
  { key: 'submit', label: '提交' },
  { key: 'withdraw', label: '撤回' },
  { key: 'approve', label: '通过' },
  { key: 'reject', label: '驳回' },
  { key: 'execute', label: '执行' },
  { key: 'complete', label: '完成' },
  { key: 'rollback', label: '回滚' },
  { key: 'close', label: '关闭' },
  { key: 'hold', label: '挂起' },
  { key: 'resume', label: '恢复' },
  { key: 'reopen', label: '重开' },
  { key: 'assign', label: '指派' },
  { key: 'remind', label: '催办' },
  { key: 'relate', label: '关联' },
  { key: 'change', label: '变更' },
  { key: 'convert', label: '转知识' },
]

// 横向 BPMN 泳道：新节点沿 X 轴顺次排布，超过 5 个折行，避免竖向堆叠
function newStep(type: ItStepType, idx: number): ProcessStep {
  return {
    id: `${type}_${Date.now().toString(36)}_${idx}`,
    name: META[type]?.label || type,
    type,
    position_x: 96 + (idx % 5) * 240,
    position_y: 200 + Math.floor(idx / 5) * 176,
    ...(type !== 'start' && type !== 'end' ? { depends_on: [] } : {}),
  }
}

function defaultSteps(): ProcessStep[] {
  const s = newStep('start', 0); s.position_x = 96;  s.position_y = 240
  const e = newStep('end', 1);   e.position_x = 576; e.position_y = 240
  return [s, e]
}

// ---- 连线条件（BPMN sequence flow condition）----
type EdgeCondKind = 'success' | 'failure' | 'always' | 'expr'

const EDGE_COND_OPTIONS: { kind: EdgeCondKind; label: string; hint: string }[] = [
  { kind: 'success', label: '上游成功', hint: '上游节点正常完成后走本条连线（缺省）' },
  { kind: 'failure', label: '上游失败', hint: '上游服务任务执行失败 / 审批驳回时走本条连线' },
  { kind: 'expr',    label: '条件表达式', hint: '按工单表单字段求值，如 risk_level == "high"' },
  { kind: 'always',  label: '默认分支', hint: '其余条件都不满足时兜底走本条连线（BPMN 默认流）' },
]

function condKind(c: string): EdgeCondKind {
  if (c === 'success' || c === 'failure' || c === 'always') return c
  return 'expr'
}

/** 连线在画布上的观感：颜色 / 虚线 / 标签文案 */
function edgeVisual(cond: string): { color: string; dash?: string; label: string } {
  switch (condKind(cond)) {
    case 'failure': return { color: '#ef4444', dash: '5,3', label: '失败' }
    case 'always':  return { color: '#64748b', dash: '2,4', label: '默认' }
    case 'expr':    return { color: '#f59e0b', label: cond.length > 22 ? `${cond.slice(0, 22)}…` : cond }
    default:        return { color: '#10b981', label: '' }
  }
}

function detectCycle(steps: ProcessStep[]): boolean {
  const idSet = new Set(steps.map(s => s.id))
  const succ = new Map<string, string[]>()
  for (const s of steps) {
    if (s.depends_on) for (const d of s.depends_on) {
      if (idSet.has(d.nodeId)) {
        if (!succ.has(d.nodeId)) succ.set(d.nodeId, [])
        succ.get(d.nodeId)!.push(s.id)
      }
    }
  }
  const WHITE = 0, GRAY = 1, BLACK = 2
  const color = new Map<string, number>()
  for (const s of steps) color.set(s.id, WHITE)
  function dfs(u: string): boolean {
    color.set(u, GRAY)
    for (const v of succ.get(u) || []) {
      const c = color.get(v)
      if (c === GRAY) return true
      if (c === WHITE && dfs(v)) return true
    }
    color.set(u, BLACK)
    return false
  }
  for (const s of steps) if (color.get(s.id) === WHITE && dfs(s.id)) return true
  return false
}

/**
 * 流程完整性校验：必须含开始/结束、不允许环路、从开始可达全部节点、全部节点可到达结束。
 * 任一不满足返回人类可读的错误提示（以「流程未设计完整」开头），满足返回 null。
 */
function validateFlow(steps: ProcessStep[]): string | null {
  const hasStart = steps.some(s => s.type === 'start')
  const hasEnd = steps.some(s => s.type === 'end')
  if (!hasStart || !hasEnd) return '流程未设计完整：必须包含「开始」与「结束」节点'
  if (detectCycle(steps)) return '流程未设计完整：检测到环路，DAG 不允许出现死循环'

  const idSet = new Set(steps.map(s => s.id))
  const succ = new Map<string, string[]>()
  const pred = new Map<string, string[]>()
  for (const s of steps) { succ.set(s.id, []); pred.set(s.id, []) }
  for (const s of steps) {
    for (const d of s.depends_on || []) {
      if (!idSet.has(d.nodeId)) continue
      succ.get(d.nodeId)!.push(s.id)
      pred.get(s.id)!.push(d.nodeId)
    }
  }
  const startId = steps.find(s => s.type === 'start')!.id
  const endId = steps.find(s => s.type === 'end')!.id

  // 从开始节点正向可达
  const reach = new Set<string>([startId])
  const q1 = [startId]
  while (q1.length) {
    const u = q1.shift()!
    for (const v of succ.get(u) || []) if (!reach.has(v)) { reach.add(v); q1.push(v) }
  }
  const unreachable = steps.filter(s => !reach.has(s.id)).map(s => s.name)
  if (unreachable.length) return `流程未设计完整：以下节点无法从「开始」节点到达：${unreachable.join('、')}`

  // 反向可达结束节点
  const toEnd = new Set<string>([endId])
  const q2 = [endId]
  while (q2.length) {
    const u = q2.shift()!
    for (const v of pred.get(u) || []) if (!toEnd.has(v)) { toEnd.add(v); q2.push(v) }
  }
  const noEnd = steps.filter(s => !toEnd.has(s.id)).map(s => s.name)
  if (noEnd.length) return `流程未设计完整：以下节点无法到达「结束」节点：${noEnd.join('、')}`

  // 网关语义（BPMN 分支网关：一进多出，条件挂在出口连线上）
  for (const g of steps.filter(s => s.type === 'gateway' || s.type === 'parallel')) {
    const inbound = (g.depends_on || []).filter(d => idSet.has(d.nodeId))
    const outbound = steps.flatMap(s => (s.depends_on || []).filter(d => d.nodeId === g.id).map(d => ({ to: s, cond: d.condition })))
    const kind = g.type === 'gateway' ? '排他网关' : '并行网关'
    if (inbound.length !== 1) return `${kind}「${g.name}」应当只有 1 条入口连线（当前 ${inbound.length} 条）：分支网关是一进多出`
    if (outbound.length < 2) return `${kind}「${g.name}」至少需要 2 条出口连线（当前 ${outbound.length} 条），否则不构成分支`
    if (g.type === 'gateway') {
      const defaults = outbound.filter(o => o.cond === 'always')
      if (defaults.length > 1) return `排他网关「${g.name}」只能有 1 条默认分支（当前 ${defaults.length} 条）`
      const noCond = outbound.filter(o => condKind(o.cond) === 'success' || condKind(o.cond) === 'failure')
      if (noCond.length && outbound.length - noCond.length === 0) {
        return `排他网关「${g.name}」的出口连线需要设置条件表达式或默认分支，否则永远只会走第一条`
      }
    }
  }

  return null
}

// 画布节点 data 为展示用途,字段松散;用宽松类型规避 @xyflow/react v12 严格的 NodeProps 默认类型
type FlowNodeData = Record<string, any>
type FlowNodeProps = NodeProps<Node<FlowNodeData>>

// ---- custom nodes ----
// BPMN 规范：连线一律「左进右出」——target 句柄统一在左侧，source 句柄统一在右侧，
// 画布因此始终是从左到右的泳道流向，不再出现上下穿插的连线。
const HANDLE_IN = { width: 10, height: 10, borderWidth: 2, borderStyle: 'solid', borderColor: '#fff' } as const
function InHandle({ color, size = 10 }: { color: string; size?: number }) {
  return <Handle type="target" position={Position.Left} style={{ ...HANDLE_IN, width: size, height: size, background: color }}/>
}
function OutHandle({ color, size = 10, id }: { color: string; size?: number; id?: string }) {
  return <Handle type="source" position={Position.Right} id={id} style={{ ...HANDLE_IN, width: size, height: size, background: color }}/>
}

function BPMNStart({ data }: FlowNodeProps) {
  return <div className="relative">
    <div className="w-[104px] h-[52px] rounded-full border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: META.start.color }}>
      <PlayCircle className="h-5 w-5 mr-1.5" style={{ color: META.start.color }}/>
      <span className="text-xs font-semibold">{data.label}</span>
    </div>
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
  </div>
}
function BPMNUser({ data }: FlowNodeProps) {
  return <BPMNBox color={META.user.color} icon={<UserCheck className="h-3.5 w-3.5"/>} data={data}>
    {data.approverCount > 0 && <p className="text-[10px]">👥 {data.approverCount} 审批人 · {data.approvalTypeLabel}</p>}
    {data.approverField && <p className="text-[10px] truncate">🔗 取自字段 {data.approverField}</p>}
  </BPMNBox>
}

/** 分支网关（排他 / 并行）：一进多出——左侧单入口，右侧单出口句柄可连出任意多条分支连线 */
function GatewayDiamond({ data, color, mark }: { data: any; color: string; mark: string }) {
  const out = data.outCount || 0
  return <div className="relative flex items-center justify-center" style={{ width: 96, height: 72 }}>
    <InHandle color={color} size={9}/>
    <div className="w-[52px] h-[52px] rotate-45 border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: color }}>
      <span className="-rotate-45 text-base font-bold leading-none" style={{ color }}>{mark}</span>
    </div>
    <div className="absolute -bottom-4 left-1/2 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium">
      {data.label}
      <span className="ml-1 text-muted-foreground">({out} 出口)</span>
    </div>
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
    <OutHandle color={META.timer.color}/>
  </div>
}
function BPMNBox({ color, icon, data, children }: { color: string; icon: React.ReactNode; data: any; children?: React.ReactNode }) {
  return <div className="relative shadow-sm rounded-xl border-2 bg-white dark:bg-gray-800 min-w-[152px]" style={{ borderColor: color }}>
    <InHandle color={color}/>
    <div className="flex items-center gap-2 px-3 py-2 rounded-t-xl text-white font-medium text-xs" style={{ backgroundColor: color }}>
      {icon}{data.label}
    </div>
    {children && <div className="px-3 py-1.5 text-[11px] text-muted-foreground">{children}</div>}
    <OutHandle color={color}/>
  </div>
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

const nodeTypes: Record<string, any> = {
  start: BPMNStart, end: BPMNEnd, user: BPMNUser,
  gateway: BPMNGateway, timer: BPMNTimer, notify: BPMNNotify,
  service: BPMNService, parallel: BPMNParallel, subflow: BPMNSubflow,
}

// ---- 可删除连线（悬停出现「×」，选中高亮；Delete 键同样可删）----
function FlowEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, data, selected }: EdgeProps) {
  const d = (data || {}) as Record<string, any>
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 14 })
  return <>
    <BaseEdge id={id} path={path} markerEnd={markerEnd}
      style={{ stroke: d.color, strokeWidth: selected ? 3 : 2, strokeDasharray: d.dash }}/>
    <EdgeLabelRenderer>
      <div
        className="nodrag nopan group absolute flex items-center gap-1"
        style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)`, pointerEvents: 'all' }}
      >
        {d.label && (
          <span className="rounded px-1.5 py-0.5 text-[9px] font-medium border bg-background shadow-sm"
            style={{ color: d.color, borderColor: d.color }}>{d.label}</span>
        )}
        <button
          type="button"
          title="删除连线"
          aria-label="删除连线"
          onClick={e => { e.stopPropagation(); d.onDelete?.(d.source, d.target) }}
          className={`h-4 w-4 shrink-0 rounded-full border bg-background text-[10px] leading-none text-muted-foreground transition-all hover:border-destructive hover:bg-destructive hover:text-white ${selected ? 'opacity-100' : 'opacity-0 group-hover:opacity-100'}`}
        >×</button>
      </div>
    </EdgeLabelRenderer>
  </>
}

const edgeTypes: Record<string, any> = { flow: FlowEdge }

// ---- 审批人选择器：用户列表勾选 + 用户组一键展开成员 ----
interface UserRef { id: string; username: string; email?: string }
interface GroupRef { id: string; name: string; user_ids?: string[] }

function ApproverPicker({ value, users, groups, onChange }: {
  value: string[]
  users: UserRef[]
  groups: GroupRef[]
  onChange: (v: string[]) => void
}) {
  const [open, setOpen] = useState(false)
  const [kw, setKw] = useState('')
  const [manual, setManual] = useState('')

  // 审批口径是用户名（工单 approve 提交的 approved_by 即 username），用户组存的是 user_id，这里统一换算
  const nameOf = useCallback((uid: string) => users.find(u => u.id === uid)?.username || uid, [users])
  const uniq = (arr: string[]) => Array.from(new Set(arr.map(s => s.trim()).filter(Boolean)))

  const toggle = (name: string) => onChange(value.includes(name) ? value.filter(v => v !== name) : uniq([...value, name]))
  const addGroup = (g: GroupRef) => onChange(uniq([...value, ...(g.user_ids || []).map(nameOf)]))
  const kwLower = kw.trim().toLowerCase()
  const filtered = users.filter(u => !kwLower || u.username.toLowerCase().includes(kwLower) || (u.email || '').toLowerCase().includes(kwLower))

  return <div className="space-y-1">
    <div className="flex items-center justify-between gap-2">
      <label className="text-xs font-medium">审批人</label>
      <Button type="button" size="sm" variant="outline" className="h-6 px-2 text-[10px]" onClick={() => setOpen(true)}>
        <Users className="h-3 w-3 mr-1"/>选择
      </Button>
    </div>
    {value.length === 0
      ? <p className="text-[10px] text-muted-foreground">未指定审批人。可从用户列表 / 用户组选择，也可只用下方「从表单字段取审批人」。</p>
      : <div className="flex flex-wrap gap-1">
          {value.map(v => (
            <Badge key={v} variant="outline" className="text-[10px] gap-1 pr-1">
              {v}
              <button type="button" onClick={() => onChange(value.filter(x => x !== v))} className="text-muted-foreground hover:text-destructive" aria-label={`移除 ${v}`}>×</button>
            </Badge>
          ))}
        </div>}

    <Dialog open={open} onClose={() => setOpen(false)} title="选择审批人">
      <div className="space-y-3">
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground"/>
          <Input value={kw} onChange={e => setKw(e.target.value)} placeholder="搜索用户名 / 邮箱" className="h-8 pl-7 text-xs"/>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <p className="text-[11px] font-medium mb-1">用户列表（{filtered.length}）</p>
            <div className="border rounded-md max-h-[38vh] overflow-y-auto divide-y">
              {filtered.length === 0 && <p className="p-3 text-[11px] text-muted-foreground">没有匹配的用户</p>}
              {filtered.map(u => (
                <label key={u.id} className="flex items-center gap-2 px-2 py-1.5 text-xs cursor-pointer hover:bg-muted/40">
                  <input type="checkbox" className="h-3.5 w-3.5 accent-primary" checked={value.includes(u.username)} onChange={() => toggle(u.username)}/>
                  <span className="font-medium">{u.username}</span>
                  {u.email && <span className="text-[10px] text-muted-foreground truncate">{u.email}</span>}
                </label>
              ))}
            </div>
          </div>
          <div>
            <p className="text-[11px] font-medium mb-1">用户组（点选展开为成员）</p>
            <div className="border rounded-md max-h-[38vh] overflow-y-auto divide-y">
              {groups.length === 0 && <p className="p-3 text-[11px] text-muted-foreground">暂无用户组</p>}
              {groups.map(g => (
                <button key={g.id} type="button" onClick={() => addGroup(g)}
                  className="w-full flex items-center justify-between gap-2 px-2 py-1.5 text-xs hover:bg-muted/40 text-left">
                  <span className="font-medium truncate">{g.name}</span>
                  <span className="text-[10px] text-muted-foreground shrink-0">{(g.user_ids || []).length} 人 · 加入</span>
                </button>
              ))}
            </div>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Input value={manual} onChange={e => setManual(e.target.value)} placeholder="手动补充账号（外部/系统账号）" className="h-8 text-xs"/>
          <Button type="button" size="sm" variant="outline" className="h-8 text-[11px] shrink-0"
            disabled={!manual.trim()} onClick={() => { onChange(uniq([...value, manual])); setManual('') }}>添加</Button>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">已选 {value.length} 人</span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => onChange([])}>清空</Button>
            <Button size="sm" onClick={() => setOpen(false)}>完成</Button>
          </div>
        </div>
      </div>
    </Dialog>
  </div>
}

// ---- 分支网关属性：一进多出，条件挂在出口连线上（节点自身不再持有条件）----
function GatewayPanel({ step, steps, onPickEdge }: {
  step: ProcessStep; steps: ProcessStep[]
  onPickEdge: (source: string, target: string) => void
}) {
  const exclusive = step.type === 'gateway'
  const outs = steps.flatMap(s => (s.depends_on || []).filter(d => d.nodeId === step.id).map(d => ({ to: s, cond: d.condition })))
  const inbound = (step.depends_on || []).length
  const warn = inbound !== 1 || outs.length < 2 || (exclusive && outs.filter(o => o.cond === 'always').length > 1)
  return <div className="space-y-2">
    <div className="rounded-md bg-muted/30 p-2 text-[10px] text-muted-foreground space-y-1">
      <p className="flex items-center gap-1 text-foreground font-medium"><Route className="h-3 w-3"/>{exclusive ? '排他网关（×）' : '并行网关（+）'} · 一进多出</p>
      {exclusive
        ? <p>入口 1 条、出口 N 条；<b>条件写在出口连线上</b>，运行期按连线条件择一分支，都不满足则走「默认分支」。</p>
        : <p>入口 1 条、出口 N 条；所有出口分支<b>全部执行</b>，因此出口连线不设条件（统一为默认分支）。</p>}
      <p>网关节点自身没有条件字段——点选画布上的出口连线即可编辑该分支的条件。</p>
    </div>
    <div className="space-y-1">
      <p className="text-[10px] font-medium text-muted-foreground">入口 {inbound} 条 · 出口 {outs.length} 条</p>
      {outs.length === 0 && <p className="text-[10px] text-muted-foreground">还没有出口分支，从网关右侧句柄拖到下游节点。</p>}
      {outs.map(o => {
        const v = edgeVisual(o.cond)
        return <button key={o.to.id} type="button" onClick={() => onPickEdge(step.id, o.to.id)}
          className="w-full flex items-center justify-between gap-2 rounded border px-2 py-1 text-[10px] hover:bg-accent text-left">
          <span className="truncate">→ {o.to.name}</span>
          <span className="shrink-0 font-medium" style={{ color: v.color }}>{v.label || '成功'}</span>
        </button>
      })}
      {warn && <p className="flex items-start gap-1 text-[10px] text-amber-600 dark:text-amber-400">
        <AlertTriangle className="h-3 w-3 mt-0.5 shrink-0"/>
        分支网关应为「1 条入口 + ≥2 条出口」{exclusive ? '，且默认分支最多 1 条' : ''}，保存时会校验。
      </p>}
    </div>
  </div>
}

// ---- step property panel ----
function StepPanel({ step, steps, scripts, channels, mainForm, users, groups, onPickEdge, onChange }: {
  step: ProcessStep; steps: ProcessStep[]; scripts: any[]; channels: any[]
  mainForm: FormSchema
  users: UserRef[]; groups: GroupRef[]
  onPickEdge: (source: string, target: string) => void
  onChange: (p: Partial<ProcessStep>) => void
}) {
  // 节点不再单独设计表单：流程只有一张主表单（向导第一步产出），
  // 节点只声明主表单字段在本节点的「可见 / 可编辑」权限。
  const [permOpen, setPermOpen] = useState(false)
  const mainFields = mainForm?.fields || []
  const permOf = useCallback((key: string): StepFieldPerm => {
    const hit = (step.form_fields || []).find(p => p.key === key)
    return hit || { key, visible: true, editable: true }   // 未声明 = 可见可编辑
  }, [step.form_fields])

  // 写回时始终落全量（对齐当前主表单字段集），避免主表单增删字段后残留脏 key
  const writePerms = (mutate: (p: StepFieldPerm) => StepFieldPerm) => {
    onChange({ form_fields: mainFields.map(f => mutate({ ...permOf(f.key) })) })
  }
  const setPerm = (key: string, patch: Partial<StepFieldPerm>) => {
    writePerms(p => {
      if (p.key !== key) return p
      const next = { ...p, ...patch }
      if (!next.visible) next.editable = false   // 不可见必然不可编辑
      if (next.editable) next.visible = true     // 可编辑必然可见
      return next
    })
  }
  const bulk = (visible: boolean, editable: boolean) => writePerms(p => ({ ...p, visible, editable }))

  const visibleCount = mainFields.filter(f => permOf(f.key).visible).length
  const editableCount = mainFields.filter(f => permOf(f.key).editable).length

  const removeDep = (nodeId: string) => {
    onChange({ depends_on: (step.depends_on || []).filter(d => d.nodeId !== nodeId) })
  }

  return <>
    <Card>
    <CardHeader className="py-3"><CardTitle className="text-sm">{META[step.type]?.label || step.type}</CardTitle></CardHeader>
    <CardContent className="space-y-2 text-sm">
      <div><label className="text-xs font-medium">名称</label><Input value={step.name} onChange={e=>onChange({name:e.target.value})} className="h-8 mt-1"/></div>
      {step.type === 'user' && <>
        <ApproverPicker value={step.approvers || []} users={users} groups={groups} onChange={v => onChange({ approvers: v })}/>
        <div>
          <label className="text-xs font-medium">审批类型</label>
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
            value={step.approval_type || 'countersign'} onChange={e => onChange({ approval_type: e.target.value as 'countersign' | 'orsign' })}>
            <option value="countersign">会签（全部审批人通过才继续）</option>
            <option value="orsign">或签（任一审批人通过即继续）</option>
          </select>
          <p className="text-[10px] text-muted-foreground mt-1">单审批人时两者等价；多人时决定表决口径。</p>
        </div>
        <div>
          <label className="text-xs font-medium">从表单字段取审批人</label>
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
            value={step.approver_field || ''} onChange={e => onChange({ approver_field: e.target.value })}>
            <option value="">不启用（只用上面固定的审批人）</option>
            {mainFields.map(f => <option key={f.key} value={f.key}>{f.label || f.key}（{f.type}）</option>)}
          </select>
          <p className="text-[10px] text-muted-foreground mt-1">
            运行期取该字段的值作为审批人，与固定审批人合并去重。适合「谁提单谁的主管审批」这类动态场景，推荐选「选人」类型字段。
          </p>
        </div>
        <div><label className="text-xs font-medium">审批消息</label><Input value={step.approval_msg||''} onChange={e=>onChange({approval_msg:e.target.value})} className="h-8 mt-1" placeholder="请审批此变更"/></div>
        <div className="space-y-1">
          <label className="text-xs font-medium">本节点可操作按钮</label>
          <div className="flex flex-wrap gap-1">
            {ACTION_OPTIONS.map(o => {
              const on = (step.actions || []).includes(o.key)
              return (
                <button type="button" key={o.key}
                  onClick={() => {
                    const cur = step.actions || []
                    const next = on ? cur.filter(k => k !== o.key) : [...cur, o.key]
                    onChange({ actions: next })
                  }}
                  className={`text-[10px] px-2 py-1 rounded border transition-colors ${on ? 'bg-primary text-primary-foreground border-primary' : 'border-input text-muted-foreground hover:bg-muted/40'}`}>
                  {o.label}
                </button>
              )
            })}
          </div>
          <p className="text-[10px] text-muted-foreground mt-1">
            勾选本节点允许展示的操作按钮；未勾选的按钮工单流转到本节点时不显示。全部留空表示沿用默认（全部显示），保持向后兼容。
          </p>
        </div>
        <div className="space-y-1">
          <div className="flex items-center justify-between gap-2">
            <label className="text-xs font-medium">表单字段权限</label>
            <Button type="button" size="sm" variant="outline" className="h-6 px-2 text-[10px]"
              disabled={mainFields.length === 0}
              onClick={() => setPermOpen(true)}>
              <Pencil className="h-3 w-3 mr-1"/>配置
            </Button>
          </div>
          {mainFields.length === 0 ? (
            <p className="text-[10px] text-muted-foreground">流程主表单还没有字段，请先回到「表单设计」步骤定义字段。</p>
          ) : (
            <p className="text-[10px] text-muted-foreground">
              主表单共 {mainFields.length} 个字段，本节点可见 <b className="text-foreground">{visibleCount}</b> 个、可编辑 <b className="text-foreground">{editableCount}</b> 个。
              节点不单独设计表单，只决定主表单字段在此处的可见与可编辑范围。
            </p>
          )}
        </div>
      </>}
      {(step.type === 'gateway' || step.type === 'parallel') && <GatewayPanel step={step} steps={steps} onPickEdge={onPickEdge}/>}
      {step.type === 'timer' && <div><label className="text-xs font-medium">超时时长</label><Input value={step.duration||''} onChange={e=>onChange({duration:e.target.value})} className="h-8 mt-1" placeholder="4h / 30m / 1d"/></div>}
      {step.type === 'notify' && <>
        <div><label className="text-xs font-medium">通知渠道</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.notify_channel||''} onChange={e=>onChange({notify_channel:e.target.value})}><option value="">选择渠道</option>{channels.filter((c:any)=>c.enabled).map((c:any)=><option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label className="text-xs font-medium">标题</label><Input value={step.notify_title||''} onChange={e=>onChange({notify_title:e.target.value})} className="h-8 mt-1"/></div>
        <div><label className="text-xs font-medium">内容</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1" value={step.notify_body||''} onChange={e=>onChange({notify_body:e.target.value})}/></div>
      </>}
      {step.type === 'service' && <div><label className="text-xs font-medium">关联脚本</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.script_id||''} onChange={e=>onChange({script_id:e.target.value})}><option value="">选择脚本</option>{scripts.map((s:any)=><option key={s.id} value={s.id}>{s.name}</option>)}</select></div>}
      {step.type === 'subflow' && <div><label className="text-xs font-medium">子流程ID</label><Input value={step.subflow_id||''} onChange={e=>onChange({subflow_id:e.target.value})} className="h-8 mt-1"/></div>}
      {(step.type === 'start' || step.type === 'end') && <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">无需额外配置</div>}

      {step.type !== 'start' && <div className="pt-2 border-t space-y-1">
        <p className="text-[10px] font-medium text-muted-foreground">入口连线（上游）</p>
        {(step.depends_on||[]).length===0?<p className="text-[10px] text-muted-foreground">无，在画布上把上游节点右侧句柄拖到本节点左侧</p>:
          (step.depends_on||[]).map(d=>{
            const v = edgeVisual(d.condition)
            return <div key={d.nodeId} className="flex items-center gap-1 text-[10px]">
              <button type="button" onClick={()=>onPickEdge(d.nodeId, step.id)} className="hover:underline">
                <Badge variant="outline" className="text-[10px]">{steps.find(s=>s.id===d.nodeId)?.name||d.nodeId}</Badge>
              </button>
              <span style={{ color: v.color }}>{v.label || '成功'}</span>
              <button onClick={()=>removeDep(d.nodeId)} className="text-red-400 hover:text-red-600 ml-auto" title="删除该连线">×</button>
            </div>
          })
        }
      </div>}
    </CardContent>
  </Card>
      <Dialog open={permOpen} onClose={() => setPermOpen(false)} title={`表单字段权限 · ${step.name}`}>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            勾选主表单字段在本节点的权限：<b>可见</b>决定字段是否渲染给处理人，<b>可编辑</b>决定能否修改（不可编辑时以只读形式展示）。
            未做任何勾选变更的字段默认可见可编辑。
          </p>
          <div className="flex items-center gap-2">
            <Button type="button" size="sm" variant="outline" className="h-7 text-[11px]" onClick={() => bulk(true, true)}>全部可编辑</Button>
            <Button type="button" size="sm" variant="outline" className="h-7 text-[11px]" onClick={() => bulk(true, false)}>全部只读</Button>
            <Button type="button" size="sm" variant="outline" className="h-7 text-[11px]" onClick={() => bulk(false, false)}>全部隐藏</Button>
          </div>
          <div className="border rounded-md max-h-[50vh] overflow-y-auto">
            <table className="w-full text-xs">
              <thead className="bg-muted/50 sticky top-0">
                <tr>
                  <th className="text-left px-3 py-2 font-medium">字段</th>
                  <th className="text-left px-3 py-2 font-medium w-24">类型</th>
                  <th className="text-center px-3 py-2 font-medium w-20">可见</th>
                  <th className="text-center px-3 py-2 font-medium w-20">可编辑</th>
                </tr>
              </thead>
              <tbody>
                {mainFields.map(f => {
                  const p = permOf(f.key)
                  return (
                    <tr key={f.key} className="border-t hover:bg-muted/30">
                      <td className="px-3 py-1.5">
                        <span className="font-medium">{f.label || f.key}</span>
                        {f.required && <span className="text-destructive ml-0.5">*</span>}
                        <span className="text-muted-foreground ml-1.5">{f.key}</span>
                      </td>
                      <td className="px-3 py-1.5 text-muted-foreground">{f.type}</td>
                      <td className="px-3 py-1.5 text-center">
                        <input type="checkbox" className="h-3.5 w-3.5 accent-primary" checked={p.visible}
                          onChange={e => setPerm(f.key, { visible: e.target.checked })} aria-label={`${f.label} 可见`} />
                      </td>
                      <td className="px-3 py-1.5 text-center">
                        <input type="checkbox" className="h-3.5 w-3.5 accent-primary" checked={p.editable}
                          onChange={e => setPerm(f.key, { editable: e.target.checked })} aria-label={`${f.label} 可编辑`} />
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          <div className="flex justify-end">
            <Button variant="outline" onClick={() => setPermOpen(false)}>完成</Button>
          </div>
        </div>
      </Dialog>
    </>
}

// ---- 连线属性面板：条件类型 + 表达式 + 删除 ----
function EdgePanel({ source, target, cond, steps, mainForm, onChangeCond, onDelete, onClose }: {
  source: ProcessStep; target: ProcessStep; cond: string
  steps: ProcessStep[]; mainForm: FormSchema
  onChangeCond: (c: string) => void
  onDelete: () => void
  onClose: () => void
}) {
  const kind = condKind(cond)
  // 并行网关所有分支都会执行，条件无意义 —— 锁死为默认分支
  const locked = source.type === 'parallel'
  const opt = EDGE_COND_OPTIONS.find(o => o.kind === kind)
  const fieldKeys = (mainForm?.fields || []).map(f => f.key)
  return <Card>
    <CardHeader className="py-3 flex-row items-center justify-between space-y-0">
      <CardTitle className="text-sm flex items-center gap-1.5"><Route className="h-3.5 w-3.5"/>连线</CardTitle>
      <button type="button" onClick={onClose} className="text-muted-foreground hover:text-foreground" aria-label="取消选中"><X className="h-3.5 w-3.5"/></button>
    </CardHeader>
    <CardContent className="space-y-2 text-sm">
      <div className="flex items-center gap-1 text-[11px]">
        <Badge variant="outline" className="text-[10px] truncate max-w-[80px]">{source.name}</Badge>
        <ChevronRight className="h-3 w-3 text-muted-foreground"/>
        <Badge variant="outline" className="text-[10px] truncate max-w-[80px]">{target.name}</Badge>
      </div>

      {locked ? (
        <div className="rounded-md bg-muted/30 p-2 text-[10px] text-muted-foreground">
          上游是<b>并行网关</b>，所有分支都会执行，本条连线不支持条件（已固定为默认分支）。
        </div>
      ) : <>
        <div>
          <label className="text-xs font-medium">触发条件</label>
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
            value={kind}
            onChange={e => {
              const k = e.target.value as EdgeCondKind
              onChangeCond(k === 'expr' ? (kind === 'expr' ? cond : '') : k)
            }}>
            {EDGE_COND_OPTIONS.map(o => <option key={o.kind} value={o.kind}>{o.label}</option>)}
          </select>
          {opt && <p className="text-[10px] text-muted-foreground mt-1">{opt.hint}</p>}
        </div>
        {kind === 'expr' && <div>
          <Input value={cond} onChange={e => onChangeCond(e.target.value)} className="h-8 text-xs font-mono" placeholder='risk_level == "high"'/>
          {fieldKeys.length > 0 && <p className="text-[10px] text-muted-foreground mt-1">
            可用字段：{fieldKeys.slice(0, 8).join('、')}{fieldKeys.length > 8 ? ' …' : ''}
          </p>}
          {!cond.trim() && <p className="text-[10px] text-amber-600 dark:text-amber-400 mt-1">表达式为空，运行期该分支不会被选中。</p>}
        </div>}
      </>}

      <Button variant="destructive" size="sm" className="w-full" onClick={onDelete}>
        <Trash2 className="h-3.5 w-3.5 mr-1"/>删除连线
      </Button>
    </CardContent>
  </Card>
}

// ---- BPMN canvas (presentational, steps 提升到页面层) ----
function BPMNCanvas({ steps, scripts, channels, mainForm, users, groups, selId, setSelId, selEdge, setSelEdge, update, add, remove, onConnect, removeEdge, setEdgeCond, snap, onToggleSnap, onAlign }: {
  steps: ProcessStep[]; scripts: any[]; channels: any[]; mainForm: FormSchema
  users: UserRef[]; groups: GroupRef[]
  selId: string | null; setSelId: (id: string | null) => void
  selEdge: { source: string; target: string } | null
  setSelEdge: (e: { source: string; target: string } | null) => void
  update: (id: string, patch: Partial<ProcessStep>) => void
  add: (type: ItStepType) => void
  remove: (id: string) => void
  onConnect: (conn: Connection) => void
  removeEdge: (source: string, target: string) => void
  setEdgeCond: (source: string, target: string, cond: string) => void
  snap: boolean; onToggleSnap: () => void; onAlign: () => void
}) {
  const sel = steps.find(s => s.id === selId) || null
  const edgeSrc = selEdge ? steps.find(s => s.id === selEdge.source) : null
  const edgeTgt = selEdge ? steps.find(s => s.id === selEdge.target) : null
  const edgeCond = (edgeTgt?.depends_on || []).find(d => d.nodeId === selEdge?.source)?.condition ?? ''

  const pickEdge = useCallback((source: string, target: string) => { setSelId(null); setSelEdge({ source, target }) }, [setSelId, setSelEdge])

  const { nodes, edges } = useMemo(() => {
    const outCount = new Map<string, number>()
    for (const s of steps) for (const d of s.depends_on || []) outCount.set(d.nodeId, (outCount.get(d.nodeId) || 0) + 1)
    const ns: Node[] = steps.map((s, i) => ({
      id: s.id, type: s.type, position: { x: s.position_x ?? 96 + (i%5)*240, y: s.position_y ?? 200 + Math.floor(i/5)*176 },
      data: {
        label: s.name,
        approverCount: (s.approvers || []).length,
        approvalTypeLabel: s.approval_type === 'orsign' ? '或签' : '会签',
        approverField: s.approver_field,
        script_id: s.script_id, scriptName: scripts.find(sc=>sc.id===s.script_id)?.name,
        duration: s.duration,
        outCount: outCount.get(s.id) || 0,
        notifyChannel: channels.find((c:any)=>c.id===s.notify_channel)?.name || s.notify_channel,
        notify_title: s.notify_title, notify_body: s.notify_body,
        subflowName: s.subflow_id,
      },
    }))
    const es: Edge[] = []
    const idSet = new Set(steps.map(s => s.id))
    for (const s of steps) {
      if (!s.depends_on) continue
      for (const d of s.depends_on) {
        if (!idSet.has(d.nodeId)) continue
        const v = edgeVisual(d.condition)
        es.push({
          id: `${d.nodeId}->${s.id}`, source: d.nodeId, target: s.id,
          type: 'flow',
          animated: condKind(d.condition) === 'success',
          selected: selEdge?.source === d.nodeId && selEdge?.target === s.id,
          markerEnd: { type: MarkerType.ArrowClosed, color: v.color },
          data: { ...v, source: d.nodeId, target: s.id, onDelete: removeEdge },
        })
      }
    }
    return { nodes: ns, edges: es }
  }, [steps, scripts, channels, selEdge, removeEdge])

  return <div className="flex gap-3" style={{ height: 680 }}>
    <div className="w-32 shrink-0 space-y-1">
      <p className="text-xs text-muted-foreground mb-2">BPMN 节点</p>
      {(Object.keys(META) as ItStepType[]).filter(t=>t!=='start'&&t!=='end').map(type=>{
        const m=META[type]; const I=m.icon
        return <button key={type} onClick={()=>add(type)} className="w-full flex items-center gap-2 px-2 py-2 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent"><I className="h-3 w-3 shrink-0"/>{m.label}</button>
      })}
      <div className="pt-2 mt-2 border-t text-[10px] text-muted-foreground space-y-1"><p>· 右侧句柄拖到下游左侧句柄连线</p><p>· 点连线设条件，悬停「×」删连线</p><p>· 选中后 Delete 键删除</p></div>
      <div className="pt-2 mt-2 border-t space-y-1">
        <button type="button" onClick={onToggleSnap} className={`w-full flex items-center justify-between px-2 py-1.5 text-xs rounded-md border ${snap ? 'bg-primary/10 border-primary/40 text-foreground' : 'hover:bg-accent'}`}>
          <span className="flex items-center gap-1.5"><Grid3x3 className="h-3 w-3" />网格吸附</span><span>{snap ? '开' : '关'}</span>
        </button>
        <button type="button" onClick={onAlign} className="w-full flex items-center gap-2 px-2 py-1.5 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent"><Grid3x3 className="h-3 w-3" />对齐到网格</button>
      </div>
    </div>
    <div className="flex-1 border rounded-md overflow-hidden bg-muted/20">
      <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes}
        onConnect={onConnect}
        onNodeClick={(_e, node) => { setSelEdge(null); setSelId(node.id) }}
        onEdgeClick={(_e, edge) => pickEdge(edge.source, edge.target)}
        onNodeDragStop={(_e, node) => update(node.id, { position_x: node.position.x, position_y: node.position.y })}
        onPaneClick={() => { setSelId(null); setSelEdge(null) }}
        onNodesDelete={nodes => nodes.forEach(n => { if (n.type !== 'start') remove(n.id) })}
        onEdgesDelete={eds => eds.forEach(e => removeEdge(e.source, e.target))}
        fitView deleteKeyCode={['Backspace','Delete']}
        snapToGrid={snap} snapGrid={[16, 16]}
        defaultEdgeOptions={{ type: 'flow' }}>
        <Controls position="bottom-right"/>
        <Background variant={BackgroundVariant.Dots} gap={20} size={1}/>
        <MiniMap position="bottom-left" nodeStrokeWidth={2} pannable zoomable style={{ height: 100 }}/>
      </ReactFlow>
    </div>
    <div className="w-60 shrink-0 overflow-y-auto" style={{ maxHeight: 680 }}>
      {edgeSrc && edgeTgt ? (
        <EdgePanel source={edgeSrc} target={edgeTgt} cond={edgeCond} steps={steps} mainForm={mainForm}
          onChangeCond={c => setEdgeCond(edgeSrc.id, edgeTgt.id, c)}
          onDelete={() => { removeEdge(edgeSrc.id, edgeTgt.id); setSelEdge(null) }}
          onClose={() => setSelEdge(null)}/>
      ) : sel ? (
        <div className="flex flex-col gap-2">
          <StepPanel step={sel} steps={steps} scripts={scripts} channels={channels} mainForm={mainForm}
            users={users} groups={groups} onPickEdge={pickEdge} onChange={p=>update(sel.id,p)}/>
          {sel.type!=='start'&&<Button variant="destructive" size="sm" onClick={()=>remove(sel.id)}><Trash2 className="h-3.5 w-3.5 mr-1"/>删除节点</Button>}
        </div>
      ) : (
        <Card className="h-full min-h-[200px] flex items-center justify-center"><CardContent className="text-center text-muted-foreground text-xs space-y-1"><p>选中节点编辑属性</p><p>选中连线设置分支条件</p></CardContent></Card>
      )}
    </div>
  </div>
}

// ---- 高级设置：SLA/OLA/通知 ----
function AdvancedSettings({ proc, slaPolicies, olaPolicies, slaPolicyId, setSlaPolicyId, olaPolicyId, setOlaPolicyId, notifyConfig, setNotifyConfig }: {
  proc: Process
  slaPolicies: { id: string; name: string; risk_level: string }[]
  olaPolicies: { id: string; name: string }[]
  slaPolicyId: string
  setSlaPolicyId: (v: string) => void
  olaPolicyId: string
  setOlaPolicyId: (v: string) => void
  notifyConfig: { channels: string[]; on_create: boolean; on_complete: boolean; template: string }
  setNotifyConfig: (v: { channels: string[]; on_create: boolean; on_complete: boolean; template: string }) => void
}) {
  const chLabels = NOTIFY_CHANNEL_LABELS
  const toggleChannel = (c: string) => {
    const has = notifyConfig.channels.includes(c)
    setNotifyConfig({ ...notifyConfig, channels: has ? notifyConfig.channels.filter(x => x !== c) : [...notifyConfig.channels, c] })
  }
  return (
    <div className="space-y-4">
      <Card><CardContent className="p-4 space-y-3">
        <div className="flex items-center gap-2 text-sm font-semibold"><Settings2 className="h-4 w-4"/>SLA 关联</div>
        <p className="text-[11px] text-muted-foreground">关联后，本流程创建的工单将按「流程级 SLA」优先匹配（高于风险等级默认 SLA）。可在 SLA 管理页按流程维度维护时限。</p>
        <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={slaPolicyId} onChange={e=>setSlaPolicyId(e.target.value)}>
          <option value="">不关联（使用风险等级默认 SLA）</option>
          {slaPolicies.map(p=><option key={p.id} value={p.id}>{p.name}（{p.risk_level}）</option>)}
        </select>
      </CardContent></Card>

      <Card><CardContent className="p-4 space-y-3">
        <div className="flex items-center gap-2 text-sm font-semibold"><Clock className="h-4 w-4"/>OLA 关联</div>
        <p className="text-[11px] text-muted-foreground">可选关联一条 OLA 策略，用于限定流程步骤的处理时限与超时升级。未关联则定时器等节点按自身配置。</p>
        <select className="flex h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={olaPolicyId} onChange={e=>setOlaPolicyId(e.target.value)}>
          <option value="">不关联</option>
          {olaPolicies.map(p=><option key={p.id} value={p.id}>{p.name}</option>)}
        </select>
      </CardContent></Card>

      <Card><CardContent className="p-4 space-y-3">
        <div className="flex items-center gap-2 text-sm font-semibold"><Bell className="h-4 w-4"/>通知策略</div>
        <div className="flex flex-wrap gap-2">
          {Object.keys(chLabels).map(c=>(
            <button key={c} onClick={()=>toggleChannel(c)} className={`px-3 py-1.5 rounded-md text-xs border ${notifyConfig.channels.includes(c) ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-600 dark:text-emerald-400' : 'border-input text-muted-foreground'}`}>{chLabels[c]}</button>
          ))}
        </div>
        <div className="flex gap-4 text-xs">
          <label className="flex items-center gap-1.5 cursor-pointer"><input type="checkbox" className="h-3.5 w-3.5" checked={notifyConfig.on_create} onChange={e=>setNotifyConfig({...notifyConfig,on_create:e.target.checked})}/><span>创建时通知</span></label>
          <label className="flex items-center gap-1.5 cursor-pointer"><input type="checkbox" className="h-3.5 w-3.5" checked={notifyConfig.on_complete} onChange={e=>setNotifyConfig({...notifyConfig,on_complete:e.target.checked})}/><span>完成时通知</span></label>
        </div>
        <div><label className="text-xs font-medium">通知模板</label><textarea className="flex h-16 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1" value={notifyConfig.template} onChange={e=>setNotifyConfig({...notifyConfig,template:e.target.value})} placeholder="流程「{{process_name}}」新工单已创建"/></div>
      </CardContent></Card>
    </div>
  )
}

// ---- page ----
// 主表单解析统一走 lib/itsm-constants，工单页运行期渲染用的是同一份实现
const parseForm = parseFormSchema
function parseNotify(raw: any): { channels: string[]; on_create: boolean; on_complete: boolean; template: string } {
  const d = (typeof raw === 'string' && raw) ? (() => { try { return JSON.parse(raw) } catch { return {} } })() : (raw || {})
  return { channels: d.channels || [], on_create: d.on_create ?? true, on_complete: d.on_complete ?? true, template: d.template || '' }
}

export default function ITSMProcessDetailPage() {
  const params = useParams(); const router = useRouter()
  const id = String(params.id || '')
  const isNew = id === 'new'

  const { processes, getProcess, createProcess, updateProcess, fetchProcesses } = useITSMStore()
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { channels, fetchChannels } = useNotificationStore()
  const { users, fetch: fetchUsers } = useUserStore()
  const { groups, fetchGroups } = useIAMStore()

  const [loading, setLoading] = useState(!isNew)
  const [proc, setProc] = useState<Process | null>(null)
  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)

  const [steps, setSteps] = useState<ProcessStep[]>(() => isNew ? defaultSteps() : [])
  const [dirty, setDirty] = useState(false)
  const [selId, setSelId] = useState<string | null>(null)
  const [selEdge, setSelEdge] = useState<{ source: string; target: string } | null>(null)
  const [step, setStep] = useState<0 | 1 | 2>(0)

  // —— 撤销/重做历史栈（仅作用于画布 steps）——
  const stepsRef = useRef(steps)
  const historyRef = useRef<ProcessStep[][]>([])
  const futureRef = useRef<ProcessStep[][]>([])
  const [history, setHistory] = useState<ProcessStep[][]>([])
  const [future, setFuture] = useState<ProcessStep[][]>([])
  useEffect(() => { stepsRef.current = steps }, [steps])
  const pushHistory = useCallback(() => {
    const snap = stepsRef.current
    const nh = [...historyRef.current.slice(-49), snap]
    historyRef.current = nh; setHistory(nh)
    futureRef.current = []; setFuture([])
  }, [])
  const undo = useCallback(() => {
    if (historyRef.current.length === 0) return
    const prev = historyRef.current[historyRef.current.length - 1]
    const nh = historyRef.current.slice(0, -1)
    historyRef.current = nh; setHistory(nh)
    const nf = [...futureRef.current, stepsRef.current]
    futureRef.current = nf; setFuture(nf)
    setSteps(prev); setDirty(true)
  }, [])
  const redo = useCallback(() => {
    if (futureRef.current.length === 0) return
    const next = futureRef.current[futureRef.current.length - 1]
    const nf = futureRef.current.slice(0, -1)
    futureRef.current = nf; setFuture(nf)
    const nh = [...historyRef.current, stepsRef.current]
    historyRef.current = nh; setHistory(nh)
    setSteps(next); setDirty(true)
  }, [])
  // 网格吸附 / 一键对齐
  const [snap, setSnap] = useState(true)
  const alignToGrid = useCallback(() => {
    pushHistory()
    setSteps(prev => prev.map(s => ({
      ...s,
      position_x: Math.round((s.position_x ?? 0) / 16) * 16,
      position_y: Math.round((s.position_y ?? 0) / 16) * 16,
    })))
    setDirty(true)
  }, [pushHistory])

  // 流程级主表单 / SLA / 通知
  const [processForm, setProcessForm] = useState<FormSchema>({ version: 1, scope: '', fields: [] })
  const [slaPolicyId, setSlaPolicyId] = useState('')
  const [olaPolicyId, setOlaPolicyId] = useState('')
  const [notifyConfig, setNotifyConfig] = useState<{ channels: string[]; on_create: boolean; on_complete: boolean; template: string }>({ channels: [], on_create: true, on_complete: true, template: '' })
  const [slaPolicies, setSlaPolicies] = useState<{ id: string; name: string; risk_level: string }[]>([])
  const [olaPolicies, setOlaPolicies] = useState<{ id: string; name: string }[]>([])

  useEffect(() => {
    if (isNew) { setLoading(false); return }
    setLoading(true)
    Promise.all([
      fetchProcesses(),
      fetchScripts(),
      fetchChannels(),
      // 审批人选择器的候选来源：用户列表 + 用户组（任一不可用只降级为空列表，不阻断流程编辑）
      fetchUsers().catch(() => {}),
      fetchGroups().catch(() => {}),
      apiList<{ id: string; name: string; risk_level: string }>('/itsm/sla/policies').then(setSlaPolicies).catch(() => {}),
      apiList<{ id: string; name: string }>('/itsm/ola/policies').then(setOlaPolicies).catch(() => {}),
    ]).then(() => setLoading(false))
  }, [id])

  useEffect(() => {
    if (isNew || loading) return
    const p = processes.find(p => p.id === id)
    if (p) setProc(p)
  }, [processes, id, loading])

  // 流程切换时载入步骤 / 主表单 / SLA / 通知, 并清空脏标记
  useEffect(() => {
    if (!proc || isNew) return
    setSteps((proc.steps && proc.steps.length) ? proc.steps : defaultSteps())
    setProcessForm(parseForm(proc.form))
    setSlaPolicyId(proc.sla_policy_id || '')
    setOlaPolicyId('')
    setNotifyConfig(parseNotify(proc.notify_config))
    setDirty(false)
    setSelId(null)
    setSelEdge(null)
    setStep(0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proc?.id])

  const update = useCallback((sid: string, patch: Partial<ProcessStep>) => {
    pushHistory()
    setSteps(prev => prev.map(s => s.id === sid ? { ...s, ...patch } : s))
    setDirty(true)
  }, [pushHistory])

  const add = useCallback((type: ItStepType) => {
    const s = newStep(type, stepsRef.current.length)
    pushHistory()
    setSteps(prev => [...prev, s])
    setSelId(s.id)
    setDirty(true)
  }, [pushHistory])

  const remove = useCallback((sid: string) => {
    pushHistory()
    setSteps(prev => prev.filter(s => s.id !== sid).map(s => s.id === sid ? s : { ...s, depends_on: (s.depends_on || []).filter(d => d.nodeId !== sid) }))
    setSelId(prev => prev === sid ? null : prev)
    setSelEdge(prev => (prev && (prev.source === sid || prev.target === sid)) ? null : prev)
    setDirty(true)
  }, [pushHistory])

  const onConnect = useCallback((conn: Connection) => {
    if (!conn.source || !conn.target) return
    if (conn.source === conn.target) return   // 自环无意义
    const src = steps.find(s => s.id === conn.source)
    const tgt = steps.find(s => s.id === conn.target)
    if (!tgt) return
    const deps = tgt.depends_on || []
    if (deps.some(d => d.nodeId === conn.source)) return
    // 分支网关出口默认落在「默认分支(always)」上：排他网关待用户逐条填条件，
    // 并行网关本来就全分支执行，条件恒为 always。其余节点沿用 success。
    const cond: string = (src?.type === 'gateway' || src?.type === 'parallel') ? 'always' : 'success'
    update(conn.target, { depends_on: [...deps, { nodeId: conn.source, condition: cond }] })
    setSelId(null)
    setSelEdge({ source: conn.source, target: conn.target })
  }, [steps, update])

  /** 删除一条连线：本质是把目标节点的 depends_on 里对应上游剔除 */
  const removeEdge = useCallback((source: string, target: string) => {
    pushHistory()
    setSteps(prev => prev.map(s => s.id === target
      ? { ...s, depends_on: (s.depends_on || []).filter(d => d.nodeId !== source) }
      : s))
    setSelEdge(prev => (prev && prev.source === source && prev.target === target) ? null : prev)
    setDirty(true)
  }, [pushHistory])

  /** 改一条连线的条件（success / failure / always / 表达式），条件挂在连线而非网关节点上 */
  const setEdgeCond = useCallback((source: string, target: string, cond: string) => {
    pushHistory()
    setSteps(prev => prev.map(s => s.id === target
      ? { ...s, depends_on: (s.depends_on || []).map(d => d.nodeId === source ? { ...d, condition: cond } : d) }
      : s))
    setDirty(true)
  }, [pushHistory])

  const markFormDirty = useCallback(() => setDirty(true), [])

  // 画布撤销/重做快捷键（仅流程编排步骤，且焦点不在输入框时生效）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (step !== 1) return
      const t = e.target as HTMLElement
      if (t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)) return
      if (!(e.metaKey || e.ctrlKey)) return
      const k = e.key.toLowerCase()
      if (k === 'z') { e.preventDefault(); e.shiftKey ? redo() : undo() }
      else if (k === 'y') { e.preventDefault(); redo() }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [step, undo, redo])

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    const pid = await createProcess({ name: newName, category: 'change', steps: [], enabled: true, version: 1 })
    setCreating(false)
    if (pid) router.push(`/itsm/process/${pid}`)
  }

  const handleSave = async () => {
    if (!proc) return
    const err = validateFlow(steps)
    if (err) { toast.error(err); return }
    const payload = {
      ...proc,
      steps,
      form: processForm,
      sla_policy_id: slaPolicyId,
      notify_config: notifyConfig,
      version: (proc.version || 1) + 1,
    }
    const ok = await updateProcess(proc.id, payload)
    if (ok) { setProc({ ...proc, steps, form: processForm as any, sla_policy_id: slaPolicyId, notify_config: notifyConfig as any }); setDirty(false); }
  }

  const discard = () => {
    if (!proc) return
    setSteps((proc.steps && proc.steps.length) ? proc.steps : defaultSteps())
    setProcessForm(parseForm(proc.form))
    setSlaPolicyId(proc.sla_policy_id || '')
    setNotifyConfig(parseNotify(proc.notify_config))
    setDirty(false)
  }

  if (loading) return <div className="p-6 flex justify-center"><Loader2 className="h-8 w-8 animate-spin"/></div>
  if (isNew) return <div className="p-6 space-y-4"><div className="flex items-center gap-3"><Button variant="ghost" size="icon" onClick={()=>router.push('/itsm/process')}><CL className="h-4 w-4"/></Button><h1 className="text-xl font-bold">新建 ITSM 流程</h1></div><Card><CardContent className="p-6 space-y-3 max-w-xl"><Input placeholder="流程名称" value={newName} onChange={e=>setNewName(e.target.value)}/><Button onClick={handleCreate} disabled={creating||!newName.trim()}>{creating?'创建中...':'创建并进入向导'}</Button></CardContent></Card></div>
  if (!proc) return <div className="p-6 text-center"><p className="text-muted-foreground">流程不存在</p><Button variant="outline" className="mt-3" onClick={()=>router.push('/itsm/process')}>返回列表</Button></div>

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0">
        <Button variant="ghost" size="icon" onClick={()=>router.push('/itsm/process')}><CL className="h-4 w-4"/></Button>
        <div className="min-w-0"><h1 className="text-xl font-bold truncate">{proc.name}</h1><p className="text-xs text-muted-foreground">{proc.description||'无描述'} · {steps.length} 节点 · v{proc.version||1}</p></div>
      </div>
    </div>

    {/* 三步向导 stepper */}
    <div className="flex items-center gap-2">
      {WIZARD_STEPS.map((label, i) => {
        const activeStep = step === i
        const done = i < step
        return (
          <div key={label} className="flex items-center gap-2">
            <button
              onClick={() => setStep(i as 0|1|2)}
              className={`flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs transition-colors ${activeStep ? 'bg-accent font-semibold' : 'text-muted-foreground hover:text-foreground'}`}
            >
              <span className={`flex h-5 w-5 items-center justify-center rounded-full text-[10px] ${activeStep ? 'bg-emerald-500 text-white' : done ? 'bg-emerald-500/20 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'}`}>{i+1}</span>
              {label}
            </button>
            {i < WIZARD_STEPS.length - 1 && <ChevronRight className="h-4 w-4 text-muted-foreground/50" />}
          </div>
        )
      })}
    </div>

    {step === 0 && (
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">
          先设计整个流程唯一的<b>主表单</b>：从左侧把控件拖到中间画布，点击字段在右侧定义属性。每张工单都套用此表单；
          各流程节点不再单独建表单，只在「流程编排」里为本节点设置这些字段的<b>可见 / 可编辑</b>范围。
        </p>
        <FormDesigner schema={processForm} onChange={s => { setProcessForm(s); markFormDirty() }} systemFields={['risk_level','status','created_by','process_id']} />
        <div className="flex justify-end"><Button onClick={() => setStep(1)}>下一步：流程编排 <ChevronRight className="h-4 w-4 ml-1"/></Button></div>
      </div>
    )}

    {step === 1 && (
      <ReactFlowProvider>
        <BPMNCanvas steps={steps} scripts={scripts} channels={channels} mainForm={processForm}
          users={users} groups={groups}
          selId={selId} setSelId={setSelId} selEdge={selEdge} setSelEdge={setSelEdge}
          update={update} add={add} remove={remove} onConnect={onConnect}
          removeEdge={removeEdge} setEdgeCond={setEdgeCond}
          snap={snap} onToggleSnap={() => setSnap(v => !v)} onAlign={alignToGrid} />
        <div className="flex justify-between items-center">
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setStep(0)}><CL className="h-4 w-4 mr-1"/>上一步</Button>
            <Button variant="outline" size="sm" onClick={undo} disabled={history.length === 0} title="撤销 (Ctrl/Cmd+Z)"><Undo2 className="h-3.5 w-3.5 mr-1"/>撤销</Button>
            <Button variant="outline" size="sm" onClick={redo} disabled={future.length === 0} title="重做 (Ctrl/Cmd+Shift+Z)"><Redo2 className="h-3.5 w-3.5 mr-1"/>重做</Button>
          </div>
          <Button onClick={() => setStep(2)}>下一步：高级设置 <ChevronRight className="h-4 w-4 ml-1"/></Button>
        </div>
      </ReactFlowProvider>
    )}

    {step === 2 && (
      <div className="space-y-3">
        <AdvancedSettings
          proc={proc}
          slaPolicies={slaPolicies}
          olaPolicies={olaPolicies}
          slaPolicyId={slaPolicyId}
          setSlaPolicyId={v => { setSlaPolicyId(v); markFormDirty() }}
          olaPolicyId={olaPolicyId}
          setOlaPolicyId={v => { setOlaPolicyId(v); markFormDirty() }}
          notifyConfig={notifyConfig}
          setNotifyConfig={v => { setNotifyConfig(v); markFormDirty() }}
        />
        <div className="flex justify-between">
          <Button variant="outline" onClick={() => setStep(1)}><CL className="h-4 w-4 mr-1"/>上一步</Button>
          <Button onClick={handleSave}><Save className="h-3 w-3 mr-1"/>保存流程</Button>
        </div>
      </div>
    )}

    {dirty && step !== 2 && (
      <div className="fixed bottom-6 right-6 z-50 flex gap-2 bg-background border rounded-full shadow-lg px-4 py-2">
        <span className="text-sm text-amber-600 self-center">有未保存的修改</span>
        <Button size="sm" variant="outline" onClick={() => setStep(2)}>去高级设置保存</Button>
      </div>
    )}
  </div>
}
