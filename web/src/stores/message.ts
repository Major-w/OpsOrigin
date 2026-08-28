
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'

export type MessageCategory = 'ticket' | 'approval' | 'mention' | 'subtask' | 'sla' | 'system'
export type MessageLevel = 'info' | 'warning' | 'critical'

// InboxMessage 与后端 message.Message 对齐(收件箱条目)。
export interface InboxMessage {
  id: string
  tenant_id: string
  user_id?: string
  username?: string
  category: MessageCategory
  level: MessageLevel
  title: string
  content?: string
  link?: string
  ref_type?: string
  ref_id?: string
  actor?: string
  read: boolean
  read_at?: string
  created_at: string
}

// 分类 -> 中文标签。
export const MESSAGE_CATEGORY_LABELS: Record<MessageCategory, string> = {
  ticket: '工单',
  approval: '审批',
  mention: '提及',
  subtask: '子任务',
  sla: 'SLA',
  system: '系统',
}

// 分类 -> 角标配色(图标/文字)。
export const MESSAGE_CATEGORY_COLORS: Record<MessageCategory, string> = {
  ticket: 'text-blue-500',
  approval: 'text-violet-500',
  mention: 'text-blue-500',
  subtask: 'text-amber-500',
  sla: 'text-red-500',
  system: 'text-gray-500',
}

interface MessageState {
  messages: InboxMessage[]
  unreadCount: number
  loading: boolean
  open: boolean
  // 最近一次加载时间, 用于轮询时判断是否需要刷新列表。
  loadedAt: number

  loadRecent: (limit?: number) => Promise<void>
  loadUnreadCount: () => Promise<void>
  markRead: (id: string) => Promise<void>
  markAllRead: () => Promise<void>
  remove: (id: string) => Promise<void>
  // pushIncoming 由 WebSocket 实时事件调用: 未读数 +1, 若面板已打开则插入列表顶部。
  pushIncoming: (m: InboxMessage) => void
  setOpen: (open: boolean) => void
}

export const useMessageStore = create<MessageState>((set, get) => ({
  messages: [],
  unreadCount: 0,
  loading: false,
  open: false,
  loadedAt: 0,

  loadRecent: async (limit = 20) => {
    set({ loading: true })
    try {
      const items = await apiList<InboxMessage>(`/messages?limit=${limit}`)
      set({ messages: items, loading: false, loadedAt: Date.now() })
    } catch {
      set({ loading: false })
    }
  },

  loadUnreadCount: async () => {
    try {
      const res = await apiFetch<{ count: number }>('/messages/unread-count')
      set({ unreadCount: res.count })
    } catch {
      /* 网络抖动不阻断主流程, 下次轮询或 WS 事件会再更新 */
    }
  },

  markRead: async (id: string) => {
    try {
      await apiFetch(`/messages/${id}/read`, { method: 'PUT' })
      set(s => ({
        messages: s.messages.map(m => (m.id === id ? { ...m, read: true } : m)),
        unreadCount: Math.max(0, s.unreadCount - 1),
      }))
    } catch {
      /* ignore */
    }
  },

  markAllRead: async () => {
    try {
      await apiFetch('/messages/read-all', { method: 'PUT' })
      set(s => ({
        messages: s.messages.map(m => ({ ...m, read: true })),
        unreadCount: 0,
      }))
    } catch {
      /* ignore */
    }
  },

  remove: async (id: string) => {
    try {
      await apiFetch(`/messages/${id}`, { method: 'DELETE' })
      set(s => ({ messages: s.messages.filter(m => m.id !== id) }))
    } catch {
      /* ignore */
    }
  },

  pushIncoming: (m: InboxMessage) => {
    set(s => {
      // 去重: 同一 id 不重复插入。
      if (s.messages.some(x => x.id === m.id)) {
        return { unreadCount: s.unreadCount + (m.read ? 0 : 1) }
      }
      const list = s.open ? [m, ...s.messages] : s.messages
      return { messages: list, unreadCount: s.unreadCount + (m.read ? 0 : 1), loadedAt: s.open ? Date.now() : s.loadedAt }
    })
  },

  setOpen: (open: boolean) => {
    set({ open })
    if (open) {
      void get().loadRecent()
    }
  },
}))
