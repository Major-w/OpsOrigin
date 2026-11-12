import type { TeamTurnLocal } from './chat-kernel'
import type { TeamStreamEvent } from '@/stores/ai/types'

/**
 * 团队流 v1 error 帧落到本地 turn 的唯一入口(从 useAIChat 的团队状态机抽出, 便于单测)。
 *
 * - 顶层错误(无 agent_id): 标记整轮 turn.error, 由 chat.ts 同时弹全局 toast。
 * - 成员级错误(带 agent_id): 只把对应成员标成 error 并带错误文案, 不升级成整轮失败;
 *   渲染层 a.status === 'error' 显示 a.error, 而 turn.error 的展示有 !agents.some(error) 守卫。
 *
 * 注意: 必须把改过的 agents 回写进返回的 turn —— 改副本但返回原 t.agents 会让错误被静默吞掉。
 */
export function applyTeamErrorEvent(turn: TeamTurnLocal, ev: Extract<TeamStreamEvent, { type: 'error' }>): TeamTurnLocal {
  if (!ev.agent_id) return { ...turn, error: ev.error }

  const agents = [...turn.agents]
  const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
  const message = ev.error ?? ''
  if (idx >= 0) {
    agents[idx] = { ...agents[idx], status: 'error', error: message }
  } else {
    // 成员错误可能先于 agent_start 到达(建连即失败), 补一条错误卡片, 否则同样不可见。
    agents.push({
      agent_id: ev.agent_id,
      agent_name: ev.agent_name ?? '',
      role: ev.role ?? '',
      status: 'error',
      text: '',
      toolCalls: [],
      error: message,
    })
  }
  return { ...turn, agents }
}
