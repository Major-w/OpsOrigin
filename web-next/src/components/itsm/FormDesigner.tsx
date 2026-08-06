'use client'

// FormDesigner —— 拖拽式表单设计器(ITSM 流程向导「表单设计」步骤)。
// 三栏: 左侧控件调色板(可拖拽) + 中间表单画布(放置/排序/选择) + 右侧属性面板。
// 复用 FormBuilder 的 FIELD_TYPES / createField / FieldPropertyPanel 与 DynamicForm 实时预览。
// 产出结构与后端 internal/itsm/form.FormSchema 对齐; 受控组件(value/onChange 均为 FormSchema)。

import { useEffect, useMemo, useState, type DragEvent } from 'react'
import { GripVertical, Plus, Trash2, ArrowUp, ArrowDown, Eye } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { FIELD_TYPES, createField, FieldPropertyPanel } from './FormBuilder'
import {
  DynamicForm,
  type FormSchema,
  type FormFieldDef,
  type FormFieldType,
} from './DynamicForm'

const MIME_TYPE = 'application/x-field-type' // 新增字段(来自调色板)
const MIME_IDX = 'application/x-field-index' // 已有字段重排(来自画布卡片)

interface FormDesignerProps {
  schema: FormSchema
  onChange: (schema: FormSchema) => void
  sources?: string[]
  systemFields?: string[]
  loadOptions?: (source: string) => Promise<{ value: string; label: string }[]>
  disabled?: boolean
}

