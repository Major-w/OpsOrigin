'use client'

import { create } from 'zustand'
import { toast } from '@/stores/toast'

// ============ 类型定义 ============

export type AppType = 'java' | 'go' | 'python' | 'nodejs' | 'static' | 'container'
export type AppStatus = 'enabled' | 'disabled'
export type DeployEnv = 'dev' | 'test' | 'staging' | 'prod'
export type ReleaseStatus = 'draft' | 'pending_approval' | 'approved' | 'deploying' | 'succeeded' | 'failed' | 'rolled_back' | 'rejected'
export type ReleaseStrategy = 'blue_green' | 'canary' | 'rolling' | 'full'

export interface DeployApp {
  id: string
  name: string
  code: string
  type: AppType
  status: AppStatus
  repo_url: string
  branch: string
  build_cmd: string
  artifact_path: string
  deploy_script: string
  health_check_url: string
  target_port: number
  envs: DeployEnv[]
  current_versions: Record<DeployEnv, string>
  description: string
  owner: string
  labels: Record<string, string>
  created_at: string
  updated_at: string
}

export interface ReleaseTicket {
  id: string
  app_id: string
  app_name: string
  app_code: string
  title: string
  version: string
  env: DeployEnv
  strategy: ReleaseStrategy
  status: ReleaseStatus
  commit_id: string
  commit_msg: string
  changelog: string
  approvers: string[]
  approved_by: string
  approved_at: string
  reject_reason: string
  deploy_steps: DeployStep[]
  rollback_enabled: boolean
  rollback_version: string
  created_by: string
  created_at: string
  updated_at: string
  started_at: string
  finished_at: string
  duration_ms: number
}

export interface DeployStep {
  id: string
  name: string
  type: 'pull' | 'build' | 'package' | 'deploy' | 'health_check' | 'notify'
  status: 'pending' | 'running' | 'success' | 'failed' | 'skipped'
  output: string
  duration_ms: number
  started_at: string
  finished_at: string
}

export interface DeployEnvironment {
  id: string
  name: string
  code: DeployEnv
  description: string
  color: string
  target_hosts: string[]
  deploy_path: string
  domain: string
  port_range: string
  requires_approval: boolean
  auto_rollback: boolean
  max_releases: number
  app_count: number
  last_release_at: string
  created_at: string
}

export interface ReleaseHistory {
  id: string
  app_id: string
  app_name: string
  app_code: string
  version: string
  env: DeployEnv
  strategy: ReleaseStrategy
  status: ReleaseStatus
  commit_id: string
  commit_msg: string
  deployed_by: string
  started_at: string
  finished_at: string
  duration_ms: number
  rollback_of: string
  rolled_back_by: string
  rolled_back_at: string
}

// ============ Mock 数据 ============

const now = Date.now()
const iso = (offset: number) => new Date(now + offset).toISOString()

