'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface Article { id: string; title: string; content: string; category: string; tags?: string[]; author: string; views: number; created_at: string }

interface KnowledgeState {
  articles: Article[]; loading: boolean
  fetch: (category?: string) => Promise<void>
  create: (data: any) => Promise<boolean>
  update: (id: string, data: any) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
}

export const useKnowledgeStore = create<KnowledgeState>((set, get) => ({
  articles: [], loading: false,
  fetch: async (category) => {
    set({ loading: true })
    try {
      const qs = category ? `?category=${encodeURIComponent(category)}` : ''
      const articles = await apiFetch<Article[]>(`/knowledge${qs}`)
      set({ articles, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载知识库文章失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/knowledge', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建文章成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`创建文章失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/knowledge/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('更新文章成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`更新文章失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/knowledge/${id}`, { method: 'DELETE' })
      set(s => ({ articles: s.articles.filter(a => a.id !== id) }))
      toast.success('删除文章成功')
      return true
    } catch (e: any) {
      toast.error(`删除文章失败: ${e.message}`)
      return false
    }
  },
}))
