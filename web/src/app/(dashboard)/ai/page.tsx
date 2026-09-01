
import { useEffect, useState, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAIStore } from '@/stores/ai'
import { useCan } from '@/stores/permissions'
import { useTeams, type ChatMessage, type TeamStreamEvent, type AgentTeam } from '@/stores/ai'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Input, Tag, Space, Modal, Tooltip, Select, Popconfirm, message } from 'antd'
import { Bot, Send, History, Settings, Sparkles, Terminal, FileText, Server, ChevronRight, Loader2, User, BarChart3, Users, ThumbsUp, ThumbsDown, Rocket, Target, AlertTriangle, CheckCircle, XCircle, ChevronDown, Trash2, Bookmark, Download, Save, Paperclip, Brain, Share2, Lock } from 'lucide-react'
import { cn } from '@/lib/utils'
import { apiFetchRaw } from '@/lib/utils'
import { AiMarkdown } from '@/components/ai-markdown'
import { useThemeStore } from '@/stores/theme'
import { renderMarkdown, renderExploreContent } from '@/lib/ai-render'
import { getMessage } from '@/lib/message'

// ============================================================================
// 类型与本地工具
// ============================================================================

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
  thinking?: string
  citations?: string[]
  feedback_rating?: 'up' | 'down'
  status?: 'streaming' | 'done' | 'error'
  errorText?: string
  timestamp: Date
}

interface ToolCallLocal {
  id: string
  name: string
  args: string
  result: string
  status: 'running' | 'done'
}

interface AgentRunLocal {
  agent_id: string
  agent_name: string
  role: string
  status: 'streaming' | 'done' | 'error'
  text: string
  error?: string
  toolCalls: ToolCallLocal[]
  supervisorNote?: string
}

interface TeamTurnLocal {
  id: string
  teamId: string
  user: string
  agents: AgentRunLocal[]
  final: string
  done: boolean
  error?: string
}

type AIMode = 'normal' | 'explore' | 'team'

const QUICK_ACTIONS = [
  { label: '查询主机状态', icon: Server, prompt: '帮我查询所有主机的运行状态' },
  { label: '执行巡检任务', icon: Terminal, prompt: '执行一次自动化巡检任务' },
  { label: '生成运维报告', icon: FileText, prompt: '生成本周的运维报告' },
  { label: '排查故障', icon: Sparkles, prompt: '帮我排查当前系统的异常情况' },
]

const modeLabelMap: Record<AIMode, string> = { normal: '普通', explore: '自主', team: '团队' }
const teamModeBadge: Record<string, string> = {
  supervisor: 'default', parallel: 'blue', sequential: 'orange', debate: 'red',
}
const teamModeLabel: Record<string, string> = {
  supervisor: '主管调度', parallel: '并行协作', sequential: '顺序流转', debate: '辩论裁决',
}
const teamRoleLabel: Record<string, string> = {
  supervisor: '主管', member: '成员', finalizer: '终结者',
}

// ============================================================================
// 页面
// ============================================================================