const mockApps: DeployApp[] = [
  {
    id: 'app-001', name: '用户中心服务', code: 'user-center', type: 'java', status: 'enabled',
    repo_url: 'https://git.example.com/ops/user-center.git', branch: 'main',
    build_cmd: 'mvn clean package -DskipTests', artifact_path: 'target/user-center.jar',
    deploy_script: '/opt/scripts/deploy-java.sh', health_check_url: '/api/health',
    target_port: 8080, envs: ['dev', 'test', 'prod'],
    current_versions: { dev: 'v2.3.1', test: 'v2.3.0', staging: 'v2.2.5', prod: 'v2.2.0' },
    description: '统一用户认证与权限管理服务', owner: 'zhangsan',
    labels: { team: 'platform', level: 'P0' }, created_at: iso(-86400000 * 30), updated_at: iso(-3600000 * 2),
  },
  {
    id: 'app-002', name: '订单网关', code: 'order-gateway', type: 'go', status: 'enabled',
    repo_url: 'https://git.example.com/ops/order-gateway.git', branch: 'master',
    build_cmd: 'go build -o order-gateway ./cmd/server', artifact_path: 'order-gateway',
    deploy_script: '/opt/scripts/deploy-go.sh', health_check_url: '/healthz',
    target_port: 9090, envs: ['dev', 'test', 'staging', 'prod'],
    current_versions: { dev: 'v1.5.2', test: 'v1.5.1', staging: 'v1.5.0', prod: 'v1.4.8' },
    description: '订单流量入口网关,负责路由与限流', owner: 'lisi',
    labels: { team: 'trade', level: 'P0' }, created_at: iso(-86400000 * 60), updated_at: iso(-3600000 * 5),
  },
  {
    id: 'app-003', name: '商品中台', code: 'product-center', type: 'nodejs', status: 'enabled',
    repo_url: 'https://git.example.com/ops/product-center.git', branch: 'develop',
    build_cmd: 'npm run build', artifact_path: 'dist/',
    deploy_script: '/opt/scripts/deploy-node.sh', health_check_url: '/api/ping',
    target_port: 3000, envs: ['dev', 'test', 'prod'],
    current_versions: { dev: 'v3.0.0-rc1', test: 'v2.9.5', staging: '', prod: 'v2.9.0' },
    description: '商品模型与上下架管理中台', owner: 'wangwu',
    labels: { team: 'product', level: 'P1' }, created_at: iso(-86400000 * 45), updated_at: iso(-3600000 * 24),
  },
  {
    id: 'app-004', name: '支付回调服务', code: 'pay-callback', type: 'python', status: 'disabled',
    repo_url: 'https://git.example.com/ops/pay-callback.git', branch: 'main',
    build_cmd: 'pip install -r requirements.txt', artifact_path: 'app.py',
    deploy_script: '/opt/scripts/deploy-python.sh', health_check_url: '/health',
    target_port: 5000, envs: ['dev', 'prod'],
    current_versions: { dev: 'v0.9.2', test: '', staging: '', prod: 'v0.9.0' },
    description: '第三方支付渠道回调处理(已暂停迭代)', owner: 'zhaoliu',
    labels: { team: 'payment', level: 'P2' }, created_at: iso(-86400000 * 90), updated_at: iso(-86400000 * 7),
  },
  {
    id: 'app-005', name: '运营后台前端', code: 'admin-web', type: 'static', status: 'enabled',
    repo_url: 'https://git.example.com/ops/admin-web.git', branch: 'main',
    build_cmd: 'npm run build', artifact_path: 'dist/',
    deploy_script: '/opt/scripts/deploy-static.sh', health_check_url: '/',
    target_port: 80, envs: ['dev', 'test', 'prod'],
    current_versions: { dev: 'v4.1.0', test: 'v4.0.5', staging: '', prod: 'v4.0.0' },
    description: '运营管理后台单页应用', owner: 'qianqi',
    labels: { team: 'platform', level: 'P1' }, created_at: iso(-86400000 * 20), updated_at: iso(-3600000 * 12),
  },
  {
    id: 'app-006', name: '风控引擎', code: 'risk-engine', type: 'container', status: 'enabled',
    repo_url: 'https://git.example.com/ops/risk-engine.git', branch: 'main',
    build_cmd: 'docker build -t risk-engine .', artifact_path: 'risk-engine:latest',
    deploy_script: '/opt/scripts/deploy-container.sh', health_check_url: '/api/healthz',
    target_port: 7000, envs: ['dev', 'test', 'staging', 'prod'],
    current_versions: { dev: 'v1.0.5', test: 'v1.0.4', staging: 'v1.0.3', prod: 'v1.0.0' },
    description: '实时风控规则引擎,容器化部署', owner: 'sunba',
    labels: { team: 'risk', level: 'P0' }, created_at: iso(-86400000 * 15), updated_at: iso(-3600000 * 3),
  },
]

