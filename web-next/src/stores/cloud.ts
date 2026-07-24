'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import type { CloudProvider, CloudCredential, CloudResource, SyncResult } from '@/lib/cloud'

interface CloudState {
  providers: CloudProvider[]
  credentials: CloudCredential[]
  resources: CloudResource[]
  loading: boolean
  error: string | null
  fetchProviders: () => Promise<void>
  listCredentials: () => Promise<void>
  saveCredential: (data: CloudCredential) => Promise<boolean>
  deleteCredential: (id: string) => Promise<boolean>
  listResources: (data: {
    provider: string
    region: string
    types: string[]
    credential?: { access_key: string; secret_key: string; region?: string; extra?: Record<string, string> }
    credential_id?: string
  }) => Promise<CloudResource[]>
  syncToCMDB: (data: {
    provider: string
    region: string
    types: string[]
    credential?: { access_key: string; secret_key: string; region?: string; extra?: Record<string, string> }
    credential_id?: string
    resources?: CloudResource[]
  }) => Promise<SyncResult | null>
}

export const useCloudStore = create<CloudState>((set, get) => ({
  providers: [], credentials: [], resources: [], loading: false, error: null,

  fetchProviders: async () => {
    set({ loading: true })
    try {
      const res = await apiFetch<{ providers: CloudProvider[] }>('/cloud/providers')
      set({ providers: res.providers || [], loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载云商列表失败: ${e.message}`)
    }
  },

  listCredentials: async () => {
    try {
      const res = await apiFetch<{ credentials: CloudCredential[] }>('/cloud/credentials')
      set({ credentials: res.credentials || [] })
    } catch (e: any) {
      toast.error(`加载凭证失败: ${e.message}`)
    }
  },

  saveCredential: async (data) => {
    try {
      await apiFetch('/cloud/credentials', { method: 'POST', body: JSON.stringify(data) })
      toast.success('凭证已保存')
      await get().listCredentials()
      return true
    } catch (e: any) {
      toast.error(`保存凭证失败: ${e.message}`)
      return false
    }
  },

  deleteCredential: async (id) => {
    try {
      await apiFetch(`/cloud/credentials/${id}`, { method: 'DELETE' })
      toast.success('凭证已删除')
      await get().listCredentials()
      return true
    } catch (e: any) {
      toast.error(`删除凭证失败: ${e.message}`)
      return false
    }
  },

  listResources: async (data) => {
    try {
      const res = await apiFetch<{ resources: CloudResource[] }>('/cloud/resources/list', {
        method: 'POST',
        body: JSON.stringify(data),
      })
      const resources = res.resources || []
      set({ resources })
      return resources
    } catch (e: any) {
      toast.error(`发现资源失败: ${e.message}`)
      return []
    }
  },

  syncToCMDB: async (data) => {
    try {
      const res = await apiFetch<SyncResult>('/cloud/resources/sync', {
        method: 'POST',
        body: JSON.stringify(data),
      })
      return res
    } catch (e: any) {
      toast.error(`同步 CMDB 失败: ${e.message}`)
      return null
    }
  },
}))
