
import { useEffect, useRef, useState } from 'react'
import { useParams, useNavigate, useSearchParams } from 'react-router-dom'
import { useKnowledgeStore, type ArticleVersion } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag as AntTag, Button, Popconfirm, Modal, Select, Input as AntInput } from 'antd'
import { formatTime, apiFetch, apiFetchRaw, cn } from '@/lib/utils'
import { MarkdownViewer } from '@/components/markdown-viewer'
import { OwnerGroupSelect } from '@/components/knowledge/owner-group-select'
import {
  ArrowLeft,
  Eye,
  Pencil,
  Trash2,
  Bookmark,
  Bell,
  Tag,
  User,
  Calendar,
  History,
  Loader2,
  RotateCcw,
  Save,
  ChevronDown,
  ChevronRight,
  EyeOff,
  Paperclip,
  FileText,
  Download,
  Upload,
  ThumbsUp,
  Star,
  MessageSquare,
  Share2,
  Send,
  CalendarOff,
  Shield,
  Lock,
  CheckCircle2,
  Sparkles,
  RefreshCw,
  Quote,
} from 'lucide-react'
import { useAuthStore } from '@/stores/auth'
import { getMessage } from '@/lib/message'
import { AIWriteModal, useAIWrite } from '@/components/ai/ai-write-modal'
import { AttachmentPreview } from '@/components/knowledge/attachment-preview'
import { ChunkExplorer } from '@/components/knowledge/chunk-explorer'

const getCurrentUser = () => useAuthStore.getState().user?.username || 'anonymous'

// —— 状态机(P3-3): 与后端 internal/knowledge/lifecycle.go 的 articleTransitions 保持一致 ——
// 后端是唯一真源(不合法迁移会返回 409), 这里只是把可选动作提前渲染出来。
const STATUS_META: Record<string, { label: string; color: string }> = {
  draft: { label: '草稿', color: 'default' },
  in_review: { label: '待评审', color: 'orange' },
  published: { label: '已发布', color: 'blue' },
  archived: { label: '已归档', color: 'default' },
}

const STATUS_ACTIONS: Record<string, { to: string; label: string; hint: string }[]> = {
  draft: [
    { to: 'in_review', label: '提交评审', hint: '交由他人复核后再发布' },
    { to: 'published', label: '直接发布', hint: '立即参与 AI 检索' },
    { to: 'archived', label: '归档', hint: '下架保留, 不参与检索' },
  ],
  in_review: [
    { to: 'published', label: '评审通过并发布', hint: '通过评审, 参与 AI 检索' },
    { to: 'draft', label: '驳回为草稿', hint: '需填写驳回原因, 作者可据此修改' },
    { to: 'archived', label: '归档', hint: '不再需要这篇知识' },
  ],
  published: [
    { to: 'in_review', label: '撤回复审', hint: '先撤下待复核' },
    { to: 'draft', label: '撤回为草稿', hint: '下架并转回草稿继续修改' },
    { to: 'archived', label: '归档', hint: '下架保留, 不参与检索' },
  ],
  archived: [
    { to: 'draft', label: '恢复为草稿', hint: '重新编辑后再发布' },
    { to: 'published', label: '重新发布', hint: '立即参与 AI 检索' },
  ],
}

/** 状态流转/评审轨迹(P3-3): 与后端 internal/knowledge.ArticleReview 字段对齐。 */
interface ArticleReview {
  id: string
  article_id: string
  from: string
  to: string
  note?: string
  actor: string
  created_at: string
}

const statusLabel = (s: string) => STATUS_META[s]?.label || s


interface ArticleAttachment {
  id: number
  article_id: string
  file_name: string
  file_size: number
  file_type: string
  uploaded_by: string
  object_key?: string
  created_at: string
  /** 解析入库状态: 已抽文本切块并嵌入, 可被 AI 检索 */
  indexed?: boolean
  index_error?: string
  chunk_count?: number
}

const fmtSize = (n: number) =>
  n >= 1048576 ? (n / 1048576).toFixed(1) + ' MB'
    : n >= 1024 ? (n / 1024).toFixed(1) + ' KB'
    : n + ' B'

function CommentItem({ c, onReply, depth }: { c: any; onReply: (v: { id: string; user: string } | null) => void; depth: number }) {
  return (
    <div className={cn('rounded-lg border border-gray-100 dark:border-gray-800 px-3 py-2', depth > 0 && 'ml-4 mt-1')}>
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <User className="h-3 w-3 text-muted-foreground" />
          <span className="text-xs font-medium">{c.user_id}</span>
          <span className="text-[10px] text-muted-foreground">{formatTime(c.created_at)}</span>
        </div>
        <button
          className="text-[11px] text-blue-500 hover:underline"
          onClick={() => onReply({ id: c.id, user: c.user_id })}
        >
          回复
        </button>
      </div>
      <p className="text-xs mt-1 whitespace-pre-wrap">{c.content}</p>
      {c.replies?.map((r: any) => <CommentItem key={r.id} c={r} onReply={onReply} depth={depth + 1} />)}
    </div>
  )
}