const mockReleases: ReleaseTicket[] = [
  {
    id: 'rel-001', app_id: 'app-001', app_name: '用户中心服务', app_code: 'user-center',
    title: '用户中心 v2.3.1 发布到生产', version: 'v2.3.1', env: 'prod', strategy: 'blue_green',
    status: 'pending_approval', commit_id: 'a1b2c3d', commit_msg: 'feat: 新增微信扫码登录',
    changelog: '1. 新增微信扫码登录\n2. 修复 token 刷新竞态\n3. 优化登录日志',
    approvers: ['admin', 'zhangsan'], approved_by: '', approved_at: '', reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's2', name: '构建打包', type: 'build', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's3', name: '蓝绿切换', type: 'deploy', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
    ],
    rollback_enabled: true, rollback_version: 'v2.2.0',
    created_by: 'zhangsan', created_at: iso(-3600000 * 2), updated_at: iso(-3600000),
    started_at: '', finished_at: '', duration_ms: 0,
  },
  {
    id: 'rel-002', app_id: 'app-002', app_name: '订单网关', app_code: 'order-gateway',
    title: '订单网关 v1.5.1 发布到测试', version: 'v1.5.1', env: 'test', strategy: 'rolling',
    status: 'deploying', commit_id: 'e4f5g6h', commit_msg: 'fix: 修复支付回调超时',
    changelog: '1. 修复支付回调超时\n2. 优化限流算法',
    approvers: ['admin'], approved_by: 'admin', approved_at: iso(-1800000),
    reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'success', output: 'HEAD e4f5g6h', duration_ms: 3200, started_at: iso(-1800000), finished_at: iso(-1797000) },
      { id: 's2', name: '构建打包', type: 'build', status: 'success', output: 'go build OK', duration_ms: 45000, started_at: iso(-1797000), finished_at: iso(-1752000) },
      { id: 's3', name: '滚动发布', type: 'deploy', status: 'running', output: '2/3 实例已更新', duration_ms: 60000, started_at: iso(-1752000), finished_at: '' },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
    ],
    rollback_enabled: true, rollback_version: 'v1.5.0',
    created_by: 'lisi', created_at: iso(-3600000 * 3), updated_at: iso(-60000),
    started_at: iso(-1800000), finished_at: '', duration_ms: 0,
  },
  {
    id: 'rel-003', app_id: 'app-006', app_name: '风控引擎', app_code: 'risk-engine',
    title: '风控引擎 v1.0.5 发布到预发', version: 'v1.0.5', env: 'staging', strategy: 'canary',
    status: 'approved', commit_id: 'i7j8k9l', commit_msg: 'feat: 新增设备指纹规则',
    changelog: '1. 新增设备指纹识别规则\n2. 优化模型加载速度',
    approvers: ['admin', 'sunba'], approved_by: 'admin', approved_at: iso(-600000),
    reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's2', name: '构建镜像', type: 'build', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's3', name: '金丝雀发布(10%)', type: 'deploy', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
    ],
    rollback_enabled: true, rollback_version: 'v1.0.3',
    created_by: 'sunba', created_at: iso(-3600000 * 4), updated_at: iso(-600000),
    started_at: '', finished_at: '', duration_ms: 0,
  },
  {
    id: 'rel-004', app_id: 'app-005', app_name: '运营后台前端', app_code: 'admin-web',
    title: '运营后台 v4.0.5 发布到测试', version: 'v4.0.5', env: 'test', strategy: 'full',
    status: 'succeeded', commit_id: 'm1n2o3p', commit_msg: 'feat: 新增数据看板',
    changelog: '1. 新增运营数据看板\n2. 修复若干样式问题',
    approvers: ['admin'], approved_by: 'admin', approved_at: iso(-7200000),
    reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'success', output: 'OK', duration_ms: 2800, started_at: iso(-7200000), finished_at: iso(-7197000) },
      { id: 's2', name: '构建打包', type: 'build', status: 'success', output: 'npm run build OK', duration_ms: 78000, started_at: iso(-7197000), finished_at: iso(-7119000) },
      { id: 's3', name: '全量发布', type: 'deploy', status: 'success', output: '部署完成', duration_ms: 15000, started_at: iso(-7119000), finished_at: iso(-7104000) },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'success', output: '200 OK', duration_ms: 2000, started_at: iso(-7104000), finished_at: iso(-7102000) },
    ],
    rollback_enabled: true, rollback_version: 'v4.0.0',
    created_by: 'qianqi', created_at: iso(-3600000 * 5), updated_at: iso(-7100000),
    started_at: iso(-7200000), finished_at: iso(-7102000), duration_ms: 98000,
  },
  {
    id: 'rel-005', app_id: 'app-003', app_name: '商品中台', app_code: 'product-center',
    title: '商品中台 v3.0.0-rc1 发布到开发', version: 'v3.0.0-rc1', env: 'dev', strategy: 'rolling',
    status: 'failed', commit_id: 'q4r5s6t', commit_msg: 'feat: 重构商品模型',
    changelog: '1. 重构商品模型为 SPU/SKU 结构\n2. 迁移到 TypeScript',
    approvers: ['admin'], approved_by: 'admin', approved_at: iso(-86400000),
    reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'success', output: 'OK', duration_ms: 3100, started_at: iso(-86400000), finished_at: iso(-8639700) },
      { id: 's2', name: '构建打包', type: 'build', status: 'failed', output: 'Error: Cannot find module @types/node', duration_ms: 12000, started_at: iso(-8639700), finished_at: iso(-8627700) },
      { id: 's3', name: '滚动发布', type: 'deploy', status: 'skipped', output: '构建失败,跳过', duration_ms: 0, started_at: '', finished_at: '' },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'skipped', output: '构建失败,跳过', duration_ms: 0, started_at: '', finished_at: '' },
    ],
    rollback_enabled: false, rollback_version: '',
    created_by: 'wangwu', created_at: iso(-90000000), updated_at: iso(-8627700),
    started_at: iso(-86400000), finished_at: iso(-8627700), duration_ms: 37300,
  },
  {
    id: 'rel-006', app_id: 'app-001', app_name: '用户中心服务', app_code: 'user-center',
    title: '用户中心 v2.2.0 发布到生产', version: 'v2.2.0', env: 'prod', strategy: 'blue_green',
    status: 'rolled_back', commit_id: 'u7v8w9x', commit_msg: 'feat: 增加多因素认证',
    changelog: '1. 增加多因素认证\n2. 性能优化',
    approvers: ['admin', 'zhangsan'], approved_by: 'admin', approved_at: iso(-86400000 * 2),
    reject_reason: '',
    deploy_steps: [
      { id: 's1', name: '拉取代码', type: 'pull', status: 'success', output: 'OK', duration_ms: 3000, started_at: iso(-86400000 * 2), finished_at: iso(-86400000 * 2 + 3000) },
      { id: 's2', name: '构建打包', type: 'build', status: 'success', output: 'OK', duration_ms: 60000, started_at: iso(-86400000 * 2 + 3000), finished_at: iso(-86400000 * 2 + 63000) },
      { id: 's3', name: '蓝绿切换', type: 'deploy', status: 'success', output: 'OK', duration_ms: 8000, started_at: iso(-86400000 * 2 + 63000), finished_at: iso(-86400000 * 2 + 71000) },
      { id: 's4', name: '健康检查', type: 'health_check', status: 'failed', output: '健康检查超时,自动回滚', duration_ms: 30000, started_at: iso(-86400000 * 2 + 71000), finished_at: iso(-86400000 * 2 + 101000) },
    ],
    rollback_enabled: true, rollback_version: 'v2.1.5',
    created_by: 'zhangsan', created_at: iso(-86400000 * 2 - 3600000), updated_at: iso(-86400000 * 2 + 101000),
    started_at: iso(-86400000 * 2), finished_at: iso(-86400000 * 2 + 101000), duration_ms: 101000,
  },
]

