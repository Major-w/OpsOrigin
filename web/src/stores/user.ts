
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
export interface User {
  id: string
  username: string
  email?: string
  role: string
  status: string
  last_login?: string
  created_at: string
  updated_at: string
}

interface UserState {
  users: User[]
  loading: boolean
  fetch: () => Promise<void>
  create: (data: { username: string; password: string; email?: string; role?: string }) => Promise<boolean>
  update: (id: string, data: Partial<User>) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  changePassword: (id: string, oldPassword: string, newPassword: string) => Promise<boolean>
}

export const useUserStore = create<UserState>((set, get) => ({
  users: [],
  loading: false,

  fetch: async () => {
    set({ loading: true })
    try {
      const users = await apiList<User>('/users')
      set({ users, loading: false })
    } catch (e: any) {
      set({ loading: false })
      message.error(`加载用户列表失败: ${e.message}`)
    }
  },

  create: async (data) => {
    try {
      await apiFetch('/users', { method: 'POST', body: JSON.stringify(data) })
      message.success('用户创建成功')
      await get().fetch()
      return true
    } catch (e: any) {
      message.error(`创建用户失败: ${e.message}`)
      return false
    }
  },

  update: async (id, data) => {
    try {
      await apiFetch(`/users/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      message.success('用户更新成功')
      await get().fetch()
      return true
    } catch (e: any) {
      message.error(`更新用户失败: ${e.message}`)
      return false
    }
  },

  remove: async (id) => {
    try {
      await apiFetch(`/users/${id}`, { method: 'DELETE' })
      set(s => ({ users: s.users.filter(u => u.id !== id) }))
      message.success('用户已删除')
      return true
    } catch (e: any) {
      message.error(`删除用户失败: ${e.message}`)
      return false
    }
  },

  changePassword: async (id, oldPassword, newPassword) => {
    try {
      await apiFetch(`/users/${id}/password`, {
        method: 'PUT',
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      })
      message.success('密码修改成功')
      return true
    } catch (e: any) {
      message.error(`修改密码失败: ${e.message}`)
      return false
    }
  },
}))
