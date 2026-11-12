
import { useEffect, useState, useMemo, useCallback, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
// React Flow 定位样式必须本页自引:Vite 按需注入 CSS,深链直达设计器时
// 若从未访问过 topology/ProcessFlowView,style.css 不会加载 → position:absolute
// 全失效,画布 DOM 被按文档流堆到页面底部(视觉上只剩一团黑块)。
import '@xyflow/react/dist/style.css'
// @xyflow/react 必须静态整包引入：本文件同时静态引了 Position/getSmoothStepPath 等,
// 库代码本就在主 bundle,把 Handle/ReactFlow 拆成异步 dynamic 会让句柄晚于节点测量挂载,
// 触发 reactflow error#008(handle id null)——一大半连线静默不渲染,且 resize 不自愈。
import {
  ReactFlow, Controls, Background, MiniMap, Handle, ReactFlowProvider, useReactFlow,
  BaseEdge, EdgeLabelRenderer, Position, getSmoothStepPath, MarkerType, BackgroundVariant,
} from '@xyflow/react'
import type { Node, NodeProps, EdgeProps, Connection, Edge } from '@xyflow/react'
import { useProcessStore } from '@/stores/itsm/processes'
import type { Process, ProcessStep, TicketNumberRule, DryRunResult, DecisionBinding } from '@/stores/itsm/types'
import { useAgentStore } from '@/stores/agent'
import { useScriptStore } from '@/stores/script'
import { useNotificationStore } from '@/stores/notification'
import { useUserStore } from '@/stores/user'
import { useIAMStore } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { formatTime } from '@/lib/utils'
import { apiList, apiFetch } from '@/lib/utils'
import { NOTIFY_CHANNEL_LABELS } from '@/lib/itsm-constants'
import { FormDesigner } from '@/components/itsm/FormDesigner'
import { FormRulesEditor } from '@/components/itsm/FormRulesEditor'
import { type FormSchema } from '@/components/itsm/DynamicForm'
import { parseFormSchema, normalizeStepFieldPerm, type StepFieldPerm } from '@/lib/itsm-constants'
import { getMessage } from '@/lib/message'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import {
  Plus, Trash2, Save, X, Pencil, ChevronLeft, Loader2,
  PlayCircle, StopCircle, UserCheck, GitBranch, Hourglass, Hash,
  Clock, Bell, Server, FolderTree, FileText, Settings2, ChevronRight, ChevronLeft as CL,
  Grid3x3, Undo2, Redo2, Users, Search, Route, AlertTriangle, Wand2, GitMerge, UserPlus, Variable, FlaskConical, Table2,
} from 'lucide-react'
import { App, Button, Form, Input, Modal, Popover, Tag } from 'antd'
import { ErrorState } from '@/components/ui/error-state'

// ---- types & meta ----
type ItStepType = ProcessStep['type']

const WIZARD_STEPS = ['表单设计', '流程编排', '高级设置'] as const

const META: Record<ItStepType, { label: string; icon: any; color: string }> = {
  start:    { label: '开始事件', icon: PlayCircle, color: '#3B82F6' },
  end:      { label: '结束事件', icon: StopCircle, color: '#ef4444' },
  user:     { label: '人工任务', icon: UserCheck, color: '#6366f1' },
  gateway:  { label: '排他网关', icon: GitBranch, color: '#8b5cf6' },
  timer:    { label: '定时器',   icon: Clock,     color: '#f97316' },
  notify:   { label: '通知',     icon: Bell,      color: '#ef4444' },
  service:  { label: '服务任务', icon: Server,    color: '#14b8a6' },
  parallel: { label: '并行网关', icon: FolderTree, color: '#06b6d4' },
  join:     { label: '汇聚网关', icon: GitMerge,  color: '#0ea5e9' },
  dispatch: { label: '派单节点', icon: UserPlus,  color: '#d946ef' },
  subflow:  { label: '子流程',   icon: FolderTree, color: '#8b5cf6' },
  wait:     { label: '等待条件', icon: Hourglass, color: '#eab308' },
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

// ---- 流程引擎动作库（与 internal/automation/actions 常量及参数描述符一一对齐）----
// service 步骤声明 action_type 后，参数统一写入 step.inputs：引擎先 substMap 展开 ${...}
// （instance.go:358），动作执行器再对字符串值做一次 tpl 兜底；缺失变量替换为空串，不阻断流程。
type ActionParamKind = 'text' | 'textarea' | 'select' | 'script' | 'kv'
interface ActionParamDef {
  key: string
  label: string
  kind: ActionParamKind
  required?: boolean
  placeholder?: string
  hint?: string
  options?: { value: string; label: string }[]
  /** kv 且 nested=true：值存为 inputs[key] 对象；否则为顶层额外参数（脚本入参 / create_ticket 自定义字段） */
  nested?: boolean
  kvKeyPlaceholder?: string
  kvValuePlaceholder?: string
}
interface ActionDef {
  type: string
  label: string
  desc: string
  params: ActionParamDef[]
}
const ACTION_DEFS: ActionDef[] = [
  {
    type: 'http', label: 'HTTP 请求',
    desc: '外发 HTTP 请求。url / body / headers 值支持 ${...} 变量；输出 status_code、body，可由下游 ${steps.<节点ID>.result.body} 引用。',
    params: [
      { key: 'method', label: '请求方法', kind: 'select', options: [
        { value: 'GET', label: 'GET' }, { value: 'POST', label: 'POST' }, { value: 'PUT', label: 'PUT' },
        { value: 'PATCH', label: 'PATCH' }, { value: 'DELETE', label: 'DELETE' },
      ], hint: '默认 GET；带请求体时自动补 Content-Type: application/json。' },
      { key: 'url', label: '请求 URL', kind: 'text', required: true, placeholder: 'https://api.example.com/hooks/${ticket.number}' },
      { key: 'body', label: '请求体', kind: 'textarea', placeholder: '{"title": "${ticket.title}"}' },
      { key: 'headers', label: '请求头', kind: 'kv', nested: true, kvKeyPlaceholder: 'Header 名', kvValuePlaceholder: '值（支持 ${...}）' },
    ],
  },
  {
    type: 'notify', label: '通知分发',
    desc: '经通知中心按事件订阅策略分发。event 为事件名，title / content 支持 ${...} 变量。',
    params: [
      { key: 'event', label: '事件名', kind: 'text', required: true, placeholder: '如 ticket.created / custom.alert' },
      { key: 'title', label: '通知标题', kind: 'text', placeholder: '${ticket.title}' },
      { key: 'content', label: '通知内容', kind: 'textarea', placeholder: '工单 ${ticket.number} 已流转' },
    ],
  },
  {
    type: 'create_ticket', label: '创建工单',
    desc: '按参数创建一张 ITSM 工单，输出 ticket_id。保留字段之外的额外参数会写入新工单的 custom_fields。',
    params: [
      { key: 'title', label: '工单标题', kind: 'text', required: true, placeholder: '由 ${ticket.title} 派生的工单' },
      { key: 'description', label: '描述', kind: 'textarea' },
      { key: 'risk_level', label: '风险等级', kind: 'select', options: [
        { value: '', label: '不指定' }, { value: 'low', label: '低' }, { value: 'medium', label: '中' },
        { value: 'high', label: '高' }, { value: 'critical', label: '紧急' },
      ] },
      { key: 'change_plan', label: '变更方案', kind: 'textarea' },
      { key: 'created_by', label: '创建人', kind: 'text', placeholder: '留空默认 action' },
      { key: '__extra__', label: '额外字段（写入 custom_fields）', kind: 'kv', kvKeyPlaceholder: '字段名', kvValuePlaceholder: '值（支持 ${...}）' },
    ],
  },
  {
    type: 'script', label: '执行脚本（动作库通道）',
    desc: '走动作库 script 通道执行脚本库脚本；除 script_id 外的参数全部作为脚本入参下发（对齐 ScriptExecutor.Execute）。',
    params: [
      { key: 'script_id', label: '关联脚本', kind: 'script', required: true },
      { key: '__extra__', label: '脚本入参', kind: 'kv', kvKeyPlaceholder: '参数名', kvValuePlaceholder: '值（支持 ${...}）' },
    ],
  },
  {
    type: 'cmdb_update', label: '更新 CMDB 配置项',
    desc: '更新指定 CI 的属性；fields 的值支持 ${...} 变量。输出 ci_id、updated。',
    params: [
      { key: 'ci_id', label: 'CI ID', kind: 'text', required: true, placeholder: 'CI ID 或 ${custom_fields.ci_id}' },
      { key: 'fields', label: '更新字段', kind: 'kv', nested: true, kvKeyPlaceholder: '属性名', kvValuePlaceholder: '属性值（支持 ${...}）' },
    ],
  },
]
function actionDef(type?: string): ActionDef | undefined {
  return ACTION_DEFS.find(d => d.type === type)
}
function actionLabel(type?: string): string {
  return actionDef(type)?.label || type || ''
}

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
    default:        return { color: '#3B82F6', label: '' }
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
/**
 * 自动分层布局：把 DAG 排成自左向右的 BPMN 泳道。
 *  - 层号 = 从开始节点出发的最长路径深度（开始层 0），网关分支天然同层并排；
 *  - 每层内保持 steps 原顺序，垂直居中均分；
 *  - 输出新的 steps（不可变，未变化节点引用不变）。
 */
const LAYOUT_COL_W = 280   // 层间距（容纳卡片宽 + 手柄余量）
const LAYOUT_ROW_H = 116   // 层内垂直间距
const LAYOUT_X0 = 96
const LAYOUT_Y0 = 224
function layoutFlow(steps: ProcessStep[]): ProcessStep[] {
  if (steps.length === 0) return steps
  const ids = new Set(steps.map(s => s.id))
  // 邻接表 src → [tgt]（依赖方向：tgt.depends_on 里指向 src）
  const succ = new Map<string, string[]>()
  const indeg = new Map<string, number>()
  for (const s of steps) { succ.set(s.id, []); indeg.set(s.id, 0) }
  for (const s of steps) {
    for (const d of s.depends_on || []) {
      if (!ids.has(d.nodeId) || !succ.has(d.nodeId)) continue
      succ.get(d.nodeId)!.push(s.id)
      indeg.set(s.id, (indeg.get(s.id) || 0) + 1)
    }
  }
  // Kahn 拓扑排序同时算最长路径层号
  const layer = new Map<string, number>()
  const q: string[] = []
  for (const s of steps) { layer.set(s.id, 0); if ((indeg.get(s.id) || 0) === 0) q.push(s.id) }
  let head = 0
  while (head < q.length) {
    const u = q[head++]
    const lu = layer.get(u) || 0
    for (const v of succ.get(u) || []) {
      layer.set(v, Math.max(layer.get(v) || 0, lu + 1))
      indeg.set(v, (indeg.get(v) || 0) - 1)
      if (indeg.get(v) === 0) q.push(v)
    }
  }
  // 兜底：环/孤立残留节点也给出层号，避免 NaN 定位
  const maxLayer = Math.max(...steps.map(s => layer.get(s.id) || 0))
  for (const s of steps) if ((layer.get(s.id) ?? -1) < 0) layer.set(s.id, maxLayer + 1)

  const byLayer = new Map<number, ProcessStep[]>()
  for (const s of steps) {
    const l = layer.get(s.id) ?? 0
    if (!byLayer.has(l)) byLayer.set(l, [])
    byLayer.get(l)!.push(s)
  }
  const keys = [...byLayer.keys()].sort((a, b) => a - b)
  const changed = new Set<string>()
  const pos = new Map<string, { x: number; y: number }>()
  for (const l of keys) {
    const list = byLayer.get(l)!
    const n = list.length
    list.forEach((s, i) => {
      pos.set(s.id, { x: LAYOUT_X0 + l * LAYOUT_COL_W, y: Math.round(LAYOUT_Y0 + (i - (n - 1) / 2) * LAYOUT_ROW_H) })
    })
  }
  return steps.map(s => {
    const p = pos.get(s.id)
    if (!p || (s.position_x === p.x && s.position_y === p.y)) return s
    changed.add(s.id)
    return { ...s, position_x: p.x, position_y: p.y }
  })
}

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

  // 汇聚网关语义（BPMN 汇合：多进一出，与并行网关配对）
  for (const j of steps.filter(s => s.type === 'join')) {
    const inbound = (j.depends_on || []).filter(d => idSet.has(d.nodeId))
    const outbound = steps.flatMap(s => (s.depends_on || []).filter(d => d.nodeId === j.id))
    if (inbound.length < 2) return `汇聚网关「${j.name}」至少需要 2 条入口连线（当前 ${inbound.length} 条）：汇聚是多进一出`
    if (outbound.length !== 1) return `汇聚网关「${j.name}」应当只有 1 条出口连线（当前 ${outbound.length} 条）：汇聚后沿单线继续`
  }

  // 动作库必填参数校验：service 节点声明 action_type 后，参数缺失会在运行期报错并走 failure 分支，
  // 因此在保存前按与后端 ExecuteAction 一致的参数描述符做一次硬校验（旧 script_id 路径不做要求）。
  for (const s of steps.filter(s => s.type === 'service' && s.action_type)) {
    const def = actionDef(s.action_type)
    if (!def) return `流程未设计完整：服务任务「${s.name}」使用了未知动作类型「${s.action_type}」`
    const inputs = (s.inputs || {}) as Record<string, any>
    for (const p of def.params) {
      if (!p.required) continue
      const v = inputs[p.key]
      if (typeof v !== 'string' || !v.trim()) {
        return `流程未设计完整：服务任务「${s.name}」的动作「${def.label}」缺少必填参数「${p.label}」`
      }
    }
  }

  return null
}

// 画布节点 data 为展示用途,字段松散;用宽松类型规避 @xyflow/react v12 严格的 NodeProps 默认类型
type FlowNodeData = Record<string, any>
type FlowNodeProps = NodeProps<Node<FlowNodeData>>

// 表决方式的画布短标签（与后端 voteLabel 语义一致）
const APPROVAL_SHORT: Record<string, string> = {
  countersign: '会签', orsign: '或签', responded_any: '全员表态',
  percent: '比例表决', count: '数量表决',
}

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
    {data.timeoutDuration && <p className="text-[10px]">⏱ {data.timeoutDuration} · {data.timeoutActionLabel}</p>}
    {data.reminderInterval && <p className="text-[10px]">🔔 每 {data.reminderInterval} 催办</p>}
  </BPMNBox>
}

