// Vitest 全局 setup：注入浏览器环境补齐与测试清理。
import '@testing-library/jest-dom/vitest'
import { afterEach, beforeAll, vi } from 'vitest'
import { cleanup } from '@testing-library/react'

// 恢复 localStorage：
// Node ≥22 内置实验性全局 localStorage（未传 --localstorage-file 时返回 undefined），
// 且该 key 不在 vitest 的 KEYS 白名单中 —— getWindowKeys 对「已存在于 global 的 key」
// 只在白名单内才复制 jsdom 实现，导致 jsdom 的 localStorage 从未挂到全局，
// 在 Node 22+/26 上所有 localStorage 单测与 setup 清理都会失败。
// 这里显式取 jsdom window 的实现挂回 globalThis；取不到时用内存实现兜底，
// 保证任何 Node/vitest/jsdom 组合下行为确定。
function ensureLocalStorage(): void {
  const current = globalThis.localStorage as Storage | undefined
  if (current && typeof current.getItem === 'function' && typeof current.clear === 'function') {
    return
  }
  const jsdomWindow = (globalThis as { jsdom?: { window?: Window } }).jsdom?.window
  const impl = jsdomWindow?.localStorage ?? createMemoryStorage()
  Object.defineProperty(globalThis, 'localStorage', {
    value: impl,
    configurable: true,
    writable: true,
  })
}

function createMemoryStorage(): Storage {
  const store = new Map<string, string>()
  return {
    get length() {
      return store.size
    },
    clear: () => store.clear(),
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    key: (index: number) => Array.from(store.keys())[index] ?? null,
    removeItem: (key: string) => {
      store.delete(key)
    },
    setItem: (key: string, value: string) => {
      store.set(key, String(value))
    },
  }
}

ensureLocalStorage()

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
