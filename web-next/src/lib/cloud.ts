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

// 前端资源类型选项（MVP 仅 VM / 数据库 / 负载均衡）。
export const CLOUD_RESOURCE_TYPES = [
  { value: 'vm', label: '虚拟机' },
  { value: 'database', label: '数据库' },
  { value: 'load_balancer', label: '负载均衡' },
] as const