/** 分支网关（排他 / 并行）：一进多出——左侧单入口，右侧单出口句柄可连出任意多条分支连线 */
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
    <OutHandle color={color} size={9}/>
  </div>
}
function BPMNGateway({ data }: FlowNodeProps) {
  return <GatewayDiamond data={data} color={META.gateway.color} mark="×"/>
}
function BPMNParallel({ data }: FlowNodeProps) {
  return <GatewayDiamond data={data} color={META.parallel.color} mark="+"/>
}

/** 汇聚网关（join）：多进一出 —— 左侧单句柄可接收多条分支连线，右侧单出口 */
function BPMNJoin({ data }: FlowNodeProps) {
  const inc = data.inCount || 0
  return <div className="relative flex items-center justify-center w-24 h-18">
    <InHandle color={META.join.color} size={9}/>
    <div className="w-[52px] h-[52px] rotate-45 border-2 flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm" style={{ borderColor: META.join.color }}>
      <span className="-rotate-45 text-base font-bold leading-none" style={{ color: META.join.color }}>∩</span>
    </div>
    <div className="absolute -bottom-4 left-1/2 -translate-x-1/2 whitespace-nowrap text-[10px] font-medium">
      {data.label}
      <span className="ml-1 text-muted-foreground">({inc} 入口)</span>
    </div>
    <OutHandle color={META.join.color} size={9}/>
  </div>
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
    {data.actionTypeLabel && <p className="text-[10px] truncate font-medium">⚙ {data.actionTypeLabel}</p>}
    {(data.actionScriptName || data.scriptName) && <p className="text-[10px] truncate">{data.actionScriptName || data.scriptName}</p>}
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

/** 派单节点（dispatch）：先派单再处理 —— auto 自动派给解析出的受派人 / manual 等人工指派 */
function BPMNDispatch({ data }: FlowNodeProps) {
  return <BPMNBox color={META.dispatch.color} icon={<UserPlus className="h-3.5 w-3.5"/>} data={data}>
    <p className="text-[10px] truncate">
      {data.dispatchMode === 'manual' ? '✋ 手动派单' : '⚡ 自动派单'}
      {(data.assigneeVar || data.assignee) && <span className="ml-1">→ {data.assigneeVar || data.assignee}</span>}
    </p>
  </BPMNBox>
}

const nodeTypes: Record<string, any> = {
  start: BPMNStart, end: BPMNEnd, user: BPMNUser,
  gateway: BPMNGateway, timer: BPMNTimer, notify: BPMNNotify,
  service: BPMNService, parallel: BPMNParallel, subflow: BPMNSubflow, join: BPMNJoin,
  dispatch: BPMNDispatch, wait: BPMNWait,
}

/** 等待条件节点（Wait for Condition）：BPMN 中间捕获事件样式（双圆 + 沙漏），条件成立自动继续 */
function BPMNWait({ data }: FlowNodeProps) {
  return <div className="relative">
    <InHandle color={META.wait.color}/>
    <div className="w-[104px] h-[52px] rounded-full border-2 border-dashed flex items-center justify-center bg-white dark:bg-gray-800 shadow-sm gap-2" style={{ borderColor: META.wait.color }}>
      <Hourglass className="h-5 w-5" style={{ color: META.wait.color }}/>
      <span className="text-[10px] font-medium">{data.label}</span>
    </div>
    <OutHandle color={META.wait.color}/>
    {data.waitCondition && <p className="text-[10px] text-center mt-0.5 truncate max-w-[140px] mx-auto" title={data.waitCondition}>⏳ {data.waitCondition}</p>}
  </div>
}

// ---- 可删除连线（悬停出现「×」，选中高亮；Delete 键同样可删）----
function FlowEdge({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, data, selected }: EdgeProps) {
  const d = (data || {}) as Record<string, any>
  // borderRadius 收到 2：BPMN 惯例是利落直角拐弯。14px 大圆角在小高度差时
  // 两段圆弧拼成 S 波浪，视觉上像"自由弯曲"而不是流程图的正交连线。
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 2 })
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

/**
 * fitSignal 变化时重新 fitView。
 * 必须显式触发：自动分层布局在进入画布后异步改写节点坐标,
 * ReactFlow 的初始化 fitView 只在首次测量时算一次, 布局后视口不再跟随,
 * 整张图会停在错误的原点（节点滑出可视区）。
 */
function AutoFitter({ signal }: { signal: number }) {
  const rf = useReactFlow()
  const first = useRef(true)
  useEffect(() => {
    let cancelled = false
    const t = setTimeout(() => {
      // fitView 在节点未完成测量时会被 React Flow 排队, 测量完成后才真正执行,
      // 所以缩放下限必须挂在 fitView 的 Promise 后面, 不能同步判断。
      rf.fitView({ padding: 0.12, duration: first.current ? 0 : 250, maxZoom: 1 })
        .then(() => {
          if (cancelled) return
          // 长流程全览缩放会低至 0.2、文字不可读：抬到 0.5 可读下限,
          // 用户可用右下角 Controls 的 fit 按钮随时回全貌。
          if (rf.getZoom() < 0.5) return rf.zoomTo(0.5, { duration: first.current ? 0 : 250 })
        })
        .finally(() => { if (!cancelled) first.current = false })
    }, 80)
    return () => { cancelled = true; clearTimeout(t) }
  }, [signal, rf])
  return null
}

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
      <Button size="middle" className="h-6 px-2 text-[10px]" onClick={() => setOpen(true)}>
        <Users className="h-3 w-3 mr-1"/>选择
      </Button>
    </div>
    {value.length === 0
      ? <p className="text-[10px] text-muted-foreground">未指定审批人。可从用户列表 / 用户组选择，也可只用下方「从表单字段取审批人」。</p>
      : <div className="flex flex-wrap gap-1">
          {value.map(v => (
            <Tag key={v} color="default" className="text-[10px] gap-1 pr-1">
              {v}
              <button type="button" onClick={() => onChange(value.filter(x => x !== v))} className="text-muted-foreground hover:text-destructive" aria-label={`移除 ${v}`}>×</button>
            </Tag>
          ))}
        </div>}

    <Modal open={open} onCancel={() => setOpen(false)} title="选择审批人" footer={null}>
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
          <Button size="middle" className="h-8 text-[11px] shrink-0"
            disabled={!manual.trim()} onClick={() => { onChange(uniq([...value, manual])); setManual('') }}>添加</Button>
        </div>
        <div className="flex items-center justify-between">
          <span className="text-[11px] text-muted-foreground">已选 {value.length} 人</span>
          <div className="flex gap-2">
            <Button onClick={() => onChange([])}>清空</Button>
            <Button type="primary" size="middle" onClick={() => setOpen(false)}>完成</Button>
          </div>
        </div>
      </div>
      </Modal>
  </div>
}

// ---- 自动节点变量链路提示：列出节点配置里可用的 ${} 占位符 ----
function VariablesHint({ steps, mainFields }: { steps: ProcessStep[]; mainFields: { key: string }[] }) {
  const svcIds = steps.filter(s => s.type === 'service').slice(0, 2).map(s => s.id).join(' / ') || '<脚本节点ID>'
  const fieldKey = mainFields[0]?.key || '<字段key>'
  return (
    <details className="text-[10px] text-muted-foreground border border-input rounded p-2">
      <summary className="cursor-pointer select-none font-medium">可用变量（自动节点变量链路）</summary>
      <div className="mt-1 space-y-0.5">
        <p><code>{'${ticket.title}'}</code> 等 — 工单字段（title / risk_level / status / created_by / assignee）</p>
        <p><code>{`\${custom_fields.${fieldKey}}`}</code> — 工单自定义表单字段</p>
        <p><code>{`\${steps.${svcIds}.output}`}</code> — 上游脚本/动作节点的输出</p>
        <p><code>{`$\{steps.<节点ID>.status}`}</code> — 上游节点结果（success / failure）</p>
        <p><code>{'${output.*}'}</code> — 上一次工单操作产出（status / risk_level 等）</p>
        <p className="pt-0.5">变量缺失时替换为空串，不阻断流程推进。</p>
      </div>
    </details>
  )
}

