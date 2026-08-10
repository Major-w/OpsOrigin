'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { useKnowledgeStore, type ArticleVersion } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatTime } from '@/lib/utils'
import { MarkdownViewer } from '@/components/markdown-viewer'
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
} from 'lucide-react'
import { toast } from '@/stores/toast'
import { useAuthStore } from '@/stores/auth'

const getCurrentUser = () => useAuthStore.getState().user?.username || 'anonymous'

export default function KnowledgeDetailPage() {
  const params = useParams()
  const router = useRouter()
  const articleId = String(params.id)

  const {
    articles,
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
  } = useKnowledgeStore()

  const article = articles.find(a => a.id === articleId)

  const [editable, setEditable] = useState(false)
  const [saving, setSaving] = useState(false)
  const [form, setForm] = useState({
    title: '',
    content: '',
    category: '',
    tags: '',
    status: 'draft',
  })

  const [versions, setVersions] = useState<ArticleVersion[]>([])
  const [showVersions, setShowVersions] = useState(false)
  const [versionsLoaded, setVersionsLoaded] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)

  const [bookmarked, setBookmarked] = useState(false)
  const [followed, setFollowed] = useState(false)

  useEffect(() => {
    fetchArticles()
    fetchCategories()
  }, [fetchArticles, fetchCategories])

  useEffect(() => {
    if (article) {
      setForm({
        title: article.title,
        content: article.content,
        category: article.category,
        tags: (article.tags || []).join(', '),
        status: article.status || 'draft',
      })
    }
  }, [article])

  useEffect(() => {
    const user = getCurrentUser()
    listBookmarks(user).then(ids => setBookmarked(ids.includes(articleId)))
    listFollows(user).then(ids => setFollowed(ids.includes(articleId)))
  }, [articleId, listBookmarks, listFollows])

  const loadVersions = async () => {
    if (!showVersions && !versionsLoaded) {
      const v = await fetchVersions(articleId)
      setVersions(v)
      setVersionsLoaded(true)
    }
    setShowVersions(s => !s)
  }

  const handleRollback = async (version: number) => {
    if (!confirm(`确认回滚到 v${version}？当前内容将保存为新版本`)) return
    setRollingBack(true)
    const ok = await rollbackVersion(articleId, version)
    setRollingBack(false)
    if (ok) {
      setShowVersions(false)
      setVersionsLoaded(false)
      await fetchArticles()
    }
  }

  const handleSave = async () => {
    if (!form.title.trim()) {
      toast.error('标题不能为空')
      return
    }
    setSaving(true)
    const data = {
      ...form,
      tags: form.tags
        .split(',')
        .map(t => t.trim())
        .filter(Boolean),
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
      })
    }
    setEditable(false)
  }

  const handleDelete = async () => {
    if (!confirm('确认删除该文章？此操作不可撤销。')) return
    const ok = await remove(articleId)
    if (ok) router.push('/knowledge')
  }

  const handleBookmarkToggle = async () => {
    const user = getCurrentUser()
    const { apiFetch } = await import('@/lib/utils')
    const res = await apiFetch<{ bookmarked: boolean }>(
      `/knowledge/${articleId}/bookmark?user=${user}`,
      { method: 'POST' }
    )
    setBookmarked(res.bookmarked)
    toast.success(res.bookmarked ? '已收藏' : '已取消收藏')
  }

  const handleFollowToggle = async () => {
    const ok = await toggleFollow(articleId, getCurrentUser())
    setFollowed(ok)
    toast.success(ok ? '已关注，文章更新将通知你' : '已取消关注')
  }

  if (!article) {
    return (
      <div className="p-6 flex flex-col items-center justify-center min-h-[400px] text-muted-foreground">
        <Loader2 className="h-6 w-6 animate-spin mb-2" />
        <p className="text-sm">加载文章中…</p>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4 max-w-4xl mx-auto">
      {/* Header */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <Button
            variant="ghost"
            size="icon"
            onClick={() => router.push('/knowledge')}
          >
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <h1 className="text-xl font-bold">
            {editable ? '编辑文章' : '文章详情'}
          </h1>
        </div>
        <div className="flex gap-2">
          {!editable && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setEditable(true)}
            >
              <Pencil className="h-3.5 w-3.5 mr-1" />
              编辑
            </Button>
          )}
          <Button
            variant="destructive"
            size="sm"
            onClick={handleDelete}
          >
            <Trash2 className="h-3.5 w-3.5 mr-1" />
            删除
          </Button>
          {editable && (
            <>
              <Button variant="ghost" size="sm" onClick={handleCancel}>
                取消
              </Button>
              <Button size="sm" onClick={handleSave} disabled={saving || !form.title}>
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
            <textarea
              value={form.content}
              onChange={e => setForm({ ...form, content: e.target.value })}
              placeholder="文章正文（Markdown 格式）"
              className="w-full min-h-[500px] rounded-md border border-input bg-background px-4 py-3 text-sm font-mono resize-y outline-none focus:ring-1 focus:ring-ring"
            />
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
              <Badge variant="secondary">{article.category}</Badge>
            )}
            <Badge
              variant={article.status === 'published' ? 'default' : 'outline'}
            >
              {article.status === 'published' ? '已发布' : '草稿'}
            </Badge>
            {(article.tags || []).map(tag => (
              <Badge key={tag} variant="outline" className="gap-1">
                <Tag className="h-2.5 w-2.5" />
                {tag}
              </Badge>
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
            <Badge variant="outline" className="text-[10px]">
              v{article.version}
            </Badge>
          </div>

          {/* Action Buttons */}
          <div className="flex gap-2 py-3 mt-4 border-b">
            <Button
              variant={bookmarked ? 'default' : 'outline'}
              size="sm"
              className="h-8 text-xs gap-1.5"
              onClick={handleBookmarkToggle}
            >
              <Bookmark
                className="h-3.5 w-3.5"
                fill={bookmarked ? 'currentColor' : 'none'}
              />
              {bookmarked ? '已收藏' : '收藏'}
            </Button>
            <Button
              variant={followed ? 'default' : 'outline'}
              size="sm"
              className="h-8 text-xs gap-1.5"
              onClick={handleFollowToggle}
            >
              {followed ? (
                <Bell className="h-3.5 w-3.5" />
              ) : (
                <EyeOff className="h-3.5 w-3.5" />
              )}
              {followed ? '已关注' : '关注'}
            </Button>
          </div>

          {/* Markdown Body */}
          <MarkdownViewer content={article.content} className="mt-4" />
        </article>
      )}

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
                      <Badge variant="outline" className="text-[10px]">
                        v{v.version}
                      </Badge>
                      <span className="text-xs truncate">{v.title}</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-0.5">
                      {formatTime(v.created_at)} · {v.content.length} 字符
                    </p>
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 text-[11px] text-amber-600 dark:text-amber-400 shrink-0"
                    onClick={() => handleRollback(v.version)}
                    disabled={rollingBack}
                  >
                    <RotateCcw className="h-3 w-3 mr-0.5" />
                    回滚
                  </Button>
                </div>
              ))}
            </div>
          </CardContent>
        )}
      </Card>
    </div>
  )
}
