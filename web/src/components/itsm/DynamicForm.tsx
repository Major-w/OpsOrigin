
// DynamicForm —— ITSM 可配置表单渲染器。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐：按字段定义渲染控件，
// 支持条件显隐(多规则 AND/OR，可引用系统字段)、动态选项来源(cmdb:* / iam:*)、
// 必填标星、只读禁用、分区(Section)分组。
// 该组件只渲染「业务自定义字段」，系统字段(title/risk/...)仍由页面负责。

import { useEffect, useMemo, useRef, useState } from 'react'
import { Upload, X, Plus, FileText, AlertCircle, Star } from 'lucide-react'
import { Tabs } from 'antd'
import { RichTextEditor } from '@/components/rich-text-editor'
import { apiFetch } from '@/lib/utils'

export type FormFieldType =
  | 'text'
  | 'textarea'
  | 'select'
  | 'multiselect'
  | 'number'
  | 'date'
  | 'datetime'
  | 'duration'
  | 'attachment'
  | 'richtext'
  | 'table'
  | 'bool'
  | 'user'
  | 'ci'
  | 'cascader'
  | 'password'
  | 'time'
  | 'timerange'
  | 'link'
  | 'expression'
  | 'group'
  | 'rating'
  | 'daterange'
  | 'note'

export interface FormFieldOption {
  value: string
  label: string
  /** 级联子项：cascader 逐级展开用；扁平控件忽略 */
  children?: FormFieldOption[]
  /** 选项价格（变量价格）：>0 时计入费用合计 */
  price?: number
}

export interface FormFieldValidation {
  regex?: string
  min?: number
  max?: number
  min_len?: number
  max_len?: number
}

// 条件运算符（与后端 form 包常量一一对应）
export type ConditionOp =
  | 'eq'
  | 'neq'
  | 'in'
  | 'not_in'
  | 'contains'
  | 'empty'
  | 'not_empty'
  | 'gt'
  | 'gte'
  | 'lt'
  | 'lte'
  | 'is_true'
  | 'is_false'

export interface FormCondition {
  field: string
  op: ConditionOp
  value?: string
}

export const CONDITION_OPS: { value: ConditionOp; label: string; needValue: boolean }[] = [
  { value: 'eq', label: '等于', needValue: true },
  { value: 'neq', label: '不等于', needValue: true },
  { value: 'in', label: '属于（逗号分隔）', needValue: true },
  { value: 'not_in', label: '不属于（逗号分隔）', needValue: true },
  { value: 'contains', label: '包含', needValue: true },
  { value: 'gt', label: '大于', needValue: true },
  { value: 'gte', label: '大于等于', needValue: true },
  { value: 'lt', label: '小于', needValue: true },
  { value: 'lte', label: '小于等于', needValue: true },
  { value: 'empty', label: '为空', needValue: false },
  { value: 'not_empty', label: '非空', needValue: false },
  { value: 'is_true', label: '为真', needValue: false },
  { value: 'is_false', label: '为假', needValue: false },
]

export interface FormFieldDef {
  key: string
  label: string
  type: FormFieldType
  options?: FormFieldOption[]
  required?: boolean
  default?: unknown
  placeholder?: string
  help?: string
  section?: string
  order?: number
  validation?: FormFieldValidation
  readonly?: boolean
  /** 禁用：控件不可交互（与 readonly 渲染一致，语义上表示"联动/节点权限禁用"） */
  disabled?: boolean
  /** 内置字段标记：工单一等列(title/description/risk_level)以锁定卡片呈现在表单设计器中，
   *  标识与类型不可改，但标签/必填/宽度/分区可配置。渲染时由页面通过 withBuiltinFields 并入。 */
  builtin?: boolean
  /** 旧版单条表达式，如 "risk_level==critical"，保留兼容 */
  visible_when?: string
  /** 多规则显隐，优先于 visible_when */
  visible_when_rules?: FormCondition[]
  /** 规则组合逻辑：and(默认) | or */
  visible_logic?: 'and' | 'or'
  /** 动态选项来源：cmdb:<typeName> / iam:user / iam:role / iam:group / cmdb:types（级联层级树） */
  source?: string
  /** 引用数据字典 ID：非空时字段定义从字典展开 */
  dict_ref?: string
  /** 配合 dict_ref 的字段级引用：非空时只展开字典内 key 匹配的单个字段（空=展开全部） */
  dict_key?: string
  /** 分组容器(group)的内嵌字段：值仍平铺存 custom_fields，与顶层字段同层 */
  children?: FormFieldDef[]
  /** 可编辑角色白名单：非空时仅这些角色的用户可编辑（空=所有人） */
  roles?: string[]
  /** 12 栅格中占用的列数(1~12)，缺省=12(整行)。由表单设计器画布拖拽调节 */
  col_span?: number
  /** 显式起始列(1~12)：设计器「不自动补全」模式下记录字段在栅格中的固定位置，
   *  拖走/删除字段后其余字段不回流占位。缺省时回落为按 col_span 自动排布。 */
  col_start?: number
  /** 附件：单文件最大体积(MB)，0/缺省=不限 */
  max_size?: number
  /** 附件：允许的扩展名/MIME，逗号分隔（如 ".png,.pdf,image/*"） */
  accept?: string
  /** 附件：最多文件数，0/缺省=不限 */
  max_count?: number
  /** 附件：是否允许多文件。undefined=未配置(不限)，false=单文件 */
  multiple?: boolean
  /** 所属页签 key：schema.tabs 非空时生效，空=归入第一个页签 */
  tab?: string
  /** 表格列定义（MRVS 结构化模式）：非空时值为按列 key 组织的对象数组；空=旧自由二维数组 */
  table_columns?: TableColumnDef[]
  /** 字段单价（变量价格，对标 SN Variable Pricing）：bool 勾选计价 / number 数量×单价；select 类走选项级 price */
  price?: number
}

/** 结构化表格列定义（与后端 form.TableColumn 对齐） */
export interface TableColumnDef {
  key: string
  label: string
  required?: boolean
}

/** 表单页签定义（与后端 form.FormTab 对齐）：长表单分步布局 */
export interface FormTabDef {
  key: string
  label: string
}

/** 栅格列数归一化：非法/缺省一律回落整行 */
export function normalizeColSpan(n?: number): number {
  const v = Math.round(Number(n) || 0)
  if (!v || v < 1) return 12
  return Math.min(12, v)
}

export interface FormSchema {
  version: number
  scope: string
  fields: FormFieldDef[]
  /** 页签定义：非空时按 field.tab 分页签展示（长表单分步），未归入页签的字段落入第一个页签 */
  tabs?: FormTabDef[]
  /** UI 联动规则（schema 级）：条件命中时对目标字段施加显隐/必填/只读/可编辑动作 */
  behavior_rules?: BehaviorRule[]
  /** 跨字段组合校验：条件命中时按 severity 阻断(block)或提示(warn) */
  combo_rules?: ComboRule[]
  /** 数据联动规则（"选 A 自动带出 B"）：条件命中时把固定值或其它字段值写入目标字段 */
  data_links?: DataLinkRule[]
  /** 决策表：条件列 × 输出列的规则矩阵，首个命中行生效 */
  decision_tables?: DecisionTable[]
}

/** 联动动作：show/hide/required/optional/readonly/editable（与后端 form 包语义一致） */
export type BehaviorAction = 'show' | 'hide' | 'required' | 'optional' | 'readonly' | 'editable'

export interface BehaviorRule {
  id?: string
  name?: string
  conditions: FormCondition[]
  /** and(默认) | or */
  logic?: 'and' | 'or'
  action: BehaviorAction
  targets: string[]
  /** Reverse if false：条件不满足时自动恢复目标字段初始态（对齐 SN UI Policy） */
  revert?: boolean
}

export interface ComboRule {
  id?: string
  name?: string
  conditions: FormCondition[]
  logic?: 'and' | 'or'
  /** 命中时的提示文案 */
  message: string
  /** block(默认，拒绝提交) | warn(仅提示) */
  severity?: 'block' | 'warn'
}

/** 赋值模式：fixed=固定值(默认) / copy_from=复制其它字段当前值 / ci_attr=从 CMDB CI 属性回填 /
 *  submitter_attr=从提单人属性回填（如 department=部门/门店）（与后端 form 包语义一致） */
export type DataSetMode = 'fixed' | 'copy_from' | 'ci_attr' | 'submitter_attr'

/** ci_attr 模式的来源声明：以 name_field 字段当前值作为 CI 名称，在 ci_type 类型下取 attr 属性 */
export interface DataSetCIAttr {
  ci_type: string
  name_field: string
  fallback?: string
}

/** submitter_attr 模式的来源声明：取当前提单人（登录用户）的 attr 属性（如 department） */
export interface DataSetSubmitter {
  attr: string
  fallback?: string
}

/** 单个赋值动作：条件命中后把值写入 target 字段 */
export interface DataSet {
  target: string
  /** fixed(默认) / copy_from / ci_attr / submitter_attr */
  mode?: DataSetMode
  /** fixed=写入的值；copy_from=来源字段 key；ci_attr=CI 属性名（如 owner）；submitter_attr=提单人属性名（如 department） */
  value?: string
  ci_attr?: DataSetCIAttr
  submitter?: DataSetSubmitter
}

/** 数据联动规则："选 A 自动带出 B"。规则按声明顺序执行，后者覆盖前者 */
export interface DataLinkRule {
  id?: string
  name?: string
  conditions: FormCondition[]
  logic?: 'and' | 'or'
  sets: DataSet[]
}

/** 决策表行：cells 空串=通配，"a,b"=任一命中；outputs 为该行命中后写入的值 */
export interface DecisionRow {
  id?: string
  cells?: Record<string, string>
  outputs?: Record<string, string>
  /** 缺省启用 */
  enabled?: boolean
}

/** 决策表资产引用：非空时本表内容由后端按已发布版本展开，保存态只保留引用标记 */
export interface DecisionTableAssetRef {
  asset_id: string
  /** 0 / 缺省 = 跟随资产最新已发布版本；>0 = 锁定该不可变版本快照 */
  version?: number
}

/** 决策表：条件列 × 输出列的规则矩阵，自上而下首个命中且启用的行生效 */
export interface DecisionTable {
  id?: string
  name?: string
  condition_keys: string[]
  output_keys: string[]
  rows: DecisionRow[]
  asset_ref?: DecisionTableAssetRef
}