// ---- 数据 pill 式变量插入器（对齐 SN Data Pill 交互）----
// 把手写 ${} 升级为「点选插入」：按命名空间分组列出可用变量 pill，
// 点击即把 ${token} 追加进目标输入框。steps 里的节点输出按「本节点上游」过滤，
// 保证不会插入流程中尚未产出的变量（数据血缘在 UI 层可见）。
function VarPillPicker({ steps, mainFields, currentStepId, onInsert }: {
  steps: ProcessStep[]
  mainFields: { key: string; label?: string }[]
  currentStepId?: string
  onInsert: (token: string) => void
}) {
  const [open, setOpen] = useState(false)
  // 上游节点 = 依赖闭包（从 currentStepId 沿 depends_on 反向可达的全部节点）
  const upstream = useMemo(() => {
    if (!currentStepId) return new Set(steps.map(s => s.id))
    const pred = new Map<string, string[]>()
    for (const s of steps) for (const d of s.depends_on || []) {
      if (!pred.has(s.id)) pred.set(s.id, [])
      pred.get(s.id)!.push(d.nodeId)
    }
    const seen = new Set<string>()
    const q = [currentStepId]
    while (q.length) {
      const u = q.shift()!
      for (const v of pred.get(u) || []) if (!seen.has(v)) { seen.add(v); q.push(v) }
    }
    return seen
  }, [steps, currentStepId])

  const groups: { label: string; items: { token: string; desc?: string }[] }[] = [
    {
      label: '工单字段',
      items: ['title', 'status', 'risk_level', 'priority', 'created_by', 'assignee']
        .map(k => ({ token: `ticket.${k}`, desc: k })),
    },
    {
      label: '表单字段',
      items: (mainFields || []).map(f => ({ token: `custom_fields.${f.key}`, desc: f.label || f.key })),
    },
    {
      label: '上游节点输出',
      items: steps
        .filter(s => s.type !== 'start' && upstream.has(s.id))
        .flatMap(s => [
          { token: `steps.${s.id}.output`, desc: `${s.name} 的输出` },
          { token: `steps.${s.id}.status`, desc: `${s.name} 的结果(success/failure)` },
        ]),
    },
  ].filter(g => g.items.length > 0)

  const pick = (token: string) => { onInsert(token); setOpen(false) }

  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      trigger="click"
      content={
        <div className="w-72 max-h-72 overflow-y-auto space-y-2 text-xs">
          {groups.length === 0 && <p className="text-muted-foreground p-1">没有可用的变量</p>}
          {groups.map(g => (
            <div key={g.label}>
              <p className="text-[10px] font-semibold text-muted-foreground mb-1">{g.label}</p>
              <div className="flex flex-wrap gap-1">
                {g.items.map(it => (
                  <button key={it.token} type="button" title={it.desc} onClick={() => pick(it.token)}
                    className="rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[10px] font-mono text-sky-600 dark:text-sky-400 hover:bg-sky-500/25 transition-colors">
                    {it.token}
                  </button>
                ))}
              </div>
            </div>
          ))}
          <p className="text-[10px] text-muted-foreground">点击 pill 追加 <code>{'${变量}'}</code> 到输入框末尾。</p>
        </div>
      }
    >
      <button type="button"
        className="inline-flex items-center gap-1 h-6 px-2 rounded border border-input text-[10px] text-muted-foreground hover:bg-accent hover:text-foreground">
        <Variable className="h-3 w-3" />插入变量
      </button>
    </Popover>
  )
}

// ---- 键值对参数编辑器（headers / fields / 脚本入参 / 自定义字段）----
// 行数据存于本地 state，保证「新增空行」在填写 key 期间不会被父级 inputs 过滤掉而消失；
// 提交时同步 emitted 指纹，外部变更（撤销/重做、切换动作类型）才会覆盖本地行。
function KVEditor({ rows, onChange, steps, mainFields, currentStepId, keyPlaceholder, valuePlaceholder }: {
  rows: [string, string][]
  onChange: (rows: [string, string][]) => void
  steps: ProcessStep[]
  mainFields: { key: string; label?: string }[]
  currentStepId?: string
  keyPlaceholder?: string
  valuePlaceholder?: string
}) {
  const [local, setLocal] = useState<[string, string][]>(() => rows.length ? rows : [['', '']])
  const emitted = useRef<string>(JSON.stringify(rows))
  useEffect(() => {
    const sig = JSON.stringify(rows)
    if (sig !== emitted.current) {
      emitted.current = sig
      setLocal(rows.length ? rows : [['', '']])
    }
  }, [rows])

  const commit = (next: [string, string][]) => {
    setLocal(next)
    const kept: [string, string][] = next.filter(([k]) => k.trim()).map(([k, v]) => [k.trim(), v])
    emitted.current = JSON.stringify(kept)
    onChange(kept)
  }
  const setRow = (i: number, patch: { k?: string; v?: string }) => {
    commit(local.map(([k, v], j) => (j === i ? [patch.k ?? k, patch.v ?? v] : [k, v]) as [string, string]))
  }

  return <div className="space-y-1">
    {local.map(([k, v], i) => (
      <div key={i} className="flex items-center gap-1.5">
        <Input className="h-7 w-28 font-mono text-[11px]" value={k} placeholder={keyPlaceholder || '参数名'}
          onChange={e => setRow(i, { k: e.target.value })}/>
        <Input className="h-7 flex-1 font-mono text-[11px]" value={v} placeholder={valuePlaceholder || '值'}
          onChange={e => setRow(i, { v: e.target.value })}/>
        <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={currentStepId}
          onInsert={t => setRow(i, { v: v + '${' + t + '}' })}/>
        <button type="button" className="text-red-400 hover:text-red-600 text-xs shrink-0" title="删除该行"
          onClick={() => commit(local.filter((_, j) => j !== i))}>×</button>
      </div>
    ))}
    <button type="button" className="text-[11px] text-blue-500 hover:underline"
      onClick={() => commit([...local, ['', '']])}>+ 添加一行</button>
  </div>
}

