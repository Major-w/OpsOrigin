/** ITSM 共享常量 — 避免各页面重复定义 */

export const STATUS_LABELS: Record<string, string> = {
  draft: '草稿',
  pending: '待审批',
  pending_review: '待评审',
  approved: '已审批',
  rejected: '已驳回',
  processing: '执行中',
  executing: '执行中',
  completed: '已完成',
  closed: '已关闭',
  withdrawn: '已撤回',
  failed: '执行失败',
  rolled_back: '已回滚',
  open: '待处理',
  on_hold: '已挂起',
}

export const STATUS_COLORS: Record<string, string> = {
  draft: 'bg-gray-100 text-gray-700',
  pending: 'bg-yellow-100 text-yellow-700',
  pending_review: 'bg-yellow-100 text-yellow-700',
  approved: 'bg-blue-100 text-blue-700',
  rejected: 'bg-red-100 text-red-700',
  processing: 'bg-indigo-100 text-indigo-700',
  executing: 'bg-indigo-100 text-indigo-700',
  completed: 'bg-green-100 text-green-700',
  closed: 'bg-gray-100 text-gray-500',
  withdrawn: 'bg-orange-100 text-orange-700',
  failed: 'bg-red-100 text-red-700',
  rolled_back: 'bg-purple-100 text-purple-700',
  open: 'bg-amber-100 text-amber-700',
  on_hold: 'bg-cyan-100 text-cyan-700',
}

export const RISK_LABELS: Record<string, string> = {
  low: '低',
  medium: '中',
  high: '高',
  critical: '紧急',
  custom: '自定义',
}

export const RISK_COLORS: Record<string, string> = {
  low: 'bg-green-100 text-green-700',
  medium: 'bg-yellow-100 text-yellow-700',
  high: 'bg-orange-100 text-orange-700',
  critical: 'bg-red-100 text-red-700',
  custom: 'bg-purple-100 text-purple-700',
}

export const CHANNEL_LABELS: Record<string, string> = {
  manual: '人工创建',
  mail: '邮件入站',
  system: '系统',
  catalog: '服务目录',
  ai: 'AI 巡检',
}

/** 通知渠道标签 */
export const NOTIFY_CHANNEL_LABELS: Record<string, string> = {
  email: '邮件',
  wecom: '企业微信',
  dingtalk: '钉钉',
  feishu: '飞书',
}

export const PRIORITY_LABELS: Record<string, string> = {
  P0: 'P0 紧急',
  P1: 'P1 高',
  P2: 'P2 中',
  P3: 'P3 低',
}

/** 风险等级 → shadcn Badge variant */
export const RISK_BADGE_VARIANT: Record<string, 'destructive' | 'warning' | 'secondary' | 'success'> = {
  critical: 'destructive',
  high: 'warning',
  medium: 'secondary',
  low: 'success',
}

export const PRIORITY_COLORS: Record<string, string> = {
  P0: 'bg-red-100 text-red-700',
  P1: 'bg-orange-100 text-orange-700',
  P2: 'bg-yellow-100 text-yellow-700',
  P3: 'bg-green-100 text-green-700',
}

/** SLA 时限 (小时) — 按风险等级 */
export const SLA_BY_RISK: Record<string, { response: number; resolution: number }> = {
  low: { response: 24, resolution: 72 },
  medium: { response: 8, resolution: 48 },
  high: { response: 4, resolution: 24 },
  critical: { response: 1, resolution: 8 },
}

/** 格式化时间戳为本地时间字符串 */
export function formatDateTime(ts: string | number | Date): string {
  if (!ts) return '-'
  const d = new Date(typeof ts === 'number' ? ts : ts)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

/** 格式化时间戳为完整日期时间 */
export function formatFullDateTime(ts: string | number | Date): string {
  if (!ts) return '-'
  const d = new Date(typeof ts === 'number' ? ts : ts)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN')
}

/** 格式化持续时间 (秒 → 人类可读) */
export function formatDuration(seconds: number): string {
  if (seconds < 0) return '已超时'
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (h > 24) return `${Math.floor(h / 24)}天${h % 24}小时`
  if (h > 0) return `${h}小时${m}分`
  return `${m}分钟`
}

/** 获取状态 Badge 样式 */
export function getStatusBadgeClass(status: string): string {
  return STATUS_COLORS[status] || 'bg-gray-100 text-gray-600'
}

/** 获取风险等级 Badge 样式 */
export function getRiskBadgeClass(risk: string): string {
  return RISK_COLORS[risk] || 'bg-gray-100 text-gray-600'
}

/** 构建树形结构 (通用) */
export interface TreeNode<T> {
  item: T
  children: TreeNode<T>[]
}

export function buildTree<T extends Record<string, any>>(
  items: T[],
  idKey = 'id',
  parentKey = 'parent_id',
): TreeNode<T>[] {
  const map = new Map<string, TreeNode<T>>()
  const roots: TreeNode<T>[] = []

  for (const item of items) {
    map.set(item[idKey], { item, children: [] })
  }

  for (const item of items) {
    const node = map.get(item[idKey])!
    const parentId = item[parentKey]
    if (parentId && map.has(parentId)) {
      map.get(parentId)!.children.push(node)
    } else {
      roots.push(node)
    }
  }

  return roots
}

/** 扁平化树节点 (用于搜索过滤) */
export function flattenTree<T>(nodes: TreeNode<T>[]): T[] {
  const result: T[] = []
  for (const node of nodes) {
    result.push(node.item)
    result.push(...flattenTree(node.children))
  }
  return result
}

/** 获取树节点的所有后代 ID */
export function getDescendantIds<T extends Record<string, any>>(
  nodes: TreeNode<T>[],
  idKey = 'id',
): string[] {
  const ids: string[] = []
  function walk(ns: TreeNode<T>[]) {
    for (const n of ns) {
      ids.push(n.item[idKey])
      walk(n.children)
    }
  }
  walk(nodes)
  return ids
}
