'use client'

// FormDesigner —— 拖拽式表单设计器(ITSM 流程向导「表单设计」步骤)。
// 三栏: 左侧控件调色板(可拖拽) + 中间 12 栅格画布 + 右侧属性面板。
//
// 画布能力:
//  1) 12 栅格布局: 字段卡片按 col_span 占列, 拖右缘手柄可实时调宽并吸附栅格;
//  2) 分区是一等公民: 可从调色板拖入分区块, 支持命名/折叠/排序, 字段可在分区间拖拽;
//  3) 与后端契约不变: 画布内部的「分区块」序列化回扁平 fields(写 section 标签 + order),
//     渲染端 DynamicForm 按同样的分组规则还原, 后端 FormSchema 无需感知块结构。
//
// 复用 FormBuilder 的 FIELD_TYPES / createField / FieldPropertyPanel 与 DynamicForm 实时预览。

import { useEffect, useMemo, useRef, useState, type DragEvent, type PointerEvent as ReactPointerEvent } from 'react'
import {
  ArrowDown, ArrowUp, ChevronDown, ChevronRight, Eye, GripVertical, Plus, SquareStack, Trash2,
} from 'lucide-react'
import { FIELD_TYPES, createField, FieldPropertyPanel } from './FormBuilder'
import {
  DynamicForm,
  normalizeColSpan,
  type FormSchema,
  type FormFieldDef,
  type FormFieldType,
} from './DynamicForm'

const MIME_TYPE = 'application/x-field-type' // 调色板 → 新增字段
const MIME_FIELD = 'application/x-field-key' // 画布字段卡片重排/跨分区搬运
const MIME_SECTION = 'application/x-new-section' // 调色板 → 新增分区
const MIME_BLOCK = 'application/x-block-title' // 分区块整体排序

const GRID_GAP = 8 // 与画布 gap-2 (0.5rem) 保持一致, 用于换算栅格列宽

/** 栅格列数 → 类名静态映射(Tailwind 编译期扫描, 不能动态拼) */
const SPAN_CLS: Record<number, string> = {
  1: 'col-span-1', 2: 'col-span-2', 3: 'col-span-3', 4: 'col-span-4',
  5: 'col-span-5', 6: 'col-span-6', 7: 'col-span-7', 8: 'col-span-8',
  9: 'col-span-9', 10: 'col-span-10', 11: 'col-span-11', 12: 'col-span-12',
}

/** 画布内部的分区块视图模型（不落库，落库时 flatten 回扁平 fields） */
interface Block {
  title: string
  items: FormFieldDef[]
}

interface FormDesignerProps {
  schema: FormSchema
  onChange: (schema: FormSchema) => void
  sources?: string[]
  systemFields?: string[]
  loadOptions?: (source: string) => Promise<{ value: string; label: string }[]>
  disabled?: boolean
}