export interface FieldState {
  visible: boolean
  required: boolean
  readonly: boolean
  disabled: boolean
}

/**
 * 有效字段状态（与后端 form.EffectiveStates 语义一致）：
 * 初始取字段自身规则（visible_when/required/readonly），再按 behavior_rules 声明顺序叠加命中动作。
 * 隐藏字段必填自动失效。
 */
export function effectiveFieldStates(schema: FormSchema | null | undefined, ctx: Record<string, unknown>): Record<string, FieldState> {
  const out: Record<string, FieldState> = {}
  if (!schema?.fields) return out
  for (const f of schema.fields) {
    out[f.key] = {
      visible: fieldVisible(f, ctx),
      required: !!f.required,
      readonly: !!f.readonly,
      disabled: !!f.disabled,
    }
  }
  // Revert（Reverse if false）反向动作映射：与后端 form.inverseAction 一致
  const REVERSE: Record<string, BehaviorAction> = {
    show: 'hide', hide: 'show', required: 'optional', optional: 'required', readonly: 'editable', editable: 'readonly',
  }
  for (const r of schema.behavior_rules || []) {
    if (!r.targets?.length || !r.action) continue
    let action: string = r.action
    if (!evalConditions(r.conditions, r.logic, ctx)) {
      if (!r.revert) continue
      // Reverse if false：施加反向动作（不清掉其他规则已产生的效果）
      action = REVERSE[action] || action
    }
    for (const t of r.targets) {
      const st = out[t]
      if (!st) continue
      switch (action) {
        case 'show': st.visible = true; break
        case 'hide': st.visible = false; st.required = false; break
        case 'required': if (st.visible) st.required = true; break
        case 'optional': st.required = false; break
        case 'readonly': st.readonly = true; break
        case 'editable': st.readonly = false; st.disabled = false; break
      }
    }
  }
  return out
}

/**
 * 跨字段组合校验（与后端 form.ValidateComboRules 语义一致）。
 * 返回 block 级命中文案（应阻断提交）；warn 级文案放 warnings 由调用方提示。
 */
export function validateComboRules(
  schema: FormSchema | null | undefined,
  ctx: Record<string, unknown>,
): { blocks: string[]; warnings: string[] } {
  const blocks: string[] = []
  const warnings: string[] = []
  for (const r of schema?.combo_rules || []) {
    if (!r.conditions?.length || !evalConditions(r.conditions, r.logic, ctx)) continue
    const msg = r.message?.trim() || '组合校验未通过'
    if ((r.severity || 'block') === 'warn') warnings.push(msg)
    else blocks.push(msg)
  }
  return { blocks, warnings }
}

/**
 * 决策表单元格匹配：空=通配；"a,b" 任一命中（逗号分隔，与 OpIn 同语义）。
 */
function decisionCellHits(cell: string | undefined, raw: unknown): boolean {
  const c = (cell || '').trim()
  if (!c) return true
  const set = c.split(',').map(s => s.trim()).filter(Boolean)
  const vals = Array.isArray(raw) ? raw.map(v => String(v)) : [String(raw)]
  return vals.some(v => set.includes(v))
}

/** 决策表输出 ${path} 插值（与后端 form.interpolate 语义一致）：按点路径下钻取值，
 *  变量缺失替换为空串，未闭合占位符原样保留。用于拼出动态组名等组合值。 */
function interpolateValue(s: string, values: Record<string, unknown>): string {
  if (!s.includes('${')) return s
  return s.replace(/\$\{([^}]+)\}/g, (_m, path: string) => {
    let cur: unknown = values
    for (const seg of path.split('.')) {
      if (!cur || typeof cur !== 'object' || Array.isArray(cur)) return ''
      cur = (cur as Record<string, unknown>)[seg]
      if (cur === undefined) return ''
    }
    return cur === null || cur === undefined ? '' : String(cur)
  })
}

/**
 * 应用数据联动与决策表推导（与后端 form.ApplyDataRules 语义一致）。
 * 求值顺序：先 data_links（声明顺序），再 decision_tables（声明顺序，各自取首个命中行）。
 * 返回推导后的值副本与触发的规则说明（可转 toast 提示用户"已自动带出"）。
 */
export function applyDataRules(
  schema: FormSchema | null | undefined,
  ctx: Record<string, unknown>,
): { values: Record<string, unknown>; fired: string[] } {
  const out = { ...ctx }
  const fired: string[] = []
  if (!schema) return { values: out, fired }
  const declared = new Set((schema.fields || []).map(f => f.key))

  for (const r of schema.data_links || []) {
    if (!r.sets?.length || !evalConditions(r.conditions, r.logic, out)) continue
    let applied = 0
    for (const s of r.sets) {
      const target = (s.target || '').trim()
      if (!target || !declared.has(target)) continue
      const mode = s.mode || 'fixed'
      if (mode === 'copy_from') {
        const src = (s.value || '').trim()
        if (!src || src === target) continue
        if (out[src] !== undefined && out[src] !== null) {
          out[target] = out[src]
          applied++
        }
      } else if (mode === 'fixed') {
        out[target] = s.value ?? ''
        applied++
      }
      // ci_attr / submitter_attr：需后端解析（CMDB/用户中心），前端镜像不本地推导，
      // 由实时回填通道（字段变更 fetch / 挂载 fetch）写值，提交时后端仍会统一落定。
    }
    if (applied === 0) continue
    fired.push(`联动:${r.name || r.id || '未命名规则'}`)
  }

  for (const dt of schema.decision_tables || []) {
    const rows = dt.rows || []
    for (let ri = 0; ri < rows.length; ri++) {
      const row = rows[ri]
      if (row.enabled === false) continue
      let hit = true
      for (const k of dt.condition_keys || []) {
        if (!decisionCellHits(row.cells?.[k], out[k])) { hit = false; break }
      }
      if (!hit) continue
      for (const k of dt.output_keys || []) {
        if (!declared.has(k)) continue
        if (row.outputs && k in row.outputs) out[k] = interpolateValue(String(row.outputs[k] ?? ''), out)
      }
      fired.push(`决策表:${dt.name || dt.id || '未命名表'}#第${ri + 1}行`)
      break
    }
  }
  return { values: out, fired }
}

/** 内置字段：工单的一等列(title/description/risk_level)，在表单设计器中以锁定卡片呈现。
 *  可配置标签/必填/宽度/分区，但标识与类型锁定。渲染时由页面用 withBuiltinFields 并入自定义字段，
 *  提交时由 splitBuiltinValues 拆回一等列。order 取负值确保始终排在所有自定义字段之前。 */
export const BUILTIN_FIELDS: FormFieldDef[] = [
  { key: 'title', label: '申请标题', type: 'text', required: true, builtin: true, col_span: 12, order: -3 },
  { key: 'description', label: '申请说明', type: 'textarea', builtin: true, col_span: 12, order: -2 },
  {
    key: 'risk_level',
    label: '紧急程度',
    type: 'select',
    builtin: true,
    col_span: 6,
    order: -1,
    options: [
      { value: 'low', label: '低风险' },
      { value: 'medium', label: '中风险' },
      { value: 'high', label: '高风险' },
      { value: 'critical', label: '紧急' },
    ],
  },
]

export const BUILTIN_KEYS: string[] = BUILTIN_FIELDS.map(f => f.key)

/**
 * 动态默认值 token 解析（与后端 form.ResolveDefaultTokens 同语义，对标 SN g_user/日期）：
 *   {{current_user}} 当前用户名 · {{current_date}} YYYY-MM-DD · {{current_datetime}} YYYY-MM-DD HH:mm
 * token 可整串或混排；current_user 缺失（未登录/无上下文）时置空串。
 * 用于表单渲染/预填前把 schema 默认值落成实际值。
 */
