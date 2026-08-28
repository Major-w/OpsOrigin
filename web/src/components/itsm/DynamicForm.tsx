
// DynamicForm —— ITSM 可配置表单渲染器。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐：按字段定义渲染控件，
// 支持条件显隐(多规则 AND/OR，可引用系统字段)、动态选项来源(cmdb:* / iam:*)、
// 必填标星、只读禁用、分区(Section)分组。
// 该组件只渲染「业务自定义字段」，系统字段(title/risk/...)仍由页面负责。

import { useEffect, useMemo, useRef, useState } from 'react'
import { Upload, X, Plus, FileText, AlertCircle } from 'lucide-react'
import { RichTextEditor } from '@/components/rich-text-editor'

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

export interface FormFieldOption {
  value: string
  label: string
  /** 级联子项：cascader 逐级展开用；扁平控件忽略 */
  children?: FormFieldOption[]
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

/**
 * 动态选项加载器：由页面注入，通常打 GET /itsm/form-options?source=
 * parent 传字符串（含空串）时进入层级模式，返回该父节点下的子选项，供 cascader 逐级展开；
 * 不传 parent 即平铺模式。
 */
export type LoadOptionsFn = (
  source: string,
  keyword?: string,
  parent?: string,
) => Promise<FormFieldOption[]>

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
}

const fieldBaseCls =
  'flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function DynamicForm({ schema, values, context, onChange, readOnly, loadOptions, ticketId }: DynamicFormProps) {
  // 桌面端(>=640px)才应用显式 col_start：窄屏统一单列，避免强制定位导致横向溢出
  const [desktop, setDesktop] = useState(true)
  useEffect(() => {
    const mq = window.matchMedia('(min-width: 640px)')
    const sync = () => setDesktop(mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])

  if (!schema || !schema.fields || schema.fields.length === 0) {
    return (
      <p className="text-xs text-muted-foreground italic">
        当前作用域（{schema?.scope || 'ticket:default'}）未配置自定义字段。可点击「表单配置」定义业务字段。
      </p>
    )
  }

  const ctx = { ...context, ...values }
  const fields = [...schema.fields].sort((a, b) => (a.order || 0) - (b.order || 0))
  const visibleFields = fields.filter(f => fieldVisible(f, ctx))

  // 按 section 分组
  const sections = new Map<string, FormFieldDef[]>()
  for (const f of visibleFields) {
    const sec = f.section || ''
    if (!sections.has(sec)) sections.set(sec, [])
    sections.get(sec)!.push(f)
  }

  if (visibleFields.length === 0) {
    return <p className="text-xs text-muted-foreground italic">当前条件下无可填写的自定义字段。</p>
  }

  return (
    <div className="space-y-4">
      {Array.from(sections.entries()).map(([sec, fs]) => (
        <div key={sec || '__root'} className="space-y-3">
          {sec && <h4 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{sec}</h4>}
          {/* 12 栅格：字段按 col_span 占列，缺省整行；窄屏统一降为单列避免挤压 */}
          <div className="grid grid-cols-1 gap-x-4 gap-y-3 sm:grid-cols-12">
            {fs.map(f => {
              const span = normalizeColSpan(f.col_span)
              const explicit = desktop && f.col_start ? { gridColumn: `${f.col_start} / span ${span}` } : undefined
              return (
              <div key={f.key} className={COL_SPAN_CLS[span]} style={explicit}>
                <label className="text-sm font-medium flex items-center gap-1">
                  {f.label}
                  {f.required && <span className="text-destructive">*</span>}
                </label>
                <div className="mt-1">
                  <FieldControl
                    field={f}
                    value={values[f.key]}
                    disabled={readOnly || !!f.readonly}
                    onChange={v => onChange(f.key, v)}
                    loadOptions={loadOptions}
                    ticketId={ticketId}
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

/** useFieldOptions —— 静态 options 优先；否则按 source 异步拉取（带 loading / error 态） */
function useFieldOptions(field: FormFieldDef, loadOptions?: LoadOptionsFn) {
  const staticOpts = field.options || []
  const source = effectiveSource(field)
  // 级联字段自己按层级拉取（见 Cascader），这里不重复打平铺请求
  const needRemote = staticOpts.length === 0 && !!source && !!loadOptions && field.type !== 'cascader'

  const [remote, setRemote] = useState<FormFieldOption[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const reqRef = useRef(0)

  useEffect(() => {
    if (!needRemote) return
    const seq = ++reqRef.current
    setLoading(true)
    setError('')
    loadOptions!(source)
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
  }, [needRemote, source, loadOptions])

  return {
    options: staticOpts.length > 0 ? staticOpts : remote,
    loading,
    error,
    remote: needRemote,
  }
}

function FieldControl({
  field,
  value,
  disabled,
  onChange,
  loadOptions,
  ticketId,
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
  loadOptions?: LoadOptionsFn
  ticketId?: string
}) {
  const { options, loading, error, remote } = useFieldOptions(field, loadOptions)

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

// TableControl —— table 字段控件: 二维网格编辑, 值 = string[][]。
function TableControl({
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
