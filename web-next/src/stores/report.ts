'use client'

import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { toast } from '@/stores/toast'

// —— 类型：与后端 internal/report 模型对齐 ——

export type FieldType = 'string' | 'number' | 'time' | 'bool'

export interface Dimension {
  name: string
  label: string
  type: FieldType
  enum?: string[]
  filterable: boolean
  groupable: boolean
}

export interface Metric {
  name: string
  label: string
  agg: string
  unit?: string
  decimals?: number
}

export interface Dataset {
  name: string
  label: string
  module: string
  description?: string
  dimensions: Dimension[]
  metrics: Metric[]
  timeField?: string
}

export type FilterOp =
  | 'eq' | 'ne' | 'in' | 'not_in' | 'gt' | 'gte' | 'lt' | 'lte'
  | 'contains' | 'between' | 'is_null' | 'not_null'

export interface Filter {
  field: string
  op: FilterOp
  value?: unknown
}

export interface TimeRange {
  field?: string
  from?: string
  to?: string
  last?: string
}

export type Granularity = '' | 'hour' | 'day' | 'week' | 'month' | 'quarter' | 'year'

export interface OrderBy {
  field: string
  desc?: boolean
}

export interface Query {
  dataset: string
  dimensions?: string[]
  metrics?: string[]
  filters?: Filter[]
  timeRange?: TimeRange
  granularity?: Granularity
  orderBy?: OrderBy[]
  limit?: number
}

export interface Column {
  name: string
  label: string
  type: FieldType
  role: 'dimension' | 'metric'
  unit?: string
  decimals?: number
}

export interface Result {
  columns: Column[]
  rows: unknown[][]
  rowCount: number
  truncated: boolean
  elapsedMs: number
}

export interface Layout {
  x: number
  y: number
  w: number
  h: number
}

export interface Widget {
  id: string
  title: string
  type: string
  query: Query
  layout: Layout
}

export interface Dashboard {
  id: string
  tenantId: string
  name: string
  description?: string
  widgets: Widget[]
  createdAt: string
  updatedAt: string
  /** 创建者由服务端从可信用户上下文写入，前端传入无效 */
  createdBy?: string
  createdByName?: string
}

export interface DashboardSummary {
  id: string
  tenantId: string
  name: string
  description?: string
  widgetCount: number
  createdAt: string
  updatedAt: string
  createdBy?: string
  createdByName?: string
}

export interface RenderedWidget {
  id: string
  type: string
  title: string
  layout: Layout
  data?: Result
  error?: string
}

interface ReportState {
  datasets: Dataset[]
  dashboards: DashboardSummary[]
  current: Dashboard | null
  rendered: RenderedWidget[]
  rendering: boolean
  loading: boolean

  loadDatasets: () => Promise<void>
  listDashboards: () => Promise<void>
  getDashboard: (id: string) => Promise<void>
  renderDashboard: (id: string) => Promise<void>
  createDashboard: (d: Partial<Dashboard>) => Promise<Dashboard>
  updateDashboard: (id: string, d: Partial<Dashboard>) => Promise<Dashboard>
  deleteDashboard: (id: string) => Promise<void>
  runQuery: (q: Query) => Promise<Result>
  closeDashboard: () => void
}

export const useReportStore = create<ReportState>((set, get) => ({
  datasets: [],
  dashboards: [],
  current: null,
  rendered: [],
  rendering: false,
  loading: false,

  loadDatasets: async () => {
    try {
      const ds = await apiList<Dataset>('/api/v1/report/datasets')
      set({ datasets: ds })
    } catch (e) {
      toast.error('加载数据集失败: ' + (e as Error).message)
    }
  },

  listDashboards: async () => {
    const list = await apiList<DashboardSummary>('/api/v1/report/dashboards')
    set({ dashboards: list })
  },

  getDashboard: async (id: string) => {
    set({ loading: true })
    try {
      const d = await apiFetch<Dashboard>(`/api/v1/report/dashboards/${id}`)
      set({ current: d })
      await get().renderDashboard(id)
    } catch (e) {
      toast.error('打开看板失败: ' + (e as Error).message)
    } finally {
      set({ loading: false })
    }
  },

  renderDashboard: async (id: string) => {
    set({ rendering: true })
    try {
      const res = await apiFetch<{ id: string; name: string; widgets: RenderedWidget[] }>(
        `/api/v1/report/dashboards/${id}/render`
      )
      set({ rendered: res.widgets || [] })
    } catch (e) {
      toast.error('渲染看板失败: ' + (e as Error).message)
    } finally {
      set({ rendering: false })
    }
  },

  createDashboard: async (d: Partial<Dashboard>) => {
    const payload: Partial<Dashboard> = {
      name: d.name || '未命名看板',
      description: d.description || '',
      widgets: d.widgets || [],
    }
    const created = await apiFetch<Dashboard>('/api/v1/report/dashboards', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await get().listDashboards()
    set({ current: created })
    await get().renderDashboard(created.id)
    toast.success('看板已创建')
    return created
  },

  updateDashboard: async (id: string, d: Partial<Dashboard>) => {
    const updated = await apiFetch<Dashboard>(`/api/v1/report/dashboards/${id}`, {
      method: 'PUT',
      body: JSON.stringify(d),
    })
    set({ current: updated })
    await get().renderDashboard(id)
    await get().listDashboards()
    toast.success('看板已保存')
    return updated
  },

  deleteDashboard: async (id: string) => {
    await apiFetch(`/api/v1/report/dashboards/${id}`, { method: 'DELETE' })
    if (get().current?.id === id) set({ current: null, rendered: [] })
    await get().listDashboards()
    toast.success('看板已删除')
  },

  runQuery: async (q: Query) => {
    return apiFetch<Result>('/api/v1/report/query', {
      method: 'POST',
      body: JSON.stringify(q),
    })
  },

  closeDashboard: () => set({ current: null, rendered: [] }),
}))
