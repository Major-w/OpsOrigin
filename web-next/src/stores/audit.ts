'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface AuditEvent {
  id: string
  trace_id: string
  tenant_id: string
  event_type: string
  actor: { type: string; id: string }
  target: { type: string; id: string }
  action: string
  success: boolean
  error?: string
  timestamp: string
}

interface AuditState {
  events: AuditEvent[]
  loading: boolean
  error: string | null
  fetch: () => Promise<void>
}

export const useAuditStore = create<AuditState>((set) => ({
  events: [], loading: false, error: null,
  fetch: async () => {
    set({ loading: true })
    try {
      const events = await apiFetch<AuditEvent[]>('/audit/events')
      set({ events, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载审计事件失败: ${e.message}`)
    }
  },
}))
