import { useSyncExternalStore } from 'react'

const zhCN: Record<string, string> = {
  // Common
  'common.search': '搜索',
  'common.refresh': '刷新',
  'common.create': '新建',
  'common.edit': '编辑',
  'common.delete': '删除',
  'common.cancel': '取消',
  'common.confirm': '确认',
  'common.save': '保存',
  'common.export': '导出',
  'common.import': '导入',
  'common.batchDelete': '批量删除',
  'common.total': '共 {total} 条',
  'common.selected': '已选择 {count} 项',
  'common.actions': '操作',
  'common.status': '状态',
  'common.loading': '加载中...',
  'common.empty': '暂无数据',
  'common.success': '操作成功',
  'common.failed': '操作失败',
  'common.noPermission': '无权限访问',
  'common.backToHome': '返回首页',
  'common.pageNotFound': '页面不存在',
  'common.serverError': '服务器错误',

  // Navigation
  'nav.dashboard': '仪表盘',
  'nav.cmdb': 'CMDB',
  'nav.resources': '资源纳管',
  'nav.scripts': '脚本管理',
  'nav.workflows': '作业流程',
  'nav.deploy': '发布管理',
  'nav.monitor': '监控告警',
  'nav.itsm': '工单管理',
  'nav.knowledge': '知识库',
  'nav.settings': '系统设置',
  'nav.credentials': '凭证管理',
  'nav.schedules': '定时任务',
  'nav.ai': 'AI 中心',
  'nav.auto': '自动化',
  'nav.lowcode': '低代码',
  'nav.audit': '审计日志',
  'nav.reports': '报表统计',
  'nav.guide': '使用指南',

  // Auth
  'auth.login': '登录',
  'auth.logout': '退出登录',
  'auth.username': '用户名',
  'auth.password': '密码',

  // Dashboard
  'dashboard.welcome': '欢迎回来',
  'dashboard.overview': '系统概览',

  // Scripts
  'scripts.title': '脚本管理',
  'scripts.subtitle': '运维脚本管理、参数化定义与执行历史',
  'scripts.create': '新建脚本',
  'scripts.name': '脚本名称',
  'scripts.type': '类型',
  'scripts.shell': 'Shell',
  'scripts.python': 'Python',

  // Deploy
  'deploy.title': '发布管理',
  'deploy.apps': '应用管理',
  'deploy.releases': '发布单',
  'deploy.history': '发布历史',
  'deploy.environments': '环境管理',

  // CMDB
  'cmdb.title': 'CMDB',
  'cmdb.instances': '实例管理',
  'cmdb.models': '模型管理',
  'cmdb.topology': '拓扑关系',
  'cmdb.stats': '统计概览',
  'cmdb.search': '资源搜索',

  // Monitor
  'monitor.title': '监控告警',
  'monitor.alerts': '告警管理',
  'monitor.targets': '监控目标',
  'monitor.panorama': '全景视图',

  // System
  'system.title': '系统管理',
  'system.users': '用户管理',
  'system.roles': '角色管理',
  'system.groups': '分组管理',
  'system.auth': '权限管理',
  'system.settings': '系统设置',
  'system.health': '健康检查',

  // ITSM
  'itsm.title': '工单管理',
  'itsm.tickets': '工单列表',
  'itsm.process': '流程管理',
  'itsm.sla': 'SLA 管理',
  'itsm.oncall': 'On-Call 值班',
  'itsm.reports': '工单报表',

  // Knowledge
  'knowledge.title': '知识库',
  'knowledge.categories': '分类管理',
  'knowledge.new': '新建文档',

  // AI
  'ai.title': 'AI 中心',
  'ai.analytics': '智能分析',
  'ai.teams': 'AI 团队',
  'ai.settings': 'AI 设置',

  // Lowcode
  'lowcode.title': '低代码',
  'lowcode.pages': '页面管理',
  'lowcode.models': '数据模型',

  // Common UI
  'ui.language': '语言',
  'ui.theme': '主题',
  'ui.darkMode': '暗色模式',
  'ui.lightMode': '浅色模式',
}

