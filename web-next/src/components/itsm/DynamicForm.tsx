'use client'

// DynamicForm —— ITSM 可配置表单渲染器。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐：按字段定义渲染控件，
// 支持条件显隐(多规则 AND/OR，可引用系统字段)、动态选项来源(cmdb:* / iam:*)、
// 必填标星、只读禁用、分区(Section)分组。
// 该组件只渲染「业务自定义字段」，系统字段(title/risk/...)仍由页面负责。

import { useEffect, useMemo, useRef, useState } from 'react'

export type FormFieldType =
  | 'text'
  | 'textarea'
  | 'select'
  | 'multiselect'
  | 'number'
  | 'date'
  | 'bool'
  | 'user'
  | 'ci'
  | 'cascader'

export interface FormFieldOption {
  value: string
  label: string
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
  /** 旧版单条表达式，如 "risk_level==critical"，保留兼容 */
  visible_when?: string
  /** 多规则显隐，优先于 visible_when */
  visible_when_rules?: FormCondition[]
  /** 规则组合逻辑：and(默认) | or */
  visible_logic?: 'and' | 'or'
  /** 动态选项来源：cmdb:<typeName> / iam:user / iam:role / iam:group */
  source?: string
}

export interface FormSchema {
  version: number
  scope: string
  fields: FormFieldDef[]
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

/** 动态选项加载器：由页面注入，通常打 GET /itsm/form-options?source= */
export type LoadOptionsFn = (source: string, keyword?: string) => Promise<FormFieldOption[]>

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
}

const fieldBaseCls =
  'flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function DynamicForm({ schema, values, context, onChange, readOnly, loadOptions }: DynamicFormProps) {
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
          {fs.map(f => (
            <div key={f.key}>
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
                />
              </div>
              {f.help && <p className="text-xs text-muted-foreground mt-1">{f.help}</p>}
            </div>
          ))}
        </div>
      ))}
    </div>
  )
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
  const needRemote = staticOpts.length === 0 && !!source && !!loadOptions

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
}: {
  field: FormFieldDef
  value: unknown
  disabled: boolean
  onChange: (v: unknown) => void
  loadOptions?: LoadOptionsFn
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
                    ? 'bg-emerald-500/15 border-emerald-500 text-emerald-600 dark:text-emerald-400'
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
