import React from 'react'
import type { Access } from '@/access'

const lazy = (factory: () => Promise<{ default: React.ComponentType<any> }>) => {
  const Component = React.lazy(factory)
  return (props: any) => (
    <React.Suspense fallback={<div style={{ padding: 32, textAlign: 'center' }}>Loading...</div>}>
      <Component {...props} />
    </React.Suspense>
  )
}

export interface RouteConfig {
  path: string
  name?: string
  icon?: string
  access?: keyof Access
  component?: React.ComponentType<any>
  layout?: boolean
  redirect?: string
  routes?: RouteConfig[]
}

const routes: RouteConfig[] = [
  { path: '/login', component: lazy(() => import('./app/login/page')), layout: false },
  {
    path: '/user',
    layout: false,
    routes: [
      { path: '/user/login', redirect: '/login' },
    ],
  },
  {
    path: '/exception',
    layout: false,
    routes: [
      { path: '/exception/403', component: lazy(() => import('./components/exception').then(m => ({ default: () => <m.default type="403" /> }))) },
      { path: '/exception/404', component: lazy(() => import('./components/exception').then(m => ({ default: () => <m.default type="404" /> }))) },
      { path: '/exception/500', component: lazy(() => import('./components/exception').then(m => ({ default: () => <m.default type="500" /> }))) },
    ],
  },
  { path: '/dashboard', name: 'dashboard', icon: 'dashboard', component: lazy(() => import('./app/(dashboard)/dashboard/page')), access: 'canUser' },
  { path: '/resources', name: 'resources', icon: 'server', component: lazy(() => import('./app/(dashboard)/resources/page')), access: 'canResource' },
  { path: '/credentials', name: 'credentials', icon: 'key', component: lazy(() => import('./app/(dashboard)/credentials/page')), access: 'canCredential' },
  { path: '/reports', name: 'reports', icon: 'barChart', component: lazy(() => import('./app/(dashboard)/reports/page')), access: 'canReport' },
  { path: '/schedules', name: 'schedules', icon: 'clockCircle', component: lazy(() => import('./app/(dashboard)/schedules/page')), access: 'canSchedule' },
  {
    path: '/workflows',
    name: 'workflows',
    icon: 'branches',
    access: 'canWorkflow',
    routes: [
      { path: '/workflows', component: lazy(() => import('./app/(dashboard)/workflows/page')) },
      { path: '/workflows/:id', component: lazy(() => import('./app/(dashboard)/workflows/[id]/page')) },
    ],
  },
  {
    path: '/auto',
    name: 'auto',
    icon: 'thunderbolt',
    access: 'canAuto',
    routes: [
      { path: '/auto', component: lazy(() => import('./app/(dashboard)/auto/page')) },
      { path: '/auto/job', name: 'autoJob', component: lazy(() => import('./app/(dashboard)/auto/job/page')) },
      { path: '/auto/patch', name: 'autoPatch', component: lazy(() => import('./app/(dashboard)/auto/patch/page')) },
      { path: '/auto/inspection', name: 'autoInspection', component: lazy(() => import('./app/(dashboard)/auto/inspection/page')) },
      { path: '/auto/software', name: 'autoSoftware', component: lazy(() => import('./app/(dashboard)/auto/software/page')) },
      { path: '/auto/vuln-scan', name: 'autoVulnScan', component: lazy(() => import('./app/(dashboard)/auto/vuln-scan/page')) },
      { path: '/automation/inbound', name: 'automationInbound', component: lazy(() => import('./app/(dashboard)/automation/inbound/page')) },
    ],
  },
  {
    path: '/deploy',
    name: 'deploy',
    icon: 'cloudUpload',
    access: 'canDeploy',
    routes: [
      { path: '/deploy', component: lazy(() => import('./app/(dashboard)/deploy/page')) },
      { path: '/deploy/apps', name: 'deployApps', component: lazy(() => import('./app/(dashboard)/deploy/apps/page')) },
      { path: '/deploy/apps/:id', component: lazy(() => import('./app/(dashboard)/deploy/apps/[id]/page')) },
      { path: '/deploy/environments', name: 'deployEnvironments', component: lazy(() => import('./app/(dashboard)/deploy/environments/page')) },
      { path: '/deploy/history', name: 'deployHistory', component: lazy(() => import('./app/(dashboard)/deploy/history/page')) },
      { path: '/deploy/releases', name: 'deployReleases', component: lazy(() => import('./app/(dashboard)/deploy/releases/page')) },
    ],
  },
  {
    path: '/monitor',
    name: 'monitor',
    icon: 'alert',
    access: 'canMonitor',
    routes: [
      { path: '/monitor', component: lazy(() => import('./app/(dashboard)/monitor/page')) },
      { path: '/monitor/panorama', name: 'monitorPanorama', component: lazy(() => import('./app/(dashboard)/monitor/panorama/page')) },
      { path: '/monitor/predict', name: 'monitorPredict', component: lazy(() => import('./app/(dashboard)/monitor/predict/page')) },
      { path: '/monitor/targets', name: 'monitorTargets', component: lazy(() => import('./app/(dashboard)/monitor/targets/page')) },
      { path: '/monitor/alerts', name: 'monitorAlerts', component: lazy(() => import('./app/(dashboard)/monitor/alerts/page')) },
      { path: '/monitor/collection', name: 'monitorCollection', component: lazy(() => import('./app/(dashboard)/monitor/collection/page')) },
      { path: '/monitor/plugins', name: 'monitorPlugins', component: lazy(() => import('./app/(dashboard)/monitor/plugins/page')) },
    ],
  },
  {
    path: '/ai',
    name: 'ai',
    icon: 'robot',
    access: 'canAi',
    routes: [
      { path: '/ai', component: lazy(() => import('./app/(dashboard)/ai/page')) },
      { path: '/ai/analytics', name: 'aiAnalytics', component: lazy(() => import('./app/(dashboard)/ai/analytics/page')) },
      { path: '/ai/teams', name: 'aiTeams', component: lazy(() => import('./app/(dashboard)/ai/teams/page')) },
      { path: '/ai/impact', name: 'aiImpact', component: lazy(() => import('./app/(dashboard)/ai/impact/page')) },
      { path: '/ai/inspections', name: 'aiInspections', component: lazy(() => import('./app/(dashboard)/ai/inspections/page')) },
      { path: '/ai/mcp', name: 'aiMcp', component: lazy(() => import('./app/(dashboard)/ai/mcp/page')) },
      { path: '/ai/marketplace', name: 'aiMarketplace', component: lazy(() => import('./app/(dashboard)/ai/marketplace/page')) },
      { path: '/ai/scenes', name: 'aiScenes', component: lazy(() => import('./app/(dashboard)/ai/scenes/page')) },
      { path: '/ai/drills', name: 'aiDrills', component: lazy(() => import('./app/(dashboard)/ai/drills/page')) },
      { path: '/ai/settings', name: 'aiSettings', component: lazy(() => import('./app/(dashboard)/ai/settings/page')) },
    ],
  },
  {
    path: '/cmdb',
    name: 'cmdb',
    icon: 'database',
    access: 'canCmdb',
    routes: [
      { path: '/cmdb', component: lazy(() => import('./app/(dashboard)/cmdb/page')) },
      { path: '/cmdb/stats', name: 'cmdbStats', component: lazy(() => import('./app/(dashboard)/cmdb/stats/page')) },
      { path: '/cmdb/auto-collect', name: 'cmdbAutoCollect', component: lazy(() => import('./app/(dashboard)/cmdb/auto-collect/page')) },
      { path: '/cmdb/models', name: 'cmdbModels', component: lazy(() => import('./app/(dashboard)/cmdb/models/page')) },
      { path: '/cmdb/impact', name: 'cmdbImpact', component: lazy(() => import('./app/(dashboard)/cmdb/impact/page')) },
      { path: '/cmdb/quality', name: 'cmdbQuality', component: lazy(() => import('./app/(dashboard)/cmdb/quality/page')) },
      { path: '/cmdb/query', name: 'cmdbQuery', component: lazy(() => import('./app/(dashboard)/cmdb/query/page')) },
      { path: '/cmdb/instances', name: 'cmdbInstances', component: lazy(() => import('./app/(dashboard)/cmdb/instances/page')) },
      { path: '/cmdb/instances/new', component: lazy(() => import('./app/(dashboard)/cmdb/instances/new/page')) },
      { path: '/cmdb/instances/:id', component: lazy(() => import('./app/(dashboard)/cmdb/instances/[id]/page')) },
      { path: '/cmdb/data-policy', name: 'cmdbDataPolicy', component: lazy(() => import('./app/(dashboard)/cmdb/data-policy/page')) },
      { path: '/cmdb/history', name: 'cmdbHistory', component: lazy(() => import('./app/(dashboard)/cmdb/history/page')) },
      { path: '/cmdb/topology', name: 'cmdbTopology', component: lazy(() => import('./app/(dashboard)/cmdb/topology/page')) },
      { path: '/cmdb/subscriptions', name: 'cmdbSubscriptions', component: lazy(() => import('./app/(dashboard)/cmdb/subscriptions/page')) },
      { path: '/cmdb/idc', name: 'cmdbIdc', component: lazy(() => import('./app/(dashboard)/cmdb/idc/page')) },
      { path: '/cmdb/search', name: 'cmdbSearch', component: lazy(() => import('./app/(dashboard)/cmdb/search/page')) },
    ],
  },
  {
    path: '/scripts',
    name: 'scripts',
    icon: 'code',
    access: 'canScript',
    routes: [
      { path: '/scripts', component: lazy(() => import('./app/(dashboard)/scripts/page')) },
      { path: '/scripts/:id', component: lazy(() => import('./app/(dashboard)/scripts/[id]/page')) },
    ],
  },
  {
    path: '/knowledge',
    name: 'knowledge',
    icon: 'book',
    access: 'canKnowledge',
    routes: [
      { path: '/knowledge', component: lazy(() => import('./app/(dashboard)/knowledge/page')) },
      { path: '/knowledge/categories', name: 'knowledgeCategories', component: lazy(() => import('./app/(dashboard)/knowledge/categories/page')) },
      { path: '/knowledge/new', component: lazy(() => import('./app/(dashboard)/knowledge/new/page')) },
      { path: '/knowledge/:id', component: lazy(() => import('./app/(dashboard)/knowledge/[id]/page')) },
    ],
  },
  {
    path: '/system',
    name: 'system',
    icon: 'setting',
    access: 'canSystem',
    routes: [
      { path: '/system/notifications', name: 'systemNotifications', component: lazy(() => import('./app/(dashboard)/system/notifications/page')) },
      { path: '/system/groups', name: 'systemGroups', component: lazy(() => import('./app/(dashboard)/system/groups/page')) },
      { path: '/system/health', name: 'systemHealth', component: lazy(() => import('./app/(dashboard)/system/health/page')) },
      { path: '/system/collection-policy', name: 'systemCollectionPolicy', component: lazy(() => import('./app/(dashboard)/system/collection-policy/page')) },
      { path: '/system/roles', name: 'systemRoles', component: lazy(() => import('./app/(dashboard)/system/roles/page')) },
      { path: '/system/auth', name: 'systemAuth', component: lazy(() => import('./app/(dashboard)/system/auth/page')) },
      { path: '/system/users', name: 'systemUsers', component: lazy(() => import('./app/(dashboard)/system/users/page')) },
      { path: '/system/settings', name: 'systemSettings', component: lazy(() => import('./app/(dashboard)/system/settings/page')) },
    ],
  },
  { path: '/audit', name: 'audit', icon: 'audit', component: lazy(() => import('./app/(dashboard)/audit/page')), access: 'canAudit' },
  {
    path: '/lowcode',
    name: 'lowcode',
    icon: 'blocks',
    access: 'canLowcode',
    routes: [
      { path: '/lowcode', component: lazy(() => import('./app/(dashboard)/lowcode/page')) },
      { path: '/lowcode/pages', name: 'lowcodePages', component: lazy(() => import('./app/(dashboard)/lowcode/pages/page')) },
      { path: '/lowcode/models', name: 'lowcodeModels', component: lazy(() => import('./app/(dashboard)/lowcode/models/page')) },
    ],
  },
  {
    path: '/itsm',
    name: 'itsm',
    icon: 'tool',
    access: 'canItsm',
    routes: [
      { path: '/itsm/process', name: 'itsmProcess', component: lazy(() => import('./app/(dashboard)/itsm/process/page')) },
      { path: '/itsm/process/:id', component: lazy(() => import('./app/(dashboard)/itsm/process/[id]/page')) },
      { path: '/itsm/reports', name: 'itsmReports', component: lazy(() => import('./app/(dashboard)/itsm/reports/page')) },
      { path: '/itsm/ola', name: 'itsmOla', component: lazy(() => import('./app/(dashboard)/itsm/ola/page')) },
      { path: '/itsm/oncall', name: 'itsmOncall', component: lazy(() => import('./app/(dashboard)/itsm/oncall/page')) },
      { path: '/itsm/oncall/calendar', component: lazy(() => import('./app/(dashboard)/itsm/oncall/calendar/page')) },
      { path: '/itsm/overview', name: 'itsmOverview', component: lazy(() => import('./app/(dashboard)/itsm/overview/page')) },
      { path: '/itsm/change-assess', name: 'itsmChangeAssess', component: lazy(() => import('./app/(dashboard)/itsm/change-assess/page')) },
      { path: '/itsm/sla', name: 'itsmSla', component: lazy(() => import('./app/(dashboard)/itsm/sla/page')) },
      { path: '/itsm/form-dicts', name: 'itsmFormDicts', component: lazy(() => import('./app/(dashboard)/itsm/form-dicts/page')) },
      { path: '/itsm/inbound', name: 'itsmInbound', component: lazy(() => import('./app/(dashboard)/itsm/inbound/page')) },
      { path: '/itsm/knowledge', name: 'itsmKnowledge', component: lazy(() => import('./app/(dashboard)/itsm/knowledge/page')) },
      { path: '/itsm/sla-breached', name: 'itsmSlaBreached', component: lazy(() => import('./app/(dashboard)/itsm/sla-breached/page')) },
      { path: '/itsm/catalog', name: 'itsmCatalog', component: lazy(() => import('./app/(dashboard)/itsm/catalog/page')) },
      { path: '/itsm/sla-ola', name: 'itsmSlaOla', component: lazy(() => import('./app/(dashboard)/itsm/sla-ola/page')) },
      { path: '/itsm/portal', name: 'itsmPortal', component: lazy(() => import('./app/(dashboard)/itsm/portal/page')) },
      { path: '/itsm/change', name: 'itsmChange', component: lazy(() => import('./app/(dashboard)/itsm/change/page')) },
      { path: '/itsm/tickets', name: 'itsmTickets', component: lazy(() => import('./app/(dashboard)/itsm/tickets/page')) },
      { path: '/itsm/tickets/:id/sla', component: lazy(() => import('./app/(dashboard)/itsm/tickets/[id]/sla/page')) },
      { path: '/itsm/tickets/:id/sla-log', component: lazy(() => import('./app/(dashboard)/itsm/tickets/[id]/sla-log/page')) },
    ],
  },
  { path: '/guide', name: 'guide', icon: 'questionCircle', component: lazy(() => import('./app/(dashboard)/guide/page')) },
  { path: '/', redirect: '/dashboard' },
]

export default routes
