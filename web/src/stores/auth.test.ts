import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useAuthStore } from './auth'

describe('auth store', () => {
  beforeEach(() => {
    localStorage.clear()
    useAuthStore.setState({ token: null, user: null, loading: false, error: null })
  })

  it('should initialize with no token', () => {
    const state = useAuthStore.getState()
    expect(state.token).toBeNull()
    expect(state.user).toBeNull()
  })

  it('should set token on login', async () => {
    // Mock fetch for login
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({
        token: 'test-token',
        user_id: 'user-1',
        username: 'admin',
        role: 'admin',
        tenant_id: 'tenant-1',
      }),
    })

    await useAuthStore.getState().login('admin', 'password')
    const state = useAuthStore.getState()
    expect(state.token).toBe('test-token')
    expect(state.user?.username).toBe('admin')
  })

  it('should clear token on logout', () => {
    useAuthStore.setState({ token: 'test-token', user: { user_id: '1', username: 'admin', role: 'admin', tenant_id: '1' } })
    useAuthStore.getState().logout()
    const state = useAuthStore.getState()
    expect(state.token).toBeNull()
    expect(state.user).toBeNull()
  })

  it('should init from localStorage', () => {
    localStorage.setItem('opsorigin_token', 'stored-token')
    localStorage.setItem('opsorigin_user', JSON.stringify({ user_id: '1', username: 'admin', role: 'admin', tenant_id: '1' }))
    useAuthStore.getState().init()
    const state = useAuthStore.getState()
    expect(state.token).toBe('stored-token')
    expect(state.user?.username).toBe('admin')
  })
})
