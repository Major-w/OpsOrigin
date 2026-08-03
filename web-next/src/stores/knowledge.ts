'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

export interface Article {
  id: string; title: string; content: string; category: string; tags?: string[]
  author: string; views: number; status: string; version: number; created_at: string; updated_at: string
}

export interface Category {
  id: string; name: string; description?: string; sort_order: number; color?: string; created_at?: string
}

export interface ArticleVersion {
  id: string; article_id: string; version: number; title: string; content: string; category: string
  tags?: string[]; created_at: string
}

const FALLBACK_CATEGORIES = ['常见问题', '变更操作', '故障处理', '最佳实践', '参考文档']

interface KnowledgeState {
  articles: Article[]; loading: boolean
  categories: string[]           // 分类名称(列表 Tab 过滤用)
  categoryList: Category[]       // 完整分类元数据(管理弹窗用, 含 id/颜色/排序)
  fetch: (category?: string) => Promise<void>
  create: (data: any) => Promise<boolean>
  update: (id: string, data: any) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  // P1 内容管理
  fetchCategories: () => Promise<void>
  saveCategory: (data: { name: string; description?: string; sort_order?: number; color?: string }) => Promise<boolean>
  updateCategory: (id: string, data: { name: string; description?: string; sort_order?: number; color?: string }) => Promise<boolean>
  deleteCategory: (id: string) => Promise<boolean>
  fetchVersions: (id: string) => Promise<ArticleVersion[]>
  rollbackVersion: (id: string, version: number) => Promise<boolean>
  importArticles: (items: { title: string; content: string; category?: string; tags?: string[]; status?: string }[]) => Promise<{ imported: number; errors: string[] }>
  // 文件导入: 上传原始文件(.md/.txt/.json/.pdf/.docx), 由后端解析建文章
  importFile: (file: File, category?: string) => Promise<{ imported: number; errors: string[] }>
}

export const useKnowledgeStore = create<KnowledgeState>((set, get) => ({
  articles: [], loading: false, categories: FALLBACK_CATEGORIES, categoryList: [],

  fetch: async (category) => {
    set({ loading: true })
    try {
      const qs = category ? `?category=${encodeURIComponent(category)}` : ''
      const articles = await apiList<Article>(`/knowledge${qs}`)
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

  // —— P1 内容管理 ——
  fetchCategories: async () => {
    try {
      const cats = await apiList<Category>('/knowledge/categories')
      const names = cats.map(c => c.name).filter(Boolean)
      set({ categoryList: cats, categories: names.length > 0 ? names : FALLBACK_CATEGORIES })
    } catch {
      /* 后端不可用时保持兜底分类 */
    }
  },
  saveCategory: async (data) => {
    try {
      await apiFetch('/knowledge/categories', { method: 'POST', body: JSON.stringify(data) })
      toast.success('分类已保存')
      await get().fetchCategories()
      return true
    } catch (e: any) {
      toast.error(`保存分类失败: ${e.message}`)
      return false
    }
  },
  updateCategory: async (id, data) => {
    try {
      await apiFetch(`/knowledge/categories/${id}`, { method: 'PUT', body: JSON.stringify(data) })
      toast.success('分类已更新')
      await get().fetchCategories()
      return true
    } catch (e: any) {
      toast.error(`更新分类失败: ${e.message}`)
      return false
    }
  },
  deleteCategory: async (id) => {
    try {
      await apiFetch(`/knowledge/categories/${id}`, { method: 'DELETE' })
      toast.success('分类已删除')
      await get().fetchCategories()
      return true
    } catch (e: any) {
      toast.error(`删除分类失败: ${e.message}`)
      return false
    }
  },
  fetchVersions: async (id) => {
    try {
      return await apiList<ArticleVersion>(`/knowledge/${id}/versions`)
    } catch {
      return []
    }
  },
  rollbackVersion: async (id, version) => {
    try {
      await apiFetch(`/knowledge/${id}/versions/${version}/rollback`, { method: 'POST' })
      toast.success(`已回滚到 v${version}`)
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`回滚失败: ${e.message}`)
      return false
    }
  },
  importArticles: async (items) => {
    try {
      const res = await apiFetch<{ imported: number; errors: string[] }>('/knowledge/import', {
        method: 'POST', body: JSON.stringify({ items }),
      })
      await get().fetch()
      return res
    } catch (e: any) {
      toast.error(`导入失败: ${e.message}`)
      return { imported: 0, errors: [e.message] }
    }
  },
  importFile: async (file, category) => {
    try {
      const fd = new FormData()
      fd.append('file', file)
      if (category) fd.append('category', category)
      // multipart 不走 apiFetch 的 JSON 头, 直接 fetch 同源接口
      const resp = await fetch(`${window.location.origin}/api/v1/knowledge/import-file`, {
        method: 'POST', body: fd,
      })
      if (!resp.ok) {
        const msg = await resp.text().catch(() => resp.statusText)
        throw new Error(msg || `HTTP ${resp.status}`)
      }
      const res = await resp.json() as { imported: number; errors: string[] }
      await get().fetch()
      return res
    } catch (e: any) {
      toast.error(`导入失败: ${e.message}`)
      return { imported: 0, errors: [e.message] }
    }
  },
}))
