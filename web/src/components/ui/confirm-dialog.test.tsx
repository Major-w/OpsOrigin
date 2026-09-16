import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@/test/render'
import userEvent from '@testing-library/user-event'
import { ConfirmDialog } from './confirm-dialog'

describe('ConfirmDialog', () => {
  it('无 requireText 时可直接确认', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog open title="删除实例" onClose={() => {}} onConfirm={onConfirm} />,
    )
    await user.click(screen.getByRole('button', { name: '确定' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('requireText 未匹配时确认按钮禁用', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog
        open
        title="删除模型"
        requireText="prod-db"
        onClose={() => {}}
        onConfirm={onConfirm}
      />,
    )
    const confirm = screen.getByRole('button', { name: '确定' })
    expect(confirm).toBeDisabled()
    await user.type(screen.getByPlaceholderText('请输入 prod-db 以确认'), 'wrong')
    expect(confirm).toBeDisabled()
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('输入与 requireText 完全一致后才可确认', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    render(
      <ConfirmDialog
        open
        title="删除模型"
        requireText="prod-db"
        onClose={() => {}}
        onConfirm={onConfirm}
      />,
    )
    await user.type(screen.getByPlaceholderText('请输入 prod-db 以确认'), 'prod-db')
    const confirm = screen.getByRole('button', { name: '确定' })
    expect(confirm).not.toBeDisabled()
    await user.click(confirm)
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })
})
