// 统一采集调度相关的共享 TS 类型，与后端 internal/cmdb 的采集框架对齐。
//
// 架构要点（见 docs/cmdb-collection-architecture.svg）：
//   - 资源纳管层只建立通道（装 Agent / 验连通性），不拉数据、不直写 CMDB；
//   - CMDB 是唯一调度者，持有全部采集任务，按 frequency 触发，结果按 kind 路由落库；
//   - channel 区分服务端拉取（server）与 Agent 推送上报（agent）。

/** 采集任务的资源类型。 */
export type CollectionResourceType = 'cloud' | 'host' | 'container'

/** 采集类别：cmdb 落 CI，monitor 落指标。 */
export type CollectionKind = 'cmdb' | 'monitor'

/** 采集通道：server=CMDB 服务端拉取，agent=经 Agent 推送上报。 */
export type CollectionChannel = 'server' | 'agent'

export type CollectionStatus = 'idle' | 'running' | 'success' | 'failed' | 'pending'

/** 一条采集任务（CMDB 侧唯一真相源）。 */
export interface CollectionTask {
  id: string
  tenant_id: string
  name: string
  resource_type: CollectionResourceType
  target: string
  kind: CollectionKind
  channel: CollectionChannel
  frequency: string
  enabled: boolean
  /** 动态执行目标模式：static=静态资源 ID（默认），query=按保存查询实时解析（P1-4）。 */
  target_mode?: 'static' | 'query'
  /** target_mode=query 时引用的保存查询 ID（"我想查"）。 */
  target_query?: string
  last_run_at?: string
  status: CollectionStatus
  error?: string
  created_at: string
  updated_at: string
}

/** 平台级全局默认采集策略（单例，纳管表单勾选框预填此值，可逐资源覆盖）。 */
export interface PlatformPolicy {
  tenant_id?: string
  default_cmdb_enabled: boolean
  default_monitor_enabled: boolean
  cmdb_default_frequency: string
  monitor_default_frequency: string
  updated_at?: string
}

/** 纳管资源请求：开关为空则回落平台默认策略。 */
export interface OnboardResourceRequest {
  resource_type: CollectionResourceType
  target: string
  enable_cmdb_collect?: boolean
  enable_monitor_collect?: boolean
}

/** 新建采集任务请求。 */
export interface CreateCollectionTaskRequest {
  name?: string
  resource_type: CollectionResourceType
  target: string
  kind: CollectionKind
  frequency?: string
  enabled: boolean
  target_mode?: 'static' | 'query'
  target_query?: string
}

export const DEFAULT_PLATFORM_POLICY: PlatformPolicy = {
  default_cmdb_enabled: true,
  default_monitor_enabled: false,
  cmdb_default_frequency: '1h',
  monitor_default_frequency: '5m',
}

export const RESOURCE_TYPE_LABELS: Record<CollectionResourceType, string> = {
  cloud: '云资源',
  host: '主机',
  container: '容器',
}

export const KIND_LABELS: Record<CollectionKind, string> = {
  cmdb: 'CMDB 采集',
  monitor: '监控采集',
}

export const CHANNEL_LABELS: Record<CollectionChannel, string> = {
  server: '服务端拉取',
  agent: 'Agent 上报',
}

export const STATUS_LABELS: Record<CollectionStatus, string> = {
  idle: '待执行',
  running: '运行中',
  success: '成功',
  failed: '失败',
  pending: '等待中',
}

/** 常用频率预设，供设置页与任务表单快速选择。 */
export const FREQUENCY_PRESETS = [
  { value: '1m', label: '1 分钟' },
  { value: '5m', label: '5 分钟' },
  { value: '15m', label: '15 分钟' },
  { value: '30m', label: '30 分钟' },
  { value: '1h', label: '1 小时' },
  { value: '6h', label: '6 小时' },
  { value: '12h', label: '12 小时' },
  { value: '24h', label: '24 小时' },
] as const

/** 校验 Go duration 频率字符串，返回错误信息或 null。 */
export function validateFrequency(v: string): string | null {
  if (!v.trim()) return '频率不能为空'
  if (!/^(\d+(\.\d+)?(ns|us|ms|s|m|h))+$/.test(v.trim())) {
    return '格式无效，示例：30s / 5m / 1h'
  }
  return null
}