export function FormDesigner({
  schema,
  onChange,
  sources = [],
  systemFields = ['risk_level', 'status', 'created_by', 'process_id'],
  disabled,
}: FormDesignerProps) {
  const fields = useMemo(() => schema.fields || [], [schema.fields])

  // 选中态用 key 而非下标：跨分区搬运/排序后下标会变，key 稳定
  const [activeKey, setActiveKey] = useState('')
  const [preview, setPreview] = useState<Record<string, unknown>>({})
  const [dragOver, setDragOver] = useState(false)
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  // 还没有字段的空分区：flatten 时无处安放，暂存在本地，落字段后自动并入
  const [emptySections, setEmptySections] = useState<string[]>([])
  // 分区顺序（含空分区），保证块排序稳定且可被 flatten 持久化
  const [sectionOrder, setSectionOrder] = useState<string[]>(() => deriveSections(fields))

  const sectionsKey = fields.map(f => f.section || '').join('\u0001')
  useEffect(() => {
    const fromFields = deriveSections(fields)
    setSectionOrder(prev => {
      const known = new Set([...fromFields, ...emptySections])
      const kept = prev.filter(t => known.has(t))
      const merged = [...kept, ...fromFields.filter(t => !kept.includes(t)), ...emptySections.filter(t => !kept.includes(t))]
      return merged.join('\u0001') === prev.join('\u0001') ? prev : merged
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sectionsKey, emptySections])

  const blocks: Block[] = useMemo(() => {
    const grouped = new Map<string, FormFieldDef[]>()
    for (const f of fields) {
      const s = f.section || ''
      if (!grouped.has(s)) grouped.set(s, [])
      grouped.get(s)!.push(f)
    }
    const titles = sectionOrder.filter(t => grouped.has(t) || emptySections.includes(t))
    for (const t of grouped.keys()) if (!titles.includes(t)) titles.push(t)
    if (titles.length === 0) titles.push('')
    return titles.map(t => ({ title: t, items: grouped.get(t) || [] }))
  }, [fields, sectionOrder, emptySections])

  const active = fields.find(f => f.key === activeKey)

  const referableFields = useMemo(
    () => [...systemFields, ...fields.filter(f => f.key !== activeKey).map(f => f.key)].filter(Boolean),
    [fields, activeKey, systemFields],
  )

  /** 把块结构拍平回 FormSchema.fields：写回 section 标签并重排 order */
  const commit = (next: Block[]) => {
    const flat: FormFieldDef[] = []
    next.forEach(b => b.items.forEach(f => {
      const copy: FormFieldDef = { ...f, order: flat.length + 1 }
      if (b.title) copy.section = b.title
      else delete copy.section
      flat.push(copy)
    }))
    setSectionOrder(next.map(b => b.title))
    setEmptySections(next.filter(b => b.items.length === 0).map(b => b.title))
    onChange({ ...schema, fields: flat })
  }

  const updateActive = (changes: Partial<FormFieldDef>) => {
    if (!active) return
    const nextKey = changes.key !== undefined ? changes.key : active.key
    commit(blocks.map(b => ({ ...b, items: b.items.map(f => (f.key === active.key ? { ...f, ...changes } : f)) })))
    if (nextKey !== active.key) setActiveKey(nextKey)
  }

  const updateFieldByKey = (key: string, changes: Partial<FormFieldDef>) => {
    commit(blocks.map(b => ({ ...b, items: b.items.map(f => (f.key === key ? { ...f, ...changes } : f)) })))
  }

  const removeField = (key: string) => {
    commit(blocks.map(b => ({ ...b, items: b.items.filter(f => f.key !== key) })))
    if (activeKey === key) setActiveKey('')
  }

  /** 新增字段：落到指定分区的 beforeKey 之前（beforeKey 为空则追加到该分区末尾） */
  const addFieldTo = (type: FormFieldType, section: string, beforeKey?: string) => {
    const f = createField(type, nextSeq(fields))
    const next = blocks.map(b => {
      if (b.title !== section) return b
      const items = [...b.items]
      const at = beforeKey ? items.findIndex(x => x.key === beforeKey) : -1
      if (at >= 0) items.splice(at, 0, f)
      else items.push(f)
      return { ...b, items }
    })
    commit(next)
    setActiveKey(f.key)
  }

  /** 搬运字段：可跨分区，落到 beforeKey 之前（为空则该分区末尾） */
  const moveFieldTo = (key: string, section: string, beforeKey?: string) => {
    if (key === beforeKey) return
    const moving = fields.find(f => f.key === key)
    if (!moving) return
    const stripped = blocks.map(b => ({ ...b, items: b.items.filter(f => f.key !== key) }))
    const next = stripped.map(b => {
      if (b.title !== section) return b
      const items = [...b.items]
      const at = beforeKey ? items.findIndex(x => x.key === beforeKey) : -1
      if (at >= 0) items.splice(at, 0, moving)
      else items.push(moving)
      return { ...b, items }
    })
    commit(next)
    setActiveKey(key)
  }

  // ---- 分区块操作 ----
  const addSection = () => {
    const title = uniqueTitle('分区', blocks.map(b => b.title))
    commit([...blocks, { title, items: [] }])
  }

  const renameSection = (from: string, to: string) => {
    if (from === to) return
    if (blocks.some(b => b.title === to)) return // 重名会导致分区合并，直接忽略
    commit(blocks.map(b => (b.title === from ? { ...b, title: to } : b)))
    setCollapsed(prev => ({ ...prev, [to]: prev[from] || false }))
  }

  const removeSection = (title: string) => {
    const b = blocks.find(x => x.title === title)
    if (!b) return
    if (b.items.length > 0 && !window.confirm(`删除分区「${title || '默认区'}」将同时删除其中 ${b.items.length} 个字段，确定继续？`)) return
    const rest = blocks.filter(x => x.title !== title)
    commit(rest.length > 0 ? rest : [{ title: '', items: [] }])
  }

  const moveBlock = (title: string, delta: number) => {
    const idx = blocks.findIndex(b => b.title === title)
    const target = idx + delta
    if (idx < 0 || target < 0 || target >= blocks.length) return
    const next = [...blocks]
    ;[next[idx], next[target]] = [next[target], next[idx]]
    commit(next)
  }

  const moveBlockBefore = (from: string, before: string) => {
    if (from === before) return
    const next = blocks.filter(b => b.title !== from)
    const moving = blocks.find(b => b.title === from)
    if (!moving) return
    const at = next.findIndex(b => b.title === before)
    next.splice(at < 0 ? next.length : at, 0, moving)
    commit(next)
  }

  // ---- 栅格拖拽调宽 ----
  const gridRefs = useRef<Record<string, HTMLDivElement | null>>({})
  const [resizing, setResizing] = useState<{ key: string; startX: number; startSpan: number; step: number } | null>(null)

  const beginResize = (e: ReactPointerEvent, key: string, section: string, span: number) => {
    if (disabled) return
    e.preventDefault()
    e.stopPropagation()
    const grid = gridRefs.current[section]
    if (!grid) return
    const colW = (grid.clientWidth - GRID_GAP * 11) / 12
    setResizing({ key, startX: e.clientX, startSpan: span, step: colW + GRID_GAP })
  }

  useEffect(() => {
    if (!resizing) return
    const onMove = (ev: globalThis.PointerEvent) => {
      const delta = Math.round((ev.clientX - resizing.startX) / Math.max(1, resizing.step))
      const span = Math.max(1, Math.min(12, resizing.startSpan + delta))
      const cur = fields.find(f => f.key === resizing.key)
      if (cur && normalizeColSpan(cur.col_span) !== span) updateFieldByKey(resizing.key, { col_span: span })
    }
    const stop = () => setResizing(null)
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', stop)
    window.addEventListener('pointercancel', stop)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', stop)
      window.removeEventListener('pointercancel', stop)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resizing, fields])

  // ---- HTML5 拖拽 ----
  const dragTypes = (e: DragEvent) => Array.from(e.dataTransfer.types)
  const allowDrop = (e: DragEvent, accepts: string[]) => {
    if (disabled) return false
    const types = dragTypes(e)
    if (!accepts.some(t => types.includes(t))) return false
    e.preventDefault()
    e.stopPropagation()
    return true
  }

  const onDropInBlock = (e: DragEvent, section: string, beforeKey?: string) => {
    if (!allowDrop(e, [MIME_TYPE, MIME_FIELD])) return
    setDragOver(false)
    const newType = e.dataTransfer.getData(MIME_TYPE)
    const movingKey = e.dataTransfer.getData(MIME_FIELD)
    if (newType) addFieldTo(newType as FormFieldType, section, beforeKey)
    else if (movingKey) moveFieldTo(movingKey, section, beforeKey)
  }

  const onCanvasDrop = (e: DragEvent) => {
    if (disabled) return
    setDragOver(false)
    const types = dragTypes(e)
    if (types.includes(MIME_SECTION)) {
      e.preventDefault()
      addSection()
      return
    }
    // 落在画布空白：并入最后一个分区
    const last = blocks[blocks.length - 1]
    if (!last) return
    onDropInBlock(e, last.title)
  }

  const fieldCount = fields.length

  return (
    <div className="grid grid-cols-[176px_1fr_310px] items-start gap-3">
      {/* 左: 控件调色板 */}
      <div className="rounded-md border border-input">
        <div className="border-b border-input px-2 py-1.5 text-xs font-semibold">布局</div>
        <div className="p-2">
          <div
            draggable={!disabled}
            onDragStart={e => {
              e.dataTransfer.setData(MIME_SECTION, '1')
              e.dataTransfer.effectAllowed = 'copy'
            }}
            onClick={() => !disabled && addSection()}
            title="拖到画布空白处新建分区，或点击直接添加"
            className={`flex items-center gap-1.5 rounded-md border border-dashed border-emerald-500/50 bg-emerald-500/5 px-2 py-1.5 text-xs text-emerald-600 dark:text-emerald-400 ${
              disabled ? 'cursor-not-allowed opacity-50' : 'cursor-grab active:cursor-grabbing hover:border-solid'
            }`}
          >
            <SquareStack className="h-3 w-3 shrink-0" />
            <span className="truncate">分区</span>
          </div>
        </div>
        <div className="border-y border-input px-2 py-1.5 text-xs font-semibold">表单控件</div>
        <div className="grid max-h-[30rem] grid-cols-1 gap-1 overflow-auto p-2">
          {FIELD_TYPES.map(ft => (
            <div
              key={ft.value}
              draggable={!disabled}
              onDragStart={e => {
                e.dataTransfer.setData(MIME_TYPE, ft.value)
                e.dataTransfer.effectAllowed = 'copy'
              }}
              onClick={() => !disabled && addFieldTo(ft.value, blocks[blocks.length - 1]?.title ?? '')}
              title="拖到分区内，或点击追加到最后一个分区"
              className={`flex items-center gap-1.5 rounded-md border border-dashed px-2 py-1.5 text-xs hover:border-solid hover:bg-accent ${
                disabled ? 'cursor-not-allowed opacity-50' : 'cursor-grab active:cursor-grabbing'
              }`}
            >
              <Plus className="h-3 w-3 shrink-0 text-muted-foreground" />
              <span className="truncate">{ft.label}</span>
            </div>
          ))}
        </div>
      </div>

      {/* 中: 12 栅格画布 + 实时预览 */}
      <div className="space-y-3">
        <div
          onDragOver={e => {
            if (allowDrop(e, [MIME_TYPE, MIME_FIELD, MIME_SECTION])) setDragOver(true)
          }}
          onDragLeave={() => setDragOver(false)}
          onDrop={onCanvasDrop}
          className={`min-h-[22rem] space-y-2 rounded-md border border-input p-3 ${dragOver ? 'bg-emerald-500/5 ring-2 ring-emerald-500/40' : ''}`}
        >
          <div className="mb-1 flex items-center justify-between">
            <span className="text-xs font-semibold">
              表单页面（{blocks.length} 个分区 · {fieldCount} 个字段）
            </span>
            <span className="text-[10px] text-muted-foreground">拖控件入分区 · 拖卡片右缘调宽 · 拖分区头排序</span>
          </div>

          {blocks.map((b, bi) => {
            const isCollapsed = !!collapsed[b.title]
            return (
              <div
                key={b.title || '__default'}
                onDragOver={e => allowDrop(e, [MIME_BLOCK, MIME_TYPE, MIME_FIELD])}
                onDrop={e => {
                  if (!allowDrop(e, [MIME_BLOCK, MIME_TYPE, MIME_FIELD])) return
                  const from = e.dataTransfer.getData(MIME_BLOCK)
                  // 拖分区头 → 块排序；拖控件/字段落到分区留白 → 并入该分区末尾
                  if (from) moveBlockBefore(from, b.title)
                  else onDropInBlock(e, b.title)
                }}
                className="rounded-md border border-dashed border-border bg-muted/20"
              >
                {/* 分区头：拖拽排序 / 重命名 / 折叠 / 删除 */}
                <div
                  draggable={!disabled}
                  onDragStart={e => {
                    e.dataTransfer.setData(MIME_BLOCK, b.title)
                    e.dataTransfer.effectAllowed = 'move'
                  }}
                  className="group flex items-center gap-1.5 border-b border-dashed border-border px-2 py-1.5"
                >
                  <GripVertical className="h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground" />
                  <button
                    type="button"
                    className="shrink-0 text-muted-foreground hover:text-foreground"
                    title={isCollapsed ? '展开' : '折叠'}
                    onClick={() => setCollapsed(p => ({ ...p, [b.title]: !p[b.title] }))}
                  >
                    {isCollapsed ? <ChevronRight className="h-3.5 w-3.5" /> : <ChevronDown className="h-3.5 w-3.5" />}
                  </button>
                  <input
                    className="h-6 min-w-0 flex-1 rounded border border-transparent bg-transparent px-1.5 text-xs font-medium hover:border-input focus:border-input focus-visible:outline-none"
                    value={b.title}
                    disabled={disabled}
                    placeholder="默认区（点击命名，如：变更信息）"
                    onChange={e => renameSection(b.title, e.target.value)}
                  />
                  <span className="shrink-0 text-[10px] text-muted-foreground">{b.items.length} 字段</span>
                  <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
                    <IconBtn title="上移分区" disabled={disabled || bi === 0} onClick={() => moveBlock(b.title, -1)}>
                      <ArrowUp className="h-3 w-3" />
                    </IconBtn>
                    <IconBtn title="下移分区" disabled={disabled || bi === blocks.length - 1} onClick={() => moveBlock(b.title, 1)}>
                      <ArrowDown className="h-3 w-3" />
                    </IconBtn>
                    <IconBtn title="删除分区" danger disabled={disabled} onClick={() => removeSection(b.title)}>
                      <Trash2 className="h-3 w-3" />
                    </IconBtn>
                  </div>
                </div>

                {/* 分区体：12 栅格 */}
                {!isCollapsed && (
                  <div className="p-2">
                  <div
                    // 栅格容器不带内边距，clientWidth 即为 12 列的净宽，调宽换算才准
                    ref={el => {
                      gridRefs.current[b.title] = el
                    }}
                    onDragOver={e => allowDrop(e, [MIME_TYPE, MIME_FIELD])}
                    onDrop={e => onDropInBlock(e, b.title)}
                    className="grid grid-cols-12 gap-2"
                  >
                    {b.items.length === 0 ? (
                      <div className="col-span-12 flex flex-col items-center justify-center rounded-md border border-dashed border-border py-6 text-center text-muted-foreground">
                        <Plus className="mb-1 h-5 w-5 opacity-30" />
                        <p className="text-[11px]">把控件拖到这个分区</p>
                      </div>
                    ) : (
                      b.items.map(f => {
                        const span = normalizeColSpan(f.col_span)
                        const selected = f.key === activeKey
                        const isResizing = resizing?.key === f.key
                        return (
                          <div
                            key={f.key}
                            draggable={!disabled && !resizing}
                            onDragStart={e => {
                              // 调宽过程中禁止触发 HTML5 拖拽，避免手柄拖动被误识别为搬运
                              if (resizing) {
                                e.preventDefault()
                                return
                              }
                              e.dataTransfer.setData(MIME_FIELD, f.key)
                              e.dataTransfer.effectAllowed = 'move'
                            }}
                            onDragOver={e => allowDrop(e, [MIME_TYPE, MIME_FIELD])}
                            onDrop={e => onDropInBlock(e, b.title, f.key)}
                            onClick={() => setActiveKey(f.key)}
                            className={`group relative flex min-w-0 cursor-pointer items-center gap-1.5 rounded-md border bg-background px-2 py-2 ${SPAN_CLS[span]} ${
                              selected ? 'border-primary ring-1 ring-primary/40' : 'border-input hover:bg-accent/50'
                            } ${isResizing ? 'ring-2 ring-emerald-500/50' : ''}`}
                          >
                            <GripVertical className="h-3.5 w-3.5 shrink-0 cursor-grab text-muted-foreground" />
                            <div className="min-w-0 flex-1">
                              <div className="truncate text-xs font-medium">
                                {f.label || f.key}
                                {f.required && <span className="ml-0.5 text-destructive">*</span>}
                              </div>
                              <div className="truncate text-[10px] text-muted-foreground">
                                {f.key} · {FIELD_TYPES.find(t => t.value === f.type)?.label || f.type}
                              </div>
                            </div>
                            <span
                              className={`shrink-0 rounded px-1 text-[10px] tabular-nums ${
                                isResizing ? 'bg-emerald-500/20 text-emerald-600 dark:text-emerald-400' : 'bg-muted text-muted-foreground'
                              }`}
                            >
                              {span}/12
                            </span>
                            <IconBtn
                              title="删除字段"
                              danger
                              disabled={disabled}
                              onClick={() => removeField(f.key)}
                              className="opacity-0 transition-opacity group-hover:opacity-100"
                            >
                              <Trash2 className="h-3 w-3" />
                            </IconBtn>
                            {/* 右缘调宽手柄：按住左右拖动，实时吸附到栅格 */}
                            <span
                              onPointerDown={e => beginResize(e, f.key, b.title, span)}
                              onDragStart={e => {
                                e.preventDefault()
                                e.stopPropagation()
                              }}
                              onClick={e => e.stopPropagation()}
                              title="拖动调整宽度占比"
                              className={`absolute inset-y-1 right-0 w-1.5 rounded-r cursor-col-resize transition-colors ${
                                isResizing ? 'bg-emerald-500' : 'bg-transparent group-hover:bg-emerald-500/40'
                              } ${disabled ? 'pointer-events-none' : ''}`}
                            />
                          </div>
                        )
                      })
                    )}
                  </div>
                  </div>
                )}
              </div>
            )
          })}
        </div>

        {/* 实时预览 */}
        <div className="rounded-md border border-input p-3">
          <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold">
            <Eye className="h-3.5 w-3.5" /> 实时预览
            <span className="font-normal text-muted-foreground">（试填以验证宽度、分区与条件显隐）</span>
          </div>
          <DynamicForm schema={schema} values={preview} onChange={(k, v) => setPreview(p => ({ ...p, [k]: v }))} loadOptions={undefined} />
        </div>
      </div>

      {/* 右: 属性面板 */}
      <div className="max-h-[38rem] overflow-auto rounded-md border border-input p-3">
        {!active ? (
          <p className="text-xs italic text-muted-foreground">点击画布中的字段以编辑属性。</p>
        ) : (
          <FieldPropertyPanel
            field={active}
            sources={sources}
            referableFields={referableFields}
            disabled={disabled}
            onChange={updateActive}
          />
        )}
      </div>
    </div>
  )
}

/** 取 fields 中出现过的分区名（保持首次出现顺序） */
function deriveSections(fields: FormFieldDef[]): string[] {
  const seen: string[] = []
  for (const f of fields) {
    const s = f.section || ''
    if (!seen.includes(s)) seen.push(s)
  }
  return seen.length > 0 ? seen : ['']
}

/** field_N 序号：避开已存在的 key，防止重名 */
function nextSeq(fields: FormFieldDef[]): number {
  let n = fields.length + 1
  const keys = new Set(fields.map(f => f.key))
  while (keys.has(`field_${n}`)) n++
  return n
}

function uniqueTitle(base: string, used: string[]): string {
  let n = used.filter(Boolean).length + 1
  while (used.includes(`${base} ${n}`)) n++
  return `${base} ${n}`
}

function IconBtn({
  children,
  onClick,
  disabled,
  title,
  danger,
  className,
}: {
  children: React.ReactNode
  onClick: () => void
  disabled?: boolean
  title: string
  danger?: boolean
  className?: string
}) {
  return (
    <button
      type="button"
      title={title}
      disabled={disabled}
      onClick={e => {
        e.stopPropagation()
        onClick()
      }}
      className={`shrink-0 rounded p-0.5 hover:bg-background disabled:cursor-not-allowed disabled:opacity-30 ${
        danger ? 'text-destructive' : 'text-muted-foreground'
      } ${className || ''}`}
    >
      {children}
    </button>
  )
}