// ---- 服务任务动作库配置面板：action_type 下拉 + 按类型生成的参数表单 ----
// 参数统一写入 step.inputs（后端 instance.go 用 substMap(cur.Inputs) 作为动作参数）；
// action_type 留空时退回 Step.script_id 旧路径，并暴露脚本入参 KV（此前只能选脚本、不能传参）。
function ActionConfigPanel({ step, steps, scripts, mainFields, onChange }: {
  step: ProcessStep; steps: ProcessStep[]; scripts: any[]
  mainFields: { key: string; label?: string }[]
  onChange: (p: Partial<ProcessStep>) => void
}) {
  // 动作模板：两种消费方式并存 —— 引用（运行期按已发布版本解析）/ 复制为快照（配置期解耦）。
  interface ActionTemplateOption {
    id: string; name: string; action_type: string; inputs: Record<string, any>
    enabled: boolean; status?: string; version?: number
  }
  type TemplateMode = 'ref' | 'copy'
  const [templates, setTemplates] = useState<ActionTemplateOption[]>([])
  const [tplLoading, setTplLoading] = useState(false)
  const [tplId, setTplId] = useState('')
  const [pendingTpl, setPendingTpl] = useState<{ t: ActionTemplateOption; mode: TemplateMode } | null>(null)
  const loadTemplates = async () => {
    setTplLoading(true)
    try {
      setTemplates((await apiList<ActionTemplateOption>('/itsm/action-templates')) || [])
    } catch (e) {
      getMessage().error(`加载动作模板失败: ${String(e)}`)
    } finally {
      setTplLoading(false)
    }
  }
  useEffect(() => { loadTemplates() }, [])

  const commitTemplate = (t: ActionTemplateOption, mode: TemplateMode) => {
    if (mode === 'ref') {
      if (t.status !== 'published') {
        getMessage().error('该模板尚未发布，请先在「动作库」页面发布后再引用')
        return
      }
      onChange({ template_ref: { template_id: t.id, version: 0 }, action_type: t.action_type, inputs: {} })
      getMessage().success(`已引用模板「${t.name}」（跟随最新已发布版本）`)
    } else {
      onChange({ template_ref: undefined, action_type: t.action_type, inputs: { ...(t.inputs || {}) } })
      getMessage().success(`已复制模板「${t.name}」为节点快照（此后与模板解耦）`)
    }
    setPendingTpl(null)
    setTplId('')
  }
  const applyTemplate = (id: string, mode: TemplateMode) => {
    if (!id) return
    const t = templates.find(x => x.id === id)
    if (!t) return
    // 覆盖已有配置属于破坏性操作 → 二次确认（无配置时直接应用）。
    const hasConfig = !!step.action_type || !!step.template_ref || Object.keys((step.inputs || {})).length > 0
    if (hasConfig) setPendingTpl({ t, mode })
    else commitTemplate(t, mode)
  }

  const def = actionDef(step.action_type)
  const inputs = (step.inputs || {}) as Record<string, any>
  // 保留键 = 标量参数 + 嵌套 KV 参数；其余 inputs 键即「顶层额外参数」
  const reserved = new Set<string>((def?.params || []).filter(p => p.kind !== 'kv' || p.nested).map(p => p.key))
  const extras: [string, string][] = Object.entries(inputs)
    .filter(([k]) => !reserved.has(k))
    .map(([k, v]) => [k, typeof v === 'string' ? v : String(v ?? '')] as [string, string])

  const setParam = (key: string, value: string) => {
    const base = { ...inputs }
    if (value === '') delete base[key]
    else base[key] = value
    onChange({ inputs: base })
  }
  const writeExtras = (rows: [string, string][]) => {
    const base: Record<string, any> = { ...inputs }
    for (const k of Object.keys(base)) if (!reserved.has(k)) delete base[k]
    for (const [k, v] of rows) if (k.trim()) base[k.trim()] = v
    onChange({ inputs: base })
  }
  const writeNested = (key: string, rows: [string, string][]) => {
    const base: Record<string, any> = { ...inputs }
    const obj: Record<string, string> = {}
    for (const [k, v] of rows) if (k.trim()) obj[k.trim()] = v
    if (Object.keys(obj).length) base[key] = obj
    else delete base[key]
    onChange({ inputs: base })
  }
  const changeType = (v: string) => {
    if (v === (step.action_type || '')) return
    // 参数集随动作类型变化，切换时清空旧 inputs；切到 script 通道时沿用已选脚本 ID。
    const next: Record<string, any> = {}
    if (v === 'script' && step.script_id) next.script_id = step.script_id
    // 切换动作类型意味着不再以模板为准，解除引用避免后端仍用模板覆盖用户新选类型。
    const patch: Partial<ProcessStep> = { action_type: v || undefined, inputs: next, template_ref: undefined }
    // 从动作库 script 通道退回旧路径时，把已选脚本带回 script_id，避免要重新选一次。
    if (!v && step.action_type === 'script' && typeof inputs.script_id === 'string' && inputs.script_id) {
      patch.script_id = inputs.script_id
    }
    onChange(patch)
  }

  const renderParam = (p: ActionParamDef) => {
    const val = typeof inputs[p.key] === 'string' ? (inputs[p.key] as string) : ''
    if (p.kind === 'kv') {
      const rows: [string, string][] = p.nested
        ? Object.entries((inputs[p.key] as Record<string, any>) || {})
            .map(([k, v]) => [k, String(v ?? '')] as [string, string])
        : extras
      return <div key={p.key} className="space-y-1">
        <div className="flex items-center justify-between gap-2">
          <label className="text-xs font-medium">{p.label}</label>
        </div>
        <KVEditor key={`${step.id}:${step.action_type || 'legacy'}:${p.key}`} rows={rows}
          onChange={rs => p.nested ? writeNested(p.key, rs) : writeExtras(rs)}
          steps={steps} mainFields={mainFields} currentStepId={step.id}
          keyPlaceholder={p.kvKeyPlaceholder} valuePlaceholder={p.kvValuePlaceholder}/>
      </div>
    }
    return <div key={p.key} className="space-y-1">
      <div className="flex items-center justify-between gap-2">
        <label className="text-xs font-medium">{p.label}{p.required && <span className="text-destructive ml-0.5">*</span>}</label>
        {(p.kind === 'text' || p.kind === 'textarea') &&
          <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={step.id}
            onInsert={t => setParam(p.key, val + '${' + t + '}')}/>}
      </div>
      {p.kind === 'select' ? (
        <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
          value={val} onChange={e => setParam(p.key, e.target.value)}>
          {p.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      ) : p.kind === 'script' ? (
        <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
          value={val} onChange={e => setParam(p.key, e.target.value)}>
          <option value="">选择脚本</option>
          {scripts.map((s: any) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
      ) : p.kind === 'textarea' ? (
        <Input.TextArea rows={2} value={val} onChange={e => setParam(p.key, e.target.value)}
          className="font-mono text-xs" placeholder={p.placeholder}/>
      ) : (
        <Input value={val} onChange={e => setParam(p.key, e.target.value)} className="h-8 font-mono text-xs" placeholder={p.placeholder}/>
      )}
      {p.hint && <p className="text-[10px] text-muted-foreground">{p.hint}</p>}
    </div>
  }

  return <div className="rounded-md border border-border/60 p-2 space-y-2 bg-muted/20">
    <div className="flex items-center gap-2">
      <label className="text-xs font-medium whitespace-nowrap">动作模板</label>
      <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
        value={tplId} disabled={tplLoading} onChange={e => setTplId(e.target.value)}>
        <option value="">{tplLoading ? '加载模板中...' : '选择动作模板（可选）'}</option>
        {templates.filter(t => t.enabled !== false).map(t => (
          <option key={t.id} value={t.id}>
            {t.name} · {actionLabel(t.action_type)}{t.status === 'published' ? ` · v${t.version || 1}` : ' · 草稿'}
          </option>
        ))}
      </select>
      <button type="button" title="刷新模板列表" className="text-muted-foreground hover:text-primary shrink-0"
        onClick={loadTemplates}><Wand2 className="h-3.5 w-3.5"/></button>
    </div>
    <div className="flex items-center gap-2 flex-wrap">
      <Button size="small" disabled={!tplId} onClick={() => applyTemplate(tplId, 'ref')}>引用模板（跟随版本）</Button>
      <Button size="small" disabled={!tplId} onClick={() => applyTemplate(tplId, 'copy')}>复制为快照</Button>
      {step.template_ref && (
        <Button size="small" danger onClick={() => onChange({ template_ref: undefined })}>解除引用</Button>
      )}
    </div>
    <p className="text-[10px] text-muted-foreground -mt-1">
      「引用模板」运行期按已发布版本解析，模板改版后跟随版流程立即生效（仅已发布模板可引用）；
      「复制为快照」把参数写进本节点，此后与模板解耦。引用时下方参数留空即用模板值，填写则覆盖。
    </p>
    {step.template_ref && (
      <div className="rounded-md bg-blue-500/10 px-2 py-1.5 text-[11px] text-foreground">
        已引用「{templates.find(t => t.id === step.template_ref?.template_id)?.name || step.template_ref.template_id}」
        · {step.template_ref.version ? `锁定 v${step.template_ref.version}` : '跟随最新已发布版本'}。
        切换下方动作类型会自动解除引用。
      </div>
    )}
    <div>
      <label className="text-xs font-medium">动作类型（流程引擎动作库）</label>
      <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
        value={step.action_type || ''} onChange={e => changeType(e.target.value)}>
        <option value="">不启用 — 执行「关联脚本」旧路径</option>
        {ACTION_DEFS.map(d => <option key={d.type} value={d.type}>{d.label}</option>)}
      </select>
      <p className="text-[10px] text-muted-foreground mt-1">
        {def ? def.desc : '留空时走 Step.script_id 脚本路径；动作参数写入 inputs，运行期先按 ${...} 变量上下文展开。切换动作类型会清空已填参数。'}
      </p>
    </div>

    {def ? def.params.map(renderParam) : <>
      <div>
        <label className="text-xs font-medium">关联脚本</label>
        <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
          value={step.script_id || ''} onChange={e => onChange({ script_id: e.target.value })}>
          <option value="">选择脚本</option>
          {scripts.map((s: any) => <option key={s.id} value={s.id}>{s.name}</option>)}
        </select>
      </div>
      <div className="space-y-1">
        <label className="text-xs font-medium">脚本入参（键值对）</label>
        <KVEditor key={`${step.id}:legacy:extras`} rows={extras} onChange={writeExtras}
          steps={steps} mainFields={mainFields} currentStepId={step.id}
          keyPlaceholder="参数名" valuePlaceholder="值（支持 ${...}）"/>
        <p className="text-[10px] text-muted-foreground">键为脚本参数名；值支持 {'${...}'} 变量。特殊键 run_as 可指定执行身份。</p>
      </div>
    </>}
    <ConfirmDialog
      open={!!pendingTpl}
      onClose={() => setPendingTpl(null)}
      onConfirm={() => pendingTpl && commitTemplate(pendingTpl.t, pendingTpl.mode)}
      title={`${pendingTpl?.mode === 'ref' ? '引用' : '载入'}模板「${pendingTpl?.t.name || ''}」`}
      description="会覆盖当前节点已配置的动作类型与全部参数，且不保留原配置。确定继续？"
      confirmLabel="覆盖并继续"
    />
  </div>
}

// ---- 流程级决策表绑定：输出注入 decision.<alias> 变量命名空间 ----
// 与表单内嵌决策表（走 custom_fields）并存：让网关分支 / 派单 / 动作参数能直接引用资产。
interface DecisionAssetOption {
  id: string; name: string; status?: string; version?: number
}

function textToInputs(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const idx = line.indexOf('=')
    if (idx < 1) continue
    const k = line.slice(0, idx).trim()
    if (k) out[k] = line.slice(idx + 1).trim()
  }
  return out
}
function inputsToText(m?: Record<string, string>): string {
  return Object.entries(m || {}).map(([k, v]) => `${k}=${v}`).join('\n')
}

function DecisionBindingsPanel({ bindings, onChange }: {
  bindings: DecisionBinding[]
  onChange: (next: DecisionBinding[]) => void
}) {
  const [assets, setAssets] = useState<DecisionAssetOption[]>([])
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [sample, setSample] = useState('{}')
  const [testingIdx, setTestingIdx] = useState<number | null>(null)
  const [testResult, setTestResult] = useState('')
  const [deleteIdx, setDeleteIdx] = useState<number | null>(null)

  const loadAssets = useCallback(async () => {
    setLoading(true); setLoadError(null)
    try {
      setAssets((await apiList<DecisionAssetOption>('/itsm/decision-tables')) || [])
    } catch (e: any) {
      setLoadError(e.message || '加载决策表资产失败')
    } finally { setLoading(false) }
  }, [])
  useEffect(() => { loadAssets() }, [loadAssets])

  const patch = (i: number, p: Partial<DecisionBinding>) =>
    onChange(bindings.map((b, j) => (j === i ? { ...b, ...p } : b)))
  const add = () => onChange([...bindings, { asset_id: '', alias: '', version: 0, inputs: {} }])
  const remove = (i: number) => {
    onChange(bindings.filter((_, j) => j !== i))
    setDeleteIdx(null)
  }

  const preview = async (i: number) => {
    const b = bindings[i]
    if (!b.asset_id) { getMessage().error('请先选择决策表资产'); return }
    let values: Record<string, any> = {}
    try { values = JSON.parse(sample || '{}') }
    catch { getMessage().error('试算样例值不是合法 JSON'); return }
    setTestingIdx(i); setTestResult('')
    try {
      const res: any = await apiFetch(`/itsm/decision-tables/${b.asset_id}/preview`, {
        method: 'POST',
        body: JSON.stringify({ version: b.version || 0, values }),
      })
      setTestResult(`命中：${(res.fired || []).join('、') || '无'}；推导值：${JSON.stringify(res.values || {})}`)
    } catch (e: any) {
      getMessage().error(`试算失败: ${e.message}`)
    } finally { setTestingIdx(null) }
  }

  return (
    <Card><CardContent className="p-4 space-y-3">
      <div className="flex items-center justify-between flex-wrap gap-2">
        <div className="flex items-center gap-2 text-sm font-semibold"><Table2 className="h-4 w-4"/>流程级决策表绑定</div>
        <Button size="small" onClick={add}><Plus className="w-3.5 h-3.5 mr-1"/>添加绑定</Button>
      </div>
      <p className="text-[11px] text-muted-foreground">
        运行期按已发布版本求值，输出写入 <code>decision.&lt;别名&gt;.&lt;输出列&gt;</code>。
        网关条件可写 <code>decision.route.target == "L2"</code>，派单受派人可写 <code>decision.route.owner</code>，
        动作参数可用 <code>{'${decision.route.owner}'}</code>。版本 0 = 跟随最新已发布版本；表单内嵌决策表仍按原 custom_fields 链路工作。
      </p>
      {loadError && <ErrorState message={`加载决策表资产失败: ${loadError}`} onRetry={loadAssets} />}
      {bindings.length === 0 ? (
        <div className="border rounded-md py-6 text-center text-xs text-muted-foreground">
          暂无绑定。无需按决策结果分支/派单时可保持为空。
        </div>
      ) : (
        <div className="space-y-2">
          {bindings.map((b, i) => (
            <div key={i} className="rounded-md border border-border/60 p-3 space-y-2 bg-muted/20">
              <div className="grid grid-cols-12 gap-2 items-end">
                <div className="col-span-5">
                  <label className="text-[11px] font-medium">决策表资产（仅已发布）</label>
                  <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
                    value={b.asset_id} onChange={e => patch(i, { asset_id: e.target.value })}>
                    <option value="">{loading ? '加载中...' : '选择资产'}</option>
                    {assets.filter(a => a.status === 'published').map(a => (
                      <option key={a.id} value={a.id}>{a.name} · v{a.version || 1}</option>
                    ))}
                  </select>
                </div>
                <div className="col-span-3">
                  <label className="text-[11px] font-medium">别名（decision.&lt;别名&gt;）</label>
                  <Input size="small" className="h-8 mt-1" value={b.alias} placeholder="如 route"
                    onChange={e => patch(i, { alias: e.target.value.trim() })} />
                </div>
                <div className="col-span-2">
                  <label className="text-[11px] font-medium">版本（0=跟随）</label>
                  <Input type="number" min={0} size="small" className="h-8 mt-1" value={b.version ?? 0}
                    onChange={e => patch(i, { version: Number(e.target.value) || 0 })} />
                </div>
                <div className="col-span-2 flex gap-1 justify-end">
                  <Button size="small" loading={testingIdx === i} onClick={() => preview(i)}>试算</Button>
                  <Button size="small" danger onClick={() => setDeleteIdx(i)} aria-label="移除绑定">
                    <Trash2 className="w-3.5 h-3.5" /></Button>
                </div>
              </div>
              <div>
                <label className="text-[11px] font-medium">条件列输入映射（每行「条件列名=变量表达式」，留空按同名字段取值）</label>
                <Input.TextArea key={`${i}:inputs`} rows={2} className="font-mono text-xs mt-1"
                  defaultValue={inputsToText(b.inputs)} placeholder={'env=${custom_fields.env}\nrisk_level=${ticket.risk_level}'}
                  onBlur={e => patch(i, { inputs: textToInputs(e.target.value) })} />
              </div>
            </div>
          ))}
        </div>
      )}
      <div>
        <label className="text-[11px] font-medium">试算样例值（JSON，作为条件列取值）</label>
        <Input.TextArea rows={2} value={sample} onChange={e => setSample(e.target.value)}
          placeholder={'{"env":"prod","risk_level":"high"}'} className="font-mono text-xs mt-1" />
        {testResult && <p className="text-[11px] text-muted-foreground mt-1 font-mono break-all">{testResult}</p>}
      </div>
      <ConfirmDialog
        open={deleteIdx !== null}
        onClose={() => setDeleteIdx(null)}
        onConfirm={() => deleteIdx !== null && remove(deleteIdx)}
        title="移除决策表绑定"
        description="移除后引用该别名的网关条件 / 派单变量将取不到值，请确认下游配置已调整。"
        confirmLabel="移除"
      />
    </CardContent></Card>
  )
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
  // 节点只声明主表单字段在本节点的状态（可编辑/必填/只读/禁用/隐藏）。
  const [permOpen, setPermOpen] = useState(false)
  const mainFields = mainForm?.fields || []
  const permOf = useCallback((key: string): StepFieldPerm => {
    const hit = (step.form_fields || []).find(p => p.key === key)
    return normalizeStepFieldPerm(hit || { key, mode: 'editable' })  // 未声明 = 可编辑
  }, [step.form_fields])

  // 写回时始终落全量（对齐当前主表单字段集），避免主表单增删字段后残留脏 key
  const writePerms = (mutate: (p: StepFieldPerm) => StepFieldPerm) => {
    onChange({ form_fields: mainFields.map(f => mutate({ ...permOf(f.key) })) })
  }
  const setPerm = (key: string, patch: Partial<StepFieldPerm>) => {
    writePerms(p => {
      if (p.key !== key) return p
      const next = { ...p, ...patch } as StepFieldPerm
      if (next.mode === 'hidden') next.required = false   // 隐藏谈不上必填
      if (next.required && next.mode !== 'editable') next.mode = 'editable' // 必填必然可编辑
      return next
    })
  }
  const bulk = (mode: StepFieldPerm['mode']) => writePerms(p => ({ ...p, mode, required: mode === 'editable' ? p.required : false }))

  const visibleCount = mainFields.filter(f => permOf(f.key).mode !== 'hidden').length
  const editableCount = mainFields.filter(f => permOf(f.key).mode === 'editable').length
  const requiredCount = mainFields.filter(f => permOf(f.key).mode === 'editable' && permOf(f.key).required).length

  const removeDep = (nodeId: string) => {
    onChange({ depends_on: (step.depends_on || []).filter(d => d.nodeId !== nodeId) })
  }

  return <>
    <Card>
    <CardHeader className="py-3"><CardTitle className="text-sm">{META[step.type]?.label || step.type}</CardTitle></CardHeader>
    <CardContent className="space-y-2 text-sm">
      <div><label className="text-xs font-medium">名称</label><Input value={step.name} onChange={e=>onChange({name:e.target.value})} className="h-8 mt-1"/></div>
      <div><label className="text-xs font-medium">所属阶段</label><Input value={step.stage||''} onChange={e=>onChange({stage:e.target.value||undefined})} className="h-8 mt-1" placeholder="如：受理 / 处理 / 关闭（留空不参与阶段进度）"/></div>
      {step.type === 'user' && <>
        <ApproverPicker value={step.approvers || []} users={users} groups={groups} onChange={v => onChange({ approvers: v })}/>
        <div>
          <label className="text-xs font-medium">表决方式</label>
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
            value={step.approval_type || 'countersign'} onChange={e => onChange({ approval_type: e.target.value as ProcessStep['approval_type'] })}>
            <option value="countersign">会签 — 全部审批人通过才继续</option>
            <option value="orsign">或签 — 任一审批人通过即继续</option>
            <option value="responded_any">全员表态 — 所有人投完且任一通过</option>
            <option value="percent">比例表决 — 通过人数达到名册的百分比</option>
            <option value="count">数量表决 — 通过人数达到指定数量</option>
          </select>
          {(step.approval_type === 'percent' || step.approval_type === 'count') && (
            <Input type="number" min={1} className="h-8 mt-1"
              value={step.approval_threshold ?? (step.approval_type === 'percent' ? 50 : 1)}
              onChange={e => onChange({ approval_threshold: Number(e.target.value) || 0 })}
              placeholder={step.approval_type === 'percent' ? '百分比（默认 50）' : '人数（默认 1）'}/>
          )}
          <p className="text-[10px] text-muted-foreground mt-1">单审批人时各方式等价；多人时决定表决口径（对齐 ServiceNow 5 种语义）。</p>
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
        <div className="rounded-md border border-border/60 p-2 space-y-2 bg-muted/20">
          <p className="text-[10px] font-semibold text-muted-foreground">审批时间策略（对齐 SLA 超时语义）</p>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs font-medium">审批时限</label>
              <Input value={step.timeout_duration||''} onChange={e=>onChange({timeout_duration:e.target.value||undefined})} className="h-8 mt-1" placeholder="如 24h / 3d"/>
            </div>
            <div>
              <label className="text-xs font-medium">超时处置</label>
              <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1"
                value={step.timeout_action || 'notify_only'} onChange={e => onChange({ timeout_action: e.target.value as 'auto_approve' | 'auto_reject' | 'notify_only' })}>
                <option value="notify_only">仅催办通知（继续等待）</option>
                <option value="auto_approve">自动通过</option>
                <option value="auto_reject">自动驳回</option>
              </select>
            </div>
          </div>
          <div>
            <label className="text-xs font-medium">周期催办间隔</label>
            <Input value={step.reminder_interval||''} onChange={e=>onChange({reminder_interval:e.target.value||undefined})} className="h-8 mt-1" placeholder="如 24h（停留期间每 24h 催一次，留空不催办）"/>
          </div>
          <div>
            <label className="text-xs font-medium">工作日历（可选）</label>
            <Input value={step.calendar||''} onChange={e=>onChange({calendar:e.target.value||undefined})} className="h-8 mt-1" placeholder="服务时间窗口名称，如 8x5（时限按业务时间计算，跳过节假日）"/>
          </div>
          <p className="text-[10px] text-muted-foreground">
            超过时限后按处置策略执行：自动通过/驳回会完成工单状态迁移并推进流程（操作者记为 system）；仅催办则继续等待人工。时限留空表示不启用超时策略。配置工作日历后，时限与催办按业务时间累计（如 8x5 日历下 24h ≈ 3 个工作日）。
          </p>
        </div>
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
            <Button size="middle" className="h-6 px-2 text-[10px]"
              disabled={mainFields.length === 0}
              onClick={() => setPermOpen(true)}>
              <Pencil className="h-3 w-3 mr-1"/>配置
            </Button>
          </div>
          {mainFields.length === 0 ? (
            <p className="text-[10px] text-muted-foreground">流程主表单还没有字段，请先回到「表单设计」步骤定义字段。</p>
          ) : (
            <p className="text-[10px] text-muted-foreground">
              主表单共 {mainFields.length} 个字段，本节点可见 <b className="text-foreground">{visibleCount}</b> 个、可编辑 <b className="text-foreground">{editableCount}</b> 个、必填 <b className="text-foreground">{requiredCount}</b> 个。
              节点不单独设计表单，只决定主表单字段在此处的状态。
            </p>
          )}
        </div>
      </>}
      {(step.type === 'gateway' || step.type === 'parallel') && <GatewayPanel step={step} steps={steps} onPickEdge={onPickEdge}/>}
      {step.type === 'join' && <div>
        <label className="text-xs font-medium">汇聚模式</label>
        <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.join_mode||'all'} onChange={e=>onChange({join_mode:e.target.value as 'all'|'any'})}>
          <option value="all">全部到达（all）— 等待所有上游分支汇合</option>
          <option value="any">任一到达（any）— 首个上游到达即放行</option>
        </select>
        <p className="text-[10px] text-muted-foreground mt-1">与并行网关配对：把各分支的出线连到本节点，全部分支汇合后沿出线继续。</p>
        <VariablesHint steps={steps} mainFields={mainFields} />
      </div>}
      {step.type === 'dispatch' && <>
        <div>
          <label className="text-xs font-medium">派单方式</label>
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.dispatch_mode||'auto'} onChange={e=>onChange({dispatch_mode:e.target.value as 'auto'|'manual'})}>
            <option value="auto">自动派单 — 引擎解析受派人后自动指派并继续流转</option>
            <option value="manual">手动派单 — 停在本节点，人工在工单上执行「指派」后续跑</option>
          </select>
        </div>
        <div>
          <div className="flex items-center justify-between gap-2">
            <label className="text-xs font-medium">受派人（变量优先）</label>
            <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={step.id}
              onInsert={t => onChange({ assignee: (step.assignee || '') + '${' + t + '}' })}/>
          </div>
          <Input value={step.assignee_var||''} onChange={e=>onChange({assignee_var:e.target.value})} className="h-8 mt-1" placeholder="custom_fields.owner"/>
          <Input value={step.assignee||''} onChange={e=>onChange({assignee:e.target.value})} className="h-8 mt-1" placeholder="静态账号，或 ${custom_fields.owner}"/>
          <p className="text-[10px] text-muted-foreground mt-1">
            上栏填变量点路径（custom_fields.owner / steps.xxx.output.assignee / ticket.created_by），优先解析；
            下栏填静态账号，支持 {'${'}变量{'}'} 占位与逗号多候选（取首个非空）。两者都为空时自动派单按失败分支走。
          </p>
        </div>
        <VariablesHint steps={steps} mainFields={mainFields} />
      </>}
      {step.type === 'timer' && <div><label className="text-xs font-medium">超时时长</label><Input value={step.duration||''} onChange={e=>onChange({duration:e.target.value})} className="h-8 mt-1" placeholder="4h / 30m / 1d"/></div>}
      {step.type === 'wait' && <>
        <div>
          <div className="flex items-center justify-between gap-2">
            <label className="text-xs font-medium">等待条件（成立后自动继续）*</label>
            <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={step.id}
              onInsert={t => onChange({ condition: (step.condition || '') + '${' + t + '}' })}/>
          </div>
          <Input.TextArea rows={3} value={step.condition||''} onChange={e=>onChange({condition:e.target.value})} className="mt-1 font-mono text-xs" placeholder={`如 ticket.status == "resolved"`}/>
          <p className="text-[10px] text-muted-foreground mt-1">
            流程在此节点挂起，引擎每 30 秒重新求值条件，成立后沿「成功」分支自动继续。变量与网关同一套：ticket.* / custom_fields.* / steps.*。
          </p>
        </div>
        <VariablesHint steps={steps} mainFields={mainFields} />
      </>}
      {step.type === 'notify' && <>
        <div><label className="text-xs font-medium">通知渠道</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={step.notify_channel||''} onChange={e=>onChange({notify_channel:e.target.value})}><option value="">选择渠道</option>{channels.filter((c:any)=>c.enabled).map((c:any)=><option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><div className="flex items-center justify-between gap-2">
          <label className="text-xs font-medium">标题</label>
          <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={step.id}
            onInsert={t => onChange({ notify_title: (step.notify_title || '') + '${' + t + '}' })}/>
        </div><Input value={step.notify_title||''} onChange={e=>onChange({notify_title:e.target.value})} className="h-8 mt-1"/></div>
        <div><div className="flex items-center justify-between gap-2">
          <label className="text-xs font-medium">内容</label>
          <VarPillPicker steps={steps} mainFields={mainFields} currentStepId={step.id}
            onInsert={t => onChange({ notify_body: (step.notify_body || '') + '${' + t + '}' })}/>
        </div><Input.TextArea rows={3} value={step.notify_body||''} onChange={e=>onChange({notify_body:e.target.value})} className="mt-1"/></div>
      </>}
      {step.type === 'service' && <>
        <ActionConfigPanel step={step} steps={steps} scripts={scripts} mainFields={mainFields}
          onChange={onChange}/>
        <div className="rounded-md border border-border/60 p-2 space-y-2 bg-muted/20">
          <p className="text-[10px] font-semibold text-muted-foreground">失败重试策略</p>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs font-medium">重试次数</label>
              <Input type="number" min={0} max={5} className="h-8 mt-1" value={step.retry_count ?? 0}
                onChange={e=>onChange({ retry_count: Math.max(0, Math.min(5, Number(e.target.value) || 0)) })} placeholder="0 = 不重试"/>
            </div>
            <div>
              <label className="text-xs font-medium">重试间隔</label>
              <Input className="h-8 mt-1" value={step.retry_interval||''} onChange={e=>onChange({ retry_interval: e.target.value || undefined })}
                placeholder="如 10s / 1m（空=立即）" disabled={!step.retry_count}/>
            </div>
          </div>
          <p className="text-[10px] text-muted-foreground">执行失败按间隔自动重试，全部耗尽仍失败才走「失败」分支（对齐 SN REST Step 内置重试）。</p>
        </div>
      </>}
      {step.type === 'subflow' && <>
        <div><label className="text-xs font-medium">子流程ID</label><Input value={step.subflow_id||''} onChange={e=>onChange({subflow_id:e.target.value})} className="h-8 mt-1"/></div>
        <div className="rounded-md border border-border/60 p-2 space-y-2 bg-muted/20">
          <p className="text-[10px] font-semibold text-muted-foreground">显式传参（子流程内以 {'${params.参数名}'} 引用）</p>
          {(step.subflow_params&&Object.keys(step.subflow_params).length>0?Object.entries(step.subflow_params):[]).map(([k,v],i)=>(
            <div key={i} className="flex items-center gap-1.5">
              <Input className="h-7 w-24 font-mono text-[11px]" value={k} placeholder="参数名"
                onChange={e=>{
                  const next={...(step.subflow_params||{})}; delete next[k]
                  const nk=e.target.value.trim(); if(nk) next[nk]=v
                  onChange({subflow_params:next})
                }}/>
              <Input className="h-7 flex-1 text-[11px]" value={String(v??'')} placeholder="${custom_fields.owner}"
                onChange={e=>onChange({subflow_params:{...(step.subflow_params||{}),[k]:e.target.value}})}/>
              <button type="button" className="text-red-400 hover:text-red-600 text-xs" title="删除参数"
                onClick={()=>{const next={...(step.subflow_params||{})}; delete next[k]; onChange({subflow_params:next})}}>×</button>
            </div>
          ))}
          <button type="button" className="text-[11px] text-blue-500 hover:underline"
            onClick={()=>onChange({subflow_params:{...(step.subflow_params||{}),[`p${Object.keys(step.subflow_params||{}).length+1}`]:''}})}>+ 添加参数</button>
          <p className="text-[10px] text-muted-foreground">参数值支持 {'${...}'} 变量（如 {'${custom_fields.owner}'}）；纯引用保留原始形态。未配置时子流程全量继承父上下文。</p>
        </div>
        <div>
          <label className="text-xs font-medium">For Each 循环（可选）</label>
          <Input className="h-8 mt-1" value={step.for_each||''} onChange={e=>onChange({for_each:e.target.value||undefined})}
            placeholder="${steps.query.output.items}（留空=只执行一次）"/>
          <p className="text-[10px] text-muted-foreground mt-1">对表达式求值出的列表逐项执行子流程，子流程内用 {'${item}'} / {'${item_index}'} 引用当前项；仅支持全自动子流程。</p>
        </div>
      </>}
      {step.type === 'start' || step.type === 'end' ? <div className="text-xs text-muted-foreground bg-muted/30 rounded p-2">无需额外配置</div>
        : (step.type === 'service' || step.type === 'notify' || step.type === 'gateway' || step.type === 'dispatch') && <VariablesHint steps={steps} mainFields={mainFields} />}

      {step.type !== 'start' && <div className="pt-2 border-t space-y-1">
        <p className="text-[10px] font-medium text-muted-foreground">入口连线（上游）</p>
        {(step.depends_on||[]).length===0?<p className="text-[10px] text-muted-foreground">无，在画布上把上游节点右侧句柄拖到本节点左侧</p>:
          (step.depends_on||[]).map(d=>{
            const v = edgeVisual(d.condition)
            return <div key={d.nodeId} className="flex items-center gap-1 text-[10px]">
              <button type="button" onClick={()=>onPickEdge(d.nodeId, step.id)} className="hover:underline">
                <Tag color="default" className="text-[10px]">{steps.find(s=>s.id===d.nodeId)?.name||d.nodeId}</Tag>
              </button>
              <span style={{ color: v.color }}>{v.label || '成功'}</span>
              <button onClick={()=>removeDep(d.nodeId)} className="text-red-400 hover:text-red-600 ml-auto" title="删除该连线">×</button>
            </div>
          })
        }
      </div>}
    </CardContent>
  </Card>
      <Modal open={permOpen} onCancel={() => setPermOpen(false)} title={`表单字段权限 · ${step.name}`} footer={null}>
        <div className="space-y-3">
          <p className="text-xs text-muted-foreground">
            每个字段在本节点有四种状态：<b>可编辑</b>（可配合「必填」）、<b>只读</b>、<b>禁用</b>、<b>隐藏</b>（不渲染给处理人）。
            未调整的字段默认可编辑。
          </p>
          <div className="flex items-center gap-2">
            <Button size="middle" className="h-7 text-[11px]" onClick={() => bulk('editable')}>全部可编辑</Button>
            <Button size="middle" className="h-7 text-[11px]" onClick={() => bulk('readonly')}>全部只读</Button>
            <Button size="middle" className="h-7 text-[11px]" onClick={() => bulk('disabled')}>全部禁用</Button>
            <Button size="middle" className="h-7 text-[11px]" onClick={() => bulk('hidden')}>全部隐藏</Button>
          </div>
          <div className="border rounded-md max-h-[50vh] overflow-y-auto">
            <table className="w-full text-xs">
              <thead className="bg-muted/50 sticky top-0">
                <tr>
                  <th className="text-left px-3 py-2 font-medium">字段</th>
                  <th className="text-left px-3 py-2 font-medium w-24">类型</th>
                  <th className="text-center px-3 py-2 font-medium w-28">状态</th>
                  <th className="text-center px-3 py-2 font-medium w-16">必填</th>
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
                        <select className="h-7 w-full rounded border border-input bg-background px-1 text-[11px]"
                          value={p.mode} onChange={e => setPerm(f.key, { mode: e.target.value as StepFieldPerm['mode'] })}
                          aria-label={`${f.label} 节点状态`}>
                          <option value="editable">可编辑</option>
                          <option value="readonly">只读</option>
                          <option value="disabled">禁用</option>
                          <option value="hidden">隐藏</option>
                        </select>
                      </td>
                      <td className="px-3 py-1.5 text-center">
                        <input type="checkbox" className="h-3.5 w-3.5 accent-primary disabled:opacity-30"
                          checked={p.mode === 'editable' && !!p.required}
                          disabled={p.mode !== 'editable'}
                          onChange={e => setPerm(f.key, { required: e.target.checked })}
                          aria-label={`${f.label} 本节点必填`} />
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          <div className="flex justify-end">
            <Button onClick={() => setPermOpen(false)}>完成</Button>
          </div>
        </div>
    </Modal>
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
        <Tag color="default" className="text-[10px] truncate max-w-[80px]">{source.name}</Tag>
        <ChevronRight className="h-3 w-3 text-muted-foreground"/>
        <Tag color="default" className="text-[10px] truncate max-w-[80px]">{target.name}</Tag>
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
          <div className="flex items-center justify-between gap-2">
            <label className="text-xs font-medium">条件表达式</label>
            <VarPillPicker steps={steps} mainFields={mainForm?.fields || []} currentStepId={target.id}
              onInsert={t => onChangeCond(cond + '${' + t + '}')}/>
          </div>
          <Input value={cond} onChange={e => onChangeCond(e.target.value)} className="h-8 text-xs font-mono" placeholder='risk_level == "high"'/>
          {fieldKeys.length > 0 && <p className="text-[10px] text-muted-foreground mt-1">
            可用字段：{fieldKeys.slice(0, 8).join('、')}{fieldKeys.length > 8 ? ' …' : ''}
          </p>}
          {!cond.trim() && <p className="text-[10px] text-amber-600 dark:text-amber-400 mt-1">表达式为空，运行期该分支不会被选中。</p>}
        </div>}
      </>}

      <Button danger size="middle" className="w-full" onClick={onDelete}>
        <Trash2 className="h-3.5 w-3.5 mr-1"/>删除连线
      </Button>
    </CardContent>
  </Card>
}

