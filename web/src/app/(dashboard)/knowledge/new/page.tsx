
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useKnowledgeStore } from '@/stores/knowledge'
import { Button, Image, Input, Upload } from 'antd'
import { useAuthStore } from '@/stores/auth'
import { MarkdownViewer } from '@/components/markdown-viewer'
import { AIWriteModal, useAIWrite } from '@/components/ai/ai-write-modal'
import dynamic from '@/lib/dynamic'

const RichTextEditor = dynamic(() => import('@/components/rich-text-editor').then(m => ({ default: m.RichTextEditor })), { ssr: false, loading: () => <div className="h-64 bg-muted animate-pulse rounded" /> })
import { fileToCompressedDataUrl } from '@/lib/image-utils'
import { apiFetch, cn } from '@/lib/utils'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { OwnerGroupSelect } from '@/components/knowledge/owner-group-select'
import {
  ArrowLeft, Save, Loader2, Sparkles, Eye, EyeOff,
  PenLine, AlignLeft, MoveHorizontal, MoveVertical,
  Image as ImageIcon, Link2, CheckCircle2, Upload as UploadIcon, UserCheck,
} from 'lucide-react'
import { getMessage } from '@/lib/message'

/** 创建去重的候选项(P3-1): 与后端 internal/knowledge.DuplicateCandidate 字段对齐。 */
interface DuplicateCandidate {
  id: string
  title: string
  status: string
  category?: string
  updated_at: string
  similarity: number
  reason: string
}

const DUP_REASON_LABEL: Record<string, string> = {
  title_same: '标题完全相同',
  content_same: '正文完全相同',
  title_similar: '标题高度相似',
}

/** 状态中文名(与详情页标签保持一致语义)。 */
function statusLabel(status: string): string {
  switch (status) {
    case 'published': return '已发布'
    case 'in_review': return '待评审'
    case 'archived': return '已归档'
    default: return '草稿'
  }
}

const getKBUser = () => useAuthStore.getState().user?.username || 'anonymous'

function clamp(v: number, lo: number, hi: number) {
  return Math.min(hi, Math.max(lo, v))
}

