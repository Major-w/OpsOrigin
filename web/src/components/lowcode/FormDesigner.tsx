
import { useState, useCallback, useRef } from 'react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import {
  Plus, Trash2, GripVertical, Type, Hash, Calendar, Mail, Phone,
  Link, FileText, Code, CheckSquare, ToggleLeft, ChevronDown,
  Copy, X, Columns, Rows
} from 'lucide-react'

export interface FormField {
  id: string
  type: string
  title: string
  name: string
  required: boolean
  placeholder?: string
  description?: string
  colSpan?: number
}

interface FormRow {
  id: string
  columns: number
  fields: (FormField | null)[]
}

const FIELD_TYPES = [
  { type: 'input', label: '单行文本', icon: Type, category: '基础字段' },
  { type: 'textarea', label: '多行文本', icon: FileText, category: '基础字段' },
  { type: 'number', label: '数字', icon: Hash, category: '基础字段' },
  { type: 'select', label: '下拉选择', icon: ChevronDown, category: '选择字段' },
  { type: 'checkbox', label: '复选框', icon: CheckSquare, category: '选择字段' },
  { type: 'radio', label: '单选按钮', icon: ToggleLeft, category: '选择字段' },
  { type: 'date', label: '日期', icon: Calendar, category: '高级字段' },
  { type: 'email', label: '邮箱', icon: Mail, category: '高级字段' },
  { type: 'phone', label: '电话', icon: Phone, category: '高级字段' },
  { type: 'url', label: '链接', icon: Link, category: '高级字段' },
  { type: 'code', label: '代码块', icon: Code, category: '高级字段' },
]

interface FormDesignerProps {
  value?: FormField[]
  onChange?: (fields: FormField[]) => void
  readOnly?: boolean
  className?: string
}

function generateId() {
  return `f_${Date.now()}_${Math.random().toString(36).substr(2, 6)}`
}

function createField(type: string): FormField {
  const ft = FIELD_TYPES.find(f => f.type === type)
  return {
    id: generateId(),
    type,
    title: ft?.label || type,
    name: `field_${Date.now()}`,
    required: false,
    placeholder: `请输入${ft?.label || ''}`,
  }
}

function fieldsToRows(fields: FormField[]): FormRow[] {
  if (fields.length === 0) return []

  const rows: FormRow[] = []

  for (const field of fields) {
    if (field.colSpan && field.colSpan < 12) {
      const columnsNeeded = field.colSpan === 6 ? 2 : field.colSpan === 4 ? 3 : 4

      let placed = false
      for (let i = rows.length - 1; i >= 0; i--) {
        const row = rows[i]
        if (row.columns === columnsNeeded) {
          const emptyCol = row.fields.findIndex(f => f === null)
          if (emptyCol !== -1) {
            row.fields[emptyCol] = field
            placed = true
            break
          }
        }
      }

      if (!placed) {
        rows.push({ id: generateId(), columns: columnsNeeded, fields: [field, ...Array(columnsNeeded - 1).fill(null)] })
      }
    } else {
      rows.push({ id: generateId(), columns: 1, fields: [field] })
    }
  }

  return rows
}

function rowsToFields(rows: FormRow[]): FormField[] {
  const fields: FormField[] = []
  for (const row of rows) {
    for (const field of row.fields) {
      if (field) {
        fields.push(field)
      }
    }
  }
  return fields
}

