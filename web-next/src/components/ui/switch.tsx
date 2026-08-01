'use client'

import * as React from 'react'
import { cn } from '@/lib/utils'

interface SwitchProps extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, 'onChange'> {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  size?: 'sm' | 'md'
}

/**
 * Switch 开关。无障碍实现：role=switch + aria-checked，支持键盘 Space/Enter 切换。
 */
const Switch = React.forwardRef<HTMLButtonElement, SwitchProps>(
  ({ className, checked, onCheckedChange, disabled, size = 'md', ...props }, ref) => {
    const dims = size === 'sm'
      ? { track: 'h-4 w-7', thumb: 'h-3 w-3', on: 'translate-x-3.5', off: 'translate-x-0.5' }
      : { track: 'h-5 w-9', thumb: 'h-4 w-4', on: 'translate-x-4', off: 'translate-x-0.5' }

    return (
      <button
        ref={ref}
        type="button"
        role="switch"
        aria-checked={checked}
        disabled={disabled}
        onClick={() => !disabled && onCheckedChange(!checked)}
        className={cn(
          'relative inline-flex shrink-0 items-center rounded-full transition-colors duration-200',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/50 focus-visible:ring-offset-2 focus-visible:ring-offset-background',
          disabled && 'opacity-50 cursor-not-allowed',
          !disabled && 'cursor-pointer',
          checked ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600',
          dims.track,
          className
        )}
        {...props}
      >
        <span
          className={cn(
            'inline-block rounded-full bg-white shadow-sm transition-transform duration-200',
            dims.thumb,
            checked ? dims.on : dims.off
          )}
        />
      </button>
    )
  }
)
Switch.displayName = 'Switch'

/** SwitchField 带标题/描述的开关行，用于设置页与表单。 */
interface SwitchFieldProps {
  label: string
  description?: string
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  className?: string
}

function SwitchField({ label, description, checked, onCheckedChange, disabled, className }: SwitchFieldProps) {
  return (
    <label
      className={cn(
        'flex items-start justify-between gap-4 rounded-lg border border-gray-200 dark:border-gray-700 px-3 py-2.5 transition-colors',
        !disabled && 'hover:border-gray-300 dark:hover:border-gray-600 cursor-pointer',
        className
      )}
    >
      <div className="min-w-0">
        <div className="text-sm font-medium">{label}</div>
        {description && <p className="text-xs text-muted-foreground mt-0.5 leading-relaxed">{description}</p>}
      </div>
      <Switch checked={checked} onCheckedChange={onCheckedChange} disabled={disabled} className="mt-0.5" />
    </label>
  )
}

export { Switch, SwitchField }
