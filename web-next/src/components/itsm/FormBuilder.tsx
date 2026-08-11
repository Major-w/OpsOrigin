'use client'

// FormBuilder —— ITSM 表单 Schema 可视化配置器。
// 左侧字段列表（增删改 + 排序），右侧属性面板（基础属性 / 选项来源 / 校验 / 条件显隐），
// 底部实时预览用真实 DynamicForm 渲染，所见即所得。
// 与后端 internal/itsm/form 的 FormSchema 完全对齐，产出结构可直接 PUT /itsm/forms。

import { useEffect, useMemo, useState } from 'react'
import { ArrowDown, ArrowUp, Copy, Eye, Lock, Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { apiList } from '@/lib/utils'
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
  normalizeColSpan,
  parseVisibleWhen,
} from './DynamicForm'

export const FIELD_TYPES: { value: FormFieldType; label: string }[] = [
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

export const OPTION_TYPES: FormFieldType[] = ['select', 'multiselect', 'user', 'ci', 'cascader']

/** 常用宽度档位：整行 / 半行 / 三分之一 / 四分之一 */
export const COL_SPAN_PRESETS: { span: number; label: string }[] = [
  { span: 12, label: '整行' },
  { span: 6, label: '1/2' },
  { span: 4, label: '1/3' },
  { span: 3, label: '1/4' },
]

/** 附件常用配置一键套用 */
const ATTACHMENT_PRESETS: { label: string; patch: Partial<FormFieldDef> }[] = [
  { label: '图片(≤5MB×9)', patch: { max_size: 5, accept: 'image/*', max_count: 9, multiple: true } },
  { label: '文档(≤20MB×5)', patch: { max_size: 20, accept: '.pdf,.doc,.docx,.xls,.xlsx,.ppt,.pptx', max_count: 5, multiple: true } },
  { label: '单个≤10MB', patch: { max_size: 10, accept: undefined, max_count: 1, multiple: false } },
]

/**
 * 默认宽度档位：长文本/富文本/附件/表格天然占整行(12)，其余控件默认半行(6)。
 * 这样从调色板拖出两个普通控件时，会自然落在同一行，满足「一行放多个控件」。
 */
export function defaultColSpan(type: FormFieldType): number {
  const wide = new Set<FormFieldType>(['textarea', 'richtext', 'attachment', 'table'])
  return wide.has(type) ? 12 : 6
}

/** 附件「允许的类型」勾选项：每项对应一组扩展名/MIME，落库仍是逗号分隔的 accept 串 */
const ACCEPT_GROUPS: { label: string; tokens: string[] }[] = [
  { label: '图片', tokens: ['image/*'] },
  { label: 'PDF', tokens: ['.pdf'] },
  { label: 'Word', tokens: ['.doc', '.docx'] },
  { label: 'Excel', tokens: ['.xls', '.xlsx'] },
  { label: 'PPT', tokens: ['.ppt', '.pptx'] },
  { label: '文本/日志', tokens: ['.txt', '.log', '.md'] },
  { label: '压缩包', tokens: ['.zip', '.rar', '.7z', '.tar', '.gz'] },
  { label: '视频', tokens: ['video/*'] },
  { label: '音频', tokens: ['audio/*'] },
]

/** 「选人」控件的候选人来源前缀：全部用户 iam:user，限定组 iam:user@<组ID>[,<组ID>] */
const USER_SOURCE_ALL = 'iam:user'

/** 从 source 解析出选人控件限定的用户组 ID 列表（非组限定形态返回空数组） */
export function parseUserGroups(source?: string): string[] {
  const s = (source || '').trim()
  if (!s.startsWith(`${USER_SOURCE_ALL}@`)) return []
  return s
    .slice(USER_SOURCE_ALL.length + 1)
    .split(',')
    .map(x => x.trim())
    .filter(Boolean)
}

/** 用户组 ID 列表 → source 串（空列表回落为全部用户） */
function buildUserSource(groupIDs: string[]): string {
  const ids = groupIDs.map(x => x.trim()).filter(Boolean)
  return ids.length ? `${USER_SOURCE_ALL}@${ids.join(',')}` : USER_SOURCE_ALL
}

/**
 * 字段标识：控件类型前缀 + 随机短串。
 * 设计器里对用户只读——标识一旦被工单数据引用就不能再改，随机化同时规避了
 * 「删掉再拖一个同名字段」导致新字段读到历史脏数据的问题。
 */
export function fieldKey(type: FormFieldType): string {
  const rand = Math.random().toString(36).slice(2, 8)
  return `${type}_${rand}`
}

/** 字段工厂：按类型生成带唯一 key 的 FormFieldDef，供拖拽表单设计器新增字段复用。 */
export function createField(type: FormFieldType, n: number): FormFieldDef {
  const typeLabel = FIELD_TYPES.find(t => t.value === type)?.label || '字段'
  const field: FormFieldDef = {
    key: fieldKey(type),
    label: `${typeLabel} ${n}`,
    type,
    required: false,
    order: n,
    col_span: defaultColSpan(type),
  }
  // 选人控件默认可选全部用户，免去每次手填来源
  if (type === 'user') field.source = USER_SOURCE_ALL
  return field
}

const inputCls =
  'flex h-9 w-full rounded-md border border-input bg-background px-2 text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

interface DictRef {
  id: string
  key?: string
  label?: string
}
interface GroupRef {
  id: string
  name?: string
}

// 字典与用户组是全局字典型数据，切换字段时不该反复打后端 —— 模块级缓存 + 单飞请求。
let dictCache: DictRef[] | null = null
let groupCache: GroupRef[] | null = null

function useRemoteRefs<T>(cacheRead: () => T[] | null, cacheWrite: (v: T[]) => void, path: string): T[] {
  const [items, setItems] = useState<T[]>(() => cacheRead() || [])
  useEffect(() => {
    if (cacheRead()) return
    let alive = true
    apiList<T>(path)
      .then(list => {
        const v = list || []
        cacheWrite(v)
        if (alive) setItems(v)
      })
      // 字典/用户组服务不可用时降级为空列表，不阻断表单配置本身
      .catch(() => undefined)
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [path])
  return items
}

const useFormDicts = () =>
  useRemoteRefs<DictRef>(
    () => dictCache,
    v => (dictCache = v),
    '/itsm/form-dicts',
  )

const useUserGroups = () =>
  useRemoteRefs<GroupRef>(
    () => groupCache,
    v => (groupCache = v),
    '/iam/groups',
  )

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
    patch([...fields, { ...createField('text', n), col_span: undefined }])
    setActiveIdx(fields.length)
  }

  const duplicateField = (idx: number) => {
    const src = fields[idx]
    // 副本必须换新标识：同 key 会让两个字段写同一份数据
    const copy: FormFieldDef = { ...src, key: fieldKey(src.type), label: `${src.label} 副本`, order: fields.length + 1 }
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

export function FieldPropertyPanel({
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
  const isUser = field.type === 'user'
  // 选人控件有专属的候选范围配置，不再暴露通用的 source/静态选项编辑
  const supportsOptions = OPTION_TYPES.includes(field.type) && !isUser
  const isTextual = ['text', 'textarea'].includes(field.type)
  const isCascader = field.type === 'cascader'
  const typeLabel = FIELD_TYPES.find(t => t.value === field.type)?.label || field.type

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
      {field.builtin && (
        <div className="flex items-center gap-2 rounded-md border border-sky-500/30 bg-sky-500/10 px-2.5 py-1.5 text-xs text-sky-600 dark:text-sky-400">
          <span className="rounded bg-sky-500/20 px-1 text-[10px] font-medium">内置</span>
          系统内置字段（工单一等列）：标识与类型锁定，仅可调整显示名 / 必填 / 宽度 / 分区。
        </div>
      )}
      <Row>
        <Labeled label="字段标识 key">
          <div
            className="flex h-9 w-full items-center gap-1.5 rounded-md border border-dashed border-input bg-muted/40 px-2 text-xs text-muted-foreground"
            title="标识由控件类型自动生成且不可修改：工单数据以它为准落库，改名会让历史数据对不上"
          >
            <Lock className="h-3 w-3 shrink-0 opacity-60" />
            <code className="truncate">{field.key}</code>
          </div>
        </Labeled>
        <Labeled label="显示名">
          <input className={inputCls} value={field.label} disabled={disabled} onChange={e => onChange({ label: e.target.value })} />
        </Labeled>
      </Row>

      <Row>
        <Labeled label="类型">
          <div
            className="flex h-9 w-full items-center gap-1.5 rounded-md border border-dashed border-input bg-muted/40 px-2 text-xs text-muted-foreground"
            title="类型在拖入画布时确定，不能改：换类型请删除本控件后重新拖入目标类型"
          >
            <Lock className="h-3 w-3 shrink-0 opacity-60" />
            <span className="truncate">{typeLabel}</span>
          </div>
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
      <p className="-mt-1 text-[11px] text-muted-foreground">
        标识与类型创建后锁定。需要换控件类型时，删除本字段后从左侧重新拖入。
      </p>

      <Labeled label={`宽度占比 ${normalizeColSpan(field.col_span)}/12 栅格`}>
        <div className="flex items-center gap-2">
          <input
            type="range"
            min={1}
            max={12}
            step={1}
            className="h-1.5 flex-1 cursor-pointer accent-emerald-500"
            value={normalizeColSpan(field.col_span)}
            disabled={disabled}
            onChange={e => onChange({ col_span: Number(e.target.value) })}
          />
          <div className="flex shrink-0 gap-1">
            {COL_SPAN_PRESETS.map(p => (
              <button
                key={p.span}
                type="button"
                disabled={disabled}
                onClick={() => onChange({ col_span: p.span })}
                className={`h-6 rounded border px-1.5 text-[11px] transition-colors disabled:opacity-50 ${
                  normalizeColSpan(field.col_span) === p.span
                    ? 'border-emerald-500 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                    : 'border-input text-muted-foreground hover:bg-accent'
                }`}
              >
                {p.label}
              </button>
            ))}
          </div>
        </div>
      </Labeled>

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

      {supportsOptions && !field.builtin && (
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
          <DictRefPicker value={field.dict_ref} disabled={disabled} onChange={v => onChange({ dict_ref: v })} />
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

      {isUser && (
        <Section title="候选人范围">
          <UserScopePicker
            source={field.source}
            disabled={disabled}
            onChange={src => onChange({ source: src })}
          />
          <DictRefPicker value={field.dict_ref} disabled={disabled} onChange={v => onChange({ dict_ref: v })} />
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

      {field.type === 'attachment' && (
        <Section title="附件设置">
          <Labeled label="单文件最大体积（MB，0=不限）">
            <input
              type="number"
              min={0}
              className={inputCls}
              value={field.max_size ?? ''}
              disabled={disabled}
              onChange={e => onChange({ max_size: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value)) })}
              placeholder="如 10"
            />
          </Labeled>
          <Labeled label="最多文件数（0=不限）">
            <input
              type="number"
              min={0}
              className={inputCls}
              value={field.max_count ?? ''}
              disabled={disabled || field.multiple === false}
              onChange={e => onChange({ max_count: e.target.value === '' ? undefined : Math.max(0, Number(e.target.value)) })}
              placeholder="如 5"
            />
          </Labeled>
          <AcceptPicker value={field.accept} disabled={disabled} onChange={v => onChange({ accept: v })} />
          <div className="flex flex-wrap items-center gap-4 text-xs">
            <label className="flex cursor-pointer items-center gap-1.5">
              <input
                type="checkbox"
                className="h-3.5 w-3.5"
                checked={field.multiple !== false}
                disabled={disabled}
                onChange={e => onChange({ multiple: e.target.checked })}
              />
              允许上传多个文件
            </label>
            {ATTACHMENT_PRESETS.map(p => (
              <button
                key={p.label}
                type="button"
                disabled={disabled}
                onClick={() => onChange(p.patch)}
                className="h-6 rounded border border-input px-2 text-[11px] text-muted-foreground hover:bg-accent disabled:opacity-50"
              >
                {p.label}
              </button>
            ))}
          </div>
          <p className="text-[11px] text-muted-foreground">
            体积与类型在选择文件时前端即时拦截；数量上限提交时后端会再校验一次，防止绕过界面直接提交。
          </p>
        </Section>
      )}

      {field.type === 'richtext' && (
        <Section title="富文本设置">
          <p className="text-[11px] text-muted-foreground">
            使用所见即所得编辑器（标题 / 加粗 / 列表 / 任务项 / 链接 / 代码块 / 引用），
            内容以 <code className="rounded bg-muted px-1">Markdown</code> 存储，与知识库、工单描述的渲染管线一致，
            天然规避富文本 HTML 注入风险。
          </p>
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

/**
 * DictRefPicker —— 引用数据字典。
 * 字典是全局定义的复用型字段集合，让人手抄 ID 既易错又不可发现，这里直接列出可选字典。
 */
function DictRefPicker({
  value,
  disabled,
  onChange,
}: {
  value?: string
  disabled?: boolean
  onChange: (v: string | undefined) => void
}) {
  const dicts = useFormDicts()
  const current = value || ''
  // 引用的字典已被删除时仍要能看见原值，否则用户无从察觉引用已失效
  const dangling = current && !dicts.some(d => d.id === current)
  return (
    <Labeled label="引用数据字典（选中后字段定义从字典展开，可跨表单复用）">
      <select
        className={inputCls}
        value={current}
        disabled={disabled}
        onChange={e => onChange(e.target.value || undefined)}
      >
        <option value="">不引用</option>
        {dicts.map(d => (
          <option key={d.id} value={d.id}>
            {d.label || d.key || d.id}
            {d.key ? `（${d.key}）` : ''}
          </option>
        ))}
        {dangling && <option value={current}>已失效：{current}</option>}
      </select>
      {dicts.length === 0 && (
        <p className="mt-1 text-[11px] text-muted-foreground">
          暂无可用字典，先到「ITSM · 数据字典」新建后再回来引用。
        </p>
      )}
    </Labeled>
  )
}

/**
 * UserScopePicker —— 「选人」控件的候选人范围。
 * 全部用户 → source=iam:user；限定用户组 → source=iam:user@<组ID>,<组ID>，
 * 后端 source_resolver 按组成员并集过滤候选人，不需要额外字段落库。
 */
function UserScopePicker({
  source,
  disabled,
  onChange,
}: {
  source?: string
  disabled?: boolean
  onChange: (source: string) => void
}) {
  const groups = useUserGroups()
  const picked = parseUserGroups(source)
  const byGroup = picked.length > 0

  const toggle = (id: string) => {
    const next = picked.includes(id) ? picked.filter(x => x !== id) : [...picked, id]
    onChange(buildUserSource(next))
  }

  return (
    <div className="space-y-2">
      <div className="flex gap-4 text-xs">
        {[
          { key: 'all', label: '全部用户' },
          { key: 'group', label: '限定用户组' },
        ].map(m => (
          <label key={m.key} className="flex cursor-pointer items-center gap-1.5">
            <input
              type="radio"
              className="h-3.5 w-3.5"
              checked={m.key === 'group' ? byGroup : !byGroup}
              disabled={disabled}
              // 从「限定组」切回「全部」会丢掉已选组，这是符合直觉的：范围本就互斥
              onChange={() => onChange(m.key === 'group' ? buildUserSource(picked) : USER_SOURCE_ALL)}
            />
            {m.label}
          </label>
        ))}
      </div>

      {byGroup || groups.length > 0 ? (
        <div className="max-h-36 space-y-1 overflow-y-auto rounded-md border border-input p-2">
          {groups.length === 0 && <p className="text-[11px] text-muted-foreground">暂无用户组，可到「用户与权限 · 用户组」创建。</p>}
          {groups.map(g => (
            <label key={g.id} className="flex cursor-pointer items-center gap-1.5 text-xs">
              <input
                type="checkbox"
                className="h-3.5 w-3.5"
                checked={picked.includes(g.id)}
                disabled={disabled}
                onChange={() => toggle(g.id)}
              />
              {g.name || g.id}
            </label>
          ))}
        </div>
      ) : null}

      <p className="text-[11px] text-muted-foreground">
        {byGroup
          ? `候选人限定在已勾选的 ${picked.length} 个用户组成员内（取并集）。`
          : '候选人为平台全部用户；勾选用户组后自动收窄到组成员。'}
      </p>
    </div>
  )
}

/**
 * AcceptPicker —— 附件允许的类型。
 * 常见类型走勾选，另留一个自定义补充框兜住业务特有后缀（如 .apk / .ipa）；
 * 落库仍是逗号分隔的 accept 串，DynamicForm 与后端校验都无需改动。
 */
function AcceptPicker({
  value,
  disabled,
  onChange,
}: {
  value?: string
  disabled?: boolean
  onChange: (v: string | undefined) => void
}) {
  const tokens = (value || '')
    .split(',')
    .map(s => s.trim())
    .filter(Boolean)
  const known = new Set(ACCEPT_GROUPS.flatMap(g => g.tokens))
  const extras = tokens.filter(t => !known.has(t))

  const commit = (next: string[]) => {
    const uniq = Array.from(new Set(next.filter(Boolean)))
    onChange(uniq.length ? uniq.join(',') : undefined)
  }

  const toggleGroup = (g: (typeof ACCEPT_GROUPS)[number]) => {
    const on = g.tokens.every(t => tokens.includes(t))
    commit(on ? tokens.filter(t => !g.tokens.includes(t)) : [...tokens, ...g.tokens])
  }

  return (
    <div className="space-y-2">
      <Labeled label="允许的类型（不勾选=不限制）">
        <div className="grid grid-cols-3 gap-x-3 gap-y-1.5">
          {ACCEPT_GROUPS.map(g => (
            <label key={g.label} className="flex cursor-pointer items-center gap-1.5 text-xs">
              <input
                type="checkbox"
                className="h-3.5 w-3.5"
                checked={g.tokens.every(t => tokens.includes(t))}
                disabled={disabled}
                onChange={() => toggleGroup(g)}
              />
              {g.label}
            </label>
          ))}
        </div>
      </Labeled>
      <Labeled label="其他类型（逗号分隔，如 .apk,.ipa）">
        <input
          className={inputCls}
          value={extras.join(',')}
          disabled={disabled}
          onChange={e => {
            const custom = e.target.value
              .split(',')
              .map(s => s.trim())
              .filter(Boolean)
            commit([...tokens.filter(t => known.has(t)), ...custom])
          }}
          placeholder=".apk,.ipa"
        />
      </Labeled>
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
