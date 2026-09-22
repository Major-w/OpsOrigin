
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useChatStore, useAgentAIStore } from '@/stores/ai'
import { useKnowledgeStore } from '@/stores/knowledge'
import { useCan } from '@/stores/permissions'
import { useAIChat, ChatMessageList, ChatInputBar, TeamChatView, ChatContextBar, ConversationList, estimateContextTokens, AttachmentChip, extractAttachmentsFromParts, type ChatMessage, type ChatAttachmentMeta, type ConversationItem } from '@/components/ai/chat-kernel'
import { Button, Input, Select, Space, Modal, Popconfirm } from 'antd'
import {
  Sparkles, History, Settings, BarChart3, Share2, Lock, PanelLeftClose, PanelLeftOpen,
  AlertTriangle, XCircle, CheckCircle, Loader2, Brain, Bookmark, Download, Save, Terminal, Paperclip,
  FileDown, Server, FileBarChart, Bug, ClipboardList, BookOpen, Database, Bell,
  Maximize2, Minimize2, Columns3,
} from 'lucide-react'
import { exportDocFile } from '@/lib/doc-export'
import { cn, apiFetch, apiFetchRaw } from '@/lib/utils'
import { exportSessionMarkdown } from '@/lib/export-session'
import { kbArticleIDsFromCitations } from '@/components/ai/cite'
import { useThemeStore } from '@/stores/theme'
import { getMessage } from '@/lib/message'
import { t, useLocale } from '@/lib/i18n'

// ============================================================================
// AI 助手（全页 /ai）
// 复用 chat-kernel 的 useAIChat / ChatMessageList / ChatInputBar / TeamChatView,
// 保留本页独有的: 左侧会话列表(可收起) / 分享 / 删除 / 会话产物(对象存储) /
// 工作区(工具产物) / 上下文用量条 / 只读共享权限门禁 / 记忆·知识库·下载·归档 消息操作。
// ============================================================================

