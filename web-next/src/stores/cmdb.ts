'use client'

import { create } from 'zustand'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import type { AttrMeta } from '@/components/cmdb/SourceBadge'

export interface CIModel {
  id: string
  type_id: string
  tenant_id: string
  name: string
  description?: string
  status: string
  attributes: Record<string, any>
  attribute_meta?: Record<string, AttrMeta>
  labels?: Record<string, string>
  channel: number
  created_at: string
  updated_at: string
}

export interface CIRelation {
  id: string
  tenant_id: string
  source_id: string
  target_id: string
  relation_type: string
  properties?: Record<string, any>
  created_at: string
}

export interface AttributeChange {
  ci_id: string
  attr_name: string
  old_value: any
  new_value: any
  channel: number
  changed_at: string
}

export interface CIModelType {
  id: string
  tenant_id: string
  name: string
  description?: string
  schema: Record<string, { type: string; required: boolean; description?: string }>
}

// 拓扑图节点
export interface TopologyNode {
  id: string
  type: string
  name: string
  tenant_id: string
}

// 拓扑图边
export interface TopologyEdge {
  source_id: string
  target_id: string
  relation_type: string
}

// 拓扑查询结果
export interface TopologyResult {
  nodes: TopologyNode[]
  edges: TopologyEdge[]
}

// 影响分析结果
export interface ImpactResult {
  source_ci: string
  affected_ci: string[]
  depth: number
  has_cycle: boolean
}

interface CMDBState {
  items: CIModel[]
  relations: CIRelation[]
  modelTypes: CIModelType[]
  topology: TopologyResult | null
  impactResult: ImpactResult | null
  total: number
  loading: boolean
  error: string | null
  fetch: (params?: { type_id?: string; sort_by?: string; sort_order?: string; limit?: number; offset?: number }) => Promise<void>
  search: (q: string, type_id?: string) => Promise<void>
  create: (data: Partial<CIModel>) => Promise<boolean>
  update: (id: string, data: Partial<CIModel>) => Promise<boolean>
  remove: (id: string) => Promise<boolean>
  getAudit: (id: string) => Promise<AttributeChange[]>
  fetchRelations: () => Promise<void>
  createRelation: (data: { source_id: string; target_id: string; relation_type: string }) => Promise<boolean>
  deleteRelation: (id: string) => Promise<boolean>
  fetchModelTypes: () => Promise<void>
  createModelType: (data: Partial<CIModelType>) => Promise<boolean>
  fetchTopology: (ciId: string) => Promise<TopologyResult | null>
  fetchImpact: (ciId: string) => Promise<ImpactResult | null>
}

export const useCMDBStore = create<CMDBState>((set, get) => ({
  items: [],
  relations: [],
  modelTypes: [],
  topology: null,
  impactResult: null,
  total: 0,
  loading: false,
  error: null,
  fetch: async (params) => {
    set({ loading: true })
    try {
      const qs = new URLSearchParams()
      if (params?.type_id) qs.set('type_id', params.type_id)
      if (params?.sort_by) qs.set('sort_by', params.sort_by)
      if (params?.sort_order) qs.set('sort_order', params.sort_order)
      if (params?.limit) qs.set('limit', String(params.limit))
      if (params?.offset) qs.set('offset', String(params.offset))
      const path = `/cmdb/ci${qs.toString() ? '?' + qs.toString() : ''}`
      const items = await apiFetch<CIModel[]>(path)
      set({ items, total: items.length, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`加载CMDB实例失败: ${e.message}`)
    }
  },
  search: async (q, type_id) => {
    set({ loading: true })
    try {
      const qs = new URLSearchParams()
      if (q) qs.set('q', q)
      if (type_id) qs.set('type_id', type_id)
      const items = await apiFetch<CIModel[]>(`/cmdb/search?${qs.toString()}`)
      set({ items, total: items.length, loading: false })
    } catch (e: any) {
      set({ loading: false })
      toast.error(`搜索CMDB实例失败: ${e.message}`)
    }
  },
  create: async (data) => {
    try {
      await apiFetch('/cmdb/ci', { method: 'POST', body: JSON.stringify({ ...data, channel: 100 }) })
      toast.success('创建资产成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`创建资产失败: ${e.message}`)
      return false
    }
  },
  update: async (id, data) => {
    try {
      await apiFetch(`/cmdb/ci/${id}`, { method: 'PUT', body: JSON.stringify({ ...data, channel: 100 }) })
      toast.success('更新资产成功')
      await get().fetch()
      return true
    } catch (e: any) {
      toast.error(`更新资产失败: ${e.message}`)
      return false
    }
  },
  remove: async (id) => {
    try {
      await apiFetch(`/cmdb/ci/${id}`, { method: 'DELETE' })
      set((s) => ({ items: s.items.filter(i => i.id !== id), total: Math.max(0, s.total - 1) }))
      toast.success('删除资产成功')
      return true
    } catch (e: any) {
      toast.error(`删除资产失败: ${e.message}`)
      return false
    }
  },
  getAudit: async (id) => {
    try {
      return await apiFetch<AttributeChange[]>(`/cmdb/ci/${id}/audit`)
    } catch (e: any) {
      toast.error(`加载变更记录失败: ${e.message}`)
      throw e
    }
  },
  fetchRelations: async () => {
    try {
      const relations = await apiFetch<CIRelation[]>('/cmdb/relation')
      set({ relations })
    } catch (e: any) {
      toast.error(`加载关联关系失败: ${e.message}`)
    }
  },
  createRelation: async (data) => {
    try {
      await apiFetch('/cmdb/relation', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建关联成功')
      await get().fetchRelations()
      return true
    } catch (e: any) {
      toast.error(`创建关联失败: ${e.message}`)
      return false
    }
  },
  deleteRelation: async (id) => {
    try {
      await apiFetch(`/cmdb/relation/${id}`, { method: 'DELETE' })
      set((s) => ({ relations: s.relations.filter(r => r.id !== id) }))
      toast.success('删除关联成功')
      return true
    } catch (e: any) {
      toast.error(`删除关联失败: ${e.message}`)
      return false
    }
  },
  fetchModelTypes: async () => {
    try {
      const items = await apiFetch<CIModel[]>('/cmdb/ci?limit=200')
      const types = new Map<string, CIModelType>()
      for (const ci of items) {
        if (!types.has(ci.type_id)) {
          types.set(ci.type_id, { id: ci.type_id, tenant_id: ci.tenant_id, name: ci.type_id, schema: {} })
        }
      }
      set({ modelTypes: Array.from(types.values()) })
    } catch (e: any) {
      toast.error(`加载模型类型失败: ${e.message}`)
    }
  },
  createModelType: async (data) => {
    try {
      await apiFetch('/cmdb/model-type', { method: 'POST', body: JSON.stringify(data) })
      toast.success('创建模型类型成功')
      return true
    } catch (e: any) {
      toast.error(`创建模型类型失败: ${e.message}`)
      return false
    }
  },

  fetchTopology: async (ciId) => {
    try {
      const result = await apiFetch<TopologyResult>(`/cmdb/topology/${ciId}`)
      set({ topology: result })
      return result
    } catch (e: any) {
      toast.error(`加载拓扑图失败: ${e.message}`)
      return null
    }
  },

  fetchImpact: async (ciId) => {
    try {
      const result = await apiFetch<ImpactResult>(`/cmdb/impact/${ciId}`)
      set({ impactResult: result })
      return result
    } catch (e: any) {
      toast.error(`影响分析失败: ${e.message}`)
      return null
    }
  },
}))