// ---- BPMN canvas (presentational, steps 提升到页面层) ----
function BPMNCanvas({ steps, scripts, channels, mainForm, users, groups, selId, setSelId, selEdge, setSelEdge, update, add, remove, onConnect, removeEdge, setEdgeCond, snap, onToggleSnap, onAlign, onAutoLayout, fitSignal }: {
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
  snap: boolean; onToggleSnap: () => void; onAlign: () => void; onAutoLayout: () => void; fitSignal: number
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
        approvalTypeLabel: APPROVAL_SHORT[s.approval_type || 'countersign'] || '会签',
        approverField: s.approver_field,
        timeoutDuration: s.timeout_duration,
        timeoutActionLabel: s.timeout_action === 'auto_approve' ? '超时自动通过' : s.timeout_action === 'auto_reject' ? '超时自动驳回' : '超时催办',
        reminderInterval: s.reminder_interval,
        script_id: s.script_id, scriptName: scripts.find(sc=>sc.id===s.script_id)?.name,
        actionTypeLabel: s.action_type ? actionLabel(s.action_type) : undefined,
        actionScriptName: scripts.find(sc => sc.id === (s.inputs as Record<string, any> | undefined)?.script_id)?.name,
        duration: s.duration,
        outCount: outCount.get(s.id) || 0,
        inCount: (s.depends_on || []).length,
        notifyChannel: channels.find((c:any)=>c.id===s.notify_channel)?.name || s.notify_channel,
        notify_title: s.notify_title, notify_body: s.notify_body,
        subflowName: s.subflow_id,
        assignee: s.assignee, assigneeVar: s.assignee_var, dispatchMode: s.dispatch_mode,
        waitCondition: s.type === 'wait' ? s.condition : undefined,
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
        <button type="button" onClick={onAutoLayout} className="w-full flex items-center gap-2 px-2 py-1.5 text-xs rounded-md border border-dashed hover:border-solid hover:bg-accent text-blue-500 dark:text-blue-400"><Wand2 className="h-3 w-3" />自动分层布局</button>
        <p className="text-[10px] text-muted-foreground">· 自动按依赖关系排成自左向右的泳道：并行分支同层对齐，分支合并自动换层。</p>
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
        minZoom={0.08} maxZoom={1.5} deleteKeyCode={['Backspace','Delete']}
        snapToGrid={snap} snapGrid={[16, 16]}
        defaultEdgeOptions={{ type: 'flow' }}>
        <Controls position="bottom-right"/>
        <Background variant={BackgroundVariant.Dots} gap={20} size={1}/>
        <MiniMap position="bottom-left" nodeStrokeWidth={2} pannable zoomable style={{ height: 100 }}/>
        <AutoFitter signal={fitSignal}/>
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
          {sel.type!=='start'&&<Button danger size="middle" onClick={()=>remove(sel.id)}><Trash2 className="h-3.5 w-3.5 mr-1"/>删除节点</Button>}
        </div>
      ) : (
        <Card className="h-full min-h-[200px] flex items-center justify-center"><CardContent className="text-center text-muted-foreground text-xs space-y-1"><p>选中节点编辑属性</p><p>选中连线设置分支条件</p></CardContent></Card>
      )}
    </div>
  </div>
}

