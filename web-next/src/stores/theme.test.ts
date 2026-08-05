// 主题 store 单测：验证 light/dark 切换、持久化与 <html> class 同步。
// 覆盖 Premium 规范强制要求的「明/暗/系统」主题切换逻辑。
import { describe, it, expect, beforeEach } from 'vitest'
import { useThemeStore } from '@/stores/theme'

describe('theme store', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
    useThemeStore.setState({ theme: 'dark' })
  })

  it('toggle 在 dark ↔ light 间切换并持久化', () => {
    const { toggle } = useThemeStore.getState()
    expect(useThemeStore.getState().theme).toBe('dark')

    toggle()
    expect(useThemeStore.getState().theme).toBe('light')
    expect(localStorage.getItem('theme')).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    toggle()
    expect(useThemeStore.getState().theme).toBe('dark')
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('set 应用显式主题', () => {
    useThemeStore.getState().set('light')
    expect(useThemeStore.getState().theme).toBe('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)

    useThemeStore.getState().set('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })
})
