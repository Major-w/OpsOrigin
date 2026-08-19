// 图可视化专用调色板（拓扑关系色 / 健康度色）。
// 集中维护，避免拓扑、全景等页面散落硬编码十六进制；
// 关系色与健康度色为「分类语义色」（图例含义固定），故保持为 JS 常量而非主题变量，
// 仅缺省兜底色抽为可主题化的 GRAPH_DEFAULT_COLOR。

export const RELATION_COLORS: Record<string, string> = {
  depends_on: '#ef4444',
  connects_to: '#3b82f6',
  contains: '#10b981',
  backup_of: '#f59e0b',
  deployed_on: '#8b5cf6',
  calls: '#06b6d4',
  belongs_to: '#ec4899',
  part_of: '#6366f1',
}

export const HEALTH_COLOR: Record<string, string> = {
  critical: '#ef4444',
  warning: '#f59e0b',
  healthy: '#22c55e',
  unknown: '#94a3b8',
}

// 图元缺省色（节点/边未匹配到具体类型时兜底）。
// 与 globals.css 的 --muted-foreground 语义一致，便于主题统一。
export const GRAPH_DEFAULT_COLOR = '#94a3b8'