const mockEnvironments: DeployEnvironment[] = [
  {
    id: 'env-dev', name: '开发环境', code: 'dev', description: '开发自测环境,频繁迭代',
    color: 'blue', target_hosts: ['dev-host-01', 'dev-host-02'], deploy_path: '/opt/apps/dev',
    domain: 'dev.example.com', port_range: '8000-8999', requires_approval: false, auto_rollback: false,
    max_releases: 20, app_count: 5, last_release_at: iso(-3600000 * 3), created_at: iso(-86400000 * 90),
  },
  {
    id: 'env-test', name: '测试环境', code: 'test', description: 'QA 测试与集成验证',
    color: 'amber', target_hosts: ['test-host-01', 'test-host-02', 'test-host-03'], deploy_path: '/opt/apps/test',
    domain: 'test.example.com', port_range: '9000-9999', requires_approval: false, auto_rollback: true,
    max_releases: 10, app_count: 4, last_release_at: iso(-3600000 * 2), created_at: iso(-86400000 * 80),
  },
  {
    id: 'env-staging', name: '预发环境', code: 'staging', description: '生产数据演练,发布前最后验证',
    color: 'purple', target_hosts: ['staging-host-01'], deploy_path: '/opt/apps/staging',
    domain: 'staging.example.com', port_range: '7000-7999', requires_approval: true, auto_rollback: true,
    max_releases: 5, app_count: 2, last_release_at: iso(-3600000 * 5), created_at: iso(-86400000 * 70),
  },
  {
    id: 'env-prod', name: '生产环境', code: 'prod', description: '正式对外服务环境',
    color: 'red', target_hosts: ['prod-host-01', 'prod-host-02', 'prod-host-03', 'prod-host-04'], deploy_path: '/opt/apps/prod',
    domain: 'api.example.com', port_range: '80,443,8080', requires_approval: true, auto_rollback: true,
    max_releases: 3, app_count: 5, last_release_at: iso(-86400000 * 2), created_at: iso(-86400000 * 60),
  },
]

