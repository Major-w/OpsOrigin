
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
  type?: 'threshold' | 'promql'
  labels?: Record<string, string>
  expr?: string
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
  sources: MetricSource[]
  fetchSources: () => Promise<void>
  createSource: (data: Partial<MetricSource>) => Promise<boolean>
  deleteSource: (id: string) => Promise<boolean>
  querySeries: (query: string, start: number, end: number, step?: number) => Promise<TSQueryResult | null>
  sourceMetrics: string[]
  fetchSourceMetrics: (id: string) => Promise<void>
  webhook: { configured: boolean; format: string; url: string }
  fetchWebhook: () => Promise<void>
  setWebhook: (url: string) => Promise<boolean>
  testWebhook: () => Promise<boolean>
  email: { configured: boolean; host: string; port: number; user: string; from: string; to: string; tls: boolean; pass_set: boolean }
  fetchEmail: () => Promise<void>
  setEmail: (cfg: any) => Promise<boolean>
  testEmail: () => Promise<boolean>
  maintenances: MaintenanceWindow[]
  fetchMaintenances: () => Promise<void>
  createMaintenance: (data: Partial<MaintenanceWindow>) => Promise<boolean>
  deleteMaintenance: (id: string) => Promise<boolean>
}

export interface MetricSource {
  id: string
  tenant_id: string
  name: string
  type: 'zabbix' | 'prometheus'
  address: string
  port: number
  path?: string
  keys?: string[]
  interval_sec: number
  ci_id?: string
  // 采集健康状态
  last_success?: string
  last_error?: string
  last_duration_ms?: number
  healthy?: boolean
}

export interface TSQueryResult {
  status: string
  data?: {
    resultType: string
    result: Array<{ metric: Record<string, string>; values: Array<[number, string]> }>
  }
  error?: string
}

export interface MaintenanceWindow {
  id: string
  tenant_id: string
  name: string
  start_at: string
  end_at: string
  enabled: boolean
  match?: {
    instances?: string[]
    ci_ids?: string[]
    sources?: string[]
    severities?: string[]
  }
  created_at: string
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
  sources: [],
  fetchSources: async () => {
    try {
      const sources = await apiList<MetricSource>('/monitor/sources')
      set({ sources: sources || [] })
    } catch (e: any) {
      getMessage().error(`加载数据源失败: ${e.message}`)
    }
  },
  createSource: async (data) => {
    try {
      await apiFetch('/monitor/sources', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('新增数据源成功')
      await get().fetchSources()
      return true
    } catch (e: any) {
      getMessage().error(`新增数据源失败: ${e.message}`)
      return false
    }
  },
  deleteSource: async (id) => {
    try {
      await apiFetch(`/monitor/sources/${id}`, { method: 'DELETE' })
      set(s => ({ sources: s.sources.filter(x => x.id !== id) }))
      getMessage().success('删除数据源成功')
      return true
    } catch (e: any) {
      getMessage().error(`删除数据源失败: ${e.message}`)
      return false
    }
  },
  querySeries: async (query, start, end, step = 60) => {
    const params = new URLSearchParams({ query, start: String(start), end: String(end), step: String(step) })
    try {
      return await apiFetch<TSQueryResult>(`/monitor/ts/query?${params.toString()}`)
    } catch (e: any) {
      getMessage().error(`时序查询失败: ${e.message}`)
      return null
    }
  },
  sourceMetrics: [],
  fetchSourceMetrics: async (id) => {
    try {
      const metrics = await apiList<string>(`/monitor/sources/${id}/metrics`)
      set({ sourceMetrics: metrics || [] })
    } catch (e: any) {
      set({ sourceMetrics: [] })
      getMessage().error(`加载指标目录失败: ${e.message}`)
    }
  },

  webhook: { configured: false, format: 'json', url: '' },
  email: { configured: false, host: '', port: 25, user: '', from: '', to: '', tls: false, pass_set: false },
  maintenances: [],
  fetchWebhook: async () => {
    try {
      const cfg = await apiFetch<any>('/monitor/notify/webhook')
      set({ webhook: { configured: !!cfg.configured, format: cfg.format || 'json', url: cfg.url || '' } })
    } catch {
      // 渠道非核心, 静默
    }
  },
  setWebhook: async (url) => {
    try {
      const cfg = await apiFetch<any>('/monitor/notify/webhook', { method: 'POST', body: JSON.stringify({ url }) })
      set({ webhook: { configured: !!cfg.configured, format: cfg.format || 'json', url: cfg.url || '' } })
      getMessage().success(url ? '通知渠道已更新' : '通知渠道已停用')
      return true
    } catch (e: any) {
      getMessage().error(`设置通知渠道失败: ${e.message}`)
      return false
    }
  },
  testWebhook: async () => {
    try {
      await apiFetch('/monitor/notify/webhook/test', { method: 'POST' })
      getMessage().success('测试消息已发送，请检查接收端')
      return true
    } catch (e: any) {
      getMessage().error(`发送测试失败: ${e.message}`)
      return false
    }
  },

  fetchEmail: async () => {
    try {
      const cfg = await apiFetch<any>('/monitor/notify/email')
      set({
        email: {
          configured: !!cfg.configured,
          host: cfg.host || '',
          port: cfg.port || 25,
          user: cfg.user || '',
          from: cfg.from || '',
          to: cfg.to || '',
          tls: !!cfg.tls,
          pass_set: !!cfg.pass_set,
        },
      })
    } catch {
      // 渠道非核心, 静默
    }
  },
  setEmail: async (cfg) => {
    try {
      const r = await apiFetch<any>('/monitor/notify/email', { method: 'POST', body: JSON.stringify(cfg) })
      set({
        email: {
          configured: !!r.configured,
          host: r.host || '',
          port: r.port || 25,
          user: r.user || '',
          from: r.from || '',
          to: r.to || '',
          tls: !!r.tls,
          pass_set: !!r.pass_set,
        },
      })
      getMessage().success(r.to || r.from ? '邮件渠道已更新' : '邮件渠道已停用')
      return true
    } catch (e: any) {
      getMessage().error(`设置邮件渠道失败: ${e.message}`)
      return false
    }
  },
  testEmail: async () => {
    try {
      await apiFetch('/monitor/notify/email/test', { method: 'POST' })
      getMessage().success('测试邮件已发送，请检查收件箱')
      return true
    } catch (e: any) {
      getMessage().error(`发送测试失败: ${e.message}`)
      return false
    }
  },

  fetchMaintenances: async () => {
    try {
      const wins = await apiList<MaintenanceWindow>('/monitor/maintenances')
      set({ maintenances: wins || [] })
    } catch (e: any) {
      getMessage().error(`加载维护期失败: ${e.message}`)
    }
  },
  createMaintenance: async (data) => {
    try {
      await apiFetch('/monitor/maintenances', { method: 'POST', body: JSON.stringify(data) })
      getMessage().success('维护期已创建')
      await get().fetchMaintenances()
      return true
    } catch (e: any) {
      getMessage().error(`创建维护期失败: ${e.message}`)
      return false
    }
  },
  deleteMaintenance: async (id) => {
    try {
      await apiFetch(`/monitor/maintenances/${id}`, { method: 'DELETE' })
      set(s => ({ maintenances: s.maintenances.filter(m => m.id !== id) }))
      getMessage().success('维护期已删除')
      return true
    } catch (e: any) {
      getMessage().error(`删除维护期失败: ${e.message}`)
      return false
    }
  },
}))
