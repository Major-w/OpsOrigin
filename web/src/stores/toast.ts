
import { create } from 'zustand'

export type ToastType = 'success' | 'error' | 'info' | 'warning'

export interface Toast {
  id: string
  type: ToastType
  message: string
  duration?: number
}

interface ToastState {
  toasts: Toast[]
  show: (type: ToastType, message: string, duration?: number) => void
  dismiss: (id: string) => void
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  show: (type, message, duration = 4000) => {
    const id = typeof crypto !== 'undefined' && crypto.randomUUID
      ? crypto.randomUUID()
      : Math.random().toString(36).slice(2)
    set(s => ({ toasts: [...s.toasts, { id, type, message, duration }] }))
    if (duration > 0) {
      setTimeout(() => {
        set(s => ({ toasts: s.toasts.filter(t => t.id !== id) }))
      }, duration)
    }
  },
  dismiss: (id) => set(s => ({ toasts: s.toasts.filter(t => t.id !== id) })),
}))

// 便捷函数,可在非组件代码中调用(如 store 的 catch 块)
export const toast = {
  success: (msg: string) => useToastStore.getState().show('success', msg),
  error: (msg: string) => useToastStore.getState().show('error', msg),
  info: (msg: string) => useToastStore.getState().show('info', msg),
  warning: (msg: string) => useToastStore.getState().show('warning', msg),
}
