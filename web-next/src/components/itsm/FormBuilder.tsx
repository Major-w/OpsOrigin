'use client'

// FormBuilder —— ITSM 表单 Schema 可视化配置器。
// 左侧字段列表（增删改 + 排序），右侧属性面板（基础属性 / 选项来源 / 校验 / 条件显隐），
// 底部实时预览用真实 DynamicForm 渲染，所见即所得。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐，产出结构可直接 PUT /itsm/forms。

import { useEffect, useMemo, useState } from 'react'
import { ArrowDown, ArrowUp, Copy, Eye, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import {
  CONDITION_OPS,
  DynamicForm,
  type ConditionOp,
  type FormCondition,
  type FormFieldDef,
  type FormFieldOption,
  type FormFieldType,
  type FormSchema,
  type LoadOptionsFn,
  parseVisibleWhen,
} from './DynamicForm'

const FIELD_TYPES: { value: FormFieldType; label: string }[] = [
  { value: 'text', label: '单行文本' },
  { value: 'textarea', label: '多行文本' },
  { value: 'select', label: '单选' },
  { value: 'multiselect', label: '多选' },
  { value: 'number', label: '数字' },
  { value: 'date', label: '日期' },
  { value: 'datetime', label: '日期时间' },
  { value: 'duration', label: '时长' },
  { value: 'attachment', label: '附件' },
  { value: 'richtext', label: '富文本' },
  { value: 'table', label: '表格' },
  { value: 'bool', label: '开关' },
  { value: 'user', label: '选人' },
  { value: 'ci', label: '选配置项' },
  { value: 'cascader', label: '级联' },
]

const OPTION_TYPES: FormFieldType[] = ['select', 'multiselect', 'user', 'ci', 'cascader']

const inputCls =
  'flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

interface FormBuilderProps {
  schema: FormSchema
  onChange: (schema: FormSchema) => void
  /** 可用的动态选项来源清单（GET /itsm/form-options 无参返回） */
  sources?: string[]
  /** 预览用的选项加载器 */
  loadOptions?: LoadOptionsFn
  /** 条件显隐可引用的系统字段（除自定义字段外） */
  systemFields?: string[]
  disabled?: boolean
}

const DEFAULT_SYSTEM_FIELDS = ['risk_level', 'status', 'created_by', 'process_id']

export function FormBuilder({
  schema,
  onChange,
  sources = [],
  loadOptions,
  systemFields = DEFAULT_SYSTEM_FIELDS,
  disabled,
}: FormBuilderProps) {
  const fields = schema.fields || []
  const [activeIdx, setActiveIdx] = useState(0)
  const [previewValues, setPreviewValues] = useState<Record<string, unknown>>({})

  useEffect(() => {
    if (activeIdx >= fields.length) setActiveIdx(Math.max(0, fields.length - 1))
  }, [fields.length, activeIdx])

  const active = fields[activeIdx]

  const patch = (next: FormFieldDef[]) => onChange({ ...schema, fields: next })

  const updateField = (idx: number, changes: Partial<FormFieldDef>) => {
    patch(fields.map((f, i) => (i === idx ? { ...f, ...changes } : f)))
  }

  const addField = () => {
    const n = fields.length + 1
    const next: FormFieldDef = {
      key: `field_${n}`,
      label: `字段 ${n}`,
      type: 'text',
      required: false,
      order: n,
    }
    patch([...fields, next])
    setActiveIdx(fields.length)
  }

  const duplicateField = (idx: number) => {
    const src = fields[idx]
    const copy: FormFieldDef = { ...src, key: `${src.key}_copy`, label: `${src.label} 副本`, order: fields.length + 1 }
    const next = [...fields]
    next.splice(idx + 1, 0, copy)
    patch(next.map((f, i) => ({ ...f, order: i + 1 })))
    setActiveIdx(idx + 1)
  }

  const removeField = (idx: number) => {
    patch(fields.filter((_, i) => i !== idx).map((f, i) => ({ ...f, order: i + 1 })))
    setActiveIdx(Math.max(0, idx - 1))
  }

  const move = (idx: number, delta: number) => {
    const target = idx + delta
    if (target < 0 || target >= fields.length) return
    const next = [...fields]
    ;[next[idx], next[target]] = [next[target], next[idx]]
    patch(next.map((f, i) => ({ ...f, order: i + 1 })))
    setActiveIdx(target)
  }

  // 条件显隐可引用的字段：系统字段 + 排在当前字段之前的自定义字段
  const referableFields = useMemo(() => {
    const custom = fields.filter((_, i) => i !== activeIdx).map(f => f.key)
    return [...systemFields, ...custom].filter(Boolean)
  }, [fields, activeIdx, systemFields])

  const duplicateKeys = useMemo(() => {
    const seen = new Set<string>()
    const dup = new Set<string>()
    for (const f of fields) {
      if (seen.has(f.key)) dup.add(f.key)
      seen.add(f.key)
    }
    return dup
  }, [fields])

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-1 lg:grid-cols-[220px_1fr] gap-3">
        {/* 字段列表 */}
        <div className="rounded-md border border-input">
          <div className="flex items-center justify-between px-2 py-1.5 border-b border-input">
            <span className="text-xs font-semibold">字段（{fields.length}）</span>
            <Button size="sm" variant="ghost" onClick={addField} disabled={disabled} className="h-6 px-1.5">
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
          <div className="max-h-64 overflow-auto py-1">
            {fields.length === 0 && (
              <p className="px-2 py-3 text-xs text-muted-foreground italic">暂无字段，点击右上角 + 新增。</p>
            )}
            {fields.map((f, i) => (
              <div
                key={`${f.key}-${i}`}
                onClick={() => setActiveIdx(i)}
                className={`group flex items-center gap-1 px-2 py-1.5 cursor-pointer text-xs ${
                  i === activeIdx ? 'bg-accent' : 'hover:bg-accent/50'
                }`}
              >
                <div className="flex-1 min-w-0">
                  <div className="truncate font-medium">
                    {f.label || f.key}
                    {f.required && <span className="text-destructive ml-0.5">*</span>}
                  </div>
                  <div
                    className={`truncate text-[10px] ${
                      duplicateKeys.has(f.key) ? 'text-destructive' : 'text-muted-foreground'
                    }`}
                  >
                    {f.key} · {FIELD_TYPES.find(t => t.value === f.type)?.label || f.type}
                    {duplicateKeys.has(f.key) && ' · key 重复'}
                  </div>
                </div>
                <div className="hidden group-hover:flex items-center gap-0.5">
                  <IconBtn title="上移" onClick={e => { e.stopPropagation(); move(i, -1) }} disabled={disabled || i === 0}>
                    <ArrowUp className="h-3 w-3" />
                  </IconBtn>
                  <IconBtn title="下移" onClick={e => { e.stopPropagation(); move(i, 1) }} disabled={disabled || i === fields.length - 1}>
                    <ArrowDown className="h-3 w-3" />
                  </IconBtn>
                  <IconBtn title="复制" onClick={e => { e.stopPropagation(); duplicateField(i) }} disabled={disabled}>
                    <Copy className="h-3 w-3" />
                  </IconBtn>
                  <IconBtn title="删除" onClick={e => { e.stopPropagation(); removeField(i) }} disabled={disabled} danger>
                    <Trash2 className="h-3 w-3" />
                  </IconBtn>
                </div>
              </div>
            ))}
          </div>
        </div>

        {/* 属性面板 */}
        <div className="rounded-md border border-input p-3 max-h-[26rem] overflow-auto">
          {!active ? (
            <p className="text-xs text-muted-foreground italic">选择左侧字段以编辑属性。</p>
          ) : (
            <FieldPropertyPanel
              field={active}
              sources={sources}
              referableFields={referableFields}
              disabled={disabled}
              onChange={changes => updateField(activeIdx, changes)}
            />
          )}
        </div>
      </div>

      {/* 实时预览 */}
      <div className="rounded-md border border-input p-3">
        <div className="flex items-center gap-1.5 mb-2 text-xs font-semibold">
          <Eye className="h-3.5 w-3.5" />
          实时预览
          <span className="font-normal text-muted-foreground">（可试填以验证条件显隐）</span>
        </div>
        <DynamicForm
          schema={schema}
          values={previewValues}
          onChange={(k, v) => setPreviewValues(p => ({ ...p, [k]: v }))}
          loadOptions={loadOptions}
        />
      </div>
    </div>
  )
}

