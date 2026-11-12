/** ITSM 共享常量 — 避免各页面重复定义 */

import type { FormSchema, FormFieldDef } from '@/components/itsm/DynamicForm'
// 组合校验 / 有效状态求值实现位于 DynamicForm（与后端 form 包同语义），这里转出口供各页面统一引用
export { validateComboRules, effectiveFieldStates } from '@/components/itsm/DynamicForm'

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

// ---- 状态显示名覆盖(P2-⑯ 状态管理自定义) ----
// 模块级可变覆盖: 配置页/工单页加载时拉取租户 status-flow 配置后应用,
// 后续所有用 STATUS_LABELS 的展示点自动生效; 未配置时保持内置名。

export function applyStatusLabelOverrides(overrides?: Record<string, string> | null) {
  if (!overrides) return
  for (const [k, v] of Object.entries(overrides)) {
    if (v) STATUS_LABELS[k] = v
  }
}

/** 拉取全局(或指定目录)状态流转配置并应用显示名覆盖; 失败静默回退内置名。 */
export async function initStatusLabelOverrides(catalogId = '') {
  try {
    const { apiFetch } = await import('@/lib/utils')
    const d = await apiFetch<{ status_labels?: Record<string, string> }>(
      `/itsm/status-flows${catalogId ? `?catalog_id=${encodeURIComponent(catalogId)}` : ''}`)
    applyStatusLabelOverrides(d?.status_labels)
  } catch { /* 无配置/接口不可用: 保持内置名 */ }
}

