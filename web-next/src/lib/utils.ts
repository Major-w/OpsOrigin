'use client'

import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export function formatTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN')
}

export const apiBase = '/api/v1'

function getToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem('opsnexus_token')
}

function getPrincipal(): { type: string; id: string } | null {
  if (typeof window === 'undefined') return null
  const userStr = localStorage.getItem('opsnexus_user')
  if (userStr) {
    try {
      const user = JSON.parse(userStr)
      return { type: 'HUMAN', id: user.user_id || user.id || user.username || 'unknown' }
    } catch {}
  }
  return null
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    'X-Trace-ID': typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2),
  }
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  const principal = getPrincipal()
  if (principal) {
    headers['X-Principal-Type'] = principal.type
    headers['X-Principal-ID'] = principal.id
  }

  const res = await fetch(`${apiBase}${path}`, { headers, ...init })
  if (res.status === 401) {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('opsnexus_token')
      localStorage.removeItem('opsnexus_user')
      window.location.href = '/login'
    }
    throw new Error('unauthorized')
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    throw new Error(body.error || `HTTP ${res.status}`)
  }
  if (res.status === 204) return undefined as T
  return res.json()
}
