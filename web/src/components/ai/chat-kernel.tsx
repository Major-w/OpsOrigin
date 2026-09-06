
// ============================================================================
// AI 聊天内核（共享）
// 浮窗 AIAssistant / 纯净模式 PureMode / 全页 /ai 三入口共用同一套:
//   - useAIChat: 普通/自主/团队 三模式状态 + streamChat/streamTeamChat 编排 + 审批 + 会话持久化
//   - ChatMessageList: 消息渲染(思考折叠 / 任务列表 / 引用 / 反馈 / 重试)
//   - ChatInputBar: 模式切换 / 模型 / 思考开关 / Agent / 输入框 / 快捷操作
//   - TeamChatView: 团队模式(选队 + 多智能体流式 + 输入)
//   - ApprovalBar: 敏感操作审批条
// 通过 props/variant 适配三处差异, 消除三套重复 UI。
// ============================================================================

import { useEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { useAIStore, useTeams, type TeamStreamEvent, type AgentTeam } from '@/stores/ai'
import type { ModelInfo, PendingApproval } from '@/stores/ai/types'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import { AiMarkdown } from '@/components/ai-markdown'
import { exportDocFile } from '@/lib/doc-export'
import { cn, apiFetchRaw } from '@/lib/utils'
import {
  Bot, User, Send, Square, Loader2, Terminal, Brain, CheckCircle, AlertTriangle,
  ThumbsUp, ThumbsDown, Users, Sparkles, XCircle, Settings, Lock, Share2, Trash2,
  Paperclip, FileText, Image as ImageIcon, X, Search, Copy, RefreshCw, Check, ArrowDown,
  ChevronRight, Download, FileDown, Presentation,
} from 'lucide-react'

// ---------------------------------------------------------------------------
// 共享类型
// ---------------------------------------------------------------------------

export type ChatMode = 'normal' | 'explore' | 'team'

/** 执行者: 用户在输入前唯一需要选择的东西(替代原「模式」概念)。
 *  default=默认助手(纯对话); agent=单个数字员工/智能体(绑定工具, 按需自动多步执行);
 *  team=智能体团队(主管统管成员协作)。后端按执行者是否绑定工具自主决定行为, 无需用户区分模式。 */
export interface Executor {
  type: 'default' | 'agent' | 'team'
  /** type=agent/team 时的目标 ID; default 时留空 */
  id?: string
}

// 浮窗(AIAssistant) 与 纯净模式(PureMode) 共享同一草稿会话 —— 实现两个轻量入口草稿互通。
// 同一 persistKey 使消息跨入口续接; 同一 convIdKey 使后端会话 ID 也共享, 端到端互通。
// 全页(/ai) 为多会话后端工作台, 语义不同, 不并入此共享草稿。
export const SHARED_SESSION_KEY = 'ai.shared.session'
export const SHARED_CONV_ID_KEY = 'ai.shared.convId'

export interface ToolCallLocal {
  id: string
  name: string
  args: string
  result: string
  status: 'running' | 'done'
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  /** 思考耗时毫秒(首答 token 到达时刻 - 发送时刻), 用于「已深度思考(用时 X 秒)」 */
  thinkMs?: number
  /** 整轮回答耗时毫秒(完成时刻 - 发送时刻), 用于消息统计行(用时/速度/用量) */
  durationMs?: number
  /** 用户消息附件(上传的图片/文件缩略信息) */
  attachments?: ChatAttachmentMeta[]
  citations?: string[]
  feedback_rating?: 'up' | 'down'
  status?: 'streaming' | 'done' | 'error'
  errorText?: string
  /** 输出被 max_tokens 上限截断(上游 finish_reason=length), 由后端 SSE event:truncated 触发 */
  truncated?: string
  timestamp: number
  toolCalls?: ToolCallLocal[]
  messageId?: string
}

export interface AgentRunLocal {
  agent_id: string
  agent_name: string
  role: string
  status: 'streaming' | 'done' | 'error'
  text: string
  error?: string
  toolCalls: ToolCallLocal[]
  supervisorNote?: string
}

export interface TeamTurnLocal {
  id: string
  teamId: string
  user: string
  agents: AgentRunLocal[]
  final: string
  done: boolean
  error?: string
}

// 把思考过程文本解析成步骤(按序号/项目符号/换行), 用于折叠式"思考步骤"展示。
export function parseThinkingSteps(text: string): string[] {
  if (!text) return []
  const lines = text.split('\n').map(l => l.trim()).filter(Boolean)
  const steps: string[] = []
  for (const line of lines) {
    const m = line.match(/^(\d+[.、]|[-*•]|\s*-\s|步骤\s*\d+[.、:]?)\s*(.*)$/)
    if (m) {
      steps.push(m[2] || line)
    } else if (steps.length === 0) {
      steps.push(line)
    } else {
      steps[steps.length - 1] += ' ' + line
    }
  }
  return steps.length ? steps : [text]
}

const TEAM_ROLE_LABEL: Record<string, string> = { supervisor: '主管', member: '成员', finalizer: '终结者' }
const TEAM_MODE_LABEL: Record<string, string> = {
  supervisor: '主管调度', parallel: '并行协作', sequential: '顺序流转', debate: '辩论裁决',
}

function newConvId(prefix: string): string {
  return `${prefix}_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
}

// ---------------------------------------------------------------------------
// 通用小工具: 会话标题清洗 / 附件标记解析 / 文件大小格式化 / 主流思考卡
// ---------------------------------------------------------------------------

/** 会话标题清洗: 去思维链/内联标签/装饰符号, 压缩空白(旧会话标题是原文截断, 展示侧兜底)。 */
export function sanitizeConvTitle(title?: string): string {
  if (!title) return '新对话'
  let s = title
  try {
    // 逐行丢弃附件标记行, 去 <tag> 残片与装饰符号
    s = s.split('\n').filter(l => !l.trim().startsWith('[附件]')).join('\n')
    s = s.replace(/<\/?[A-Za-z][A-Za-z0-9_-]{0,30}>/g, ' ')
    s = s.replace(/<think[\s\S]*?(<\/think>|$)/gi, ' ')
    s = s.replace(/[#*_~`>|]+/g, ' ')
    s = s.replace(/\s+/g, ' ').trim()
  } catch { /* ignore */ }
  return s || '新对话'
}

/** 附件元数据(消息内的缩略信息)。id 存在时可下载/预览。 */
export interface ChatAttachmentMeta {
  id?: string
  name: string
  type: string
  size?: number
}

const ATTACH_LINE_RE = /^\[附件\] (.+?) \((.+?), (.+?)\)$/

/** 从消息文本解析附件标记行, 返回正文与附件列表(历史会话加载时还原 chip 渲染)。 */
export function parseAttachmentLines(content: string): { text: string; attachments: ChatAttachmentMeta[] } {
  const attachments: ChatAttachmentMeta[] = []
  const lines = content.split('\n')
  const kept: string[] = []
  for (const ln of lines) {
    const m = ln.trim().match(ATTACH_LINE_RE)
    if (m) {
      attachments.push({ name: m[1], type: m[2], size: parseSizeText(m[3]) })
    } else {
      kept.push(ln)
    }
  }
  return { text: kept.join('\n').replace(/\n{3,}$/, '\n\n').trimEnd(), attachments }
}

/** 从服务端持久化的 content_parts 提取附件引用(attachment://<id>#<name>), 历史会话加载时还原 chip / 缩略图。 */
export function extractAttachmentsFromParts(parts?: any[]): ChatAttachmentMeta[] {
  if (!parts?.length) return []
  const out: ChatAttachmentMeta[] = []
  for (const p of parts) {
    const url: string | undefined = p?.image_url?.url || p?.file_url?.url
    if (typeof url === 'string' && url.startsWith('attachment://')) {
      const raw = url.slice('attachment://'.length)
      const h = raw.indexOf('#')
      const id = h >= 0 ? raw.slice(0, h) : raw
      const name = h >= 0 ? raw.slice(h + 1) : ''
      out.push({
        id,
        name: name || '附件',
        type: p.type === 'image_url' ? 'image' : p.type === 'file_url' ? 'file' : 'application/octet-stream',
      })
    }
  }
  return out
}

/** "12.3 KB"/"2.0 MB"/"480 B" → 字节数(仅用于展示大小, 不精确也可接受)。 */
function parseSizeText(s: string): number {
  const m = s.match(/^([\d.]+)\s*(B|KB|MB|GB)?$/i)
  if (!m) return 0
  const n = parseFloat(m[1]) || 0
  const unit = (m[2] || 'B').toUpperCase()
  return Math.round(n * (unit === 'GB' ? 1 << 30 : unit === 'MB' ? 1 << 20 : unit === 'KB' ? 1 << 10 : 1))
}

/** 字节数 → 人类可读(与后端 formatSizeShort 语义一致)。 */
export function formatBytes(n: number): string {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + ' MB'
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(1) + ' KB'
  return `${n} B`
}

/** 把附件元数据格式化为追加到消息正文的标记行(与后端持久化/解析约定一致)。 */
export function formatAttachmentLines(att?: ChatAttachmentMeta[]): string {
  if (!att?.length) return ''
  return '\n\n' + att.map(a => `[附件] ${a.name} (${a.type || 'application/octet-stream'}, ${formatBytes(a.size ?? 0)})`).join('\n')
}

/** 附件 chip: 图标 + 文件名 + 大小(消息气泡内与输入区待发送区共用)。 */
export function AttachmentChip({ att, onRemove, variant = 'page' }: { att: ChatAttachmentMeta; onRemove?: () => void; variant?: 'page' | 'panel' }) {
  const isImg = att.type.startsWith('image/')
  const Icon = isImg ? ImageIcon : FileText
  return (
    <span className={cn(
      'inline-flex items-center gap-1.5 max-w-[260px] px-2 py-1 rounded-lg text-[11px] border',
      variant === 'panel'
        ? 'bg-white/10 border-white/15 text-gray-200'
        : 'bg-white dark:bg-gray-800 border-gray-200 dark:border-gray-700 text-gray-700 dark:text-gray-200',
    )}>
      <Icon className={cn('h-3.5 w-3.5 shrink-0', isImg ? 'text-blue-500' : 'text-muted-foreground')} />
      <span className="truncate font-medium">{att.name}</span>
      {att.size ? <span className="text-[9px] text-muted-foreground shrink-0">{formatBytes(att.size)}</span> : null}
      {onRemove && (
        <button onClick={onRemove} className="ml-0.5 p-0.5 rounded text-muted-foreground hover:text-red-500 transition-colors" title="移除附件">
          <X className="h-3 w-3" />
        </button>
      )}
    </span>
  )
}

