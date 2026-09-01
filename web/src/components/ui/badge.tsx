
import * as React from "react"
import { cn } from "@/lib/utils"

const badgeVariants = {
  default: "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-500/30 dark:bg-blue-500/10 dark:text-blue-400",
  success: "border-blue-200 bg-blue-50 text-blue-700 dark:border-blue-500/30 dark:bg-blue-500/10 dark:text-blue-400",
  destructive: "border-red-200 bg-red-50 text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-400",
  warning: "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-400",
  secondary: "border-gray-200 bg-gray-100 text-gray-600 dark:border-[var(--border)] dark:bg-[rgba(255,255,255,0.04)] dark:text-[var(--secondary-foreground)]",
  outline: "border-gray-300 text-gray-600 dark:border-[var(--input)] dark:text-[var(--secondary-foreground)]",
  info: "border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-500/30 dark:bg-violet-500/10 dark:text-violet-400",
}

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: keyof typeof badgeVariants
  pulse?: boolean
}

function Badge({ className, variant = "default", pulse, ...props }: BadgeProps) {
  return (
    <div
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-[11px] font-mono font-medium tracking-tight",
        badgeVariants[variant],
        pulse && "animate-pulse-soft",
        className
      )}
      {...props}
    >
      {pulse && <span className="w-1.5 h-1.5 rounded-full bg-current animate-pulse-soft" />}
      {props.children}
    </div>
  )
}

export { Badge, badgeVariants }
