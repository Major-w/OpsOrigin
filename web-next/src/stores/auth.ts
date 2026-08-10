'use client'

import { create } from 'zustand'
import { apiBase } from '@/lib/utils'
import { usePermissions } from '@/stores/permissions'

interface User {
  user_id: string
  username: string
  role: string
  tenant_id: string
}

interface AuthState {
  token: string | null
  user: User | null
  loading: boolean
  error: string | null
  login: (username: string, password: string) => Promise<void>
  logout: () => void
  isAuthenticated: () => boolean
  init: () => void
}

export const useAuthStore = create<AuthState>((set, get) => ({
  token: null,
  user: null,
  loading: false,
  error: null,

  init: () => {
    if (typeof window === 'undefined') return
    const token = localStorage.getItem('opsorigin_token')
    const userRaw = localStorage.getItem('opsorigin_user')
    if (token && userRaw) {
      try {
        set({ token, user: JSON.parse(userRaw) })
        // 本地 token 恢复(刷新/重开页面):同步拉取权限,保证 useCan 在首屏即生效
        usePermissions.getState().fetchMyPermissions()
      } catch {
        localStorage.removeItem('opsorigin_token')
        localStorage.removeItem('opsorigin_user')
      }
    }
  },

  login: async (username: string, password: string) => {
    set({ loading: true, error: null })
    try {
      const res = await fetch(`${apiBase}/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        throw new Error(body.error || 'login failed')
      }
      const data = await res.json()
      localStorage.setItem('opsorigin_token', data.token)
      localStorage.setItem('opsorigin_user', JSON.stringify({ user_id: data.user_id, username: data.username, role: data.role, tenant_id: data.tenant_id }))
      set({ token: data.token, user: { user_id: data.user_id, username: data.username, role: data.role, tenant_id: data.tenant_id }, loading: false })
      // 登录成功后拉取当前用户有效权限,供 useCan 做模块级按钮权限感知
      usePermissions.getState().fetchMyPermissions()
    } catch (e: any) {
      set({ error: e.message, loading: false })
      throw e
    }
  },

  logout: () => {
    localStorage.removeItem('opsorigin_token')
    localStorage.removeItem('opsorigin_user')
    set({ token: null, user: null })
  },

  isAuthenticated: () => {
    return get().token !== null
  },
}))
