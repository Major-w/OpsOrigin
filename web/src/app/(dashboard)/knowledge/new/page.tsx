
import { message } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useKnowledgeStore } from '@/stores/knowledge'
import { Button, Input } from 'antd'
import { apiFetch } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { MarkdownViewer } from '@/components/markdown-viewer'
import dynamic from '@/lib/dynamic'

const RichTextEditor = dynamic(() => import('@/components/rich-text-editor').then(m => ({ default: m.RichTextEditor })), { ssr: false, loading: () => <div className="h-64 bg-muted animate-pulse rounded" /> })
import { fileToCompressedDataUrl } from '@/lib/image-utils'
import { cn } from '@/lib/utils'
import {
  ArrowLeft, Save, Loader2, Sparkles, Wand2, Eye, EyeOff,
  PenLine, AlignLeft, MoveHorizontal, MoveVertical, X,
  Image as ImageIcon, Upload, Link2, CheckCircle2,
} from 'lucide-react'

const getKBUser = () => useAuthStore.getState().user?.username || 'anonymous'

function clamp(v: number, lo: number, hi: number) {
  return Math.min(hi, Math.max(lo, v))
}

export default function KnowledgeNewPage() {
  const navigate = useNavigate()
  const { categories, fetchCategories, create } = useKnowledgeStore()

  const [form, setForm] = useState({ title: '', content: '', category: '', tags: '', status: 'draft', expires_at: '', view_permission: 'all', edit_permission: 'owner' })
  const [inputMode, setInputMode] = useState<'markdown' | 'rich'>('markdown')
  const [previewOn, setPreviewOn] = useState(true)
  const [direction, setDirection] = useState<'row' | 'col'>('row')
  const [ratio, setRatio] = useState(0.42) // 预览区占比(宽/高)
  const [aiOpen, setAiOpen] = useState(false)
  const [aiPrompt, setAiPrompt] = useState('')
  const [aiGen, setAiGen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')

  const splitRef = useRef<HTMLDivElement>(null)
  const [dragging, setDragging] = useState(false)
  // Markdown 模式图片插入: 菜单 + 隐藏文件输入 + 文本域光标定位
  const [mdImgMenu, setMdImgMenu] = useState(false)
  const mdImgInputRef = useRef<HTMLInputElement>(null)
  const mdTextareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => { fetchCategories() }, [fetchCategories])

  // 拖拽分隔条: 调整预览区比例 (row=左右, col=上下)
  useEffect(() => {
    if (!dragging) return
    const onMove = (e: MouseEvent) => {
      const rect = splitRef.current?.getBoundingClientRect()
      if (!rect) return
      const r = direction === 'row'
        ? (rect.right - e.clientX) / rect.width
        : (rect.bottom - e.clientY) / rect.height
      setRatio(clamp(r, 0.18, 0.7))
    }
    const onUp = () => setDragging(false)
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [dragging, direction])

  // —— Markdown 模式图片插入: 上传(压缩为 Base64) 或 贴 URL, 在光标处插入 ——
  const insertMdAtCursor = (text: string) => {
    const ta = mdTextareaRef.current
    const cur = form.content
    if (!ta) {
      setForm(f => ({ ...f, content: (f.content ? f.content + '\n\n' : '') + text }))
      return
    }
    const start = ta.selectionStart
    const end = ta.selectionEnd
    setForm({ ...form, content: cur.slice(0, start) + text + cur.slice(end) })
    requestAnimationFrame(() => {
      ta.focus()
      const pos = start + text.length
      ta.setSelectionRange(pos, pos)
    })
  }

  const onPickMdImage = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!f) return
    try {
      const src = await fileToCompressedDataUrl(f)
      insertMdAtCursor(`![图片](${src})`)
      setMdImgMenu(false)
    } catch (err: any) {
      message.error('图片插入失败: ' + err.message)
    }
  }

  const insertMdImageByUrl = () => {
    setMdImgMenu(false)
    const url = window.prompt('图片URL')
    if (url) insertMdAtCursor(`![图片](${url})`)
  }

  const handleAIGenerate = async () => {
    if (!form.title.trim() && !aiPrompt.trim()) {
      message.error('请先填写标题或AI提示词')
      return
    }
    setAiGen(true)
    try {
      const res = await apiFetch<{ content: string }>('/ai/chat', {
        method: 'POST',
        body: JSON.stringify({
          prompt: `写一篇运维知识库文章,题目是"${form.title.trim() || aiPrompt.trim()}".请生成Markdown格式的完整文章,包含背景、步骤、注意事项等。直接输出文章正文,不要加任何前缀说明。`,
          stream: false,
        }),
      })
      const content = res.content || ''
      if (content) {
        setForm(f => ({ ...f, content }))
        message.success('AI 草稿已生成')
      } else {
        message.error('AI 未返回内容')
      }
    } catch (e: any) {
      message.error('AI生成失败: ' + e.message)
    }
    setAiGen(false)
  }

  const handleSave = async (status: 'draft' | 'published' = 'draft') => {
    if (!form.title.trim()) {
      setFormError('标题不能为空')
      message.error('标题不能为空')
      return
    }
    setSaving(true)
    const data = {
      ...form,
      status,
      category: form.category || categories[0] || '',
      tags: form.tags.split(',').map(t => t.trim()).filter(Boolean),
      author: getKBUser(),
      expires_at: form.expires_at ? new Date(form.expires_at).toISOString() : null,
    }
    const ok = await create(data)
    setSaving(false)
    if (!ok) return
    const arts = useKnowledgeStore.getState().articles
    const created = arts
      .filter(a => a.title === data.title)
      .sort((x, y) => y.updated_at.localeCompare(x.updated_at))[0]
    if (created?.id) navigate(`/knowledge/${created.id}`)
    else navigate('/knowledge')
  }

  const previewStyle = previewOn
    ? (direction === 'row' ? { width: `${ratio * 100}%` } : { height: `${ratio * 100}%` })
    : undefined

  return (
    <div className="p-6 flex flex-col gap-3 overflow-hidden" style={{ height: 'calc(100vh - 56px)' }}>
      {/* 顶栏: 返回 + 标题 + 保存 */}
      <div className="flex items-center justify-between gap-3 shrink-0">
        <div className="flex items-center gap-3">
          <Button type="text" size="middle" onClick={() => navigate('/knowledge')} title="返回文章列表">
            <ArrowLeft className="h-4 w-4" />
          </Button>
          <h1 className="text-2xl font-bold">新建文章</h1>
        </div>
        <div className="flex gap-2">
          <Button size="middle" onClick={() => handleSave('draft')} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Save className="h-4 w-4 mr-1" />}
            保存草稿
          </Button>
          <Button type="primary" size="middle" onClick={() => handleSave('published')} disabled={saving}>
            {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <CheckCircle2 className="h-4 w-4 mr-1" />}
            发布
          </Button>
        </div>
      </div>

      {/* 基本信息行 */}
      <div className="flex flex-wrap items-center gap-2 shrink-0">
        <Input
          className={cn('flex-1 min-w-[180px]', !form.title && formError && 'border-red-500')}
          placeholder="文章标题 *"
          value={form.title}
          onChange={e => { setForm({ ...form, title: e.target.value }); if (formError) setFormError('') }}
        />
        <select
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
          value={form.category}
          onChange={e => setForm({ ...form, category: e.target.value })}
        >
          <option value="">未分类</option>
          {categories.map(c => <option key={c} value={c}>{c}</option>)}
        </select>
        <Input className="w-44" placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({ ...form, tags: e.target.value })} />
        <input
          type="datetime-local"
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
          value={form.expires_at}
          onChange={e => setForm({ ...form, expires_at: e.target.value })}
          title="有效期(不设则永不过期)"
        />
        <select
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
          value={form.view_permission}
          onChange={e => setForm({ ...form, view_permission: e.target.value })}
          title="查看权限"
        >
          <option value="all">所有人可看</option>
          <option value="team">仅团队</option>
          <option value="owner">仅自己</option>
        </select>
        <select
          className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm"
          value={form.edit_permission}
          onChange={e => setForm({ ...form, edit_permission: e.target.value })}
          title="编辑权限"
        >
          <option value="owner">仅自己可编辑</option>
          <option value="team">团队可编辑</option>
          <option value="all">所有人可编辑</option>
        </select>
      </div>

      {/* 工具栏: 录入模式 / 预览开关 / 分栏方向 / AI 伴写 */}
      <div className="flex items-center gap-2 shrink-0 flex-wrap">
        <div className="flex items-center gap-1">
          <Button size="middle" type={inputMode === 'markdown' ? 'primary' : 'default'} onClick={() => setInputMode('markdown')}><PenLine className="h-3.5 w-3.5 mr-1" />Markdown</Button>
          <Button size="middle" type={inputMode === 'rich' ? 'primary' : 'default'} onClick={() => setInputMode('rich')}><AlignLeft className="h-3.5 w-3.5 mr-1" />富文本</Button>
        </div>
        <div className="flex-1" />
        <Button size="middle" type={previewOn ? 'primary' : 'default'} onClick={() => setPreviewOn(v => !v)} title="开关实时预览">
          {previewOn ? <Eye className="h-3.5 w-3.5 mr-1" /> : <EyeOff className="h-3.5 w-3.5 mr-1" />}
          {previewOn ? '预览开' : '预览关'}
        </Button>
        {previewOn && (
          <Button size="middle" type="default" onClick={() => setDirection(d => d === 'row' ? 'col' : 'row')} title="切换左右/上下分栏">
            {direction === 'row' ? <MoveVertical className="h-3.5 w-3.5 mr-1" /> : <MoveHorizontal className="h-3.5 w-3.5 mr-1" />}
            {direction === 'row' ? '上下分栏' : '左右分栏'}
          </Button>
        )}
        {inputMode === 'markdown' && (
          <div className="relative">
            <Button size="middle" type={mdImgMenu ? 'primary' : 'default'} onClick={() => setMdImgMenu(v => !v)} title="插入图片"><ImageIcon className="h-3.5 w-3.5 mr-1" />插入图片</Button>
            {mdImgMenu && (
              <div className="absolute left-0 top-full z-20 mt-1 w-44 rounded-md border border-border bg-background shadow-lg p-1">
                <button type="button" className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted" onClick={() => { setMdImgMenu(false); mdImgInputRef.current?.click() }}><Upload className="h-3.5 w-3.5" />上传图片</button>
                <button type="button" className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted" onClick={insertMdImageByUrl}><Link2 className="h-3.5 w-3.5" />图片URL</button>
              </div>
            )}
            <input ref={mdImgInputRef} type="file" accept="image/*" className="hidden" onChange={onPickMdImage} />
          </div>
        )}
        <Button size="middle" type={aiOpen ? 'primary' : 'default'} onClick={() => setAiOpen(v => !v)}><Sparkles className="h-3.5 w-3.5 mr-1" />AI 伴写</Button>
      </div>

      {/* 编辑区 + 预览区 (可拖拽调整比例) */}
      <div ref={splitRef} className={cn('flex-1 min-h-0 flex gap-1 select-none', direction === 'col' && 'flex-col')}>
        {/* 编辑区 */}
        <div className="flex-1 min-w-0 min-h-0 border rounded-md overflow-hidden flex flex-col">
          <div className="flex-1 min-h-0 overflow-auto">
            {inputMode === 'rich'
              ? <RichTextEditor value={form.content} onChange={md => setForm(f => ({ ...f, content: md }))} minHeight="480px" />
              : <textarea
                  ref={mdTextareaRef}
                  className="w-full h-full min-h-[320px] bg-background px-3 py-2 text-sm font-mono resize-none focus-visible:outline-none"
                  value={form.content}
                  onChange={e => setForm({ ...form, content: e.target.value })}
                  placeholder="文章正文 (Markdown, 支持表格/任务列表等 GFM 语法)"
                />}
          </div>
        </div>

        {/* 分隔条 */}
        {previewOn && (
          <div
            onMouseDown={e => { e.preventDefault(); setDragging(true) }}
            className={cn(
              'shrink-0 bg-border/60 hover:bg-primary/40 active:bg-primary/50 transition-colors',
              direction === 'row' ? 'w-1.5 cursor-col-resize self-stretch' : 'h-1.5 cursor-row-resize w-full',
            )}
            title="拖拽调整预览区大小"
          />
        )}

        {/* 预览区 */}
        {previewOn && (
          <div className="min-w-0 min-h-0 border rounded-md bg-background overflow-auto" style={previewStyle}>
            <div className="p-4 md:p-6">
              {form.content.trim()
                ? (inputMode === 'rich'
                    ? <RichTextEditor value={form.content} onChange={() => {}} readOnly />
                    : <MarkdownViewer content={form.content} />)
                : <p className="text-xs text-muted-foreground italic">（空内容）</p>}
            </div>
          </div>
        )}
      </div>

      {/* AI 伴写折叠面板 */}
      {aiOpen && (
        <div className="shrink-0 border rounded-md p-3 space-y-2 bg-muted/30">
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-medium flex items-center gap-1"><Wand2 className="h-4 w-4 text-amber-500" />AI 伴写</h3>
            <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => setAiOpen(false)}><X className="h-3.5 w-3.5" /></Button>
          </div>
          <p className="text-xs text-muted-foreground">输入提示词让 AI 生成文章草稿，将替换当前正文</p>
          <div className="flex gap-2">
            <textarea
              className="flex h-16 flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs"
              value={aiPrompt}
              onChange={e => setAiPrompt(e.target.value)}
              placeholder="例如: 介绍一下Linux磁盘空间清理的步骤和注意事项"
            />
            <Button size="middle" onClick={handleAIGenerate} disabled={aiGen} className="shrink-0">
              {aiGen ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Sparkles className="h-3.5 w-3.5 mr-1" />}
              {aiGen ? '生成中…' : '生成草稿'}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
