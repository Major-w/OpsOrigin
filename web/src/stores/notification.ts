
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
// 通知渠道类型(与后端 ChannelType 一一对应)
export type ChannelType = 'email' | 'dingtalk' | 'wecom' | 'feishu' | 'webhook' | 'script'

// 事件类型(8 种平台事件)
export type EventType =
  | 'alert.triggered'
  | 'alert.recovered'
  | 'ticket.created'
  | 'ticket.assigned'
  | 'ticket.approved'
  | 'agent.offline'
  | 'workflow.waiting'
  | 'workflow.failed'

// 渠道配置字段元信息(由后端 RequiredFields 提供,供前端动态渲染表单)
export interface FieldSpec {
  name: string
  label: string
  placeholder: string
  required: boolean
  secret: boolean // 是否为敏感字段(密码/token),展示时掩码
}

// 渠道类型元信息(包含该类型的字段规格列表)
export interface ChannelTypeMeta {
  type: ChannelType
  required_fields: FieldSpec[]
}

// 通知渠道:一个可发送通知的外部目的地配置
export interface Channel {
  id: string
  tenant_id: string
  name: string
  type: ChannelType
  config: Record<string, string>
  enabled: boolean
  created_at: string
  updated_at: string
}

// 通知策略:事件 -> 渠道的订阅规则
export interface Policy {
  id: string
  tenant_id: string
  name: string
  event: EventType
  channel_ids: string[]
  template?: string
  quiet_hours?: string
  enabled: boolean
  created_at: string
  updated_at: string
}

// 通知发送历史记录
export interface NotificationRecord {
  id: string
  tenant_id: string
  channel_id: string
  event: EventType | ''
  title: string
  content: string
  status: 'sent' | 'failed'
  error?: string
  created_at: string
}

interface NotificationState {
  channels: Channel[]
  policies: Policy[]
  notifications: NotificationRecord[]
  channelTypes: ChannelTypeMeta[]
  events: EventType[]
  loading: boolean

  // 渠道
  fetchChannels: () => Promise<void>
  createChannel: (data: { name: string; type: ChannelType; config: Record<string, string>; enabled: boolean }) => Promise<string | null>
  updateChannel: (id: string, data: { name: string; config: Record<string, string>; enabled: boolean }) => Promise<boolean>
  deleteChannel: (id: string) => Promise<boolean>
  testChannel: (id: string, title?: string, content?: string) => Promise<{ success: boolean; error?: string } | null>

  // 策略
  fetchPolicies: () => Promise<void>
  createPolicy: (data: { name: string; event: EventType; channel_ids: string[]; template?: string; quiet_hours?: string; enabled: boolean }) => Promise<string | null>
  updatePolicy: (id: string, data: { name: string; event: EventType; channel_ids: string[]; template?: string; quiet_hours?: string; enabled: boolean }) => Promise<boolean>
  deletePolicy: (id: string) => Promise<boolean>

  // 元信息
  fetchChannelTypes: () => Promise<void>
  fetchEvents: () => Promise<void>

  // 历史
  fetchHistory: (limit?: number) => Promise<void>

  // 事件触发
  sendEvent: (event: EventType, title: string, content: string) => Promise<{ sent: number; failed: number } | null>
}

