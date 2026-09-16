import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@/test/render'
import userEvent from '@testing-library/user-event'
import { ErrorState } from './error-state'

describe('ErrorState', () => {
  it('展示错误文案；无 onRetry 时不出现重试按钮', () => {
    render(<ErrorState message="加载失败" />)
    expect(screen.getByText('加载失败')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /重试/ })).toBeNull()
  })

  it('点击重试触发 onRetry', async () => {
    const user = userEvent.setup()
    const onRetry = vi.fn()
    render(<ErrorState message="网络错误" onRetry={onRetry} />)
    await user.click(screen.getByRole('button', { name: /重试/ }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
})
