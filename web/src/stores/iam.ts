
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
export interface Permission {
  id?: string
  name: string
  description: string
  category: string
}

export interface Role {
  id: string
  name: string
  display_name: string
  description?: string
  permissions: string[]
  is_built_in: boolean
  created_at: string
  updated_at: string
}

export interface Group {
  id: string
  name: string
  description?: string
  user_ids: string[]
  created_at: string
  updated_at: string
}

export interface UserRoles {
  user_id: string
  role_ids: string[]
  roles?: Role[]
}

interface IAMState {
  permissions: Permission[]
  roles: Role[]
  groups: Group[]
  loading: boolean
  fetchPermissions: () => Promise<void>
  fetchRoles: () => Promise<void>
  createRole: (data: { name: string; display_name: string; description?: string; permissions: string[] }) => Promise<string | null>
  updateRole: (id: string, data: { display_name: string; description?: string; permissions: string[] }) => Promise<boolean>
  deleteRole: (id: string) => Promise<boolean>
  fetchGroups: () => Promise<void>
  createGroup: (data: { name: string; description?: string; user_ids: string[] }) => Promise<string | null>
  updateGroup: (id: string, data: { name: string; description?: string; user_ids: string[] }) => Promise<boolean>
  deleteGroup: (id: string) => Promise<boolean>
  fetchUserRoles: (userId: string) => Promise<UserRoles | null>
  assignUserRoles: (userId: string, roleIds: string[]) => Promise<boolean>
}

export const useIAMStore = create<IAMState>((set, get) => ({
  permissions: [],
  roles: [],
  groups: [],
  loading: false,

  fetchPermissions: async () => {
    try {
      const permissions = await apiList<Permission>('/iam/permissions')
      set({ permissions })
    } catch (e: any) {
      getMessage().error(`加载权限列表失败: ${e.message}`)
    }
  },

  fetchRoles: async () => {
    set({ loading: true })
    try {
      const roles = await apiList<Role>('/iam/roles')
      set({ roles, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载角色列表失败: ${e.message}`)
    }
  },

  createRole: async (data) => {
    try {
      const role = await apiFetch<Role>('/iam/roles', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('角色创建成功')
      await get().fetchRoles()
      return role.id
    } catch (e: any) {
      getMessage().error(`创建角色失败: ${e.message}`)
      return null
    }
  },

  updateRole: async (id, data) => {
    try {
      await apiFetch(`/iam/roles/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      getMessage().success('角色更新成功')
      await get().fetchRoles()
      return true
    } catch (e: any) {
      getMessage().error(`更新角色失败: ${e.message}`)
      return false
    }
  },

  deleteRole: async (id) => {
    try {
      await apiFetch(`/iam/roles/${id}`, { method: 'DELETE' })
      set(s => ({ roles: s.roles.filter(r => r.id !== id) }))
      getMessage().success('角色已删除')
      return true
    } catch (e: any) {
      getMessage().error(`删除角色失败: ${e.message}`)
      return false
    }
  },

  fetchGroups: async () => {
    set({ loading: true })
    try {
      const groups = await apiList<Group>('/iam/groups')
      set({ groups, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载用户组列表失败: ${e.message}`)
    }
  },

  createGroup: async (data) => {
    try {
      const group = await apiFetch<Group>('/iam/groups', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('用户组创建成功')
      await get().fetchGroups()
      return group.id
    } catch (e: any) {
      getMessage().error(`创建用户组失败: ${e.message}`)
      return null
    }
  },

  updateGroup: async (id, data) => {
    try {
      await apiFetch(`/iam/groups/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      getMessage().success('用户组更新成功')
      await get().fetchGroups()
      return true
    } catch (e: any) {
      getMessage().error(`更新用户组失败: ${e.message}`)
      return false
    }
  },

  deleteGroup: async (id) => {
    try {
      await apiFetch(`/iam/groups/${id}`, { method: 'DELETE' })
      set(s => ({ groups: s.groups.filter(g => g.id !== id) }))
      getMessage().success('用户组已删除')
      return true
    } catch (e: any) {
      getMessage().error(`删除用户组失败: ${e.message}`)
      return false
    }
  },

  fetchUserRoles: async (userId) => {
    try {
      return await apiFetch<UserRoles>(`/iam/users/${userId}/roles`)
    } catch (e: any) {
      getMessage().error(`加载用户角色失败: ${e.message}`)
      return null
    }
  },

  assignUserRoles: async (userId, roleIds) => {
    try {
      await apiFetch(`/iam/users/${userId}/roles`, { method: 'PUT', body: JSON.stringify({ role_ids: roleIds }) })
      getMessage().success('角色分配成功')
      return true
    } catch (e: any) {
      getMessage().error(`角色分配失败: ${e.message}`)
      return false
    }
  },
}))