/** 图片附件缩略图: 经鉴权请求对象存储二进制 → objectURL 渲染。点击在新标签打开原图。 */
export function AttachmentThumb({ att }: { att: ChatAttachmentMeta }) {
  const [url, setUrl] = useState<string | null>(null)
  useEffect(() => {
    if (!att.id || !att.type.startsWith('image/')) return
    let revoked = false
    ;(async () => {
      try {
        const res = await apiFetchRaw(`/ai/attachments/${att.id}`)
        if (!res.ok) return
        const blob = await res.blob()
        if (revoked) return
        setUrl(URL.createObjectURL(blob))
      } catch { /* 缩略图加载失败不阻塞正文 */ }
    })()
    return () => {
      revoked = true
      if (url) URL.revokeObjectURL(url)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [att.id, att.type])
  if (!url) {
    return <AttachmentChip att={att} />
  }
  return (
    <a href={`/api/v1/ai/attachments/${att.id}`} target="_blank" rel="noreferrer" title={att.name}>
      <img src={url} alt={att.name} className="max-h-40 rounded-lg border border-gray-200 dark:border-gray-700 object-cover" />
    </a>
  )
}

/**
 * 思考卡(主流 AI 交互, 参考 DeepSeek/豆包):
 *  - live(流式思考中, 尚无正文): 展开的「思考中…」区块, 实时滚动展示思考内容;
 *  - 结束/历史: 折叠为一行「已深度思考(用时 X 秒)」, 点击展开详情, 默认不打扰。
 */
export function ThinkingBlock({ thinking, live, thinkMs }: { thinking: string; live: boolean; thinkMs?: number }) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  // live 态实时计时(DeepSeek 风格「思考中… 5 秒」), 结束后由 thinkMs 静态展示
  const thinkStartRef = useRef(0)
  const [liveSecs, setLiveSecs] = useState(0)
  useEffect(() => {
    if (live && scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight
  }, [thinking, live])
  useEffect(() => {
    if (!live) return
    thinkStartRef.current = Date.now()
    const iv = setInterval(() => setLiveSecs(Math.round((Date.now() - thinkStartRef.current) / 1000)), 1000)
    return () => clearInterval(iv)
  }, [live])
  // 思考结束瞬间从展开态自动收起, 保持「完成后只留一行」的主流观感
  useEffect(() => { if (!live) setOpen(false) }, [live])

  if (live) {
    return (
      <div className="mb-2 rounded-lg border border-blue-200/60 dark:border-blue-500/25 bg-blue-50/50 dark:bg-blue-500/[0.06] overflow-hidden">
        <div className="px-3 pt-2 pb-1 flex items-center gap-1.5 text-xs text-blue-600 dark:text-blue-400 font-medium">
          <Loader2 className="h-3 w-3 animate-spin" />
          思考中…{liveSecs > 0 ? `（${liveSecs} 秒）` : ''}
        </div>
        <div ref={scrollRef} className="max-h-44 overflow-y-auto px-3 pb-2 text-xs leading-relaxed text-muted-foreground whitespace-pre-wrap">
          {thinking}
        </div>
      </div>
    )
  }
  return (
    <details open={open} onToggle={e => setOpen((e.target as HTMLDetailsElement).open)} className="mb-2 group/think">
      <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground select-none inline-flex items-center gap-1.5 list-none">
        <Brain className="h-3 w-3 text-violet-500" />
        <span className="group-open/think:hidden">已深度思考{typeof thinkMs === 'number' && thinkMs > 0 ? `（用时 ${(thinkMs / 1000).toFixed(1)} 秒）` : ''}</span>
        <span className="hidden group-open/think:inline">收起思考过程</span>
      </summary>
      <div className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-xs whitespace-pre-wrap text-muted-foreground border-l-2 border-violet-300 dark:border-violet-500/40 max-h-72 overflow-y-auto">
        {thinking}
      </div>
    </details>
  )
}

// ---------------------------------------------------------------------------
// useAIChat —— 统一聊天状态与编排
// ---------------------------------------------------------------------------

interface UseAIChatOptions {
  welcome: string
  welcomeId?: string
  // 会话持久化(localStorage): 传 key 则跨刷新续接
  persistKey?: string
  convIdKey?: string
  // 续接被拒(403)时是否清空本地会话 ID(浮窗/纯净=true; 全页=false, 由服务端历史驱动)
  resetConvOn403?: boolean
  // 自定义历史裁剪(全页带 HIST_BUDGET 压缩); 不传则取最近 20 条
  buildHistory?: (msgs: ChatMessage[]) => { role: string; content: string }[]
  // 是否把 tool_event 累积进助手消息的 toolCalls(全页=true 用于"任务列表")
  captureToolCalls?: boolean
  // 是否支持团队模式(纯净/全页=true; 浮窗=false)
  enableTeam?: boolean
  // 是否启用 abort(浮窗/纯净=true; 全页=false)
  enableAbort?: boolean
  // 思考过程已默认常开, 开关与持久化 key 均废弃(保留字段定义避免老调用报错)
  showThinkingKey?: string
  // 默认模式
  defaultMode?: ChatMode
  // 是否暴露"知识范围"选择器(全页/纯净=true; 浮窗=false)
  enableKnowledgeScope?: boolean
}

export function useAIChat(opts: UseAIChatOptions) {
  const {
    welcome,
    welcomeId = 'welcome',
    persistKey,
    convIdKey,
    resetConvOn403 = true,
    buildHistory,
    captureToolCalls = false,
    enableTeam = false,
    enableAbort = true,
    defaultMode = 'normal',
    enableKnowledgeScope = false,
  } = opts

  const { streamChat, respondApproval, models, agents, fetchAgents, fetchModels } = useAIStore()
  const teamApi = useTeams()

  const [mode, setMode] = useState<ChatMode>(defaultMode)
  // 思考过程展示默认常开(产品决策: 去掉开关按钮, 思考步骤默认折叠可见入口)
  const [showThinking] = useState<boolean>(true)
  const [selectedModel, setSelectedModel] = useState('')
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [knowledgeCategories, setKnowledgeCategories] = useState<string[]>([])

  const [messages, setMessages] = useState<ChatMessage[]>(() => {
    const welcomeMsg: ChatMessage = { id: welcomeId, role: 'assistant', content: welcome, timestamp: Date.now() }
    if (persistKey && typeof window !== 'undefined') {
      try {
        const raw = localStorage.getItem(persistKey)
        if (raw) {
          const parsed = JSON.parse(raw) as { role: string; content: string }[]
          if (Array.isArray(parsed) && parsed.length > 0) {
            return parsed.map((m, i) => ({
              id: `m_${Date.now()}_${i}`,
              role: m.role === 'user' ? 'user' : 'assistant',
              content: m.content || '',
              timestamp: Date.now(),
            }))
          }
        }
      } catch { /* ignore */ }
    }
    return [welcomeMsg]
  })

  const [input, setInput] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [pending, setPending] = useState<PendingApproval | null>(null)
  const [approving, setApproving] = useState(false)
  const [convId, setConvId] = useState<string>(() => {
    if (convIdKey && typeof window !== 'undefined') {
      try { return localStorage.getItem(convIdKey) || '' } catch { return '' }
    }
    return ''
  })

  // 团队模式状态
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)

  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => { fetchAgents(); fetchModels() }, [fetchAgents, fetchModels])
  useEffect(() => { if (enableTeam && mode === 'team') teamApi.loadTeams() }, [enableTeam, mode, teamApi])

  // 模型默认选中: 加载到模型池后, 若用户未手动选择, 默认选第一个健康模型
  useEffect(() => {
    if (!selectedModel && models.length > 0) {
      const healthy = models.find(m => m.healthy !== false) || models[0]
      setSelectedModel(healthy.model)
    }
  }, [models, selectedModel])

  // 会话持久化(localStorage, 仅存 role/content, 刷新不丢)
  useEffect(() => {
    if (!persistKey) return
    try {
      const slim = messages.filter(m => m.id !== welcomeId).map(m => ({ role: m.role, content: m.content }))
      localStorage.setItem(persistKey, JSON.stringify(slim))
    } catch { /* ignore */ }
  }, [messages, persistKey, welcomeId])

  const defaultHistory = (msgs: ChatMessage[]): { role: string; content: string }[] =>
    msgs
      .filter(m => m.id !== welcomeId && (m.role === 'user' || m.role === 'assistant'))
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

  // ----------------------- 普通 / 自主模式 -----------------------

  // 流式补全共用路径: send(新用户消息)与 regenerate(重发上一问)都走这里, 只追加助手占位气泡。
  const runStream = async (content: string, historyBase: ChatMessage[], attachments?: ChatAttachmentMeta[]) => {
    if (enableAbort) {
      abortRef.current?.abort()
      abortRef.current = new AbortController()
    }

    const aiId = `a_${Date.now()}`
    const startAt = Date.now()
    let firstTokenSeen = false
    const aiMsg: ChatMessage = { id: aiId, role: 'assistant', content: '', thinking: '', timestamp: Date.now() }
    setMessages(prev => [...prev, aiMsg])
    setStreaming(true)

    const history = buildHistory ? buildHistory(historyBase) : defaultHistory(historyBase)

    let acc = ''
    const onToken = (t: string) => {
      if (!firstTokenSeen) {
        firstTokenSeen = true
        const thinkMs = Date.now() - startAt
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, content: acc + t, thinkMs } : m)))
        acc += t
        return
      }
      acc += t
      setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, content: acc } : m)))
    }
    let thinkingAcc = ''
    const onThinking = (t: string) => {
      thinkingAcc += t
      setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, thinking: thinkingAcc } : m)))
    }
    const onAsk = (approval: PendingApproval) => setPending(approval)

    const onTool = captureToolCalls
      ? (tc: { name: string; args: any; status: 'running' | 'done'; result?: string }) => {
          setMessages(prev => prev.map(m => {
            if (m.id !== aiId) return m
            const list = m.toolCalls ? [...m.toolCalls] : []
            const argsStr = tc.args ? JSON.stringify(tc.args) : ''
            if (tc.status === 'done') {
              for (let i = list.length - 1; i >= 0; i--) {
                if (list[i].name === tc.name && list[i].status === 'running') {
                  list[i] = { ...list[i], status: 'done', result: tc.result || '', args: argsStr || list[i].args }
                  return { ...m, toolCalls: list }
                }
              }
              list.push({ id: `${tc.name}-${Date.now()}-${list.length}`, name: tc.name, args: argsStr, result: tc.result || '', status: 'done' })
            } else {
              list.push({ id: `${tc.name}-${Date.now()}-${list.length}`, name: tc.name, args: argsStr, result: '', status: 'running' })
            }
            return { ...m, toolCalls: list }
          }))
        }
      : undefined

    let cid = convId
    if (!cid) {
      cid = newConvId('chat')
      setConvId(cid)
      if (convIdKey) { try { localStorage.setItem(convIdKey, cid) } catch { /* ignore */ } }
    }

    await streamChat(
      // 附件以元数据上传(不再拼进正文): 后端转为多模态片段(attachment:// 引用), 模型可见且会话只存引用不存 base64
      content,
      onToken,
      (meta) => {
        setMessages(prev => prev.map(m => {
          if (m.id !== aiId) return m
          // 已标 error(流内 error 事件 / HTTP 失败)的消息不被 [DONE] 覆盖回 done
          if (m.status === 'error') return m
          // 正常结束但没吐任何字: 大概率 max_tokens 过小 / 模型静默失败 —— 提示而非空气泡
          if (meta?.message_id && !acc.trim()) {
            return { ...m, status: 'error', errorText: '模型未返回任何内容：可能是 max_tokens 设置过小或该模型当前不可用，请重试或切换模型' }
          }
          return { ...m, status: 'done', citations: meta?.citations ?? m.citations, messageId: meta?.message_id ?? m.messageId, durationMs: Date.now() - startAt }
        }))
        if (meta?.conversation_id && meta.conversation_id !== cid) {
          setConvId(meta.conversation_id)
          if (convIdKey) { try { localStorage.setItem(convIdKey, meta.conversation_id) } catch { /* ignore */ } }
        }
        setStreaming(false)
      },
      history,
      selectedAgent?.id,
      showThinking,
      showThinking ? onThinking : undefined,
      true, // explore 标志: 模式概念已移除, 恒为 true——后端按执行者是否绑定工具自主决定行为
      onAsk,
      cid,
      enableAbort ? abortRef.current?.signal : undefined,
      selectedModel,
      (err?: string) => {
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, status: 'error', errorText: err || 'AI 响应失败' } : m)))
        setStreaming(false)
        if (resetConvOn403 && err && err.includes('403')) {
          setConvId('')
          if (convIdKey) { try { localStorage.removeItem(convIdKey) } catch { /* ignore */ } }
        }
      },
      onTool,
      knowledgeCategories.length ? knowledgeCategories : undefined,
      // 输出被 max_tokens 截断: 不覆盖已有正文, 仅追加标记, 前端在消息末尾展示黄色提示条。
      (note?: string) => {
        setMessages(prev => prev.map(m => (m.id === aiId ? { ...m, truncated: note || '输出触达 max_tokens 上限, 可能不完整' } : m)))
      },
      attachments?.map(a => ({ id: a.id, name: a.name, type: a.type })),
    )
  }

  const send = async (raw: string, attachments?: ChatAttachmentMeta[]) => {
    const content = raw.trim()
    if (!content || streaming) return
    const userMsg: ChatMessage = { id: `u_${Date.now()}`, role: 'user', content, timestamp: Date.now() }
    if (attachments?.length) userMsg.attachments = attachments
    setMessages(prev => [...prev, userMsg])
    setInput('')
    await runStream(content, [...messages, userMsg], attachments)
  }

  // 正常态重新生成: 移除最后一条助手回复, 重发其前的用户消息(附件随消息复用)。
  const regenerate = () => {
    if (streaming) return
    let aiIdx = -1
    for (let i = messages.length - 1; i >= 0; i--) {
      if (messages[i].role === 'assistant' && messages[i].id !== welcomeId) { aiIdx = i; break }
    }
    if (aiIdx < 0) return
    let userIdx = -1
    for (let i = aiIdx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { userIdx = i; break }
    }
    if (userIdx < 0) return
    const userMsg = messages[userIdx]
    const base = [...messages.slice(0, aiIdx), ...messages.slice(aiIdx + 1)]
    setMessages(base)
    runStream(userMsg.content, base, userMsg.attachments)
  }

  const clearSession = () => {
    if (streaming) return
    if (enableAbort) { abortRef.current?.abort(); abortRef.current = null }
    if (persistKey) { try { localStorage.removeItem(persistKey) } catch { /* ignore */ } }
    if (convIdKey) { try { localStorage.removeItem(convIdKey) } catch { /* ignore */ } }
    setConvId('')
    setSelectedAgent(null)
    setTeamTurns([])
    setSelectedTeamId(null)
    setMessages([{ id: welcomeId, role: 'assistant', content: welcome, timestamp: Date.now() }])
  }

  const stopGeneration = () => {
    if (!enableAbort) return
    abortRef.current?.abort()
    abortRef.current = null
    setStreaming(false)
  }

  const respond = async (action: 'approve' | 'reject') => {
    if (!pending) return
    setApproving(true)
    const ok = await respondApproval(pending.id, action)
    setApproving(false)
    if (ok) setPending(null)
  }

  const dismissApproval = () => setPending(null)

  // ----------------------- 团队模式 -----------------------

  const handleTeamSend = async (text: string) => {
    const content = text.trim()
    if (!content || !selectedTeamId || teamStreaming) return

    const turnId = Date.now().toString()
    const newTurn: TeamTurnLocal = { id: turnId, teamId: selectedTeamId, user: content, agents: [], final: '', done: false }
    setTeamTurns(prev => [...prev, newTurn])
    setTeamInput('')
    setTeamStreaming(true)

    const history: { role: string; content: string }[] = []
    for (const t of teamTurns) {
      if (t.done && t.final) {
        history.push({ role: 'user', content: t.user })
        history.push({ role: 'assistant', content: t.final })
      }
    }

    const onEvent = (ev: TeamStreamEvent) => {
      setTeamTurns(prev => prev.map(t => {
        if (t.id !== turnId) return t
        switch (ev.type) {
          case 'agent_start': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], status: 'streaming' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'streaming', text: '', toolCalls: [] })
            return { ...t, agents }
          }
          case 'token': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], text: agents[idx].text + ev.content, status: 'streaming' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'streaming', text: ev.content, toolCalls: [] })
            return { ...t, agents }
          }
          case 'agent_end': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], text: ev.content, status: 'done' }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: ev.content, toolCalls: [] })
            return { ...t, agents }
          }
          case 'tool_call': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) {
              agents[idx] = {
                ...agents[idx],
                status: 'streaming',
                toolCalls: [...agents[idx].toolCalls, { id: ev.tool_name + '_' + Date.now(), name: ev.tool_name, args: ev.tool_args, result: '', status: 'running' }],
              }
            }
            return { ...t, agents }
          }
          case 'tool_result': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) {
              const toolCalls = [...agents[idx].toolCalls]
              for (let i = toolCalls.length - 1; i >= 0; i--) {
                if (toolCalls[i].name === ev.tool_name && toolCalls[i].status === 'running') {
                  toolCalls[i] = { ...toolCalls[i], result: ev.tool_result, status: 'done' }
                  break
                }
              }
              agents[idx] = { ...agents[idx], toolCalls }
            }
            return { ...t, agents }
          }
          case 'supervisor_review': {
            const agents = [...t.agents]
            const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
            if (idx >= 0) agents[idx] = { ...agents[idx], supervisorNote: ev.content }
            else agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: '', toolCalls: [], supervisorNote: ev.content })
            return { ...t, agents }
          }
          case 'final':
            return { ...t, final: ev.content, done: ev.done }
          case 'error': {
            const agents = [...t.agents]
            if (ev.agent_id) {
              const idx = agents.findIndex(a => a.agent_id === ev.agent_id)
              if (idx >= 0) agents[idx] = { ...agents[idx], status: 'error', error: ev.error }
            }
            return { ...t, error: ev.agent_id ? t.error : ev.error }
          }
          default:
            return t
        }
      }))
    }

    await teamApi.streamTeamChat(selectedTeamId, content, history, onEvent)
    setTeamStreaming(false)
  }

  // 思考过程已默认常开, 开关按钮移除; 保留空实现兼容既有调用方
  const toggleThinking = () => {}

  // —— 执行者(替代原「模式」的用户视角) ——
  // 内部仍复用 mode/selectedAgent/selectedTeamId 三套状态(团队渲染分支/工具链依赖它们),
  // 但对外只暴露一个「执行者」概念, 三者由它统一驱动。
  const executor: Executor = mode === 'team'
    ? { type: 'team', id: selectedTeamId || undefined }
    : selectedAgent
      ? { type: 'agent', id: selectedAgent.id }
      : { type: 'default' }
  const setExecutor = (x: Executor) => {
    if (x.type === 'team') {
      setMode('team')
      if (x.id) setSelectedTeamId(x.id)
    } else {
      setMode('normal')
      setSelectedAgent(x.type === 'agent' ? (agents.find(a => a.id === x.id) || null) : null)
    }
  }

  return {
    // 普通/自主
    mode, setMode,
    // 执行者(用户唯一入口)
    executor, setExecutor,
    showThinking, toggleThinking,
    selectedModel, setSelectedModel,
    selectedAgent, setSelectedAgent,
    knowledgeCategories, setKnowledgeCategories,
    models, agents,
    messages, setMessages,
    input, setInput,
    streaming,
    send,
    regenerate,
    clearSession,
    stopGeneration,
    // 审批
    pending, approving, respond, dismissApproval,
    // 会话
    convId, setConvId,
    // 团队
    selectedTeamId, setSelectedTeamId,
    teamTurns, setTeamTurns,
    teamInput, setTeamInput,
    teamStreaming,
    handleTeamSend,
    teams: teamApi.teams,
    teamsLoading: teamApi.loading,
  }
}

