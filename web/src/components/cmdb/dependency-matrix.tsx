
/**
 * 依赖矩阵（N×N 邻接矩阵）视图。
 *
 * 关系图在节点上百后就退化成"毛线球"，而矩阵在同样规模下仍可精确回答
 * "A 和 B 到底有没有关系、是什么关系"——这是大图场景最可靠的形态，与关系图互补。
 *
 * 规模控制：默认只渲染关联度最高的前 MAX_NODES 个节点（矩阵是 O(n²) DOM），
 * 超出部分明确告知而非静默丢弃；可切换"仅显示有关联的节点"进一步降噪。
 */

import { useMemo, useState } from 'react'
import { Button, Tooltip } from 'antd'
import { cn } from '@/lib/utils'
import { RELATION_COLORS, RELATION_LABELS } from '@/lib/graph-colors'
import { Grid3x3 } from 'lucide-react'

export interface MatrixNode {
  id: string
  name: string
  type?: string
}

export interface MatrixEdge {
  source_id: string
  target_id: string
  relation_type: string
}

interface Props {
  nodes: MatrixNode[]
  edges: MatrixEdge[]
  /** 点击事件：把矩阵格子的关系回传给画布（聚焦到该 pair） */
  onCellClick?: (source: string, target: string) => void
  className?: string
}

const MAX_NODES = 40

export function DependencyMatrix({ nodes, edges, onCellClick, className }: Props) {
  const [onlyLinked, setOnlyLinked] = useState(true)

  const degree = useMemo(() => {
    const m = new Map<string, number>()
    nodes.forEach(n => m.set(n.id, 0))
    for (const e of edges) {
      if (m.has(e.source_id)) m.set(e.source_id, (m.get(e.source_id) ?? 0) + 1)
      if (m.has(e.target_id)) m.set(e.target_id, (m.get(e.target_id) ?? 0) + 1)
    }
    return m
  }, [nodes, edges])

  // 关联矩阵：source -> target -> 关系类型集合（无向：双向都填）
  const cellMap = useMemo(() => {
    const m = new Map<string, Set<string>>()
    const put = (a: string, b: string, t: string) => {
      const k = `${a}|${b}`
      if (!m.has(k)) m.set(k, new Set())
      m.get(k)!.add(t)
    }
    for (const e of edges) {
      if (e.source_id === e.target_id) continue
      put(e.source_id, e.target_id, e.relation_type)
      put(e.target_id, e.source_id, e.relation_type)
    }
    return m
  }, [edges])

  const shown = useMemo(() => {
    let list = [...nodes]
    if (onlyLinked) list = list.filter(n => (degree.get(n.id) ?? 0) > 0)
    // 关联度高者优先：保证截断时留下的是主干，而不是随机的前 N 个
    list.sort((a, b) => (degree.get(b.id) ?? 0) - (degree.get(a.id) ?? 0) || a.name.localeCompare(b.name, 'zh'))
    return { list: list.slice(0, MAX_NODES), total: list.length }
  }, [nodes, onlyLinked, degree])

  const truncated = shown.total > MAX_NODES
  const ids = shown.list.map(n => n.id)

  if (nodes.length === 0) {
    return <p className="py-12 text-center text-sm text-muted-foreground">暂无数据</p>
  }

  return (
    <div className={cn('space-y-2', className)}>
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="inline-flex items-center gap-1 text-muted-foreground">
          <Grid3x3 className="h-3.5 w-3.5" />
          矩阵 {ids.length}×{ids.length}
        </span>
        <Button
          type={onlyLinked ? 'primary' : 'text'}
          size="small"
          className="h-6 text-xs"
          onClick={() => setOnlyLinked(v => !v)}
        >仅显示有关联</Button>
        {truncated && (
          <span className="text-amber-600">
            关联节点共 {shown.total}，已按关联度取前 {MAX_NODES}；换一个根节点可看局部
          </span>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {Object.entries(RELATION_COLORS).map(([t, c]) => (
            <span key={t} className="inline-flex items-center gap-1 text-muted-foreground">
              <span className="h-2.5 w-2.5 rounded-sm" style={{ background: c }} />
              {RELATION_LABELS[t] || t}
            </span>
          ))}
        </div>
      </div>

      <div className="overflow-auto rounded-md border" style={{ maxHeight: 620 }}>
        <table className="border-collapse text-[11px]" style={{ tableLayout: 'fixed' }}>
          <thead>
            <tr>
              <th
                className="sticky left-0 top-0 z-20 border bg-card px-2 py-1 text-left font-medium"
                style={{ width: 128, minWidth: 128 }}
              >
                <span className="text-muted-foreground">被依赖 → / 依赖 ↓</span>
              </th>
              {shown.list.map(n => (
                <th
                  key={n.id}
                  className="sticky top-0 z-10 border bg-card p-0 align-bottom"
                  style={{ width: 30, height: 108, minWidth: 30 }}
                >
                  <Tooltip title={n.name}>
                    <div
                      className="mx-auto truncate text-muted-foreground"
                      style={{ writingMode: 'vertical-rl', height: 96, maxWidth: 22 }}
                    >{n.name}</div>
                  </Tooltip>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.list.map(row => (
              <tr key={row.id}>
                <th
                  className="sticky left-0 z-10 border bg-card px-2 py-1 text-left font-normal"
                  style={{ width: 128, minWidth: 128 }}
                >
                  <Tooltip title={`${row.name}${row.type ? ` (${row.type})` : ''}`}>
                    <div className="truncate">{row.name}</div>
                    {row.type && <div className="truncate text-[10px] text-muted-foreground">{row.type}</div>}
                  </Tooltip>
                </th>
                {ids.map(colId => {
                  const types = row.id === colId ? undefined : cellMap.get(`${row.id}|${colId}`)
                  if (!types) {
                    return <td key={colId} className="border bg-muted/20" style={{ height: 30 }} />
                  }
                  const list = [...types]
                  const color = RELATION_COLORS[list[0]] || '#94a3b8'
                  const label = list.map(t => RELATION_LABELS[t] || t).join(' / ')
                  return (
                    <td
                      key={colId}
                      className="border p-0 text-center align-middle"
                      style={{ height: 30 }}
                    >
                      <button
                        type="button"
                        className="mx-auto block h-4 w-4 rounded-sm transition-transform hover:scale-125"
                        style={{ background: color }}
                        title={`${row.name} — ${shown.list.find(n => n.id === colId)?.name || colId.slice(0, 8)}：${label}`}
                        onClick={() => onCellClick?.(row.id, colId)}
                      />
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {ids.length === 0 && (
        <p className="py-8 text-center text-sm text-muted-foreground">当前范围内没有带关系的节点</p>
      )}
      {ids.length > 0 && (
        <p className="text-[11px] text-muted-foreground">
          点击格子可跳回关系图谱并聚焦该行节点（其邻域自动高亮）；矩阵适合回答"谁和谁有关系"，看整体形状与层级请回到关系图谱。
        </p>
      )}
    </div>
  )
}

export default DependencyMatrix
