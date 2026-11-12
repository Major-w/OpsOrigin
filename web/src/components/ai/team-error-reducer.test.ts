import { describe, it, expect } from 'vitest'
import { applyTeamErrorEvent } from './team-error-reducer'
import type { TeamTurnLocal } from './chat-kernel'

const makeTurn = (): TeamTurnLocal => ({
  id: 't1',
  teamId: 'team1',
  user: '问题',
  agents: [
    { agent_id: 'm1', agent_name: '成员A', role: 'member', status: 'streaming', text: '已生成一半', toolCalls: [] },
    { agent_id: 'm2', agent_name: '成员B', role: 'member', status: 'done', text: '完成', toolCalls: [] },
  ],
  final: '',
  done: false,
})

describe('applyTeamErrorEvent', () => {
  it('顶层错误(无 agent_id)标记整轮, 成员状态不变', () => {
    const before = makeTurn()
    const after = applyTeamErrorEvent(before, { type: 'error', error: 'AI 调用失败：模型超时' })
    expect(after.error).toBe('AI 调用失败：模型超时')
    expect(after.agents.map(a => a.status)).toEqual(['streaming', 'done'])
  })

  it('成员级错误回写 agents: 对应成员标红并带错误文案, 不升级整轮失败', () => {
    const before = makeTurn()
    const after = applyTeamErrorEvent(before, {
      type: 'error', agent_id: 'm1', agent_name: '成员A', role: 'member', error: 'AI 调用失败：限流',
    })
    expect(after.agents[0].status).toBe('error')
    expect(after.agents[0].error).toBe('AI 调用失败：限流')
    expect(after.agents[0].text).toBe('已生成一半')
    expect(after.agents[1].status).toBe('done')
    expect(after.error).toBeUndefined()
  })

  it('错误早于 agent_start 到达时补一条错误卡片, 不静默吞掉', () => {
    const after = applyTeamErrorEvent(makeTurn(), {
      type: 'error', agent_id: 'm3', agent_name: '成员C', role: 'supervisor', error: 'AI 调用失败：建连失败',
    })
    expect(after.agents).toHaveLength(3)
    expect(after.agents[2]).toMatchObject({
      agent_id: 'm3', agent_name: '成员C', role: 'supervisor', status: 'error', error: 'AI 调用失败：建连失败',
    })
    expect(after.error).toBeUndefined()
  })

  it('不可变更新: 不改动传入的 turn 与其 agents', () => {
    const before = makeTurn()
    const snapshot = JSON.stringify(before)
    applyTeamErrorEvent(before, { type: 'error', agent_id: 'm1', error: 'boom' })
    expect(JSON.stringify(before)).toBe(snapshot)
  })
})
