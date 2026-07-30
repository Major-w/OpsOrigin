'use client'

import { create } from 'zustand'
import { apiFetch, apiBase } from '@/lib/utils'
import { useAgentStore } from '@/stores/agent'
import { toast } from '@/stores/toast'

export interface FileItem {
  id: string; original_name: string; size_bytes: number
  checksum: string; checksum_algo: string; tenant_id: string; created_at: string
}

interface FileState {
  files: FileItem[]; loading: boolean
  fetch: () => Promise<void>
  upload: (file: File) => Promise<FileItem | null>
  remove: (id: string) => Promise<boolean>
}

export const useFileStore = create<FileState>((set, get) => ({
  files: [], loading: false,
  fetch: async () => {
    set({ loading: true })
    try {
      const files = await apiFetch<FileItem[]>('/files')
      set({ files, loading: false })
    } catch (e: any) { set({ loading: false }); toast.error('加载文件列表失败: ' + e.message) }
  },
  upload: async (file) => {
    const form = new FormData(); form.append('file', file)
    try {
      const token = typeof window !== 'undefined' ? localStorage.getItem('opsnexus_token') || '' : ''
      const res = await fetch(`${apiBase}/files/upload`, {
        method: 'POST', body: form,
        headers: { 'Authorization': `Bearer ${token}` },
      })
      if (!res.ok) throw new Error((await res.json()).error || 'upload failed')
      const meta = await res.json()
      toast.success(`文件已上传: ${meta.original_name}`)
      await get().fetch()
      return meta
    } catch (e: any) { toast.error('上传失败: ' + e.message); return null }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/files/${id}`, { method: 'DELETE' })
      toast.success('文件已删除')
      await get().fetch()
      return true
    } catch (e: any) { toast.error('删除失败: ' + e.message); return false }
  },
}))
