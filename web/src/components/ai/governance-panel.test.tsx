import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@/test/render'
import userEvent from '@testing-library/user-event'
import { GovernancePanel } from './governance-panel'

// 覆盖治理面板 F2.4 权限门控: 轨迹读视图受 ai:trace:read 控制, 数字员工列表不受影响。
// 同时钉死上一轮修复的「挂载即加载轨迹」行为与 F3.2 错误态重试。
const h = vi.hoisted(() => ({
  fetch: vi.fn(),
  canTrace: true as boolean,
  agents: [] as unknown[],
  turns: [] as unknown[],
  failTraceOnce: false,
  success: vi.fn(),
  error: vi.fn(),
}))

vi.mock('@/lib/utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/utils')>()
  // 保留 cn 等真实导出(ui 组件依赖), 仅替换网络调用。
  return { ...actual, apiFetch: (url: string, opts?: unknown) => h.fetch(url, opts) }
})
vi.mock('@/stores/permissions', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/permissions')>()
  return { ...actual, useCan: () => h.canTrace }
})
vi.mock('@/lib/message', () => ({ getMessage: () => ({ success: h.success, error: h.error }) }))

const agent = {
  id: 'a1',
  name: '值班助手',
  owner_id: 'u1',
  visibility: 'private',
  permission: 'owner',
}

const turn = {
  id: 't1',
  agent_id: 'a1',
  conversation_id: 'c1',
  started_at: '2026-11-12T10:00:00Z',
  prompt: '如何扩容 Kafka？',
  steps: [{ index: 0 }],
}

const traceCalls = () => h.fetch.mock.calls.filter(c => String(c[0]).startsWith('/ai/trajectories'))

beforeEach(() => {
  h.fetch.mockReset()
  h.canTrace = true
  h.agents = [agent]
  h.turns = [turn]
  h.failTraceOnce = false
  h.success.mockReset()
  h.error.mockReset()
  h.fetch.mockImplementation(async (url: string) => {
    if (url.startsWith('/ai/trajectories')) {
      if (h.failTraceOnce) {
        h.failTraceOnce = false
        throw new Error('HTTP 403')
      }
      return h.turns
    }
    if (url === '/ai/agents') return h.agents
    return []
  })
})

describe('GovernancePanel 轨迹权限门控', () => {
  it('有 ai:trace:read 时挂载即拉员工与轨迹, 轨迹列表正常渲染', async () => {
    render(<GovernancePanel />)
    expect(await screen.findByText('如何扩容 Kafka？')).toBeInTheDocument()
    expect(screen.getByText('值班助手')).toBeInTheDocument()
    expect(h.fetch.mock.calls.some(c => c[0] === '/ai/agents')).toBe(true)
    expect(traceCalls()).toHaveLength(1)
    expect(screen.getByPlaceholderText('按会话 ID 筛选（留空查全部）')).toBeInTheDocument()
  })

  it('无 ai:trace:read 时不发轨迹请求, 显示无权限态, 员工列表仍可见', async () => {
    h.canTrace = false
    render(<GovernancePanel />)
    expect(await screen.findByText(/当前角色无 ai:trace:read 权限/)).toBeInTheDocument()
    expect(await screen.findByText('值班助手')).toBeInTheDocument()
    expect(traceCalls()).toHaveLength(0)
    expect(screen.queryByPlaceholderText('按会话 ID 筛选（留空查全部）')).not.toBeInTheDocument()
    expect(screen.queryByText('暂无运行轨迹（发起一次对话后即可看到）')).not.toBeInTheDocument()
  })

  it('轨迹加载失败进入 ErrorState, 重试后恢复列表', async () => {
    const user = userEvent.setup()
    h.failTraceOnce = true
    render(<GovernancePanel />)
    expect(await screen.findByText('HTTP 403')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('如何扩容 Kafka？')).toBeInTheDocument()
    expect(traceCalls()).toHaveLength(2)
  })

  it('有权限但无轨迹数据时显示空态, 不误报权限不足', async () => {
    h.turns = []
    render(<GovernancePanel />)
    expect(await screen.findByText('暂无运行轨迹（发起一次对话后即可看到）')).toBeInTheDocument()
    expect(screen.queryByText(/当前角色无 ai:trace:read 权限/)).not.toBeInTheDocument()
  })
})
