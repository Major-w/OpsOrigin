// 云资源接出相关的共享 TS 类型，与后端 internal/cloud 服务的响应结构对齐。

export interface CloudProvider {
  name: string
  types: string[]
}

export interface CloudCredential {
  id?: string
  provider: string
  region: string
  access_key: string
  secret_key: string
  extra?: Record<string, string>
  has_secret?: boolean
  // 纳管时的采集开关（可选）：为空则回落平台全局默认策略。
  // 后端保存凭证成功后回调 CMDB 的 OnboardResource 创建对应默认采集任务。
  enable_cmdb_collect?: boolean
  enable_monitor_collect?: boolean
}

export interface CloudResource {
  provider: string
  type: string
  id: string
  name: string
  region: string
  zone?: string
  status: string
  public_ip?: string
  private_ip?: string
  labels?: Record<string, string>
  extra?: Record<string, any>
  discovered_at?: string
}

export interface SyncResult {
  created: number
  updated: number
  skipped: number
  failed: number
  errors?: string[]
}

// K8s 发现选项（经 Agent 通道，kubeconfig/context 经 env 注入而非 CLI 参数）。
export interface K8sDiscoverOptions {
  kubeconfig_b64?: string
  kube_context?: string
}

// 前端资源类型选项（对齐后端 AllResourceTypes）。
export const CLOUD_RESOURCE_TYPES = [
  { value: 'vm', label: '虚拟机' },
  { value: 'database', label: '数据库' },
  { value: 'load_balancer', label: '负载均衡' },
  { value: 'vpc', label: '私有网络 VPC' },
  { value: 'bucket', label: '对象存储' },
  { value: 'disk', label: '云硬盘' },
  { value: 'firewall', label: '安全组/防火墙' },
  { value: 'nat_gateway', label: 'NAT 网关' },
  { value: 'cdn', label: 'CDN 加速' },
  { value: 'dns', label: 'DNS 解析' },
] as const

// K8s 容器资源纳管类型选项（经 Agent 通道发现）。
export const K8S_RESOURCE_TYPES = [
  { value: 'k8s_cluster', label: '集群' },
  { value: 'k8s_namespace', label: '命名空间' },
  { value: 'k8s_workload', label: '工作负载' },
  { value: 'k8s_pod', label: 'Pod' },
  { value: 'k8s_container', label: '容器' },
] as const
