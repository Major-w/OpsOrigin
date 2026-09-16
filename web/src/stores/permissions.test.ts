import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { useCan, useCanAny, usePermissions } from './permissions'
import { jsonRes, stubFetch } from '@/test/mock-api'

describe('permissions / useCan', () => {
  beforeEach(() => {
    localStorage.clear()
    usePermissions.setState({ permissions: [], loaded: false })
    vi.unstubAllGlobals()
  })
  afterEach(() => vi.restoreAllMocks())

  it('未加载时乐观放行', () => {
    const { result } = renderHook(() => useCan('cmdb:delete'))
    expect(result.current).toBe(true)
  })

  it('加载后按权限集合判断', async () => {
    stubFetch(async () => jsonRes({ permissions: ['cmdb:read', 'cmdb:write'] }))
    await usePermissions.getState().fetchMyPermissions()

    const { result: canRead } = renderHook(() => useCan('cmdb:read'))
    const { result: canDelete } = renderHook(() => useCan('cmdb:delete'))
    expect(canRead.current).toBe(true)
    expect(canDelete.current).toBe(false)
    expect(JSON.parse(localStorage.getItem('opsorigin_permissions') || '[]')).toEqual([
      'cmdb:read',
      'cmdb:write',
    ])
  })

  it('useCanAny：任一命中即放行', async () => {
    stubFetch(async () => jsonRes({ permissions: ['itsm:write'] }))
    await usePermissions.getState().fetchMyPermissions()

    const { result: ok } = renderHook(() => useCanAny(['itsm:read', 'itsm:write']))
    const { result: no } = renderHook(() => useCanAny(['cmdb:admin']))
    expect(ok.current).toBe(true)
    expect(no.current).toBe(false)
  })

  it('拉取失败不置 loaded，继续乐观放行', async () => {
    stubFetch(async () => jsonRes({ error: 'down' }, 500))
    await usePermissions.getState().fetchMyPermissions()
    expect(usePermissions.getState().loaded).toBe(false)

    const { result } = renderHook(() => useCan('cmdb:admin'))
    expect(result.current).toBe(true)
  })

  it('fetch 打到 /api/v1/iam/me/permissions', async () => {
    const fetchMock = stubFetch(async () => jsonRes({ permissions: [] }))
    await usePermissions.getState().fetchMyPermissions()
    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(String(fetchMock.mock.calls[0][0])).toBe('/api/v1/iam/me/permissions')
  })
})