export function resolveDefaultValue(field: FormFieldDef, username?: string): unknown {
  const d = field.default
  if (typeof d !== 'string' || !d.includes('{{')) return d
  const now = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const date = `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
  return d
    .replaceAll('{{current_user}}', username || '')
    .replaceAll('{{current_date}}', date)
    .replaceAll('{{current_datetime}}', `${date} ${pad(now.getHours())}:${pad(now.getMinutes())}`)
}

/** 把内置字段并入 schema 用于渲染：内置字段排在最前；若 schema 已含同名 key 则以其配置为准(并补 builtin 标记)。 */
export function withBuiltinFields(schema: FormSchema | null | undefined): FormSchema {
  if (!schema) return { version: 1, scope: 'builtin', fields: BUILTIN_FIELDS.map(f => ({ ...f })) }
  const have = new Map((schema.fields || []).map(f => [f.key, f]))
  const out: FormFieldDef[] = []
  for (const bf of BUILTIN_FIELDS) {
    const ex = have.get(bf.key)
    out.push(ex ? { ...bf, ...ex, builtin: true } : { ...bf })
  }
  for (const f of schema.fields || []) if (!BUILTIN_KEYS.includes(f.key)) out.push(f)
  return { ...schema, fields: out }
}

/** 从渲染值(含内置字段)拆出内置字段与自定义字段，供提交时分别落一等列与 custom_fields。 */
export function splitBuiltinValues(values: Record<string, unknown>): {
  builtin: Record<string, unknown>
  custom: Record<string, unknown>
} {
  const builtin: Record<string, unknown> = {}
  const custom: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(values || {})) {
    if (BUILTIN_KEYS.includes(k)) builtin[k] = v
    else custom[k] = v
  }
  return { builtin, custom }
}

/** 把旧版 "key==value" / "key!=value" 表达式解析为规则列表（与后端 ParseVisibleWhen 对齐） */
export function parseVisibleWhen(expr?: string): FormCondition[] {
  const s = (expr || '').trim()
  if (!s) return []
  for (const [sep, op] of [['!=', 'neq'], ['==', 'eq']] as const) {
    const idx = s.indexOf(sep)
    if (idx > 0) {
      return [{ field: s.slice(0, idx).trim(), op, value: s.slice(idx + sep.length).trim() }]
    }
  }
  return []
}

function toNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null
  if (typeof v === 'string' && v.trim() !== '') {
    const n = Number(v)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function isEmptyValue(v: unknown): boolean {
  if (v === undefined || v === null) return true
  if (typeof v === 'string') return v.trim() === ''
  if (Array.isArray(v)) return v.length === 0
  return false
}

function truthy(v: unknown): boolean {
  if (typeof v === 'boolean') return v
  if (typeof v === 'string') return ['true', '1', 'yes'].includes(v.trim().toLowerCase())
  const n = toNumber(v)
  return n !== null && n !== 0
}

function matchAny(raw: unknown, pred: (s: string) => boolean): boolean {
  if (Array.isArray(raw)) return raw.some(item => pred(String(item)))
  return pred(String(raw))
}

function splitList(v?: string): string[] {
  return (v || '')
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
}

/** 单条规则求值（与后端 evalCondition 语义一致） */
export function evalCondition(c: FormCondition, context: Record<string, unknown>): boolean {
  const field = (c.field || '').trim()
  if (!field) return true
  const exists = Object.prototype.hasOwnProperty.call(context, field) && context[field] !== undefined
  const raw = context[field]
  const op = (c.op || 'eq') as ConditionOp
  const want = c.value ?? ''

  switch (op) {
    case 'empty':
      return !exists || isEmptyValue(raw)
    case 'not_empty':
      return exists && !isEmptyValue(raw)
    case 'is_true':
      return truthy(raw)
    case 'is_false':
      return exists && !truthy(raw)
  }

  if (!exists) return op === 'neq' || op === 'not_in'

  switch (op) {
    case 'eq':
      return matchAny(raw, s => s === want)
    case 'neq':
      return !matchAny(raw, s => s === want)
    case 'in': {
      const set = splitList(want)
      return matchAny(raw, s => set.includes(s))
    }
    case 'not_in': {
      const set = splitList(want)
      return !matchAny(raw, s => set.includes(s))
    }
    case 'contains':
      return matchAny(raw, s => s.includes(want))
    case 'gt':
    case 'gte':
    case 'lt':
    case 'lte': {
      const left = toNumber(raw)
      const right = toNumber(want)
      if (left === null || right === null) return false
      if (op === 'gt') return left > right
      if (op === 'gte') return left >= right
      if (op === 'lt') return left < right
      return left <= right
    }
    default:
      return true
  }
}

/** 组合求值：or 任一命中，and 全部命中 */
export function evalConditions(
  rules: FormCondition[],
  logic: 'and' | 'or' | undefined,
  context: Record<string, unknown>,
): boolean {
  if (!rules || rules.length === 0) return true
  const or = (logic || 'and').toLowerCase() === 'or'
  for (const c of rules) {
    const hit = evalCondition(c, context)
    if (or && hit) return true
    if (!or && !hit) return false
  }
  return !or
}

// fieldVisible 与后端 form.FieldVisible 语义一致：优先多规则，回退旧版单条表达式。
export function fieldVisible(field: FormFieldDef, context: Record<string, unknown>): boolean {
  if (field.visible_when_rules && field.visible_when_rules.length > 0) {
    return evalConditions(field.visible_when_rules, field.visible_logic, context)
  }
  const rules = parseVisibleWhen(field.visible_when)
  if (rules.length === 0) return true
  return evalConditions(rules, 'and', context)
}

// ---- 计算字段求值器（与后端 internal/itsm/form/expression.go 同语义）----
// 语法: {key} 字段引用 | 数字 | '字符串' | "字符串" | + - * / % | 括号 | 函数 abs/round/floor/ceil/min/max。
// + 对任一侧为字符串时做拼接（数字自动转展示串），否则数值相加。

function exprToNumber(v: unknown): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null
  if (typeof v === 'string' && v.trim() !== '') {
    const n = Number(v)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function exprFormatNumber(v: unknown): string {
  if (typeof v === 'string') return v
  const n = exprToNumber(v)
  if (n === null) return String(v ?? '')
  if (Number.isInteger(n) && Math.abs(n) < 1e15) return String(n)
  return String(parseFloat(n.toPrecision(12)))
}

export class ExpressionError extends Error {}

/** evalExpression 求值计算字段表达式；失败抛 ExpressionError（调用方回落展示原文）。 */
export function evalExpression(expr: string, values: Record<string, unknown>): unknown {
  const p = { src: expr, pos: 0, values }

  const skipWS = () => {
    while (p.pos < p.src.length && /\s/.test(p.src[p.pos])) p.pos++
  }
  const peek = () => p.src[p.pos] ?? ''

  const primary = (): unknown => {
    skipWS()
    const c = peek()
    if (c === '(') {
      p.pos++
      const v = parseExpr()
      skipWS()
      if (peek() !== ')') throw new ExpressionError('缺少右括号')
      p.pos++
      return v
    }
    if (c === '{') {
      const end = p.src.indexOf('}', p.pos)
      if (end < 0) throw new ExpressionError('字段引用缺少右花括号')
      const key = p.src.slice(p.pos + 1, end)
      p.pos = end + 1
      if (!key) throw new ExpressionError('字段引用为空')
      if (!(key in p.values)) throw new ExpressionError(`引用字段 "${key}" 不存在`)
      return p.values[key]
    }
    if (c === "'" || c === '"') {
      p.pos++
      let out = ''
      while (p.pos < p.src.length) {
        const ch = p.src[p.pos]
        if (ch === '\\' && p.pos + 1 < p.src.length) {
          out += p.src[p.pos + 1]
          p.pos += 2
          continue
        }
        if (ch === c) {
          p.pos++
          return out
        }
        out += ch
        p.pos++
      }
      throw new ExpressionError('字符串未闭合')
    }
    if (/[0-9.]/.test(c)) {
      const m = /^[0-9]*\.?[0-9]+/.exec(p.src.slice(p.pos))
      if (!m) throw new ExpressionError('非法数字')
      p.pos += m[0].length
      return Number(m[0])
    }
    if (/[a-zA-Z_]/.test(c)) {
      const m = /^[a-zA-Z_][a-zA-Z0-9_]*/.exec(p.src.slice(p.pos))!
      p.pos += m[0].length
      skipWS()
      if (peek() !== '(') throw new ExpressionError(`未知标识符 "${m[0]}"（字段引用请写 {key}）`)
      p.pos++
      const args: unknown[] = []
      if (peek() === ')') {
        p.pos++
      } else {
        for (;;) {
          args.push(parseExpr())
          skipWS()
          if (peek() === ',') {
            p.pos++
            continue
          }
          if (peek() === ')') {
            p.pos++
            break
          }
          throw new ExpressionError('函数参数缺少右括号')
        }
      }
      return applyFunc(m[0], args)
    }
    throw new ExpressionError(`非法字符 "${c}"`)
  }

  const unary = (): unknown => {
    skipWS()
    if (peek() === '-') {
      p.pos++
      const n = exprToNumber(unary())
      if (n === null) throw new ExpressionError('负号操作数需为数值')
      return -n
    }
    return primary()
  }

  const term = (): unknown => {
    let left = unary()
    for (;;) {
      skipWS()
      const op = peek()
      if (op !== '*' && op !== '/' && op !== '%') return left
      p.pos++
      const right = unary()
      const l = exprToNumber(left)
      const r = exprToNumber(right)
      if (l === null || r === null) throw new ExpressionError('乘除模操作数需为数值')
      if (op === '*') left = l * r
      else if (op === '/') {
        if (r === 0) throw new ExpressionError('除数为零')
        left = l / r
      } else {
        if (Math.trunc(r) === 0) throw new ExpressionError('取模除数为零')
        left = Math.trunc(l) % Math.trunc(r)
      }
    }
  }

  function parseExpr(): unknown {
    let left = term()
    for (;;) {
      skipWS()
      const op = peek()
      if (op !== '+' && op !== '-') return left
      p.pos++
      const right = term()
      if (op === '+') {
        if (typeof left === 'string' || typeof right === 'string') {
          left =
            (typeof left === 'string' ? left : exprFormatNumber(left)) +
            (typeof right === 'string' ? right : exprFormatNumber(right))
          continue
        }
      }
      const l = exprToNumber(left)
      const r = exprToNumber(right)
      if (l === null || r === null) throw new ExpressionError('加减操作数需为数值')
      left = op === '+' ? l + r : l - r
    }
  }

  const result = parseExpr()
  skipWS()
  if (p.pos < p.src.length) throw new ExpressionError('表达式存在多余字符')
  return result

  function applyFunc(name: string, args: unknown[]): unknown {
    const nums = args.map(a => {
      const n = exprToNumber(a)
      if (n === null) throw new ExpressionError(`函数 ${name} 参数需为数值`)
      return n
    })
    switch (name) {
      case 'abs':
        return Math.abs(nums[0])
      case 'round':
        if (nums.length === 1) return Math.round(nums[0])
        {
          const shift = Math.pow(10, nums[1])
          return Math.round(nums[0] * shift) / shift
        }
      case 'floor':
        return Math.floor(nums[0])
      case 'ceil':
        return Math.ceil(nums[0])
      case 'min':
        return Math.min(...nums)
      case 'max':
        return Math.max(...nums)
      default:
        throw new ExpressionError(`未知函数 "${name}"`)
    }
  }
}

/** 计算字段展示值：求值成功返回格式化结果，失败回落展示表达式原文（与旧版只读展示兼容）。 */
export function expressionDisplay(field: FormFieldDef, context: Record<string, unknown>): string {
  const expr = typeof field.default === 'string' ? field.default : ''
  if (!expr.trim()) return ''
  try {
    const v = evalExpression(expr, context)
    return typeof v === 'number' ? exprFormatNumber(v) : String(v ?? '')
  } catch {
    return expr
  }
}

/**
 * 动态选项加载器：由页面注入，通常打 GET /itsm/form-options?source=
 * parent 传字符串（含空串）时进入层级模式，返回该父节点下的子选项，供 cascader 逐级展开；
 * 不传 parent 即平铺模式。
 * params: 参数化级联 —— source 里的 ${key} 占位符用当前表单值替换（如 机柜?room=${room}）。
 */
export type LoadOptionsFn = (
  source: string,
  keyword?: string,
  parent?: string,
  params?: Record<string, string>,
) => Promise<FormFieldOption[]>

/** 提取 source 串中的 ${key} 占位符键列表（前端判断级联依赖与就绪态） */
function sourcePlaceholderKeys(source: string): string[] {
  const out: string[] = []
  const re = /\$\{([^}]+)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(source)) !== null) out.push(m[1].trim())
  return out
}

interface DynamicFormProps {
  schema?: FormSchema | null
  // values: 本表单采集到的自定义字段值（受控）
  values: Record<string, unknown>
  // context: 用于显隐判定的上下文（通常含系统字段，如 risk_level），可选
  context?: Record<string, unknown>
  // onChange(key, value): 单字段变更回调
  onChange: (key: string, value: unknown) => void
  readOnly?: boolean
  // loadOptions: 带 source 的字段用它拉取选项；不传则退化为纯文本/静态 options
  loadOptions?: LoadOptionsFn
  // ticketId: 附件字段上传目标工单。传入后 attachment 字段可上传；未传（如创建表单）显示提示。
  ticketId?: string
  // permCaps 节点字段权限上限（itsm-constants.stepPermCaps 产出）：
  // readonly→强制只读并豁免必填；required→强制必填。行为规则(behavior_rules)只可在权限边界内收紧、不可放宽。
  permCaps?: Record<string, { readonly?: boolean; required?: boolean }>
}

const fieldBaseCls =
  'flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function DynamicForm({ schema, values, context, onChange, readOnly, loadOptions, ticketId, permCaps }: DynamicFormProps) {
  // 桌面端(>=640px)才应用显式 col_start：窄屏统一单列，避免强制定位导致横向溢出
  const [desktop, setDesktop] = useState(true)
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 640px)')
    const sync = () => setDesktop(mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])

  // 异步回填（submitter_attr / ci_attr）落值后，必须基于「合并后的完整值」重跑一次
  // 数据联动/决策表推导：否则依赖这些回填字段的决策表条件列在前端会先命中兜底行，
  // 与提交时后端 ApplyDataRulesFull 的结果不一致（处理人显示错、实际派单正确）。
  const rerunDerivedAfterAsyncFill = (filled: Record<string, unknown>) => {
    const merged = { ...context, ...values, ...filled }
    const { values: derived } = applyDataRules(schema, merged)
    for (const [k, val] of Object.entries(derived)) {
      if (k in filled) continue
      if (merged[k] !== val) onChange(k, val)
    }
  }

  // submitter_attr 回填：挂载 / schema 变化时取当前提单人属性（如 department=部门/门店）
  // 回填目标字段（与后端提交校验的 ApplyDataRulesFull 同一通道；失败静默，提交时后端仍会落定）。
  // 仅在目标字段为空时回填，不覆盖已有值；只读（详情）视图不回填。
  useEffect(() => {
    if (!schema || readOnly) return
    const sets = (schema.data_links || [])
      .flatMap(r => r.sets || [])
      .filter(s => s.mode === 'submitter_attr' && (s.target || '').trim() && (s.submitter?.attr || '').trim())
    if (!sets.length) return
    for (const s of sets) {
      const target = (s.target || '').trim()
      const cur = values[target]
      if (cur !== undefined && cur !== null && cur !== '') continue
      apiFetch<{ found: boolean; value?: string }>(
        `/itsm/form/submitter-attr?attr=${encodeURIComponent(s.submitter!.attr.trim())}`,
      )
        .then(res => {
          if (res?.found && res.value) {
            onChange(target, res.value)
            rerunDerivedAfterAsyncFill({ [target]: res.value })
          } else if (!res?.found && s.submitter?.fallback) {
            onChange(target, s.submitter.fallback)
            rerunDerivedAfterAsyncFill({ [target]: s.submitter.fallback })
          }
        })
        .catch(() => { /* 回填失败静默：提交时后端仍会按同一联动落定 */ })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schema, readOnly])

  if (!schema || !schema.fields || schema.fields.length === 0) {
    return (
      <p className="text-xs text-muted-foreground italic">
        当前作用域（{schema?.scope || 'ticket:default'}）未配置自定义字段。可点击「表单配置」定义业务字段。
      </p>
    )
  }

  const ctx = { ...context, ...values }
  // 展平字段里引用了 ${key} 级联占位符的映射: key -> 依赖它的字段列表（用于清空过期选择）
  const cascadeDependents = (() => {
    const deps = new Map<string, FormFieldDef[]>()
    for (const f of schema?.fields || []) {
      if (f.type === 'group') continue
      for (const k of sourcePlaceholderKeys(f.source || '')) {
        if (!deps.has(k)) deps.set(k, [])
        deps.get(k)!.push(f)
      }
    }
    return deps
  })()
  // 字段变更时：先回传新值，再基于合并后的完整值应用数据联动/决策表推导，
  // 把派生覆盖的键逐个回传给父级（引用比较，仅真正被改写的键才触发）。
  const handleFieldChange = (key: string, v: unknown) => {
    onChange(key, v)
    // 级联清理: 上级字段值变化后, 引用它的下级字段原选择大概率失效, 清空让其重选
    if (cascadeDependents.has(key)) {
      for (const dep of cascadeDependents.get(key)!) {
        if (dep.key !== key && values[dep.key] !== undefined && values[dep.key] !== '' && values[dep.key] !== null) {
          onChange(dep.key, '')
        }
      }
    }
    const { values: derived } = applyDataRules(schema, { ...context, ...values, [key]: v })
    for (const [k, val] of Object.entries(derived)) {
      if (k === key) continue
      if (values[k] !== val) onChange(k, val)
    }
    // ci_attr 回填：数据联动声明了「以本字段值为 CI 名称取属性」时，异步查后端并回填
    // （与后端提交校验的 ApplyDataRulesWithCI 同一通道；失败静默，不阻断填写）。
    for (const r of schema?.data_links || []) {
      for (const s of r.sets || []) {
        if (s.mode !== 'ci_attr' || !s.ci_attr || s.ci_attr.name_field !== key) continue
        const ciName = String(v ?? '').trim()
        if (!ciName) continue
        const attr = (s.value || '').trim()
        const target = (s.target || '').trim()
        if (!attr || !target) continue
        const qs = new URLSearchParams({ type_name: s.ci_attr.ci_type || '', name: ciName, attr })
        apiFetch<{ found: boolean; value?: string }>(`/itsm/form/ci-attr?${qs.toString()}`)
          .then(res => {
            if (res?.found && res.value && res.value !== values[target]) {
              onChange(target, res.value)
              rerunDerivedAfterAsyncFill({ [target]: res.value })
            } else if (!res?.found && s.ci_attr?.fallback && values[target] !== s.ci_attr.fallback) {
              onChange(target, s.ci_attr.fallback)
              rerunDerivedAfterAsyncFill({ [target]: s.ci_attr.fallback })
            }
          })
          .catch(() => { /* 回填失败静默：提交时后端仍会按同一联动落定 */ })
      }
    }
  }
  const fields = [...schema.fields].sort((a, b) => (a.order || 0) - (b.order || 0))
  // 有效状态 = 字段自身规则 + behavior_rules 联动叠加（与后端校验口径一致），
  // 再按节点字段权限 cap 兜底（与后端 form.EffectiveStatesForStep 镜像）：联动不得放宽节点权限。
  const states = effectiveFieldStates(schema, ctx)
  if (permCaps) {
    for (const [k, cap] of Object.entries(permCaps)) {
      const st = states[k]
      if (!st) continue
      if (cap.readonly) { st.readonly = true; st.required = false }
      if (cap.required) st.required = true
    }
  }
  const visibleFields = fields.filter(f => states[f.key]?.visible !== false)

  if (visibleFields.length === 0) {
    return <p className="text-xs text-muted-foreground italic">当前条件下无可填写的自定义字段。</p>
  }

  // ---- 变量价格（对标 SN Variable Pricing）：纯前端汇总，提交值不受影响 ----
  // 计价口径：bool 勾选后计单价；number 数量×单价；select/multiselect 按选项级 price 累加。
  const pricingLines: { label: string; amount: number }[] = []
  const round2 = (n: number) => Math.round(n * 100) / 100
  for (const f of visibleFields) {
    if (f.type === 'bool' && (f.price || 0) > 0 && truthy(values[f.key])) {
      pricingLines.push({ label: f.label, amount: round2(f.price!) })
    } else if (f.type === 'number' && (f.price || 0) > 0) {
      const qty = toNumber(values[f.key])
      if (qty !== null && qty > 0) pricingLines.push({ label: `${f.label} × ${qty}`, amount: round2(qty * f.price!) })
    } else if (f.type === 'select' && typeof values[f.key] === 'string') {
      const opt = (f.options || []).find(o => o.value === values[f.key])
      if (opt && (opt.price || 0) > 0) pricingLines.push({ label: `${f.label}（${opt.label}）`, amount: round2(opt.price!) })
    } else if (f.type === 'multiselect' && Array.isArray(values[f.key])) {
      let sum = 0
      const picked: string[] = []
      for (const o of f.options || []) {
        if ((values[f.key] as unknown[]).includes(o.value) && (o.price || 0) > 0) {
          sum += o.price!
          picked.push(o.label)
        }
      }
      if (sum > 0) pricingLines.push({ label: `${f.label}（${picked.join('、')}）`, amount: round2(sum) })
    }
  }
  const pricingTotal = round2(pricingLines.reduce((s, l) => s + l.amount, 0))
  // 仅当 schema 里存在可计价配置时才渲染合计条（避免无价格表单常驻空条）
  const hasPricing = [...schema.fields].some(
    f => (f.price || 0) > 0 || (f.options || []).some(o => (o.price || 0) > 0),
  )
  const renderPricing = () =>
    hasPricing ? (
      <div className="mt-4 rounded-lg border border-dashed border-input bg-muted/30 px-4 py-3">
        <div className="flex items-center justify-between">
          <span className="text-sm font-medium">费用合计</span>
          <span className="text-lg font-semibold text-blue-600 dark:text-blue-400">¥{pricingTotal.toFixed(2)}</span>
        </div>
        {pricingLines.length > 0 && (
          <div className="mt-2 space-y-1">
            {pricingLines.map((l, i) => (
              <div key={i} className="flex items-center justify-between text-xs text-muted-foreground">
                <span>{l.label}</span>
                <span>¥{l.amount.toFixed(2)}</span>
              </div>
            ))}
          </div>
        )}
        {pricingLines.length === 0 && (
          <p className="mt-1 text-xs text-muted-foreground">按当前选择暂无计费项。</p>
        )}
      </div>
    ) : null

  // 把一段可见字段按 section 分组渲染成 12 栅格（页签模式下每个页签复用同一渲染路径）
  const renderSections = (list: FormFieldDef[]) => {
    const secMap = new Map<string, FormFieldDef[]>()
    for (const f of list) {
      const sec = f.section || ''
      if (!secMap.has(sec)) secMap.set(sec, [])
      secMap.get(sec)!.push(f)
    }
    return (
    <div className="space-y-4">
      {Array.from(secMap.entries()).map(([sec, fs]) => (
        <div key={sec || '__root'} className="space-y-3">
          {sec && <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{sec}</h4>}
          {/* 12 栅格：字段按 col_span 占列，缺省整行；窄屏统一降为单列避免挤压 */}
          <div className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-12">
            {fs.map(f => {
              const span = normalizeColSpan(f.col_span)
              const explicit = desktop && f.col_start ? { gridColumn: `${f.col_start} / span ${span}` } : undefined
              const st = states[f.key] || { visible: true, required: !!f.required, readonly: !!f.readonly, disabled: !!f.disabled }
              // 分组容器(group)：整行卡片, 内嵌字段递归渲染(值仍平铺 custom_fields);
              // 组内字段显隐按各自 visible_when 判定, 读写状态随容器。
              if (f.type === 'group') {
                const children = (f.children || []).filter(c => fieldVisible(c, ctx))
                return (
                  <div key={f.key} className="col-span-12 rounded-lg border border-dashed border-input bg-muted/20 p-3" style={explicit}>
                    <div className="mb-2 flex items-center gap-1.5">
                      <span className="text-sm font-medium">{f.label}</span>
                      {f.help && <span className="text-xs text-muted-foreground">{f.help}</span>}
                    </div>
                    {children.length === 0 ? (
                      <p className="text-xs text-muted-foreground italic">空分组（在表单设计器中为本分组添加子字段）</p>
                    ) : (
                      <div className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-12">
                        {children.map(c => {
                          const cspan = normalizeColSpan(c.col_span)
                          return (
                            <div key={c.key} className={COL_SPAN_CLS[cspan]}>
                              <label className="text-sm font-medium flex items-center gap-1">
                                {c.label}
                                {(c.required || st.required) && c.required && <span className="text-destructive">*</span>}
                              </label>
                              <div className="mt-1">
                                <FieldControl
                                  field={c}
                                  value={values[c.key]}
                                  disabled={readOnly || st.readonly || st.disabled || c.readonly || !!c.disabled}
                                  onChange={v => handleFieldChange(c.key, v)}
                                  loadOptions={loadOptions}
                                  ticketId={ticketId}
                                  evalContext={ctx}
                                  values={values}
                                />
                              </div>
                              {c.help && <p className="text-xs text-muted-foreground mt-1">{c.help}</p>}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              }
              // 说明区块(note)：纯展示整行卡片，不渲染 label/控件结构（FieldControl 对 note 返回 null）
              if (f.type === 'note') {
                return (
                  <div key={f.key} className="col-span-12 rounded-md border border-input bg-muted/30 px-3 py-2.5" style={explicit}>
                    <p className="text-sm font-medium">{f.label}</p>
                    {f.help && <p className="mt-1 text-xs leading-relaxed text-muted-foreground whitespace-pre-wrap">{f.help}</p>}
                  </div>
                )
              }
              return (
              <div key={f.key} className={COL_SPAN_CLS[span]} style={explicit}>
                <label className="text-sm font-medium flex items-center gap-1">
                  {f.label}
                  {st.required && <span className="text-destructive">*</span>}
                </label>
                <div className="mt-1">
                  <FieldControl
                    field={f}
                    value={values[f.key]}
                    disabled={readOnly || st.readonly || st.disabled}
                    onChange={v => handleFieldChange(f.key, v)}
                    loadOptions={loadOptions}
                    ticketId={ticketId}
                    evalContext={ctx}
                    values={values}
                  />
                </div>
                {f.help && <p className="text-xs text-muted-foreground mt-1">{f.help}</p>}
              </div>
              )
            })}
          </div>
        </div>
      ))}
    </div>
    )
  }

  // 页签分步布局：schema.tabs 非空（≥2）时按 field.tab 分页签渲染（长表单分步，
  // 与后端 form.FormTabs 对齐）；未归入页签或归入了不存在页签的字段落入第一个页签。
  const tabs = (schema.tabs || []).filter(t => (t.key || '').trim())
  if (tabs.length >= 2) {
    const firstKey = tabs[0].key
    const groups = new Map<string, FormFieldDef[]>()
    for (const f of visibleFields) {
      const k = f.tab && tabs.some(t => t.key === f.tab) ? f.tab : firstKey
      if (!groups.has(k)) groups.set(k, [])
      groups.get(k)!.push(f)
    }
    const items = tabs.map(t => {
      const fs = groups.get(t.key) || []
      return {
        key: t.key,
        label: t.label || t.key,
        children: fs.length === 0 ? <p className="text-xs text-muted-foreground italic">本页签暂无字段。</p> : renderSections(fs),
      }
    })
    return (
      <div>
        <Tabs defaultActiveKey={firstKey} items={items} size="small" />
        {renderPricing()}
      </div>
    )
  }

  return (
    <div>
      {renderSections(visibleFields)}
      {renderPricing()}
    </div>
  )
}

/**
 * 栅格列数 → Tailwind 类名静态映射。
 * 不能用模板字符串拼 `sm:col-span-${n}`：Tailwind 编译期扫描源码，动态类名会被丢弃。
 */
export const COL_SPAN_CLS: Record<number, string> = {
  1: 'sm:col-span-1',
  2: 'sm:col-span-2',
  3: 'sm:col-span-3',
  4: 'sm:col-span-4',
  5: 'sm:col-span-5',
  6: 'sm:col-span-6',
  7: 'sm:col-span-7',
  8: 'sm:col-span-8',
  9: 'sm:col-span-9',
  10: 'sm:col-span-10',
  11: 'sm:col-span-11',
  12: 'sm:col-span-12',
}

/** 默认 source 推断：user/ci 类型未显式配 source 时给个合理缺省 */
function effectiveSource(field: FormFieldDef): string {
  if (field.source) return field.source
  if (field.type === 'user') return 'iam:user'
  return ''
}

/** useFieldOptions —— 静态 options 优先；否则按 source 异步拉取（带 loading / error 态）。
 *  source 含 ${key} 占位符时为级联字段：从 values 取参数，前置未就绪则不请求并返回 pending 态。 */
function useFieldOptions(field: FormFieldDef, loadOptions?: LoadOptionsFn, values?: Record<string, unknown>) {
  const staticOpts = field.options || []
  const source = effectiveSource(field)
  // 级联字段自己按层级拉取（见 Cascader），这里不重复打平铺请求
  const needRemote = staticOpts.length === 0 && !!source && !!loadOptions && field.type !== 'cascader'

  // 参数化级联: 计算 source 占位符对应的表单当前值
  const placeholderKeys = useMemo(() => (needRemote ? sourcePlaceholderKeys(source) : []), [needRemote, source])
  const cascadeParams = useMemo(() => {
    if (placeholderKeys.length === 0 || !values) return null
    const p: Record<string, string> = {}
    for (const k of placeholderKeys) {
      const v = values[k]
      if (v === undefined || v === null || v === '') return null // 前置字段未选
      p[k] = typeof v === 'object' ? JSON.stringify(v) : String(v)
    }
    return p
  }, [placeholderKeys, values])
  const cascadeKey = cascadeParams ? JSON.stringify(cascadeParams) : ''

  const [remote, setRemote] = useState<FormFieldOption[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const reqRef = useRef(0)

  useEffect(() => {
    if (!needRemote) return
    if (placeholderKeys.length > 0 && !cascadeParams) {
      setRemote([]) // 级联前置未就绪: 清空候选, 由 UI 提示先选上级
      return
    }
    const seq = ++reqRef.current
    setLoading(true)
    setError('')
    loadOptions!(source, undefined, undefined, cascadeParams || undefined)
      .then(opts => {
        if (reqRef.current !== seq) return
        setRemote(opts || [])
      })
      .catch((e: unknown) => {
        if (reqRef.current !== seq) return
        setError(e instanceof Error ? e.message : '选项加载失败')
      })
      .finally(() => {
        if (reqRef.current === seq) setLoading(false)
      })
    // cascadeKey 汇总参数变化（引用稳定）, 避免每次 render 重复请求
  }, [needRemote, source, loadOptions, cascadeKey])

  return {
    options: staticOpts.length > 0 ? staticOpts : remote,
    loading,
    error,
    remote: needRemote,
    cascadePending: needRemote && placeholderKeys.length > 0 && !cascadeParams,
  }
}

function FieldControl({
  field,
  value,
  disabled,
  onChange,
  loadOptions,
  ticketId,
  evalContext,
  values,
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
  loadOptions?: LoadOptionsFn
  ticketId?: string
  /** 计算字段(expression)求值上下文：当前表单全部字段值+系统字段 */
  evalContext?: Record<string, unknown>
  /** 全表单当前值：参数化级联(${key} 占位符)取参数用 */
  values?: Record<string, unknown>
}) {
  const { options, loading, error, remote, cascadePending } = useFieldOptions(field, loadOptions, values)

  switch (field.type) {
    case 'textarea':
      return (
        <textarea
          className="flex h-20 w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          value={(value as string) ?? ''}
          placeholder={field.placeholder}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'select':
      if (cascadePending) return <OptionsHint text="请先选择上级字段" />
      return (
        <SearchableSelect
          options={options}
          value={(value as string) ?? ''}
          placeholder={field.placeholder}
          disabled={disabled}
          loading={loading}
          error={error}
          searchable={remote || options.length > 8}
          onChange={onChange}
        />
      )
    case 'multiselect': {
      const arr = Array.isArray(value) ? (value as string[]) : []
      const toggle = (v: string) => {
        if (arr.includes(v)) onChange(arr.filter(x => x !== v))
        else onChange([...arr, v])
      }
      if (loading) return <OptionsHint text="选项加载中…" />
      if (error) return <OptionsHint text={`选项加载失败：${error}`} tone="error" />
      if (options.length === 0) return <OptionsHint text="暂无可选项" />
      return (
        <div className="flex flex-wrap gap-1.5">
          {options.map(o => {
            const active = arr.includes(o.value)
            return (
              <button
                type="button"
                key={o.value}
                disabled={disabled}
                onClick={() => toggle(o.value)}
                className={`text-xs px-2.5 py-1 rounded-full border transition-colors ${
                  active
                    ? 'bg-blue-500/15 border-blue-500 text-blue-600 dark:text-blue-400'
                    : 'border-input hover:bg-accent'
                } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
              >
                {o.label}
              </button>
            )
          })}
        </div>
      )
    }
    case 'number':
      return (
        <input
          type="number"
          className={fieldBaseCls}
          value={value === undefined || value === null ? '' : String(value)}
          placeholder={field.placeholder}
          disabled={disabled}
          min={field.validation?.min}
          max={field.validation?.max}
          onChange={e => onChange(e.target.value === '' ? '' : Number(e.target.value))}
        />
      )
    case 'date':
      return (
        <input
          type="date"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'datetime':
      return (
        <input
          type="datetime-local"
          className={fieldBaseCls}
          value={toDatetimeLocal((value as string) ?? '')}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'duration':
      return (
        <input
          type="text"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          placeholder={field.placeholder || '如 1h / 30m / 3d'}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'attachment':
      return (
        <AttachmentControl
          field={field}
          value={value}
          disabled={disabled}
          ticketId={ticketId}
          onChange={onChange}
        />
      )
    case 'richtext':
      // 真·所见即所得编辑器（TipTap）。存储格式为 Markdown，与知识库/工单描述保持一致
      return (
        <RichTextEditor
          value={(value as string) ?? ''}
          onChange={md => onChange(md)}
          readOnly={disabled}
          minHeight="160px"
        />
      )
    case 'table':
      return (
        <TableControl
          field={field}
          value={value}
          disabled={disabled}
          onChange={onChange}
        />
      )
    case 'bool':
      return (
        <label className="flex items-center gap-2 text-sm cursor-pointer select-none">
          <input
            type="checkbox"
            className="h-4 w-4 rounded border-input"
            checked={Boolean(value)}
            disabled={disabled}
            onChange={e => onChange(e.target.checked)}
          />
          <span className="text-muted-foreground">{field.placeholder || '是 / 否'}</span>
        </label>
      )
    // user(选人) / ci(选配置项)：配了来源就走下拉搜索，否则退化为文本输入
    case 'user':
    case 'ci':
      if (remote || options.length > 0) {
        return (
          <SearchableSelect
            options={options}
            value={(value as string) ?? ''}
            placeholder={field.placeholder || (field.type === 'user' ? '搜索并选择用户' : '搜索并选择配置项')}
            disabled={disabled}
            loading={loading}
            error={error}
            searchable
            onChange={onChange}
          />
        )
      }
      return (
        <input
          type="text"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          placeholder={field.placeholder || '未配置数据来源，可手工填写'}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'cascader':
      return (
        <Cascader
          field={field}
          value={value}
          disabled={disabled}
          onChange={onChange}
          loadOptions={loadOptions}
        />
      )
    case 'password':
      return (
        <input
          type="password"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          placeholder={field.placeholder || '请输入密码'}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'time':
      return (
        <input
          type="time"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
    case 'timerange': {
      // 值形如 "HH:mm~HH:mm"，拆成起止两个 time 输入
      const parts = typeof value === 'string' && value.includes('~') ? value.split('~') : ['', '']
      const setRange = (i: number, v: string) => {
        const next = [parts[0], parts[1]]
        next[i] = v
        onChange(next[0] || next[1] ? `${next[0]}~${next[1]}` : '')
      }
      return (
        <div className="flex items-center gap-2">
          <input
            type="time"
            className={fieldBaseCls}
            value={parts[0]}
            disabled={disabled}
            onChange={e => setRange(0, e.target.value)}
          />
          <span className="text-muted-foreground text-sm">~</span>
          <input
            type="time"
            className={fieldBaseCls}
            value={parts[1]}
            disabled={disabled}
            onChange={e => setRange(1, e.target.value)}
          />
        </div>
      )
    }
    case 'link':
      return (
        <div className="space-y-1">
          <input
            type="url"
            className={fieldBaseCls}
            value={(value as string) ?? ''}
            placeholder={field.placeholder || 'https://'}
            disabled={disabled}
            onChange={e => onChange(e.target.value)}
          />
          {typeof value === 'string' && value.trim() !== '' && !disabled && (
            <a
              href={value}
              target="_blank"
              rel="noreferrer"
              className="text-xs text-primary underline underline-offset-2 break-all"
            >
              {value}
            </a>
          )}
        </div>
      )
    case 'expression':
      // 计算/表达式字段：引用 {其它字段} 实时求值只读展示（求值失败回落显示表达式原文）。
      return (
        <input
          type="text"
          className={`${fieldBaseCls} font-mono bg-muted/40 text-muted-foreground`}
          value={expressionDisplay(field, evalContext || {})}
          disabled
          placeholder="计算字段（自动求值）"
        />
      )
    case 'rating': {
      // 评分（1~5 星，对标 SN 5-point scale）：值为数字；范围可用 validation.min/max 覆盖
      const lo = toNumber(field.validation?.min) ?? 1
      const hi = toNumber(field.validation?.max) ?? 5
      const cur = toNumber(value)
      const stars: number[] = []
      for (let n = Math.max(1, lo); n <= Math.min(5, hi); n++) stars.push(n)
      const setRating = (n: number) => onChange(cur === n ? undefined : n) // 再点一次取消
      return (
        <div className="flex items-center gap-0.5">
          {stars.map(n => (
            <button
              type="button"
              key={n}
              disabled={disabled}
              onClick={() => setRating(n)}
              title={`${n} 分`}
              className={`p-0.5 transition-colors ${disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}
            >
              <Star
                className={`h-6 w-6 ${cur !== null && n <= cur ? 'fill-amber-400 text-amber-400' : 'text-muted-foreground/40'}`}
              />
            </button>
          ))}
          {cur !== null && <span className="ml-1.5 text-xs text-muted-foreground">{cur} 分</span>}
        </div>
      )
    }
    case 'daterange': {
      // 日期区间：值形如 "YYYY-MM-DD~YYYY-MM-DD"（与 timerange 同构），拆成起止两个 date 输入
      const parts = typeof value === 'string' && value.includes('~') ? value.split('~') : ['', '']
      const setRange = (i: number, v: string) => {
        const next = [parts[0] || '', parts[1] || '']
        next[i] = v
        onChange(next[0] && next[1] ? `${next[0]}~${next[1]}` : '')
      }
      return (
        <div className="flex items-center gap-2">
          <input
            type="date"
            className={fieldBaseCls}
            value={parts[0]}
            disabled={disabled}
            onChange={e => setRange(0, e.target.value)}
          />
          <span className="text-muted-foreground text-sm">~</span>
          <input
            type="date"
            className={fieldBaseCls}
            value={parts[1]}
            disabled={disabled}
            onChange={e => setRange(1, e.target.value)}
          />
        </div>
      )
    }
    case 'note':
      // 说明区块：纯展示不产生值（后端不校验），在 renderSections 中整行渲染
      return null
    case 'text':
    default:
      return (
        <input
          type="text"
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          placeholder={field.placeholder}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        />
      )
  }
}

/** 把级联值归一化成路径数组：新版是 string[]，旧版文本输入按 "a/b/c" 斜杠兼容 */
export function toCascaderPath(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(x => String(x).trim()).filter(Boolean)
  if (typeof v === 'string') {
    return v
      .split('/')
      .map(s => s.trim())
      .filter(Boolean)
  }
  return []
}

interface CascaderLevel {
  options: FormFieldOption[]
  selected: string
  parent: string
}

/**
 * resolveLevels —— 按已选路径自顶向下推导出每一级的候选项。
 * 返回 levels（要渲染的下拉个数）与 stale（路径中在树里找不到的残留片段）。
 */
function resolveLevels(
  rootOptions: FormFieldOption[],
  path: string[],
  lazy: Record<string, FormFieldOption[]>,
): { levels: CascaderLevel[]; stale: string[] } {
  const levels: CascaderLevel[] = []
  let cur = rootOptions
  let parent = ''
  let i = 0
  for (; ; i++) {
    levels.push({ options: cur, selected: path[i] ?? '', parent })
    const sel = path[i]
    if (!sel) break
    const node = cur.find(o => o.value === sel)
    if (!node) return { levels, stale: path.slice(i) } // 历史值已不在当前树里
    const kids = node.children ?? lazy[sel]
    if (!kids || kids.length === 0) break // 叶子节点，不再往下渲染
    parent = sel
    cur = kids
  }
  return { levels, stale: [] }
}

/**
 * Cascader —— 多级联动选择器。
 * 静态 options（带 children）直接本地逐级展开；配了 source 则先取根层级，
 * 选中项若未随包返回 children 再按 parent 懒加载下一级。值统一为 string[] 路径。
 */
function Cascader({
  field,
  value,
  disabled,
  onChange,
  loadOptions,
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
  loadOptions?: LoadOptionsFn
}) {
  const staticOpts = useMemo(() => field.options || [], [field.options])
  const source = field.source || ''
  const needRemote = staticOpts.length === 0 && !!source && !!loadOptions

  const path = useMemo(() => toCascaderPath(value), [value])

  const [root, setRoot] = useState<FormFieldOption[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [lazy, setLazy] = useState<Record<string, FormFieldOption[]>>({})
  const reqRef = useRef(0)
  const attemptedRef = useRef<Set<string>>(new Set())

  // 根层级（parent 传空串 → 层级模式）
  useEffect(() => {
    if (!needRemote) return
    const seq = ++reqRef.current
    setLoading(true)
    setError('')
    attemptedRef.current = new Set()
    loadOptions!(source, undefined, '')
      .then(opts => {
        if (reqRef.current !== seq) return
        setRoot(opts || [])
      })
      .catch((e: unknown) => {
        if (reqRef.current !== seq) return
        setError(e instanceof Error ? e.message : '选项加载失败')
      })
      .finally(() => {
        if (reqRef.current === seq) setLoading(false)
      })
  }, [needRemote, source, loadOptions])

  const rootOptions = staticOpts.length > 0 ? staticOpts : root
  const { levels, stale } = useMemo(
    () => resolveLevels(rootOptions, path, lazy),
    [rootOptions, path, lazy],
  )

  // 末级选中项若未随包带回 children，按 parent 懒加载一次（空结果=叶子，也会被缓存住不再重试）
  const tail = path.length > 0 ? path[path.length - 1] : ''
  const tailHasChildren = useMemo(() => {
    if (!tail) return true
    const last = levels[levels.length - 1]
    const node = last?.options.find(o => o.value === tail)
    return node?.children !== undefined
  }, [tail, levels])

  useEffect(() => {
    if (!needRemote || !tail || tailHasChildren) return
    if (attemptedRef.current.has(tail)) return
    attemptedRef.current.add(tail)
    loadOptions!(source, undefined, tail)
      .then(kids => setLazy(prev => ({ ...prev, [tail]: kids || [] })))
      .catch(() => setLazy(prev => ({ ...prev, [tail]: [] })))
  }, [needRemote, source, loadOptions, tail, tailHasChildren])

  const pick = (levelIdx: number, v: string) => {
    const next = path.slice(0, levelIdx)
    if (v) next.push(v)
    onChange(next)
  }

  const crumbs = useMemo(() => {
    const out: string[] = []
    levels.forEach(lv => {
      if (!lv.selected) return
      out.push(lv.options.find(o => o.value === lv.selected)?.label || lv.selected)
    })
    return out.concat(stale)
  }, [levels, stale])

  if (needRemote && loading && rootOptions.length === 0) return <OptionsHint text="选项加载中…" />
  if (needRemote && error) return <OptionsHint text={`选项加载失败：${error}`} tone="error" />
  if (rootOptions.length === 0) {
    return (
      <OptionsHint
        text={
          needRemote
            ? '该来源暂无可选层级'
            : '未配置层级选项来源，请在表单配置中设置动态来源或静态层级选项'
        }
      />
    )
  }

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap gap-1.5">
        {levels.map((lv, i) => (
          <select
            key={`${lv.parent}-${i}`}
            className={`${fieldBaseCls} w-auto min-w-[9rem] flex-1`}
            value={lv.selected}
            disabled={disabled}
            onChange={e => pick(i, e.target.value)}
          >
            <option value="">{i === 0 ? field.placeholder || '请选择' : '下一级（可选）'}</option>
            {lv.options.map(o => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        ))}
      </div>
      {crumbs.length > 0 && (
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="truncate">已选：{crumbs.join(' / ')}</span>
          {!disabled && (
            <button type="button" className="shrink-0 hover:text-foreground underline" onClick={() => onChange([])}>
              清空
            </button>
          )}
        </div>
      )}
      {stale.length > 0 && (
        <OptionsHint text={`历史取值「${stale.join(' / ')}」已不在当前层级中，请重新选择`} tone="error" />
      )}
    </div>
  )
}

function OptionsHint({ text, tone }: { text: string; tone?: 'error' }) {
  return (
    <p className={`text-xs italic ${tone === 'error' ? 'text-destructive' : 'text-muted-foreground'}`}>{text}</p>
  )
}

/** SearchableSelect —— 带前端搜索的下拉；选项少时退化为普通 select 体验 */
function SearchableSelect({
  options,
  value,
  placeholder,
  disabled,
  loading,
  error,
  searchable,
  onChange,
}: {
  options: FormFieldOption[]
  value: string
  placeholder?: string
  disabled?: boolean
  loading?: boolean
  error?: string
  searchable?: boolean
  onChange: (v: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [keyword, setKeyword] = useState('')
  const boxRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDocClick = (e: MouseEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', onDocClick)
    return () => document.removeEventListener('mousedown', onDocClick)
  }, [open])

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return options
    return options.filter(o => o.label.toLowerCase().includes(kw) || o.value.toLowerCase().includes(kw))
  }, [options, keyword])

  const selectedLabel = options.find(o => o.value === value)?.label || value

  if (!searchable) {
    return (
      <>
        <select
          className={fieldBaseCls}
          value={value}
          disabled={disabled || loading}
          onChange={e => onChange(e.target.value)}
        >
          <option value="">{loading ? '选项加载中…' : placeholder || '请选择'}</option>
          {options.map(o => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        {error && <OptionsHint text={`选项加载失败：${error}`} tone="error" />}
      </>
    )
  }

  return (
    <div className="relative" ref={boxRef}>
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen(v => !v)}
        className={`${fieldBaseCls} items-center justify-between text-left ${
          disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'
        }`}
      >
        <span className={selectedLabel ? '' : 'text-muted-foreground'}>
          {loading ? '选项加载中…' : selectedLabel || placeholder || '请选择'}
        </span>
        <span className="text-muted-foreground text-xs ml-2">▾</span>
      </button>
      {error && <OptionsHint text={`选项加载失败：${error}`} tone="error" />}
      {open && !disabled && (
        <div className="absolute z-50 mt-1 w-full rounded-md border border-input bg-popover shadow-lg">
          <div className="p-2 border-b border-input">
            <input
              autoFocus
              type="text"
              className="h-8 w-full rounded border border-input bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              placeholder="搜索…"
              value={keyword}
              onChange={e => setKeyword(e.target.value)}
            />
          </div>
          <div className="max-h-56 overflow-auto py-1">
            {value && (
              <button
                type="button"
                className="w-full px-3 py-1.5 text-left text-xs text-muted-foreground hover:bg-accent"
                onClick={() => {
                  onChange('')
                  setOpen(false)
                }}
              >
                清空选择
              </button>
            )}
            {filtered.length === 0 && (
              <p className="px-3 py-2 text-xs text-muted-foreground italic">
                {loading ? '加载中…' : '无匹配选项'}
              </p>
            )}
            {filtered.map(o => (
              <button
                type="button"
                key={o.value}
                className={`w-full px-3 py-1.5 text-left text-sm hover:bg-accent ${
                  o.value === value ? 'bg-accent/60 font-medium' : ''
                }`}
                onClick={() => {
                  onChange(o.value)
                  setOpen(false)
                  setKeyword('')
                }}
              >
                {o.label}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// toDatetimeLocal 将 RFC3339/ISO 值转成 <input type="datetime-local"> 需要的 "YYYY-MM-DDTHH:mm"。
// 无法解析时原样返回(空值也原样), 保证受控组件不回填乱码。
function toDatetimeLocal(v: string): string {
  if (!v) return ''
  if (/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(v)) return v
  const d = new Date(v)
  if (isNaN(d.getTime())) return v
  const pad = (n: number) => String(n).padStart(2, '0')
  return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes())
}

/** 附件元数据（后端上传/列表接口返回的字段子集） */
interface AttachmentMeta {
  id: string
  file_name?: string
  file_size?: number
  file_type?: string
}

/** 人类可读体积 */
export function formatBytes(n?: number): string {
  const v = Number(n || 0)
  if (!v) return ''
  if (v < 1024) return `${v} B`
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`
  return `${(v / 1024 / 1024).toFixed(1)} MB`
}

/**
 * 按 accept 规则校验文件类型。规则用逗号分隔，支持三种写法：
 * ".png"（扩展名）、"image/*"（MIME 通配）、"application/pdf"（精确 MIME）。
 */
export function matchAccept(file: File, accept: string): boolean {
  const rules = accept.split(',').map(s => s.trim().toLowerCase()).filter(Boolean)
  if (rules.length === 0) return true
  const name = file.name.toLowerCase()
  const mime = (file.type || '').toLowerCase()
  return rules.some(r => {
    if (r.startsWith('.')) return name.endsWith(r)
    if (r.endsWith('/*')) return mime.startsWith(r.slice(0, -1))
    return mime === r
  })
}

// AttachmentControl —— attachment 字段控件：上传文件到工单附件区, 值 = 附件 ID 字符串数组。
// 支持字段级约束：单文件体积上限(max_size MB)、允许类型(accept)、最多数量(max_count)、多文件开关(multiple)。
// 上传成功后展示预览（图片走缩略图，其它走文件卡片）。需要 ticketId 才能上传。
function AttachmentControl({
  field,
  value,
  disabled,
  ticketId,
  onChange,
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  ticketId?: string
  onChange: (v: unknown) => void
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')
  const [metaMap, setMetaMap] = useState<Record<string, AttachmentMeta>>({})
  const ids = Array.isArray(value) ? (value as (string | number)[]).map(String) : []

  const maxSizeMB = Number(field.max_size || 0)
  const accept = (field.accept || '').trim()
  const maxCount = Number(field.max_count || 0)
  // undefined 视为未配置(允许多选)，仅显式 false 才限制为单文件
  const multiple = field.multiple !== false

  const idsKey = ids.join(',')
  // 回填历史附件的元数据，让已保存的值也能显示文件名/缩略图而不是「附件 #12」
  useEffect(() => {
    if (!ticketId || ids.length === 0) return
    if (ids.every(id => metaMap[id])) return
    let alive = true
    fetch(`/api/v1/itsm/tickets/${ticketId}/attachments`)
      .then(r => (r.ok ? r.json() : []))
      .then((list: unknown) => {
        if (!alive || !Array.isArray(list)) return
        setMetaMap(prev => {
          const next = { ...prev }
          for (const a of list as AttachmentMeta[]) next[String(a.id)] = { ...a, id: String(a.id) }
          return next
        })
      })
      .catch(() => {})
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ticketId, idsKey])

  const constraintHint = [
    maxSizeMB > 0 ? `单文件 ≤ ${maxSizeMB}MB` : '',
    accept ? `允许 ${accept}` : '',
    maxCount > 0 ? `最多 ${maxCount} 个` : '',
    multiple ? '' : '仅限 1 个',
  ]
    .filter(Boolean)
    .join(' · ')

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const picked = Array.from(e.target.files || [])
    if (picked.length === 0 || !ticketId) return
    setError('')

    // 数量约束（多选一次挑多个也要拦住）
    const limit = !multiple ? 1 : maxCount > 0 ? maxCount : Infinity
    if (ids.length + picked.length > limit) {
      setError(!multiple ? '该字段只允许上传 1 个附件' : `最多允许上传 ${maxCount} 个附件`)
      if (inputRef.current) inputRef.current.value = ''
      return
    }
    // 体积 / 类型约束
    for (const f of picked) {
      if (maxSizeMB > 0 && f.size > maxSizeMB * 1024 * 1024) {
        setError(`「${f.name}」超过 ${maxSizeMB}MB 限制（实际 ${formatBytes(f.size)}）`)
        if (inputRef.current) inputRef.current.value = ''
        return
      }
      if (accept && !matchAccept(f, accept)) {
        setError(`「${f.name}」类型不被允许，仅接受 ${accept}`)
        if (inputRef.current) inputRef.current.value = ''
        return
      }
    }

    setUploading(true)
    const added: string[] = []
    try {
      for (const f of picked) {
        const fd = new FormData()
        fd.append('file', f)
        fd.append('uploaded_by', 'form')
        const res = await fetch(`/api/v1/itsm/tickets/${ticketId}/attachments`, { method: 'POST', body: fd })
        if (!res.ok) throw new Error(`「${f.name}」上传失败(${res.status})`)
        const att = (await res.json()) as AttachmentMeta
        const id = String(att.id)
        added.push(id)
        setMetaMap(prev => ({ ...prev, [id]: { ...att, id } }))
      }
      onChange([...ids, ...added])
    } catch (err: unknown) {
      // 已成功的部分照样写回，避免用户重传
      if (added.length > 0) onChange([...ids, ...added])
      setError(err instanceof Error ? err.message : '上传失败')
    }
    setUploading(false)
    if (inputRef.current) inputRef.current.value = ''
  }

  const remove = (id: string) => onChange(ids.filter(x => x !== id))

  if (!ticketId) {
    return (
      <div className="space-y-1">
        <p className="text-xs text-muted-foreground">附件需在工单创建后关联，可稍后在工单附件区补充。</p>
        {constraintHint && <p className="text-[11px] text-muted-foreground/80">{constraintHint}</p>}
      </div>
    )
  }

  const reachedLimit = !multiple ? ids.length >= 1 : maxCount > 0 && ids.length >= maxCount

  return (
    <div className="space-y-2">
      <input
        ref={inputRef}
        type="file"
        className="hidden"
        multiple={multiple}
        accept={accept || undefined}
        onChange={handleUpload}
      />
      <div className="flex items-center gap-2 flex-wrap">
        <button
          type="button"
          disabled={disabled || uploading || reachedLimit}
          onClick={() => inputRef.current?.click()}
          className="inline-flex items-center gap-1 h-8 px-3 rounded-md border border-input text-xs hover:bg-accent disabled:opacity-50"
        >
          <Upload className="h-3 w-3" />
          {uploading ? '上传中…' : reachedLimit ? '已达数量上限' : multiple ? '上传附件' : '上传附件（单个）'}
        </button>
        {constraintHint && <span className="text-[11px] text-muted-foreground">{constraintHint}</span>}
      </div>
      {ids.length > 0 && (
        <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          {ids.map(id => {
            const meta = metaMap[id]
            const url = `/api/v1/itsm/tickets/${ticketId}/attachments/${id}`
            const isImage = (meta?.file_type || '').startsWith('image/')
            return (
              <div key={id} className="flex items-center gap-2 border rounded-md px-2 py-1.5 text-xs bg-muted/20">
                {isImage ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img src={url} alt={meta?.file_name || id} className="h-9 w-9 shrink-0 rounded object-cover border" />
                ) : (
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded border bg-background">
                    <FileText className="h-4 w-4 text-muted-foreground" />
                  </span>
                )}
                <div className="min-w-0 flex-1">
                  <a
                    className="block truncate text-primary underline underline-offset-2"
                    href={url}
                    target="_blank"
                    rel="noreferrer"
                    title={meta?.file_name || `附件 #${id}`}
                  >
                    {meta?.file_name || `附件 #${id}`}
                  </a>
                  {meta?.file_size ? (
                    <span className="text-[11px] text-muted-foreground">{formatBytes(meta.file_size)}</span>
                  ) : null}
                </div>
                {!disabled && (
                  <button
                    type="button"
                    className="shrink-0 text-muted-foreground hover:text-destructive"
                    onClick={() => remove(id)}
                    title="移除"
                  >
                    <X className="h-3 w-3" />
                  </button>
                )}
              </div>
            )
          })}
        </div>
      )}
      {error && (
        <p className="flex items-start gap-1 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3 w-3 shrink-0" />
          <span>{error}</span>
        </p>
      )}
    </div>
  )
}

// TableControl —— table 字段控件。
// 结构化模式（field.table_columns 非空，对标 SN MRVS）：值 = 对象数组 [{列key: 单元格文本}]，
// 表头由列定义驱动，逐列输入；旧自由模式：值 = string[][]，可自由加行加列。
// 旧数据（二维数组）遇到列定义时按列顺序自动迁移显示。
function TableControl({
  field,
  value,
  disabled,
  onChange,
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
}) {
  const columns = (field.table_columns || []).filter(c => (c.key || '').trim())
  if (columns.length > 0) {
    return <StructuredTableControl columns={columns} value={value} disabled={disabled} onChange={onChange} />
  }
  return <FreeTableControl value={value} disabled={disabled} onChange={onChange} />
}

function StructuredTableControl({
  columns,
  value,
  disabled,
  onChange,
}: {
  columns: TableColumnDef[]
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
}) {
  // 归一化：对象行直接用；旧二维数组按列顺序迁移（row[i] → columns[i].key）
  const rows: Record<string, string>[] = Array.isArray(value)
    ? (value as unknown[]).map(r => {
        if (r && typeof r === 'object' && !Array.isArray(r)) {
          const obj = r as Record<string, unknown>
          const out: Record<string, string> = {}
          for (const c of columns) out[c.key] = obj[c.key] === undefined || obj[c.key] === null ? '' : String(obj[c.key])
          return out
        }
        const legacy = Array.isArray(r) ? (r as unknown[]) : []
        const out: Record<string, string> = {}
        columns.forEach((c, i) => { out[c.key] = legacy[i] === undefined || legacy[i] === null ? '' : String(legacy[i]) })
        return out
      })
    : []

  const emptyRow = () => Object.fromEntries(columns.map(c => [c.key, '']))
  const setCell = (r: number, key: string, v: string) =>
    onChange(rows.map((row, ri) => (ri === r ? { ...row, [key]: v } : row)))

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/40">
              {columns.map(c => (
                <th key={c.key} className="whitespace-nowrap px-2 py-1.5 text-left text-xs font-medium text-muted-foreground">
                  {c.label || c.key}
                  {c.required && <span className="text-destructive">*</span>}
                </th>
              ))}
              {!disabled && <th className="w-8" />}
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 && (
              <tr>
                <td colSpan={columns.length + (disabled ? 0 : 1)} className="px-2 py-2 text-center text-xs text-muted-foreground italic">
                  暂无数据行
                </td>
              </tr>
            )}
            {rows.map((row, ri) => (
              <tr key={ri} className="border-b last:border-b-0">
                {columns.map(c => (
                  <td key={c.key} className="border-r last:border-r-0 p-0">
                    <input
                      className="w-full min-w-[80px] bg-transparent px-2 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      value={row[c.key] ?? ''}
                      disabled={disabled}
                      onChange={e => setCell(ri, c.key, e.target.value)}
                    />
                  </td>
                ))}
                {!disabled && (
                  <td className="w-8 p-0">
                    <button type="button" className="h-full w-full text-muted-foreground hover:text-destructive" onClick={() => onChange(rows.filter((_, i) => i !== ri))}>
                      <X className="h-3 w-3 mx-auto" />
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!disabled && (
        <button type="button" onClick={() => onChange([...rows, emptyRow()])} className="inline-flex items-center gap-1 h-7 px-2.5 rounded-md border border-input text-xs hover:bg-accent">
          <Plus className="h-3 w-3" />加一行
        </button>
      )}
    </div>
  )
}

function FreeTableControl({
  value,
  disabled,
  onChange,
}: {
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
}) {
  const grid: string[][] = Array.isArray(value)
    ? (value as unknown[][]).map(row => Array.isArray(row) ? row.map(c => String(c ?? '')) : [])
    : [['', ''], ['', '']]

  const setCell = (r: number, c: number, v: string) => {
    const next = grid.map((row, ri) => row.map((cell, ci) => (ri === r && ci === c ? v : cell)))
    onChange(next)
  }
  const addRow = () => onChange([...grid, Array(grid[0]?.length || 2).fill('')])
  const addCol = () => onChange(grid.map(row => [...row, '']))
  const removeRow = (r: number) => onChange(grid.filter((_, i) => i !== r))
  const removeCol = (c: number) => onChange(grid.map(row => row.filter((_, i) => i !== c)))

  return (
    <div className="space-y-2">
      <div className="overflow-x-auto border rounded-md">
        <table className="w-full text-sm">
          <tbody>
            {grid.map((row, ri) => (
              <tr key={ri} className="border-b last:border-b-0">
                {row.map((cell, ci) => (
                  <td key={ci} className="border-r last:border-r-0 p-0">
                    <input
                      className="w-full min-w-[80px] bg-transparent px-2 py-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      value={cell}
                      disabled={disabled}
                      onChange={e => setCell(ri, ci, e.target.value)}
                    />
                  </td>
                ))}
                {!disabled && (
                  <td className="w-8 p-0">
                    <button type="button" className="h-full w-full text-muted-foreground hover:text-destructive" onClick={() => removeRow(ri)}>
                      <X className="h-3 w-3 mx-auto" />
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!disabled && (
        <div className="flex gap-2">
          <button type="button" onClick={addRow} className="inline-flex items-center gap-1 h-7 px-2.5 rounded-md border border-input text-xs hover:bg-accent">
            <Plus className="h-3 w-3" />加行
          </button>
          <button type="button" onClick={addCol} className="inline-flex items-center gap-1 h-7 px-2.5 rounded-md border border-input text-xs hover:bg-accent">
            <Plus className="h-3 w-3" />加列
          </button>
          {grid[0]?.length > 1 && (
            <button type="button" onClick={() => removeCol(grid[0].length - 1)} className="inline-flex items-center gap-1 h-7 px-2.5 rounded-md border border-input text-xs text-muted-foreground hover:text-destructive">
              <X className="h-3 w-3" />删末列
            </button>
          )}
        </div>
      )}
    </div>
  )
}
