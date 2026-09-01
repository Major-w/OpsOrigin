
import { useState } from 'react'
import { LayoutGrid, List } from 'lucide-react'
import { cn } from '@/lib/utils'

export type ViewMode = 'list' | 'grid'

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
      <button
        className={cn(
          "h-7 w-7 flex items-center justify-center rounded transition-colors",
          mode === 'list' ? "bg-white dark:bg-[rgba(255,255,255,0.08)] shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"
        )}
        onClick={() => handleChange('list')}
      >
        <List className="h-3.5 w-3.5" />
      </button>
      <button
        className={cn(
          "h-7 w-7 flex items-center justify-center rounded transition-colors",
          mode === 'grid' ? "bg-white dark:bg-[rgba(255,255,255,0.08)] shadow-sm text-foreground" : "text-muted-foreground hover:text-foreground"
        )}
        onClick={() => handleChange('grid')}
      >
        <LayoutGrid className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}
