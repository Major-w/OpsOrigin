
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
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

/** AI 生成的看板草稿：只在前端预览，用户确认后才走创建接口落库 */
export interface DashboardDraft {
  dashboard: {
    name: string
    description?: string
    widgets: Widget[]
  }
  /** 模型自述：这个看板回答了什么问题 */
  explanation?: string
  /** 平台对模型输出做白名单修复时留下的提示，透明展示避免黑箱 */
  warnings?: string[]
}

/** 提示词建议 chip，由后端按已注册数据集动态生成 */
export interface WorkspaceSuggestion {
  dataset: string
  label: string
  prompt: string
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
  generateDashboard: (prompt: string) => Promise<DashboardDraft>
  loadWorkspaceSuggestions: () => Promise<WorkspaceSuggestion[]>
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
      const ds = await apiList<Dataset>('/report/datasets')
      set({ datasets: ds })
    } catch (e) {
      message.error('加载数据集失败: ' + (e as Error).message)
    }
  },

  listDashboards: async () => {
    const list = await apiList<DashboardSummary>('/report/dashboards')
    set({ dashboards: list })
  },

  getDashboard: async (id: string) => {
    set({ loading: true })
    try {
      const d = await apiFetch<Dashboard>(`/report/dashboards/${id}`)
      set({ current: d })
      await get().renderDashboard(id)
    } catch (e) {
      message.error('打开看板失败: ' + (e as Error).message)
    } finally {
      set({ loading: false })
    }
  },

  renderDashboard: async (id: string) => {
    set({ rendering: true })
    try {
      const res = await apiFetch<{ id: string; name: string; widgets: RenderedWidget[] }>(
        `/report/dashboards/${id}/render`
      )
      set({ rendered: res.widgets || [] })
    } catch (e) {
      message.error('渲染看板失败: ' + (e as Error).message)
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
    const created = await apiFetch<Dashboard>('/report/dashboards', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
    await get().listDashboards()
    set({ current: created })
    await get().renderDashboard(created.id)
    message.success('看板已创建')
    return created
  },

  updateDashboard: async (id: string, d: Partial<Dashboard>) => {
    const updated = await apiFetch<Dashboard>(`/report/dashboards/${id}`, {
      method: 'PUT',
      body: JSON.stringify(d),
    })
    set({ current: updated })
    await get().renderDashboard(id)
    await get().listDashboards()
    message.success('看板已保存')
    return updated
  },

  deleteDashboard: async (id: string) => {
    await apiFetch(`/report/dashboards/${id}`, { method: 'DELETE' })
    if (get().current?.id === id) set({ current: null, rendered: [] })
    await get().listDashboards()
    message.success('看板已删除')
  },

  runQuery: async (q: Query) => {
    return apiFetch<Result>('/report/query', {
      method: 'POST',
      body: JSON.stringify(q),
    })
  },

  // AI 只产草稿，落库仍走 createDashboard，权限与归属和人工搭建完全一致
  generateDashboard: async (prompt: string) => {
    return apiFetch<DashboardDraft>('/ai/workspace/generate', {
      method: 'POST',
      body: JSON.stringify({ prompt }),
    })
  },

  loadWorkspaceSuggestions: async () => {
    try {
      const res = await apiFetch<{ suggestions: WorkspaceSuggestion[] }>(
        '/ai/workspace/suggestions'
      )
      return res.suggestions || []
    } catch {
      return []
    }
  },

  closeDashboard: () => set({ current: null, rendered: [] }),
}))