const enUS: Record<string, string> = {
  // Common
  'common.search': 'Search',
  'common.refresh': 'Refresh',
  'common.create': 'Create',
  'common.edit': 'Edit',
  'common.delete': 'Delete',
  'common.cancel': 'Cancel',
  'common.confirm': 'Confirm',
  'common.save': 'Save',
  'common.export': 'Export',
  'common.import': 'Import',
  'common.batchDelete': 'Batch Delete',
  'common.total': 'Total {total} items',
  'common.selected': '{count} items selected',
  'common.actions': 'Actions',
  'common.status': 'Status',
  'common.loading': 'Loading...',
  'common.empty': 'No data',
  'common.success': 'Success',
  'common.failed': 'Failed',
  'common.noPermission': 'No Permission',
  'common.backToHome': 'Back to Home',
  'common.pageNotFound': 'Page Not Found',
  'common.serverError': 'Server Error',

  // Navigation
  'nav.dashboard': 'Dashboard',
  'nav.cmdb': 'CMDB',
  'nav.resources': 'Resources',
  'nav.scripts': 'Scripts',
  'nav.workflows': 'Workflows',
  'nav.deploy': 'Deploy',
  'nav.monitor': 'Monitor',
  'nav.itsm': 'ITSM',
  'nav.knowledge': 'Knowledge Base',
  'nav.settings': 'Settings',
  'nav.credentials': 'Credentials',
  'nav.schedules': 'Schedules',
  'nav.ai': 'AI Center',
  'nav.auto': 'Automation',
  'nav.lowcode': 'Low-Code',
  'nav.audit': 'Audit Log',
  'nav.reports': 'Reports',
  'nav.guide': 'Guide',

  // Auth
  'auth.login': 'Login',
  'auth.logout': 'Logout',
  'auth.username': 'Username',
  'auth.password': 'Password',

  // Dashboard
  'dashboard.welcome': 'Welcome back',
  'dashboard.overview': 'System Overview',

  // Scripts
  'scripts.title': 'Scripts',
  'scripts.subtitle': 'Manage, define parameters, and view execution history',
  'scripts.create': 'New Script',
  'scripts.name': 'Script Name',
  'scripts.type': 'Type',
  'scripts.shell': 'Shell',
  'scripts.python': 'Python',

  // Deploy
  'deploy.title': 'Deploy',
  'deploy.apps': 'Applications',
  'deploy.releases': 'Releases',
  'deploy.history': 'Deploy History',
  'deploy.environments': 'Environments',

  // CMDB
  'cmdb.title': 'CMDB',
  'cmdb.instances': 'Instances',
  'cmdb.models': 'Models',
  'cmdb.topology': 'Topology',
  'cmdb.stats': 'Statistics',
  'cmdb.search': 'Search',

  // Monitor
  'monitor.title': 'Monitor',
  'monitor.alerts': 'Alerts',
  'monitor.targets': 'Targets',
  'monitor.panorama': 'Panorama',

  // System
  'system.title': 'System',
  'system.users': 'Users',
  'system.roles': 'Roles',
  'system.groups': 'Groups',
  'system.auth': 'Permissions',
  'system.settings': 'Settings',
  'system.health': 'Health Check',

  // ITSM
  'itsm.title': 'ITSM',
  'itsm.tickets': 'Tickets',
  'itsm.process': 'Process',
  'itsm.sla': 'SLA',
  'itsm.oncall': 'On-Call',
  'itsm.reports': 'Reports',

  // Knowledge
  'knowledge.title': 'Knowledge Base',
  'knowledge.categories': 'Categories',
  'knowledge.new': 'New Document',

  // AI
  'ai.title': 'AI Center',
  'ai.analytics': 'Analytics',
  'ai.teams': 'AI Teams',
  'ai.settings': 'AI Settings',

  // Lowcode
  'lowcode.title': 'Low-Code',
  'lowcode.pages': 'Pages',
  'lowcode.models': 'Models',

  // Common UI
  'ui.language': 'Language',
  'ui.theme': 'Theme',
  'ui.darkMode': 'Dark Mode',
  'ui.lightMode': 'Light Mode',
}

export type Locale = 'zh-CN' | 'en-US'

const messages: Record<Locale, Record<string, string>> = {
  'zh-CN': zhCN,
  'en-US': enUS,
}

let currentLocale: Locale = (localStorage.getItem('opsorigin_locale') as Locale) || 'zh-CN'

type Listener = () => void
const listeners = new Set<Listener>()

function emitChange() {
  for (const l of listeners) l()
}

export function getLocale(): Locale {
  return currentLocale
}

export function setLocale(locale: Locale) {
  currentLocale = locale
  localStorage.setItem('opsorigin_locale', locale)
  document.documentElement.lang = locale
  emitChange()
}

export function useLocale(): Locale {
  return useSyncExternalStore(
    (cb) => { listeners.add(cb); return () => listeners.delete(cb) },
    () => currentLocale,
    () => currentLocale,
  )
}

export function t(key: string, params?: Record<string, string | number>): string {
  const msg = messages[currentLocale]?.[key] || messages['zh-CN']?.[key] || key
  if (!params) return msg
  return Object.entries(params).reduce(
    (str, [k, v]) => str.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v)),
    msg
  )
}
