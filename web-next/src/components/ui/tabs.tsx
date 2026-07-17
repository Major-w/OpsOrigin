'use client'

import * as React from "react"
import { cn } from "@/lib/utils"

const TabsContext = React.createContext<{ value: string; onChange: (v: string) => void } | null>(null)

interface TabsProps {
  value?: string
  defaultValue?: string
  onValueChange?: (value: string) => void
  children: React.ReactNode
  className?: string
}

export function Tabs({ value, defaultValue, onValueChange, children, className }: TabsProps) {
  const [internalValue, setInternalValue] = React.useState(defaultValue ?? "")
  const activeValue = value ?? internalValue

  const handleChange = (nextValue: string) => {
    if (value === undefined) {
      setInternalValue(nextValue)
    }
    onValueChange?.(nextValue)
  }

  return <TabsContext.Provider value={{ value: activeValue, onChange: handleChange }}><div className={className}>{children}</div></TabsContext.Provider>
}

export function TabsList({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("inline-flex h-10 items-center gap-1 rounded-md bg-muted p-1 text-muted-foreground", className)}>{children}</div>
}

export function TabsTrigger({ value, children, className, onClick }: { value: string; children: React.ReactNode; className?: string; onClick?: () => void }) {
  const ctx = React.useContext(TabsContext)
  const active = ctx?.value === value
  return (
    <button className={cn("inline-flex items-center justify-center whitespace-nowrap rounded-sm px-3 py-1.5 text-sm font-medium transition-all cursor-pointer", active ? "bg-background text-foreground shadow-sm" : "hover:bg-background/50", className)} onClick={() => { ctx?.onChange(value); onClick?.() }}>
      {children}
    </button>
  )
}

export function TabsContent({ value, children, className }: { value: string; children: React.ReactNode; className?: string }) {
  const ctx = React.useContext(TabsContext)
  if (ctx?.value !== value) return null
  return <div className={cn("mt-2", className)}>{children}</div>
}
