import { describe, it, expect, beforeEach, vi } from 'vitest'
import { useTicketStore } from './tickets'

describe('ticket store', () => {
  beforeEach(() => {
    useTicketStore.setState({ tickets: [], stats: null, loading: false, error: null })
  })

  it('should initialize with empty state', () => {
    const state = useTicketStore.getState()
    expect(state.tickets).toEqual([])
    expect(state.stats).toBeNull()
    expect(state.loading).toBe(false)
  })

  it('should fetch tickets', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve([
        { id: '1', title: 'Test Ticket', status: 'open', risk_level: 'low', created_by: 'admin', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
      ]),
    })

    await useTicketStore.getState().fetch()
    const state = useTicketStore.getState()
    expect(state.tickets).toHaveLength(1)
    expect(state.tickets[0].title).toBe('Test Ticket')
  })

  it('should create ticket', async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: () => Promise.resolve({ id: 'new-1' }),
    })

    const result = await useTicketStore.getState().create({ title: 'New Ticket', risk_level: 'low' })
    expect(result).toBe(true)
  })
})
