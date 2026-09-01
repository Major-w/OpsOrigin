
import { useState } from 'react'
import { Search, RotateCcw, ChevronDown } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

export interface QueryField {
  name: string
  label: string
  type?: 'text' | 'select' | 'date' | 'number'
  placeholder?: string
  options?: { label: string; value: string }[]
  span?: 1 | 2 | 3 | 4
}

interface QueryFilterProps {
  fields: QueryField[]
  values: Record<string, any>
  onChange: (values: Record<string, any>) => void
  onSearch?: () => void
  onReset?: () => void
  loading?: boolean
  className?: string
  columns?: 1 | 2 | 3 | 4
}

export function QueryFilter({
  fields,
  values,
  onChange,
  onSearch,
  onReset,
  loading = false,
  className,
  columns = 3,
}: QueryFilterProps) {
  const [expanded, setExpanded] = useState(false)
  const visibleFields = expanded ? fields : fields.slice(0, columns)

  const handleChange = (name: string, value: any) => {
    onChange({ ...values, [name]: value })
  }

  const handleReset = () => {
    const reset: Record<string, any> = {}
    fields.forEach(f => { reset[f.name] = undefined })
    onChange(reset)
    onReset?.()
  }

  const hasMore = fields.length > columns

  return (
    <div className={cn("bg-gray-50/50 dark:bg-[rgba(255,255,255,0.02)] rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.06)] p-4", className)}>
      <div className={cn("grid gap-3", `grid-cols-${columns}`)}>
        {visibleFields.map(field => (
          <div key={field.name}>
            <label className="block text-xs font-medium text-muted-foreground mb-1">{field.label}</label>
            {field.type === 'select' ? (
              <select
                className="h-9 w-full rounded-md border border-gray-200 dark:border-[rgba(255,255,255,0.08)] bg-white dark:bg-[rgba(0,0,0,0.2)] px-3 text-sm"
                value={values[field.name] || ''}
                onChange={e => handleChange(field.name, e.target.value)}
              >
                <option value="">全部</option>
                {field.options?.map(o => (
                  <option key={o.value} value={o.value}>{o.label}</option>
                ))}
              </select>
            ) : (
              <Input
                className="h-9 text-sm"
                type={field.type || 'text'}
                placeholder={field.placeholder || field.label}
                value={values[field.name] || ''}
                onChange={e => handleChange(field.name, e.target.value)}
              />
            )}
          </div>
        ))}
      </div>

      <div className="flex items-center gap-2 mt-3 pt-3 border-t border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
        <Button size="sm" onClick={onSearch} disabled={loading}>
          <Search className="h-3.5 w-3.5 mr-1" />
          {loading ? '搜索中...' : '查询'}
        </Button>
        <Button size="sm" variant="outline" onClick={handleReset}>
          <RotateCcw className="h-3.5 w-3.5 mr-1" />
          重置
        </Button>
        {hasMore && (
          <Button size="sm" variant="ghost" onClick={() => setExpanded(!expanded)} className="ml-auto">
            {expanded ? '收起' : '展开'}
            <ChevronDown className={cn("h-3.5 w-3.5 ml-1 transition-transform", expanded && "rotate-180")} />
          </Button>
        )}
      </div>
    </div>
  )
}