export default function AIPage() {
  const loadConversations = useChatStore(s => s.loadConversations)
  const submitFB = useChatStore(s => s.submitFeedback)
  const fetchAgents = useAgentAIStore(s => s.fetchAgents)
  const { theme } = useThemeStore()
  const navigate = useNavigate()
  const canUseAI = useCan('ai:tool:exec')
  const locale = useLocale()

  const QUICK_ACTIONS = useMemo(() => [
    { label: t('ai.quickActions.queryHost'), icon: Server, desc: t('ai.quickActions.queryHostDesc'), prompt: t('ai.quickActions.queryHostPrompt') },
    { label: t('ai.quickActions.inspection'), icon: ClipboardList, desc: t('ai.quickActions.inspectionDesc'), prompt: t('ai.quickActions.inspectionPrompt') },
    { label: t('ai.quickActions.report'), icon: FileBarChart, desc: t('ai.quickActions.reportDesc'), prompt: t('ai.quickActions.reportPrompt') },
    { label: t('ai.quickActions.troubleshoot'), icon: Bug, desc: t('ai.quickActions.troubleshootDesc'), prompt: t('ai.quickActions.troubleshootPrompt') },
    { label: t('ai.quickActions.alertAnalysis'), icon: Bell, desc: t('ai.quickActions.alertAnalysisDesc'), prompt: t('ai.quickActions.alertAnalysisPrompt') },
    { label: t('ai.quickActions.runbook'), icon: BookOpen, desc: t('ai.quickActions.runbookDesc'), prompt: t('ai.quickActions.runbookPrompt') },
    { label: t('ai.quickActions.sqlOptimize'), icon: Database, desc: t('ai.quickActions.sqlOptimizeDesc'), prompt: t('ai.quickActions.sqlOptimizePrompt') },
    { label: t('ai.quickActions.capacityPredict'), icon: BarChart3, desc: t('ai.quickActions.capacityPredictDesc'), prompt: t('ai.quickActions.capacityPredictPrompt') },
  ], [locale])

  const WELCOME_MESSAGE = useMemo(() => t('ai.welcome'), [locale])

  // 历史压缩: 最近 20 条, 超预算从最新往前裁剪(与 chat-kernel 默认一致, 这里保留 HIST_BUDGET 行为)
  const buildHistory = (msgs: ChatMessage[]): { role: string; content: string }[] => {
    const base = msgs
      .filter(m => m.id !== '0' && (m.role === 'user' || m.role === 'assistant'))
      .slice(-20)
      .map(m => ({ role: m.role, content: m.content }))
    const HIST_BUDGET = 24000
    let histTokens = base.reduce((n, m) => n + Math.ceil((m.content?.length || 0) / 2), 0)
    if (histTokens > HIST_BUDGET && base.length > 4) {
      const trimmed: typeof base = []
      let acc = 0
      for (let i = base.length - 1; i >= 0; i--) {
        const t = Math.ceil((base[i].content?.length || 0) / 2)
        if (acc + t > HIST_BUDGET && trimmed.length > 0) break
        trimmed.unshift(base[i])
        acc += t
      }
      if (base.length - trimmed.length > 0) {
        return [{ role: 'assistant', content: `⚠️ 上下文过长，更早的 ${base.length - trimmed.length} 条消息已自动省略。` }, ...trimmed]
      }
    }
    return base
  }

  const chat = useAIChat({
    welcome: WELCOME_MESSAGE,
    welcomeId: '0',
    // 跨路由切换续接: 消息与后端会话 ID 落 localStorage, 导航离开再回来不丢对话
    persistKey: 'ai.page.messages',
    convIdKey: 'ai.page.convId',
    enableTeam: true,
    captureToolCalls: true,
    // 启用 abort: 流式输出可随时「停止生成」(ChatInputBar 的方块按钮调 stopGeneration)
    enableAbort: true,
    resetConvOn403: false,
    buildHistory,
    defaultMode: 'normal',
    enableKnowledgeScope: true,
  })

  const {
    mode, setMode, executor, setExecutor, selectedModel, setSelectedModel,
    selectedAgent, setSelectedAgent, models, agents, messages, setMessages, input, setInput,
    streaming, send, stopGeneration, regenerate, editAndResend, clearSession, pending, approving, respond, dismissApproval, convId, setConvId,
    selectedTeamId, setSelectedTeamId, teamTurns, teamInput, setTeamInput, teamStreaming, handleTeamSend,
    teams, teamsLoading,
    knowledgeCategories, setKnowledgeCategories,
  } = chat

  // 知识范围选择器选项(挂载时加载分类列表)
  const knowledgeCategoryOptions = useKnowledgeStore(s => s.categories)
  useEffect(() => { useKnowledgeStore.getState().fetchCategories() }, [])

  // —— 本页独有状态 ——
  const [showHistory, setShowHistory] = useState(false)
  const [conversations, setConversations] = useState<ConversationItem[]>([])
  const [sidebarOpen, setSidebarOpen] = useState<boolean>(() => {
    if (typeof window === 'undefined') return true
    const stored = localStorage.getItem('ai.sidebarOpen')
    if (stored !== null) return stored !== 'false'
    // 无历史偏好时: 窄屏默认收起(抽屉), 桌面默认展开
    return !window.matchMedia('(max-width: 767px)').matches
  })
  const [convPerm, setConvPerm] = useState<'owner' | 'edit' | 'view' | ''>('')
  const [shareConv, setShareConv] = useState<{ id: string; title: string } | null>(null)
  const [shareForm, setShareForm] = useState({ target_type: 'user', target_id: '', permission: 'view' })
  const [artifacts, setArtifacts] = useState<{ id: string; message_id: string; file_name: string; file_type: string; file_size: number; created_at: string }[]>([])
  const [workspaceFiles, setWorkspaceFiles] = useState<{ id: string; tool_name: string; file_name: string; file_size: number; created_at: string }[]>([])
  const [pendingConvId, setPendingConvId] = useState<string | null>(null)
  const [pendingArtifactId, setPendingArtifactId] = useState<string | null>(null)
  const [pendingWsFileId, setPendingWsFileId] = useState<string | null>(null)

  // —— 全屏: CSS 铺满隐藏后台布局(侧栏/面包屑) + 整个文档进入原生全屏(隐藏浏览器 chrome)。
  // 原生全屏必须对 documentElement 调用: 若只对容器调用, antd 弹层(Modal/Popconfirm 挂在 body)会
  // 落在全屏元素之外而不可见, 分享/删除等弹窗会"点了没反应"。
  const shellRef = useRef<HTMLDivElement>(null)
  const [nativeFs, setNativeFs] = useState(false)
  const [cssFs, setCssFs] = useState(false)
  const isFullscreen = nativeFs || cssFs

  const toggleFullscreen = async () => {
    if (isFullscreen) {
      if (document.fullscreenElement) { try { await document.exitFullscreen() } catch { /* 忽略 */ } }
      setCssFs(false)
      return
    }
    setCssFs(true)
    // 原生全屏需用户手势触发; 不支持/被拒时静默降级为纯 CSS 铺满(仍占满视口)
    try {
      if (document.documentElement.requestFullscreen) await document.documentElement.requestFullscreen()
    } catch { /* 降级: 仅 CSS 铺满 */ }
  }

  // 原生全屏状态同步: 浏览器 Esc / F11 退出时同步收起 CSS 铺满, 一次退出即回到常规视图
  useEffect(() => {
    const onChange = () => {
      const active = !!document.fullscreenElement
      setNativeFs(active)
      if (!active) setCssFs(false)
    }
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])
  // 纯 CSS 铺满(原生不可用时)的 Esc 退出
  useEffect(() => {
    if (!cssFs || nativeFs) return
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setCssFs(false) }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [cssFs, nativeFs])
  // 卸载时兜底退出原生全屏, 避免路由离开后仍停留在全屏
  useEffect(() => () => { if (document.fullscreenElement) document.exitFullscreen().catch(() => {}) }, [])

  // —— 对话附件: 待发送列表 + 上传中计数(上传完成即入列, 发送时随消息附带) ——
  const [pendingFiles, setPendingFiles] = useState<ChatAttachmentMeta[]>([])
  const [uploadingCount, setUploadingCount] = useState(0)

  // 健康模型下拉(失败换模型重试): useMemo 稳定引用, 避免每帧新数组击穿 MessageItem 的 memo
  const healthyModelOptions = useMemo(
    () => models.filter(m => m.healthy !== false).map(m => ({ value: m.model, label: m.name || m.model })),
    [models],
  )
  // 窄屏抽屉式侧栏: 选中/新建会话后自动收起, 不遮挡对话区
  const closeSidebarOnNarrow = () => {
    if (typeof window !== 'undefined' && window.matchMedia('(max: 767px)').matches) setSidebarOpen(false)
  }
  // 切换运行级模型: 选中推理模型时提示「首答前先深度思考, 较慢但质量更高」,
  // 直接缓解此前「AI 对话慢」的体感误判——用户能主动区分推理/极速模型。
  const handleModelChange = (m: string) => {
    setSelectedModel(m)
    if (!m) return
    const info = models.find(x => x.model === m)
    if (info?.reasoning) {
      getMessage().info('已选推理模型：首答前会先深度思考（较慢），质量更高；追求极速可切回「自动」或极速模型')
    }
  }

  const handleFiles = async (files: File[]) => {
    for (const f of files.slice(0, 5)) { // 单次最多 5 个
      if (f.size > 20 * 1024 * 1024) {
        getMessage().error(`「${f.name}」超过 20MB 上限`)
        continue
      }
      setUploadingCount(n => n + 1)
      try {
        const fd = new FormData()
        fd.append('file', f)
        const resp = await apiFetchRaw('/ai/attachments', { method: 'POST', body: fd })
        if (!resp.ok) throw new Error(`上传失败(${resp.status})`)
        const meta = await resp.json()
        setPendingFiles(prev => [...prev, { id: meta.id, name: meta.file_name, type: meta.file_type, size: meta.file_size }])
      } catch (e: any) {
        getMessage().error(`「${f.name}」${e.message}`)
      } finally {
        setUploadingCount(n => n - 1)
      }
    }
  }

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const teamMessagesEndRef = useRef<HTMLDivElement>(null)
  // 页面级快捷键与拖拽上传需要触达输入框
  const inputTaRef = useRef<HTMLTextAreaElement | null>(null)
  const [dragActive, setDragActive] = useState(false)

  // 键盘快捷键: Cmd/Ctrl+K 聚焦输入框 / Esc 停止生成(编辑框内 Esc 由编辑器自处理, 此处跳过) /
  // 输入框为空时 ↑/↓ 在历史提问里前后翻(终端习惯, 便于快速修改重发)
  // 历史提问栈(发送顺序): ↑ 往更早翻, ↓ 往更新翻, 翻回起点为空输入
  const userPrompts = useMemo(
    () => messages.filter(m => m.role === 'user' && m.content).map(m => m.content),
    [messages],
  )
  const histIdxRef = useRef(-1) // -1=未翻历史; 0=最近一条, 依次更早
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        inputTaRef.current?.focus()
        return
      }
      const inEditor = (e.target as HTMLElement | null)?.closest?.('textarea, input, [contenteditable="true"]')
      if (e.key === 'Escape' && streaming && !inEditor) { stopGeneration(); return }
      if (e.target !== inputTaRef.current || streaming || userPrompts.length === 0) return
      if (e.key === 'ArrowUp') {
        // 多行内容且光标不在首行时不劫持(保留正常光标移动)
        const ta = inputTaRef.current
        if (ta && ta.selectionStart > 0) return
        const next = histIdxRef.current + 1
        if (next >= userPrompts.length) return
        histIdxRef.current = next
        e.preventDefault()
        setInput(userPrompts[userPrompts.length - 1 - next])
      } else if (e.key === 'ArrowDown') {
        if (histIdxRef.current < 0) return
        const next = histIdxRef.current - 1
        histIdxRef.current = next
        e.preventDefault()
        setInput(next < 0 ? '' : userPrompts[userPrompts.length - 1 - next])
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [streaming, stopGeneration, setInput, userPrompts])

  // 会话级导出: 共享导出逻辑(lib/export-session.ts), /ai 与浮窗/纯净模式三入口行为一致
  const exportSessionMd = () => {
    if (!exportSessionMarkdown(messages)) { getMessage().info('当前会话没有可导出的消息'); return }
    getMessage().success('已导出当前会话')
  }

  useEffect(() => {
    if (mode === 'normal') loadConversations(selectedAgent?.id).then(setConversations)
    // eslint-disable-next-line react-hooks/exhaustive-deps -- loadConversations 引用稳定, 仅在 mode/agent 切换时触发
  }, [mode, selectedAgent])

  // 一轮流式结束后延迟刷新会话列表: 新会话首轮完成后端正用 LLM 生成自动标题(约 1~3 秒)
  const prevStreamingRef = useRef(false)
  useEffect(() => {
    const was = prevStreamingRef.current
    prevStreamingRef.current = streaming
    if (was && !streaming && mode === 'normal' && convId) {
      const t = setTimeout(() => { loadConversations(selectedAgent?.id).then(setConversations) }, 3000)
      return () => clearTimeout(t)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅监听 streaming 变化, 其他引用在闭包内保持最新
  }, [streaming])
  // 滚动已由 ChatMessageList/TeamChatView 内部 useStickToBottom 接管(跟随流式 + 回到底部),
  // 这里不再 messages 一变就强滚(翻历史会被硬拽回底的糟糕体验)
  useEffect(() => { loadArtifacts(convId); loadWorkspace(convId) }, [convId])

  // ----- 会话产物 / 工作区 -----
  const loadArtifacts = async (id: string) => {
    if (!id) { setArtifacts([]); return }
    try {
      const r = await apiFetchRaw(`/ai/conversations/${id}/artifacts`)
      if (r.ok) setArtifacts(await r.json())
    } catch {}
  }
  const loadWorkspace = async (id: string) => {
    if (!id) { setWorkspaceFiles([]); return }
    try {
      const r = await apiFetchRaw(`/ai/conversations/${id}/workspace`)
      if (r.ok) setWorkspaceFiles(await r.json())
    } catch {}
  }

  // ----- 会话列表操作 -----
  const handleOpenHistory = async () => {
    if (!showHistory) {
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    }
    setShowHistory(!showHistory)
  }
  const handleLoadConversation = async (id: string) => {
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${id}`)
      if (!resp.ok) return
      const conv = await resp.json()
      setConvPerm(conv?.permission === 'view' ? 'view' : conv?.permission === 'edit' ? 'edit' : conv?.permission === 'owner' ? 'owner' : '')
      if (conv?.messages) {
        // 只还原真实对话(user/assistant): 过滤 system/tool 等内部轮次(后端会话里持久化了
        // 工具调用 JSON 等执行轨迹, 直接渲染会变成"后台逻辑泄漏")。附件从持久化的 content_parts
        // (attachment://<id>#<name> 引用)还原, 不再依赖正文里的 [附件] 标记行。
        const msgs: ChatMessage[] = conv.messages
          .filter((m: any) => m.role === 'user' || m.role === 'assistant')
          .filter((m: any) => m.role !== 'assistant' || (m.content || '').trim() || (m.content_parts && m.content_parts.length > 0))
          .map((m: any, i: number) => {
            const atts = extractAttachmentsFromParts(m.content_parts)
            const msg: ChatMessage = { id: `${id}-${i}`, role: m.role, content: m.content || '', timestamp: Date.now() }
            // 截断标志随消息持久化(后端 llm.Message.Truncated): 历史回放同样展示「输出不完整」提示
            if (m.role === 'assistant' && m.truncated) msg.truncated = '该回复生成时被中断（页面关闭/连接断开/长度上限），内容可能不完整'
            if (atts.length) msg.attachments = atts
            return msg
          })
        setMessages(msgs)
      }
      setConvId(id)
      setShowHistory(false)
    } catch {}
  }
  const handleNewConversation = () => {
    clearSession()
    setConvPerm('')
    setShowHistory(false)
  }
  const handleDeleteConversation = async () => {
    const id = pendingConvId
    if (!id) return
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${id}`, { method: 'DELETE' })
      if (!resp.ok) throw new Error(`删除失败(${resp.status})`)
      getMessage().success('会话已删除')
      if (id === convId) {
        clearSession()
        setConvPerm('')
      }
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
    } catch (e: any) {
      getMessage().error('删除失败: ' + e.message)
    }
    setPendingConvId(null)
  }

  // ----- 消息操作: 反馈 / 记忆 / 知识库 / 下载 / 归档 -----
  const submitFeedback = async (msg: ChatMessage, rating: 'up' | 'down' = 'up') => {
    // 引用文章透传(检索反馈闭环): 赞踩落到引用文章, 检索侧据此降权
    const cited = kbArticleIDsFromCitations(msg.citations)
    await submitFB({ message_id: msg.messageId || msg.id, rating, conversation_id: convId || undefined, ...(cited.length ? { cited_articles: cited } : {}) })
  }
  const saveMsgToMemory = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const idx = messages.findIndex(m => m.id === msg.id)
    let problem = '对话要点'
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { problem = messages[i].content.replace(/\s+/g, ' ').slice(0, 40) || '对话要点'; break }
    }
    try {
      const r = await apiFetchRaw('/ai/memories', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ agent_id: selectedAgent?.id || '', category: '对话要点', problem, resolution: msg.content.slice(0, 500) }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已记住（长期记忆，重启不丢）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }
  const saveMsgToKnowledge = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const title = msg.content.replace(/[#*`>\-\n\r]+/g, ' ').replace(/\s+/g, ' ').trim().slice(0, 40) || 'AI 对话沉淀'
    try {
      // 走 apiFetchRaw: 自动附带 Authorization / X-Principal 等鉴权头(此前裸 fetch 缺 token → 401)
      const r = await apiFetchRaw('/knowledge', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title, content: msg.content, category: 'AI 对话沉淀', tags: ['ai', 'conversation'], status: 'published' }),
      })
      if (!r.ok) throw new Error(`保存失败(${r.status})`)
      getMessage().success('已保存到知识库（分类「AI 对话沉淀」）')
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }
  const downloadMsgAsMd = (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    const blob = new Blob([msg.content], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `ai-${msg.id}.md`
    a.click()
    URL.revokeObjectURL(url)
  }
  const saveMsgAsArtifact = async (msg: ChatMessage) => {
    if (!msg.content.trim()) return
    if (!convId) { getMessage().error('请先新建或选择对话会话'); return }
    try {
      const r = await apiFetchRaw(`/ai/conversations/${convId}/artifacts`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
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
      await apiFetchRaw(`/ai/conversations/${convId}/artifacts/${aid}`, { method: 'DELETE' })
      getMessage().success('已删除')
      loadArtifacts(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingArtifactId(null)
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
      await apiFetchRaw(`/ai/conversations/${convId}/workspace/${fid}`, { method: 'DELETE' })
      getMessage().success('已删除')
      loadWorkspace(convId)
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingWsFileId(null)
  }

  const retryMessage = (errMsg: ChatMessage, modelOverride?: string) => {
    const idx = messages.findIndex(m => m.id === errMsg.id)
    if (idx < 0) return
    for (let i = idx - 1; i >= 0; i--) {
      if (messages[i].role === 'user') { send(messages[i].content, undefined, modelOverride); return }
    }
  }

  // 失败卡片内联换模型重试: 同步顶部选中模型, 并以该模型重发(不经 selectedModel 闭包, 避免拿到旧值)
  const retryWithModel = (errMsg: ChatMessage, model: string) => {
    if (!model) return
    setSelectedModel(model)
    retryMessage(errMsg, model)
  }

  // 会话重命名(ConversationList 双击标题): PATCH 后刷新列表
  const handleRenameConversation = async (id: string, title: string) => {
    try {
      const resp = await apiFetchRaw(`/ai/conversations/${id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title }),
      })
      if (!resp.ok) throw new Error(`重命名失败(${resp.status})`)
      const convs = await loadConversations(selectedAgent?.id)
      setConversations(convs)
      return true
    } catch (e: any) {
      getMessage().error(e.message || '重命名失败')
      return false
    }
  }

  const saveShare = async () => {
    if (!shareConv || !shareForm.target_id.trim()) { getMessage().error('请填写目标用户/组 ID'); return }
    try {
      const r = await apiFetchRaw(`/ai/conversations/${shareConv.id}/shares`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(shareForm),
      })
      if (!r.ok) throw new Error(`共享失败(${r.status})`)
      getMessage().success(shareForm.permission === 'edit' ? '已共享（可继续对话）' : '已共享（仅查看）')
      setShareConv(null)
      setShareForm({ target_type: 'user', target_id: '', permission: 'view' })
    } catch (e: any) { getMessage().error('共享失败: ' + e.message) }
  }

  // 上下文用量估算
  const memoizedCtxTokens = useMemo(() => estimateContextTokens(messages), [messages])
  const ctxPct = Math.min(100, Math.round((memoizedCtxTokens / 32000) * 100))

  // 消息导出 docx(纯前端转换, loading 态 + 防重)
  const [exportingMsg, setExportingMsg] = useState<string | null>(null)
  const exportMsgAs = async (msg: ChatMessage, fmt: 'docx') => {
    if (exportingMsg) return
    const k = `${msg.id}:${fmt}`
    setExportingMsg(k)
    try {
      await exportDocFile(msg.content, `ai-${msg.id}`, fmt, `msg:${msg.id}`)
    } finally {
      setExportingMsg(null)
    }
  }

  // 消息额外操作(记忆 / 知识库 / 下载 / 归档)
  const renderMsgActions = (msg: ChatMessage) => (
    <>
      <button onClick={() => saveMsgToMemory(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-violet-500 transition-colors" title="记住（存长期记忆，跨会话复用）">
        <Brain className="h-3 w-3" />
      </button>
      <button onClick={() => saveMsgToKnowledge(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-amber-500 transition-colors" title="保存到知识库">
        <Bookmark className="h-3 w-3" />
      </button>
      <button onClick={() => exportMsgAs(msg, 'docx')} disabled={!!exportingMsg} className="p-1 rounded text-[10px] text-muted-foreground hover:text-blue-500 transition-colors disabled:opacity-50" title="导出 Word (.docx)">
        {exportingMsg === `${msg.id}:docx` ? <Loader2 className="h-3 w-3 animate-spin" /> : <FileDown className="h-3 w-3" />}
      </button>
      <button onClick={() => downloadMsgAsMd(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-blue-500 transition-colors" title="下载为 Markdown">
        <Download className="h-3 w-3" />
      </button>
      <button onClick={() => saveMsgAsArtifact(msg)} className="p-1 rounded text-[10px] text-muted-foreground hover:text-indigo-500 transition-colors" title="归档到对象存储">
        <Save className="h-3 w-3" />
      </button>
    </>
  )

  // 输入区底部: 上下文用量 + 工作区 + 产物
  const inputFooter = (
    <>
      <div className="flex items-center gap-2">
        <div className="flex-1 h-1 rounded-full bg-muted overflow-hidden">
          <div className={cn('h-full rounded-full transition-all', ctxPct > 85 ? 'bg-red-500' : ctxPct > 60 ? 'bg-amber-500' : 'bg-blue-500')} style={{ width: `${ctxPct}%` }} />
        </div>
        <p className="text-[10px] text-muted-foreground whitespace-nowrap">
          {memoizedCtxTokens >= 1000 ? `≈ ${(memoizedCtxTokens / 1000).toFixed(1)}k tokens` : `≈ ${memoizedCtxTokens} tokens`}
          {ctxPct >= 85 ? ' · 上下文偏满，建议新开对话' : ''}
        </p>
      </div>
      {workspaceFiles.length > 0 && (
        <div className="rounded-lg border border-violet-200 dark:border-violet-500/30 p-2 space-y-1">
          <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Terminal className="h-3 w-3" />会话工作区（{workspaceFiles.length}）— 工具产物自动沉淀</p>
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
      {artifacts.length > 0 && (
        <div className="rounded-lg border border-border p-2 space-y-1">
          <p className="text-[10px] text-muted-foreground flex items-center gap-1"><Paperclip className="h-3 w-3" />本会话产物（{artifacts.length}）— 已归档对象存储</p>
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
    </>
  )

  return (
    // 高度对齐 layout.tsx 顶部面包屑栏(px-6 py-3 + 一行文字 + border ≈ 45px):
    // 页面正好填满面包屑以下视口, 输入区不被挤出屏幕, 对话区拿到最大垂直空间。
    // 全屏时改为铺满视口(原生 Fullscreen 用 h-screen; 降级模式用 fixed 覆盖后台布局)。
    <div
      ref={shellRef}
      className={cn(
        'p-3 md:p-6 flex flex-col bg-background',
        cssFs ? 'fixed inset-0 z-[60]' : nativeFs ? 'h-screen' : 'h-[calc(100vh-45px)]',
      )}
    >
      <div className="flex items-center justify-between mb-3 gap-2">
        <div className="min-w-0">
          <h1 className="text-xl font-bold flex items-center gap-2"><Sparkles className="h-5 w-5 text-blue-500 shrink-0" />{t('ai.assistant')}</h1>
        </div>
        {/* 窄屏按钮只留图标(title 提示), 文字标签 sm 以上显示 */}
        <div className="flex items-center gap-2 shrink-0">
          {mode === 'normal' && (<Button size="middle" onClick={() => navigate('/ai/analytics')} title="分析"><BarChart3 className="h-4 w-4" /><span className="max-sm:hidden">分析</span></Button>)}
          {mode === 'normal' && (<Button size="middle" onClick={exportSessionMd} title="将当前会话导出为 Markdown"><Download className="h-4 w-4" /><span className="max-sm:hidden">导出会话</span></Button>)}
          <Button size="middle" onClick={toggleFullscreen} title={isFullscreen ? '退出全屏（Esc）' : '全屏对话'}>
            {isFullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
            <span className="max-sm:hidden">{isFullscreen ? '退出全屏' : '全屏'}</span>
          </Button>
          <Button size="middle" onClick={() => navigate('/ai/models')} title="模型"><Brain className="h-4 w-4" /><span className="max-sm:hidden">模型</span></Button>
          <Button size="middle" onClick={() => navigate('/ai/settings')} title="设置"><Settings className="h-4 w-4" /><span className="max-sm:hidden">设置</span></Button>
        </div>
      </div>

      {/* 普通 / 自主 模式：左会话列表(可收起) + 全宽对话区。
          窄屏(<md)侧栏变绝对定位抽屉覆盖在对话区上, 不挤压聊天主区; md 以上恢复常规左栏 */}
      {mode !== 'team' && (
        <div className="flex-1 flex gap-4 min-h-0 relative">
          {/* 移动端侧栏遮罩层 */}
          {sidebarOpen && (
            <div
              className="hidden max-md:block fixed inset-0 bg-black/40 z-20"
              onClick={() => { setSidebarOpen(false); localStorage.setItem('ai.sidebarOpen', 'false') }}
            />
          )}
          {sidebarOpen ? (
            <div className="w-64 shrink-0 border rounded-lg overflow-hidden flex flex-col max-md:absolute max-md:inset-y-0 max-md:left-0 max-md:z-30 max-md:shadow-xl max-md:bg-background">
              <div className="p-3 border-b flex items-center justify-between">
                <span className="text-sm font-medium flex items-center gap-1.5"><History className="h-4 w-4 text-blue-500" />对话列表</span>
                <button onClick={() => { setSidebarOpen(false); localStorage.setItem('ai.sidebarOpen', 'false') }} className="p-1 rounded text-muted-foreground hover:text-foreground hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors" title="收起会话列表">
                  <PanelLeftClose className="h-4 w-4" />
                </button>
              </div>
              <div className="flex-1 overflow-y-auto p-2 space-y-1">
                <ConversationList
                  conversations={conversations}
                  activeId={convId}
                  onSelect={(id) => { handleLoadConversation(id); closeSidebarOnNarrow() }}
                  onNew={() => { handleNewConversation(); closeSidebarOnNarrow() }}
                  onDelete={(id) => setPendingConvId(id)}
                  onRename={handleRenameConversation}
                  onShare={(c) => { setShareConv({ id: c.id, title: c.title || '新对话' }); setShareForm({ target_type: 'user', target_id: '', permission: 'view' }) }}
                />
              </div>
            </div>
          ) : (
            <button onClick={() => { setSidebarOpen(true); localStorage.setItem('ai.sidebarOpen', 'true') }} className="w-9 shrink-0 border rounded-lg flex flex-col items-center justify-center gap-1 text-muted-foreground hover:text-foreground hover:bg-gray-100 dark:hover:bg-gray-800 transition-colors" title="展开会话列表">
              <PanelLeftOpen className="h-4 w-4" />
              <span className="text-[10px] [writing-mode:vertical-rl]">对话</span>
            </button>
          )}

          <div
            className="flex-1 flex flex-col border rounded-lg overflow-hidden min-w-0 relative"
            onDragOver={e => { e.preventDefault(); if (canUseAI && convPerm !== 'view') setDragActive(true) }}
            onDragLeave={e => {
              // 仅在真正离开容器时收起(子元素间移动会触发 dragLeave, 用 relatedTarget 判定)
              if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragActive(false)
            }}
            onDrop={e => {
              e.preventDefault()
              setDragActive(false)
              if (!canUseAI || convPerm === 'view') return
              const files = Array.from(e.dataTransfer.files || [])
              if (files.length) handleFiles(files)
            }}
          >
            {dragActive && (
              <div className="absolute inset-0 z-20 flex items-center justify-center bg-blue-500/10 border-2 border-dashed border-blue-400 rounded-lg pointer-events-none">
                <p className="text-sm font-medium text-blue-600 dark:text-blue-300">松开以上传附件</p>
              </div>
            )}
            <ChatMessageList
              messages={messages}
              mode={mode}
              welcomeId="0"
              variant="page"
              enableCitations
              enableFeedback
              enableTaskList
              onFeedback={submitFeedback}
              onRetry={retryMessage}
              onRegenerate={regenerate}
              onEditResend={editAndResend}
              modelOptions={healthyModelOptions}
              onRetryWithModel={retryWithModel}
              onSuggestion={(t) => { if (!streaming && canUseAI) send(t) }}
              extraActions={renderMsgActions}
              loading={streaming}
              loadingText="思考中..."
              enableSearch
              emptyQuickActions={
                <div className="w-full max-w-2xl grid grid-cols-1 sm:grid-cols-2 gap-3">
                  {QUICK_ACTIONS.map(a => (
                    <button
                      key={a.label}
                      title="点击填入输入框，可修改后再发送"
                      onClick={() => { if (!streaming && canUseAI) { setInput(a.prompt); inputTaRef.current?.focus() } }}
                      disabled={streaming || !canUseAI}
                      className="flex items-start gap-2.5 text-left rounded-xl border border-border bg-background hover:bg-accent hover:border-blue-300 dark:hover:border-blue-500/40 px-3 py-2.5 transition-colors disabled:opacity-50 group"
                    >
                      <span className="mt-0.5 h-7 w-7 shrink-0 rounded-lg bg-blue-500/10 text-blue-500 flex items-center justify-center">
                        <a.icon className="h-4 w-4" />
                      </span>
                      <span className="min-w-0">
                        <span className="block text-xs font-medium text-foreground/90 group-hover:text-blue-600 dark:group-hover:text-blue-300 transition-colors">{a.label}</span>
                        {a.desc && <span className="block text-[11px] text-muted-foreground mt-0.5 leading-snug">{a.desc}</span>}
                      </span>
                    </button>
                  ))}
                </div>
              }
              endRef={messagesEndRef}
              className="p-4"
            />

            <ChatContextBar
              modelLabel={selectedModel ? (models.find(m => m.model === selectedModel)?.name || selectedModel) : undefined}
              knowledgeScope={knowledgeCategories}
              executor={executor}
              agents={agents}
              teams={teams}
              onExecutorChange={setExecutor}
              models={models}
              selectedModel={selectedModel}
              onModelChange={handleModelChange}
              knowledgeCategories={knowledgeCategories}
              knowledgeCategoryOptions={knowledgeCategoryOptions}
              onKnowledgeCategoriesChange={setKnowledgeCategories}
              contextTokens={memoizedCtxTokens}
              variant="page"
              onConfigure={() => navigate('/ai/settings')}
            />
            <ChatInputBar
              value={input}
              onChange={setInput}
              onSend={() => { histIdxRef.current = -1; send(input, pendingFiles); setPendingFiles([]) }}
              onStop={stopGeneration}
              onFiles={handleFiles}
              streaming={streaming}
              disabled={!canUseAI || convPerm === 'view'}
              // 欢迎态(messages.length===1)已在消息区渲染场景卡片(点击即发送), 此处不再重复展示胶囊
              enableQuickActions={!streaming && messages.length > 1}
              quickActions={QUICK_ACTIONS}
              // 提示词库: 选模板插入输入框(复用 /ai/prompts), 让沉淀的团队话术真正被一线取用
              enablePromptLibrary
              textareaRef={inputTaRef}
              variant="page"
              inputFooter={inputFooter}
            />
            {pendingFiles.length > 0 && (
              <div className="flex flex-wrap gap-1.5 px-4 pb-2 -mt-1">
                {pendingFiles.map((a, i) => (
                  <AttachmentChip key={`${a.name}-${i}`} att={a} onRemove={() => setPendingFiles(prev => prev.filter((_, j) => j !== i))} />
                ))}
                {uploadingCount > 0 && (
                  <span className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg text-[11px] border border-dashed border-gray-300 dark:border-gray-600 text-muted-foreground">
                    <Loader2 className="h-3 w-3 animate-spin" />上传中…
                  </span>
                )}
                {/* 多文件对比问答: 附了 2 个及以上文档时给一键指令, 省去用户自己写提示词。
                    附件正文由服务端抽取后注入(支持 PDF/Word/Excel/PPT/文本), 模型直接横向比对。 */}
                {pendingFiles.length >= 2 && (
                  <button
                    type="button"
                    className="inline-flex items-center gap-1.5 px-2 py-1 rounded-lg text-[11px] border border-sky-500/40 bg-sky-500/10 text-sky-600 dark:text-sky-300 hover:brightness-110 transition"
                    title="插入对比指令: 让 AI 横向比对这些附件"
                    onClick={() => {
                      const names = pendingFiles.map(f => f.name).join('、')
                      setInput(prev => (prev ? prev.replace(/\s*$/, ' ') : '') +
                        `请对比分析以下 ${pendingFiles.length} 个文档（${names}）的异同：先给出结论，再用表格逐项对比关键差异，最后指出需要注意的风险点。`)
                      inputTaRef.current?.focus()
                    }}
                  >
                    <Columns3 className="h-3 w-3" />对比这些文件
                  </button>
                )}
              </div>
            )}

            {!canUseAI && (
              <p className="text-[10px] text-amber-600 dark:text-amber-400 flex items-center gap-1 px-4 pb-2">
                <AlertTriangle className="h-3 w-3" />无 AI 工具执行权限，仅可查看历史对话。请联系管理员在角色中授予 ai:tool:exec。
              </p>
            )}
            {canUseAI && convPerm === 'view' && (
              <p className="text-[10px] text-violet-600 dark:text-violet-400 flex items-center gap-1 px-4 pb-2">
                <Lock className="h-3 w-3" />此会话由他人共享（仅查看）。如需继续对话，请向属主申请「可编辑」权限。
              </p>
            )}
          </div>
        </div>
      )}

      {/* 团队模式 */}
      {mode === 'team' && (
        <TeamChatView
          teams={teams}
          teamsLoading={teamsLoading}
          selectedTeamId={selectedTeamId}
          onSelectTeam={setSelectedTeamId}
          teamTurns={teamTurns}
          teamInput={teamInput}
          onTeamInput={setTeamInput}
          teamStreaming={teamStreaming}
          onSend={(t) => handleTeamSend(t)}
          onManageTeams={() => navigate('/ai/teams')}
          variant="page"
          endRef={teamMessagesEndRef}
        />
      )}

      {/* 敏感操作审批弹窗 */}
      <Modal open={!!pending} onCancel={() => { if (!approving) dismissApproval() }} title="🔐 敏感操作确认" footer={
        <Space>
          <Button onClick={() => respond('reject')} disabled={approving} danger><XCircle className="h-4 w-4 mr-1" />拒绝</Button>
          <Button type="primary" onClick={() => respond('approve')} disabled={approving}>
            {approving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <CheckCircle className="h-4 w-4 mr-1" />}批准执行
          </Button>
        </Space>
      }>
        {pending && (
          <div className="space-y-3">
            <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-amber-500/5 border border-amber-500/10' : 'bg-amber-50 border border-amber-200')}>
              <AlertTriangle className="h-4 w-4 text-amber-500 shrink-0" />
              <span>AI 请求执行敏感操作，请确认后放行。超时未响应将自动拒绝。</span>
            </div>
            <div className="grid gap-2 text-sm">
              <div><span className="text-muted-foreground text-xs">操作：</span><code className="font-mono text-xs bg-gray-100 dark:bg-gray-800 rounded px-1.5 py-0.5">{pending.tool}</code></div>
              <div><span className="text-muted-foreground text-xs">说明：</span><span className="text-sm">{pending.summary || '无'}</span></div>
              {pending.args && pending.args !== '{}' && (
                <div><span className="text-muted-foreground text-xs">参数：</span><pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-28 overflow-y-auto">{pending.args}</pre></div>
              )}
              <div className="text-[11px] text-muted-foreground">截止：{pending.deadline ? new Date(pending.deadline).toLocaleTimeString('zh-CN') : '-'}</div>
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
              <Select value={shareForm.target_type} onChange={v => setShareForm({ ...shareForm, target_type: v })} className="w-full" options={[{ value: 'user', label: '用户' }, { value: 'group', label: '用户组' }]} />
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">权限</p>
              <Select value={shareForm.permission} onChange={v => setShareForm({ ...shareForm, permission: v })} className="w-full" options={[{ value: 'view', label: '仅查看' }, { value: 'edit', label: '可继续对话' }]} />
            </div>
          </div>
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">目标 ID（用户名 / 用户组 ID）</p>
            <Input className="h-9 text-xs font-mono" placeholder="如: zhangsan 或 group-001" value={shareForm.target_id} onChange={e => setShareForm({ ...shareForm, target_id: e.target.value })} />
          </div>
        </div>
      </Modal>

      {/* 删除确认弹窗 */}
      <Popconfirm title="删除会话" description="删除该会话？此操作不可恢复。" onConfirm={handleDeleteConversation} okText="删除" cancelText="取消" open={!!pendingConvId} onCancel={() => setPendingConvId(null)}><span /></Popconfirm>
      <Popconfirm title="删除产物文件" description="删除该产物文件？" onConfirm={deleteArtifact} okText="删除" cancelText="取消" open={!!pendingArtifactId} onCancel={() => setPendingArtifactId(null)}><span /></Popconfirm>
      <Popconfirm title="删除工作区文件" description="删除该工作区文件？" onConfirm={deleteWorkspaceFile} okText="删除" cancelText="取消" open={!!pendingWsFileId} onCancel={() => setPendingWsFileId(null)}><span /></Popconfirm>
    </div>
  )
}
