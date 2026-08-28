
import { useState, useMemo, useCallback } from 'react'
import { LayoutGrid, List, SlidersHorizontal, ChevronsUpDown, Settings, Search, X, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export type ViewMode = 'list' | 'grid'

export interface Column<T> {
  key: string
  label: string
  sortable?: boolean
  filterable?: boolean
  visible?: boolean
  width?: string
  render?: (item: T) => React.ReactNode
}

export interface FilterOption {
  key: string
  label: string
  type: 'text' | 'select' | 'date'
  options?: { label: string; value: string }[]
}

export interface BulkAction {
  key: string
  label: string
  icon?: React.ReactNode
  variant?: 'default' | 'destructive'
  onClick: (selected: any[]) => void
  confirmMessage?: string
}

interface ViewToggleProps {
  value?: ViewMode
  onChange?: (mode: ViewMode) => void
  className?: string
}

export function ViewToggle({ value, onChange, className }: ViewToggleProps) {
  const [internalMode, setInternalMode] = useState<ViewMode>('list')
  const mode = value ?? internalMode

  const handleChange = (m: ViewMode) => {
    setInternalMode(m)
    onChange?.(m)
  }

  return (
    <div className={cn("flex items-center gap-1 p-0.5 rounded-md bg-gray-100 dark:bg-[rgba(255,255,255,0.04)]", className)}>
      <Button variant="ghost" size="icon" className={cn("h-7 w-7", mode === 'list' && "bg-white dark:bg-[rgba(255,255,255,0.08)] shadow-sm")} onClick={() => handleChange('list')}>
        <List className="h-3.5 w-3.5" />
      </Button>
      <Button variant="ghost" size="icon" className={cn("h-7 w-7", mode === 'grid' && "bg-white dark:bg-[rgba(255,255,255,0.08)] shadow-sm")} onClick={() => handleChange('grid')}>
        <LayoutGrid className="h-3.5 w-3.5" />
      </Button>
    </div>
  )
}

function ColumnSelector({ columns, onChange }: { columns: Column<any>[]; onChange: (cols: Column<any>[]) => void }) {
  const [open, setOpen] = useState(false)

  const toggleVisible = (key: string) => {
    onChange(columns.map(c => c.key === key ? { ...c, visible: c.visible === false ? true : false } : c))
  }

  const allVisible = columns.every(c => c.visible !== false)

  return (
    <div className="relative">
      <Button variant="outline" size="sm" onClick={() => setOpen(!open)} className="gap-1">
        <Settings className="h-3.5 w-3.5" />
        <span className="hidden sm:inline">显示字段</span>
        <ChevronDown className={cn("h-3 w-3 transition-transform", open && "rotate-180")} />
      </Button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full mt-1 z-50 w-48 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg p-2">
            <div className="flex items-center justify-between px-2 py-1 text-xs text-muted-foreground border-b mb-1">
              <span>选择显示列</span>
              <button className="text-blue-500 hover:text-blue-600" onClick={() => onChange(columns.map(c => ({ ...c, visible: allVisible ? false : true })))}>
                {allVisible ? '全部隐藏' : '全部显示'}
              </button>
            </div>
            {columns.map(col => (
              <label key={col.key} className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-gray-50 dark:hover:bg-gray-800 cursor-pointer text-xs">
                <input
                  type="checkbox"
                  checked={col.visible !== false}
                  onChange={() => toggleVisible(col.key)}
                  className="rounded"
                />
                <span>{col.label}</span>
              </label>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

function FilterPanel({ filters, values, onChange, onClear }: {
  filters: FilterOption[]
  values: Record<string, string>
  onChange: (key: string, value: string) => void
  onClear: () => void
}) {
  const [open, setOpen] = useState(false)
  const activeCount = Object.values(values).filter(Boolean).length

  return (
    <div className="relative">
      <Button variant="outline" size="sm" onClick={() => setOpen(!open)} className="gap-1">
        <SlidersHorizontal className="h-3.5 w-3.5" />
        <span className="hidden sm:inline">筛选</span>
        {activeCount > 0 && <Badge variant="success" className="ml-1 text-[10px] px-1 py-0">{activeCount}</Badge>}
      </Button>
      {open && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setOpen(false)} />
          <div className="absolute right-0 top-full mt-1 z-50 w-72 bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg p-3 space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-muted-foreground">筛选条件</span>
              {activeCount > 0 && (
                <button className="text-xs text-blue-500 hover:text-blue-600" onClick={onClear}>清除全部</button>
              )}
            </div>
            {filters.map(f => (
              <div key={f.key}>
                <label className="text-xs text-muted-foreground mb-1 block">{f.label}</label>
                {f.type === 'select' ? (
                  <select
                    className="h-8 w-full rounded-md border border-input bg-background px-2 text-xs"
                    value={values[f.key] || ''}
                    onChange={e => onChange(f.key, e.target.value)}
                  >
                    <option value="">全部</option>
                    {f.options?.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                  </select>
                ) : (
                  <Input
                    className="h-8 text-xs"
                    placeholder={f.label}
                    value={values[f.key] || ''}
                    onChange={e => onChange(f.key, e.target.value)}
                  />
                )}
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

interface ListItem {
  id: string
  [key: string]: any
}

interface DataListProps<T extends ListItem> {
  data: T[]
  columns: Column<T>[]
  viewMode: ViewMode
  filters?: FilterOption[]
  bulkActions?: BulkAction[]
  listView: (item: T, selectedColumns: Column<T>[]) => React.ReactNode
  gridView: (item: T) => React.ReactNode
  emptyMessage?: string
  searchPlaceholder?: string
  className?: string
  onRefresh?: () => void
  customHeader?: React.ReactNode
}

export function DataList<T extends ListItem>({
  data, columns, viewMode, filters = [], bulkActions = [],
  listView, gridView, emptyMessage = '暂无数据', searchPlaceholder = '搜索...',
  className, onRefresh, customHeader
}: DataListProps<T>) {
  const [searchQ, setSearchQ] = useState('')
  const [filterValues, setFilterValues] = useState<Record<string, string>>({})
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [sortKey, setSortKey] = useState<string>('')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [confirmAction, setConfirmAction] = useState<{ action: BulkAction; items: T[] } | null>(null)
  const [localColumns, setLocalColumns] = useState<Column<T>[]>(columns)

  const filtered = useMemo(() => {
    let result = [...data]

    if (searchQ) {
      const q = searchQ.toLowerCase()
      result = result.filter(item =>
        Object.values(item).some(v =>
          String(v).toLowerCase().includes(q)
        )
      )
    }

    Object.entries(filterValues).forEach(([key, value]) => {
      if (value) {
        result = result.filter(item =>
          String(item[key] || '').toLowerCase().includes(value.toLowerCase())
        )
      }
    })

    if (sortKey) {
      result.sort((a, b) => {
        const aVal = a[sortKey] || ''
        const bVal = b[sortKey] || ''
        const cmp = String(aVal).localeCompare(String(bVal))
        return sortDir === 'asc' ? cmp : -cmp
      })
    }

    return result
  }, [data, searchQ, filterValues, sortKey, sortDir])

  const visibleColumns = useMemo(() => localColumns.filter(c => c.visible !== false), [localColumns])

  const handleSelect = useCallback((id: string) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const handleSelectAll = useCallback(() => {
    if (selectedIds.size === filtered.length) {
      setSelectedIds(new Set())
    } else {
      setSelectedIds(new Set(filtered.map(i => i.id)))
    }
  }, [selectedIds.size, filtered])

  const handleBulkAction = (action: BulkAction) => {
    const selected = filtered.filter(i => selectedIds.has(i.id))
    if (action.confirmMessage) {
      setConfirmAction({ action, items: selected })
    } else {
      action.onClick(selected)
      setSelectedIds(new Set())
    }
  }

  const handleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const isAllSelected = filtered.length > 0 && selectedIds.size === filtered.length
  const isPartial = selectedIds.size > 0 && selectedIds.size < filtered.length

  return (
    <div className={cn("space-y-3", className)}>
      <div className="flex items-center gap-2 flex-wrap">
        <div className="relative flex-1 min-w-[200px] max-w-xs">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input
            className="pl-9 h-9 text-xs"
            placeholder={searchPlaceholder}
            value={searchQ}
            onChange={e => setSearchQ(e.target.value)}
          />
          {searchQ && (
            <button className="absolute right-3 top-1/2 -translate-y-1/2" onClick={() => setSearchQ('')}>
              <X className="h-3 w-3 text-muted-foreground hover:text-foreground" />
            </button>
          )}
        </div>

        {filters.length > 0 && (
          <FilterPanel
            filters={filters}
            values={filterValues}
            onChange={(key, value) => setFilterValues(prev => ({ ...prev, [key]: value }))}
            onClear={() => setFilterValues({})}
          />
        )}

        <ColumnSelector columns={localColumns} onChange={setLocalColumns} />

        {onRefresh && (
          <Button variant="outline" size="sm" onClick={onRefresh}>
            <ChevronsUpDown className="h-3.5 w-3.5 mr-1" />刷新
          </Button>
        )}

        {customHeader}
      </div>

      {selectedIds.size > 0 && bulkActions.length > 0 && (
        <div className="flex items-center gap-2 p-2 bg-blue-50 dark:bg-blue-500/5 rounded-lg border border-blue-200 dark:border-blue-500/20">
          <span className="text-xs text-blue-600 dark:text-blue-400 font-medium">
            已选择 {selectedIds.size} 项
          </span>
          <div className="flex gap-1">
            {bulkActions.map(action => (
              <Button
                key={action.key}
                size="sm"
                variant={action.variant === 'destructive' ? 'destructive' : 'default'}
                className="h-7 text-xs"
                onClick={() => handleBulkAction(action)}
              >
                {action.icon}
                {action.label}
              </Button>
            ))}
          </div>
          <Button size="sm" variant="ghost" className="h-7 text-xs ml-auto" onClick={() => setSelectedIds(new Set())}>
            取消选择
          </Button>
        </div>
      )}

      {filtered.length === 0 ? (
        <div className="py-12 text-center text-muted-foreground">{emptyMessage}</div>
      ) : viewMode === 'list' ? (
        <div className="space-y-1 overflow-x-auto">
          <div className="flex items-center gap-4 px-4 py-2.5 text-xs text-muted-foreground font-medium border-b min-w-max">
            <input
              type="checkbox"
              checked={isAllSelected}
              ref={el => { if (el) el.indeterminate = isPartial }}
              onChange={handleSelectAll}
              className="rounded"
            />
            {visibleColumns.map(col => (
              <div
                key={col.key}
                className={cn(
                  "flex items-center gap-1 cursor-pointer hover:text-foreground",
                  col.width || "flex-1"
                )}
                onClick={() => col.sortable && handleSort(col.key)}
              >
                {col.label}
                {col.sortable && sortKey === col.key && (
                  <ChevronsUpDown className={cn("h-3 w-3", sortDir === 'desc' && "rotate-180")} />
                )}
              </div>
            ))}
          </div>
          {filtered.map(item => (
            <div key={item.id} className={cn(
              "flex items-center gap-4 px-4 py-3 border-b border-gray-50 dark:border-[rgba(255,255,255,0.02)] hover:bg-gray-50 dark:hover:bg-[rgba(255,255,255,0.02)] transition-colors min-w-max",
              selectedIds.has(item.id) && "bg-blue-50/50 dark:bg-blue-500/5"
            )}>
              <input
                type="checkbox"
                checked={selectedIds.has(item.id)}
                onChange={() => handleSelect(item.id)}
                className="rounded"
              />
              {listView(item, visibleColumns)}
            </div>
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {filtered.map(item => (
            <div key={item.id}>{gridView(item)}</div>
          ))}
        </div>
      )}

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>共 {filtered.length} 条记录</span>
        {selectedIds.size > 0 && <span>已选择 {selectedIds.size} 条</span>}
      </div>

      {confirmAction && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="fixed inset-0 bg-black/50" onClick={() => setConfirmAction(null)} />
          <div className="relative z-50 w-full max-w-md bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
            <h3 className="text-lg font-semibold mb-2">确认操作</h3>
            <p className="text-sm text-muted-foreground mb-4">
              {confirmAction.action.confirmMessage}
            </p>
            <p className="text-xs text-muted-foreground mb-4">
              将影响 {confirmAction.items.length} 条记录
            </p>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setConfirmAction(null)}>取消</Button>
              <Button
                variant={confirmAction.action.variant === 'destructive' ? 'destructive' : 'default'}
                onClick={() => {
                  confirmAction.action.onClick(confirmAction.items)
                  setSelectedIds(new Set())
                  setConfirmAction(null)
                }}
              >
                确认
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
