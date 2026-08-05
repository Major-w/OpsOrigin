// Vitest 全局 setup：注入浏览器环境补齐与测试清理。
import '@testing-library/jest-dom/vitest'
import { afterEach, beforeAll, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

// 每个用例结束后清理 DOM 与 localStorage，避免用例间状态污染。
afterEach(() => {
  cleanup()
  localStorage.clear()
})

// jsdom 不实现的部分浏览器 API：echarts / tremor / @xyflow / 富文本等组件常依赖。
beforeAll(() => {
  if (!window.matchMedia) {
    window.matchMedia = vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }))
  }

  if (!('IntersectionObserver' in window)) {
    class IO {
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = vi.fn()
      takeRecords = vi.fn(() => [])
      root = null
      rootMargin = ''
      thresholds = []
    }
    ;(window as any).IntersectionObserver = IO
    ;(globalThis as any).IntersectionObserver = IO
  }

  if (!('ResizeObserver' in window)) {
    class RO {
      observe = vi.fn()
      unobserve = vi.fn()
      disconnect = vi.fn()
    }
    ;(window as any).ResizeObserver = RO
    ;(globalThis as any).ResizeObserver = RO
  }

  // jsdom 默认无 crypto.randomUUID（utils.ts 的 apiFetch 用到）
  if (!('crypto' in globalThis) || !(globalThis.crypto as Crypto).randomUUID) {
    ;(globalThis as any).crypto = {
      randomUUID: () =>
        '00000000-0000-4000-8000-' +
        Math.random().toString(16).slice(2, 10) +
        Math.random().toString(16).slice(2, 10),
    }
  }
})