export const useNotificationStore = create<NotificationState>((set, get) => ({
  channels: [],
  policies: [],
  notifications: [],
  channelTypes: [],
  events: [],
  loading: false,

  // ---------- 渠道 ----------

  fetchChannels: async () => {
    set({ loading: true })
    try {
      const channels = await apiList<Channel>('/notification/channels')
      set({ channels, loading: false })
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载通知渠道失败: ${e.message}`)
    }
  },

  createChannel: async (data) => {
    try {
      const channel = await apiFetch<Channel>('/notification/channels', { method: 'POST', body: JSON.stringify(data) })
      message.success('通知渠道创建成功')
      await get().fetchChannels()
      return channel.id
    } catch (e: any) {
      message.error(`创建通知渠道失败: ${e.message}`)
      return null
    }
  },

  updateChannel: async (id, data) => {
    try {
      await apiFetch(`/notification/channels/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      message.success('通知渠道更新成功')
      await get().fetchChannels()
      return true
    } catch (e: any) {
      message.error(`更新通知渠道失败: ${e.message}`)
      return false
    }
  },

  deleteChannel: async (id) => {
    try {
      await apiFetch(`/notification/channels/${id}`, { method: 'DELETE' })
      set(s => ({ channels: s.channels.filter(c => c.id !== id) }))
      message.success('通知渠道已删除')
      return true
    } catch (e: any) {
      message.error(`删除通知渠道失败: ${e.message}`)
      return false
    }
  },

  testChannel: async (id, title, content) => {
    try {
      const res = await apiFetch<{ success: boolean; error?: string }>(`/notification/channels/${id}/test`, {
        method: 'POST',
        body: JSON.stringify({ title, content }),
      })
      if (res.success) {
        message.success('测试通知发送成功')
      } else {
        message.error(`测试发送失败: ${res.error || '未知错误'}`)
      }
      return res
    } catch (e: any) {
      message.error(`测试发送异常: ${e.message}`)
      return null
    }
  },

  // ---------- 策略 ----------

  fetchPolicies: async () => {
    set({ loading: true })
    try {
      const policies = await apiList<Policy>('/notification/policies')
      set({ policies, loading: false })
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载通知策略失败: ${e.message}`)
    }
  },

  createPolicy: async (data) => {
    try {
      const policy = await apiFetch<Policy>('/notification/policies', { method: 'POST', body: JSON.stringify(data) })
      message.success('通知策略创建成功')
      await get().fetchPolicies()
      return policy.id
    } catch (e: any) {
      message.error(`创建通知策略失败: ${e.message}`)
      return null
    }
  },

  updatePolicy: async (id, data) => {
    try {
      await apiFetch(`/notification/policies/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      message.success('通知策略更新成功')
      await get().fetchPolicies()
      return true
    } catch (e: any) {
      message.error(`更新通知策略失败: ${e.message}`)
      return false
    }
  },

  deletePolicy: async (id) => {
    try {
      await apiFetch(`/notification/policies/${id}`, { method: 'DELETE' })
      set(s => ({ policies: s.policies.filter(p => p.id !== id) }))
      message.success('通知策略已删除')
      return true
    } catch (e: any) {
      message.error(`删除通知策略失败: ${e.message}`)
      return false
    }
  },

  // ---------- 元信息 ----------

  fetchChannelTypes: async () => {
    try {
      const types = await apiList<ChannelTypeMeta>('/notification/channel-types')
      set({ channelTypes: types })
    } catch (e: any) {
      message.error(`加载渠道类型失败: ${e.message}`)
    }
  },

  fetchEvents: async () => {
    try {
      const events = await apiList<EventType>('/notification/events')
      set({ events })
    } catch (e: any) {
      message.error(`加载事件类型失败: ${e.message}`)
    }
  },

  // ---------- 历史 ----------

  fetchHistory: async (limit = 100) => {
    try {
      const notifications = await apiList<NotificationRecord>(`/notification/history?limit=${limit}`)
      set({ notifications })
    } catch (e: any) {
      message.error(`加载发送历史失败: ${e.message}`)
    }
  },

  // ---------- 事件触发 ----------

  sendEvent: async (event, title, content) => {
    try {
      const res = await apiFetch<{ sent: number; failed: number }>('/notification/send', {
        method: 'POST',
        body: JSON.stringify({ event, title, content }),
      })
      if (res.failed > 0) {
        message.warning(`事件已分发: 成功 ${res.sent} 条,失败 ${res.failed} 条`)
      } else {
        message.success(`事件已分发至 ${res.sent} 个渠道`)
      }
      await get().fetchHistory()
      return res
    } catch (e: any) {
      message.error(`事件触发失败: ${e.message}`)
      return null
    }
  },
}))

// 事件类型 -> 中文标签映射
export const EVENT_LABELS: Record<EventType, string> = {
  'alert.triggered': '告警触发',
  'alert.recovered': '告警恢复',
  'ticket.created': '工单创建',
  'ticket.assigned': '工单指派',
  'ticket.approved': '工单审批通过',
  'agent.offline': 'Agent 离线',
  'workflow.waiting': '工作流等待审批',
  'workflow.failed': '工作流执行失败',
}

// 渠道类型 -> 中文标签映射
export const CHANNEL_TYPE_LABELS: Record<ChannelType, string> = {
  email: '邮件 (SMTP)',
  dingtalk: '钉钉机器人',
  wecom: '企业微信群机器人',
  feishu: '飞书自定义机器人',
  webhook: '通用 Webhook',
  script: '脚本执行',
}

// 渠道类型对应的图标颜色(用于展示)
export const CHANNEL_TYPE_COLORS: Record<ChannelType, string> = {
  email: 'text-blue-500',
  dingtalk: 'text-cyan-500',
  wecom: 'text-blue-500',
  feishu: 'text-violet-500',
  webhook: 'text-amber-500',
  script: 'text-pink-500',
}