// ---------------------------------------------------------------------------
// ChatMessageList —— 消息渲染
// ---------------------------------------------------------------------------

// 引用来源标题解析: id→title 模块级缓存(跨消息/跨渲染共享), 拉取失败回退显示 ID。
const citationTitleCache = new Map<string, string>()

function CitationList({ ids }: { ids: string[] }) {
  const [titles, setTitles] = useState<Record<string, string>>(() => {
    const init: Record<string, string> = {}
    for (const id of ids) {
      const t = citationTitleCache.get(id)
      if (t) init[id] = t
    }
    return init
  })
  // 拉取失败(404/已删除/无权限)的引用: 展示为不可点击的失效条目, 不再产出死链
  const [dead, setDead] = useState<Record<string, boolean>>({})
  const key = ids.join('|')
  useEffect(() => {
    let cancelled = false
    for (const id of key.split('|').filter(Boolean)) {
      if (citationTitleCache.has(id)) continue
      apiFetchRaw(`/knowledge/${encodeURIComponent(id)}`)
        .then(r => (r.ok ? r.json() : null))
        .then((a: any) => {
          const t = typeof a?.title === 'string' ? a.title : ''
          if (!t) {
            if (!cancelled) setDead(prev => ({ ...prev, [id]: true }))
            return
          }
          citationTitleCache.set(id, t)
          if (!cancelled) setTitles(prev => ({ ...prev, [id]: t }))
        })
        .catch(() => { if (!cancelled) setDead(prev => ({ ...prev, [id]: true })) })
    }
    return () => { cancelled = true }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return (
    <div className="mt-1 space-y-0.5">
      {ids.map((id, i) => {
        const title = titles[id]
        if (dead[id] && !title) {
          return (
            <div
              key={`${id}-${i}`}
              className="block text-[10px] text-muted-foreground/70 pl-2 border-l-2 border-gray-200 dark:border-white/10 truncate cursor-not-allowed"
              title="该文章不存在或已删除"
            >
              [{id}] 文章不存在或已删除
            </div>
          )
        }
        return (
          <a
            key={`${id}-${i}`}
            href={`/knowledge/${encodeURIComponent(id)}`}
            target="_blank"
            rel="noopener"
            className="block text-[10px] text-blue-500 hover:underline pl-2 border-l-2 border-blue-200 dark:border-blue-500/30 truncate"
            title={title || id}
          >
            {title ? <><span className="truncate">{title}</span> <span className="text-muted-foreground">[{id}]</span></> : `[${id}]`}
          </a>
        )
      })}
    </div>
  )
}

interface ChatMessageListProps {
  messages: ChatMessage[]
  /** 模式概念已移除; 保留仅为兼容旧调用。渲染按消息内容自适应(含 plan/step/report 标签走结构化渲染) */
  mode?: ChatMode
  welcomeId?: string
  variant?: 'page' | 'panel' | 'floating'
  enableCitations?: boolean
  enableFeedback?: boolean
  enableTaskList?: boolean
  onFeedback?: (msg: ChatMessage, rating: 'up' | 'down') => void
  onRetry?: (msg: ChatMessage) => void
  /** 传入则在最后一条正常完成的助手回复下显示「重新生成」(重发其前的用户消息) */
  onRegenerate?: () => void
  extraActions?: (msg: ChatMessage) => ReactNode
  loading?: boolean
  loadingText?: string
  emptyQuickActions?: ReactNode
  endRef?: RefObject<HTMLDivElement>
  className?: string
}

// ---------------------------------------------------------------------------
// useStickToBottom —— 主流对话产品的智能滚动(Claude/ChatGPT/DeepSeek 标配):
//   用户停在底部 → 流式输出自动跟随; 用户上滚翻历史 → 停止跟随 + 浮现「回到底部」。
//   替代旧版 messages 一变就强制 scrollIntoView(翻历史被硬拽回底的糟糕体验)。
// ---------------------------------------------------------------------------

export function useStickToBottom(deps: unknown[]) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const atBottomRef = useRef(true)
  const [atBottom, setAtBottom] = useState(true)

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    const near = el.scrollHeight - el.scrollTop - el.clientHeight < 60
    if (near !== atBottomRef.current) {
      atBottomRef.current = near
      setAtBottom(near)
    }
  }

  const scrollToBottom = (smooth = false) => {
    const el = scrollRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior: smooth ? 'smooth' : 'auto' })
    atBottomRef.current = true
    setAtBottom(true)
  }

  // deps 变化(流式 token/新消息)时仅在底部才跟随, 直接 scrollTop 赋值避免 smooth 动画抖动
  useEffect(() => {
    if (atBottomRef.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return { scrollRef, atBottom, onScroll, scrollToBottom }
}

// ----------------------- 文档产物: 长文档收敛为卡片(对标 DeepSeek 文档/Kimi 长文折叠) -----------------------

export interface DocArtifact {
  title: string
  body: string
  /** 正文首个标题前的引导语, 仍以聊天气泡样式渲染 */
  preamble: string
  words: number
}

/** 识别「文档型回复」: 足够长 + 有 Markdown 标题结构, 才折叠为文档卡片; 短回复/结构化卡片路径不受影响。 */
export function detectDocArtifact(content: string): DocArtifact | null {
  if (!content || content.length < 600) return null
  // 结构化标记(plan/step/report/工具调用)走 renderExploreContent 卡片渲染, 不抢
  if (/<plan>|<\/step>|<report>|🔧 \*\*调用工具/.test(content)) return null
  // 逐行扫描, 跳过 ```代码围栏 内的行(代码注释里的 # 不是 Markdown 标题)
  const lines = content.split('\n')
  let inCode = false
  let headingIdx = -1
  for (let i = 0; i < lines.length; i++) {
    const l = lines[i]
    if (l.trimStart().startsWith('```')) { inCode = !inCode; continue }
    if (!inCode && /^#{1,3} /.test(l)) { headingIdx = i; break }
  }
  if (headingIdx < 0) return null
  const preamble = lines.slice(0, headingIdx).join('\n').trim()
  const body = lines.slice(headingIdx).join('\n').trim()
  if (body.length < 500 || preamble.length > 500) return null
  const title = lines[headingIdx].replace(/^#+\s*/, '').trim().slice(0, 60) || 'AI 文档'
  const words = body.replace(/\s+/g, '').length
  return { title, body, preamble, words }
}

function useDocActions(doc: DocArtifact) {
  const [copied, setCopied] = useState(false)
  const [exporting, setExporting] = useState<'docx' | 'pptx' | null>(null)
  const copyDoc = async () => {
    try {
      await navigator.clipboard.writeText(doc.body)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch { /* 剪贴板不可用时静默 */ }
  }
  const downloadDoc = () => {
    const blob = new Blob([doc.body], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${doc.title.replace(/[\\/:*?"<>|#\n]/g, '_') || 'ai-doc'}.md`
    a.click()
    URL.revokeObjectURL(url)
  }
  // 纯前端导出 docx/pptx（浏览器内转换，loading 态 + 模块级防重）
  const exportAs = async (fmt: 'docx' | 'pptx') => {
    if (exporting) return
    setExporting(fmt)
    try {
      await exportDocFile(doc.body, doc.title || 'ai-doc', fmt, `doc:${doc.title || doc.body.slice(0, 24)}`)
    } finally {
      setExporting(null)
    }
  }
  return { copied, copyDoc, downloadDoc, exporting, exportAs }
}

/** 全屏文档预览(点击卡片打开): 标题栏 + 完整 Markdown 渲染, Esc/点击遮罩关闭。 */
function DocumentViewer({ doc, onClose }: { doc: DocArtifact; onClose: () => void }) {
  const { copied, copyDoc, downloadDoc, exporting, exportAs } = useDocActions(doc)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="fixed inset-0 z-[1000] flex items-center justify-center bg-black/40 p-4 md:p-8" onClick={onClose}>
      <div
        className="flex flex-col w-full max-w-4xl h-full max-h-[92vh] bg-white dark:bg-[#1c1c1e] rounded-xl border border-gray-200 dark:border-white/10 shadow-2xl overflow-hidden"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center gap-2.5 px-4 py-3 border-b border-gray-200 dark:border-white/10 shrink-0">
          <FileText className="h-[18px] w-[18px] text-blue-500 shrink-0" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium truncate">{doc.title}</p>
            <p className="text-[11px] text-muted-foreground">Markdown · 约 {doc.words} 字</p>
          </div>
          <button
            onClick={copyDoc}
            className={cn('p-1.5 rounded-md text-xs transition-colors flex items-center gap-1',
              copied ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:bg-gray-100 dark:hover:bg-white/10')}
            title={copied ? '已复制' : '复制全文'}
          >
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">{copied ? '已复制' : '复制'}</span>
          </button>
          <button
            onClick={() => exportAs('docx')}
            disabled={!!exporting}
            className="p-1.5 rounded-md text-xs text-muted-foreground hover:bg-gray-100 dark:hover:bg-white/10 transition-colors flex items-center gap-1 disabled:opacity-50"
            title="导出 Word (.docx)"
          >
            {exporting === 'docx' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <FileDown className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">docx</span>
          </button>
          <button
            onClick={() => exportAs('pptx')}
            disabled={!!exporting}
            className="p-1.5 rounded-md text-xs text-muted-foreground hover:bg-gray-100 dark:hover:bg-white/10 transition-colors flex items-center gap-1 disabled:opacity-50"
            title="导出 PowerPoint (.pptx)"
          >
            {exporting === 'pptx' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Presentation className="h-3.5 w-3.5" />}
            <span className="hidden sm:inline">pptx</span>
          </button>
          <button
            onClick={downloadDoc}
            className="p-1.5 rounded-md text-xs text-muted-foreground hover:bg-gray-100 dark:hover:bg-white/10 transition-colors flex items-center gap-1"
            title="下载 .md"
          >
            <Download className="h-3.5 w-3.5" />
            <span className="hidden sm:inline">下载</span>
          </button>
          <button
            onClick={onClose}
            className="p-1.5 rounded-md text-muted-foreground hover:bg-gray-100 dark:hover:bg-white/10 transition-colors"
            title="关闭 (Esc)"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="flex-1 overflow-y-auto px-5 py-4 md:px-8 md:py-6">
          <AiMarkdown content={doc.body} />
        </div>
      </div>
    </div>
  )
}

/** 聊天内的文档卡片: 标题 + 字数 + 两行预览, 操作: 打开预览 / 复制 / 下载 / 导出 docx·pptx。 */
function DocumentCard({ doc }: { doc: DocArtifact }) {
  const [open, setOpen] = useState(false)
  const { copied, copyDoc, downloadDoc, exporting, exportAs } = useDocActions(doc)
  const preview = doc.body
    .split('\n')
    .map(l => l.replace(/^#+\s*/, '').trim())
    .filter(Boolean)
    .slice(0, 2)
    .join(' · ')
  return (
    <>
      <div className="rounded-lg border border-gray-200 dark:border-white/10 bg-white dark:bg-white/[0.03] overflow-hidden max-w-[520px] shadow-sm">
        <button onClick={() => setOpen(true)} className="w-full text-left px-4 py-3 hover:bg-gray-50 dark:hover:bg-white/[0.04] transition-colors">
          <div className="flex items-center gap-2.5">
            <div className="h-9 w-9 rounded-lg bg-blue-50 dark:bg-blue-500/15 flex items-center justify-center shrink-0">
              <FileText className="h-4 w-4 text-blue-500" />
            </div>
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium truncate">{doc.title}</p>
              <p className="text-[11px] text-muted-foreground mt-0.5">Markdown 文档 · 约 {doc.words} 字</p>
            </div>
            <span className="text-xs text-blue-500 shrink-0 flex items-center gap-0.5">
              打开 <ChevronRight className="h-3 w-3" />
            </span>
          </div>
          {preview && <p className="mt-2 text-xs text-muted-foreground line-clamp-2 leading-relaxed">{preview}</p>}
        </button>
        <div className="flex items-center gap-1 px-3 py-1.5 border-t border-gray-100 dark:border-white/[0.06] bg-gray-50/60 dark:bg-white/[0.02]">
          <button
            onClick={copyDoc}
            className={cn('px-2 py-1 rounded text-[11px] transition-colors flex items-center gap-1',
              copied ? 'text-blue-500' : 'text-muted-foreground hover:text-blue-500')}
          >
            {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
            {copied ? '已复制' : '复制全文'}
          </button>
          <button
            onClick={downloadDoc}
            className="px-2 py-1 rounded text-[11px] text-muted-foreground hover:text-blue-500 transition-colors flex items-center gap-1"
          >
            <Download className="h-3 w-3" /> 下载 .md
          </button>
          <button
            onClick={() => exportAs('docx')}
            disabled={!!exporting}
            className="px-2 py-1 rounded text-[11px] text-muted-foreground hover:text-blue-500 transition-colors flex items-center gap-1 disabled:opacity-50"
            title="导出 Word (.docx)"
          >
            {exporting === 'docx' ? <Loader2 className="h-3 w-3 animate-spin" /> : <FileDown className="h-3 w-3" />} docx
          </button>
          <button
            onClick={() => exportAs('pptx')}
            disabled={!!exporting}
            className="px-2 py-1 rounded text-[11px] text-muted-foreground hover:text-blue-500 transition-colors flex items-center gap-1 disabled:opacity-50"
            title="导出 PowerPoint (.pptx)"
          >
            {exporting === 'pptx' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Presentation className="h-3 w-3" />} pptx
          </button>
        </div>
      </div>
      {open && <DocumentViewer doc={doc} onClose={() => setOpen(false)} />}
    </>
  )
}

/** 消息统计行(对标 DeepSeek 每轮统计条): 用时 / 首 token / 输出 tok 与字数 / 速度, 仅完成的助手消息展示。 */
function MsgStats({ msg }: { msg: ChatMessage }) {
  if (msg.status !== 'done' || typeof msg.durationMs !== 'number' || msg.durationMs <= 0 || !msg.content.trim()) return null
  const secs = msg.durationMs / 1000
  const raw = msg.content.length
  const chars = msg.content.replace(/\s/g, '').length
  const tokens = estimateContextTokens([{ content: msg.content }])
  const cps = secs > 0.2 ? Math.round(raw / secs) : 0
  const first = typeof msg.thinkMs === 'number' && msg.thinkMs > 0 ? ` · 首 token ${(msg.thinkMs / 1000).toFixed(1)} 秒` : ''
  return (
    <div
      className="mt-1 text-[10px] text-muted-foreground/60 select-none"
      title={`总用时 ${secs.toFixed(1)} 秒${first.replace(' · ', ' · ')} · 输出 ${chars} 字 ≈ ${formatTokenCount(tokens)} tok${cps ? ` · 约 ${cps} 字/秒` : ''}`}
    >
      ⚡ 用时 {secs.toFixed(1)} 秒{first} · 输出 {formatTokenCount(tokens)} tok（{chars} 字）{cps ? ` · ~${cps} 字/秒` : ''}
    </div>
  )
}

export function ChatMessageList({
  messages, mode = 'normal', welcomeId = 'welcome', variant = 'page',
  enableCitations = false, enableFeedback = false, enableTaskList = false,
  onFeedback, onRetry, onRegenerate, extraActions, loading, loadingText, emptyQuickActions, endRef, className,
}: ChatMessageListProps) {
  const isPanel = variant === 'panel'
  const isFloating = variant === 'floating'
  // 单条复制反馈: 短暂把按钮文案变「已复制」
  const [copiedId, setCopiedId] = useState<string | null>(null)
  // 智能滚动接管: 流式跟随 + 回到底部按钮(此前由调用方 messages 一变就强滚, 翻历史体验差)
  const { scrollRef, atBottom, onScroll, scrollToBottom } = useStickToBottom([messages, loading])
  const copyMessage = async (msg: ChatMessage) => {
    try {
      await navigator.clipboard.writeText(msg.content)
      setCopiedId(msg.id)
      setTimeout(() => setCopiedId(prev => (prev === msg.id ? null : prev)), 1500)
    } catch { /* 剪贴板不可用(非安全上下文等)时静默 */ }
  }
  // 最后一条真实助手回复的索引: 「重新生成」只挂在这条下面
  let lastAiIdx = -1
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === 'assistant' && messages[i].id !== welcomeId) { lastAiIdx = i; break }
  }

  return (
    <div className={cn('relative flex-1 min-h-0 flex flex-col')}>
      <div ref={scrollRef} onScroll={onScroll} className={cn('flex-1 overflow-y-auto space-y-3 min-h-0', className)}>
      {messages.map((msg, mi) => {
        // 欢迎语: 居中无气泡大样式(空态观感对齐 Claude/ChatGPT 首屏)
        if (msg.id === welcomeId) {
          return (
            <div key={msg.id} className="flex flex-col items-center justify-center py-8 text-center">
              <div className={cn('rounded-2xl bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center mb-3', isFloating ? 'h-10 w-10' : 'h-12 w-12')}>
                <Sparkles className={cn('text-blue-500', isFloating ? 'h-5 w-5' : 'h-6 w-6')} />
              </div>
              <div
                className={cn('prose-sm max-w-none text-muted-foreground [&_p]:my-0.5', isFloating ? 'text-xs' : 'text-sm')}
                dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }}
              />
            </div>
          )
        }
        // 流式等待中隐藏空占位助手消息: send() 会先追加 content 为空的占位气泡,
        // 与下方「思考中」指示重复(两行); 等首字/思考/工具调用出现后再渲染。
        if (
          loading && msg.role === 'assistant' && msg.id !== welcomeId &&
          !msg.content && !msg.thinking && !(msg.toolCalls && msg.toolCalls.length > 0) &&
          msg.status !== 'error'
        ) {
          return null
        }
        return (
        <div key={msg.id} className={cn('group/msg flex gap-3', msg.role === 'user' ? 'justify-end' : 'justify-start')}>
          {msg.role === 'assistant' && (
            <div className={cn(
              'h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0',
              isFloating && 'h-7 w-7',
            )}>
              <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
            </div>
          )}
          <div className={cn(
            'rounded-lg px-4 py-2 text-sm',
            msg.role === 'user'
              ? isPanel || isFloating
                ? 'bg-blue-600 text-white rounded-br-sm whitespace-pre-wrap'
                : 'bg-blue-600 text-white whitespace-pre-wrap'
              : isPanel
                ? 'bg-white dark:bg-white/[0.04] text-gray-800 dark:text-gray-100 rounded-bl-sm border border-gray-200/60 dark:border-white/10'
                : isFloating
                  ? 'bg-gray-100 dark:bg-gray-800/80 text-gray-800 dark:text-gray-100 rounded-bl-sm border border-gray-200/60 dark:border-gray-700/50'
                  : 'bg-gray-100 dark:bg-gray-800 prose-sm max-w-none',
            isPanel ? 'max-w-[80%] rounded-2xl py-3' : isFloating ? 'max-w-[85%] rounded-2xl py-2.5 text-[13px] leading-relaxed' : 'max-w-[88%]',
          )}>
            {msg.role === 'assistant' ? (
              <>
                {/* 任务列表: 工具调用并发可视化(默认折叠) */}
                {enableTaskList && msg.toolCalls && msg.toolCalls.length > 0 && (
                  <details className="mb-2 group/task">
                    <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground flex items-center gap-1">
                      <Terminal className="h-3 w-3" />任务列表 ({msg.toolCalls.length})
                      {msg.toolCalls.some(t => t.status === 'running') && <Loader2 className="h-3 w-3 animate-spin text-blue-500" />}
                    </summary>
                    <div className="mt-1 space-y-1">
                      {msg.toolCalls.map(tc => (
                        <div key={tc.id} className={cn(
                          'flex items-center gap-2 text-xs rounded border px-2 py-1',
                          tc.status === 'running'
                            ? 'border-amber-300 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10'
                            : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)]',
                        )}>
                          {tc.status === 'running'
                            ? <Loader2 className="h-3 w-3 animate-spin text-amber-500 shrink-0" />
                            : <CheckCircle className="h-3 w-3 text-blue-500 shrink-0" />}
                          <code className="font-mono font-semibold">{tc.name}</code>
                          <span className="text-[10px] text-muted-foreground truncate flex-1">{tc.status === 'done' ? (tc.result || '已完成') : '执行中…'}</span>
                        </div>
                      ))}
                    </div>
                  </details>
                )}

                {/* 思考过程(主流交互): 流式思考中展开实时展示, 结束后折叠为一行「已深度思考」 */}
                {msg.thinking && (
                  <ThinkingBlock thinking={msg.thinking} live={msg.status === 'streaming' && !msg.content} thinkMs={msg.thinkMs} />
                )}

                {/* 内容自适应渲染: 含结构化标记(plan/step/report/工具调用)走卡片式渲染, 否则标准 Markdown */}
                {/<plan>|<\/step>|<report>|🔧 \*\*调用工具/.test(msg.content)
                  ? <div className={msg.status === 'streaming' ? 'ai-streaming' : undefined} dangerouslySetInnerHTML={{ __html: renderExploreContent(msg.content) }} />
                  : (() => {
                      // 文档型回复: 流式期间仍平铺渲染(可跟读), 完成后收敛为文档卡片(标题/字数/预览/打开/复制/下载)
                      const doc = msg.status !== 'streaming' ? detectDocArtifact(msg.content) : null
                      if (doc) {
                        return (
                          <div className="space-y-2.5">
                            {doc.preamble && <AiMarkdown content={doc.preamble} />}
                            <DocumentCard doc={doc} />
                          </div>
                        )
                      }
                      return <div className={msg.status === 'streaming' ? 'ai-streaming' : undefined}><AiMarkdown content={msg.content} /></div>
                    })()}

                {/* RAG 引用来源: 拉取文章标题展示, 仍可点击跳转 */}
                {enableCitations && msg.citations && msg.citations.length > 0 && (
                  <details className="mt-2">
                    <summary className="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">📚 引用来源 ({msg.citations.length})</summary>
                    <CitationList ids={msg.citations.map(c => c.replace(/^\[|\]$/g, ''))} />
                  </details>
                )}

                {/* 失败态: 错误提示 + 重试 */}
                {msg.status === 'error' && (
                  <div className="mt-2 rounded-md border border-red-300 dark:border-red-500/40 bg-red-50 dark:bg-red-500/10 p-2">
                    <p className="text-[11px] text-red-600 dark:text-red-400 flex items-center gap-1">
                      <AlertTriangle className="h-3 w-3 shrink-0" />
                      {msg.errorText || 'AI 响应失败'}
                    </p>
                    {onRetry && (
                      <button onClick={() => onRetry(msg)} className="mt-1.5 text-[11px] text-red-600 dark:text-red-400 hover:underline font-medium">
                        ↻ 重试
                      </button>
                    )}
                  </div>
                )}

                {/* 输出被 max_tokens 上限截断: 黄色提示 + 重试(不与 error 状态冲突, 此时 status 是 done) */}
                {msg.status !== 'error' && msg.truncated && (
                  <div className="mt-2 rounded-md border border-amber-300 dark:border-amber-500/40 bg-amber-50 dark:bg-amber-500/10 p-2">
                    <p className="text-[11px] text-amber-700 dark:text-amber-300 flex items-center gap-1">
                      <AlertTriangle className="h-3 w-3 shrink-0" />
                      {msg.truncated}
                    </p>
                    {onRetry && (
                      <button onClick={() => onRetry(msg)} className="mt-1.5 text-[11px] text-amber-700 dark:text-amber-300 hover:underline font-medium">
                        ↻ 重试
                      </button>
                    )}
                  </div>
                )}

                {/* 单条消息操作: 复制 / 重新生成 / 反馈 / 额外 */}
                {(() => {
                  const canCopy = msg.role === 'assistant' && msg.id !== welcomeId && !!msg.content.trim()
                  const canRegen = !!onRegenerate && mi === lastAiIdx && !loading && msg.status !== 'error' && !!msg.content.trim()
                  if (!(canCopy || canRegen || (enableFeedback && onFeedback && msg.id !== welcomeId) || (extraActions && msg.id !== welcomeId))) return null
                  return (
                    <div className={cn(
                      'flex items-center gap-1 mt-2 pt-1.5 border-t border-gray-200 dark:border-[rgba(255,255,255,0.04)] transition-opacity',
                      // 主流交互: hover 消息才浮现操作栏, 降低视觉噪音; 复制成功反馈时保持可见
                      copiedId === msg.id ? 'opacity-100' : 'opacity-0 group-hover/msg:opacity-100 focus-within:opacity-100',
                    )}>
                      {canCopy && (
                        <button
                          onClick={() => copyMessage(msg)}
                          className={cn('p-1 rounded text-[10px] transition-colors', copiedId === msg.id ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:text-blue-500')}
                          title={copiedId === msg.id ? '已复制' : '复制内容'}
                        >
                          {copiedId === msg.id ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
                        </button>
                      )}
                      {canRegen && (
                        <button
                          onClick={onRegenerate}
                          className="p-1 rounded text-[10px] text-muted-foreground hover:text-blue-500 transition-colors"
                          title="重新生成"
                        >
                          <RefreshCw className="h-3 w-3" />
                        </button>
                      )}
                      {enableFeedback && onFeedback && msg.id !== welcomeId && (
                      <>
                        <button
                          onClick={() => { if (msg.feedback_rating !== 'up') onFeedback(msg, 'up') }}
                          disabled={!!msg.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', msg.feedback_rating === 'up' ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:text-blue-500')}
                          title="有帮助"
                        >
                          <ThumbsUp className="h-3 w-3" />
                        </button>
                        <button
                          onClick={() => { if (msg.feedback_rating !== 'down') onFeedback(msg, 'down') }}
                          disabled={!!msg.feedback_rating}
                          className={cn('p-1 rounded text-[10px] transition-colors', msg.feedback_rating === 'down' ? 'text-red-500 bg-red-50 dark:bg-red-500/10' : 'text-muted-foreground hover:text-red-500')}
                          title="没帮助"
                        >
                          <ThumbsDown className="h-3 w-3" />
                        </button>
                        <span className="w-px h-3 bg-gray-200 dark:bg-gray-700 mx-0.5" />
                      </>
                      )}
                      {extraActions && msg.id !== welcomeId && extraActions(msg)}
                    </div>
                  )
                })()}

                {/* 消息统计行: 用时/首 token/输出用量(对标 DeepSeek) */}
                <MsgStats msg={msg} />
              </>
            ) : (
              <>
                {msg.attachments && msg.attachments.length > 0 && (
                  <div className="flex flex-wrap gap-1.5 mb-1.5">
                    {msg.attachments.map((a, i) => (
                      a.type.startsWith('image/') && a.id
                        ? <AttachmentThumb key={`${a.name}-${i}`} att={a} />
                        : <AttachmentChip key={`${a.name}-${i}`} att={a} />
                    ))}
                  </div>
                )}
                {/* 用户消息也走 Markdown 渲染(与主流对话产品一致); 蓝底气泡下强制白字+白链接, 转义防 XSS */}
                <div
                  className="text-white leading-relaxed [a]:text-white [a]:underline"
                  dangerouslySetInnerHTML={{ __html: renderMarkdown(msg.content) }}
                />
              </>
            )}
          </div>
          {/* 用户消息悬停复制(气泡左侧浮现) */}
          {msg.role === 'user' && msg.content.trim() && (
            <button
              onClick={() => copyMessage(msg)}
              className="opacity-0 group-hover/msg:opacity-100 self-start mt-1.5 p-1 rounded text-muted-foreground hover:text-blue-500 transition-opacity"
              title={copiedId === msg.id ? '已复制' : '复制内容'}
            >
              {copiedId === msg.id ? <Check className="h-3.5 w-3.5 text-blue-500" /> : <Copy className="h-3.5 w-3.5" />}
            </button>
          )}
          {msg.role === 'user' && (
            <div className={cn('h-8 w-8 rounded-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0', isFloating && 'h-7 w-7')}>
              <User className="h-4 w-4 text-gray-600 dark:text-gray-300" />
            </div>
          )}
        </div>
        )
      })}

      {loading && (
        <div className="flex gap-3 justify-start">
          {!isFloating && (
            <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
              <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
            </div>
          )}
          <div className={cn('rounded-lg px-4 py-2 text-sm flex items-center gap-2', isPanel || isFloating ? 'bg-gray-100 dark:bg-gray-800/80' : 'bg-gray-100 dark:bg-gray-800')}>
            <Loader2 className="h-4 w-4 animate-spin" />
            <span>{loadingText || '思考中...'}</span>
          </div>
        </div>
      )}

      {/* 空态快捷操作 */}
      {emptyQuickActions && messages.length === 1 && (
        <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mt-2 px-6 pb-4">{emptyQuickActions}</div>
      )}

      <div ref={endRef} />
      </div>

      {/* 回到底部: 用户上滚翻历史时浮现(主流对话产品标配) */}
      {!atBottom && (
        <button
          onClick={() => scrollToBottom(true)}
          className="absolute bottom-3 left-1/2 -translate-x-1/2 z-10 h-8 px-2.5 rounded-full bg-background/95 border border-gray-200 dark:border-gray-700 shadow-md flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground hover:border-blue-300 dark:hover:border-blue-500/50 transition-colors"
          title="回到底部"
        >
          <ArrowDown className="h-3.5 w-3.5" />
          回到底部
        </button>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// ChatInputBar —— 输入区
// ---------------------------------------------------------------------------

interface QuickAction { label: string; prompt: string }

interface ChatInputBarProps {
  value: string
  onChange: (v: string) => void
  onSend: () => void
  onStop?: () => void
  streaming: boolean
  disabled?: boolean
  placeholder?: string
  /** 模式概念已移除, 仅作内部兼容(团队分支等); 留空=normal */
  mode?: ChatMode
  models?: ModelInfo[]
  selectedModel?: string
  onModelChange?: (v: string) => void
  agents?: any[]
  selectedAgent?: any
  onAgentChange?: (a: any) => void
  enableModel?: boolean
  enableAgent?: boolean
  enableKnowledgeScope?: boolean
  knowledgeCategories?: string[]
  onKnowledgeCategoriesChange?: (cats: string[]) => void
  knowledgeCategoryOptions?: string[]
  enableQuickActions?: boolean
  quickActions?: QuickAction[]
  /** 附件: 传入则显示上传按钮并支持粘贴图片/文件(上传逻辑由调用方负责) */
  onFiles?: (files: File[]) => void
  variant?: 'page' | 'panel' | 'floating'
  inputFooter?: ReactNode
  className?: string
}

export function ChatInputBar(props: ChatInputBarProps) {
  const {
    value, onChange, onSend, onStop, streaming, disabled, placeholder,
    mode = 'normal',
    models = [], selectedModel, onModelChange,
    agents = [], selectedAgent, onAgentChange,
    enableModel = false, enableAgent = false,
    enableKnowledgeScope = false, knowledgeCategories = [], knowledgeCategoryOptions = [], onKnowledgeCategoriesChange,
    enableQuickActions = false, quickActions = [],
    onFiles,
    variant = 'page', inputFooter, className,
  } = props

  const isDark = variant === 'panel'
  const fileInputRef = useRef<HTMLInputElement>(null)
  const taRef = useRef<HTMLTextAreaElement>(null)
  // 自动增高(主流标配): 随内容长高, 超上限后内部滚动
  const maxH = variant === 'page' ? 200 : 128
  useEffect(() => {
    const el = taRef.current
    if (!el) return
    el.style.height = 'auto'
    const h = Math.min(el.scrollHeight, maxH)
    el.style.height = `${h}px`
    el.style.overflowY = el.scrollHeight > maxH ? 'auto' : 'hidden'
  }, [value, maxH])
  const send = () => { if (value.trim()) onSend() }
  const toggleCategory = (c: string) => {
    const next = knowledgeCategories.includes(c)
      ? knowledgeCategories.filter(x => x !== c)
      : [...knowledgeCategories, c]
    onKnowledgeCategoriesChange?.(next)
  }

  return (
    <div className={cn('border-t p-4 space-y-2 shrink-0', isDark && 'border-[rgba(255,255,255,0.04)]', variant === 'floating' && 'px-3 py-2.5', className)}>
      {/* 模式概念已移除(执行者选择在顶部状态卡); 浮窗等独立入口一律默认助手 */}
      {/* 工具栏：模型 + Agent(思考过程开关已移除, 默认常开) */}
      {(enableModel || enableAgent) && (
        <div className="flex flex-wrap items-center gap-2">
          {/* 即使 models=0 也要显示下拉, 让用户能选 auto(默认) */}
          {enableModel && (
            <select
              value={selectedModel || ''}
              onChange={e => onModelChange?.(e.target.value)}
              className={cn(
                'h-7 rounded-md border px-1.5 text-xs bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50',
                isDark ? 'border-white/10 text-gray-200' : 'border-gray-200 text-gray-700',
              )}
              title="运行级模型(空=按权重/健康度路由)"
            >
              <option value="">⚡ 自动（按权重/健康/类型匹配）</option>
              {models.map(m => (
                <option
                  key={`${m.name}-${m.model}`}
                  value={m.model}
                  title={m.name !== 'default' && m.name !== m.model ? `${m.model} · ${m.base_url || m.name}` : undefined}
                >
                  {m.healthy === false ? '⚠️ ' : ''}
                  {m.name !== 'default' && m.name !== m.model ? m.name : m.model}
                  {m.healthy === false ? '（降级）' : ''}
                </option>
              ))}
            </select>
          )}
          {enableAgent && agents.length > 0 && (
            <select
              value={selectedAgent?.id || ''}
              onChange={e => onAgentChange?.(agents.find((a: any) => a.id === e.target.value) || null)}
              className={cn(
                'h-7 max-w-[180px] rounded-md border px-1.5 text-xs bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50',
                isDark ? 'border-white/10 text-gray-200' : 'border-gray-200 text-gray-700',
              )}
              title="选择专属 Agent"
            >
              <option value="">默认助手（无 Agent）</option>
              {agents.map((a: any) => <option key={a.id} value={a.id}>{a.name}</option>)}
            </select>
          )}
        </div>
      )}

      {/* 知识范围选择: 默认全部; 选中若干分类则限定本次对话检索范围 */}
      {enableKnowledgeScope && knowledgeCategoryOptions.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-muted-foreground shrink-0">知识范围</span>
          <button
            onClick={() => onKnowledgeCategoriesChange?.([])}
            className={cn(
              'px-2 py-0.5 rounded-full text-[11px] border transition-colors',
              knowledgeCategories.length === 0
                ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/15 dark:border-blue-500/40 dark:text-blue-300'
                : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
            )}
            title="检索全部知识库"
          >
            全部
          </button>
          {knowledgeCategoryOptions.map(c => {
            const active = knowledgeCategories.includes(c)
            return (
              <button
                key={c}
                onClick={() => toggleCategory(c)}
                className={cn(
                  'px-2 py-0.5 rounded-full text-[11px] border transition-colors',
                  active
                    ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/15 dark:border-blue-500/40 dark:text-blue-300'
                    : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                )}
                title={`仅检索「${c}」分类`}
              >
                {c}
              </button>
            )
          })}
        </div>
      )}

      <div className="flex gap-2 items-end">
        {onFiles && (
          <>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              className="hidden"
              onChange={e => {
                const files = Array.from(e.target.files || [])
                if (files.length) onFiles(files)
                e.target.value = '' // 允许重复选择同一文件
              }}
            />
            <button
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled || streaming}
              className={cn(
                'flex items-center justify-center rounded-md border text-muted-foreground hover:text-blue-500 hover:border-blue-300 disabled:opacity-40 transition-colors shrink-0',
                variant === 'page' ? 'h-9 w-9' : 'h-11 w-11 rounded-xl',
                isDark && 'border-white/10',
              )}
              title="上传图片/文件（也可直接粘贴）"
              aria-label="上传附件"
            >
              <Paperclip className="h-4 w-4" />
            </button>
          </>
        )}
        <textarea
          ref={taRef}
          value={value}
          onChange={e => onChange(e.target.value)}
          onKeyDown={e => {
            // 输入法组字态(拼音候选确认)的回车不发送, 仅真实回车才触发
            if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send() }
          }}
          onPaste={e => {
            // 粘贴图片/文件: 剪贴板里有文件时拦截默认粘贴, 交由调用方上传
            const files = Array.from(e.clipboardData?.files || [])
            if (files.length && onFiles) { e.preventDefault(); onFiles(files) }
          }}
          placeholder={placeholder || (mode === 'explore' ? '给出目标，AI 自主规划执行…' : disabled ? '当前不可发送' : '输入你的问题...（Enter 发送，Shift+Enter 换行）')}
          disabled={disabled}
          rows={variant === 'page' ? 2 : 1}
          className={cn(
            'flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm resize-none placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:opacity-60',
            isDark && 'border-white/10 bg-white/[0.03] text-white focus-visible:ring-blue-500/50',
            variant === 'floating' && 'rounded-xl min-h-[38px] max-h-32',
          )}
        />
        <button
          onClick={streaming && onStop ? onStop : send}
          disabled={(!value.trim() && !streaming) || disabled}
          className={cn(
            'flex items-center justify-center rounded-md bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0',
            variant === 'page' ? 'h-9 w-9' : 'h-11 w-11 rounded-xl',
          )}
          title={streaming ? '停止生成' : '发送'}
          aria-label={streaming ? '停止生成' : '发送'}
        >
          {streaming ? <Square className="h-4 w-4" /> : <Send className="h-4 w-4" />}
        </button>
      </div>

      {/* 快捷操作: 输入框下方, 点击仅填充输入框不直接发送 */}
      {enableQuickActions && !streaming && quickActions.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {quickActions.map(q => (
            <button
              key={q.label}
              onClick={() => onChange(q.prompt)}
              className={cn(
                'px-2.5 py-1 rounded-full text-[11px] border transition-colors',
                isDark ? 'border-white/10 text-gray-300 hover:border-blue-500/40 hover:text-blue-400' : 'border-gray-200 text-gray-600 hover:border-blue-300 hover:text-blue-600',
              )}
              title="点击填入输入框, 可修改后再发送"
            >
              {q.label}
            </button>
          ))}
        </div>
      )}


      {inputFooter}
    </div>
  )
}

// ---------------------------------------------------------------------------
// TeamChatView —— 团队模式
// ---------------------------------------------------------------------------

interface TeamChatViewProps {
  teams: AgentTeam[]
  teamsLoading: boolean
  selectedTeamId: string | null
  onSelectTeam: (id: string) => void
  teamTurns: TeamTurnLocal[]
  teamInput: string
  onTeamInput: (v: string) => void
  teamStreaming: boolean
  onSend: (text: string) => void
  onManageTeams: () => void
  variant?: 'page' | 'panel'
  endRef?: RefObject<HTMLDivElement>
}

export function TeamChatView({
  teams, teamsLoading, selectedTeamId, onSelectTeam, teamTurns, teamInput, onTeamInput,
  teamStreaming, onSend, onManageTeams, variant = 'page', endRef,
}: TeamChatViewProps) {
  const isDark = variant === 'panel'
  // 团队流式同样走智能滚动(跟随输出/翻历史不拽回)
  const teamScroll = useStickToBottom([teamTurns, teamStreaming])
  return (
    <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-h-0">
      {/* 团队分段卡片选择 */}
      <div className={cn('border-b p-3', isDark ? 'border-white/10' : '')}>
        <div className="flex items-center gap-2 mb-2">
          <Users className="h-4 w-4 text-blue-400" />
          <span className="text-sm font-medium">选择团队</span>
          {teamsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
        </div>
        {teams.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            暂无团队，<button className="text-blue-500 underline" onClick={onManageTeams}>前往创建</button>
          </p>
        ) : (
          <div className="flex gap-3 overflow-x-auto pb-1">
            {teams.map(team => (
              <button
                key={team.id}
                onClick={() => onSelectTeam(team.id)}
                className={cn(
                  'shrink-0 w-56 text-left rounded-xl border p-3 transition-colors',
                  selectedTeamId === team.id
                    ? isDark ? 'border-blue-400 bg-blue-500/10' : 'border-blue-400 bg-blue-50/60'
                    : isDark ? 'border-white/10 hover:border-white/20' : 'border-gray-200 hover:border-gray-300',
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-sm font-medium truncate">{team.name}</span>
                  <span className={cn('text-[10px] px-1.5 py-0.5 rounded shrink-0', isDark ? 'bg-white/10 text-gray-300' : 'bg-gray-100 text-gray-500')}>
                    {TEAM_MODE_LABEL[team.mode] || team.mode}
                  </span>
                </div>
                <p className="text-xs text-muted-foreground mt-1 line-clamp-2 h-8 overflow-hidden">{team.description || '无描述'}</p>
                <div className="flex items-center gap-1.5 mt-2 text-[11px] text-muted-foreground">
                  <Users className="h-3 w-3" /> {team.members.length} 名成员
                </div>
              </button>
            ))}
          </div>
        )}
      </div>

      {/* 对话流 */}
      <div className="relative flex-1 flex flex-col min-h-0">
        <div ref={teamScroll.scrollRef} onScroll={teamScroll.onScroll} className="flex-1 overflow-y-auto p-4 space-y-4">
        {teamTurns.length === 0 ? (
          <div className="h-full flex items-center justify-center">
            <p className="text-sm text-muted-foreground text-center max-w-xs">
              {selectedTeamId ? '向团队发送第一条消息，多智能体将协同作答（流式实时呈现）。' : '请先在上方选择一个团队。'}
            </p>
          </div>
        ) : (
          teamTurns.map(turn => (
            <div key={turn.id} className="space-y-3">
              <div className="flex justify-end">
                <div className="max-w-[70%] rounded-lg px-4 py-2 text-sm bg-blue-600 text-white whitespace-pre-wrap rounded-br-sm">{turn.user}</div>
              </div>

              {turn.agents.map(a => (
                <div
                  key={`${turn.id}-${a.agent_id}`}
                  className={cn(
                    'rounded-2xl px-4 py-3 text-sm border',
                    isDark ? 'border-white/10 bg-white/[0.04] text-gray-100' : 'border-gray-200/60 bg-white text-gray-800',
                  )}
                >
                  <div className="flex items-center gap-2 mb-1.5">
                    <span className={cn(
                      'text-[10px] px-1.5 py-0.5 rounded shrink-0',
                      a.status === 'error' ? 'bg-red-500/15 text-red-500'
                        : a.status === 'done' ? 'bg-blue-500/15 text-blue-500'
                        : 'bg-blue-500/15 text-blue-500',
                    )}>
                      {TEAM_ROLE_LABEL[a.role] || a.role} · {a.agent_name}
                    </span>
                    {a.status === 'streaming' && <Loader2 className="h-3 w-3 animate-spin text-blue-400" />}
                    {a.status === 'done' && <span className="text-[10px] text-blue-500">✓ 完成</span>}
                  </div>

                  {a.toolCalls.length > 0 && (
                    <details className="mb-1.5" open>
                      <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">🔧 工具调用 ({a.toolCalls.length})</summary>
                      <div className="mt-1 space-y-1">
                        {a.toolCalls.map(tc => (
                          <div key={tc.id} className={cn(
                            'rounded border px-2 py-1 text-xs',
                            tc.status === 'running'
                              ? 'border-amber-300 bg-amber-50 dark:border-amber-500/30 dark:bg-amber-500/10'
                              : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)]',
                          )}>
                            <div className="flex items-center justify-between">
                              <span className="font-mono text-[11px] font-semibold">{tc.name}</span>
                              {tc.status === 'running' && <Loader2 className="h-3 w-3 animate-spin text-amber-500" />}
                              {tc.status === 'done' && <span className="text-[10px] text-blue-500">✓</span>}
                            </div>
                            {tc.args && (
                              <details className="mt-0.5">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer">参数</summary>
                                <pre className="mt-0.5 p-1 rounded bg-gray-100 dark:bg-gray-800 text-[10px] overflow-x-auto whitespace-pre-wrap max-h-20 overflow-y-auto">{tc.args}</pre>
                              </details>
                            )}
                            {tc.status === 'done' && tc.result && (
                              <details className="mt-0.5">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer">结果</summary>
                                <pre className="mt-0.5 p-1 rounded bg-gray-100 dark:bg-gray-800 text-[10px] overflow-x-auto whitespace-pre-wrap max-h-24 overflow-y-auto">{tc.result}</pre>
                              </details>
                            )}
                          </div>
                        ))}
                      </div>
                    </details>
                  )}

                  {a.supervisorNote && (
                    <details className="mb-1.5" open>
                      <summary className="text-xs text-purple-600 dark:text-purple-400 cursor-pointer hover:underline">📋 主管审核</summary>
                      <div className="mt-1 p-2 rounded border border-purple-200 dark:border-purple-500/30 bg-purple-50 dark:bg-purple-500/10 text-xs whitespace-pre-wrap">{a.supervisorNote}</div>
                    </details>
                  )}

                  {a.status === 'error' ? (
                    <p className="text-xs text-red-500 whitespace-pre-wrap">{a.error}</p>
                  ) : (
                    <div className="text-xs text-gray-700 dark:text-[var(--foreground)] whitespace-pre-wrap" dangerouslySetInnerHTML={{ __html: renderMarkdown(a.text) }} />
                  )}
                </div>
              ))}

              {turn.final && (
                <div className="flex gap-3 justify-start">
                  <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                    <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                  </div>
                  <div className={cn(
                    'max-w-[80%] rounded-lg px-4 py-2 text-sm border',
                    isDark ? 'border-white/10 bg-white/[0.04] text-gray-100' : 'border-gray-200/60 bg-white text-gray-800',
                  )}>
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-blue-500/15 text-blue-500 mb-1.5 inline-block">团队最终答复</span>
                    {(() => {
                      // 团队最终答复同样收敛长文档为卡片(团队产出多为报告类长文)
                      const doc = detectDocArtifact(turn.final)
                      if (!doc) return <div dangerouslySetInnerHTML={{ __html: renderMarkdown(turn.final) }} />
                      return (
                        <div className="space-y-2.5">
                          {doc.preamble && <div dangerouslySetInnerHTML={{ __html: renderMarkdown(doc.preamble) }} />}
                          <DocumentCard doc={doc} />
                        </div>
                      )
                    })()}
                  </div>
                </div>
              )}

              {turn.error && !turn.agents.some(a => a.status === 'error') && (
                <p className="text-xs text-red-500">⚠ {turn.error}</p>
              )}
            </div>
          ))
        )}
        <div ref={endRef} />
        </div>
        {!teamScroll.atBottom && (
          <button
            onClick={() => teamScroll.scrollToBottom(true)}
            className="absolute bottom-3 left-1/2 -translate-x-1/2 z-10 h-8 px-2.5 rounded-full bg-background/95 border border-gray-200 dark:border-gray-700 shadow-md flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground hover:border-blue-300 dark:hover:border-blue-500/50 transition-colors"
            title="回到底部"
          >
            <ArrowDown className="h-3.5 w-3.5" />
            回到底部
          </button>
        )}
      </div>

      {/* 输入区 */}
      <div className={cn('border-t p-4', isDark && 'border-white/10')}>
        <div className="flex gap-2">
          <textarea
            value={teamInput}
            onChange={e => onTeamInput(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && (e.preventDefault(), onSend(teamInput))}
            placeholder={selectedTeamId ? '向团队发送消息...' : '请先选择上方团队'}
            className={cn(
              'flex-1 resize-none rounded-xl border px-3 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500/50 min-h-[44px] max-h-32',
              isDark ? 'border-white/10 bg-white/[0.03] text-white' : 'border-gray-200 bg-white text-gray-900',
            )}
            disabled={!selectedTeamId || teamStreaming}
          />
          <button
            onClick={() => onSend(teamInput)}
            disabled={!selectedTeamId || !teamInput.trim() || teamStreaming}
            className="flex h-11 w-11 items-center justify-center rounded-xl bg-blue-600 text-white hover:bg-blue-500 disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
            title="发送"
            aria-label="发送"
          >
            <Send className="h-4 w-4" />
          </button>
        </div>
        <div className="flex items-center justify-between mt-2">
          <button onClick={onManageTeams} className={cn(
            'flex items-center gap-1.5 py-1 rounded-md text-xs font-medium transition-colors',
            isDark ? 'text-gray-300 hover:bg-white/10' : 'text-gray-600 hover:bg-gray-100',
          )}>
            <Users className="h-3.5 w-3.5" />管理团队
          </button>
          {teamStreaming && (
            <span className="text-xs text-muted-foreground flex items-center gap-1">
              <Loader2 className="h-3 w-3 animate-spin" />团队响应中...
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// ApprovalBar —— 敏感操作审批条(浮窗 / 纯净模式通用)
// ---------------------------------------------------------------------------

interface ApprovalBarProps {
  pending: PendingApproval | null
  approving: boolean
  onRespond: (action: 'approve' | 'reject') => void
  variant?: 'floating' | 'panel'
  className?: string
}

export function ApprovalBar({ pending, approving, onRespond, variant = 'panel', className }: ApprovalBarProps) {
  if (!pending) return null
  return (
    <div className={cn(
      'mx-3 mb-2 rounded-xl border border-amber-200 dark:border-amber-500/30 bg-amber-50 dark:bg-amber-500/10 px-3 py-2.5 shrink-0',
      variant === 'floating' && 'mx-3',
      className,
    )}>
      <div className="flex items-start gap-2">
        <AlertTriangle className="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
        <div className="flex-1 min-w-0">
          <p className="text-xs font-medium text-amber-700 dark:text-amber-400">敏感操作需要确认</p>
          <p className="text-[11px] text-amber-600/90 dark:text-amber-500/80 mt-0.5">
            工具 <code className="font-mono">{pending.tool}</code>
            {pending.summary ? ` · ${pending.summary}` : ''}
          </p>
          {pending.args && pending.args !== '{}' && (
            <pre className="mt-1 max-h-16 overflow-auto rounded bg-amber-100/60 dark:bg-amber-500/10 px-2 py-1 text-[10px] whitespace-pre-wrap break-all">{pending.args}</pre>
          )}
          {pending.deadline && <p className="text-[10px] text-amber-500/70 mt-1">截止：{new Date(pending.deadline).toLocaleTimeString()}</p>}
        </div>
      </div>
      <div className="flex gap-2 mt-2 justify-end">
        <button onClick={() => onRespond('reject')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-gray-200/70 dark:bg-gray-700/60 text-gray-700 dark:text-gray-200 hover:bg-gray-300/70 dark:hover:bg-gray-600/60 disabled:opacity-50 transition-colors">
          <XCircle className="h-3 w-3" /> 拒绝
        </button>
        <button onClick={() => onRespond('approve')} disabled={approving} className="flex items-center gap-1 px-3 py-1 rounded-md text-xs bg-amber-500 text-white hover:bg-amber-600 disabled:opacity-50 transition-colors">
          {approving ? <Loader2 className="h-3 w-3 animate-spin" /> : <CheckCircle className="h-3 w-3" />} 批准执行
        </button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// 生效状态卡：对话主入口顶部一条横条, 展示当前生效的「模式 / 模型 / 助手 / 知识范围」,
// 并提供一键跳配置。解决「配了不会用、用了不知道配了啥」的配用割裂(P1 #3)。
// ---------------------------------------------------------------------------

/** 后端历史压缩的估算 token 预算(与 internal/ai/agentic/engine.go historyTokenBudget 对齐) */
export const CONTEXT_TOKEN_BUDGET = 12000

/**
 * estimateContextTokens 前端侧上下文用量粗估(与后端 estimateTokens 同口径):
 * CJK(汉字/全角/假名)≈1 token/字符, 其他(ASCII/拉丁)≈4 字符/token。误差 ±20% 量级,
 * 仅用于展示「上下文用量」徽标; 超过 CONTEXT_TOKEN_BUDGET 时后端会自动压缩较早历史。
 */
export function estimateContextTokens(messages: Array<{ role?: string; content?: string }>): number {
  let cjk = 0
  let other = 0
  for (const m of messages) {
    const s = m.content || ''
    for (const ch of s) {
      const code = ch.codePointAt(0) ?? 0
      if ((code >= 0x4e00 && code <= 0x9fff) || (code >= 0x3000 && code <= 0x30ff) || (code >= 0xff00 && code <= 0xffef)) {
        cjk++
      } else {
        other++
      }
    }
  }
  return cjk + Math.ceil(other / 4)
}

/** 把 token 数格式化为紧凑展示(如 850 / 1.2k / 12.5k) */
export function formatTokenCount(n: number): string {
  if (n < 1000) return String(n)
  return `${(n / 1000).toFixed(1)}k`
}

export interface ChatContextBarProps {
  /** 当前运行级模型展示名(空=自动路由; 未启用切换时展示用) */
  modelLabel?: string
  /** 知识范围分类(空数组=全部) */
  knowledgeScope: string[]
  /** 执行者: 默认助手 / 数字员工 / 智能体团队 三合一选择(替代原「模式」+「助手」两个概念) */
  executor?: Executor
  /** 可选执行者列表: 数字员工 + 智能体团队(下拉分组展示) */
  agents?: any[]
  teams?: any[]
  onExecutorChange?: (x: Executor) => void
  /** 模型列表 + 当前值 + 切换回调: 三者齐备时「模型」chip 变为下拉可直接切换 */
  models?: ModelInfo[]
  selectedModel?: string
  onModelChange?: (v: string) => void
  variant?: 'page' | 'panel'
  /** 一键跳配置回调(主入口传 navigate 到 /ai/settings 等) */
  onConfigure?: () => void
  /** 三者齐备时「知识范围」chip 变为下拉多选(全页/纯净模式唯一知识范围入口) */
  knowledgeCategories?: string[]
  knowledgeCategoryOptions?: string[]
  onKnowledgeCategoriesChange?: (cats: string[]) => void
  /** 点击「知识范围」chip 跳转知识库(未传多选回调时 chip 变可点击跳转) */
  onOpenKnowledge?: () => void
  /** 当前会话上下文用量(估算 token, 经 estimateContextTokens 计算; 0/未传=不展示) */
  contextTokens?: number
  className?: string
}

export function ChatContextBar({
  modelLabel,
  knowledgeScope,
  executor,
  agents = [],
  teams = [],
  onExecutorChange,
  models,
  selectedModel,
  onModelChange,
  variant = 'page',
  onConfigure,
  knowledgeCategories = [],
  knowledgeCategoryOptions = [],
  onKnowledgeCategoriesChange,
  onOpenKnowledge,
  contextTokens,
  className,
}: ChatContextBarProps) {
  const isDark = variant === 'panel'
  const [scopeOpen, setScopeOpen] = useState(false)
  const scopeLabel = knowledgeScope.length === 0 ? '全部' : knowledgeScope.join('、')
  // models 为空也允许下拉, 让用户至少能看到/选择 auto(空值=按权重/健康路由)
  const canPickModel = !!onModelChange
  const modelDisplay = canPickModel
    ? (selectedModel ? (models?.find(m => m.model === selectedModel)?.name || selectedModel) : '自动路由')
    : (modelLabel || '自动路由')
  const executorValue = executor?.type === 'team' ? `team:${executor.id || ''}` : executor?.type === 'agent' ? `agent:${executor.id || ''}` : 'default'
  const executorTitle = executor?.type === 'team'
    ? '智能体团队：主管拆解任务，各成员智能体协作完成'
    : executor?.type === 'agent'
      ? '数字员工：绑定专属工具与知识，按需自动多步执行'
      : '默认助手：纯对话推理，按需自动判断是否调用工具'
  const chipCls = cn(
    'px-1.5 py-0.5 rounded font-medium',
    isDark ? 'bg-blue-500/15 text-blue-300' : 'bg-blue-50 text-blue-700',
  )
  const wrapCls = 'flex items-center gap-1 text-muted-foreground'
  const selectCls = cn(
    'px-1 py-0.5 rounded font-medium bg-transparent focus:outline-none focus:ring-1 focus:ring-blue-500/50 cursor-pointer max-w-[160px]',
    isDark ? 'bg-blue-500/15 text-blue-300' : 'bg-blue-50 text-blue-700',
  )
  const canPickScope = !!onKnowledgeCategoriesChange && knowledgeCategoryOptions.length > 0
  const toggleScopeCategory = (c: string) => {
    const next = knowledgeCategories.includes(c)
      ? knowledgeCategories.filter(x => x !== c)
      : [...knowledgeCategories, c]
    onKnowledgeCategoriesChange?.(next)
  }
  return (
    <div className={cn(
      'flex items-center flex-wrap gap-x-3 gap-y-1 px-4 py-1.5 text-[11px] border-t shrink-0',
      isDark ? 'border-white/10 bg-white/[0.03]' : 'border-gray-100 bg-gray-50/70',
      className,
    )}>
      {/* 执行者: 默认助手 / 数字员工 / 智能体团队(唯一入口, 替代原「模式」+「助手」) */}
      {onExecutorChange ? (
        <span className={wrapCls}>
          <span className="opacity-70">执行者</span>
          <select
            value={executorValue}
            onChange={e => {
              const v = e.target.value
              if (v === 'default') return onExecutorChange({ type: 'default' })
              const [type, id] = v.split(':')
              onExecutorChange(type === 'team' ? { type: 'team', id } : { type: 'agent', id })
            }}
            className={selectCls}
            title={executorTitle}
          >
            <option value="default">默认助手</option>
            {agents.length > 0 && (
              <optgroup label="数字员工">
                {agents.map((a: any) => <option key={a.id} value={`agent:${a.id}`}>{a.name}</option>)}
              </optgroup>
            )}
            {teams.length > 0 && (
              <optgroup label="智能体团队">
                {teams.map((t: any) => <option key={t.id} value={`team:${t.id}`}>{t.name}</option>)}
              </optgroup>
            )}
          </select>
        </span>
      ) : (
        <span title={executorTitle} className={wrapCls}>
          <span className="opacity-70">执行者</span>
          <span className={chipCls}>{executor?.type === 'team' ? '智能体团队' : executor?.type === 'agent' ? (agents.find((a: any) => a.id === executor.id)?.name || '数字员工') : '默认助手'}</span>
        </span>
      )}
      {/* 模型: 传入 models+onModelChange 时可直接下拉切换 */}
      {canPickModel ? (
        <span className={wrapCls}>
          <span className="opacity-70">模型</span>
          <select
            value={selectedModel || ''}
            onChange={e => onModelChange?.(e.target.value)}
            className={selectCls}
            title="切换运行级模型(空=按权重/健康度路由)"
          >
            <option value="">自动（按权重/健康/类型）</option>
            {models!.map(m => (
              <option
                key={`${m.name}-${m.model}`}
                value={m.model}
                title={m.name !== 'default' && m.name !== m.model ? `${m.model} · ${m.base_url || m.name}` : undefined}
              >
                {m.healthy === false ? '⚠️ ' : ''}{m.name !== 'default' && m.name !== m.model ? m.name : m.model}{m.healthy === false ? '（降级）' : ''}
              </option>
            ))}
          </select>
        </span>
      ) : (
        <span title={modelDisplay && modelDisplay !== '自动路由' ? `当前模型：${modelDisplay}` : '自动路由：由后端按负载选择模型'} className={wrapCls}>
          <span className="opacity-70">模型</span>
          <span className={chipCls}>{modelDisplay}</span>
        </span>
      )}
      {/* 知识范围: 传入多选回调时 chip 变下拉多选(唯一入口); 否则按 onOpenKnowledge 跳转/只读 */}
      {canPickScope ? (
        <div className={cn(wrapCls, 'relative')}>
          <button
            onClick={() => setScopeOpen(v => !v)}
            title={knowledgeCategories.length === 0 ? '检索全部知识库分类，点击选择范围' : `仅检索分类：${scopeLabel}，点击修改`}
            className={cn(wrapCls, 'cursor-pointer hover:opacity-80 transition-opacity')}
          >
            <span className="opacity-70">知识范围</span>
            <span className={chipCls}>{scopeLabel}</span>
            <span className={cn('text-[9px] transition-transform', scopeOpen && 'rotate-180')}>▾</span>
          </button>
          {scopeOpen && (
            <>
              <button className="fixed inset-0 z-40 cursor-default" onClick={() => setScopeOpen(false)} aria-label="关闭知识范围选择" />
              <div className={cn(
                'absolute z-50 top-full left-0 mt-1.5 w-44 max-h-64 overflow-auto rounded-md border shadow-lg p-1.5 space-y-0.5',
                isDark ? 'border-white/10 bg-gray-900' : 'border-gray-200 bg-white',
              )}>
                <label className={cn('flex items-center gap-2 rounded px-2 py-1 text-xs cursor-pointer', isDark ? 'hover:bg-white/5' : 'hover:bg-gray-50')}>
                  <input
                    type="checkbox"
                    checked={knowledgeCategories.length === 0}
                    onChange={() => onKnowledgeCategoriesChange?.([])}
                    className="accent-blue-500"
                  />
                  全部
                </label>
                {knowledgeCategoryOptions.map(c => (
                  <label key={c} className={cn('flex items-center gap-2 rounded px-2 py-1 text-xs cursor-pointer', isDark ? 'hover:bg-white/5' : 'hover:bg-gray-50')}>
                    <input
                      type="checkbox"
                      checked={knowledgeCategories.includes(c)}
                      onChange={() => toggleScopeCategory(c)}
                      className="accent-blue-500"
                    />
                    <span className="truncate" title={c}>{c}</span>
                  </label>
                ))}
              </div>
            </>
          )}
        </div>
      ) : onOpenKnowledge ? (
        <button
          onClick={onOpenKnowledge}
          title={knowledgeScope.length === 0 ? '检索全部知识库分类，点击打开知识库' : `仅检索分类：${scopeLabel}，点击在知识库中查看`}
          className={cn(wrapCls, 'cursor-pointer hover:opacity-80 transition-opacity')}
        >
          <span className="opacity-70">知识范围</span>
          <span className={chipCls}>{scopeLabel}</span>
        </button>
      ) : (
        <span title={knowledgeScope.length === 0 ? '检索全部知识库分类' : `仅检索分类：${scopeLabel}`} className={wrapCls}>
          <span className="opacity-70">知识范围</span>
          <span className={chipCls}>{scopeLabel}</span>
        </span>
      )}
      {/* 上下文用量(只读, 恒显示便于感知): 估算 token; 超过预算提示将自动压缩 */}
      <span
          title={
            (contextTokens || 0) >= CONTEXT_TOKEN_BUDGET
              ? `上下文约 ${formatTokenCount(contextTokens || 0)} tokens，已达预算（${formatTokenCount(CONTEXT_TOKEN_BUDGET)}），较早对话将自动压缩为摘要`
              : `当前会话上下文约 ${formatTokenCount(contextTokens || 0)} tokens（预算 ${formatTokenCount(CONTEXT_TOKEN_BUDGET)}，超出后较早对话自动压缩）`
          }
          className={wrapCls}
        >
          <span className="opacity-70">上下文</span>
          <span className={cn(
            'px-1.5 py-0.5 rounded font-medium',
            (contextTokens || 0) >= CONTEXT_TOKEN_BUDGET
              ? (isDark ? 'bg-amber-500/20 text-amber-300' : 'bg-amber-50 text-amber-700')
              : chipCls,
          )}>
            ~{formatTokenCount(contextTokens || 0)}{(contextTokens || 0) >= CONTEXT_TOKEN_BUDGET ? ' · 将压缩' : ''}
          </span>
        </span>
      {onConfigure && (
        <button
          onClick={onConfigure}
          className="ml-auto flex items-center gap-1 text-muted-foreground hover:text-blue-600 dark:hover:text-blue-400 transition-colors"
          title="查看 / 修改 AI 配置"
        >
          <Settings className="h-3 w-3" /> 配置
        </button>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// 会话历史列表(共享): 全页侧栏与纯净模式历史抽屉共用, 统一当前会话高亮 /
// 共享权限标识 / 新建入口的观感; 分享与删除通过可选回调按需启用(不依赖 antd)。
// ---------------------------------------------------------------------------

export interface ConversationItem {
  id: string
  title?: string
  updated_at?: string
  UpdatedAt?: string
  permission?: string
}

export interface ConversationListProps {
  conversations: ConversationItem[]
  /** 当前会话 id, 命中则高亮 */
  activeId?: string
  onSelect: (id: string) => void
  /** 传入则渲染「新建对话」虚线按钮 */
  onNew?: () => void
  /** 传入则对属主会话显示删除按钮(确认逻辑由调用方负责, 遵循契约禁原生 confirm) */
  onDelete?: (id: string) => void
  /** 传入则对属主会话显示分享按钮 */
  onShare?: (c: ConversationItem) => void
  /** 传入则允许双击标题重命名会话(调用方负责调 PATCH 接口, 返回是否成功) */
  onRename?: (id: string, title: string) => Promise<boolean> | boolean
  emptyText?: string
  className?: string
}

export function ConversationList({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  onShare,
  onRename,
  emptyText = '暂无历史对话',
  className,
}: ConversationListProps) {
  // 标题关键字过滤(本地过滤, 不发请求); 无会话时隐藏搜索框
  const [query, setQuery] = useState('')
  // 重命名内联编辑: 双击标题进入, Enter/失焦提交, Esc 取消
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editTitle, setEditTitle] = useState('')
  const commitRename = async (id: string) => {
    const title = editTitle.trim()
    setEditingId(null)
    if (!title || !onRename) return
    await onRename(id, title)
  }
  const kw = query.trim().toLowerCase()
  const filtered = kw
    ? conversations.filter(c => sanitizeConvTitle(c.title).toLowerCase().includes(kw))
    : conversations
  return (
    <div className={cn('space-y-1', className)}>
      {onNew && (
        <button onClick={onNew} className="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 rounded-md border border-dashed border-gray-300 dark:border-gray-700 text-xs text-muted-foreground hover:border-blue-400 hover:text-blue-600 dark:hover:border-blue-500/40 transition-colors">
          <Sparkles className="h-3.5 w-3.5" />新建对话
        </button>
      )}
      {conversations.length > 0 && (
        <div className="relative">
          <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3 w-3 text-muted-foreground pointer-events-none" />
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            placeholder="搜索会话标题"
            className="w-full pl-6 pr-2 py-1.5 text-xs rounded-md border border-gray-200 dark:border-gray-700 bg-transparent text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-blue-300 dark:focus:ring-blue-500/40"
          />
        </div>
      )}
      {conversations.length === 0 ? (
        <p className="text-xs text-muted-foreground text-center py-4">{emptyText}</p>
      ) : filtered.length === 0 ? (
        <p className="text-xs text-muted-foreground text-center py-3">无匹配会话</p>
      ) : (
        filtered.map(c => {
          const canRename = !!onRename && (!c.permission || c.permission === 'owner')
          return (
          <div key={c.id} className="group flex items-center gap-1">
            {editingId === c.id ? (
              <input
                autoFocus
                value={editTitle}
                onChange={e => setEditTitle(e.target.value)}
                onKeyDown={e => {
                  if (e.key === 'Enter') commitRename(c.id)
                  else if (e.key === 'Escape') setEditingId(null)
                }}
                onBlur={() => commitRename(c.id)}
                placeholder="会话标题"
                className="flex-1 min-w-0 text-sm rounded-lg border border-blue-300 dark:border-blue-500/40 bg-white dark:bg-gray-900 px-2.5 py-2 text-foreground focus:outline-none focus:ring-1 focus:ring-blue-400"
              />
            ) : (
            <button
              onClick={() => onSelect(c.id)}
              onDoubleClick={canRename ? () => { setEditingId(c.id); setEditTitle(c.title || '') } : undefined}
              title={canRename ? '双击重命名' : undefined}
              className={cn(
                'flex-1 min-w-0 text-left p-2.5 rounded-lg transition-colors',
                c.id === activeId
                  ? 'bg-blue-50 dark:bg-blue-500/10 ring-1 ring-blue-200 dark:ring-blue-500/30'
                  : 'hover:bg-gray-100 dark:hover:bg-gray-800',
              )}
            >
              <p className="text-sm font-medium truncate flex items-center gap-1">
                {sanitizeConvTitle(c.title)}
                {c.permission === 'edit' && <span className="text-[9px] px-1 py-0.5 rounded bg-violet-500/15 text-violet-500 shrink-0">共享·可编辑</span>}
                {c.permission === 'view' && <span className="text-[9px] px-1 py-0.5 rounded bg-violet-500/15 text-violet-500 shrink-0 flex items-center gap-0.5"><Lock className="h-2.5 w-2.5" />共享·只读</span>}
              </p>
              <p className="text-xs text-muted-foreground mt-0.5">{new Date(c.updated_at || c.UpdatedAt || Date.now()).toLocaleString('zh-CN')}</p>
            </button>
            )}
            {onShare && (!c.permission || c.permission === 'owner') && (
              <button onClick={() => onShare(c)} className="opacity-0 group-hover:opacity-100 p-1.5 rounded-md text-muted-foreground hover:text-violet-500 hover:bg-violet-500/10 transition-opacity" title="分享会话">
                <Share2 className="h-3.5 w-3.5" />
              </button>
            )}
            {onDelete && (!c.permission || c.permission === 'owner') && (
              <button onClick={() => onDelete(c.id)} className="opacity-0 group-hover:opacity-100 p-1.5 rounded-md text-red-400 hover:bg-red-500/10 transition-opacity" title="删除会话">
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          )
        })
      )}
    </div>
  )
}
