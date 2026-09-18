import {
  MessagesSquare, Package, Radar, Users, Gauge, FlaskConical, ShieldCheck,
  Store, Wrench, BarChart3, Settings, Cpu, Webhook, Code2, type LucideIcon,
} from 'lucide-react'
import { t } from '@/lib/i18n'

export interface AINavItem {
  to: string
  label: string
  icon: LucideIcon
}

// 「AI 助手」分组菜单项。Layout 侧边栏与纯净模式左侧功能区共享此单一数据源，
// 确保两处菜单永远一致：改动只在此一处。
export function getAINavItems(): AINavItem[] {
  return [
    { to: '/ai', label: t('ai.chat'), icon: MessagesSquare },
    { to: '/ai/scenes', label: t('ai.scenes'), icon: Package },
    { to: '/ai/inspections', label: t('ai.inspections'), icon: Radar },
    { to: '/ai/teams', label: t('ai.agentTeams'), icon: Users },
    { to: '/ai/impact', label: t('ai.opsEffect'), icon: Gauge },
    { to: '/ai/drills', label: t('ai.drill'), icon: FlaskConical },
    { to: '/ai/sre', label: t('ai.sreWorkbench'), icon: ShieldCheck },
    { to: '/ai/coding', label: t('ai.coding'), icon: Code2 },
    { to: '/ai/marketplace', label: t('ai.marketplace'), icon: Store },
    { to: '/ai/mcp', label: t('ai.mcp'), icon: Wrench },
    { to: '/ai/analytics', label: t('ai.usageAnalytics'), icon: BarChart3 },
    { to: '/ai/models', label: t('ai.models'), icon: Cpu },
    { to: '/ai/settings', label: t('ai.agentWorkbench'), icon: Settings },
    { to: '/ai/chatops', label: t('ai.chatopsChannel'), icon: Webhook },
  ]
}

// 向后兼容: 旧代码仍可 import AI_ASSISTANT_NAV
export const AI_ASSISTANT_NAV: AINavItem[] = getAINavItems()