const mockHistory: ReleaseHistory[] = [
  {
    id: 'his-001', app_id: 'app-001', app_name: '用户中心服务', app_code: 'user-center',
    version: 'v2.2.0', env: 'prod', strategy: 'blue_green', status: 'rolled_back',
    commit_id: 'u7v8w9x', commit_msg: 'feat: 增加多因素认证',
    deployed_by: 'zhangsan', started_at: iso(-86400000 * 2), finished_at: iso(-86400000 * 2 + 101000),
    duration_ms: 101000, rollback_of: '', rolled_back_by: 'system', rolled_back_at: iso(-86400000 * 2 + 101000),
  },
  {
    id: 'his-002', app_id: 'app-005', app_name: '运营后台前端', app_code: 'admin-web',
    version: 'v4.0.5', env: 'test', strategy: 'full', status: 'succeeded',
    commit_id: 'm1n2o3p', commit_msg: 'feat: 新增数据看板',
    deployed_by: 'qianqi', started_at: iso(-3600000 * 5), finished_at: iso(-3600000 * 5 + 98000),
    duration_ms: 98000, rollback_of: '', rolled_back_by: '', rolled_back_at: '',
  },
  {
    id: 'his-003', app_id: 'app-002', app_name: '订单网关', app_code: 'order-gateway',
    version: 'v1.4.8', env: 'prod', strategy: 'rolling', status: 'succeeded',
    commit_id: 'y0z1a2b', commit_msg: 'release: v1.4.8',
    deployed_by: 'lisi', started_at: iso(-86400000 * 3), finished_at: iso(-86400000 * 3 + 180000),
    duration_ms: 180000, rollback_of: '', rolled_back_by: '', rolled_back_at: '',
  },
  {
    id: 'his-004', app_id: 'app-006', app_name: '风控引擎', app_code: 'risk-engine',
    version: 'v1.0.3', env: 'staging', strategy: 'canary', status: 'succeeded',
    commit_id: 'c3d4e5f', commit_msg: 'feat: 优化模型加载',
    deployed_by: 'sunba', started_at: iso(-86400000), finished_at: iso(-86400000 + 220000),
    duration_ms: 220000, rollback_of: '', rolled_back_by: '', rolled_back_at: '',
  },
  {
    id: 'his-005', app_id: 'app-003', app_name: '商品中台', app_code: 'product-center',
    version: 'v2.9.0', env: 'prod', strategy: 'rolling', status: 'succeeded',
    commit_id: 'g6h7i8j', commit_msg: 'release: v2.9.0',
    deployed_by: 'wangwu', started_at: iso(-86400000 * 5), finished_at: iso(-86400000 * 5 + 150000),
    duration_ms: 150000, rollback_of: '', rolled_back_by: '', rolled_back_at: '',
  },
]

