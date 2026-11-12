import { describe, it, expect, vi, beforeEach } from 'vitest'
import { TextDecoder as NodeTextDecoder, TextEncoder as NodeTextEncoder } from 'node:util'

// chat.ts 用 TextDecoder 解码 SSE 流; 从 node:util 统一注入, 避免 jsdom/vitest 环境差异。
Object.assign(globalThis, { TextDecoder: NodeTextDecoder, TextEncoder: NodeTextEncoder })

// 团队流走 /ai/chat + executor=team, 后端输出 v1 帧, chat.ts 负责翻译回 TeamStreamEvent。
// 本文件钉死两个易回归行为: 顶层错误不能丢文案; 成员级错误必须带身份且不能升级成整轮失败。
const h = vi.hoisted(() => ({
  response: null as unknown,
  onError: vi.fn(),
  onTeamEvent: vi.fn(),
  toastError: vi.fn(),
}))

vi.mock('@/lib/utils', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/utils')>()
  return { ...actual, apiFetchRaw: async () => h.response }
})
vi.mock('@/lib/message', () => ({
  getMessage: () => ({ success: vi.fn(), error: h.toastError }),
}))

function sseResponse(body: string) {
  const bytes = new TextEncoder().encode(body)
  let sent = false
  return {
    ok: true,
    status: 200,
    body: {
      getReader: () => ({
        read: async () => {
          if (!sent) {
            sent = true
            return { done: false, value: bytes }
          }
          return { done: true, value: undefined }
        },
      }),
    },
  } as unknown as Response
}

async function runTeamStream(frames: string) {
  h.response = sseResponse(frames + 'data: [DONE]\n\n')
  const { useChatStore } = await import('./chat')
  await useChatStore.getState().streamChat(
    'hi',
    () => {},
    () => {},
    [],
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    (m: string) => h.onError(m),
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    undefined,
    { type: 'team', id: 't1' },
    (ev: unknown) => h.onTeamEvent(ev),
  )
}

beforeEach(() => {
  h.onError.mockReset()
  h.onTeamEvent.mockReset()
  h.toastError.mockReset()
})

describe('团队 v1 错误帧翻译', () => {
  it('顶层错误透出可读原因, 触发全局错误并标记 turn', async () => {
    await runTeamStream('event: error\ndata: 模型不可用\n\n')
    expect(h.toastError).toHaveBeenCalledWith(expect.stringContaining('模型不可用'))
    expect(h.onError).toHaveBeenCalledWith(expect.stringContaining('模型不可用'))
    expect(h.onTeamEvent).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'error', error: expect.stringContaining('模型不可用') }),
    )
  })

  it('成员级错误带身份转发, 不弹全局 toast 也不升级整轮失败', async () => {
    await runTeamStream(
      'event: error\ndata: {"message":"成员调用失败","agent_id":"m1","agent_name":"巡检员","role":"member"}\n\n',
    )
    expect(h.toastError).not.toHaveBeenCalled()
    expect(h.onError).not.toHaveBeenCalled()
    expect(h.onTeamEvent).toHaveBeenCalledWith({
      type: 'error',
      error: expect.stringContaining('成员调用失败'),
      agent_id: 'm1',
      agent_name: '巡检员',
      role: 'member',
    })
  })
})

describe('团队 v1 token 帧身份', () => {
  it('普通 token 携带 agent_name 与 role', async () => {
    await runTeamStream(
      'event: agent_token\ndata: {"agent_id":"m1","agent_name":"巡检员","role":"member","content":"ok"}\n\n',
    )
    expect(h.onTeamEvent).toHaveBeenCalledWith({
      type: 'token',
      agent_id: 'm1',
      agent_name: '巡检员',
      role: 'member',
      content: 'ok',
    })
  })

  it('主管点评用 member_role 还原真实角色, 不被帧标记覆盖', async () => {
    await runTeamStream(
      'event: agent_token\ndata: {"agent_id":"s1","agent_name":"主管","role":"supervisor","member_role":"supervisor_reviewer","content":"需修订"}\n\n',
    )
    expect(h.onTeamEvent).toHaveBeenCalledWith({
      type: 'supervisor_review',
      agent_id: 's1',
      agent_name: '主管',
      role: 'supervisor_reviewer',
      content: '需修订',
    })
  })
})
