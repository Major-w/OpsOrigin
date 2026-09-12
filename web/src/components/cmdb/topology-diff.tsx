
/**
 * 拓扑变更对比（时间维度）—— 选两个基线快照（或拿某快照与当前实时比），
 * 把新增 / 消失 / 变更的 CI 与关系画成一张三色图。
 *
 * 数据来自现成的 POST /cmdb/snapshots/compare（漂移报告），此前只有表格形态，
 * 没有"拓扑长什么样变了"的视图；这里补上时间维度这一档。
 */

import { useEffect, useMemo, useState } from 'react'
import {
  ReactFlow, ReactFlowProvider, Background, BackgroundVariant, Controls, Handle, Position, MarkerType,
  useNodesState, type Node, type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Select } from 'antd'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'
import { GitCompareArrows } from 'lucide-react'

interface SnapshotRef { id: string; name: string; created_at: string; ci_count: number; rel_count: number }
interface DiffCI { id: string; name: string; type_id: string; status?: string }
interface DiffChanged { id: string; name: string; type_id: string; fields: { field: string; old: any; new: any }[] }
interface DiffRel { source_id: string; target_id: string; relation_type: string }
interface DiffReport {
  ci_added?: DiffCI[]
  ci_removed?: DiffCI[]
  ci_changed?: DiffChanged[]
  rel_added?: DiffRel[]
  rel_removed?: DiffRel[]
  base_name?: string
  target_name?: string
}

type DiffState = 'added' | 'removed' | 'changed'

interface DiffNodeData {
  name: string
  typeId: string
  state: DiffState
  fields?: { field: string; old: any; new: any }[]
  [key: string]: any
}

const STATE_STYLE: Record<DiffState, { border: string; bg: string; text: string; label: string }> = {
  added: { border: 'border-emerald-500', bg: 'bg-emerald-50 dark:bg-emerald-500/10', text: 'text-emerald-700 dark:text-emerald-300', label: '新增' },
  removed: { border: 'border-rose-500', bg: 'bg-rose-50 dark:bg-rose-500/10', text: 'text-rose-700 dark:text-rose-300', label: '消失' },
  changed: { border: 'border-amber-500', bg: 'bg-amber-50 dark:bg-amber-500/10', text: 'text-amber-700 dark:text-amber-300', label: '变更' },
}

function DiffNodeCard({ data }: NodeProps<Node<DiffNodeData>>) {
  const st = STATE_STYLE[data.state]
  return (
    <div className={`w-[168px] rounded-lg border-2 ${st.border} ${st.bg} px-2.5 py-2 shadow-sm`}>
      <Handle type="target" position={Position.Left} className="!h-2 !w-2" />
      <div className="flex items-center justify-between gap-1">
        <span className="truncate text-xs font-medium">{data.name || data.id}</span>
        <span className={`shrink-0 rounded px-1 text-[10px] ${st.text}`}>{st.label}</span>
      </div>
      <p className="mt-0.5 truncate text-[10px] text-muted-foreground">{data.typeId}</p>
      {data.fields && data.fields.length > 0 && (
        <p className="mt-1 truncate text-[10px] text-muted-foreground" title={data.fields.map(f => f.field).join(', ')}>
          改了 {data.fields.length} 个字段：{data.fields.slice(0, 2).map(f => f.field).join('/')}
          {data.fields.length > 2 ? '…' : ''}
        </p>
      )}
      <Handle type="source" position={Position.Right} className="!h-2 !w-2" />
    </div>
  )
}

const nodeTypes = { diff: DiffNodeCard }