// ============ Store ============

interface DeployState {
  apps: DeployApp[]
  releases: ReleaseTicket[]
  environments: DeployEnvironment[]
  history: ReleaseHistory[]
  loading: boolean

  // 应用管理
  fetchApps: () => Promise<void>
  createApp: (data: Partial<DeployApp>) => Promise<string | null>
  updateApp: (id: string, data: Partial<DeployApp>) => Promise<boolean>
  deleteApp: (id: string) => Promise<boolean>

  // 发布单
  fetchReleases: () => Promise<void>
  createRelease: (data: Partial<ReleaseTicket>) => Promise<string | null>
  approveRelease: (id: string) => Promise<boolean>
  rejectRelease: (id: string, reason: string) => Promise<boolean>
  executeRelease: (id: string) => Promise<boolean>
  rollbackRelease: (id: string) => Promise<boolean>

  // 环境
  fetchEnvironments: () => Promise<void>
  createEnvironment: (data: Partial<DeployEnvironment>) => Promise<boolean>
  updateEnvironment: (id: string, data: Partial<DeployEnvironment>) => Promise<boolean>
  deleteEnvironment: (id: string) => Promise<boolean>

  // 历史
  fetchHistory: () => Promise<void>
  rollbackHistory: (id: string) => Promise<boolean>
}

function delay(ms: number) { return new Promise(r => setTimeout(r, ms)) }

