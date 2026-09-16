import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { apiFetch, apiFetchRaw } from '@/lib/utils'
import { jsonRes, stubFetch } from '@/test/mock-api'

describe('apiFetch', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.unstubAllGlobals()
  })
  afterEach(() => vi.restoreAllMocks())

  it('自动前置 /api/v1，并带上 Bearer 与 X-Trace-ID', async () => {
    const fetchMock = stubFetch(async () => jsonRes({ ok: true }))
    localStorage.setItem('opsorigin_token', 'tok-1')
    localStorage.setItem(
      'opsorigin_user',
      JSON.stringify({ user_id: 'u1', username: 'admin' }),
    )

    await expect(apiFetch<{ ok: boolean }>('/scripts')).resolves.toEqual({ ok: true })

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/scripts')
    const headers = init?.headers as Record<string, string>
    expect(headers.Authorization).toBe('Bearer tok-1')
    expect(headers['X-Trace-ID']).toBeTruthy()
    expect(headers['X-Principal-Type']).toBe('HUMAN')
    expect(headers['X-Principal-ID']).toBe('u1')
  })

  it('调用方自带 Content-Type 时不能覆盖鉴权头', async () => {
    const fetchMock = stubFetch(async () => jsonRes({}))
    localStorage.setItem('opsorigin_token', 'tok-1')

    await apiFetch('/x', { headers: { 'Content-Type': 'application/json' } })

    const headers = fetchMock.mock.calls[0][1]?.headers as Record<string, string>
    expect(headers.Authorization).toBe('Bearer tok-1')
    expect(headers['Content-Type']).toBe('application/json')
  })

  it('401 清 token 并抛 unauthorized', async () => {
    stubFetch(async () => jsonRes({}, 401))
    localStorage.setItem('opsorigin_token', 'tok-1')
    localStorage.setItem('opsorigin_user', JSON.stringify({ user_id: 'u1' }))

    await expect(apiFetch('/x')).rejects.toThrow('unauthorized')
    expect(localStorage.getItem('opsorigin_token')).toBeNull()
    expect(localStorage.getItem('opsorigin_user')).toBeNull()
  })

  it('非 2xx 抛出 body.error', async () => {
    stubFetch(async () => jsonRes({ error: 'forbidden' }, 403))
    await expect(apiFetch('/x')).rejects.toThrow('forbidden')
  })

  it('204 返回 undefined', async () => {
    stubFetch(async () => jsonRes(undefined, 204))
    await expect(apiFetch('/x')).resolves.toBeUndefined()
  })
})

describe('apiFetchRaw', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.unstubAllGlobals()
  })
  afterEach(() => vi.restoreAllMocks())

  it('redirectOn401=false 时 401 抛错但保留 token', async () => {
    stubFetch(async () => jsonRes({}, 401))
    localStorage.setItem('opsorigin_token', 'tok-1')

    await expect(apiFetchRaw('/export', {}, { redirectOn401: false })).rejects.toThrow(
      'unauthorized',
    )
    expect(localStorage.getItem('opsorigin_token')).toBe('tok-1')
  })
})