export function FormDesigner({ value = [], onChange, readOnly = false, className }: FormDesignerProps) {
  const [rows, setRows] = useState<FormRow[]>(() => fieldsToRows(value))
  const [selectedFieldId, setSelectedFieldId] = useState<string | null>(null)
  const [draggedType, setDraggedType] = useState<string | null>(null)
  const [dropTarget, setDropTarget] = useState<{ rowId: string; colIndex: number } | null>(null)
  const [dropPosition, setDropPosition] = useState<'before' | 'after' | null>(null)
  const canvasRef = useRef<HTMLDivElement>(null)

  const syncFields = (newRows: FormRow[]) => {
    setRows(newRows)
    onChange?.(rowsToFields(newRows))
  }

  const findFieldPosition = (fieldId: string) => {
    for (let r = 0; r < rows.length; r++) {
      for (let c = 0; c < rows[r].fields.length; c++) {
        if (rows[r].fields[c]?.id === fieldId) {
          return { rowIndex: r, colIndex: c }
        }
      }
    }
    return null
  }

  const addFieldToRow = (type: string, rowId: string, colIndex: number) => {
    const field = createField(type)
    const newRows = rows.map(row => {
      if (row.id === rowId) {
        const newFields = [...row.fields]
        newFields[colIndex] = field
        return { ...row, fields: newFields }
      }
      return row
    })
    syncFields(newRows)
    setSelectedFieldId(field.id)
  }

  const addRow = (columns: number, afterRowId?: string) => {
    const newRow: FormRow = {
      id: generateId(),
      columns,
      fields: Array(columns).fill(null),
    }

    let newRows: FormRow[]
    if (afterRowId) {
      const idx = rows.findIndex(r => r.id === afterRowId)
      newRows = [...rows.slice(0, idx + 1), newRow, ...rows.slice(idx + 1)]
    } else {
      newRows = [...rows, newRow]
    }
    syncFields(newRows)
  }

  const deleteRow = (rowId: string) => {
    syncFields(rows.filter(r => r.id !== rowId))
    setSelectedFieldId(null)
  }

  const updateField = (fieldId: string, updates: Partial<FormField>) => {
    const newRows = rows.map(row => ({
      ...row,
      fields: row.fields.map(f => f?.id === fieldId ? { ...f, ...updates } : f)
    }))
    syncFields(newRows)
  }

  const deleteField = (fieldId: string) => {
    const newRows = rows.map(row => ({
      ...row,
      fields: row.fields.map(f => f?.id === fieldId ? null : f)
    }))
    const cleanedRows = newRows.filter(row => row.fields.some(f => f !== null))
    syncFields(cleanedRows)
    setSelectedFieldId(null)
  }

  const duplicateField = (fieldId: string) => {
    const pos = findFieldPosition(fieldId)
    if (!pos) return

    const sourceField = rows[pos.rowIndex].fields[pos.colIndex]
    if (!sourceField) return

    const newField: FormField = {
      ...sourceField,
      id: generateId(),
      name: `${sourceField.name}_copy`,
      title: `${sourceField.title} (副本)`,
    }

    const row = rows[pos.rowIndex]
    if (pos.colIndex < row.columns - 1 && !row.fields[pos.colIndex + 1]) {
      const newRows = rows.map(r => {
        if (r.id === row.id) {
          const newFields = [...r.fields]
          newFields[pos.colIndex + 1] = newField
          return { ...r, fields: newFields }
        }
        return r
      })
      syncFields(newRows)
    } else {
      addRow(1)
      const lastRow = rows[rows.length - 1]
      const newRows = [...rows, { id: lastRow.id, columns: 1, fields: [newField] }]
      syncFields(newRows)
    }
    setSelectedFieldId(newField.id)
  }

  const handleTypeDragStart = (e: React.DragEvent, type: string) => {
    e.dataTransfer.setData('application/field-type', type)
    e.dataTransfer.effectAllowed = 'copy'
    setDraggedType(type)
  }

  const handleCellDragOver = (e: React.DragEvent, rowId: string, colIndex: number) => {
    e.preventDefault()
    e.stopPropagation()
    e.dataTransfer.dropEffect = 'copy'
    setDropTarget({ rowId, colIndex })
  }

  const handleCellDrop = (e: React.DragEvent, rowId: string, colIndex: number) => {
    e.preventDefault()
    e.stopPropagation()

    const fieldType = e.dataTransfer.getData('application/field-type')
    const fieldId = e.dataTransfer.getData('application/field-id')

    if (fieldType) {
      addFieldToRow(fieldType, rowId, colIndex)
    } else if (fieldId) {
      const pos = findFieldPosition(fieldId)
      if (pos) {
        const field = rows[pos.rowIndex].fields[pos.colIndex]
        if (!field) return

        const newRows = rows.map(row => {
          if (row.id === pos.rowIndex.toString()) {
            const newFields = [...row.fields]
            newFields[pos.colIndex] = null
            return { ...row, fields: newFields }
          }
          if (row.id === rowId) {
            const newFields = [...row.fields]
            newFields[colIndex] = field
            return { ...row, fields: newFields }
          }
          return row
        })
        syncFields(newRows.filter(r => r.fields.some(f => f !== null)))
      }
    }

    setDraggedType(null)
    setDropTarget(null)
  }

  const handleCellDragLeave = () => {
    setDropTarget(null)
  }

  const handleFieldDragStart = (e: React.DragEvent, fieldId: string) => {
    e.dataTransfer.setData('application/field-id', fieldId)
    e.dataTransfer.effectAllowed = 'move'
    e.stopPropagation()
  }

  const selectedField = selectedFieldId
    ? rows.flatMap(r => r.fields).find(f => f?.id === selectedFieldId)
    : null

  const renderFieldPreview = (field: FormField) => {
    switch (field.type) {
      case 'input':
      case 'email':
      case 'phone':
      case 'url':
        return <Input placeholder={field.placeholder} disabled className="h-8 text-xs" />
      case 'textarea':
        return <textarea className="flex h-12 w-full rounded-md border border-input bg-background px-2 py-1.5 text-xs" placeholder={field.placeholder} disabled />
      case 'number':
        return <Input type="number" placeholder={field.placeholder} disabled className="h-8 text-xs" />
      case 'select':
        return (
          <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 py-1.5 text-xs" disabled>
            <option>{field.placeholder || '请选择'}</option>
          </select>
        )
      case 'checkbox':
        return (
          <div className="flex items-center gap-2">
            <input type="checkbox" disabled className="rounded" />
            <span className="text-xs">{field.placeholder || '选项'}</span>
          </div>
        )
      case 'radio':
        return (
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-1"><input type="radio" disabled /><span className="text-xs">选项1</span></label>
            <label className="flex items-center gap-1"><input type="radio" disabled /><span className="text-xs">选项2</span></label>
          </div>
        )
      case 'date':
        return <Input type="date" disabled className="h-8 text-xs" />
      case 'code':
        return <textarea className="flex h-14 w-full rounded-md border border-input bg-gray-950 text-blue-400 px-2 py-1.5 text-xs font-mono" placeholder="// 代码" disabled />
      default:
        return <Input placeholder={field.placeholder} disabled className="h-8 text-xs" />
    }
  }

  return (
    <div className={cn("flex h-[600px] border rounded-lg overflow-hidden bg-white dark:bg-gray-950", className)}>
      {!readOnly && (
        <div className="w-48 border-r bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] overflow-y-auto shrink-0">
          <div className="p-3 border-b">
            <h3 className="text-sm font-semibold">字段组件</h3>
            <p className="text-[10px] text-muted-foreground mt-0.5">拖拽到画布</p>
          </div>
          <div className="p-2 space-y-2">
            {['基础字段', '选择字段', '高级字段'].map(category => (
              <div key={category}>
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider px-2 mb-1">{category}</p>
                <div className="space-y-0.5">
                  {FIELD_TYPES.filter(f => f.category === category).map(ft => {
                    const Icon = ft.icon
                    return (
                      <div
                        key={ft.type}
                        draggable
                        onDragStart={(e) => handleTypeDragStart(e, ft.type)}
                        onDragEnd={() => setDraggedType(null)}
                        className={cn(
                          "w-full flex items-center gap-1.5 px-2 py-1.5 rounded text-xs cursor-grab active:cursor-grabbing transition-all",
                          "hover:bg-white dark:hover:bg-[rgba(255,255,255,0.05)] border border-transparent hover:border-blue-500/30",
                          draggedType === ft.type && "opacity-50 border-blue-500 bg-blue-50/50"
                        )}
                      >
                        <GripVertical className="h-3 w-3 text-muted-foreground/30" />
                        <Icon className="h-3 w-3 text-muted-foreground" />
                        <span>{ft.label}</span>
                      </div>
                    )
                  })}
                </div>
              </div>
            ))}

            {!readOnly && (
              <div className="pt-2 border-t">
                <p className="text-[10px] text-muted-foreground uppercase tracking-wider px-2 mb-1">布局</p>
                <div className="grid grid-cols-2 gap-1 px-1">
                  <button
                    className="flex items-center justify-center gap-1 px-2 py-1.5 rounded text-[10px] border border-dashed border-gray-300 dark:border-gray-600 hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/5 transition-colors"
                    onClick={() => addRow(2)}
                  >
                    <Columns className="h-3 w-3" />2列
                  </button>
                  <button
                    className="flex items-center justify-center gap-1 px-2 py-1.5 rounded text-[10px] border border-dashed border-gray-300 dark:border-gray-600 hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/5 transition-colors"
                    onClick={() => addRow(3)}
                  >
                    <Columns className="h-3 w-3" />3列
                  </button>
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      <div ref={canvasRef} className="flex-1 overflow-y-auto p-4">
        <div className="max-w-3xl mx-auto">
          {rows.length === 0 ? (
            <div className={cn(
              "py-16 text-center text-muted-foreground border-2 border-dashed rounded-lg transition-colors min-h-[300px] flex flex-col items-center justify-center",
              draggedType ? "border-blue-500 bg-blue-50/50 dark:bg-blue-500/5" : ""
            )}>
              <Plus className="h-8 w-8 mb-2 opacity-30" />
              <p className="text-sm">从左侧拖拽字段到此区域</p>
              <p className="text-xs mt-1">或点击底部按钮添加多列布局</p>
            </div>
          ) : (
            <div className="space-y-3">
              {rows.map((row) => (
                <div
                  key={row.id}
                  className={cn(
                    "group/row relative rounded-lg border transition-all",
                    row.fields.some(f => f !== null) ? "border-gray-100 dark:border-[rgba(255,255,255,0.04)]" : "border-dashed border-gray-300 dark:border-gray-600"
                  )}
                >
                  {!readOnly && (
                    <div className="absolute -top-3 right-2 opacity-0 group-hover/row:opacity-100 transition-opacity z-10 flex gap-1">
                      <div className="flex items-center gap-0.5 bg-white dark:bg-gray-900 border rounded-md px-1 py-0.5 shadow-sm">
                        <button
                          className={cn("px-1.5 py-0.5 rounded text-[10px]", row.columns === 1 ? "bg-blue-100 text-blue-700" : "hover:bg-gray-100")}
                          onClick={() => {
                            const newRows = rows.map(r => r.id === row.id ? { ...r, columns: 1, fields: [r.fields.find(f => f !== null) ?? null] } : r)
                            syncFields(newRows)
                          }}
                        >1列</button>
                        <button
                          className={cn("px-1.5 py-0.5 rounded text-[10px]", row.columns === 2 ? "bg-blue-100 text-blue-700" : "hover:bg-gray-100")}
                          onClick={() => {
                            const existingFields = row.fields.filter(f => f !== null)
                            const newFields = [existingFields[0] || null, existingFields[1] || null]
                            const newRows = rows.map(r => r.id === row.id ? { ...r, columns: 2, fields: newFields } : r)
                            syncFields(newRows)
                          }}
                        >2列</button>
                        <button
                          className={cn("px-1.5 py-0.5 rounded text-[10px]", row.columns === 3 ? "bg-blue-100 text-blue-700" : "hover:bg-gray-100")}
                          onClick={() => {
                            const existingFields = row.fields.filter(f => f !== null)
                            const newFields = [existingFields[0] || null, existingFields[1] || null, existingFields[2] || null]
                            const newRows = rows.map(r => r.id === row.id ? { ...r, columns: 3, fields: newFields } : r)
                            syncFields(newRows)
                          }}
                        >3列</button>
                        <div className="w-px h-3 bg-gray-200 mx-0.5" />
                        <button
                          className="px-1.5 py-0.5 rounded text-[10px] text-destructive hover:bg-red-50"
                          onClick={() => deleteRow(row.id)}
                        ><Trash2 className="h-2.5 w-2.5" /></button>
                      </div>
                    </div>
                  )}

                  <div className={cn(
                    "gap-2 p-2",
                    row.columns === 1 ? "grid grid-cols-1" : row.columns === 2 ? "grid grid-cols-2" : "grid grid-cols-3",
                  )}>
                    {row.fields.map((field, colIndex) => {
                      const isDropTargetHere = dropTarget?.rowId === row.id && dropTarget?.colIndex === colIndex
                      return (
                        <div
                          key={colIndex}
                          className={cn(
                            "min-h-[60px] rounded-md border border-dashed transition-all p-2",
                            isDropTargetHere
                              ? "border-blue-500 bg-blue-50/50 dark:bg-blue-500/5"
                              : field
                                ? "border-transparent"
                                : "border-gray-200 dark:border-gray-700 hover:border-gray-300 dark:hover:border-gray-600"
                          )}
                          onDragOver={(e) => handleCellDragOver(e, row.id, colIndex)}
                          onDrop={(e) => handleCellDrop(e, row.id, colIndex)}
                          onDragLeave={handleCellDragLeave}
                        >
                          {field ? (
                            <div
                              draggable={!readOnly}
                              onDragStart={(e) => handleFieldDragStart(e, field.id)}
                              onClick={() => !readOnly && setSelectedFieldId(field.id)}
                              className={cn(
                                "p-2 rounded-md border transition-all cursor-move group/field",
                                selectedFieldId === field.id
                                  ? "border-blue-500 bg-blue-50/50 dark:bg-blue-500/5"
                                  : "border-gray-100 dark:border-[rgba(255,255,255,0.04)] hover:border-blue-500/30"
                              )}
                            >
                              <div className="flex items-start gap-1.5">
                                <GripVertical className="h-3.5 w-3.5 text-muted-foreground/30 mt-0.5 shrink-0 opacity-0 group-hover/field:opacity-100 transition-opacity" />
                                <div className="flex-1 min-w-0">
                                  <div className="flex items-center gap-1.5 mb-1">
                                    <span className="text-xs font-medium truncate">{field.title}</span>
                                    {field.required && <span className="text-destructive text-[10px]">*</span>}
                                    <Badge variant="outline" className="text-[9px] px-1 py-0">{field.type}</Badge>
                                  </div>
                                  {renderFieldPreview(field)}
                                </div>
                                {!readOnly && selectedFieldId === field.id && (
                                  <div className="flex gap-0.5 shrink-0">
                                    <Button variant="ghost" size="icon" className="h-5 w-5" onClick={(e) => { e.stopPropagation(); duplicateField(field.id) }}>
                                      <Copy className="h-2.5 w-2.5" />
                                    </Button>
                                    <Button variant="ghost" size="icon" className="h-5 w-5 text-destructive" onClick={(e) => { e.stopPropagation(); deleteField(field.id) }}>
                                      <Trash2 className="h-2.5 w-2.5" />
                                    </Button>
                                  </div>
                                )}
                              </div>
                            </div>
                          ) : (
                            <div className="h-full min-h-[50px] flex items-center justify-center text-[10px] text-muted-foreground/50">
                              拖入字段
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}

              {!readOnly && (
                <div className="flex justify-center gap-2 pt-2">
                  <button
                    className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs border border-dashed border-gray-300 dark:border-gray-600 hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/5 transition-colors"
                    onClick={() => addRow(1)}
                  >
                    <Plus className="h-3 w-3" />满宽
                  </button>
                  <button
                    className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs border border-dashed border-gray-300 dark:border-gray-600 hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/5 transition-colors"
                    onClick={() => addRow(2)}
                  >
                    <Columns className="h-3 w-3" />2列
                  </button>
                  <button
                    className="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs border border-dashed border-gray-300 dark:border-gray-600 hover:border-blue-500 hover:bg-blue-50 dark:hover:bg-blue-500/5 transition-colors"
                    onClick={() => addRow(3)}
                  >
                    <Columns className="h-3 w-3" />3列
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {!readOnly && selectedField && (
        <div className="w-56 border-l bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] overflow-y-auto shrink-0">
          <div className="p-3 border-b flex items-center justify-between">
            <h3 className="text-sm font-semibold">字段属性</h3>
            <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => setSelectedFieldId(null)}>
              <X className="h-3 w-3" />
            </Button>
          </div>
          <div className="p-3 space-y-3">
            <div>
              <label className="text-xs font-medium text-muted-foreground">标题</label>
              <Input className="h-8 text-xs mt-1" value={selectedField.title} onChange={e => updateField(selectedField.id, { title: e.target.value })} />
            </div>
            <div>
              <label className="text-xs font-medium text-muted-foreground">字段名</label>
              <Input className="h-8 text-xs mt-1 font-mono" value={selectedField.name} onChange={e => updateField(selectedField.id, { name: e.target.value })} />
            </div>
            <div>
              <label className="text-xs font-medium text-muted-foreground">占位文本</label>
              <Input className="h-8 text-xs mt-1" value={selectedField.placeholder || ''} onChange={e => updateField(selectedField.id, { placeholder: e.target.value })} />
            </div>
            <div className="flex items-center gap-2">
              <input type="checkbox" checked={selectedField.required} onChange={e => updateField(selectedField.id, { required: e.target.checked })} className="rounded" />
              <label className="text-xs font-medium text-muted-foreground">必填字段</label>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default FormDesigner
