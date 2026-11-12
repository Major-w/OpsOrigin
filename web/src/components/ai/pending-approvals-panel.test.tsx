import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@/test/render'
import userEvent from '@testing-library/user-event'
import { PendingApprovalsPanel } from './pending-approvals-panel'

// 覆盖新增面板的四态与审批闭环: 挂载即拉取、失败可重试、批准需二次确认且响应后刷新、无权限只读。
const h = vi.hoisted(() => ({
  list: vi.fn(),
  respond: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  canRespond: true as boolean,
}))

vi.mock('@/stores/ai', () => ({
  useApprovalStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({ listPendingApprovals: h.list, respondApproval: h.respond }),
}))
vi.mock('@/stores/permissions', () => ({ useCan: () => h.canRespond }))
vi.mock('@/lib/message', () => ({ getMessage: () => ({ success: h.success, error: h.error }) }))

const item = {
  id: 'ap-1',
  tool: 'script_run',
  summary: '在生产主机执行清理脚本',
  args: '{"host":"prod-01"}',
  created_at: '2026-11-12T19:59:00Z',
  deadline: '2026-11-12T20:00:00Z',
  agent_id: 'agent-1',
}

beforeEach(() => {
  h.list.mockReset().mockResolvedValue([])
  h.respond.mockReset().mockResolvedValue(true)
  h.success.mockReset()
  h.error.mockReset()
  h.canRespond = true
})

describe('PendingApprovalsPanel', () => {
  it('挂载即拉取, 空队列显示空态而不是卡在加载中', async () => {
    render(<PendingApprovalsPanel />)
    expect(screen.getByText('加载中…')).toBeInTheDocument()
    expect(await screen.findByText('当前没有待审批的操作')).toBeInTheDocument()
    expect(h.list).toHaveBeenCalledTimes(1)
  })

  it('加载失败进入 ErrorState, 重试后恢复列表', async () => {
    const user = userEvent.setup()
    h.list.mockRejectedValueOnce(new Error('HTTP 403')).mockResolvedValueOnce([item])
    render(<PendingApprovalsPanel />)
    expect(await screen.findByText('HTTP 403')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByText('在生产主机执行清理脚本')).toBeInTheDocument()
    expect(h.list).toHaveBeenCalledTimes(2)
  })

  it('批准需二次确认, 成功后 toast + 刷新并迁移到空态', async () => {
    const user = userEvent.setup()
    h.list.mockResolvedValueOnce([item]).mockResolvedValueOnce([])
    render(<PendingApprovalsPanel />)
    await user.click(await screen.findByRole('button', { name: '批准' }))
    expect(screen.getByText('批准敏感操作')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '批准执行' }))
    await waitFor(() => expect(h.respond).toHaveBeenCalledWith('ap-1', 'approve'))
    expect(h.success).toHaveBeenCalledTimes(1)
    await waitFor(() => expect(h.list).toHaveBeenCalledTimes(2))
    expect(await screen.findByText('当前没有待审批的操作')).toBeInTheDocument()
  })

  it('无 ai:write 时只读, 响应按钮禁用', async () => {
    h.canRespond = false
    h.list.mockResolvedValue([item])
    render(<PendingApprovalsPanel />)
    expect(await screen.findByRole('button', { name: '批准' })).toBeDisabled()
    expect(await screen.findByRole('button', { name: '拒绝' })).toBeDisabled()
    expect(screen.getByText(/仅可查看待审批项/)).toBeInTheDocument()
  })
})