export function FormDesigner({ schema, onChange, sources = [], systemFields = ['risk_level', 'status', 'created_by', 'process_id'], disabled }: FormDesignerProps) {
  const fields = schema.fields || []
  const [activeIdx, setActiveIdx] = useState(0)
  const [preview, setPreview] = useState<Record<string, unknown>>({})
  const [dragOver, setDragOver] = useState(false)

  useEffect(() => {
    if (activeIdx >= fields.length) setActiveIdx(Math.max(0, fields.length - 1))
  }, [fields.length, activeIdx])

  const active = fields[activeIdx]

  const patch = (next: FormFieldDef[]) => onChange({ ...schema, fields: next })
  const updateField = (idx: number, changes: Partial<FormFieldDef>) =>
    patch(fields.map((f, i) => (i === idx ? { ...f, ...changes } : f)))

  const addField = (type: FormFieldType, at?: number) => {
    const n = fields.length + 1
    const f = createField(type, n)
    const next = [...fields]
    const pos = at === undefined ? next.length : Math.max(0, Math.min(at, next.length))
    next.splice(pos, 0, f)
    patch(next.map((x, i) => ({ ...x, order: i + 1 })))
    setActiveIdx(pos)
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

  const referableFields = useMemo(
    () => [...systemFields, ...fields.filter((_, i) => i !== activeIdx).map(f => f.key)].filter(Boolean),
    [fields, activeIdx, systemFields],
  )

  // ---- drag handlers ----
  const onPaletteDragStart = (e: DragEvent, type: FormFieldType) => {
    e.dataTransfer.setData(MIME_TYPE, type)
    e.dataTransfer.effectAllowed = 'copy'
  }
  const onCardDragStart = (e: DragEvent, idx: number) => {
    e.dataTransfer.setData(MIME_IDX, String(idx))
    e.dataTransfer.effectAllowed = 'move'
  }
  const onCanvasDragOver = (e: DragEvent) => {
    if (disabled) return
    if (e.dataTransfer.types.includes(MIME_TYPE) || e.dataTransfer.types.includes(MIME_IDX)) {
      e.preventDefault()
      e.dataTransfer.dropEffect = e.dataTransfer.types.includes(MIME_IDX) ? 'move' : 'copy'
      setDragOver(true)
    }
  }
  const onCanvasDrop = (e: DragEvent) => {
    if (disabled) return
    e.preventDefault()
    setDragOver(false)
    const newType = e.dataTransfer.getData(MIME_TYPE)
    const moveIdxRaw = e.dataTransfer.getData(MIME_IDX)
    if (newType) {
      addField(newType as FormFieldType)
    } else if (moveIdxRaw !== '') {
      const from = Number(moveIdxRaw)
      const target = fields.length - 1 // 落到画布末尾
      if (from !== target) {
        const next = [...fields]
        const [item] = next.splice(from, 1)
        next.splice(target, 0, item)
        patch(next.map((f, i) => ({ ...f, order: i + 1 })))
        setActiveIdx(target)
      }
    }
  }
  const onCardDrop = (e: DragEvent, overIdx: number) => {
    if (disabled) return
    const moveIdxRaw = e.dataTransfer.getData(MIME_IDX)
    if (moveIdxRaw === '') return
    e.preventDefault()
    e.stopPropagation()
    const from = Number(moveIdxRaw)
    if (from === overIdx) return
    const next = [...fields]
    const [item] = next.splice(from, 1)
    next.splice(overIdx, 0, item)
    patch(next.map((f, i) => ({ ...f, order: i + 1 })))
    setActiveIdx(overIdx)
  }

  return (
    <div className="grid grid-cols-[170px_1fr_300px] gap-3 items-start">
      {/* 左: 控件调色板(可拖拽) */}
      <div className="rounded-md border border-input">
        <div className="px-2 py-1.5 border-b border-input text-xs font-semibold">表单控件</div>
        <div className="p-2 grid grid-cols-1 gap-1 max-h-[34rem] overflow-auto">
          {FIELD_TYPES.map(ft => (
            <div
              key={ft.value}
              draggable={!disabled}
              onDragStart={e => onPaletteDragStart(e, ft.value)}
              onClick={() => !disabled && addField(ft.value)}
              title="拖拽到中间, 或点击直接添加"
              className={`flex items-center gap-1.5 px-2 py-1.5 rounded-md border border-dashed text-xs cursor-grab active:cursor-grabbing hover:border-solid hover:bg-accent ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
            >
              <Plus className="h-3 w-3 shrink-0 text-muted-foreground" />
              <span className="truncate">{ft.label}</span>
            </div>
          ))}
        </div>
      </div>

      {/* 中: 表单画布 + 实时预览 */}
      <div className="space-y-3">
        <div
          onDragOver={onCanvasDragOver}
          onDragLeave={() => setDragOver(false)}
          onDrop={onCanvasDrop}
          className={`rounded-md border border-input p-3 min-h-[22rem] ${dragOver ? 'ring-2 ring-emerald-500/40 bg-emerald-500/5' : ''}`}
        >
          <div className="flex items-center justify-between mb-2">
            <span className="text-xs font-semibold">表单页面（{fields.length} 个字段）</span>
            <span className="text-[10px] text-muted-foreground">从左侧拖入控件 · 拖动卡片可重排</span>
          </div>
          {fields.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-12 text-center text-muted-foreground">
              <Plus className="h-8 w-8 opacity-30 mb-2" />
              <p className="text-xs">把左侧控件拖到这里, 或点击控件快速添加</p>
            </div>
          ) : (
            <div className="space-y-1.5">
              {fields.map((f, i) => (
                <div
                  key={`${f.key}-${i}`}
                  draggable={!disabled}
                  onDragStart={e => onCardDragStart(e, i)}
                  onDragOver={e => disabled ? undefined : e.preventDefault()}
                  onDrop={e => onCardDrop(e, i)}
                  onClick={() => setActiveIdx(i)}
                  className={`group flex items-center gap-2 px-2 py-2 rounded-md border cursor-pointer ${i === activeIdx ? 'border-primary bg-accent' : 'border-input hover:bg-accent/50'}`}
                >
                  <GripVertical className="h-3.5 w-3.5 shrink-0 text-muted-foreground cursor-grab" />
                  <div className="flex-1 min-w-0">
                    <div className="truncate text-xs font-medium">
                      {f.label || f.key}
                      {f.required && <span className="text-destructive ml-0.5">*</span>}
                    </div>
                    <div className="truncate text-[10px] text-muted-foreground">{f.key} · {FIELD_TYPES.find(t => t.value === f.type)?.label || f.type}</div>
                  </div>
                  <div className="hidden group-hover:flex items-center gap-0.5" onClick={e => e.stopPropagation()}>
                    <IconBtn title="上移" disabled={disabled || i === 0} onClick={() => move(i, -1)}><ArrowUp className="h-3 w-3" /></IconBtn>
                    <IconBtn title="下移" disabled={disabled || i === fields.length - 1} onClick={() => move(i, 1)}><ArrowDown className="h-3 w-3" /></IconBtn>
                    <IconBtn title="删除" danger disabled={disabled} onClick={() => removeField(i)}><Trash2 className="h-3 w-3" /></IconBtn>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* 实时预览 */}
        <div className="rounded-md border border-input p-3">
          <div className="flex items-center gap-1.5 mb-2 text-xs font-semibold">
            <Eye className="h-3.5 w-3.5" /> 实时预览
            <span className="font-normal text-muted-foreground">（试填以验证条件显隐）</span>
          </div>
          <DynamicForm schema={schema} values={preview} onChange={(k, v) => setPreview(p => ({ ...p, [k]: v }))} loadOptions={undefined} />
        </div>
      </div>

      {/* 右: 属性面板 */}
      <div className="rounded-md border border-input p-3 max-h-[34rem] overflow-auto">
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
  )
}

function IconBtn({ children, onClick, disabled, title, danger }: {
  children: React.ReactNode
  onClick: () => void
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
      className={`p-0.5 rounded hover:bg-background disabled:opacity-30 disabled:cursor-not-allowed ${danger ? 'text-destructive' : 'text-muted-foreground'}`}
    >
      {children}
    </button>
  )
}