export function TopologyDiff() {
  const [snapshots, setSnapshots] = useState<SnapshotRef[]>([])
  const [baseId, setBaseId] = useState('')
  const [targetId, setTargetId] = useState('') // 空 = 与当前实时对比
  const [report, setReport] = useState<DiffReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [rfNodes, setRfNodes, onNodesChange] = useNodesState<Node<DiffNodeData>>([])

  useEffect(() => {
    apiFetch<SnapshotRef[]>('/cmdb/snapshots')
      .then(list => setSnapshots(Array.isArray(list) ? list : []))
      .catch(() => setSnapshots([]))
  }, [])

  const runCompare = async () => {
    if (!baseId) { getMessage().warning('请选择基准快照'); return }
    setLoading(true)
    try {
      const r = await apiFetch<DiffReport>('/cmdb/snapshots/compare', {
        method: 'POST',
        body: JSON.stringify({ base_id: baseId, target_id: targetId || '' }),
      })
      setReport(r)
      const n = (r?.ci_added?.length ?? 0) + (r?.ci_removed?.length ?? 0) + (r?.ci_changed?.length ?? 0)
      if (n === 0) getMessage().success('两个状态之间没有差异')
    } catch (e: any) {
      getMessage().error('对比失败: ' + e.message)
      setReport(null)
    } finally {
      setLoading(false)
    }
  }

  // 三列布局：新增 / 变更 / 消失各占一列，同一列内顺序排列。
  // diff 场景节点量通常不大，且分列比力导向更能表达"变了哪一类"。
  const { nodes, edges } = useMemo(() => {
    const cols: Record<DiffState, DiffCI[]> = { added: report?.ci_added ?? [], removed: report?.ci_removed ?? [], changed: [] }
    for (const c of report?.ci_changed ?? []) {
      cols.changed.push({ id: c.id, name: c.name, type_id: c.type_id })
    }
    const fieldMap = new Map((report?.ci_changed ?? []).map(c => [c.id, c.fields]))
    const order: DiffState[] = ['added', 'changed', 'removed']
    const next: Node<DiffNodeData>[] = []
    const ids = new Set<string>()
    order.forEach((state, ci) => {
      cols[state].forEach((c, ri) => {
        ids.add(c.id)
        next.push({
          id: c.id,
          type: 'diff',
          position: { x: 40 + ci * 260, y: 30 + ri * 84 },
          data: { name: c.name, typeId: c.type_id, state, fields: fieldMap.get(c.id) },
        })
      })
    })
    const mk = (list: DiffRel[], kind: 'added' | 'removed') =>
      (list ?? [])
        .filter(r => ids.has(r.source_id) && ids.has(r.target_id))
        .map((r, i) => ({
          id: `${kind}-${i}-${r.source_id}-${r.target_id}`,
          source: r.source_id,
          target: r.target_id,
          label: r.relation_type,
          animated: kind === 'added',
          style: {
            stroke: kind === 'added' ? 'var(--color-success, #10b981)' : 'var(--color-danger, #ef4444)',
            strokeWidth: 1.6,
            strokeDasharray: kind === 'removed' ? '5 3' : undefined,
          },
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: kind === 'added' ? '#10b981' : '#ef4444' },
          labelStyle: { fontSize: 10 },
        }))
    return {
      nodes: next,
      edges: [...mk(report?.rel_added ?? [], 'added'), ...mk(report?.rel_removed ?? [], 'removed')],
    }
  }, [report])

  useEffect(() => { setRfNodes(nodes) }, [nodes, setRfNodes])

  const stat = [
    { label: '新增 CI', value: report?.ci_added?.length ?? 0, cls: 'text-emerald-600' },
    { label: '消失 CI', value: report?.ci_removed?.length ?? 0, cls: 'text-rose-600' },
    { label: '变更 CI', value: report?.ci_changed?.length ?? 0, cls: 'text-amber-600' },
    { label: '新增关系', value: report?.rel_added?.length ?? 0, cls: 'text-emerald-600' },
    { label: '消失关系', value: report?.rel_removed?.length ?? 0, cls: 'text-rose-600' },
  ]

  return (
    <Card className="mt-4">
      <CardHeader className="py-3">
        <CardTitle className="text-sm flex items-center gap-2">
          <GitCompareArrows className="h-4 w-4" />拓扑变更对比
          <span className="text-[11px] font-normal text-muted-foreground">
            选两个基线快照（目标留空＝与当前实时对比），看拓扑在这段时间里长了什么、少了什么
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <p className="mb-1 text-xs text-muted-foreground">基准快照</p>
            <Select
              className="w-64" showSearch optionFilterProp="label" placeholder="选择基准快照"
              value={baseId || undefined} onChange={setBaseId}
              options={snapshots.map(s => ({ value: s.id, label: `${s.name}（${s.ci_count} CI / ${s.rel_count} 关系）` }))}
            />
          </div>
          <div>
            <p className="mb-1 text-xs text-muted-foreground">目标快照（留空＝当前实时）</p>
            <Select
              className="w-64" showSearch optionFilterProp="label" allowClear placeholder="当前实时"
              value={targetId || undefined} onChange={v => setTargetId(v || '')}
              options={snapshots.filter(s => s.id !== baseId).map(s => ({ value: s.id, label: `${s.name}（${s.ci_count} CI / ${s.rel_count} 关系）` }))}
            />
          </div>
          <Button type="primary" loading={loading} onClick={runCompare}>对比</Button>
        </div>

        {snapshots.length === 0 && (
          <p className="py-6 text-center text-xs text-muted-foreground">
            还没有基线快照。可到「CMDB / 基线快照」先捕获一份，之后就能做拓扑变更回放。
          </p>
        )}

        {report && (
          <>
            <div className="flex flex-wrap gap-4 text-xs">
              {stat.map(s => (
                <span key={s.label}>
                  <span className="text-muted-foreground">{s.label} </span>
                  <span className={`font-medium ${s.cls}`}>{s.value}</span>
                </span>
              ))}
            </div>
            {nodes.length === 0 ? (
              <p className="py-10 text-center text-sm text-muted-foreground">两个状态之间没有差异</p>
            ) : (
              <div className="h-[440px] rounded-md border">
                <ReactFlowProvider>
                  <ReactFlow
                    nodes={rfNodes}
                    edges={edges}
                    nodeTypes={nodeTypes}
                    onNodesChange={onNodesChange}
                    fitView
                    fitViewOptions={{ padding: 0.15 }}
                    minZoom={0.2}
                    maxZoom={2}
                    proOptions={{ hideAttribution: true }}
                  >
                    <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
                    <Controls showInteractive={false} />
                  </ReactFlow>
                </ReactFlowProvider>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

export default TopologyDiff
