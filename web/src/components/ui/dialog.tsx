
import * as React from "react"
import { cn } from "@/lib/utils"

export function Dialog({ open, onClose, children, title, className }: { open: boolean; onClose: () => void; children: React.ReactNode; title?: string; className?: string }) {
  if (!open) return null
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="fixed inset-0 bg-black/50 backdrop-blur-[2px]" onClick={onClose} />
      {/* 弹窗高度受限 + 内部滚动: 长表单 / 版本差异对比不会撑破视口 */}
      <div className={cn("relative z-50 w-full max-w-lg max-h-[88vh] overflow-y-auto rounded-xl border bg-background p-6 shadow-2xl", className)}>
        {title && <h2 className="text-lg font-semibold mb-4">{title}</h2>}
        {children}
      </div>
    </div>
  )
}

export function DialogContent({ className, children, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("relative z-50 w-full max-w-lg rounded-xl border bg-background p-6 shadow-lg", className)} {...props}>{children}</div>
}

export function DialogHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-col space-y-1.5 text-center sm:text-left mb-4", className)} {...props} />
}

export function DialogTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h2 className={cn("text-lg font-semibold leading-none tracking-tight", className)} {...props} />
}

export function DialogFooter({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex justify-end gap-2 mt-6", className)} {...props} />
}
