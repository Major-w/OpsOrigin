
import { create } from 'zustand'
import { apiFetch, apiList } from '@/lib/utils'
import { getMessage } from '@/lib/message'
export interface ProbeResult {
  success: boolean
  latency_ms: number
  status_code?: number
  error_message?: string
  checked_at: string
}

export interface MonitorTarget {
  id: string
  ci_id: string
  name: string
  address: string
  port?: number
  probes: string[]
  status: string
  last_checked?: string
  created_at: string
}

export interface AlertEvent {
  id: string
  tenant_id: string
  target_id?: string
  source_ci_id?: string
  rule_name: string
  severity: string
  title: string
  description?: string
  is_suppressed: boolean
  root_cause_ci?: string
  cluster_id?: string
  triggered_at: string
  resolved_at?: string
}

/** 启发式根因分析候选(按评分降序) */
export interface RCAEntry {
  ci_id: string
  rule_name: string
  title: string
  severity: string
  score: number
  triggered_at: string
}

export interface AlertRule {
  id: string
  name: string
  metric_name: string
  operator: string
  threshold: number
  duration: number
  severity: string
  enabled: boolean
}

interface MonitorState {
  targets: MonitorTarget[]
  alerts: AlertEvent[]
  alertHistory: AlertEvent[]   // 已确认/已解决的处理历史
  rules: AlertRule[]
  alertStats: Record<string, number>
  rcaEntries: RCAEntry[]
  probeResult: ProbeResult | null
  probeResults: any[]
  loading: boolean
  error: string | null
  fetchTargets: () => Promise<void>
  addTarget: (data: any) => Promise<boolean>
  deleteTarget: (id: string) => Promise<boolean>
  fetchAlerts: () => Promise<void>
  fetchAlertHistory: () => Promise<void>
  fetchAlertStats: () => Promise<void>
  fetchRCA: () => Promise<void>
  ackAlert: (id: string) => Promise<boolean>
  resolveAlert: (id: string) => Promise<boolean>
  fetchRules: () => Promise<void>
  createRule: (data: Partial<AlertRule>) => Promise<boolean>
  deleteRule: (id: string) => Promise<boolean>
  probe: (data: any) => Promise<ProbeResult | null>
  fetchMetrics: () => Promise<void>
}

export const useMonitorStore = create<MonitorState>((set, get) => ({
  targets: [], alerts: [], alertHistory: [], rules: [], alertStats: {},
  rcaEntries: [], probeResult: null, probeResults: [], loading: false, error: null,

  fetchTargets: async () => {
    set({ loading: true })
    try {
      const targets = await apiList<MonitorTarget>('/monitor/targets')
      set({ targets, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载监控目标失败: ${e.message}`)
    }
  },
  addTarget: async (data) => {
    try {
      await apiFetch('/monitor/targets', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('新增监控目标成功')
      await get().fetchTargets()
      return true
    } catch (e: any) {
      getMessage().error(`新增监控目标失败: ${e.message}`)
      return false
    }
  },
  deleteTarget: async (id) => {
    try {
      await apiFetch(`/monitor/targets/${id}`, { method: 'DELETE' })
      set(s => ({ targets: s.targets.filter(t => t.id !== id) }))
      getMessage().success('删除监控目标成功')
      return true
    } catch (e: any) {
      getMessage().error(`删除监控目标失败: ${e.message}`)
      return false
    }
  },
  fetchAlerts: async () => {
    set({ loading: true })
    try {
      const alerts = await apiList<AlertEvent>('/monitor/alerts')
      set({ alerts, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载监控告警失败: ${e.message}`)
    }
  },
  fetchAlertHistory: async () => {
    try {
      const history = await apiList<AlertEvent>('/monitor/alerts?status=history')
      set({ alertHistory: history })
    } catch (e: any) {
      getMessage().error(`加载告警历史失败: ${e.message}`)
    }
  },
  fetchAlertStats: async () => {
    set({ loading: true })
    try {
      const stats = await apiFetch<any>('/monitor/alert-stats')
      set({ alertStats: stats, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载告警统计失败: ${e.message}`)
    }
  },
  fetchRCA: async () => {
    try {
      const entries = await apiList<RCAEntry>('/monitor/alerts/rca')
      set({ rcaEntries: entries || [] })
    } catch (e: any) {
      getMessage().error(`加载根因分析失败: ${e.message}`)
    }
  },
  ackAlert: async (id) => {
    try {
      await apiFetch(`/monitor/alerts/${id}/ack`, { method: 'POST', body: JSON.stringify({ acknowledged_by: 'admin' }) })
      getMessage().success('确认告警成功')
      await get().fetchAlerts()
      return true
    } catch (e: any) {
      getMessage().error(`确认告警失败: ${e.message}`)
      return false
    }
  },
  resolveAlert: async (id) => {
    try {
      await apiFetch(`/monitor/alerts/${id}/resolve`, { method: 'POST' })
      getMessage().success('解决告警成功')
      await get().fetchAlerts()
      return true
    } catch (e: any) {
      getMessage().error(`解决告警失败: ${e.message}`)
      return false
    }
  },
  fetchRules: async () => {
    set({ loading: true })
    try {
      const rules = await apiList<AlertRule>('/monitor/rules')
      set({ rules, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载告警规则失败: ${e.message}`)
    }
  },
  createRule: async (data) => {
    try {
      await apiFetch('/monitor/rules', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('创建告警规则成功')
      await get().fetchRules()
      return true
    } catch (e: any) {
      getMessage().error(`创建告警规则失败: ${e.message}`)
      return false
    }
  },
  deleteRule: async (id) => {
    try {
      await apiFetch(`/monitor/rules/${id}`, { method: 'DELETE' })
      set(s => ({ rules: s.rules.filter(r => r.id !== id) }))
      getMessage().success('删除告警规则成功')
      return true
    } catch (e: any) {
      getMessage().error(`删除告警规则失败: ${e.message}`)
      return false
    }
  },
  probe: async (data) => {
    set({ loading: true, error: null })
    try {
      const result = await apiFetch<ProbeResult>('/monitor/probe', { method: 'POST', body: JSON.stringify(data) })
      set({ probeResult: result, loading: false })
      return result
    } catch (e: any) {
      set({ error: e.message, loading: false })
      getMessage().error(`探测失败: ${e.message}`)
      return null
    }
  },
  fetchMetrics: async () => {
    set({ loading: true })
    try {
      const results = await apiList<any>('/monitor/metrics')
      set({ probeResults: results, loading: false })
    } catch (e: any) {
      set({ loading: false })
      getMessage().error(`加载监控指标失败: ${e.message}`)
    }
  },
}))
