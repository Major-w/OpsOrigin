import {
  MessagesSquare, Package, Radar, Users, Gauge, FlaskConical,
  Store, Wrench, BarChart3, Settings, Cpu, type LucideIcon,
} from 'lucide-react'

export interface AINavItem {
  to: string
  label: string
  icon: LucideIcon
}

// 「AI 助手」分组菜单项。Layout 侧边栏与纯净模式左侧功能区共享此单一数据源，
// 确保两处菜单永远一致：改动只在此一处。
export const AI_ASSISTANT_NAV: AINavItem[] = [
  { to: '/ai', label: 'AI 对话', icon: MessagesSquare },
  { to: '/ai/scenes', label: '场景包', icon: Package },
  { to: '/ai/inspections', label: '主动巡检', icon: Radar },
  { to: '/ai/teams', label: '智能体团队', icon: Users },
  { to: '/ai/impact', label: '运营效果', icon: Gauge },
  { to: '/ai/drills', label: '故障演练', icon: FlaskConical },
  { to: '/ai/marketplace', label: '技能市场', icon: Store },
  { to: '/ai/mcp', label: 'MCP 工具', icon: Wrench },
  { to: '/ai/analytics', label: '使用分析', icon: BarChart3 },
  { to: '/ai/models', label: '模型管理', icon: Cpu },
  { to: '/ai/settings', label: '智能体工作台', icon: Settings },
]
