import React, { Suspense } from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import Providers from '@/components/providers'
import DashboardLayout from './app/(dashboard)/layout'

const LoginPage = React.lazy(() => import('./app/login/page'))

const DashboardPage = React.lazy(() => import('./app/(dashboard)/dashboard/page'))
const CredentialsPage = React.lazy(() => import('./app/(dashboard)/credentials/page'))
const ReportsPage = React.lazy(() => import('./app/(dashboard)/reports/page'))
const SchedulesPage = React.lazy(() => import('./app/(dashboard)/schedules/page'))
const WorkflowsPage = React.lazy(() => import('./app/(dashboard)/workflows/page'))
const WorkflowDetailPage = React.lazy(() => import('./app/(dashboard)/workflows/[id]/page'))
const AutoPage = React.lazy(() => import('./app/(dashboard)/auto/page'))
const AutoJobPage = React.lazy(() => import('./app/(dashboard)/auto/job/page'))
const AutoPatchPage = React.lazy(() => import('./app/(dashboard)/auto/patch/page'))
const AutoInspectionPage = React.lazy(() => import('./app/(dashboard)/auto/inspection/page'))
const AutoSoftwarePage = React.lazy(() => import('./app/(dashboard)/auto/software/page'))
const AutoVulnScanPage = React.lazy(() => import('./app/(dashboard)/auto/vuln-scan/page'))
const AutomationInboundPage = React.lazy(() => import('./app/(dashboard)/automation/inbound/page'))
const DeployPage = React.lazy(() => import('./app/(dashboard)/deploy/page'))
const DeployAppsPage = React.lazy(() => import('./app/(dashboard)/deploy/apps/page'))
const DeployAppDetailPage = React.lazy(() => import('./app/(dashboard)/deploy/apps/[id]/page'))
const DeployEnvironmentsPage = React.lazy(() => import('./app/(dashboard)/deploy/environments/page'))
const DeployHistoryPage = React.lazy(() => import('./app/(dashboard)/deploy/history/page'))
const DeployReleasesPage = React.lazy(() => import('./app/(dashboard)/deploy/releases/page'))
const ResourcesPage = React.lazy(() => import('./app/(dashboard)/resources/page'))
const GuidePage = React.lazy(() => import('./app/(dashboard)/guide/page'))
const MonitorPage = React.lazy(() => import('./app/(dashboard)/monitor/page'))
const MonitorPanoramaPage = React.lazy(() => import('./app/(dashboard)/monitor/panorama/page'))
const MonitorPredictPage = React.lazy(() => import('./app/(dashboard)/monitor/predict/page'))
const MonitorTargetsPage = React.lazy(() => import('./app/(dashboard)/monitor/targets/page'))
const MonitorAlertsPage = React.lazy(() => import('./app/(dashboard)/monitor/alerts/page'))
const MonitorCollectionPage = React.lazy(() => import('./app/(dashboard)/monitor/collection/page'))
const MonitorPluginsPage = React.lazy(() => import('./app/(dashboard)/monitor/plugins/page'))
const MonitorSourcesPage = React.lazy(() => import('./app/(dashboard)/monitor/sources/page'))
const MonitorIngestPage = React.lazy(() => import('./app/(dashboard)/monitor/ingest/page'))
const AIPage = React.lazy(() => import('./app/(dashboard)/ai/page'))
const AITasksPage = React.lazy(() => import('./app/(dashboard)/ai/tasks/page'))
const AIAnalyticsPage = React.lazy(() => import('./app/(dashboard)/ai/analytics/page'))
const AITeamsPage = React.lazy(() => import('./app/(dashboard)/ai/teams/page'))
const AIImpactPage = React.lazy(() => import('./app/(dashboard)/ai/impact/page'))
const AIInspectionsPage = React.lazy(() => import('./app/(dashboard)/ai/inspections/page'))
const AIMcpPage = React.lazy(() => import('./app/(dashboard)/ai/mcp/page'))
const AIMarketplacePage = React.lazy(() => import('./app/(dashboard)/ai/marketplace/page'))
const AIScenesPage = React.lazy(() => import('./app/(dashboard)/ai/scenes/page'))
const AIDrillsPage = React.lazy(() => import('./app/(dashboard)/ai/drills/page'))
const AISREPage = React.lazy(() => import('./app/(dashboard)/ai/sre/page'))
const AICodingPage = React.lazy(() => import('./app/(dashboard)/ai/coding/page'))
const AISettingsPage = React.lazy(() => import('./app/(dashboard)/ai/settings/page'))
const AIChatOpsPage = React.lazy(() => import('./app/(dashboard)/ai/chatops/page'))
const AIModelsLayoutPage = React.lazy(() => import('./app/(dashboard)/ai/models/layout'))
const AIModelListPage = React.lazy(() => import('./app/(dashboard)/ai/models/list/page'))
const AIModelNewPage = React.lazy(() => import('./app/(dashboard)/ai/models/new/page'))
const AIModelDetailPage = React.lazy(() => import('./app/(dashboard)/ai/models/[id]/page'))
const CMDBPage = React.lazy(() => import('./app/(dashboard)/cmdb/page'))
const CMDBStatsPage = React.lazy(() => import('./app/(dashboard)/cmdb/stats/page'))
const CMDBScreenPage = React.lazy(() => import('./app/cmdb/screen/page'))
const CMDBAutoCollectPage = React.lazy(() => import('./app/(dashboard)/cmdb/auto-collect/page'))
const CMDBModelsPage = React.lazy(() => import('./app/(dashboard)/cmdb/models/page'))
const CMDBImpactPage = React.lazy(() => import('./app/(dashboard)/cmdb/impact/page'))
const CMDBQualityPage = React.lazy(() => import('./app/(dashboard)/cmdb/quality/page'))
const CMDBQueryPage = React.lazy(() => import('./app/(dashboard)/cmdb/query/page'))
const CMDBInstancesPage = React.lazy(() => import('./app/(dashboard)/cmdb/instances/page'))
const CMDBInstanceNewPage = React.lazy(() => import('./app/(dashboard)/cmdb/instances/new/page'))
const CMDBInstanceDetailPage = React.lazy(() => import('./app/(dashboard)/cmdb/instances/[id]/page'))
const CMDBDataPolicyPage = React.lazy(() => import('./app/(dashboard)/cmdb/data-policy/page'))
const CMDBHistoryPage = React.lazy(() => import('./app/(dashboard)/cmdb/history/page'))
const CMDBTopologyPage = React.lazy(() => import('./app/(dashboard)/cmdb/topology/page'))
const CMDBSubscriptionsPage = React.lazy(() => import('./app/(dashboard)/cmdb/subscriptions/page'))
const CMDBIdcPage = React.lazy(() => import('./app/(dashboard)/cmdb/idc/page'))
const CMDBSearchPage = React.lazy(() => import('./app/(dashboard)/cmdb/search/page'))
const CMDBSnapshotsPage = React.lazy(() => import('./app/(dashboard)/cmdb/snapshots/page'))
const CMDBUsagePage = React.lazy(() => import('./app/(dashboard)/cmdb/usage/page'))
const ScriptsPage = React.lazy(() => import('./app/(dashboard)/scripts/page'))
const ScriptDetailPage = React.lazy(() => import('./app/(dashboard)/scripts/[id]/page'))
const KnowledgePage = React.lazy(() => import('./app/(dashboard)/knowledge/page'))
const KnowledgeCategoriesPage = React.lazy(() => import('./app/(dashboard)/knowledge/categories/page'))
const KnowledgeNewPage = React.lazy(() => import('./app/(dashboard)/knowledge/new/page'))
const KnowledgeDetailPage = React.lazy(() => import('./app/(dashboard)/knowledge/[id]/page'))
const SystemNotificationsPage = React.lazy(() => import('./app/(dashboard)/system/notifications/page'))
const SystemGroupsPage = React.lazy(() => import('./app/(dashboard)/system/groups/page'))
const SystemHealthPage = React.lazy(() => import('./app/(dashboard)/system/health/page'))
const SystemCollectionPolicyPage = React.lazy(() => import('./app/(dashboard)/system/collection-policy/page'))
const SystemRolesPage = React.lazy(() => import('./app/(dashboard)/system/roles/page'))
const SystemAuthPage = React.lazy(() => import('./app/(dashboard)/system/auth/page'))
const SystemUsersPage = React.lazy(() => import('./app/(dashboard)/system/users/page'))
const SystemAPIKeysPage = React.lazy(() => import('./app/(dashboard)/system/api-keys/page'))
const SystemSettingsPage = React.lazy(() => import('./app/(dashboard)/system/settings/page'))
const AuditPage = React.lazy(() => import('./app/(dashboard)/audit/page'))
const LowcodePage = React.lazy(() => import('./app/(dashboard)/lowcode/page'))
const LowcodePagesPage = React.lazy(() => import('./app/(dashboard)/lowcode/pages/page'))
const LowcodeModelsPage = React.lazy(() => import('./app/(dashboard)/lowcode/models/page'))
const ITSMProcessPage = React.lazy(() => import('./app/(dashboard)/itsm/process/page'))
const ITSMProcessDetailPage = React.lazy(() => import('./app/(dashboard)/itsm/process/[id]/page'))
const ITSMReportsPage = React.lazy(() => import('./app/(dashboard)/itsm/reports/page'))
const ITSMOlaPage = React.lazy(() => import('./app/(dashboard)/itsm/ola/page'))
const ITSMOncallPage = React.lazy(() => import('./app/(dashboard)/itsm/oncall/page'))
const ITSMOncallCalendarPage = React.lazy(() => import('./app/(dashboard)/itsm/oncall/calendar/page'))
const ITSMOverviewPage = React.lazy(() => import('./app/(dashboard)/itsm/overview/page'))
const ITSMChangeAssessPage = React.lazy(() => import('./app/(dashboard)/itsm/change-assess/page'))
const ITSMSlaPage = React.lazy(() => import('./app/(dashboard)/itsm/sla/page'))
const ITSMFormDictsPage = React.lazy(() => import('./app/(dashboard)/itsm/form-dicts/page'))
const ITSMInboundPage = React.lazy(() => import('./app/(dashboard)/itsm/inbound/page'))
const ITSMKnowledgePage = React.lazy(() => import('./app/(dashboard)/itsm/knowledge/page'))
const ITSMSlaBreachedPage = React.lazy(() => import('./app/(dashboard)/itsm/sla-breached/page'))
const ITSMCatalogPage = React.lazy(() => import('./app/(dashboard)/itsm/catalog/page'))
const ITSMSlaOlaPage = React.lazy(() => import('./app/(dashboard)/itsm/sla-ola/page'))
const ISMTPortalPage = React.lazy(() => import('./app/(dashboard)/itsm/portal/page'))
const ITSMChangePage = React.lazy(() => import('./app/(dashboard)/itsm/change/page'))
const ITSMTicketsPage = React.lazy(() => import('./app/(dashboard)/itsm/tickets/page'))
const ITSMTicketSlaPage = React.lazy(() => import('./app/(dashboard)/itsm/tickets/[id]/sla/page'))
const ITSMTicketSlaLogPage = React.lazy(() => import('./app/(dashboard)/itsm/tickets/[id]/sla-log/page'))