export const useDeployStore = create<DeployState>((set, get) => ({
  apps: [], releases: [], environments: [], history: [], loading: false,

  fetchApps: async () => {
    set({ loading: true })
    await delay(100)
    set({ apps: [...mockApps], loading: false })
  },

  createApp: async (data) => {
    await delay(200)
    const id = `app-${String(get().apps.length + 1).padStart(3, '0')}`
    const app: DeployApp = {
      id, name: data.name || '', code: data.code || '', type: data.type || 'java',
      status: 'enabled', repo_url: data.repo_url || '', branch: data.branch || 'main',
      build_cmd: data.build_cmd || '', artifact_path: data.artifact_path || '',
      deploy_script: data.deploy_script || '', health_check_url: data.health_check_url || '',
      target_port: data.target_port || 8080, envs: data.envs || ['dev'],
      current_versions: { dev: '', test: '', staging: '', prod: '' },
      description: data.description || '', owner: data.owner || '',
      labels: data.labels || {}, created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
    }
    set(s => ({ apps: [app, ...s.apps] }))
    toast.success('应用创建成功')
    return id
  },

  updateApp: async (id, data) => {
    await delay(200)
    set(s => ({ apps: s.apps.map(a => a.id === id ? { ...a, ...data, updated_at: new Date().toISOString() } : a) }))
    toast.success('应用更新成功')
    return true
  },

  deleteApp: async (id) => {
    await delay(200)
    set(s => ({ apps: s.apps.filter(a => a.id !== id) }))
    toast.success('应用已删除')
    return true
  },

  fetchReleases: async () => {
    set({ loading: true })
    await delay(100)
    set({ releases: [...mockReleases], loading: false })
  },

  createRelease: async (data) => {
    await delay(200)
    const id = `rel-${String(get().releases.length + 1).padStart(3, '0')}`
    const app = get().apps.find(a => a.id === data.app_id)
    const rel: ReleaseTicket = {
      id, app_id: data.app_id || '', app_name: app?.name || '', app_code: app?.code || '',
      title: data.title || '', version: data.version || '', env: data.env || 'dev',
      strategy: data.strategy || 'rolling', status: 'draft',
      commit_id: data.commit_id || '', commit_msg: data.commit_msg || '', changelog: data.changelog || '',
      approvers: data.approvers || [], approved_by: '', approved_at: '', reject_reason: '',
      deploy_steps: [
        { id: 's1', name: '拉取代码', type: 'pull', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
        { id: 's2', name: '构建打包', type: 'build', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
        { id: 's3', name: '发布部署', type: 'deploy', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
        { id: 's4', name: '健康检查', type: 'health_check', status: 'pending', output: '', duration_ms: 0, started_at: '', finished_at: '' },
      ],
      rollback_enabled: true, rollback_version: app?.current_versions[data.env || 'dev'] || '',
      created_by: 'current_user', created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
      started_at: '', finished_at: '', duration_ms: 0,
    }
    set(s => ({ releases: [rel, ...s.releases] }))
    toast.success('发布单创建成功')
    return id
  },

  approveRelease: async (id) => {
    await delay(200)
    set(s => ({ releases: s.releases.map(r => r.id === id ? { ...r, status: 'approved', approved_by: 'current_user', approved_at: new Date().toISOString() } : r) }))
    toast.success('审批通过')
    return true
  },

  rejectRelease: async (id, reason) => {
    await delay(200)
    set(s => ({ releases: s.releases.map(r => r.id === id ? { ...r, status: 'rejected', reject_reason: reason } : r) }))
    toast.success('已驳回发布单')
    return true
  },

  executeRelease: async (id) => {
    await delay(200)
    set(s => ({ releases: s.releases.map(r => r.id === id ? { ...r, status: 'deploying', started_at: new Date().toISOString() } : r) }))
    toast.success('开始执行发布')
    return true
  },

  rollbackRelease: async (id) => {
    await delay(300)
    set(s => ({ releases: s.releases.map(r => r.id === id ? { ...r, status: 'rolled_back' } : r) }))
    toast.success('回滚指令已下发')
    return true
  },

  fetchEnvironments: async () => {
    set({ loading: true })
    await delay(100)
    set({ environments: [...mockEnvironments], loading: false })
  },

  createEnvironment: async (data) => {
    await delay(200)
    const env: DeployEnvironment = {
      id: `env-${Date.now()}`, name: data.name || '', code: data.code || 'dev',
      description: data.description || '', color: data.color || 'blue',
      target_hosts: data.target_hosts || [], deploy_path: data.deploy_path || '',
      domain: data.domain || '', port_range: data.port_range || '',
      requires_approval: data.requires_approval ?? false, auto_rollback: data.auto_rollback ?? false,
      max_releases: data.max_releases || 5, app_count: 0, last_release_at: '',
      created_at: new Date().toISOString(),
    }
    set(s => ({ environments: [...s.environments, env] }))
    toast.success('环境创建成功')
    return true
  },

  updateEnvironment: async (id, data) => {
    await delay(200)
    set(s => ({ environments: s.environments.map(e => e.id === id ? { ...e, ...data } : e) }))
    toast.success('环境更新成功')
    return true
  },

  deleteEnvironment: async (id) => {
    await delay(200)
    set(s => ({ environments: s.environments.filter(e => e.id !== id) }))
    toast.success('环境已删除')
    return true
  },

  fetchHistory: async () => {
    set({ loading: true })
    await delay(100)
    set({ history: [...mockHistory], loading: false })
  },

  rollbackHistory: async (id) => {
    await delay(300)
    set(s => ({ history: s.history.map(h => h.id === id ? { ...h, status: 'rolled_back', rolled_back_by: 'current_user', rolled_back_at: new Date().toISOString() } : h) }))
    toast.success('回滚指令已下发')
    return true
  },
}))