// ---- 高级设置：SLA/OLA/通知 ----
function AdvancedSettings({ proc, slaPolicies, olaPolicies, slaPolicyId, setSlaPolicyId, olaPolicyId, setOlaPolicyId, notifyConfig, setNotifyConfig, numberRule, setNumberRule, decisionBindings, setDecisionBindings }: {
  proc: Process
  slaPolicies: { id: string; name: string; risk_level: string }[]
  olaPolicies: { id: string; name: string }[]
  slaPolicyId: string
  setSlaPolicyId: (v: string) => void
  olaPolicyId: string
  setOlaPolicyId: (v: string) => void
  notifyConfig: { channels: string[]; on_create: boolean; on_complete: boolean; template: string }
  setNotifyConfig: (v: { channels: string[]; on_create: boolean; on_complete: boolean; template: string }) => void
  numberRule: TicketNumberRule
  setNumberRule: (v: TicketNumberRule) => void
  decisionBindings: DecisionBinding[]
  setDecisionBindings: (v: DecisionBinding[]) => void
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
            <button key={c} onClick={()=>toggleChannel(c)} className={`px-3 py-1.5 rounded-md text-xs border ${notifyConfig.channels.includes(c) ? 'bg-blue-500/15 border-blue-500/40 text-blue-600 dark:text-blue-400' : 'border-input text-muted-foreground'}`}>{chLabels[c]}</button>
          ))}
        </div>
        <div className="flex gap-4 text-xs">
          <label className="flex items-center gap-1.5 cursor-pointer"><input type="checkbox" className="h-3.5 w-3.5" checked={notifyConfig.on_create} onChange={e=>setNotifyConfig({...notifyConfig,on_create:e.target.checked})}/><span>创建时通知</span></label>
          <label className="flex items-center gap-1.5 cursor-pointer"><input type="checkbox" className="h-3.5 w-3.5" checked={notifyConfig.on_complete} onChange={e=>setNotifyConfig({...notifyConfig,on_complete:e.target.checked})}/><span>完成时通知</span></label>
        </div>
        <div><label className="text-xs font-medium">通知模板</label><Input.TextArea rows={3} value={notifyConfig.template} onChange={e=>setNotifyConfig({...notifyConfig,template:e.target.value})} placeholder="流程「{{process_name}}」新工单已创建"/></div>
      </CardContent></Card>

      <Card><CardContent className="p-4 space-y-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 text-sm font-semibold"><Hash className="h-4 w-4"/>工单编号规则</div>
          <label className="flex items-center gap-1.5 cursor-pointer text-xs"><input type="checkbox" className="h-3.5 w-3.5" checked={!!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, enabled: e.target.checked })}/><span>启用自定义</span></label>
        </div>
        <p className="text-[11px] text-muted-foreground">定义本流程创建的工单号组成结构：<code>前缀 + 日期 + 序号</code>。关闭则回退全局默认 <code>TK-YYYY-NNNN</code>。序号按「流程 + 年/月/全局」独立计序，保证唯一连续。</p>
        <div className="grid grid-cols-2 gap-3">
          <div><label className="text-xs font-medium">前缀（业务标识）</label><Input className="h-8 mt-1" value={numberRule.prefix || ''} disabled={!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, prefix: e.target.value.toUpperCase() })} placeholder="如 CHG / INC / REQ"/></div>
          <div><label className="text-xs font-medium">分隔符</label><Input className="h-8 mt-1" value={numberRule.separator || ''} disabled={!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, separator: e.target.value })} placeholder="默认 -"/></div>
          <div><label className="text-xs font-medium">日期片段</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={numberRule.date_format || ''} disabled={!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, date_format: e.target.value as TicketNumberRule['date_format'] })}>
            <option value="">无</option><option value="YYYY">年 (2026)</option><option value="YYYYMM">年月 (202611)</option><option value="YYYYMMDD">年月日 (20261102)</option>
          </select></div>
          <div><label className="text-xs font-medium">序号重置粒度</label><select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={numberRule.seq_scope || 'year'} disabled={!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, seq_scope: e.target.value as TicketNumberRule['seq_scope'] })}>
            <option value="year">按年</option><option value="month">按月</option><option value="global">全局</option>
          </select></div>
          <div><label className="text-xs font-medium">序号最小宽度</label><Input type="number" min={1} max={10} className="h-8 mt-1" value={numberRule.seq_width || 4} disabled={!numberRule.enabled} onChange={e=>setNumberRule({ ...numberRule, seq_width: Number(e.target.value) || 4 })} placeholder="4"/></div>
        </div>
        <div className="flex items-center gap-2 rounded-md bg-muted/50 px-3 py-2 text-xs">
          <span className="text-muted-foreground">预览：</span>
          <code className="font-mono text-foreground">{previewTicketNumber(proc.name, numberRule)}</code>
        </div>
      </CardContent></Card>

      <DecisionBindingsPanel bindings={decisionBindings} onChange={setDecisionBindings} />
    </div>
  )
}

