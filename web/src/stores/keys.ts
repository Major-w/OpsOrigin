
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
export interface KeyEntry {
  id: string; name: string; type: string; content: string
  fingerprint: string; size: number; algorithm?: string
  username?: string; host?: string; port?: string
  created_at: string; updated_at: string
}

interface KeyState {
  keys: KeyEntry[]; loading: boolean
  fetch: (type?: string) => Promise<void>
  create: (data: { name: string; type: string; content: string; username?: string; host?: string; port?: string }) => Promise<boolean>
  update: (id: string, data: { name?: string; type?: string; content?: string; username?: string; host?: string; port?: string }) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
}

export const useKeyStore = create<KeyState>((set, get) => ({
  keys: [], loading: false,
  fetch: async (type) => {
    set({ loading: true })
    try {
      const qs = type ? `?type=${type}` : ''
      const keys = await apiList<KeyEntry>(`/keys${qs}`)
      set({ keys, loading: false })
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载凭证列表失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/keys', { method: 'POST', body: JSON.stringify(data) })
      message.success('创建凭证成功')
      await get().fetch()
      return true
    } catch (e: any) {
      message.error(`创建凭证失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/keys/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      message.success('更新凭证成功')
      await get().fetch()
      return true
    } catch (e: any) {
      message.error(`更新凭证失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/keys/${id}`, { method: 'DELETE' })
      set(s => ({ keys: s.keys.filter(k => k.id !== id) }))
      message.success('删除凭证成功')
      return true
    } catch (e: any) {
      message.error(`删除凭证失败: ${e.message}`)
      return false
    }
  },
}))
