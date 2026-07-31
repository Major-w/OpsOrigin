'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface AuditActor {
  type: string
  id: string
  display_name?: string
  ticket_id?: string
}

export interface AuditTarget {
  type: string
  id: string
  name?: string
}

export interface AuditEvent {
  id: string
  trace_id: string
  tenant_id: string
  event_type: string
  actor: AuditActor
  target: AuditTarget
  action: string
  request?: any
  response?: any
  success: boolean
  error?: string
  ip_address?: string
  user_agent?: string
  timestamp: string
}

/** 审计查询参数 */
export interface AuditQuery {
  q?: string          // 关键词(动作/目标/Trace)
  event_type?: string
  actor_type?: string
  actor_id?: string   // 操作主体搜索
  target_type?: string
  success?: string    // '' | 'true' | 'false'
  from?: string       // ISO 日期
  to?: string
  page?: number       // 1-based
  page_size?: number
}

/** 分页查询响应 */
export interface AuditPage {
  items: AuditEvent[]
  total: number
  limit: number
  offset: number
}

interface AuditState {
  events: AuditEvent[]
  total: number
  loading: boolean
  error: string | null
  fetch: (query?: AuditQuery) => Promise<void>
  /** 拉取全部事件类型(去重), 供筛选下拉使用 */
  fetchEventTypes: () => Promise<string[]>
}

export const useAuditStore = create<AuditState>((set) => ({
  events: [], total: 0, loading: false, error: null,

  fetch: async (query) => {
    set({ loading: true })
    try {
      const params = new URLSearchParams()
      const q = query || {}
      if (q.q) params.set('q', q.q)
      if (q.event_type) params.set('event_type', q.event_type)
      if (q.actor_type) params.set('actor_type', q.actor_type)
      if (q.actor_id) params.set('actor_id', q.actor_id)
      if (q.target_type) params.set('target_type', q.target_type)
      if (q.success) params.set('success', q.success)
      if (q.from) params.set('from', new Date(q.from).toISOString())
      if (q.to) params.set('to', new Date(new Date(q.to).getTime() + 24 * 3600 * 1000 - 1).toISOString())
      const page = q.page || 1
      const pageSize = q.page_size || 50
      params.set('limit', String(pageSize))
      params.set('offset', String((page - 1) * pageSize))
      const data = await apiFetch<AuditPage>(`/audit/events?${params.toString()}`)
      set({ events: data?.items || [], total: data?.total || 0, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载审计事件失败: ${e.message}`)
    }
  },

  fetchEventTypes: async () => {
    try {
      const data = await apiFetch<AuditEvent[]>('/audit/events?limit=200')
      const types = Array.from(new Set((data || []).map(e => e.event_type).filter(Boolean)))
      return types.sort()
    } catch {
      return []
    }
  },
}))
