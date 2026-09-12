// 图可视化专用调色板（拓扑关系色 / 健康度色 / 全景图层色）。
// 集中维护，避免拓扑、全景等页面散落硬编码十六进制；
// 关系色与健康度色为「分类语义色」（图例含义固定），故保持为 JS 常量而非主题变量，
// 仅缺省兜底色抽为可主题化的 GRAPH_DEFAULT_COLOR。

export const RELATION_COLORS: Record<string, string> = {
  depends_on: '#ef4444',
  connects_to: '#3b82f6',
  contains: '#3B82F6',
  backup_of: '#f59e0b',
  deployed_on: '#8b5cf6',
  calls: '#06b6d4',
  belongs_to: '#ec4899',
  part_of: '#6366f1',
  // 后端实际在用的部署/承载类关系（见 internal/cmdb/model/panorama.go LayerOfRelation）
  hosts: '#14b8a6',
  runs_on: '#14b8a6',
  runs_on_node: '#14b8a6',
  connects: '#3b82f6',
}

export const RELATION_LABELS: Record<string, string> = {
  depends_on: '依赖于',
  connects_to: '连接到',
  contains: '包含',
  backup_of: '备份',
  deployed_on: '部署于',
  calls: '调用',
  belongs_to: '隶属于',
  part_of: '组成部分',
  hosts: '承载',
  runs_on: '运行于',
  runs_on_node: '运行于',
  connects: '连接',
}

export function relationLabel(type: string): string {
  return RELATION_LABELS[type] || type
}

export const HEALTH_COLOR: Record<string, string> = {
  critical: '#ef4444',
  warning: '#f59e0b',
  healthy: '#3B82F6',
  unknown: '#94a3b8',
}

// 图元缺省色（节点/边未匹配到具体类型时兜底）。
// 与 globals.css 的 --muted-foreground 语义一致，便于主题统一。
export const GRAPH_DEFAULT_COLOR = '#94a3b8'

// 全景拓扑：图层 → 边样式（纵向部署骨架实线 / 纵向逻辑依赖虚线 / 横向调用链实线）。
// 注意：样式必须按「每条边自己的 layer」取，不能复用第一条边的 layer（历史 bug）。
export interface EdgeLayerStyle {
  color: string
  dashed: boolean
  width: number
  label: string
}

export const PANORAMA_LAYER_STYLE: Record<string, EdgeLayerStyle> = {
  'cmdb-structural': { color: '#94a3b8', dashed: false, width: 2, label: '部署骨架' },
  'cmdb-logical': { color: '#6366f1', dashed: true, width: 1.8, label: '逻辑依赖' },
  callchain: { color: '#06b6d4', dashed: false, width: 1.8, label: '调用链' },
}

export const PANORAMA_LAYER_DEFAULT: EdgeLayerStyle = {
  color: GRAPH_DEFAULT_COLOR,
  dashed: true,
  width: 1.6,
  label: '其它',
}

export function panoramaLayerStyle(layer: string): EdgeLayerStyle {
  return PANORAMA_LAYER_STYLE[layer] || PANORAMA_LAYER_DEFAULT
}

// CI 类型 → 节点色（力导向图等轻量视图在无图标体系时的分类着色）。
export const NODE_TYPE_COLORS: Record<string, string> = {
  service: '#60A5FA',
  host: '#6366f1',
  application: '#14b8a6',
  database: '#a855f7',
  middleware: '#8b5cf6',
  server: '#3B82F6',
  network: '#f97316',
  storage: '#6366f1',
}

export function nodeTypeColor(type: string): string {
  return NODE_TYPE_COLORS[type] || GRAPH_DEFAULT_COLOR
}

// hex(#rrggbb | #rgb) -> rgba，用于让连线/箭头/标签随强调程度统一淡化
// （SVG marker 不继承父元素 opacity，必须把透明度算进颜色本身）。
export function alpha(hex: string, a: number): string {
  const m = (hex || '').replace('#', '')
  if (m.length < 6 && m.length !== 3) return `rgba(148,163,184,${a})`
  const full = m.length === 3 ? m.split('').map(c => c + c).join('') : m
  const r = parseInt(full.slice(0, 2), 16)
  const g = parseInt(full.slice(2, 4), 16)
  const b = parseInt(full.slice(4, 6), 16)
  if ([r, g, b].some(v => Number.isNaN(v))) return `rgba(148,163,184,${a})`
  return `rgba(${r},${g},${b},${a})`
}