// ---- 流程测试运行（dry-run，对齐 SN「Test」）----
// 纯模拟：不建实例、不执行动作、不改工单。逐节点展示「会做什么/停在哪里」。
const DRYRUN_STATUS_META: Record<string, { label: string; cls: string }> = {
  would_run:   { label: '将执行', cls: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' },
  would_pause: { label: '将停留', cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' },
  branch_end:  { label: '路径点', cls: 'bg-blue-500/15 text-blue-600 dark:text-blue-400' },
  skipped:     { label: '未经过', cls: 'bg-muted text-muted-foreground' },
}

function DryRunDialog({ open, onClose, processId, dirty }: {
  open: boolean
  onClose: () => void
  processId: string
  dirty: boolean
}) {
  const { dryRunProcess } = useProcessStore()
  const [varsText, setVarsText] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<DryRunResult | null>(null)

  useEffect(() => {
    if (open) { setResult(null); setVarsText('') }
  }, [open])

  const run = async () => {
    let vars: Record<string, any> | undefined
    if (varsText.trim()) {
      try { vars = JSON.parse(varsText) } catch {
        getMessage().error('示例变量不是合法 JSON'); return
      }
    }
    setRunning(true)
    const res = await dryRunProcess(processId, vars)
    setRunning(false)
    if (res) setResult(res)
  }

  return (
    <Modal open={open} onCancel={onClose} title="流程测试运行（dry-run）" footer={null} width={680}>
      <div className="space-y-3 text-sm">
        {dirty && (
          <p className="rounded-md bg-amber-500/10 border border-amber-500/40 px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
            当前有未保存的修改，测试按「最后一次保存」的定义运行；请先到高级设置保存后再测。
          </p>
        )}
        <div>
          <label className="text-xs font-medium">示例变量（可选，用于网关条件求值演示）</label>
          <Input.TextArea rows={3} className="mt-1 font-mono text-xs" value={varsText} onChange={e => setVarsText(e.target.value)}
            placeholder={'{\n  "risk_level": "high",\n  "custom_fields": { "env": "prod" }\n}'}/>
          <p className="text-[10px] text-muted-foreground mt-1">顶层为工单系统字段；custom_fields 子对象为表单字段。留空时网关按默认分支模拟并给出告警。</p>
        </div>
        <div className="flex justify-end">
          <Button type="primary" onClick={run} disabled={running}>
            {running ? <><Loader2 className="h-3.5 w-3.5 mr-1 animate-spin"/>模拟中...</> : <><PlayCircle className="h-3.5 w-3.5 mr-1"/>开始模拟</>}
          </Button>
        </div>
        {result && (
          <div className="space-y-2">
            <p className="text-xs font-medium">{result.summary}</p>
            {(result.warnings || []).length > 0 && (
              <div className="rounded-md border border-amber-500/40 bg-amber-500/5 px-3 py-2 space-y-0.5">
                {result.warnings!.map((wn, i) => <p key={i} className="text-[11px] text-amber-600 dark:text-amber-400">⚠ {wn}</p>)}
              </div>
            )}
            <div className="border rounded-md divide-y max-h-[46vh] overflow-y-auto">
              {result.steps.map((st, i) => {
                const m = DRYRUN_STATUS_META[st.status] || DRYRUN_STATUS_META.skipped
                const meta = META[st.type as ItStepType]
                const Icon = meta?.icon || PlayCircle
                return (
                  <div key={`${st.step_id}-${i}`} className="flex gap-2 px-3 py-2">
                    <Icon className="h-3.5 w-3.5 mt-0.5 shrink-0" style={{ color: meta?.color || '#94a3b8' }}/>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1.5">
                        <span className="text-xs font-medium truncate">{st.name}</span>
                        <span className="text-[10px] text-muted-foreground">{meta?.label || st.type}</span>
                        <span className={`ml-auto rounded px-1.5 py-0.5 text-[10px] font-medium shrink-0 ${m.cls}`}>{m.label}</span>
                      </div>
                      <p className="text-[11px] text-muted-foreground whitespace-pre-wrap">{st.detail}</p>
                      {st.branches && <p className="text-[10px] font-mono text-muted-foreground whitespace-pre-wrap">{st.branches}</p>}
                    </div>
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}

// ---- page ----
// 主表单解析统一走 lib/itsm-constants，工单页运行期渲染用的是同一份实现
const parseForm = parseFormSchema
// previewTicketNumber — 按编号规则生成示例预览（与后端 formatNumberDate/序号位宽逻辑对齐，仅展示用）
function previewTicketNumber(_procName: string, rule: TicketNumberRule): string {
  if (!rule.enabled) return 'TK-2026-0001'
  const sep = rule.separator || '-'
  const parts: string[] = []
  if (rule.prefix) parts.push(rule.prefix)
  const now = new Date()
  const y = String(now.getFullYear())
  const m = String(now.getMonth() + 1).padStart(2, '0')
  const d = String(now.getDate()).padStart(2, '0')
  if (rule.date_format === 'YYYY') parts.push(y)
  else if (rule.date_format === 'YYYYMM') parts.push(`${y}${m}`)
  else if (rule.date_format === 'YYYYMMDD') parts.push(`${y}${m}${d}`)
  parts.push('1'.padStart(rule.seq_width || 4, '0'))
  return parts.join(sep)
}
function parseNotify(raw: any): { channels: string[]; on_create: boolean; on_complete: boolean; template: string } {
  const d = (typeof raw === 'string' && raw) ? (() => { try { return JSON.parse(raw) } catch { return {} } })() : (raw || {})
  return { channels: d.channels || [], on_create: d.on_create ?? true, on_complete: d.on_complete ?? true, template: d.template || '' }
}

export default function ITSMProcessDetailPage() {
  const params = useParams(); const navigate = useNavigate()
  const id = String(params.id || '')
  const isNew = id === 'new'

  const { processes, getProcess, createProcess, updateProcess, fetchProcesses } = useProcessStore()
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
  // 流程测试运行（dry-run）弹窗
  const [dryRunOpen, setDryRunOpen] = useState(false)

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
  // 画布重适配信号：批量改坐标（自动分层/网格对齐）后让 ReactFlow 重新 fitView
  const [fitSignal, setFitSignal] = useState(0)
  const alignToGrid = useCallback(() => {
    pushHistory()
    setSteps(prev => prev.map(s => ({
      ...s,
      position_x: Math.round((s.position_x ?? 0) / 16) * 16,
      position_y: Math.round((s.position_y ?? 0) / 16) * 16,
    })))
    setDirty(true)
    setFitSignal(v => v + 1)
  }, [pushHistory])

  // 流程级主表单 / SLA / 通知
  const [processForm, setProcessForm] = useState<FormSchema>({ version: 1, scope: '', fields: [] })
  const [slaPolicyId, setSlaPolicyId] = useState('')
  const [olaPolicyId, setOlaPolicyId] = useState('')
  const [notifyConfig, setNotifyConfig] = useState<{ channels: string[]; on_create: boolean; on_complete: boolean; template: string }>({ channels: [], on_create: true, on_complete: true, template: '' })
  // 工单编号规则（流程级自定义；关闭则回退全局默认 TK-YYYY-NNNN）
  const [numberRule, setNumberRule] = useState<TicketNumberRule>({ enabled: false, prefix: '', date_format: 'YYYY', separator: '-', seq_scope: 'year', seq_width: 4 })
  // 流程级决策表绑定：运行期求值结果注入 decision.* 变量命名空间
  const [decisionBindings, setDecisionBindings] = useState<DecisionBinding[]>([])
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
    setNumberRule(proc.number_rule || { enabled: false, prefix: '', date_format: 'YYYY', separator: '-', seq_scope: 'year', seq_width: 4 })
    setDecisionBindings(proc.decision_bindings || [])
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

  /** 一键自动分层布局：整张 DAG 排成自左向右泳道（可撤销） */
  const handleAutoLayout = useCallback(() => {
    if (steps.length === 0) return
    pushHistory()
    setSteps(prev => layoutFlow(prev))
    setDirty(true)
    setFitSignal(v => v + 1)
  }, [steps.length, pushHistory])

  // 进入「流程编排」画布：若载入的流程从未保存过坐标（全部为 null），自动做一次分层布局，
  // 避免节点按数组顺序平铺成网格；布局后置 dirty（坐标变更需保存），用指纹避免切步重复触发。
  const autoLayoutedRef = useRef<string | null>(null)
  useEffect(() => {
    if (step !== 1 || isNew || steps.length === 0) return
    const hasPos = steps.some(s => s.position_x != null || s.position_y != null)
    if (hasPos) return
    const fp = steps.map(s => s.id).join(',')
    if (autoLayoutedRef.current === fp) return
    autoLayoutedRef.current = fp
    pushHistory()
    setSteps(prev => layoutFlow(prev))
    setDirty(true)
    setFitSignal(v => v + 1)
    // 仅评估 step 切换；steps 内容变化由指纹兜底
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step])

  const handleCreate = async () => {
    if (!newName.trim()) return
    setCreating(true)
    const pid = await createProcess({ name: newName, category: 'change', steps: [], enabled: true, version: 1 })
    setCreating(false)
    if (pid) navigate(`/itsm/process/${pid}`)
  }

  const handleSave = async () => {
    if (!proc) return
    const err = validateFlow(steps)
    if (err) { getMessage().error(err); return }
    // 编号格式唯一性预校验：与全局默认/其他启用规则的流程撞型会生成相同工单号（后端 409 兜底）
    if (numberRule.enabled) {
      const fmtKey = `${numberRule.prefix || ''}|${numberRule.separator || '-'}|${numberRule.date_format || ''}|${numberRule.seq_width || 4}`
      if (fmtKey === 'TK|-|YYYY|4') {
        getMessage().error('工单编号格式与全局默认 TK-YYYY-NNNN 相同，请修改前缀或日期/位宽组合'); return
      }
      const dup = processes.find(x => {
        if (x.id === proc.id || !x.number_rule?.enabled) return false
        const r = x.number_rule
        return `${r.prefix || ''}|${r.separator || '-'}|${r.date_format || ''}|${r.seq_width || 4}` === fmtKey
      })
      if (dup) {
        getMessage().error(`工单编号格式与流程「${dup.name}」重复（同格式会生成相同工单号），请调整前缀或日期/位宽组合`); return
      }
    }
    const payload = {
      ...proc,
      steps,
      form: processForm,
      sla_policy_id: slaPolicyId,
      notify_config: notifyConfig,
      number_rule: numberRule,
      decision_bindings: decisionBindings,
      version: (proc.version || 1) + 1,
    }
    const ok = await updateProcess(proc.id, payload)
    if (ok) { setProc({ ...proc, steps, form: processForm as any, sla_policy_id: slaPolicyId, notify_config: notifyConfig as any, number_rule: numberRule, decision_bindings: decisionBindings }); setDirty(false); }
  }

  const discard = () => {
    if (!proc) return
    setSteps((proc.steps && proc.steps.length) ? proc.steps : defaultSteps())
    setProcessForm(parseForm(proc.form))
    setSlaPolicyId(proc.sla_policy_id || '')
    setNotifyConfig(parseNotify(proc.notify_config))
    setNumberRule(proc.number_rule || { enabled: false, prefix: '', date_format: 'YYYY', separator: '-', seq_scope: 'year', seq_width: 4 })
    setDecisionBindings(proc.decision_bindings || [])
    setDirty(false)
  }

  if (loading) return <div className="p-6 flex justify-center"><Loader2 className="h-8 w-8 animate-spin"/></div>
  if (isNew) return <div className="p-6 space-y-4"><div className="flex items-center gap-3"><Button type="text" onClick={()=>navigate('/itsm/process')}><CL className="h-4 w-4"/></Button><h1 className="text-2xl font-bold">新建 ITSM 流程</h1></div><Card><CardContent className="p-6 space-y-3 max-w-xl"><Input placeholder="流程名称" value={newName} onChange={e=>setNewName(e.target.value)}/><Button type="primary" onClick={handleCreate} disabled={creating||!newName.trim()}>{creating?'创建中...':'创建并进入向导'}</Button></CardContent></Card></div>
  if (!proc) return <div className="p-6 text-center"><p className="text-muted-foreground">流程不存在</p><Button className="mt-3" onClick={()=>navigate('/itsm/process')}>返回列表</Button></div>

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-3 min-w-0">
        <Button type="text" onClick={()=>navigate('/itsm/process')}><CL className="h-4 w-4"/></Button>
        <div className="min-w-0"><h1 className="text-2xl font-bold truncate">{proc.name}</h1><p className="text-xs text-muted-foreground">{proc.description||'无描述'} · {steps.length} 节点 · v{proc.version||1}</p></div>
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
              <span className={`flex h-5 w-5 items-center justify-center rounded-full text-[10px] ${activeStep ? 'bg-blue-500 text-white' : done ? 'bg-blue-500/20 text-blue-600 dark:text-blue-400' : 'bg-muted text-muted-foreground'}`}>{i+1}</span>
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
          各流程节点不再单独建表单，只在「流程编排」里为本节点设置这些字段的状态（可编辑/必填/只读/禁用/隐藏）。
        </p>
        <FormDesigner schema={processForm} onChange={s => { setProcessForm(s); markFormDirty() }} systemFields={['risk_level','status','created_by','process_id']} />
        {/* 表单级联动规则（显隐/必填/只读/可编辑）与跨字段组合校验（阻断/提示） */}
        <FormRulesEditor schema={processForm} onChange={s => { setProcessForm(s); markFormDirty() }} />
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
          snap={snap} onToggleSnap={() => setSnap(v => !v)} onAlign={alignToGrid} onAutoLayout={handleAutoLayout} fitSignal={fitSignal} />
        <div className="flex justify-between items-center">
          <div className="flex gap-2">
            <Button onClick={() => setStep(0)}><CL className="h-4 w-4 mr-1"/>上一步</Button>
            <Button onClick={undo} disabled={history.length === 0} title="撤销 (Ctrl/Cmd+Z)"><Undo2 className="h-3.5 w-3.5 mr-1"/>撤销</Button>
            <Button onClick={redo} disabled={future.length === 0} title="重做 (Ctrl/Cmd+Shift+Z)"><Redo2 className="h-3.5 w-3.5 mr-1"/>重做</Button>
            <Button onClick={() => setDryRunOpen(true)} title="模拟运行整条流程（不建实例不执行动作）">
              <FlaskConical className="h-3.5 w-3.5 mr-1"/>测试运行
            </Button>
          </div>
          <Button type="primary" onClick={() => setStep(2)}>下一步：高级设置 <ChevronRight className="h-4 w-4 ml-1"/></Button>
        </div>
        <DryRunDialog open={dryRunOpen} onClose={() => setDryRunOpen(false)} processId={proc.id} dirty={dirty}/>
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
          numberRule={numberRule}
          setNumberRule={v => { setNumberRule(v); markFormDirty() }}
          decisionBindings={decisionBindings}
          setDecisionBindings={v => { setDecisionBindings(v); markFormDirty() }}
        />
        <div className="flex justify-between">
          <Button onClick={() => setStep(1)}><CL className="h-4 w-4 mr-1"/>上一步</Button>
          <Button type="primary" onClick={handleSave}><Save className="h-3 w-3 mr-1"/>保存流程</Button>
        </div>
      </div>
    )}

    {dirty && step !== 2 && (
      <div className="fixed bottom-6 right-6 z-50 flex gap-2 bg-background border rounded-full shadow-lg px-4 py-2">
        <span className="text-sm text-amber-600 self-center">有未保存的修改</span>
        <Button size="middle" onClick={() => setStep(2)}>去高级设置保存</Button>
      </div>
    )}
  </div>
}
