'use client'

// DynamicForm —— ITSM 可配置表单渲染器。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐：按字段定义渲染控件，
// 支持条件显隐(VisibleWhen, 可引用系统字段)、必填标星、只读禁用、分区(Section)分组。
// 该组件只渲染「业务自定义字段」，系统字段(title/risk/...)仍由页面负责。

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
  visible_when?: string
  readonly?: boolean
  source?: string
}

export interface FormSchema {
  version: number
  scope: string
  fields: FormFieldDef[]
}

// fieldVisible 与后端 form.FieldVisible 语义一致：解析 "key==value" / "key!=value"。
// 系统字段（如 risk_level）通过 context 传入，自定义字段通过 values 传入。
export function fieldVisible(field: FormFieldDef, context: Record<string, unknown>): boolean {
  const expr = (field.visible_when || '').trim()
  if (!expr) return true
  const evalExpr = (sep: '==' | '!=', whenEqual: boolean): boolean => {
    const [k, wantRaw] = expr.split(sep).map(s => s.trim())
    const want = wantRaw || ''
    const v = context[k]
    const exists = v !== undefined && v !== null
    const equal = exists && String(v) === want
    // == : 相等才显示；!= : 不等才显示（不存在视为不等）
    return whenEqual ? equal : !equal
  }
  if (expr.includes('==')) return evalExpr('==', true)
  if (expr.includes('!=')) return evalExpr('!=', false)
  return true
}

interface DynamicFormProps {
  schema?: FormSchema | null
  // values: 本表单采集到的自定义字段值（受控）
  values: Record<string, unknown>
  // context: 用于 VisibleWhen 判定的上下文（通常含系统字段，如 risk_level），可选
  context?: Record<string, unknown>
  // onChange(key, value): 单字段变更回调
  onChange: (key: string, value: unknown) => void
  readOnly?: boolean
}

const fieldBaseCls =
  'flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring'

export function DynamicForm({ schema, values, context, onChange, readOnly }: DynamicFormProps) {
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

function FieldControl({
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
        <select
          className={fieldBaseCls}
          value={(value as string) ?? ''}
          disabled={disabled}
          onChange={e => onChange(e.target.value)}
        >
          <option value="">{field.placeholder || '请选择'}</option>
          {(field.options || []).map(o => (
            <option key={o.value} value={o.value}>{o.label}</option>
          ))}
        </select>
      )
    case 'multiselect': {
      const arr = Array.isArray(value) ? (value as string[]) : []
      const toggle = (v: string) => {
        if (arr.includes(v)) onChange(arr.filter(x => x !== v))
        else onChange([...arr, v])
      }
      return (
        <div className="flex flex-wrap gap-1.5">
          {(field.options || []).map(o => {
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
    // user / ci / cascader 在 P0 阶段用文本输入（后续对接 IAM / CMDB 数据源）
    case 'user':
    case 'ci':
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
