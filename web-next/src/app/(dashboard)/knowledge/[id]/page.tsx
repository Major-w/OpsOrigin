'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { useKnowledgeStore, type ArticleVersion } from '@/stores/knowledge'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { formatTime } from '@/lib/utils'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ChevronLeft, Save, Sparkles, Loader2, Trash2, Heart, Bookmark, Star, MessageSquare, Share2, History, RotateCcw, CheckCircle2 } from 'lucide-react'
import { cn } from '@/lib/utils'

export default function KnowledgeEditorPage() {
  const params = useParams(); const router = useRouter()
  const isNew = params.id === 'new'
  const { articles, fetch, create, update, remove, categories, fetchCategories, fetchVersions, rollbackVersion } = useKnowledgeStore()
  const [form, setForm] = useState({ title: '', content: '', category: '', tags: '', author: 'admin', status: 'draft' })
  const [saving, setSaving] = useState(false)
  const [aiGenerating, setAiGenerating] = useState(false)
  const [aiPrompt, setAiPrompt] = useState('')
  const [loaded, setLoaded] = useState(false)
  // P1 内容管理: 版本历史
  const [versions, setVersions] = useState<ArticleVersion[]>([])
  const [showVersions, setShowVersions] = useState(false)
  const [rollingBack, setRollingBack] = useState(false)
  // Social
  const [liked, setLiked] = useState(false); const [bookmarked, setBookmarked] = useState(false)
  const [likeCount, setLikeCount] = useState(0); const [commentCount, setCommentCount] = useState(0)
  const [comments, setComments] = useState<any[]>([]); const [newComment, setNewComment] = useState('')
  const [showSocial, setShowSocial] = useState(false)
  const userId = 'admin'

  useEffect(() => {
    fetch()
    fetchCategories()
    if (!isNew && !loaded) {
      const a = articles.find(a => a.id === params.id)
      if (a) { setForm({ title: a.title, content: a.content, category: a.category, tags: (a.tags||[]).join(','), author: a.author, status: a.status || 'draft' }); setLoaded(true) }
    }
  }, [articles, params.id])

  // Fetch social stats
  useEffect(() => {
    if (isNew || !params.id) return
    apiFetch<any>(`/knowledge/${params.id}/stats?user=${userId}`).then(st => {
      setLiked(st.liked); setBookmarked(st.bookmarked); setLikeCount(st.likes)
    }).catch(() => {})
    apiFetch<any[]>(`/knowledge/${params.id}/comments`).then(c => {
      setComments(c||[]); setCommentCount((c||[]).length)
    }).catch(() => {})
  }, [params.id, isNew])

  const handleSave = async () => {
    if (!form.title.trim()) return
    setSaving(true)
    const data = { ...form, tags: form.tags.split(',').map((t: string) => t.trim()).filter(Boolean) }
    if (isNew) {
      const ok = await create(data)
      if (ok) router.push('/knowledge')
    } else {
      await update(String(params.id), data)
    }
    setSaving(false)
  }

  const handleDelete = async () => {
    if (!confirm('确认删除？')) return
    await remove(String(params.id))
    router.push('/knowledge')
  }

  const loadVersions = async () => {
    setShowVersions(v => {
      const next = !v
      if (next) fetchVersions(String(params.id)).then(setVersions)
      return next
    })
  }

  const handleRollback = async (version: number) => {
    if (!confirm(`确认回滚到 v${version}？当前内容将保存为新版本`)) return
    setRollingBack(true)
    const ok = await rollbackVersion(String(params.id), version)
    setRollingBack(false)
    if (ok) {
      setShowVersions(false)
      fetch()
      const a = useKnowledgeStore.getState().articles.find(x => x.id === params.id)
      if (a) setForm({ title: a.title, content: a.content, category: a.category, tags: (a.tags||[]).join(','), author: a.author, status: a.status || 'draft' })
    }
  }

  const toggleLike = async () => {
    const res = await apiFetch<{ liked: boolean }>(`/knowledge/${params.id}/like?user=${userId}`, { method: 'POST' })
    setLiked(res.liked); setLikeCount(c => res.liked ? c+1 : Math.max(0,c-1))
    toast.success(res.liked ? '已点赞' : '已取消')
  }
  const toggleBookmark = async () => {
    const res = await apiFetch<{ bookmarked: boolean }>(`/knowledge/${params.id}/bookmark?user=${userId}`, { method: 'POST' })
    setBookmarked(res.bookmarked); toast.success(res.bookmarked ? '已收藏' : '已取消')
  }
  const addComment = async () => {
    if (!newComment.trim()) return
    const c = await apiFetch<any>(`/knowledge/${params.id}/comments?user=${userId}`, { method: 'POST', body: JSON.stringify({ content: newComment }) })
    setComments(prev => [...prev, c]); setCommentCount(c => c+1); setNewComment('')
  }
  const handleShare = () => {
    navigator.clipboard?.writeText(window.location.origin + '/knowledge/' + params.id)
    toast.success('链接已复制到剪贴板')
  }

  const handleAIGenerate = async () => {
    if (!aiPrompt.trim()) { toast.error('请输入AI提示词'); return }
    setAiGenerating(true)
    try {
      const res = await apiFetch<{ content: string }>('/v1/ai/chat', {
        method: 'POST',
        body: JSON.stringify({ prompt: `写一篇运维知识库文章,题目是"${form.title || aiPrompt}"。请生成Markdown格式的完整文章,包含背景、步骤、注意事项等。直接输出文章正文,不要加任何前缀说明。`, stream: false }),
      })
      if (res.content) {
        setForm(f => ({ ...f, content: f.content ? f.content + '\n\n' + res.content : res.content }))
        toast.success('AI 内容已追加')
      }
    } catch (e: any) { toast.error('AI生成失败: ' + e.message) }
    setAiGenerating(false)
  }

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between gap-3">
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="icon" onClick={() => router.push('/knowledge')}><ChevronLeft className="h-4 w-4"/></Button>
        <h1 className="text-xl font-bold">{isNew ? 'AI 辅助写作' : '编辑文章'}</h1>
      </div>
      <div className="flex gap-2">
        {!isNew && <Button variant="destructive" size="sm" onClick={handleDelete}><Trash2 className="h-3.5 w-3.5 mr-1"/>删除</Button>}
        <Button size="sm" onClick={handleSave} disabled={saving||!form.title}>{saving?<Loader2 className="h-4 w-4 mr-1 animate-spin"/>:<Save className="h-4 w-4 mr-1"/>}保存</Button>
      </div>
    </div>

    <div className="grid md:grid-cols-3 gap-4">
      <div className="md:col-span-2 space-y-3">
        <Input placeholder="文章标题" value={form.title} onChange={e => setForm({...form, title: e.target.value})} className="text-lg font-bold"/>
        <div className="flex gap-2">
          <select className="flex h-8 rounded-md border border-input bg-background px-2 text-xs" value={form.category} onChange={e => setForm({...form, category: e.target.value})}>
            {categories.map(c => <option key={c} value={c}>{c}</option>)}
          </select>
          <select className="flex h-8 rounded-md border border-input bg-background px-2 text-xs" value={form.status} onChange={e => setForm({...form, status: e.target.value})}>
            <option value="draft">草稿</option>
            <option value="published">发布</option>
          </select>
          <Input placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({...form, tags: e.target.value})} className="h-8 text-xs"/>
        </div>
        {!isNew && <div className="flex gap-1 py-1">
          <Button variant={liked?'default':'outline'} size="sm" className="h-7 text-xs gap-1" onClick={toggleLike}><Heart className="h-3 w-3" fill={liked?'currentColor':'none'}/>{likeCount}</Button>
          <Button variant={bookmarked?'default':'outline'} size="sm" className="h-7 text-xs gap-1" onClick={toggleBookmark}><Bookmark className="h-3 w-3" fill={bookmarked?'currentColor':'none'}/></Button>
          <Button variant="outline" size="sm" className="h-7 text-xs gap-1" onClick={handleShare}><Share2 className="h-3 w-3"/>分享</Button>
          <Button variant="outline" size="sm" className="h-7 text-xs gap-1" onClick={()=>setShowSocial(!showSocial)}><MessageSquare className="h-3 w-3"/>{commentCount}</Button>
        </div>}
        {!isNew && showSocial && <div className="rounded-md border p-3 space-y-2 text-sm">
          <div className="flex gap-2"><Input className="h-7 text-xs" placeholder="写评论..." value={newComment} onChange={e=>setNewComment(e.target.value)} onKeyDown={e=>e.key==='Enter'&&addComment()}/><Button size="sm" className="h-7" onClick={addComment}>发送</Button></div>
          {comments.map(c => <div key={c.id} className="text-xs"><span className="font-medium">{c.user_id}</span> <span className="text-muted-foreground">{c.content}</span></div>)}
        </div>}
        <textarea className="flex min-h-[400px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono" value={form.content} onChange={e => setForm({...form, content: e.target.value})} placeholder="文章正文 (支持Markdown)"/>
      </div>

      <Card><CardContent className="p-4 space-y-3">
        <h3 className="text-sm font-medium flex items-center gap-1"><Sparkles className="h-4 w-4 text-amber-500"/>AI 伴写</h3>
        <p className="text-xs text-muted-foreground">输入提示词让AI帮你生成文章内容, 会追加到已有正文末尾</p>
        <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1" value={aiPrompt} onChange={e => setAiPrompt(e.target.value)} placeholder="例如: 介绍一下Linux磁盘空间清理的步骤和注意事项"/>
        <Button size="sm" className="w-full" onClick={handleAIGenerate} disabled={aiGenerating}>
          {aiGenerating ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin"/> : <Sparkles className="h-3.5 w-3.5 mr-1"/>}
          {aiGenerating ? '生成中...' : 'AI 生成内容'}
        </Button>
        <div className="text-[10px] text-muted-foreground pt-2 border-t">
          <p>提示: 先写好标题, 再让AI根据标题生成正文。多次调用可以持续补充内容。</p>
        </div>
      </CardContent></Card>

      {/* 版本历史 */}
      {!isNew && (
        <Card>
          <CardContent className="p-4 space-y-2">
            <button className="w-full flex items-center justify-between text-sm font-medium" onClick={loadVersions}>
              <span className="flex items-center gap-1"><History className="h-4 w-4 text-muted-foreground" />版本历史</span>
              <span className="text-xs text-muted-foreground">{showVersions ? '收起' : '展开'}</span>
            </button>
            {showVersions && (
              <div className="space-y-2 max-h-64 overflow-y-auto">
                {versions.length === 0 && <p className="text-xs text-muted-foreground">暂无历史版本（每次保存会自动留存上一版）</p>}
                {versions.map(v => (
                  <div key={v.id} className="flex items-center justify-between px-2.5 py-1.5 rounded-lg border border-gray-200 dark:border-gray-800">
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5">
                        <Badge variant="outline" className="text-[10px]">v{v.version}</Badge>
                        <span className="text-xs truncate">{v.title}</span>
                      </div>
                      <p className="text-[10px] text-muted-foreground mt-0.5">{formatTime(v.created_at)} · {v.content.length} 字符</p>
                    </div>
                    <Button variant="ghost" size="sm" className="h-6 text-[11px] text-amber-600 dark:text-amber-400" onClick={() => handleRollback(v.version)} disabled={rollingBack}>
                      <RotateCcw className="h-3 w-3 mr-0.5" />回滚
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      )}
    </div>
  </div>
}