export default function AIPage() {
  const { agents, fetchAgents, streamChat, loadConversations, submitFeedback: submitFB, models, fetchModels } = useAIStore()
  const { teams, loading: teamsLoading, loadTeams, streamTeamChat } = useTeams()
  const navigate = useNavigate()
  const { theme } = useThemeStore()

  const [mode, setMode] = useState<AIMode>('normal')
  // —— 运行级模型选择(按会话/请求覆盖; 空=回退 agent/引擎默认) ——
  const [selectedModel, setSelectedModel] = useState('')

  // —— 全局思考过程开关(localStorage 持久化) ——
  const [showThinking, setShowThinking] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    return localStorage.getItem('ai.showThinking') !== 'false'
  })

  // —— 普通模式状态 ——
  const [messages, setMessages] = useState<Message[]>([
    { id: '0', role: 'assistant', content: '你好！我是 OpsOrigin AI 助手，可以帮你完成运维任务。\n\n👉 **快速开始：** 点击右上角「设置」→「模型」配置 LLM API Key 即可使用。\n\n配置完成后可直接提问，或选择右侧快捷操作。', timestamp: new Date() },
  ])
  // 上下文用量估算: 中文约 1 token/1.5 字符, 英文约 1 token/4 字符, 折中按 1 token/2 字符粗算
  const estTokens = messages.reduce((n, m) => n + Math.ceil((m.content?.length || 0) / 2), 0)
  const ctxPct = Math.min(100, Math.round((estTokens / 32000) * 100))
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [selectedAgent, setSelectedAgent] = useState<any>(null)
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<any[]>([])
  // 当前会话 ID: 加载历史后指向该会话, 继续发送时携带以续接服务端持久历史
  const [convId, setConvId] = useState('')
  // 当前会话权限(用户隔离): owner=自己创建 / edit=共享可继续 / view=共享只读
  const [convPerm, setConvPerm] = useState<'owner' | 'edit' | 'view' | ''>('')
  // 共享弹窗
  const [shareConv, setShareConv] = useState<{ id: string; title: string } | null>(null)
  const [shareForm, setShareForm] = useState({ target_type: 'user', target_id: '', permission: 'view' })
  // AI 工具执行权限门禁: 无权限时禁发消息(加载完成前乐观放行)
  const canUseAI = useCan('ai:tool:exec')
  // 对话产物(MinIO 归档): 本会话已保存的文件列表
  const [artifacts, setArtifacts] = useState<{ id: string; message_id: string; file_name: string; file_type: string; file_size: number; created_at: string }[]>([])
  // 会话工作区(工具产物自动收集): script_run 等工具输出
  const [workspaceFiles, setWorkspaceFiles] = useState<{ id: string; tool_name: string; file_name: string; file_size: number; created_at: string }[]>([])
  const messagesEndRef = useRef<HTMLDivElement>(null)

  // —— 团队模式状态 ——
  const [selectedTeamId, setSelectedTeamId] = useState<string | null>(null)
  const [teamTurns, setTeamTurns] = useState<TeamTurnLocal[]>([])
  const [teamInput, setTeamInput] = useState('')
  const [teamStreaming, setTeamStreaming] = useState(false)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)

  // —— 自主模式状态 ——
  const [exploreRuns, setExploreRuns] = useState<{ id: string; goal: string; content: string; status: 'running' | 'done' | 'error'; error?: string }[]>([])
  const [exploreInput, setExploreInput] = useState('')
  const [exploreStreaming, setExploreStreaming] = useState(false)
  const exploreEndRef = useRef<HTMLDivElement>(null)

  // —— 敏感操作审批弹窗 ——
  const [pendingApproval, setPendingApproval] = useState<any>(null)
  const [pendingConvId, setPendingConvId] = useState<string | null>(null)
  const [pendingArtifactId, setPendingArtifactId] = useState<string | null>(null)
  const [pendingWsFileId, setPendingWsFileId] = useState<string | null>(null)
  const [approvalBusy, setApprovalBusy] = useState(false)
  const { respondApproval } = useAIStore()

  useEffect(() => {
    fetchAgents()
    fetchModels()
    if (mode === 'team') loadTeams()
  }, [fetchAgents, fetchModels, mode, loadTeams])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])
  // 切换会话时加载该会话的归档产物与工作区文件
  useEffect(() => { loadArtifacts(convId); loadWorkspace(convId) }, [convId])
  useEffect(() => {
    teamMessagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [teamTurns])
  useEffect(() => {
    exploreEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [exploreRuns])

  // ----------------------- 普通 / 思维链 模式 -----------------------

  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    }
    setShowHistory(!showHistory)
  }

  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await fetch(`${window.location.origin}/api/v1/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      // 记录当前会话权限: view=只读(禁发), owner/edit=可继续对话
      setConvPerm(conv?.permission === 'view' ? 'view' : conv?.permission === 'edit' ? 'edit' : conv?.permission === 'owner' ? 'owner' : '')
      if (conv?.messages) {
        const msgs: Message[] = conv.messages.map((m: any, i: number) => ({
          id: `${id}-${i}`,
          role: m.role,
          content: m.content,
          timestamp: new Date(),
        }))
        setMessages(msgs)
      }
      // 绑定当前会话: 后续发送将携带该 conversation_id, 续接服务端可信历史(含 tool 消息)
      setConvId(id)
      setShowHistory(false)
    } catch {}
  }

  // 删除会话(不可恢复)
  const handleDeleteConversation = async () => {
    const id = pendingConvId
    if (!id) return
    try {
      const token = localStorage.getItem('opsorigin_token')
      const resp = await fetch(`${window.location.origin}/api/v1/ai/conversations/${id}`, {
        method: 'DELETE',
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
      if (!resp.ok) throw new Error(`删除失败(${resp.status})`)
      getMessage().success('会话已删除')
      if (id === convId) {
        // 删的是当前会话: 重置为新对话
        setConvId('')
        setMessages([{ id: Date.now().toString(), role: 'assistant', content: '已删除该会话，开始新对话吧。', timestamp: new Date() }])
      }
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    } catch (e: any) {
      getMessage().error('删除失败: ' + e.message)
    }
    setPendingConvId(null)
  }

  // submitFeedback 上报点赞/点踩
  const submitFeedback = async (msg: Message, rating: 'up' | 'down' = 'up') => {
    await submitFB({ message_id: msg.id, rating, conversation_id: convId || undefined })
  }

  // 保存 AI 回复为知识库文章(分类不存在自动创建)
  const saveMsgToKnowledge = async (msg: Message) => {
    if (!msg.content.trim()) return
    const title = msg.content.replace(/[#*`>\-\n\r]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 40) || 'AI 对话沉淀'
    try {
      const r = await fetch('/api/v1/knowledge', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title, content: msg.content, category: 'AI 对话沉淀', tags: ['ai', 'conversation'], status: 'published',
        }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已保存到知识库（分类「AI 对话沉淀」）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }

  // 下载 AI 回复为 Markdown 文件
  const downloadMsgAsMd = (msg: Message) => {
    if (!msg.content.trim()) return
    const blob = new Blob([msg.content], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `ai-${msg.id}.md`
    a.click()
    URL.revokeObjectURL(url)
  }

  // —— 对话产物归档(MinIO) ——
  const authHeaders = (): Record<string, string> => {
    const token = localStorage.getItem('opsorigin_token')
    return token ? { Authorization: `Bearer ${token}` } : {}
  }

  const loadArtifacts = async (id: string) => {
    if (!id) { setArtifacts([]); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${id}/artifacts`, { headers: authHeaders() })
      if (r.ok) setArtifacts(await r.json())
    } catch {}
  }

  // 保存 AI 回复为归档文件(落 MinIO)
  const saveMsgAsArtifact = async (msg: Message) => {
    if (!msg.content.trim()) return
    if (!convId) { getMessage().error('请先新建或选择对话会话'); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${convId}/artifacts`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ message_id: msg.id, file_name: `ai-${msg.id}.md`, content: msg.content }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已归档到对象存储（本会话产物区可下载）')
      loadArtifacts(convId)
    } catch (e: any) { getMessage().error('归档失败: ' + e.message) }
  }

  const downloadArtifact = async (aid: string) => {
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/artifacts/${aid}`)
      if (!r.ok) throw new Error(`下载失败(${r.status})`)
      const blob = await r.blob()
      const cd = r.headers.get('Content-Disposition') || ''
      const name = cd.match(/filename="?([^";]+)/)?.[1] || `artifact-${aid}.md`
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = decodeURIComponent(name)
      a.click()
      URL.revokeObjectURL(url)
    } catch (e: any) { getMessage().error('下载失败: ' + e.message) }
  }

  const deleteArtifact = async () => {
    const aid = pendingArtifactId
    if (!aid) return
    try {
      await fetch(`/api/v1/ai/conversations/${convId}/artifacts/${aid}`, { method: 'DELETE', headers: authHeaders() })
      getMessage().success('已删除')
      loadArtifacts(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingArtifactId(null)
  }

  // —— 会话工作区(工具产物自动收集) ——
  const loadWorkspace = async (id: string) => {
    if (!id) { setWorkspaceFiles([]); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${id}/workspace`, { headers: authHeaders() })
      if (r.ok) setWorkspaceFiles(await r.json())
    } catch {}
  }

  const downloadWorkspaceFile = async (fid: string) => {
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/workspace/${fid}`)
      if (!r.ok) throw new Error(`下载失败(${r.status})`)
      const blob = await r.blob()
      const cd = r.headers.get('Content-Disposition') || ''
      const name = cd.match(/filename="?([^";]+)/)?.[1] || `workspace-${fid}.json`
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = decodeURIComponent(name)
      a.click()
      URL.revokeObjectURL(url)
    } catch (e: any) { getMessage().error('下载失败: ' + e.message) }
  }

  const deleteWorkspaceFile = async () => {
    const fid = pendingWsFileId
    if (!fid) return
    try {
      await fetch(`/api/v1/ai/conversations/${convId}/workspace/${fid}`, { method: 'DELETE', headers: authHeaders() })
      getMessage().success('已删除')
      loadWorkspace(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingWsFileId(null)
  }

  // 记住这条: 保存为长期记忆(重启不丢, 新开对话自动注入相关记忆)
  const saveMsgToMemory = async (msg: Message) => {
    if (!msg.content.trim()) return
    const idx = messages.findIndex(m => m.id === msg.id)
    let problem = '对话要点'
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') {
        problem = messages[i].content.replace(/\s+/g, ' ').slice(0, 40) || '对话要点'
        break
      }
    }
    try {
      const r = await fetch('/api/v1/ai/memories', {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify({ agent_id: selectedAgent?.id || '', category: '对话要点', problem, resolution: msg.content.slice(0, 500) }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已记住（长期记忆，重启不丢）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }

  // 重试失败消息: 重新发送该错误消息之前的最后一条用户消息
  const retryMessage = (errMsg: Message) => {
    const idx = messages.findIndex(m => m.id === errMsg.id)
    if (idx < 0) return
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') {
        sendMessage(messages[i].content)
        return
      }
    }
  }

  // 分享会话(仅属主): 目标用户/组 + 权限 view/edit
  const saveShare = async () => {
    if (!shareConv || !shareForm.target_id.trim()) { getMessage().error('请填写目标用户/组 ID'); return }
    try {
      const r = await fetch(`/api/v1/ai/conversations/${shareConv.id}/shares`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', ...authHeaders() },
        body: JSON.stringify(shareForm),
      })
      if (!r.ok) throw new Error(`共享失败(${r.status})`)
      getMessage().success(shareForm.permission === 'edit' ? '已共享（可继续对话）' : '已共享（仅查看）')
      setShareConv(null)
      setShareForm({ target_type: 'user', target_id: '', permission: 'view' })
    } catch (e: any) { getMessage().error('共享失败: ' + e.message) }
  }

  const sendMessage = async (text: string) => {
    const content = text.trim()
    if (!content || loading) return

    const userMsg: Message = { id: Date.now().toString(), role: 'user', content, timestamp: new Date() }
    const next = [...messages, userMsg]
    setMessages(next)
    setInput('')
    setLoading(true)

    const aiId = (Date.now() + 1).toString()
    const aiMsg: Message = { id: aiId, role: 'assistant', content: '', status: 'streaming', timestamp: new Date() }
    setMessages(prev => [...prev, aiMsg])

    let history = next
      .filter(m => m.role === 'user' || m.role === 'assistant')
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))

    // 超长自动压缩: 历史估算 token 超过预算时从最新往前裁剪, 避免上下文爆炸
    const HIST_BUDGET = 24000 // 约 24k token 预算给历史
    let histTokens = history.reduce((n, m) => n + Math.ceil((m.content?.length || 0) / 2), 0)
    if (histTokens > HIST_BUDGET && history.length > 4) {
      const trimmed: typeof history = []
      let acc = 0
      for (let i = history.length - 1; i >= 0; i--) {
        const t = Math.ceil((history[i].content?.length || 0) / 2)
        if (acc + t > HIST_BUDGET && trimmed.length > 0) break
        trimmed.unshift(history[i])
        acc += t
      }
      const omitted = history.length - trimmed.length
      if (omitted > 0) {
        history = [{ role: 'assistant', content: `⚠️ 上下文过长，更早的 ${omitted} 条消息已自动省略（估算约 ${(histTokens / 1000).toFixed(1)}k tokens）。如需完整上下文请新开对话。` }, ...trimmed]
        getMessage().info('上下文过长，已自动压缩早期消息')
      }
    }

    // 会话续接: 已有会话(点历史加载)则携带其 ID; 否则本地生成新会话 ID, 后端按 ID 自动创建持久化
    let cid = convId
    if (!cid) {
      cid = `ai_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 8)}`
      setConvId(cid)
    }

    let full = ''
    let thinkingAcc = ''
    await streamChat(content, (token) => {
      full += token
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full } : m))
    }, (meta) => {
      // 成功结束: 标注 done(meta 为 undefined 表示失败分支, 由 onError 标注 error)
      if (meta) {
        setMessages(prev => prev.map(m => m.id === aiId ? { ...m, status: 'done' as const } : m))
      }
      if (meta?.citations && meta.citations.length > 0) {
        setMessages(prev => prev.map(m => m.id === aiId ? { ...m, content: full, citations: meta.citations } : m))
      }
      setLoading(false)
    }, history, selectedAgent?.id, showThinking, showThinking ? (t) => {
      thinkingAcc += t
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, thinking: thinkingAcc } : m))
    } : undefined, undefined, (approval) => setPendingApproval(approval), cid, undefined, selectedModel, (errMsg) => {
      setMessages(prev => prev.map(m => m.id === aiId ? { ...m, status: 'error' as const, errorText: errMsg } : m))
    })
  }

  // ----------------------- 团队模式 -----------------------

  const handleTeamSend = async (text: string) => {
    const content = text.trim()
    if (!content || !selectedTeamId || teamStreaming) return

    const turnId = Date.now().toString()
    const newTurn: TeamTurnLocal = { id: turnId, teamId: selectedTeamId, user: content, agents: [], final: '', done: false }
    setTeamTurns(prev => [...prev, newTurn])
    setTeamInput('')
    setTeamStreaming(true)

    // 把历史上已完成轮次拼成历史上下文
    const history: ChatMessage[] = []
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
              // 逆序找到最后一个 running 的同名工具调用, 标记完成
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
            if (idx >= 0) {
              agents[idx] = { ...agents[idx], supervisorNote: ev.content }
            }
            else {
              agents.push({ agent_id: ev.agent_id, agent_name: ev.agent_name, role: ev.role, status: 'done', text: '', toolCalls: [], supervisorNote: ev.content })
            }
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

    await streamTeamChat(selectedTeamId, content, history, onEvent)
    setTeamStreaming(false)
  }

  // ----------------------- 自主模式 -----------------------

  const handleExploreSend = async (text: string) => {
    const goal = text.trim()
    if (!goal || exploreStreaming) return

    const runId = Date.now().toString()
    const newRun = { id: runId, goal, content: '', status: 'running' as const }
    setExploreRuns(prev => [...prev, newRun])
    setExploreInput('')
    setExploreStreaming(true)

    let acc = ''
    let thinkingAcc = ''
    await streamChat(goal, (token) => {
      acc += token
      setExploreRuns(prev => prev.map(r => r.id === runId ? { ...r, content: acc } : r))
    }, (meta) => {
      // 结束时把思考过程以 <think> 标签前置展示(可折叠)
      const final = thinkingAcc ? `<think>${thinkingAcc}</think>\n\n${acc}` : acc
      setExploreRuns(prev => prev.map(r => r.id === runId ? { ...r, content: final, status: 'done' as const } : r))
      setExploreStreaming(false)
    }, [], undefined, showThinking, showThinking ? (t) => {
      thinkingAcc += t
    } : undefined, true, (approval) => setPendingApproval(approval))
  }

  const EXPLORE_EXAMPLES = [
    '检查所有主机的运行状态，找出异常主机并给出处理建议',
    '梳理当前 CMDB 中数据库资产的拓扑关系',
    '分析近期告警，归类并给出根因分析',
  ]

  // ----------------------- 审批响应 -----------------------

  const handleApprovalRespond = async (action: 'approve' | 'reject') => {
    if (!pendingApproval) return
    setApprovalBusy(true)
    const ok = await respondApproval(pendingApproval.id, action)
    setApprovalBusy(false)
    if (ok) setPendingApproval(null)
  }

  // ============================================================================
  // 渲染
  // ============================================================================

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Sparkles className="h-6 w-6 text-blue-500" />
            AI 助手
          </h1>
          <p className="text-sm text-muted-foreground">智能运维助手，帮你高效完成运维任务</p>
        </div>
        <div className="flex items-center gap-2">
          {mode !== 'team' && (
            <>
              <button
                onClick={() => { const v = !showThinking; setShowThinking(v); localStorage.setItem('ai.showThinking', String(v)) }}
                className={cn(
                  'px-2 py-1 rounded text-xs border transition-colors flex items-center gap-1',
                  showThinking
                    ? 'bg-purple-50 border-purple-200 text-purple-700 dark:bg-purple-500/10 dark:border-purple-500/30 dark:text-purple-400'
                    : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                )}
                title="显示/隐藏 AI 思考过程"
              >
                💭 思考过程
              </button>
              {mode !== 'explore' && (
                <>
<Button size="middle" onClick={() => navigate('/ai/analytics')}><BarChart3 className="h-4 w-4" />分析</Button>
                   <Button size="middle" onClick={handleOpenHistory}><History className="h-4 w-4" />{showHistory ? '返回' : '历史'}</Button>
                   <Button size="middle" onClick={() => navigate('/ai/settings')}><Settings className="h-4 w-4" />设置</Button>
                </>
              )}
            </>
          )}
        </div>
      </div>

      {/* 模式 Tab：普通 / 自主 / 团队 */}
      <div className="flex gap-2 mb-4">
        {(['normal', 'explore', 'team'] as AIMode[]).map(m => (
          <button
            key={m}
            onClick={() => setMode(m)}
            className={cn(
              'px-4 py-1.5 rounded-md text-sm border transition-colors',
              mode === m
                ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/10 dark:border-blue-500/30 dark:text-blue-400'
                : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
            )}
          >
            {modeLabelMap[m]}模式
          </button>
        ))}
      </div>

      {/* 普通模式 */}
      {mode === 'normal' && (
        <div className="flex-1 flex gap-4 min-h-0">
          <div className="flex-1 flex flex-col border rounded-lg overflow-hidden">
            <div className="flex-1 overflow-y-auto p-4 space-y-4">
              {showHistory ? (
                <div className="space-y-2">
                  <h3 className="font-medium text-sm mb-3">对话历史</h3>
                  {conversations.length === 0 ? (
                    <p className="text-xs text-muted-foreground">暂无对话记录</p>
                  ) : (
                    conversations.map(c => (
                      <div key={c.id} className="group flex items-center gap-1">
                        <button
                          onClick={() => handleLoadConversation(c.id)}
                          className="flex-1 text-left p-3 rounded-lg bg-gray-50 dark:bg-gray-800 hover:bg-gray-100 dark:hover:bg-gray-700 transition-colors"
                        >
                          <p className="text-sm font-medium truncate flex items-center gap-1">
                            {c.title || '新对话'}
                            {c.permission === 'edit' && <Tag className="text-[9px] h-4">共享·可编辑</Tag>}
                            {c.permission === 'view' && <Tag className="text-[9px] h-4"><Lock className="h-2.5 w-2.5 mr-0.5" />共享·只读</Tag>}
                          </p>
                          <p className="text-xs text-muted-foreground mt-0.5">{new Date(c.updated_at || c.UpdatedAt).toLocaleString('zh-CN')}</p>
                        </button>
                        {(!c.permission || c.permission === 'owner') && (
                          <button
                            onClick={() => { setShareConv({ id: c.id, title: c.title || '新对话' }); setShareForm({ target_type: 'user', target_id: '', permission: 'view' }) }}
                            className="opacity-0 group-hover:opacity-100 p-1.5 rounded-md text-muted-foreground hover:text-violet-500 hover:bg-violet-500/10 transition-opacity"
                            title="分享会话"
                          >
                            <Share2 className="h-3.5 w-3.5" />
                          </button>
                        )}
                        {(!c.permission || c.permission === 'owner') && (
                          <button
                            onClick={() => setPendingConvId(c.id)}
                            className="opacity-0 group-hover:opacity-100 p-1.5 rounded-md text-red-400 hover:bg-red-500/10 transition-opacity"
                            title="删除会话"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        )}
                      </div>
                    ))
                  )}
                </div>
              ) : (
                <>
                  {messages.map(msg => (
                    <div key={msg.id} className={cn('flex gap-3', msg.role === 'user' ? 'justify-end' : 'justify-start')}>
                      {msg.role === 'assistant' && (
                        <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                          <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                        </div>
                      )}
                      <div className={cn(
                        'max-w-[70%] rounded-lg px-4 py-2 text-sm',
                        msg.role === 'user'
                          ? 'bg-blue-600 text-white whitespace-pre-wrap'
                          : 'bg-gray-100 dark:bg-gray-800 prose-sm max-w-none',
                      )}>
                        {msg.role === 'user' ? msg.content : (
                          <>
                            {msg.thinking && (
                              <details className="mb-2">
                                <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">💭 思维链 ({msg.thinking.slice(0, 30)}...)</summary>
                                <div className="mt-1 p-2 rounded bg-gray-200 dark:bg-gray-700 text-xs whitespace-pre-wrap text-muted-foreground">{msg.thinking}</div>
                              </details>
                            )}
                            <AiMarkdown content={msg.content} />

                            {/* RAG 引用来源 */}
                            {msg.citations && msg.citations.length > 0 && (
                              <details className="mt-2">
                                <summary className="text-[10px] text-muted-foreground cursor-pointer hover:text-foreground">📚 引用来源 ({msg.citations.length})</summary>
                                <div className="mt-1 space-y-0.5">
                                  {msg.citations.map((c, i) => (
                                    <div key={i} className="text-[10px] text-muted-foreground pl-2 border-l-2 border-blue-200 dark:border-blue-500/30">
                                      {c.startsWith('[') ? c : `[${c}]`}
                                    </div>
                                  ))}
                                </div>
                              </details>
                            )}

                            {/* 失败态: 错误提示 + 重试 */}
                            {msg.status === 'error' && (
                              <div className="mt-2 rounded-md border border-red-300 dark:border-red-500/40 bg-red-50 dark:bg-red-500/10 p-2">
                                <p className="text-[11px] text-red-600 dark:text-red-400 flex items-center gap-1">
                                  <AlertTriangle className="h-3 w-3 shrink-0" />
                                  {msg.errorText || 'AI 响应失败'}
                                </p>
                                <button
                                  onClick={() => retryMessage(msg)}
                                  className="mt-1.5 text-[11px] text-red-600 dark:text-red-400 hover:underline font-medium"
                                >
                                  ↻ 重试
                                </button>
                              </div>
                            )}

                            {/* 反馈按钮 + 产物操作 */}
                            {msg.id !== '0' && (
                              <div className="flex items-center gap-1 mt-2 pt-1.5 border-t border-gray-200 dark:border-[rgba(255,255,255,0.04)]">
                                <button
                                  onClick={() => {
                                    if (msg.feedback_rating === 'up') return
                                    submitFeedback(msg)
                                    setMessages(prev => prev.map(m => m.id === msg.id ? { ...m, feedback_rating: 'up' as const } : m))
                                  }}
                                  disabled={!!msg.feedback_rating}
                                  className={`p-1 rounded text-[10px] transition-colors ${msg.feedback_rating === 'up' ? 'text-blue-500 bg-blue-50 dark:bg-blue-500/10' : 'text-muted-foreground hover:text-blue-500'}`}
                                  title="有帮助"
                                >
                                  <ThumbsUp className="h-3 w-3" />
                                </button>
                                <button
                                  onClick={() => {
                                    if (msg.feedback_rating === 'down') return
                                    submitFeedback(msg, 'down')
                                    setMessages(prev => prev.map(m => m.id === msg.id ? { ...m, feedback_rating: 'down' as const } : m))
                                  }}
                                  disabled={!!msg.feedback_rating}
                                  className={`p-1 rounded text-[10px] transition-colors ${msg.feedback_rating === 'down' ? 'text-red-500 bg-red-50 dark:bg-red-500/10' : 'text-muted-foreground hover:text-red-500'}`}
                                  title="没帮助"
                                >
                                  <ThumbsDown className="h-3 w-3" />
                                </button>
                                <span className="w-px h-3 bg-gray-200 dark:bg-gray-700 mx-0.5" />
                                <button
                                  onClick={() => saveMsgToMemory(msg)}
                                  className="p-1 rounded text-[10px] text-muted-foreground hover:text-violet-500 transition-colors"
                                  title="记住（存长期记忆，跨会话复用）"
                                >
                                  <Brain className="h-3 w-3" />
                                </button>
                                <button
                                  onClick={() => saveMsgToKnowledge(msg)}
                                  className="p-1 rounded text-[10px] text-muted-foreground hover:text-amber-500 transition-colors"
                                  title="保存到知识库"
                                >
                                  <Bookmark className="h-3 w-3" />
                                </button>
                                <button
                                  onClick={() => downloadMsgAsMd(msg)}
                                  className="p-1 rounded text-[10px] text-muted-foreground hover:text-blue-500 transition-colors"
                                  title="下载为 Markdown"
                                >
                                  <Download className="h-3 w-3" />
                                </button>
                                <button
                                  onClick={() => saveMsgAsArtifact(msg)}
                                  className="p-1 rounded text-[10px] text-muted-foreground hover:text-indigo-500 transition-colors"
                                  title="归档到对象存储"
                                >
                                  <Save className="h-3 w-3" />
                                </button>
                              </div>
                            )}
                          </>
                        )}
                      </div>
                      {msg.role === 'user' && (
                        <div className="h-8 w-8 rounded-full bg-gray-200 dark:bg-gray-700 flex items-center justify-center shrink-0">
                          <User className="h-4 w-4 text-gray-600 dark:text-gray-300" />
                        </div>
                      )}
                    </div>
                  ))}
                  {loading && (
                    <div className="flex gap-3 justify-start">
                      <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                        <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                      </div>
                      <div className="bg-gray-100 dark:bg-gray-800 rounded-lg px-4 py-2 text-sm flex items-center gap-2">
                        <Loader2 className="h-4 w-4 animate-spin" />
                        <span>思考中...</span>
                      </div>
                    </div>
                  )}
                  <div ref={messagesEndRef} />
                </>
              )}
            </div>

            <div className="border-t p-4">
              {models.length > 0 && (
                <div className="flex items-center gap-2 mb-2">
                  <Select
                    value={selectedModel || undefined}
                    onChange={value => setSelectedModel(value)}
                    className="w-64"
                    options={[
                      { value: '', label: '⚡ 自动（按权重/健康度路由）' },
                      ...models.map(m => ({
                        value: m.model,
                        label: `${m.healthy === false ? '⚠️ ' : ''}${m.name !== 'default' ? `${m.name} · ${m.model}` : m.model}${m.healthy === false ? '（降级）' : ''}`,
                      })),
                    ]}
                  />
                </div>
              )}
              <div className="flex gap-2">
                <Input
                  value={input}
                  onChange={e => setInput(e.target.value)}
                  onKeyDown={e => e.key === 'Enter' && !e.shiftKey && canUseAI && convPerm !== 'view' && sendMessage(input)}
                  placeholder={!canUseAI ? '当前账号无 AI 工具执行权限（ai:tool:exec）' : convPerm === 'view' ? '此会话为只读共享，无法继续对话' : '输入你的问题...'}
                  disabled={!canUseAI || convPerm === 'view'}
                  className="flex-1"
                />
                <Button onClick={() => sendMessage(input)} disabled={!input.trim() || loading || !canUseAI || convPerm === 'view'}>
                  <Send className="h-4 w-4" />
                </Button>
              </div>
              {!canUseAI && (
                <p className="text-[10px] text-amber-600 dark:text-amber-400 flex items-center gap-1">
                  <AlertTriangle className="h-3 w-3" />无 AI 工具执行权限，仅可查看历史对话。请联系管理员在角色中授予 ai:tool:exec。
                </p>
              )}
              {canUseAI && convPerm === 'view' && (
                <p className="text-[10px] text-violet-600 dark:text-violet-400 flex items-center gap-1">
                  <Lock className="h-3 w-3" />此会话由他人共享（仅查看）。如需继续对话，请向属主申请「可编辑」权限。
                </p>
              )}

              {/* 上下文用量估算条 */}
              <div className="flex items-center gap-2 mt-2">
                <div className="flex-1 h-1 rounded-full bg-muted overflow-hidden">
                  <div
                    className={cn('h-full rounded-full transition-all', ctxPct > 85 ? 'bg-red-500' : ctxPct > 60 ? 'bg-amber-500' : 'bg-blue-500')}
                    style={{ width: `${ctxPct}%` }}
                  />
                </div>
                <p className="text-[10px] text-muted-foreground whitespace-nowrap">
                  {estTokens >= 1000 ? `≈ ${(estTokens / 1000).toFixed(1)}k tokens` : `≈ ${estTokens} tokens`}
                  {ctxPct >= 85 ? ' · 上下文偏满，建议新开对话' : ''}
                </p>
              </div>

              {/* 会话工作区(工具产物自动收集) */}
              {workspaceFiles.length > 0 && (
                <div className="mt-2 rounded-lg border border-violet-200 dark:border-violet-500/30 p-2 space-y-1">
                  <p className="text-[10px] text-muted-foreground flex items-center gap-1">
                    <Terminal className="h-3 w-3" />会话工作区（{workspaceFiles.length}）— 工具产物自动沉淀
                  </p>
                  {workspaceFiles.map(f => (
                    <div key={f.id} className="flex items-center justify-between gap-2 text-xs">
                      <span className="truncate font-mono text-[11px]">{f.file_name}</span>
                      <span className="text-[10px] text-muted-foreground shrink-0">{f.tool_name} · {(f.file_size / 1024).toFixed(1)} KB</span>
                      <span className="flex gap-2 shrink-0">
                        <button onClick={() => downloadWorkspaceFile(f.id)} className="text-[10px] text-blue-500 hover:underline">下载</button>
                        <button onClick={() => setPendingWsFileId(f.id)} className="text-[10px] text-red-400 hover:underline">删除</button>
                      </span>
                    </div>
                  ))}
                </div>
              )}

              {/* 本会话产物(对象存储归档) */}
              {artifacts.length > 0 && (
                <div className="mt-2 rounded-lg border border-border p-2 space-y-1">
                  <p className="text-[10px] text-muted-foreground flex items-center gap-1">
                    <Paperclip className="h-3 w-3" />本会话产物（{artifacts.length}）— 已归档对象存储
                  </p>
                  {artifacts.map(a => (
                    <div key={a.id} className="flex items-center justify-between gap-2 text-xs">
                      <span className="truncate font-medium">{a.file_name}</span>
                      <span className="text-[10px] text-muted-foreground shrink-0">{(a.file_size / 1024).toFixed(1)} KB</span>
                      <span className="flex gap-2 shrink-0">
                        <button onClick={() => downloadArtifact(a.id)} className="text-[10px] text-blue-500 hover:underline">下载</button>
                        <button onClick={() => setPendingArtifactId(a.id)} className="text-[10px] text-red-400 hover:underline">删除</button>
                      </span>
                    </div>
                  ))}
                </div>
              )}

              <div className="flex gap-2 mt-2">
                <p className="text-[10px] text-muted-foreground">
                  {showThinking ? '💭 思考过程已启用 — 每条回复会展示推理链' : '点击右上角 💭 可开启思考过程'}
                </p>
              </div>
            </div>
          </div>

          <div className="w-64 space-y-4 shrink-0">
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm">快捷操作</CardTitle></CardHeader>
              <CardContent className="space-y-1">
                {QUICK_ACTIONS.map(action => {
                  const Icon = action.icon
                  return (
                    <button
                      key={action.label}
                      onClick={() => sendMessage(action.prompt)}
                      className="w-full flex items-center gap-2 px-2 py-1.5 rounded text-xs text-left hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors"
                    >
                      <Icon className="h-3.5 w-3.5 text-blue-500" />
                      <span className="flex-1">{action.label}</span>
                      <ChevronRight className="h-3 w-3 text-muted-foreground" />
                    </button>
                  )
                })}
              </CardContent>
            </Card>
            <Card>
              <CardHeader className="pb-2"><CardTitle className="text-sm">当前 Agent</CardTitle></CardHeader>
              <CardContent>
                {agents.length === 0 ? (
                  <p className="text-xs text-muted-foreground">暂无可用 Agent</p>
                ) : (
                  <div className="space-y-1">
                    {agents.map((a: any) => (
                      <button
                        key={a.id}
                        onClick={() => setSelectedAgent(selectedAgent?.id === a.id ? null : a)}
                        className={cn(
                          'w-full flex items-center justify-between px-2 py-1.5 rounded text-xs text-left transition-colors',
                          selectedAgent?.id === a.id
                            ? 'bg-blue-100 dark:bg-blue-500/20 text-blue-700 dark:text-blue-300'
                            : 'hover:bg-gray-100 dark:hover:bg-gray-800',
                        )}
                      >
                        <span className="truncate flex-1">{a.name}</span>
                        <Tag color={a.status === 'active' ? 'default' : 'default'} className="text-[10px] ml-1 shrink-0">{a.status}</Tag>
                      </button>
                    ))}
                  </div>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      )}

      {/* 自主模式 */}
      {mode === 'explore' && (
        <div className="flex-1 flex flex-col min-h-0">
          {/* 目标输入区 */}
          <div className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-gray-50/60 dark:bg-[rgba(255,255,255,0.02)] p-4 mb-4">
            <div className="flex items-center gap-2 mb-2">
              <Bot className="h-4 w-4 text-blue-500" />
              <span className="text-sm font-medium">下达运维目标</span>
              <span className="text-[11px] text-muted-foreground">AI 将自主规划、调用工具、多步执行并汇报</span>
            </div>
            <div className="flex gap-2">
              <textarea
                value={exploreInput}
                onChange={e => setExploreInput(e.target.value)}
                onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleExploreSend(exploreInput) } }}
                placeholder="例如：检查所有主机的运行状态，找出异常主机并给出处理建议"
                rows={2}
                className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm resize-none placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              />
              <Button onClick={() => handleExploreSend(exploreInput)} disabled={!exploreInput.trim() || exploreStreaming} className="shrink-0 self-start">
                {exploreStreaming ? <Loader2 className="h-4 w-4 animate-spin" /> : <Rocket className="h-4 w-4" />}
                {exploreStreaming ? '执行中' : '自主执行'}
              </Button>
            </div>
            <div className="flex gap-2 mt-2 flex-wrap">
              {EXPLORE_EXAMPLES.map(ex => (
                <button
                  key={ex}
                  onClick={() => setExploreInput(ex)}
                  className="text-[11px] px-2 py-1 rounded border border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-blue-300 hover:text-blue-600 dark:hover:border-blue-500/40 transition-colors"
                >
                  {ex}
                </button>
              ))}
            </div>
          </div>

          {/* 执行过程 */}
          <div className="flex-1 overflow-y-auto space-y-4 pr-1">
            {exploreRuns.length === 0 ? (
              <div className="h-full flex items-center justify-center">
                <div className="text-center max-w-sm">
                  <Bot className="h-10 w-10 mx-auto mb-3 text-blue-500/40" />
                  <p className="text-sm text-muted-foreground">
                    输入一个目标，AI 将自动拆解计划、调用工具逐步执行，并在关键节点向你汇报。
                  </p>
                  <p className="text-xs text-muted-foreground/70 mt-2">提示：只读类任务（查询/分析/巡检）可放心交给自主模式；涉及执行类操作会触发安全审批。</p>
                </div>
              </div>
            ) : (
              exploreRuns.map(run => (
                <div key={run.id} className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] bg-white dark:bg-transparent shadow-sm">
                  {/* 目标头 */}
                  <div className="flex items-start justify-between gap-3 px-4 py-3 border-b border-gray-100 dark:border-[rgba(255,255,255,0.04)]">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground mb-1">
                        <Target className="h-3 w-3 text-blue-500" /> 目标
                      </div>
                      <p className="text-sm font-medium text-gray-800 dark:text-gray-100 whitespace-pre-wrap">{run.goal}</p>
                    </div>
                    <Tag color={run.status === 'done' ? 'green' : run.status === 'error' ? 'red' : 'blue'} className="shrink-0 text-[10px]">
                      {run.status === 'running' ? '执行中' : run.status === 'done' ? '已完成' : '异常'}
                    </Tag>
                  </div>
                  {/* 内容 */}
                  <div className="px-4 py-3">
                    {run.status === 'running' && !run.content ? (
                      <div className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Loader2 className="h-3.5 w-3.5 animate-spin text-blue-500" />
                        正在规划执行方案...
                      </div>
                    ) : (
                      <div className="text-sm text-gray-700 dark:text-[var(--foreground)] leading-relaxed" dangerouslySetInnerHTML={{ __html: renderExploreContent(run.content) }} />
                    )}
                  </div>
                </div>
              ))
            )}
            <div ref={exploreEndRef} />
          </div>
        </div>
      )}

      {/* 团队模式 */}
      {mode === 'team' && (
        <div className="flex-1 flex flex-col border rounded-lg overflow-hidden min-h-0">
          {/* 团队分段卡片选择 */}
          <div className="border-b p-3">
            <div className="flex items-center gap-2 mb-2">
              <Users className="h-4 w-4 text-blue-500" />
              <span className="text-sm font-medium">选择团队</span>
              {teamsLoading && <Loader2 className="h-3 w-3 animate-spin text-muted-foreground" />}
            </div>
            {teams.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                暂无团队，
                <button className="text-blue-600 underline" onClick={() => navigate('/ai/teams')}>前往创建</button>
              </p>
            ) : (
              <div className="flex gap-3 overflow-x-auto pb-1">
                {teams.map((team: AgentTeam) => (
                  <button
                    key={team.id}
                    onClick={() => setSelectedTeamId(team.id)}
                    className={cn(
                      'shrink-0 w-56 text-left rounded-xl border p-3 transition-colors',
                      selectedTeamId === team.id
                        ? 'border-blue-400 bg-blue-50/60 dark:bg-blue-500/10'
                        : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)] hover:border-gray-300',
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-sm font-medium truncate">{team.name}</span>
                      <Tag color={teamModeBadge[team.mode] || 'default'} className="text-[10px] shrink-0">
                        {teamModeLabel[team.mode] || team.mode}
                      </Tag>
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
          <div className="flex-1 overflow-y-auto p-4 space-y-4">
            {teamTurns.length === 0 ? (
              <div className="h-full flex items-center justify-center">
                <p className="text-sm text-muted-foreground text-center max-w-xs">
                  {selectedTeamId ? '向团队发送第一条消息，多智能体将协同作答（流式实时呈现）。' : '请先在上方选择一个团队。'}
                </p>
              </div>
            ) : (
              teamTurns.map(turn => (
                <div key={turn.id} className="space-y-3">
                  {/* 用户消息 */}
                  <div className="flex justify-end">
                    <div className="max-w-[70%] rounded-lg px-4 py-2 text-sm bg-blue-600 text-white whitespace-pre-wrap">
                      {turn.user}
                    </div>
                  </div>

                  {/* 各 agent 的流式输出 */}
                  {turn.agents.map(a => (
                    <div
                      key={`${turn.id}-${a.agent_id}`}
                      className="rounded-lg border border-gray-100 dark:border-[rgba(255,255,255,0.04)] bg-gray-50 dark:bg-[rgba(255,255,255,0.02)] p-3"
                    >
                      <div className="flex items-center gap-2 mb-1.5">
                        <Tag
                          color={a.status === 'error' ? 'red' : a.status === 'done' ? 'green' : 'blue'}
                        >
                          {teamRoleLabel[a.role] || a.role} · {a.agent_name}
                        </Tag>
                        {a.status === 'streaming' && <Loader2 className="h-3 w-3 animate-spin text-blue-500" />}
                        {a.status === 'done' && <span className="text-[10px] text-blue-500">✓ 完成</span>}
                      </div>

                      {/* 工具调用过程（可观测面板） */}
                      {a.toolCalls.length > 0 && (
                        <details className="mb-1.5" open>
                          <summary className="text-xs text-muted-foreground cursor-pointer hover:text-foreground">
                            🔧 工具调用 ({a.toolCalls.length})
                          </summary>
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

                      {/* 主管审核备注 */}
                      {a.supervisorNote && (
                        <details className="mb-1.5" open>
                          <summary className="text-xs text-purple-600 dark:text-purple-400 cursor-pointer hover:underline">📋 主管审核</summary>
                          <div className="mt-1 p-2 rounded border border-purple-200 dark:border-purple-500/30 bg-purple-50 dark:bg-purple-500/10 text-xs whitespace-pre-wrap">
                            {a.supervisorNote}
                          </div>
                        </details>
                      )}

                      {a.status === 'error' ? (
                        <p className="text-xs text-red-500 whitespace-pre-wrap">{a.error}</p>
                      ) : (
                        <div
                          className="text-xs text-gray-700 dark:text-[var(--foreground)] whitespace-pre-wrap"
                          dangerouslySetInnerHTML={{ __html: renderMarkdown(a.text) }}
                        />
                      )}
                    </div>
                  ))}

                  {/* 团队最终答复 */}
                  {turn.final && (
                    <div className="flex gap-3 justify-start">
                      <div className="h-8 w-8 rounded-full bg-blue-100 dark:bg-blue-500/20 flex items-center justify-center shrink-0">
                        <Bot className="h-4 w-4 text-blue-600 dark:text-blue-400" />
                      </div>
                      <div className="max-w-[80%] rounded-lg px-4 py-2 text-sm bg-gray-100 dark:bg-gray-800 prose-sm max-w-none">
                        <Tag color="green" className="mb-1.5 text-[10px]">团队最终答复</Tag>
                        <div dangerouslySetInnerHTML={{ __html: renderMarkdown(turn.final) }} />
                      </div>
                    </div>
                  )}

                  {/* 团队级错误（无 agent 维度） */}
                  {turn.error && !turn.agents.some(a => a.status === 'error') && (
                    <p className="text-xs text-red-500">⚠ {turn.error}</p>
                  )}
                </div>
              ))
            )}
            <div ref={teamMessagesEndRef} />
          </div>

          {/* 输入区 */}
          <div className="border-t p-4">
            <div className="flex gap-2">
              <Input
                value={teamInput}
                onChange={e => setTeamInput(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && !e.shiftKey && handleTeamSend(teamInput)}
                placeholder={selectedTeamId ? '向团队发送消息...' : '请先选择上方团队'}
                className="flex-1"
                disabled={!selectedTeamId || teamStreaming}
              />
              <Button onClick={() => handleTeamSend(teamInput)} disabled={!selectedTeamId || !teamInput.trim() || teamStreaming}>
                <Send className="h-4 w-4" />
              </Button>
            </div>
            <div className="flex items-center justify-between mt-2">
              <Button size="middle" onClick={() => navigate('/ai/teams')}>
                <Settings className="h-4 w-4 mr-1" />管理团队
              </Button>
              {teamStreaming && (
                <span className="text-xs text-muted-foreground flex items-center gap-1">
                  <Loader2 className="h-3 w-3 animate-spin" />团队响应中...
                </span>
              )}
            </div>
          </div>
        </div>
      )}

      {/* 敏感操作审批弹窗 */}
      <Modal open={!!pendingApproval} onCancel={() => { if (!approvalBusy) setPendingApproval(null) }} title="🔐 敏感操作确认" footer={
        <Space>
          <Button onClick={() => handleApprovalRespond('reject')} disabled={approvalBusy} danger>
            <XCircle className="h-4 w-4 mr-1" />拒绝
          </Button>
          <Button type="primary" onClick={() => handleApprovalRespond('approve')} disabled={approvalBusy}>
            {approvalBusy ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <CheckCircle className="h-4 w-4 mr-1" />}
            批准执行
          </Button>
        </Space>
      }>
        {pendingApproval && (
          <div className="space-y-3">
            <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-amber-500/5 border border-amber-500/10' : 'bg-amber-50 border border-amber-200')}>
              <AlertTriangle className="h-4 w-4 text-amber-500 shrink-0" />
              <span>AI 请求执行敏感操作，请确认后放行。超时未响应将自动拒绝。</span>
            </div>
            <div className="grid gap-2 text-sm">
              <div><span className="text-muted-foreground text-xs">操作：</span>
                <code className="font-mono text-xs bg-gray-100 dark:bg-gray-800 rounded px-1.5 py-0.5">{pendingApproval.tool}</code>
              </div>
              <div><span className="text-muted-foreground text-xs">说明：</span><span className="text-sm">{pendingApproval.summary || '无'}</span></div>
              {pendingApproval.args && pendingApproval.args !== '{}' && (
                <div>
                  <span className="text-muted-foreground text-xs">参数：</span>
                  <pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-28 overflow-y-auto">{pendingApproval.args}</pre>
                </div>
              )}
              <div className="text-[11px] text-muted-foreground">
                截止：{pendingApproval.deadline ? new Date(pendingApproval.deadline).toLocaleTimeString('zh-CN') : '-'}
              </div>
            </div>
          </div>
        )}
      </Modal>

      {/* 会话共享弹窗 */}
      <Modal open={!!shareConv} onCancel={() => setShareConv(null)} title={`分享会话：${shareConv?.title || ''}`} footer={
        <Space>
          <Button onClick={() => setShareConv(null)}>取消</Button>
          <Button type="primary" onClick={saveShare}><Share2 className="h-4 w-4 mr-1" />共享</Button>
        </Space>
      }>
        <div className="space-y-3 py-1">
          <p className="text-xs text-muted-foreground">默认对话仅自己可见；共享后指定用户/组成员可见。目标 ID 为用户名或用户组 ID。</p>
          <div className="grid grid-cols-2 gap-2">
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">目标类型</p>
              <Select
                value={shareForm.target_type}
                onChange={value => setShareForm({ ...shareForm, target_type: value })}
                options={[
                  { value: 'user', label: '用户' },
                  { value: 'group', label: '用户组' },
                ]}
                className="w-full"
              />
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">权限</p>
              <Select
                value={shareForm.permission}
                onChange={value => setShareForm({ ...shareForm, permission: value })}
                options={[
                  { value: 'view', label: '仅查看' },
                  { value: 'edit', label: '可继续对话' },
                ]}
                className="w-full"
              />
            </div>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">目标 ID（用户名 / 用户组 ID）</p>
            <Input className="h-9 text-xs font-mono" placeholder="如: zhangsan 或 group-001" value={shareForm.target_id} onChange={e => setShareForm({ ...shareForm, target_id: e.target.value })} />
          </div>
        </div>
      </Modal>

      {/* 删除确认弹窗 */}
      <Popconfirm
        title="删除会话"
        description="删除该会话？此操作不可恢复。"
        onConfirm={handleDeleteConversation}
        okText="删除"
        cancelText="取消"
        open={!!pendingConvId}
        onCancel={() => setPendingConvId(null)}
      >
        <span />
      </Popconfirm>
      <Popconfirm
        title="删除产物文件"
        description="删除该产物文件？"
        onConfirm={deleteArtifact}
        okText="删除"
        cancelText="取消"
        open={!!pendingArtifactId}
        onCancel={() => setPendingArtifactId(null)}
      >
        <span />
      </Popconfirm>
      <Popconfirm
        title="删除工作区文件"
        description="删除该工作区文件？"
        onConfirm={deleteWorkspaceFile}
        okText="删除"
        cancelText="取消"
        open={!!pendingWsFileId}
        onCancel={() => setPendingWsFileId(null)}
      >
        <span />
      </Popconfirm>
    </div>
  )
}