export default function KnowledgeDetailPage() {
  const params = useParams()
  const navigate = useNavigate()
  const articleId = String(params.id)

  const {
    articles,
    loading,
    fetch: fetchArticles,
    update,
    remove,
    fetchCategories,
    categories,
    fetchVersions,
    rollbackVersion,
    toggleFollow,
    listBookmarks,
    listFollows,
    getStats,
    toggleLike,
    rate,
    getComments,
    addComment,
  } = useKnowledgeStore()

  const article = articles.find(a => a.id === articleId)

  const [editable, setEditable] = useState(false)
  const [editPreview, setEditPreview] = useState(true)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    title: '',
    content: '',
    category: '',
    tags: '',
    status: 'draft',
    expires_at: '',
    view_permission: 'all',
    edit_permission: 'owner',
    owner_group_ids: [] as string[],
  })

  const [versions, setVersions] = useState<ArticleVersion[]>([])
  const [showVersions, setShowVersions] = useState(false)
  const [versionsLoaded, setVersionsLoaded] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)

  // AI 辅助编写: 编辑器空行输入 / 唤起弹窗, 生成 Markdown 回填到光标处
  const aiWrite = useAIWrite({
    getContent: () => form.content,
    setContent: (v) => setForm(f => ({ ...f, content: v })),
    defaultCategory: form.category,
    categories,
  })

  const [bookmarked, setBookmarked] = useState(false)
  const [followed, setFollowed] = useState(false)

  // —— 社交 ——
  const [liked, setLiked] = useState(false)
  const [likeCount, setLikeCount] = useState(0)
  const [userRating, setUserRating] = useState<number | undefined>(undefined)
  const [avgRating, setAvgRating] = useState(0)
  const [ratingCount, setRatingCount] = useState(0)
  const [comments, setComments] = useState<any[]>([])
  const [commentText, setCommentText] = useState('')
  const [replyTo, setReplyTo] = useState<{ id: string; user: string } | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [showComments, setShowComments] = useState(true)

  // —— 附件 ——
  const [attachments, setAttachments] = useState<ArticleAttachment[]>([])
  const [uploading, setUploading] = useState(false)
  const attInputRef = useRef<HTMLInputElement>(null)
  const [pendingConfirm, setPendingConfirm] = useState<{ title: string; description: string; onConfirm: () => void | Promise<void> } | null>(null)
  // 在线预览: 点击附件先开弹层(图片/PDF 内联, Office/文本走后端抽取), 下载仍保留独立出口
  const [previewAtt, setPreviewAtt] = useState<ArticleAttachment | null>(null)
  const [reindexing, setReindexing] = useState<number | null>(null)

  // 引用溯源: AI 回答的引用带块锚点时跳到 ?chunk=<块ID>, 此处拉取并高亮对应片段
  const [searchParams] = useSearchParams()
  const chunkParam = searchParams.get('chunk') || ''
  const [citedChunk, setCitedChunk] = useState<{ heading?: string; content: string } | null>(null)
  const chunkRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!chunkParam) { setCitedChunk(null); return }
    let cancelled = false
    apiFetch<{ heading?: string; content: string }>(`/knowledge/${articleId}/chunks/${encodeURIComponent(chunkParam)}`)
      .then(c => { if (!cancelled) setCitedChunk(c || null) })
      .catch(() => { if (!cancelled) setCitedChunk(null) })
    return () => { cancelled = true }
  }, [chunkParam])

  useEffect(() => {
    if (citedChunk && chunkRef.current) {
      chunkRef.current.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  }, [citedChunk])

  const handleReindexAtt = async (a: ArticleAttachment) => {
    setReindexing(a.id)
    try {
      const r = await apiFetch<{ indexed: boolean; index_error?: string; chunk_count?: number }>(
        `/knowledge/${articleId}/attachments/${a.id}/reindex`, { method: 'POST' })
      if (r?.indexed) getMessage().success(`已解析入库，共 ${r.chunk_count ?? 0} 个片段`)
      else getMessage().warning(r?.index_error || '未能解析入库')
      await loadAttachments()
    } catch (err: any) {
      getMessage().error('重新解析失败: ' + err.message)
    }
    setReindexing(null)
  }

  const loadAttachments = async () => {
    try {
      const list = await apiFetch<ArticleAttachment[]>(`/knowledge/${articleId}/attachments`)
      setAttachments(list || [])
    } catch { /* 附件接口不可用时不阻塞页面 */ }
  }

  useEffect(() => { loadAttachments() }, [articleId])

  const handleUploadAtt = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!f) return
    setUploading(true)
    try {
      const fd = new FormData()
      fd.append('file', f)
      fd.append('uploaded_by', getCurrentUser())
      const res = await apiFetchRaw(`/knowledge/${articleId}/attachments`, { method: 'POST', body: fd })
      if (!res.ok) throw new Error(`上传失败(${res.status})`)
      getMessage().success('附件已上传')
      loadAttachments()
    } catch (err: any) {
      getMessage().error('上传失败: ' + err.message)
    }
    setUploading(false)
  }

  const handleDownloadAtt = async (a: ArticleAttachment) => {
    try {
      const res = await apiFetchRaw(`/knowledge/${articleId}/attachments/${a.id}`)
      if (!res.ok) { getMessage().error('下载失败'); return }
      const blob = await res.blob()
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = a.file_name
      link.click()
      URL.revokeObjectURL(url)
    } catch (err: any) {
      getMessage().error('下载失败: ' + err.message)
    }
  }

  const handleDeleteAtt = async (a: ArticleAttachment) => {
    setPendingConfirm({
      title: '删除附件',
      description: `删除附件「${a.file_name}」？`,
      onConfirm: async () => {
        try {
          await apiFetch(`/knowledge/${articleId}/attachments/${a.id}`, { method: 'DELETE' })
          getMessage().success('已删除')
          loadAttachments()
        } catch (err: any) {
          getMessage().error('删除失败: ' + err.message)
        }
      },
    })
  }

  // 状态流转与评审轨迹(P3-3)
  const [reviews, setReviews] = useState<ArticleReview[]>([])
  const [transitionOpen, setTransitionOpen] = useState(false)
  const [transitionTo, setTransitionTo] = useState('')
  const [transitionNote, setTransitionNote] = useState('')
  const [transitioning, setTransitioning] = useState(false)

  useEffect(() => {
    fetchArticles()
    fetchCategories()
  }, [fetchArticles, fetchCategories])

  const loadReviews = async () => {
    try {
      const res = await apiFetch<{ items: ArticleReview[] }>(`/knowledge/${articleId}/reviews?limit=20`)
      setReviews(res.items || [])
    } catch {
      setReviews([])
    }
  }

  useEffect(() => { if (articleId) loadReviews() }, [articleId])

  // 状态流转: 提交防重 + toast + 数据刷新(F2.2/F2.3)
  const handleTransition = async () => {
    if (transitioning || !transitionTo) return
    setTransitioning(true)
    try {
      const res = await apiFetch<{ retrievable: boolean }>(`/knowledge/${articleId}/transition`, {
        method: 'POST',
        body: JSON.stringify({ to: transitionTo, note: transitionNote.trim() }),
      })
      getMessage().success(
        transitionTo === 'published'
          ? (res?.retrievable === false ? '状态已更新' : '文章已发布，AI 现在可以检索到它')
          : `已${statusLabel(transitionTo) === transitionTo ? '更新状态' : `转为${statusLabel(transitionTo)}`}`,
      )
      setTransitionOpen(false)
      setTransitionNote('')
      setTransitionTo('')
      await fetchArticles()
      await loadReviews()
    } catch (err: any) {
      getMessage().error(err?.message || '状态流转失败')
    } finally {
      setTransitioning(false)
    }
  }

  useEffect(() => {
    if (article) {
      setForm({
        title: article.title,
        content: article.content,
        category: article.category,
        tags: (article.tags || []).join(', '),
        status: article.status || 'draft',
        expires_at: article.expires_at ? article.expires_at.slice(0, 16) : '',
        view_permission: article.view_permission || 'all',
        edit_permission: article.edit_permission || 'owner',
        owner_group_ids: article.owner_group_ids || [],
      })
    }
  }, [article])

  useEffect(() => {
    const user = getCurrentUser()
    listBookmarks(user).then(ids => setBookmarked(ids.includes(articleId)))
    listFollows(user).then(ids => setFollowed(ids.includes(articleId)))
    // 加载社交统计
    getStats(articleId, user).then(s => {
      setLiked(s.liked); setLikeCount(s.likes)
      setAvgRating(s.avg_rating); setRatingCount(s.rating_count)
      if (s.user_rating !== undefined) setUserRating(s.user_rating)
    })
    getComments(articleId).then(setComments)
  }, [articleId, listBookmarks, listFollows, getStats, getComments])

  const loadVersions = async () => {
    if (!showVersions && !versionsLoaded) {
      const v = await fetchVersions(articleId)
      setVersions(v)
      setVersionsLoaded(true)
    }
    setShowVersions(s => !s)
  }

  const handleRollback = async (version: number) => {
    setPendingConfirm({
      title: '回滚版本',
      description: `确认回滚到 v${version}？当前内容将保存为新版本`,
      onConfirm: async () => {
        setRollingBack(true)
        const ok = await rollbackVersion(articleId, version)
        setRollingBack(false)
        if (ok) {
          setShowVersions(false)
          setVersionsLoaded(false)
          await fetchArticles()
        }
      },
    })
  }

  const handleSave = async () => {
    if (!form.title.trim()) {
      getMessage().error('标题不能为空')
      return
    }
    // 归属校验：team 可见但没选归属团队 → 该文章对任何人都不可见（后端同样 400 兜底）。
    if (form.view_permission === 'team' && form.owner_group_ids.length === 0) {
      getMessage().error('请选择归属团队：否则该文章对任何人都不可见')
      return
    }
    setSaving(true)
    const data = {
      ...form,
      tags: form.tags
        .split(',')
        .map(t => t.trim())
        .filter(Boolean),
      expires_at: form.expires_at ? new Date(form.expires_at).toISOString() : null,
    }
    const ok = await update(articleId, data)
    setSaving(false)
    if (ok) setEditable(false)
  }

  const handleCancel = () => {
    if (article) {
      setForm({
        title: article.title,
        content: article.content,
        category: article.category,
        tags: (article.tags || []).join(', '),
        status: article.status || 'draft',
        expires_at: article.expires_at ? article.expires_at.slice(0, 16) : '',
        view_permission: article.view_permission || 'all',
        edit_permission: article.edit_permission || 'owner',
        owner_group_ids: article.owner_group_ids || [],
      })
    }
    setEditable(false)
  }

  const handleDelete = async () => {
    setPendingConfirm({
      title: '删除文章',
      description: '确认删除该文章？此操作不可撤销。',
      onConfirm: async () => {
        const ok = await remove(articleId)
        if (ok) navigate('/knowledge')
      },
    })
  }

  const handleBookmarkToggle = async () => {
    const user = getCurrentUser()
    const { apiFetch } = await import('@/lib/utils')
    const res = await apiFetch<{ bookmarked: boolean }>(
      `/knowledge/${articleId}/bookmark?user=${user}`,
      { method: 'POST' }
    )
    setBookmarked(res.bookmarked)
    getMessage().success(res.bookmarked ? '已收藏' : '已取消收藏')
  }

  const handleFollowToggle = async () => {
    const ok = await toggleFollow(articleId, getCurrentUser())
    setFollowed(ok)
    getMessage().success(ok ? '已关注，文章更新将通知你' : '已取消关注')
  }

  const handleLikeToggle = async () => {
    const ok = await toggleLike(articleId, getCurrentUser())
    setLiked(ok)
    setLikeCount(c => ok ? c + 1 : c - 1)
  }

  const handleRate = async (rating: number) => {
    await rate(articleId, rating, '', getCurrentUser())
    setUserRating(rating)
    const s = await getStats(articleId, getCurrentUser())
    setAvgRating(s.avg_rating); setRatingCount(s.rating_count)
  }

  const handleSubmitComment = async () => {
    if (!commentText.trim()) return
    setSubmitting(true)
    const c = await addComment(articleId, commentText, replyTo?.id || '', getCurrentUser())
    setSubmitting(false)
    if (c) {
      setComments(prev => [...prev, c])
      setCommentText('')
      setReplyTo(null)
      getMessage().success('评论已发布')
    }
  }

  const handleShare = async () => {
    const url = `${window.location.origin}/knowledge/${articleId}`
    try {
      await navigator.clipboard.writeText(url)
      getMessage().success('链接已复制到剪贴板')
    } catch {
      // fallback
      const input = document.createElement('input')
      input.value = url; document.body.appendChild(input); input.select()
      document.execCommand('copy'); document.body.removeChild(input)
      getMessage().success('链接已复制到剪贴板')
    }
  }

  /** 评论列表树形化: 顶层(parent_id='') + 回复嵌套 */
  const buildCommentTree = (list: any[]) => {
    const roots: any[] = []
    const map: Record<string, any> = {}
    list.forEach(c => { map[c.id] = { ...c, replies: [] } })
    list.forEach(c => {
      if (c.parent_id && map[c.parent_id]) {
        map[c.parent_id].replies.push(map[c.id])
      } else {
        roots.push(map[c.id])
      }
    })
    return roots
  }

  const VIEW_PERM_LABELS: Record<string, string> = { all: '所有人', team: '仅团队', owner: '仅自己' }
  const EDIT_PERM_LABELS: Record<string, string> = { owner: '仅自己', team: '团队可编辑', all: '所有人可编辑' }

  const isExpired = (expiresAt?: string | null) => {
    if (!expiresAt) return false
    return new Date(expiresAt) < new Date()
  }
  const expiryLabel = (expiresAt?: string | null) => {
    if (!expiresAt) return null
    const d = new Date(expiresAt)
    const now = new Date()
    if (d < now) return '已过期'
    const diff = d.getTime() - now.getTime()
    const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
    return days <= 7 ? `${days}天后过期` : `${d.toLocaleDateString()} 过期`
  }

  if (!article) {
    // 列表仍在加载: 显示加载中; 加载完成仍找不到: 显示"不存在", 避免死链 URL 无限转圈
    if (loading) {
      return (
        <div className="p-6 flex flex-col items-center justify-center min-h-[400px] text-muted-foreground">
          <Loader2 className="h-6 w-6 animate-spin mb-2" />
          <p className="text-sm">加载文章中…</p>
        </div>
      )
    }
    return (
      <div className="p-6 flex flex-col items-center justify-center min-h-[400px] text-muted-foreground">
        <p className="text-sm mb-3">文章不存在或已删除</p>
        <Button size="middle" type="default" onClick={() => navigate('/knowledge')}>返回文章列表</Button>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4 max-w-4xl mx-auto">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <Button
            type="text"
            size="middle"
            onClick={() => navigate('/knowledge')}
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <h1 className="text-2xl font-bold">
            {editable ? '编辑文章' : '文章详情'}
          </h1>
        </div>
        <div className="flex gap-2">
          {!editable && (STATUS_ACTIONS[article.status] || []).length > 0 && (
            <Button
              type={article.status === 'published' ? 'default' : 'primary'}
              size="middle"
              onClick={() => {
                const next = STATUS_ACTIONS[article.status] || []
                setTransitionTo(next[0]?.to || '')
                setTransitionNote('')
                setTransitionOpen(true)
              }}
              disabled={saving}
            >
              <CheckCircle2 className="h-4 w-4 mr-1" />
              {article.status === 'published' ? '状态流转' : article.status === 'in_review' ? '评审' : '发布 / 流转'}
            </Button>
          )}
          {!editable && (
            <Button
              type="default"
              size="middle"
              onClick={() => setEditable(true)}
            >
              <Pencil className="h-4 w-4 mr-1" />
              编辑
            </Button>
          )}
          <Button
            danger
            size="middle"
            onClick={handleDelete}
          >
            <Trash2 className="h-4 w-4 mr-1" />
            删除
          </Button>
          {editable && (
            <>
              <Button type="text" size="middle" onClick={handleCancel}>
                取消
              </Button>
              <Button size="middle" onClick={handleSave} disabled={saving || !form.title}>
                {saving ? (
                  <Loader2 className="h-4 w-4 mr-1 animate-spin" />
                ) : (
                  <Save className="h-4 w-4 mr-1" />
                )}
                保存
              </Button>
            </>
          )}
        </div>
      </div>

      {/* Article Content */}
      {editable ? (
        <Card>
          <CardContent className="p-6 space-y-4">
            <input
              type="text"
              value={form.title}
              onChange={e => setForm({ ...form, title: e.target.value })}
              placeholder="文章标题"
              className="w-full text-2xl font-bold bg-transparent border-b border-border pb-2 outline-none placeholder:text-muted-foreground"
            />
            <div className="flex flex-wrap gap-2">
              <select
                className="flex h-9 rounded-md border border-input bg-background px-3 text-sm"
                value={form.category}
                onChange={e => setForm({ ...form, category: e.target.value })}
              >
                {categories.map(c => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
              <input
                type="text"
                value={form.tags}
                onChange={e => setForm({ ...form, tags: e.target.value })}
                placeholder="标签，逗号分隔"
                className="flex h-9 rounded-md border border-input bg-background px-3 text-sm flex-1 min-w-[200px]"
              />
            </div>
            {/* 有效期 + 权限设置 */}
            <div className="flex flex-wrap gap-3">
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-muted-foreground">有效期</label>
                <input
                  type="datetime-local"
                  value={form.expires_at}
                  onChange={e => setForm({ ...form, expires_at: e.target.value })}
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                />
              </div>
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-muted-foreground">查看权限</label>
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  value={form.view_permission}
                  onChange={e => {
                    const v = e.target.value
                    // 切离「仅团队」时清空归属：残留的团队选择会在下次切回 team 时静默生效（越权可见）。
                    setForm(f => ({ ...f, view_permission: v, owner_group_ids: v === 'team' ? f.owner_group_ids : [] }))
                  }}
                >
                  <option value="all">所有人</option>
                  <option value="team">仅团队</option>
                  <option value="owner">仅自己</option>
                </select>
              </div>
              {/* 归属团队：选了谁，谁就能在 AI 检索里命中这篇文章；为空则对任何人都不可见。 */}
              {form.view_permission === 'team' && (
                <div className="flex flex-col gap-1">
                  <label className="text-[11px] text-muted-foreground">归属团队</label>
                  <OwnerGroupSelect
                    value={form.owner_group_ids}
                    onChange={ids => setForm(f => ({ ...f, owner_group_ids: ids }))}
                    invalid={form.owner_group_ids.length === 0}
                  />
                </div>
              )}
              <div className="flex flex-col gap-1">
                <label className="text-[11px] text-muted-foreground">编辑权限</label>
                <select
                  className="h-9 rounded-md border border-input bg-background px-3 text-sm"
                  value={form.edit_permission}
                  onChange={e => setForm({ ...form, edit_permission: e.target.value })}
                >
                  <option value="owner">仅自己</option>
                  <option value="team">团队可编辑</option>
                  <option value="all">所有人可编辑</option>
                </select>
              </div>
              {form.expires_at && (
                <button
                  type="button"
                  className="text-[11px] text-red-500 self-end mb-0.5 hover:underline"
                  onClick={() => setForm({ ...form, expires_at: '' })}
                >
                  清除有效期
                </button>
              )}
            </div>
            {/* 预览开关 + AI 辅助编写 */}
            <div className="flex items-center gap-2">
              <Button size="middle" type={editPreview ? 'primary' : 'default'} onClick={() => setEditPreview(v => !v)}>
                {editPreview ? '预览开' : '预览关'}
              </Button>
              <Button size="middle" type="default" onClick={aiWrite.openModal}>
                <Sparkles className="h-4 w-4 mr-1" />
                AI 编写
              </Button>
              <span className="text-[11px] text-muted-foreground">空行输入 / 可打开命令清单</span>
            </div>
            <div className="flex gap-2 min-h-[500px]">
              <textarea
                ref={aiWrite.editorRef}
                value={form.content}
                onChange={e => setForm({ ...form, content: e.target.value })}
                onKeyDown={aiWrite.onEditorKeyDown}
                placeholder="文章正文（Markdown 格式）"
                className={cn(
                  "rounded-md border border-input bg-background px-4 py-3 text-sm font-mono resize-none outline-none focus:ring-1 focus:ring-ring",
                  editPreview ? "flex-1 min-w-0" : "w-full"
                )}
              />
              {editPreview && (
                <div className="flex-1 min-w-0 rounded-md border border-input bg-background px-4 py-3 overflow-auto">
                  {form.content.trim()
                    ? <MarkdownViewer content={form.content} />
                    : <p className="text-xs text-muted-foreground italic">（空内容）</p>}
                </div>
              )}
            </div>
          </CardContent>
        </Card>
      ) : (
        <article className="card-glass-static rounded-xl p-6 md:p-8 animate-fade-in-up">
          <h1 className="text-2xl md:text-3xl font-bold tracking-tight">
            {article.title}
          </h1>

          {/* Metadata */}
          <div className="flex flex-wrap items-center gap-3 mt-4 text-xs text-muted-foreground">
            <span className="flex items-center gap-1">
              <User className="h-3.5 w-3.5" />
              {article.author || 'admin'}
            </span>
            <span className="flex items-center gap-1">
              <Eye className="h-3.5 w-3.5" />
              {article.views} 次浏览
            </span>
            {article.category && (
              <AntTag color="default">{article.category}</AntTag>
            )}
            <AntTag
              color={STATUS_META[article.status]?.color || 'default'}
            >
              {statusLabel(article.status)}
            </AntTag>
            {(article.tags || []).map(tag => (
              <AntTag key={tag} className="gap-1">
                <Tag className="h-2.5 w-2.5" />
                {tag}
              </AntTag>
            ))}
          </div>

          <div className="flex flex-wrap items-center gap-3 mt-2 text-xs text-muted-foreground">
            <span className="flex items-center gap-1">
              <Calendar className="h-3.5 w-3.5" />
              创建于 {formatTime(article.created_at)}
            </span>
            {article.updated_at !== article.created_at && (
              <span className="flex items-center gap-1">
                <History className="h-3.5 w-3.5" />
                更新于 {formatTime(article.updated_at)}
              </span>
            )}
            <AntTag>v{article.version}</AntTag>
            {article.expires_at && (
              <AntTag color={isExpired(article.expires_at) ? 'error' : 'warning'} className="gap-1">
                <CalendarOff className="h-2.5 w-2.5" />
                {expiryLabel(article.expires_at)}
              </AntTag>
            )}
            {article.view_permission && article.view_permission !== 'all' && (
              <AntTag color="default" className="gap-1">
                <Shield className="h-2.5 w-2.5" />
                {VIEW_PERM_LABELS[article.view_permission] || article.view_permission}
              </AntTag>
            )}
            {article.edit_permission && article.edit_permission !== 'all' && (
              <AntTag color="default" className="gap-1">
                <Lock className="h-2.5 w-2.5" />
                编辑: {EDIT_PERM_LABELS[article.edit_permission] || article.edit_permission}
              </AntTag>
            )}
          </div>

          {/* Action Buttons */}
          <div className="flex flex-wrap items-center gap-2.5 py-3 mt-4 border-b">
            <Button
              type={liked ? 'primary' : 'default'}
              size="middle"
              className="text-sm gap-1.5"
              onClick={handleLikeToggle}
            >
              <ThumbsUp className="h-4 w-4" fill={liked ? 'currentColor' : 'none'} />
              {liked ? '已赞' : '点赞'}{likeCount > 0 && <span className="ml-0.5">{likeCount}</span>}
            </Button>
            <Button
              type={bookmarked ? 'primary' : 'default'}
              size="middle"
              className="text-sm gap-1.5"
              onClick={handleBookmarkToggle}
            >
              <Bookmark className="h-4 w-4" fill={bookmarked ? 'currentColor' : 'none'} />
              {bookmarked ? '已收藏' : '收藏'}
            </Button>
            <Button
              type={followed ? 'primary' : 'default'}
              size="middle"
              className="text-sm gap-1.5"
              onClick={handleFollowToggle}
            >
              {followed ? <Bell className="h-4 w-4" /> : <EyeOff className="h-4 w-4" />}
              {followed ? '已关注' : '关注'}
            </Button>
            <Button type="default" size="middle" className="text-sm gap-1.5" onClick={handleShare}>
              <Share2 className="h-4 w-4" />分享
            </Button>
            {ratingCount > 0 && (
              <span className="text-sm text-muted-foreground flex items-center gap-1 ml-1">
                <Star className="h-4 w-4 text-amber-400" fill="currentColor" />
                {avgRating.toFixed(1)} <span className="text-xs">({ratingCount}人评价)</span>
              </span>
            )}
          </div>

          {/* Markdown Body */}
          <MarkdownViewer content={article.content} className="mt-4" />

          {/* Star Rating */}
          <div className="mt-6 pt-4 border-t">
            <h3 className="text-sm font-medium mb-2">为这篇文章评分</h3>
            <div className="flex items-center gap-1">
              {[1, 2, 3, 4, 5].map(n => (
                <button
                  key={n}
                  onClick={() => handleRate(n)}
                  className="p-0.5 transition-colors"
                  title={`${n} 分`}
                >
                  <Star
                    className={`h-5 w-5 ${(userRating !== undefined ? n <= userRating : false) ? 'text-amber-400' : 'text-gray-300 dark:text-gray-600'}`}
                    fill={n <= (userRating || 0) ? 'currentColor' : 'none'}
                    strokeWidth={1.5}
                  />
                </button>
              ))}
              {userRating !== undefined && (
                <span className="text-xs text-muted-foreground ml-2">你的评分: {userRating}/5</span>
              )}
            </div>
          </div>

          {/* Comments Section */}
          <div className="mt-6 pt-4 border-t">
            <button
              className="flex items-center gap-2 text-sm font-medium mb-3"
              onClick={() => setShowComments(s => !s)}
            >
              <MessageSquare className="h-4 w-4" />
              评论 ({comments.length})
              {showComments ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
            </button>
            {showComments && (
              <div className="space-y-3">
                {/* Comment Input */}
                <div className="flex gap-2">
                  <input
                    type="text"
                    value={commentText}
                    onChange={e => setCommentText(e.target.value)}
                    onKeyDown={e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSubmitComment() } }}
                    placeholder={replyTo ? `回复 ${replyTo.user}...` : '写评论...'}
                    className="flex-1 h-9 rounded-md border border-input bg-background px-3 text-sm outline-none focus:ring-1 focus:ring-ring"
                  />
                  {replyTo && (
                    <Button type="text" size="middle" className="h-9 text-xs" onClick={() => setReplyTo(null)}>取消回复</Button>
                  )}
                  <Button size="middle" className="h-9 px-3 text-sm" onClick={handleSubmitComment} disabled={submitting || !commentText.trim()}>
                    {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                  </Button>
                </div>
                {/* Comment List */}
                {comments.length === 0 && (
                  <p className="text-xs text-muted-foreground text-center py-4">暂无评论，快来抢沙发吧~</p>
                )}
                {buildCommentTree(comments).map(c => (
                  <CommentItem key={c.id} c={c} onReply={setReplyTo} depth={0} />
                ))}
              </div>
            )}
          </div>
        </article>
      )}

      {/* 引用溯源: 从 AI 回答的引用跳转进来时, 高亮命中的原文片段 */}
      {citedChunk && (
        <div ref={chunkRef} className="rounded-lg border-l-4 border-sky-500 bg-sky-500/10 px-4 py-3">
          <div className="flex items-center gap-1.5 text-xs font-medium text-sky-700 dark:text-sky-300 mb-1">
            <Quote className="h-3.5 w-3.5" />
            AI 引用的原文片段{citedChunk.heading ? ` · ${citedChunk.heading}` : ''}
          </div>
          <div className="whitespace-pre-wrap break-words text-xs leading-relaxed text-foreground/90">
            {citedChunk.content}
          </div>
        </div>
      )}

      {/* 附件 */}
      <Card>
        <CardHeader className="pb-3">
          <div className="w-full flex items-center justify-between">
            <CardTitle className="flex items-center gap-2 text-sm">
              <Paperclip className="h-4 w-4 text-muted-foreground" />
              附件 ({attachments.length})
            </CardTitle>
            <Button
              type="default"
              size="middle"
              className="h-8 text-xs"
              onClick={() => attInputRef.current?.click()}
              disabled={uploading}
            >
              {uploading ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Upload className="h-4 w-4 mr-1" />}
              {uploading ? '上传中…' : '上传附件'}
            </Button>
            <input ref={attInputRef} type="file" className="hidden" onChange={handleUploadAtt} />
          </div>
        </CardHeader>
        <CardContent className="pt-0 space-y-2">
          {attachments.length === 0 ? (
            <p className="text-xs text-muted-foreground py-3 text-center">暂无附件</p>
          ) : attachments.map(a => (
            <div key={a.id} className="flex items-center justify-between px-3 py-2 rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-muted/50 transition-colors">
              <div className="min-w-0 flex-1 flex items-center gap-2">
                <FileText className="h-4 w-4 text-muted-foreground shrink-0" />
                <button
                  type="button"
                  className="text-xs truncate hover:underline hover:text-primary text-left"
                  title="点击预览"
                  onClick={() => setPreviewAtt(a)}
                >
                  {a.file_name}
                </button>
                <span className="text-[10px] text-muted-foreground shrink-0">{fmtSize(a.file_size)}</span>
                <span className="text-[10px] text-muted-foreground shrink-0">{formatTime(a.created_at)}</span>
                {/* 解析入库状态: 决定该附件内容能否被 AI 检索到 */}
                {a.indexed ? (
                  <span className="text-[10px] shrink-0 px-1.5 py-0.5 rounded-full bg-emerald-500/15 text-emerald-600 dark:text-emerald-300" title="内容已抽取切片并向量化，可被 AI 检索">
                    已入库 {a.chunk_count ?? 0} 段
                  </span>
                ) : (
                  <span
                    className="text-[10px] shrink-0 px-1.5 py-0.5 rounded-full bg-amber-500/15 text-amber-600 dark:text-amber-300"
                    title={a.index_error || '尚未解析入库，AI 检索不到该附件内容'}
                  >
                    未入库
                  </span>
                )}
              </div>
              <div className="flex items-center gap-1 shrink-0">
                <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" title="预览" onClick={() => setPreviewAtt(a)}>
                  <Eye className="h-3.5 w-3.5" />
                </Button>
                {!a.indexed && (
                  <Button
                    type="text"
                    size="middle"
                    className="h-6 w-6 text-amber-500"
                    title="重新解析入库"
                    disabled={reindexing === a.id}
                    onClick={() => handleReindexAtt(a)}
                  >
                    {reindexing === a.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                  </Button>
                )}
                <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" title="下载" onClick={() => handleDownloadAtt(a)}>
                  <Download className="h-3.5 w-3.5" />
                </Button>
                <Popconfirm title={`删除附件「${a.file_name}」？`} onConfirm={() => handleDeleteAtt(a)}>
                  <Button type="text" size="middle" className="h-6 w-6 text-red-400" title="删除">
                    <Trash2 className="h-3.5 w-3.5" />
                  </Button>
                </Popconfirm>
              </div>
            </div>
          ))}
        </CardContent>
      </Card>

      {/* 分块可视化(批次 5): 看文章被切成哪几段、每段多大、什么形态 —— 调分块质量时最直观 */}
      <ChunkExplorer articleId={articleId} />

      {/* Version History */}
      <Card>
        <CardHeader className="pb-3">
          <button
            className="w-full flex items-center justify-between text-sm font-medium"
            onClick={loadVersions}
          >
            <CardTitle className="flex items-center gap-2 text-sm">
              <History className="h-4 w-4 text-muted-foreground" />
              版本历史
            </CardTitle>
            {showVersions ? (
              <ChevronDown className="h-4 w-4 text-muted-foreground" />
            ) : (
              <ChevronRight className="h-4 w-4 text-muted-foreground" />
            )}
          </button>
        </CardHeader>
        {showVersions && (
          <CardContent className="pt-0">
            <div className="space-y-2 max-h-72 overflow-y-auto">
              {versions.length === 0 && (
                <p className="text-xs text-muted-foreground py-4 text-center">
                  暂无历史版本（每次保存会自动留存上一版）
                </p>
              )}
              {versions.map(v => (
                <div
                  key={v.id}
                  className="flex items-center justify-between px-3 py-2 rounded-lg border border-gray-200 dark:border-gray-800 hover:bg-muted/50 transition-colors"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <AntTag>
                        v{v.version}
                      </AntTag>
                      <span className="text-xs truncate">{v.title}</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-0.5">
                      {formatTime(v.created_at)} · {v.content.length} 字符
                    </p>
                  </div>
                  <Button
                    type="text"
                    size="middle"
                    className="h-8 text-xs text-amber-600 dark:text-amber-400 shrink-0"
                    onClick={() => handleRollback(v.version)}
                    disabled={rollingBack}
                  >
                    <RotateCcw className="h-4 w-4 mr-1" />
                    回滚
                  </Button>
                </div>
              ))}
            </div>
          </CardContent>
        )}
      </Card>

      <Popconfirm title={pendingConfirm?.title || ''} description={pendingConfirm?.description || ''} onConfirm={async () => { if (pendingConfirm) await pendingConfirm.onConfirm(); setPendingConfirm(null) }} open={!!pendingConfirm} onCancel={() => setPendingConfirm(null)}>
        <span />
      </Popconfirm>

      {/* 评审轨迹(P3-3): 谁在什么时候把文章改成了什么状态, 驳回原因留在这里 */}
      {reviews.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base flex items-center gap-2">
              <History className="h-4 w-4" />评审与状态轨迹
            </CardTitle>
          </CardHeader>
          <CardContent>
            <ol className="space-y-3">
              {reviews.map(r => (
                <li key={r.id} className="flex gap-3 text-sm">
                  <span className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-primary/60" />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-muted-foreground">{statusLabel(r.from)}</span>
                      <ArrowLeft className="h-3 w-3 rotate-180 text-muted-foreground" />
                      <span className="font-medium">{statusLabel(r.to)}</span>
                      <span className="text-xs text-muted-foreground">
                        {r.actor} · {formatTime(r.created_at)}
                      </span>
                    </div>
                    {r.note && (
                      <p className="mt-0.5 text-xs text-muted-foreground break-words">备注：{r.note}</p>
                    )}
                  </div>
                </li>
              ))}
            </ol>
          </CardContent>
        </Card>
      )}

      {/* 状态流转弹窗(P3-3): 目标状态只能从合法迁移里选, 备注会写入评审轨迹 */}
      <Modal
        title="状态流转"
        open={transitionOpen}
        onCancel={() => setTransitionOpen(false)}
        onOk={handleTransition}
        okText="确认流转"
        confirmLoading={transitioning}
        okButtonProps={{ disabled: !transitionTo }}
        destroyOnClose
      >
        <div className="space-y-3 py-1">
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            当前状态
            <AntTag color={STATUS_META[article.status]?.color || 'default'}>{statusLabel(article.status)}</AntTag>
          </div>
          <Select
            className="w-full"
            value={transitionTo || undefined}
            placeholder="选择目标状态"
            onChange={setTransitionTo}
            options={(STATUS_ACTIONS[article.status] || []).map(a => ({
              value: a.to,
              label: `${a.label}（${a.hint}）`,
            }))}
          />
          <AntInput.TextArea
            rows={3}
            maxLength={500}
            showCount
            placeholder="评审备注 / 驳回原因（可选，会记录到评审轨迹）"
            value={transitionNote}
            onChange={e => setTransitionNote(e.target.value)}
          />
          {transitionTo && transitionTo !== 'published' && (
            <p className="text-xs text-muted-foreground">
              转为{statusLabel(transitionTo)}后，该文章不再被 AI 检索召回（知识块保留，重新发布无需重建索引）。
            </p>
          )}
        </div>
      </Modal>

      {/* AI 辅助编写弹窗(工具栏按钮 / 编辑器空行输入 / 命令清单选中「AI 编写」唤起) */}
      <AIWriteModal {...aiWrite.modalProps} />
      {aiWrite.slashMenu}

      {/* 附件在线预览弹层 */}
      <AttachmentPreview
        open={!!previewAtt}
        articleId={articleId}
        attachment={previewAtt}
        onClose={() => setPreviewAtt(null)}
      />
    </div>
  )
}
