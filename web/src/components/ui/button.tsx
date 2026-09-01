
import * as React from "react"
import { cn } from "@/lib/utils"

const buttonVariants = {
  default: "bg-blue-50 text-blue-600 border border-blue-200 hover:bg-blue-100 dark:bg-blue-500/15 dark:text-blue-400 dark:border-blue-500/25 dark:hover:bg-blue-500/20",
  primary: "bg-blue-500 text-white font-semibold hover:bg-blue-600 border border-blue-500 shadow-sm dark:bg-blue-500 dark:hover:bg-blue-400 dark:text-[var(--primary-foreground)]",
  destructive: "bg-red-50 text-red-600 border border-red-200 hover:bg-red-100 dark:bg-red-500/15 dark:text-red-400 dark:border-red-500/25 dark:hover:bg-red-500/20",
  outline: "bg-transparent text-gray-600 border border-gray-200 hover:text-gray-900 hover:border-gray-300 dark:text-[var(--secondary-foreground)] dark:border-[var(--input)] dark:hover:text-white dark:hover:border-[rgba(255,255,255,0.15)]",
  secondary: "bg-gray-100 text-gray-600 border border-gray-200 hover:bg-gray-200 dark:bg-[rgba(255,255,255,0.04)] dark:text-[var(--secondary-foreground)] dark:border-[var(--border)] dark:hover:text-white dark:hover:bg-[rgba(255,255,255,0.08)]",
  ghost: "bg-transparent text-gray-600 hover:text-gray-900 hover:bg-gray-100 dark:text-[var(--secondary-foreground)] dark:hover:text-white dark:hover:bg-[rgba(255,255,255,0.04)]",
  text: "bg-transparent text-gray-600 hover:text-gray-900 hover:bg-gray-100 dark:text-[var(--secondary-foreground)] dark:hover:text-white dark:hover:bg-[rgba(255,255,255,0.06)]",
  link: "text-blue-600 underline-offset-4 hover:underline dark:text-blue-400",
}

const buttonSizes = {
  default: "h-9 px-4 text-xs",
  sm: "h-8 px-3 text-[11px]",
  middle: "h-7 px-3 text-[11px]",
  lg: "h-10 px-6 text-sm",
  icon: "h-9 w-9",
}

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: keyof typeof buttonVariants
  size?: keyof typeof buttonSizes
  asChild?: boolean
}

const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "default", size = "default", asChild = false, children, ...props }, ref) => {
    const baseClassName = cn(
      "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/30 disabled:pointer-events-none disabled:opacity-40 cursor-pointer",
      buttonVariants[variant],
      buttonSizes[size],
      className
    )

    if (asChild && React.isValidElement(children)) {
      const child = children as React.ReactElement<{ className?: string }>
      return React.cloneElement(child, {
        ...props,
        className: cn(baseClassName, child.props.className),
      } as any)
    }

    return (
      <button
        className={baseClassName}
        ref={ref}
        {...props}
      >
        {children}
      </button>
    )
  }
)
Button.displayName = "Button"

export { Button, buttonVariants }