export default function KnowledgeNewPage() {
  const navigate = useNavigate()
  const { categories, fetchCategories, create } = useKnowledgeStore()

  const [form, setForm] = useState({ title: '', content: '', category: '', tags: '', status: 'draft', expires_at: '', view_permission: 'all', edit_permission: 'owner', owner_group_ids: [] as string[] })
  const [inputMode, setInputMode] = useState<'markdown' | 'rich'>('markdown')
  const [previewOn, setPreviewOn] = useState(true)
  const [direction, setDirection] = useState<'row' | 'col'>('row')
  const [ratio, setRatio] = useState(0.42) // 预览区占比(宽/高)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')
  // 创建去重(P3-1): 命中相似文章时挂起保存, 等作者确认
  const [dupCandidates, setDupCandidates] = useState<DuplicateCandidate[]>([])
  const [dupOpen, setDupOpen] = useState(false)
  const [pendingStatus, setPendingStatus] = useState<'draft' | 'in_review' | 'published'>('draft')

  const splitRef = useRef<HTMLDivElement>(null)
  const [dragging, setDragging] = useState(false)
  // Markdown 模式图片插入: 菜单 + 隐藏文件输入 + 文本域光标定位
  const [mdImgMenu, setMdImgMenu] = useState(false)
  const mdImgInputRef = useRef<HTMLInputElement>(null)
  const mdTextareaRef = useRef<HTMLTextAreaElement>(null)

  // AI 辅助编写: 编辑器空行输入 / 唤起弹窗, 生成 Markdown 回填到光标处(复用 mdTextareaRef)
  const aiWrite = useAIWrite({
    getContent: () => form.content,
    setContent: (v) => setForm(f => ({ ...f, content: v })),
    defaultCategory: form.category,
    categories,
    editorRef: mdTextareaRef,
  })

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
      getMessage().error('图片插入失败: ' + err.message)
    }
  }

  const insertMdImageByUrl = () => {
    setMdImgMenu(false)
    const url = window.prompt('图片URL')
    if (url) insertMdAtCursor(`![图片](${url})`)
  }

  const handleSave = async (status: 'draft' | 'in_review' | 'published' = 'draft') => {
    if (!form.title.trim()) {
      setFormError('标题不能为空')
      getMessage().error('标题不能为空')
      return
    }
    // 归属校验：team 可见但没有归属团队时，该文章对任何人都不可见（后端也会 400），
    // 与其让作者以为「发给团队了」实际谁都没看到，不如在提交前拦住。
    if (form.view_permission === 'team' && form.owner_group_ids.length === 0) {
      setFormError('「仅团队」可见必须至少选择一个归属团队')
      getMessage().error('请选择归属团队：否则该文章对任何人都不可见')
      return
    }
    // 创建去重(P3-1): 提交前先查同租户内的相似文章(同名/同正文/标题高度相似),
    // 命中则先弹确认框, 由作者决定是去改已有文章还是仍然新建一篇。
    // 放在这里而不是等 409: 后端 409 会把候选清单塞进错误信息, 体验差且拿不到结构化数据。
    try {
      const res = await apiFetch<{ duplicates: DuplicateCandidate[] }>('/knowledge/duplicates', {
        method: 'POST',
        body: JSON.stringify({ title: form.title, content: form.content }),
      })
      if (res.duplicates?.length) {
        setDupCandidates(res.duplicates)
        setPendingStatus(status)
        setDupOpen(true)
        return
      }
    } catch {
      // 预检失败不阻塞创建: 后端 handleCreate 仍会做一次同样的检查(409 兜底)。
    }
    await doSave(status, false)
  }

  const doSave = async (status: 'draft' | 'in_review' | 'published', force: boolean) => {
    setSaving(true)
    const data = {
      ...form,
      status,
      category: form.category || categories[0] || '',
      tags: form.tags.split(',').map(t => t.trim()).filter(Boolean),
      author: getKBUser(),
      expires_at: form.expires_at ? new Date(form.expires_at).toISOString() : null,
      ...(force ? { force_duplicate: true } : {}),
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
          {/* 评审流程(P3-3): 需要他人复核的知识先提交评审, 而不是直接发布 */}
          <Button size="middle" onClick={() => handleSave('in_review')} disabled={saving}>
            <UserCheck className="h-4 w-4 mr-1" />
            提交评审
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
          onChange={e => {
            const v = e.target.value
            // 切离「仅团队」时清空归属：残留的团队选择会在下次切回 team 时静默生效，造成越权可见。
            setForm(f => ({ ...f, view_permission: v, owner_group_ids: v === 'team' ? f.owner_group_ids : [] }))
          }}
          title="查看权限"
        >
          <option value="all">所有人可看</option>
          <option value="team">仅团队</option>
          <option value="owner">仅自己</option>
        </select>
        {/* 归属团队：仅在「仅团队」可见时出现。选了谁，谁就能在 AI 检索里命中这篇文章。 */}
        {form.view_permission === 'team' && (
          <OwnerGroupSelect
            value={form.owner_group_ids}
            onChange={ids => setForm(f => ({ ...f, owner_group_ids: ids }))}
            invalid={form.owner_group_ids.length === 0}
          />
        )}
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
                <button type="button" className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted" onClick={() => { setMdImgMenu(false); mdImgInputRef.current?.click() }}><UploadIcon className="h-3.5 w-3.5" />上传图片</button>
                <button type="button" className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-muted" onClick={insertMdImageByUrl}><Link2 className="h-3.5 w-3.5" />图片URL</button>
              </div>
            )}
            <input ref={mdImgInputRef} type="file" accept="image/*" className="hidden" onChange={onPickMdImage} />
          </div>
        )}
        <Button size="middle" type="default" onClick={aiWrite.openModal} title="空行输入 / 打开命令清单"><Sparkles className="h-3.5 w-3.5 mr-1" />AI 编写</Button>
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
                  onKeyDown={aiWrite.onEditorKeyDown}
                  placeholder="文章正文 (Markdown, 支持表格/任务列表等 GFM 语法，空行输入 / 唤起 AI 编写)"
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

      {/* AI 辅助编写弹窗(工具栏按钮 / 编辑器空行输入 / 命令清单选中「AI 编写」), 生成结果回填正文光标处 */}
      <AIWriteModal {...aiWrite.modalProps} />
      {aiWrite.slashMenu}

      {/* 创建去重确认(P3-1): 列出相似文章, 由作者决定是否仍然创建 */}
      <ConfirmDialog
        open={dupOpen}
        onClose={() => { setDupOpen(false); setDupCandidates([]) }}
        onConfirm={async () => {
          setDupOpen(false)
          const status = pendingStatus
          setDupCandidates([])
          await doSave(status, true)
        }}
        title="发现相似的知识文章"
        description={[
          `同一知识域内已存在 ${dupCandidates.length} 篇相似文章：`,
          dupCandidates.map(d => `${d.title}（${DUP_REASON_LABEL[d.reason] || '相似'}，当前${statusLabel(d.status)}）`).join('、'),
          '重复录入会让 AI 检索时同一主题互相抢排名，建议先打开已有文章补充内容；确认仍要创建吗？',
        ].join(' ')}
        confirmLabel="仍然创建"
        loading={saving}
      />
    </div>
  )
}
