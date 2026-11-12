
import { create } from 'zustand'
import { apiFetch, apiList, apiFetchRaw } from '@/lib/utils'
import { getMessage } from '@/lib/message'
export interface User {
  id: string
  username: string
  email?: string
  department?: string
  role: string
  status: string
  last_login?: string
  created_at: string
  updated_at: string
}

/** 用户批量导入结果（accounts 仅含自动生成初始密码的新账号） */
export interface UserImportResult {
  created: number
  skipped: number
  failed: number
  errors?: { row: number; message: string }[]
  accounts?: { username: string; initial_password?: string }[]
}

interface UserState {
  users: User[]
  loading: boolean
  fetch: () => Promise<void>
  create: (data: { username: string; password: string; email?: string; department?: string; role?: string }) => Promise<boolean>
  update: (id: string, data: Partial<User>) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  changePassword: (id: string, oldPassword: string, newPassword: string) => Promise<boolean>
  /** CSV 批量导入用户（username,email,department,role,status,password），返回结果详情；失败返回 null */
  importUsers: (file: File) => Promise<UserImportResult | null>
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
      getMessage().error(`加载用户列表失败: ${e.message}`)
    }
  },

  create: async (data) => {
    try {
      await apiFetch('/users', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('用户创建成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`创建用户失败: ${e.message}`)
      return false
    }
  },

  update: async (id, data) => {
    try {
      await apiFetch(`/users/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      getMessage().success('用户更新成功')
      await get().fetch()
      return true
    } catch (e: any) {
      getMessage().error(`更新用户失败: ${e.message}`)
      return false
    }
  },

  remove: async (id) => {
    try {
      await apiFetch(`/users/${id}`, { method: 'DELETE' })
      set(s => ({ users: s.users.filter(u => u.id !== id) }))
      getMessage().success('用户已删除')
      return true
    } catch (e: any) {
      getMessage().error(`删除用户失败: ${e.message}`)
      return false
    }
  },

  changePassword: async (id, oldPassword, newPassword) => {
    try {
      await apiFetch(`/users/${id}/password`, {
        method: 'PUT',
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      })
      getMessage().success('密码修改成功')
      return true
    } catch (e: any) {
      getMessage().error(`修改密码失败: ${e.message}`)
      return false
    }
  },

  importUsers: async (file) => {
    const fd = new FormData()
    fd.append('file', file)
    try {
      const res = await apiFetchRaw('/users/import', { method: 'POST', body: fd })
      if (!res.ok) {
        throw new Error((await res.json().catch(() => ({}))).error || `HTTP ${res.status}`)
      }
      const data = await res.json() as UserImportResult
      await get().fetch()
      return data
    } catch (e: any) {
      getMessage().error(`导入用户失败: ${e.message}`)
      return null
    }
  },
}))
