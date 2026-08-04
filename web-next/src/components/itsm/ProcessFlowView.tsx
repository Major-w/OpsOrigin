'use client'

import { useMemo } from 'react'
import {
  ReactFlow, Background, BackgroundVariant, Handle, Position, MarkerType,
  type Node, type Edge, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { PlayCircle, StopCircle, UserCheck, GitBranch, Clock, Bell, Server, FolderTree, CheckCircle2 } from 'lucide-react'
import type { ProcessStep } from '@/stores/itsm'

// 只读流程视图: 渲染流程 DAG, 高亮当前步骤(current_step)与已完成步骤(step_results 记录过的)。
// 用于工单详情页观察流程流转状态(只读, 不可编辑)。

const META: Record<string, { label: string; icon: any; color: string }> = {
  start:    { label: '开始', icon: PlayCircle, color: '#10b981' },
  end:      { label: '结束', icon: StopCircle, color: '#ef4444' },
  user:     { label: '人工任务', icon: UserCheck, color: '#6366f1' },
  gateway:  { label: '网关', icon: GitBranch, color: '#8b5cf6' },
  timer:    { label: '定时器', icon: Clock, color: '#f97316' },
  notify:   { label: '通知', icon: Bell, color: '#ef4444' },
  service:  { label: '服务任务', icon: Server, color: '#14b8a6' },
  parallel: { label: '并行网关', icon: FolderTree, color: '#06b6d4' },
  subflow:  { label: '子流程', icon: FolderTree, color: '#8b5cf6' },
}

type FlowData = Record<string, any>
type FlowNodeProps = NodeProps<Node<FlowData>>

function FlowNode({ data }: FlowNodeProps) {
  const m = META[data.type] || { label: data.type, icon: Server, color: '#888888' }
  const Icon = m.icon
  const isCurrent = !!data.current
  const isDone = !!data.done
  const stateColor = isCurrent ? '#185FA5' : isDone ? '#3B6D11' : m.color
  return (
    <div className="flex flex-col items-center">
      {data.type !== 'start' && <Handle type="target" position={Position.Top} style={{ background: stateColor, width: 8, height: 8 }} />}
      <div
        className="relative flex items-center gap-1.5 min-w-[96px] max-w-[150px] h-[40px] px-2.5 border-2 rounded-lg bg-white dark:bg-gray-800 shadow-sm"
        style={{ borderColor: stateColor, outline: isCurrent ? '2px solid #185FA5' : undefined, outlineOffset: 3 }}
      >
        <Icon className="h-3.5 w-3.5 shrink-0" style={{ color: stateColor }} />
        <span className="text-[11px] font-medium leading-tight truncate">{data.label || data.type}</span>
        {isDone && <CheckCircle2 className="h-3.5 w-3.5 shrink-0" style={{ color: '#3B6D11' }} />}
      </div>
      {(data.type === 'gateway' || data.condText) && (
        <span className="text-[10px] text-muted-foreground mt-0.5 max-w-[140px] truncate">{data.condText || ''}</span>
      )}
      {data.type !== 'end' && data.type !== 'gateway' && <Handle type="source" position={Position.Bottom} style={{ background: stateColor, width: 8, height: 8 }} />}
    </div>
  )
}

interface Props {
  steps?: ProcessStep[]
  currentStep?: string
  stepResults?: Record<string, any>
  processName?: string
}

export default function ProcessFlowView({ steps, currentStep, stepResults, processName }: Props) {
  const { nodes, edges } = useMemo(() => {
    const list = steps || []
    const ns: Node[] = list.map((s, i) => {
      const done = !!(stepResults && stepResults[s.id])
      return {
        id: s.id,
        type: 'flow',
        position: {
          x: s.position_x ?? 60 + (i % 3) * 220,
          y: s.position_y ?? 50 + Math.floor(i / 3) * 110,
        },
        data: {
          label: s.name || META[s.type]?.label || s.type,
          type: s.type,
          current: s.id === currentStep && currentStep !== '',
          done,
          condText: s.type === 'gateway' ? s.condition : '',
        },
      }
    })
    const es: Edge[] = []
    const idSet = new Set(list.map(s => s.id))
    for (const s of list) {
      if (!s.depends_on) continue
      for (const d of s.depends_on) {
        if (!idSet.has(d.nodeId)) continue
        const isFail = d.condition === 'failure'
        const isExpr = !['success', 'failure', 'always'].includes(d.condition)
        es.push({
          id: `${d.nodeId}->${s.id}-${d.condition}`,
          source: d.nodeId,
          target: s.id,
          type: 'smoothstep',
          animated: !!isExpr,
          style: {
            stroke: isFail ? '#ef4444' : isExpr ? '#8b5cf6' : '#10b981',
            strokeWidth: 2,
            strokeDasharray: isFail || d.condition === 'always' ? '5,3' : undefined,
          },
          markerEnd: { type: MarkerType.ArrowClosed, color: isFail ? '#ef4444' : isExpr ? '#8b5cf6' : '#10b981' },
          label: isExpr ? d.condition : isFail ? '失败' : d.condition === 'always' ? '' : '成功',
          labelStyle: { fill: isExpr ? '#8b5cf6' : isFail ? '#ef4444' : '#10b981', fontSize: 9 },
          labelBgPadding: [3, 1] as [number, number],
          labelBgStyle: { fill: isExpr ? '#f5f3ff' : isFail ? '#fef2f2' : '#f0fdf4', fillOpacity: 0.9 },
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
      <div className="border rounded-md overflow-hidden bg-muted/20" style={{ height: 360 }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={{ flow: FlowNode }}
          fitView
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
        <span className="flex items-center gap-1"><span className="inline-block w-2.5 h-2.5 rounded-full" style={{ background: '#185FA5' }} />当前步骤</span>
        <span className="flex items-center gap-1"><CheckCircle2 className="h-3 w-3" style={{ color: '#3B6D11' }} />已完成</span>
        <span className="flex items-center gap-1"><span className="inline-block w-2.5 h-2.5 rounded-full" style={{ background: '#8b5cf6' }} />条件分支(按表单字段)</span>
      </div>
    </div>
  )
}
