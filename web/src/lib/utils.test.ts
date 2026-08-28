// 通用工具单测：cn（类名合并）/ formatTime（时间格式化）/ apiList（列表接口兜底）。
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cn, formatTime, apiList } from '@/lib/utils'

// 构造一个最小化的 fetch Response 替身，避免依赖 jsdom 是否提供全局 Response。
function mockRes(body: unknown, status = 200) {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: () => Promise.resolve(body),
  } as unknown as Response
}

describe('cn', () => {
  it('合并多个类名', () => {
    expect(cn('a', 'b')).toBe('a b')
  })

  it('丢弃 falsy 值', () => {
    expect(cn('a', false, null, undefined, 'b')).toBe('a b')
  })

  it('tailwind 冲突以最后一个为准（twMerge）', () => {
    expect(cn('px-2', 'px-4')).toBe('px-4')
  })
})

describe('formatTime', () => {
  it('null/undefined 返回空串', () => {
    expect(formatTime(null)).toBe('')
    expect(formatTime(undefined)).toBe('')
  })

  it('ISO 字符串转为本地可读格式', () => {
    const out = formatTime('2026-08-05T08:00:00Z')
    expect(out).toContain('2026')
  })
})

describe('apiList 兜底（永不返回 null/非数组，防白屏）', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.restoreAllMocks())

  it('后端返回 null（Go nil slice）→ 返回 []', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(mockRes(null)))
    const r = await apiList<{ id: string }>('/x')
    expect(r).toEqual([])
  })

  it('分页信封 { items } → 返回 items 数组', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(mockRes({ items: [{ id: '1' }], total: 1 })),
    )
    const r = await apiList<{ id: string }>('/x')
    expect(r).toEqual([{ id: '1' }])
  })

  it('直接返回数组 → 原样透传', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(mockRes([{ id: '1' }, { id: '2' }])),
    )
    const r = await apiList<{ id: string }>('/x')
    expect(r).toHaveLength(2)
  })

  it('401 → 抛出 unauthorized', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(mockRes({}, 401)))
    await expect(apiList('/x')).rejects.toThrow('unauthorized')
  })
})
