'use client'

import { create } from 'zustand'
import { apiBase } from '@/lib/utils'

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
    const token = localStorage.getItem('opsnexus_token')
    const userRaw = localStorage.getItem('opsnexus_user')
    if (token && userRaw) {
      try {
        set({ token, user: JSON.parse(userRaw) })
      } catch {
        localStorage.removeItem('opsnexus_token')
        localStorage.removeItem('opsnexus_user')
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
      localStorage.setItem('opsnexus_token', data.token)
      localStorage.setItem('opsnexus_user', JSON.stringify({ user_id: data.user_id, username: data.username, role: data.role, tenant_id: data.tenant_id }))
      set({ token: data.token, user: { user_id: data.user_id, username: data.username, role: data.role, tenant_id: data.tenant_id }, loading: false })
    } catch (e: any) {
      set({ error: e.message, loading: false })
      throw e
    }
  },

  logout: () => {
    localStorage.removeItem('opsnexus_token')
    localStorage.removeItem('opsnexus_user')
    set({ token: null, user: null })
  },

  isAuthenticated: () => {
    return get().token !== null
  },
}))