function IconBtn({
  children,
  onClick,
  disabled,
  title,
  danger,
}: {
  children: React.ReactNode
  onClick: (e: React.MouseEvent) => void
  disabled?: boolean
  title: string
  danger?: boolean
}) {
  return (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={onClick}
      className={`p-0.5 rounded hover:bg-background disabled:opacity-30 disabled:cursor-not-allowed ${
        danger ? 'text-destructive' : 'text-muted-foreground'
      }`}
    >
      {children}
    </button>
  )
}

function FieldPropertyPanel({
  field,
  sources,
  referableFields,
  disabled,
  onChange,
}: {
  field: FormFieldDef
  sources: string[]
  referableFields: string[]
  disabled?: boolean
  onChange: (changes: Partial<FormFieldDef>) => void
}) {
  const supportsOptions = OPTION_TYPES.includes(field.type)
  const isTextual = ['text', 'textarea'].includes(field.type)
  const isCascader = field.type === 'cascader'

  // 旧版单条表达式在首次编辑时自动升级为规则列表，配置界面只暴露规则形态
  const rules: FormCondition[] =
    field.visible_when_rules && field.visible_when_rules.length > 0
      ? field.visible_when_rules
      : parseVisibleWhen(field.visible_when)

  const setRules = (next: FormCondition[]) => {
    onChange({ visible_when_rules: next, visible_when: '' })
  }

  return (
    <div className="space-y-3">
      <Row>
        <Labeled label="字段标识 key">
          <input
            className={inputCls}
            value={field.key}
            disabled={disabled}
            onChange={e => onChange({ key: e.target.value.trim() })}
            placeholder="英文标识，存入 custom_fields"
          />
        </Labeled>
        <Labeled label="显示名">
          <input className={inputCls} value={field.label} disabled={disabled} onChange={e => onChange({ label: e.target.value })} />
        </Labeled>
      </Row>

      <Row>
        <Labeled label="类型">
          <select
            className={inputCls}
            value={field.type}
            disabled={disabled}
            onChange={e => onChange({ type: e.target.value as FormFieldType })}
          >
            {FIELD_TYPES.map(t => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Labeled>
        <Labeled label="分区（可选）">
          <input
            className={inputCls}
            value={field.section || ''}
            disabled={disabled}
            onChange={e => onChange({ section: e.target.value })}
            placeholder="如：变更信息"
          />
        </Labeled>
      </Row>

      <Row>
        <Labeled label="占位提示">
          <input className={inputCls} value={field.placeholder || ''} disabled={disabled} onChange={e => onChange({ placeholder: e.target.value })} />
        </Labeled>
        <Labeled label="帮助说明">
          <input className={inputCls} value={field.help || ''} disabled={disabled} onChange={e => onChange({ help: e.target.value })} />
        </Labeled>
      </Row>

      <div className="flex items-center gap-4 text-xs">
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input type="checkbox" className="h-3.5 w-3.5" checked={!!field.required} disabled={disabled} onChange={e => onChange({ required: e.target.checked })} />
          必填
        </label>
        <label className="flex items-center gap-1.5 cursor-pointer">
          <input type="checkbox" className="h-3.5 w-3.5" checked={!!field.readonly} disabled={disabled} onChange={e => onChange({ readonly: e.target.checked })} />
          只读
        </label>
        <Labeled label="默认值" inline>
          <input
            className={`${inputCls} w-40`}
            value={field.default === undefined || field.default === null ? '' : String(field.default)}
            disabled={disabled}
            onChange={e => onChange({ default: e.target.value === '' ? undefined : e.target.value })}
          />
        </Labeled>
      </div>

      {supportsOptions && (
        <Section title="选项来源">
          <Labeled label="动态来源（留空则用下方静态选项）">
            <div className="flex gap-1.5">
              <select
                className={inputCls}
                value={sources.includes(field.source || '') ? field.source : field.source ? '__custom' : ''}
                disabled={disabled}
                onChange={e => {
                  const v = e.target.value
                  if (v === '__custom') return
                  onChange({ source: v || undefined })
                }}
              >
                <option value="">静态选项</option>
                {sources.map(s => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
                {field.source && !sources.includes(field.source) && <option value="__custom">自定义：{field.source}</option>}
              </select>
              <input
                className={`${inputCls} w-44`}
                value={field.source || ''}
                disabled={disabled}
                onChange={e => onChange({ source: e.target.value.trim() || undefined })}
                placeholder={isCascader ? 'cmdb:types' : 'cmdb:host / iam:user'}
              />
            </div>
          </Labeled>
          <Labeled label="引用数据字典（填字典 ID 后字段定义从字典展开，可跨表单复用）">
            <input
              className="flex h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs"
              value={field.dict_ref || ''}
              onChange={e => onChange({ dict_ref: e.target.value.trim() || undefined })}
              placeholder="dict_id（如变更通用字段字典）"
            />
          </Labeled>
          <Labeled label="可编辑角色（逗号分隔的角色名白名单，留空=所有人可编辑）">
            <input
              className="flex h-8 w-full rounded-md border border-input bg-background px-2.5 text-xs"
              value={(field.roles || []).join(',')}
              onChange={e =>
                onChange({
                  roles: e.target.value
                    .split(',')
                    .map(s => s.trim())
                    .filter(Boolean),
                })
              }
              placeholder="admin,ops"
            />
          </Labeled>
          {!field.source &&
            (isCascader ? (
              <TreeOptionsEditor
                options={field.options || []}
                disabled={disabled}
                onChange={next => onChange({ options: next })}
              />
            ) : (
              <StaticOptionsEditor field={field} disabled={disabled} onChange={onChange} />
            ))}
          {field.source && (
            <p className="text-[11px] text-muted-foreground">
              选项将在渲染时实时从 <code className="px-1 rounded bg-muted">{field.source}</code> 拉取；后端校验同样按该来源核对取值。
              {isCascader && (
                <>
                  {' '}
                  级联建议选 <code className="px-1 rounded bg-muted">cmdb:types</code>（CMDB 模型类型层级树），
                  非层级来源只会有一级。
                </>
              )}
            </p>
          )}
        </Section>
      )}

      {(isTextual || field.type === 'number') && (
        <Section title="校验规则">
          <Row>
            {field.type === 'number' ? (
              <>
                <Labeled label="最小值">
                  <input
                    type="number"
                    className={inputCls}
                    value={field.validation?.min ?? ''}
                    disabled={disabled}
                    onChange={e => onChange({ validation: { ...field.validation, min: e.target.value === '' ? undefined : Number(e.target.value) } })}
                  />
                </Labeled>
                <Labeled label="最大值">
                  <input
                    type="number"
                    className={inputCls}
                    value={field.validation?.max ?? ''}
                    disabled={disabled}
                    onChange={e => onChange({ validation: { ...field.validation, max: e.target.value === '' ? undefined : Number(e.target.value) } })}
                  />
                </Labeled>
              </>
            ) : (
              <>
                <Labeled label="最小长度">
                  <input
                    type="number"
                    className={inputCls}
                    value={field.validation?.min_len ?? ''}
                    disabled={disabled}
                    onChange={e => onChange({ validation: { ...field.validation, min_len: e.target.value === '' ? undefined : Number(e.target.value) } })}
                  />
                </Labeled>
                <Labeled label="最大长度">
                  <input
                    type="number"
                    className={inputCls}
                    value={field.validation?.max_len ?? ''}
                    disabled={disabled}
                    onChange={e => onChange({ validation: { ...field.validation, max_len: e.target.value === '' ? undefined : Number(e.target.value) } })}
                  />
                </Labeled>
              </>
            )}
          </Row>
          {isTextual && (
            <Labeled label="正则表达式">
              <input
                className={inputCls}
                value={field.validation?.regex || ''}
                disabled={disabled}
                onChange={e => onChange({ validation: { ...field.validation, regex: e.target.value || undefined } })}
                placeholder={String.raw`^\d+\.\d+\.\d+\.\d+$`}
              />
            </Labeled>
          )}
        </Section>
      )}

      <Section title="条件显隐">
        <ConditionEditor
          rules={rules}
          logic={field.visible_logic || 'and'}
          referableFields={referableFields}
          disabled={disabled}
          onRulesChange={setRules}
          onLogicChange={l => onChange({ visible_logic: l })}
        />
      </Section>
    </div>
  )
}

function StaticOptionsEditor({
  field,
  disabled,
  onChange,
}: {
  field: FormFieldDef
  disabled?: boolean
  onChange: (changes: Partial<FormFieldDef>) => void
}) {
  const opts = field.options || []
  const setOpts = (next: FormFieldOption[]) => onChange({ options: next })
  return (
    <div className="space-y-1.5">
      {opts.map((o, i) => (
        <div key={i} className="flex gap-1.5 items-center">
          <input
            className={inputCls}
            value={o.value}
            disabled={disabled}
            placeholder="value"
            onChange={e => setOpts(opts.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
          />
          <input
            className={inputCls}
            value={o.label}
            disabled={disabled}
            placeholder="显示文本"
            onChange={e => setOpts(opts.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))}
          />
          <IconBtn title="删除选项" danger disabled={disabled} onClick={() => setOpts(opts.filter((_, j) => j !== i))}>
            <Trash2 className="h-3.5 w-3.5" />
          </IconBtn>
        </div>
      ))}
      <Button
        size="sm"
        variant="outline"
        disabled={disabled}
        className="h-7 text-xs"
        onClick={() => setOpts([...opts, { value: '', label: '' }])}
      >
        <Plus className="h-3 w-3" />
        添加选项
      </Button>
    </div>
  )
}

/**
 * TreeOptionsEditor —— 级联静态层级选项编辑器。
 * 递归渲染 value/label 行，每行可「加子项」向下扩展一级，产出结构与后端 FieldOption.Children 对齐。
 */
function TreeOptionsEditor({
  options,
  disabled,
  onChange,
  depth = 0,
}: {
  options: FormFieldOption[]
  disabled?: boolean
  onChange: (next: FormFieldOption[]) => void
  depth?: number
}) {
  const patch = (i: number, changes: Partial<FormFieldOption>) =>
    onChange(options.map((x, j) => (j === i ? { ...x, ...changes } : x)))

  return (
    <div className={`space-y-1.5 ${depth > 0 ? 'pl-3 border-l border-dashed border-input' : ''}`}>
      {options.map((o, i) => (
        <div key={i} className="space-y-1.5">
          <div className="flex gap-1.5 items-center">
            <input
              className={inputCls}
              value={o.value}
              disabled={disabled}
              placeholder="value"
              onChange={e => patch(i, { value: e.target.value })}
            />
            <input
              className={inputCls}
              value={o.label}
              disabled={disabled}
              placeholder="显示文本"
              onChange={e => patch(i, { label: e.target.value })}
            />
            <IconBtn
              title="添加子级"
              disabled={disabled || depth >= 4}
              onClick={() => patch(i, { children: [...(o.children || []), { value: '', label: '' }] })}
            >
              <Plus className="h-3.5 w-3.5" />
            </IconBtn>
            <IconBtn title="删除该项及其子级" danger disabled={disabled} onClick={() => onChange(options.filter((_, j) => j !== i))}>
              <Trash2 className="h-3.5 w-3.5" />
            </IconBtn>
          </div>
          {o.children && o.children.length > 0 && (
            <TreeOptionsEditor
              options={o.children}
              disabled={disabled}
              depth={depth + 1}
              onChange={kids => patch(i, { children: kids.length > 0 ? kids : undefined })}
            />
          )}
        </div>
      ))}
      <Button
        size="sm"
        variant="outline"
        disabled={disabled}
        className="h-7 text-xs"
        onClick={() => onChange([...options, { value: '', label: '' }])}
      >
        <Plus className="h-3 w-3" />
        {depth === 0 ? '添加一级选项' : '添加同级'}
      </Button>
    </div>
  )
}

function ConditionEditor({
  rules,
  logic,
  referableFields,
  disabled,
  onRulesChange,
  onLogicChange,
}: {
  rules: FormCondition[]
  logic: 'and' | 'or'
  referableFields: string[]
  disabled?: boolean
  onRulesChange: (rules: FormCondition[]) => void
  onLogicChange: (logic: 'and' | 'or') => void
}) {
  const update = (i: number, changes: Partial<FormCondition>) =>
    onRulesChange(rules.map((r, j) => (j === i ? { ...r, ...changes } : r)))

  return (
    <div className="space-y-1.5">
      {rules.length === 0 && <p className="text-[11px] text-muted-foreground italic">未设置条件，该字段始终显示。</p>}
      {rules.length > 1 && (
        <div className="flex items-center gap-1.5 text-[11px]">
          <span className="text-muted-foreground">规则之间</span>
          <select
            className="h-7 rounded border border-input bg-background px-1.5 text-[11px]"
            value={logic}
            disabled={disabled}
            onChange={e => onLogicChange(e.target.value as 'and' | 'or')}
          >
            <option value="and">全部满足（AND）</option>
            <option value="or">任一满足（OR）</option>
          </select>
        </div>
      )}
      {rules.map((r, i) => {
        const opMeta = CONDITION_OPS.find(o => o.value === r.op) || CONDITION_OPS[0]
        return (
          <div key={i} className="flex gap-1.5 items-center">
            <input
              className={inputCls}
              list="form-builder-referable-fields"
              value={r.field}
              disabled={disabled}
              placeholder="依赖字段"
              onChange={e => update(i, { field: e.target.value.trim() })}
            />
            <select
              className={inputCls}
              value={r.op}
              disabled={disabled}
              onChange={e => update(i, { op: e.target.value as ConditionOp })}
            >
              {CONDITION_OPS.map(o => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <input
              className={inputCls}
              value={r.value || ''}
              disabled={disabled || !opMeta.needValue}
              placeholder={opMeta.needValue ? '比较值' : '无需取值'}
              onChange={e => update(i, { value: e.target.value })}
            />
            <IconBtn title="删除规则" danger disabled={disabled} onClick={() => onRulesChange(rules.filter((_, j) => j !== i))}>
              <Trash2 className="h-3.5 w-3.5" />
            </IconBtn>
          </div>
        )
      })}
      <datalist id="form-builder-referable-fields">
        {referableFields.map(f => (
          <option key={f} value={f} />
        ))}
      </datalist>
      <Button
        size="sm"
        variant="outline"
        disabled={disabled}
        className="h-7 text-xs"
        onClick={() => onRulesChange([...rules, { field: referableFields[0] || '', op: 'eq', value: '' }])}
      >
        <Plus className="h-3 w-3" />
        添加条件
      </Button>
    </div>
  )
}

function Row({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-2 gap-2">{children}</div>
}

function Labeled({ label, children, inline }: { label: string; children: React.ReactNode; inline?: boolean }) {
  if (inline) {
    return (
      <span className="flex items-center gap-1.5">
        <span className="text-muted-foreground whitespace-nowrap">{label}</span>
        {children}
      </span>
    )
  }
  return (
    <label className="block">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <div className="mt-0.5">{children}</div>
    </label>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="pt-2 border-t border-input space-y-2">
      <h5 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">{title}</h5>
      {children}
    </div>
  )
}
