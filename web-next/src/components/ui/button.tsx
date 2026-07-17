'use client'

import * as React from "react"
import { cn } from "@/lib/utils"

const buttonVariants = {
  default: "bg-emerald-50 text-emerald-600 border border-emerald-200 hover:bg-emerald-100 dark:bg-emerald-500/15 dark:text-emerald-400 dark:border-emerald-500/25 dark:hover:bg-emerald-500/20",
  primary: "bg-emerald-500 text-white font-semibold hover:bg-emerald-600 border border-emerald-500 shadow-sm dark:bg-emerald-500 dark:hover:bg-emerald-400 dark:text-[#0B0F19]",
  destructive: "bg-red-50 text-red-600 border border-red-200 hover:bg-red-100 dark:bg-red-500/15 dark:text-red-400 dark:border-red-500/25 dark:hover:bg-red-500/20",
  outline: "bg-transparent text-gray-600 border border-gray-200 hover:text-gray-900 hover:border-gray-300 dark:text-[#94A3B8] dark:border-[rgba(255,255,255,0.08)] dark:hover:text-white dark:hover:border-[rgba(255,255,255,0.15)]",
  secondary: "bg-gray-100 text-gray-600 border border-gray-200 hover:bg-gray-200 dark:bg-[rgba(255,255,255,0.04)] dark:text-[#94A3B8] dark:border-[rgba(255,255,255,0.06)] dark:hover:text-white dark:hover:bg-[rgba(255,255,255,0.08)]",
  ghost: "bg-transparent text-gray-600 hover:text-gray-900 hover:bg-gray-100 dark:text-[#94A3B8] dark:hover:text-white dark:hover:bg-[rgba(255,255,255,0.04)]",
  link: "text-emerald-600 underline-offset-4 hover:underline dark:text-emerald-400",
}

const buttonSizes = {
  default: "h-9 px-4 text-xs",
  sm: "h-8 px-3 text-[11px]",
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
      "inline-flex items-center justify-center gap-2 whitespace-nowrap rounded-lg font-medium transition-all duration-200 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-emerald-500/30 disabled:pointer-events-none disabled:opacity-40 cursor-pointer",
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