class ErrorBoundary extends React.Component<
  { children: React.ReactNode },
  { hasError: boolean; error: Error | null }
> {
  state: { hasError: boolean; error: Error | null } = { hasError: false, error: null }

  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error }
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{ padding: 32, textAlign: 'center' }}>
          <h1>Something went wrong</h1>
          <pre style={{ marginTop: 16, color: 'var(--destructive)' }}>
            {this.state.error?.message}
          </pre>
          <button
            style={{ marginTop: 16, padding: '8px 16px', cursor: 'pointer' }}
            onClick={() => this.setState({ hasError: false, error: null })}
          >
            Try again
          </button>
        </div>
      )
    }
    return this.props.children
  }
}

function App() {
  return (
    <ErrorBoundary>
      <Providers>
        <Suspense fallback={<div style={{ padding: 32, textAlign: 'center' }}>Loading...</div>}>
          <Routes>
              <Route path="/login" element={<LoginPage />} />

              <Route element={<DashboardLayout />}>
                <Route path="/dashboard" element={<DashboardPage />} />
                <Route path="/credentials" element={<CredentialsPage />} />
                <Route path="/reports" element={<ReportsPage />} />
                <Route path="/schedules" element={<SchedulesPage />} />
                <Route path="/workflows" element={<WorkflowsPage />} />
                <Route path="/workflows/:id" element={<WorkflowDetailPage />} />
                <Route path="/auto" element={<AutoPage />} />
                <Route path="/auto/job" element={<AutoJobPage />} />
                <Route path="/auto/patch" element={<AutoPatchPage />} />
                <Route path="/auto/inspection" element={<AutoInspectionPage />} />
                <Route path="/auto/software" element={<AutoSoftwarePage />} />
                <Route path="/auto/vuln-scan" element={<AutoVulnScanPage />} />
                <Route path="/automation/inbound" element={<AutomationInboundPage />} />
                <Route path="/deploy" element={<DeployPage />} />
                <Route path="/deploy/apps" element={<DeployAppsPage />} />
                <Route path="/deploy/apps/:id" element={<DeployAppDetailPage />} />
                <Route path="/deploy/environments" element={<DeployEnvironmentsPage />} />
                <Route path="/deploy/history" element={<DeployHistoryPage />} />
                <Route path="/deploy/releases" element={<DeployReleasesPage />} />
                <Route path="/resources" element={<ResourcesPage />} />
                <Route path="/guide" element={<GuidePage />} />
                <Route path="/monitor" element={<MonitorPage />} />
                <Route path="/monitor/panorama" element={<MonitorPanoramaPage />} />
                <Route path="/monitor/predict" element={<MonitorPredictPage />} />
                <Route path="/monitor/targets" element={<MonitorTargetsPage />} />
                <Route path="/monitor/alerts" element={<MonitorAlertsPage />} />
                <Route path="/monitor/collection" element={<MonitorCollectionPage />} />
                <Route path="/monitor/plugins" element={<MonitorPluginsPage />} />
                <Route path="/monitor/sources" element={<MonitorSourcesPage />} />
                <Route path="/monitor/ingest" element={<MonitorIngestPage />} />
                <Route path="/ai" element={<AIPage />} />
                <Route path="/ai/tasks" element={<AITasksPage />} />
                <Route path="/ai/analytics" element={<AIAnalyticsPage />} />
                <Route path="/ai/teams" element={<AITeamsPage />} />
                <Route path="/ai/impact" element={<AIImpactPage />} />
                <Route path="/ai/inspections" element={<AIInspectionsPage />} />
                <Route path="/ai/mcp" element={<AIMcpPage />} />
                <Route path="/ai/marketplace" element={<AIMarketplacePage />} />
                <Route path="/ai/scenes" element={<AIScenesPage />} />
                <Route path="/ai/drills" element={<AIDrillsPage />} />
                <Route path="/ai/sre" element={<AISREPage />} />
                <Route path="/ai/coding" element={<AICodingPage />} />
                <Route path="/ai/settings" element={<AISettingsPage />} />
                <Route path="/ai/chatops" element={<AIChatOpsPage />} />
                {/* 模型管理：列表（唯一定义入口，含权重/类型/可见性/主LLM）+ 新增 / 详情子路由 */}
                <Route path="/ai/models" element={<AIModelsLayoutPage />}>
                  <Route index element={<Navigate to="list" replace />} />
                  <Route path="list" element={<AIModelListPage />} />
                  <Route path="new" element={<AIModelNewPage />} />
                  <Route path=":id" element={<AIModelDetailPage />} />
                </Route>
                <Route path="/cmdb" element={<CMDBPage />} />
                <Route path="/cmdb/stats" element={<CMDBStatsPage />} />
                <Route path="/cmdb/auto-collect" element={<CMDBAutoCollectPage />} />
                <Route path="/cmdb/models" element={<CMDBModelsPage />} />
                <Route path="/cmdb/impact" element={<CMDBImpactPage />} />
                <Route path="/cmdb/quality" element={<CMDBQualityPage />} />
                <Route path="/cmdb/query" element={<CMDBQueryPage />} />
                <Route path="/cmdb/instances" element={<CMDBInstancesPage />} />
                <Route path="/cmdb/instances/new" element={<CMDBInstanceNewPage />} />
                <Route path="/cmdb/instances/:id" element={<CMDBInstanceDetailPage />} />
                <Route path="/cmdb/data-policy" element={<CMDBDataPolicyPage />} />
                <Route path="/cmdb/history" element={<CMDBHistoryPage />} />
                <Route path="/cmdb/topology" element={<CMDBTopologyPage />} />
                <Route path="/cmdb/subscriptions" element={<CMDBSubscriptionsPage />} />
                <Route path="/cmdb/idc" element={<CMDBIdcPage />} />
                <Route path="/cmdb/search" element={<CMDBSearchPage />} />
                <Route path="/cmdb/snapshots" element={<CMDBSnapshotsPage />} />
                <Route path="/cmdb/usage" element={<CMDBUsagePage />} />
                <Route path="/scripts" element={<ScriptsPage />} />
                <Route path="/scripts/:id" element={<ScriptDetailPage />} />
                <Route path="/knowledge" element={<KnowledgePage />} />
                <Route path="/knowledge/categories" element={<KnowledgeCategoriesPage />} />
                <Route path="/knowledge/new" element={<KnowledgeNewPage />} />
                <Route path="/knowledge/:id" element={<KnowledgeDetailPage />} />
                <Route path="/system/notifications" element={<SystemNotificationsPage />} />
                <Route path="/system/groups" element={<SystemGroupsPage />} />
                <Route path="/system/health" element={<SystemHealthPage />} />
                <Route path="/system/collection-policy" element={<SystemCollectionPolicyPage />} />
                <Route path="/system/roles" element={<SystemRolesPage />} />
                <Route path="/system/auth" element={<SystemAuthPage />} />
                <Route path="/system/users" element={<SystemUsersPage />} />
                <Route path="/system/api-keys" element={<SystemAPIKeysPage />} />
                <Route path="/system/settings" element={<SystemSettingsPage />} />
                <Route path="/audit" element={<AuditPage />} />
                <Route path="/lowcode" element={<LowcodePage />} />
                <Route path="/lowcode/pages" element={<LowcodePagesPage />} />
                <Route path="/lowcode/models" element={<LowcodeModelsPage />} />
                <Route path="/itsm/process" element={<ITSMProcessPage />} />
                <Route path="/itsm/process/:id" element={<ITSMProcessDetailPage />} />
                <Route path="/itsm/reports" element={<ITSMReportsPage />} />
                <Route path="/itsm/ola" element={<ITSMOlaPage />} />
                <Route path="/itsm/oncall" element={<ITSMOncallPage />} />
                <Route path="/itsm/oncall/calendar" element={<ITSMOncallCalendarPage />} />
                <Route path="/itsm/overview" element={<ITSMOverviewPage />} />
                <Route path="/itsm/change-assess" element={<ITSMChangeAssessPage />} />
                <Route path="/itsm/sla" element={<ITSMSlaPage />} />
                <Route path="/itsm/form-dicts" element={<ITSMFormDictsPage />} />
                <Route path="/itsm/inbound" element={<ITSMInboundPage />} />
                <Route path="/itsm/knowledge" element={<ITSMKnowledgePage />} />
                <Route path="/itsm/sla-breached" element={<ITSMSlaBreachedPage />} />
                <Route path="/itsm/catalog" element={<ITSMCatalogPage />} />
                <Route path="/itsm/sla-ola" element={<ITSMSlaOlaPage />} />
                <Route path="/itsm/portal" element={<ISMTPortalPage />} />
                <Route path="/itsm/change" element={<ITSMChangePage />} />
                <Route path="/itsm/tickets" element={<ITSMTicketsPage />} />
                <Route path="/itsm/tickets/:id/sla" element={<ITSMTicketSlaPage />} />
                <Route path="/itsm/tickets/:id/sla-log" element={<ITSMTicketSlaLogPage />} />
              </Route>

              {/* 独立投放大屏：脱离后台框架，整屏固定深色 */}
              <Route path="/cmdb/screen" element={<CMDBScreenPage />} />

              <Route path="/" element={<Navigate to="/dashboard" replace />} />
              <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Routes>
        </Suspense>
      </Providers>
    </ErrorBoundary>
  )
}

export default App
