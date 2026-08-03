'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'

interface PermissionsState {
  permissions: string[]
  loaded: boolean
  fetchMyPermissions: () => Promise<void>
}

// usePermissions 持有当前登录用户的有效权限(来自 GET /iam/me/permissions,
// 由后端聚合主角色 + 已分配 IAM 角色,与 RBAC 中间件同源)。
// 失败时保持未加载,useCan 乐观放行——行为与未接入权限感知时一致,不会更差。
export const usePermissions = create<PermissionsState>((set) => ({
  permissions: [],
  loaded: false,
  fetchMyPermissions: async () => {
    try {
      const data = await apiFetch<{ permissions: string[] }>('/iam/me/permissions')
      set({ permissions: data.permissions || [], loaded: true })
    } catch {
      // 取数失败:不置 loaded,useCan 继续乐观放行(避免误隐藏全员按钮)
    }
  },
}))

// useCan 判断当前用户是否拥有某权限。加载完成前乐观放行(避免闪烁/误隐藏)。
export function useCan(perm: string): boolean {
  const permissions = usePermissions((s) => s.permissions)
  const loaded = usePermissions((s) => s.loaded)
  if (!loaded) return true
  return permissions.includes(perm)
}

// useCanAny 判断当前用户是否拥有给定权限中的任意一个(用于「读或写」类按钮)。
export function useCanAny(perms: string[]): boolean {
  const permissions = usePermissions((s) => s.permissions)
  const loaded = usePermissions((s) => s.loaded)
  if (!loaded) return true
  return perms.some(p => permissions.includes(p))
}