export const STATUS_COLORS: Record<string, string> = {
  draft: 'bg-gray-100 text-gray-700',
  pending: 'bg-yellow-100 text-yellow-700',
  pending_review: 'bg-yellow-100 text-yellow-700',
  approved: 'bg-blue-100 text-blue-700',
  rejected: 'bg-red-100 text-red-700',
  processing: 'bg-indigo-100 text-indigo-700',
  executing: 'bg-indigo-100 text-indigo-700',
  completed: 'bg-blue-100 text-blue-700',
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
  low: 'bg-blue-100 text-blue-700',
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

/** 风险等级 → antd Tag color */
export const RISK_BADGE_VARIANT: Record<string, 'error' | 'warning' | 'default' | 'success'> = {
  critical: 'error',
  high: 'warning',
  medium: 'default',
  low: 'success',
}

export const PRIORITY_COLORS: Record<string, string> = {
  P0: 'bg-red-100 text-red-700',
  P1: 'bg-orange-100 text-orange-700',
  P2: 'bg-yellow-100 text-yellow-700',
  P3: 'bg-blue-100 text-blue-700',
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

/**
 * 解析流程主表单。后端 Process.form 可能是对象也可能是 JSON 字符串，
 * 统一收敛成 FormSchema，避免各页面各写一份解析。
 */
export function parseFormSchema(raw: any): FormSchema {
  if (!raw) return { version: 1, scope: '', fields: [] }
  if (typeof raw === 'string') {
    try {
      const o = JSON.parse(raw)
      return { version: o.version || 1, scope: o.scope || '', fields: o.fields || [] }
    } catch {
      return { version: 1, scope: '', fields: [] }
    }
  }
  return { version: raw.version || 1, scope: raw.scope || '', fields: raw.fields || [] }
}

/**
 * 节点字段权限：流程节点不再各自设计表单，只声明「流程主表单的某字段在本节点的状态」。
 * mode 四态（对标竞品节点字段五态中的四个渲染态）+ required 组合出第五态「必填」：
 * - editable：可编辑（required=true 时为本节点必填）
 * - readonly：只读展示
 * - disabled：禁用（渲染同只读，语义为"联动/权限禁用"）
 * - hidden：不渲染、不下发
 * 未声明的字段按可编辑处理。旧数据（visible/editable 布尔）读取时自动映射。
 */
export interface StepFieldPerm {
  key: string
  mode: 'hidden' | 'readonly' | 'disabled' | 'editable'
  /** 本节点必填（仅 editable 态有意义） */
  required?: boolean
  /** @deprecated 旧版布尔字段，读取兼容；写入不再产出 */
  visible?: boolean
  /** @deprecated 旧版布尔字段，读取兼容；写入不再产出 */
  editable?: boolean
}

/** 旧版 perm（visible/editable 布尔）→ 新版 mode 归一 */
export function normalizeStepFieldPerm(p: Partial<StepFieldPerm> & { key: string }): StepFieldPerm {
  if (p.mode) return { ...p, mode: p.mode } as StepFieldPerm
  const visible = p.visible !== false
  const editable = !!p.editable && visible
  return { key: p.key, mode: !visible ? 'hidden' : editable ? 'editable' : 'readonly', required: false }
}

/**
 * 把节点字段权限投影到主表单上：
 * - hidden 字段直接剔除（不下发到前端渲染，也就不会被填写）
 * - readonly/disabled 字段标记（DynamicForm 会禁用控件）
 * - editable 且 required 的字段标记必填（本节点动态必填）
 * - group 分组容器递归投影（子字段同样按 key 匹配权限）
 * 未配置权限（perms 为空）时视为「全部可见可编辑」，保证老流程零改造仍能跑。
 */
export function applyStepFieldPerms(schema: FormSchema | null | undefined, perms?: StepFieldPerm[]): FormSchema | null {
  if (!schema) return null
  if (!perms || perms.length === 0) return schema
  const byKey = new Map(perms.map(p => [normalizeStepFieldPerm(p).key, normalizeStepFieldPerm(p)]))
  return { ...schema, fields: applyPermsToFields(schema.fields || [], byKey) }
}

/** 递归投影：hidden 剔除 / readonly/disabled 标记 / editable+required 标必填（镜像后端 form.applyPermsToFields）。 */
function applyPermsToFields(fields: FormFieldDef[], byKey: Map<string, StepFieldPerm>): FormFieldDef[] {
  const out: FormFieldDef[] = []
  for (const f of fields) {
    let eff = f
    const p = byKey.get(f.key)
    if (p) {
      if (p.mode === 'hidden') continue       // 本节点不可见 → 剔除
      if (p.mode === 'editable') {
        if (p.required && !f.required) eff = { ...f, required: true }
      } else {
        eff = { ...f, readonly: true, disabled: p.mode === 'disabled', required: false } // 只读不可填 → 豁免必填（镜像后端）
      }
    }
    if (eff.type === 'group' && eff.children?.length) {
      eff = { ...eff, children: applyPermsToFields(eff.children, byKey) }
    }
    out.push(eff)
  }
  return out
}

/**
 * 节点字段权限上限（cap）：行为规则(behavior_rules)在节点投影之后计算，
 * 只可在权限边界内收紧（hide/optional/readonly），不得放宽节点权限。
 * 返回值传给 DynamicForm 的 permCaps，在 effectiveFieldStates 结果后兜底（与后端
 * form.EffectiveStatesForStep 镜像）：readonly/disabled → 强制只读并豁免必填；
 * editable+required → 强制必填；hidden 已由 applyStepFieldPerms 剔除，无需 cap。
 */
export function stepPermCaps(
  perms?: StepFieldPerm[],
): Record<string, { readonly?: boolean; required?: boolean }> {
  if (!perms || perms.length === 0) return {}
  const caps: Record<string, { readonly?: boolean; required?: boolean }> = {}
  for (const raw of perms) {
    const p = normalizeStepFieldPerm(raw)
    if (p.mode === 'hidden') continue // 投影层已剔除
    if (p.mode === 'readonly' || p.mode === 'disabled') caps[p.key] = { readonly: true }
    else if (p.mode === 'editable' && p.required) caps[p.key] = { required: true }
  }
  return caps
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
