'use client'

import { useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { useKnowledgeStore, type Category, type PrecipitationRule } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { formatTime, apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Plus, Trash2, BookOpen, Pencil, Sparkles, FolderPlus, Upload, Loader2, CheckCircle2, Bookmark, Bell, Settings2, Eye, PenLine, AlignLeft } from 'lucide-react'
import { cn } from '@/lib/utils'
import { MarkdownViewer } from '@/components/markdown-viewer'
import { RichTextEditor } from '@/components/rich-text-editor'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useAuthStore } from '@/stores/auth'

const getKBUser = () => useAuthStore.getState().user?.username || 'anonymous'

/** 轻量 Markdown 清洗: 去掉标记符号, 得到适合列表摘要的纯文本 */
function stripMarkdown(s: string): string {
  return (s || '')
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/^#{1,6}\s+/gm, '')
    .replace(/^\s*[-*+]\s+/gm, '')
    .replace(/^\s*\d+[.)]\s+/gm, '')
    .replace(/^\s*>\s?/gm, '')
    .replace(/^[-*_]{3,}\s*$/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\*([^*]+)\*/g, '$1')
    .replace(/~~([^~]+)~~/g, '$1')
    .replace(/\s+/g, ' ')
    .trim()
}

const PRECIP_SOURCES = [
  { value: 'itsm', label: 'ITSM 工单' },
  { value: 'alert', label: '监控告警' },
  { value: 'chat', label: '会话/群聊' },
  { value: 'manual', label: '手动触发' },
]

export default function KnowledgeListPage() {
  const router = useRouter()
  const store = useKnowledgeStore()
  const {
    articles, fetch, create, remove, categories, categoryList, fetchCategories,
    saveCategory, updateCategory, deleteCategory, importFile,
    listBookmarks, listFollows, listFollowUpdates, markFollowUpdateRead,
    listPrecipitationRules, savePrecipitationRule, deletePrecipitationRule, runPrecipitationRule,
  } = store
  const [dialogOpen, setDialogOpen] = useState(false)
  const [catDialog, setCatDialog] = useState(false)
  const [catForm, setCatForm] = useState({ id: '', name: '', description: '', sort_order: 0, color: '' })
  const [form, setForm] = useState({ title: '', content: '', category: '', tags: '', author: '', status: 'draft' })
  const [filterCat, setFilterCat] = useState('')
  const [searchQ, setSearchQ] = useState('')
  const [importing, setImporting] = useState(false)
  const importRef = useRef<HTMLInputElement | null>(null)

  // 视图过滤: 全部 / 我的收藏 / 我关注的
  const [view, setView] = useState<'all' | 'bookmarks' | 'follows'>('all')
  const [bookmarkIds, setBookmarkIds] = useState<string[]>([])
  const [followIds, setFollowIds] = useState<string[]>([])
  const [followUpdateIds, setFollowUpdateIds] = useState<string[]>([])

  // 新建文章弹窗: 增强编辑器状态 (实时分栏预览, 不再用 编辑/预览 Tab 切换)
  const [dialogInputMode, setDialogInputMode] = useState<'markdown' | 'rich'>('markdown')
  const [dialogAiPrompt, setDialogAiPrompt] = useState('')
  const [dialogAiGen, setDialogAiGen] = useState(false)
  const [formError, setFormError] = useState('')

  // 自动沉淀策略
  const [precipOpen, setPrecipOpen] = useState(false)
  const [precipRules, setPrecipRules] = useState<PrecipitationRule[]>([])
  const [precipLoading, setPrecipLoading] = useState(false)
  const [precipForm, setPrecipForm] = useState<Partial<PrecipitationRule> | null>(null)
  const [precipSaving, setPrecipSaving] = useState(false)
  const [precipRunId, setPrecipRunId] = useState('')

  useEffect(() => { fetch(filterCat) }, [filterCat])
  useEffect(() => { fetchCategories() }, [fetchCategories])
  // 加载收藏/关注 ID, 供过滤与计数徽标使用
  useEffect(() => {
    listBookmarks(getKBUser()).then(setBookmarkIds)
    listFollows(getKBUser()).then(setFollowIds)
  }, [listBookmarks, listFollows])
  useEffect(() => {
    if (view === 'follows') {
      listFollowUpdates(getKBUser(), false).then(u => setFollowUpdateIds(u.map(x => x.article_id)))
    }
  }, [view, listFollowUpdates])

  const loadPrecip = async () => {
    setPrecipLoading(true)
    const rules = await listPrecipitationRules()
    setPrecipRules(rules)
    setPrecipLoading(false)
  }
  useEffect(() => { if (precipOpen) loadPrecip() }, [precipOpen, listPrecipitationRules])

  const handleSave = async () => {
    if (!form.title.trim()) { setFormError('请填写文章标题'); return }
    if (!form.content.trim()) { setFormError('请填写文章内容'); return }
    const data = { ...form, tags: form.tags.split(',').map((t: string) => t.trim()).filter(Boolean) }
    const ok = await create(data)
    if (ok) {
      setDialogOpen(false)
      setForm({ title: '', content: '', category: categories[0] || '', tags: '', author: getKBUser(), status: 'draft' })
      setFormError('')
    }
  }

  const handleSaveCategory = async () => {
    if (!catForm.name.trim()) return toast.warning('请输入分类名称')
    if (catForm.id) {
      await updateCategory(catForm.id, catForm)
    } else {
      await saveCategory(catForm)
    }
    setCatForm({ id: '', name: '', description: '', sort_order: 0, color: '' })
  }

  const handleDeleteCategory = async (c: Category) => {
    if (!confirm(`删除分类「${c.name}」？引用该分类的文章分类将被清空`)) return
    await deleteCategory(c.id)
  }

  const handleDialogAIGenerate = async () => {
    if (!dialogAiPrompt.trim()) { toast.error('请输入AI提示词'); return }
    setDialogAiGen(true)
    try {
      const res = await apiFetch<{ content: string }>('/ai/chat', {
        method: 'POST',
        body: JSON.stringify({
          prompt: `写一篇运维知识库文章,题目是"${form.title || dialogAiPrompt}"。请生成Markdown格式的完整文章,包含背景、步骤、注意事项等。直接输出文章正文,不要加任何前缀说明。`,
          stream: false,
        }),
      })
      const content = res.content || ''
      if (content) {
        setForm(f => ({ ...f, content: f.content ? f.content + '\n\n' + content : content }))
        toast.success('AI 内容已生成')
      } else {
        toast.error('AI 未返回内容')
      }
    } catch (e: any) {
      toast.error('AI生成失败: ' + e.message)
    }
    setDialogAiGen(false)
  }

  const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!file) return
    setImporting(true)
    try {
      const res = await importFile(file, form.category || categories[0] || '')
      if (res.imported > 0) toast.success(`成功导入 ${res.imported} 篇`)
      if (res.errors.length > 0) toast.error(`导入错误 ${res.errors.length} 条: ${res.errors[0]}`)
    } catch (err: any) {
      toast.error(`导入失败: ${err.message}`)
    } finally {
      setImporting(false)
    }
  }

  const savePrecip = async () => {
    if (!precipForm || !precipForm.name?.trim()) { toast.warning('请填写策略名称'); return }
    setPrecipSaving(true)
    try {
      await savePrecipitationRule(precipForm)
      setPrecipForm(null)
      await loadPrecip()
    } catch (e: any) {
      toast.error('保存失败: ' + e.message)
    }
    setPrecipSaving(false)
  }

  const runPrecip = async (id: string) => {
    setPrecipRunId(id)
    await runPrecipitationRule(id, {})
    setPrecipRunId('')
  }

  // 列表过滤: 搜索 + 视图(收藏/关注)
  const visible = articles.filter(a => {
    if (searchQ && !(a.title.includes(searchQ) || (a.content || '').includes(searchQ))) return false
    if (view === 'bookmarks' && !bookmarkIds.includes(a.id)) return false
    if (view === 'follows' && !followIds.includes(a.id)) return false
    return true
  })

  return <div className="p-6 space-y-4">
    <input ref={importRef} type="file" accept=".md,.markdown,.txt,.json,.pdf,.docx" className="hidden" onChange={handleImportFile} />

    <div className="flex items-center justify-between">
      <div><h1 className="text-xl font-bold">知识库</h1><p className="text-xs text-muted-foreground">运维文档、故障处理、最佳实践，AI自动检索辅助问答</p></div>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => importRef.current?.click()} disabled={importing}>
          {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Upload className="h-4 w-4 mr-1" />}导入
        </Button>
        <Button size="sm" variant="outline" onClick={() => setCatDialog(true)}><FolderPlus className="h-4 w-4 mr-1" />分类管理</Button>
        <Button size="sm" variant="outline" onClick={() => setPrecipOpen(true)}><Settings2 className="h-4 w-4 mr-1" />沉淀策略</Button>
        <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建文章</Button>
        <Button size="sm" variant="outline" onClick={() => router.push('/knowledge/new')}><Sparkles className="h-4 w-4 mr-1" />AI写作</Button>
      </div>
    </div>

    <div className="flex gap-2 flex-wrap items-center">
      {/* 视图过滤: 全部 / 我的收藏 / 我关注的 */}
      <div className="flex gap-1 rounded-lg border border-border p-1 bg-background">
        <Button variant={view === 'all' ? 'default' : 'ghost'} size="sm" className="h-7 text-xs" onClick={() => setView('all')}>全部</Button>
        <Button variant={view === 'bookmarks' ? 'default' : 'ghost'} size="sm" className="h-7 text-xs gap-1" onClick={() => setView('bookmarks')}>
          <Bookmark className="h-3 w-3" />我的收藏{bookmarkIds.length > 0 && <span className="ml-0.5 text-[10px]">{bookmarkIds.length}</span>}
        </Button>
        <Button variant={view === 'follows' ? 'default' : 'ghost'} size="sm" className="h-7 text-xs gap-1" onClick={() => setView('follows')}>
          <Bell className="h-3 w-3" />我关注的{followIds.length > 0 && <span className="ml-0.5 text-[10px]">{followIds.length}</span>}
        </Button>
      </div>
      {categories.map(c => <Button key={c} variant={filterCat === c ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat(c)}>{c}</Button>)}
      <Input className="w-48 h-8 ml-auto" placeholder="搜索..." value={searchQ} onChange={e => setSearchQ(e.target.value)} />
    </div>

    <div className="grid gap-3 md:grid-cols-2">
      {visible.length === 0 ? <Card className="col-span-2"><CardContent className="py-10 text-center text-muted-foreground"><BookOpen className="h-10 w-10 mx-auto mb-2 opacity-30" /><p>暂无文章</p></CardContent></Card> :
        visible.map(a => {
          const hasUpdate = view === 'follows' && followUpdateIds.includes(a.id)
          return <Card key={a.id} className="cursor-pointer hover:border-primary/50" onClick={() => { router.push(`/knowledge/${a.id}`); if (hasUpdate) markFollowUpdateRead(a.id, getKBUser()) }}>
            <CardContent className="p-4">
              <div className="flex items-start justify-between gap-2">
                <h3 className="font-medium text-sm truncate flex-1 flex items-center gap-1">
                  {hasUpdate && <Bell className="h-3 w-3 text-amber-500 shrink-0" />}
                  {a.title}
                </h3>
                <div className="flex items-center gap-1 shrink-0">
                  {a.status === 'published'
                    ? <Badge variant="success" className="text-[10px]"><CheckCircle2 className="h-2.5 w-2.5 mr-0.5" />已发布</Badge>
                    : <Badge variant="warning" className="text-[10px]">草稿</Badge>}
                  <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); router.push(`/knowledge/${a.id}`) }}><Pencil className="h-3 w-3" /></Button>
                  <Button variant="ghost" size="icon" className="h-6 w-6 text-red-400" onClick={e => { e.stopPropagation(); confirm('删除?') && remove(a.id) }}><Trash2 className="h-3 w-3" /></Button>
                </div>
              </div>
              <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{a.content ? stripMarkdown(a.content).slice(0, 120) : ''}</p>
              <div className="flex gap-2 mt-2">
                <Badge variant="outline" className="text-[10px]">{a.category}</Badge>
                <span className="text-[10px] text-muted-foreground">v{a.version}</span>
                <span className="text-[10px] text-muted-foreground">{formatTime(a.created_at)}</span>
              </div>
            </CardContent>
          </Card>
        })}
    </div>

    {/* 新建文章弹窗(升级版: 宽屏 + Markdown/富文本 + 实时预览 + 格式校验 + AI 伴写) */}
    {dialogOpen && <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="fixed inset-0 bg-black/50" onClick={() => setDialogOpen(false)} />
      <div className="relative z-50 w-full max-w-5xl max-h-[92vh] overflow-y-auto bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold">新建文章</h2>
          <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setDialogOpen(false)}>✕</Button>
        </div>
        <div className="grid md:grid-cols-3 gap-4">
          <div className="md:col-span-2 space-y-3">
            <Input placeholder="文章标题 *" value={form.title} onChange={e => { setForm({ ...form, title: e.target.value }); if (formError) setFormError('') }} className={cn(!form.title && formError && 'border-red-500')} />
            <div className="flex gap-2">
              <select className="flex h-10 flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.category} onChange={e => setForm({ ...form, category: e.target.value })}>
                {categories.map(c => <option key={c} value={c}>{c}</option>)}
              </select>
              <select className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.status} onChange={e => setForm({ ...form, status: e.target.value })}>
                <option value="draft">草稿</option>
                <option value="published">发布</option>
              </select>
            </div>
            <Input placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({ ...form, tags: e.target.value })} />
            <div className="flex flex-wrap items-center gap-2">
              <Tabs value={dialogInputMode} onValueChange={v => setDialogInputMode(v as 'markdown' | 'rich')}>
                <TabsList className="h-8">
                  <TabsTrigger value="markdown" className="text-xs py-1"><PenLine className="h-3 w-3 mr-1" />Markdown</TabsTrigger>
                  <TabsTrigger value="rich" className="text-xs py-1"><AlignLeft className="h-3 w-3 mr-1" />富文本</TabsTrigger>
                </TabsList>
              </Tabs>
              <div className="flex-1" />
              <span className="text-[10px] text-muted-foreground flex items-center gap-1"><Eye className="h-3 w-3" />右侧实时预览</span>
            </div>
            {/* 编辑 + 实时预览 分栏: 始终可见, 避免切到预览页才看到、且像"没渲染"的困惑 */}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              {/* 左: 编辑区 */}
              <div className="border rounded-md overflow-hidden">
                {dialogInputMode === 'rich'
                  ? <RichTextEditor value={form.content} onChange={md => setForm(f => ({ ...f, content: md }))} minHeight="360px" />
                  : <textarea className={cn('flex min-h-[360px] w-full bg-background px-3 py-2 text-sm font-mono focus-visible:outline-none', !form.content && formError && 'border border-red-500')} value={form.content} onChange={e => { setForm({ ...form, content: e.target.value }); if (formError) setFormError('') }} placeholder="文章正文 (Markdown, 支持表格/任务列表等 GFM 语法)" />}
              </div>
              {/* 右: 预览区, 兼容 Markdown / 富文本两种录入模式 */}
              <div className="rounded-md border border-border bg-background p-4 md:p-6 overflow-y-auto max-h-[480px]">
                {dialogInputMode === 'rich'
                  ? <RichTextEditor value={form.content} onChange={() => {}} readOnly minHeight="360px" />
                  : (form.content.trim() ? <MarkdownViewer content={form.content} /> : <p className="text-xs text-muted-foreground italic">（空内容）</p>)}
              </div>
            </div>
            {formError && <p className="text-xs text-red-500">{formError}</p>}
          </div>

          <Card><CardContent className="p-4 space-y-3">
            <h3 className="text-sm font-medium flex items-center gap-1"><Sparkles className="h-4 w-4 text-amber-500" />AI 伴写</h3>
            <p className="text-xs text-muted-foreground">输入提示词让AI帮你生成文章内容, 会替换当前正文</p>
            <textarea className="flex h-24 w-full rounded-md border border-input bg-background px-2 py-1 text-xs mt-1" value={dialogAiPrompt} onChange={e => setDialogAiPrompt(e.target.value)} placeholder="例如: 介绍一下Linux磁盘空间清理的步骤和注意事项" />
            <Button size="sm" className="w-full" onClick={handleDialogAIGenerate} disabled={dialogAiGen}>
              {dialogAiGen ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Sparkles className="h-3.5 w-3.5 mr-1" />}
              {dialogAiGen ? '生成中...' : 'AI 生成内容'}
            </Button>
            <div className="text-[10px] text-muted-foreground pt-2 border-t">
              <p>提示: 先写好标题, 再让AI根据标题生成正文。可多次生成持续补充。</p>
            </div>
          </CardContent></Card>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleSave} disabled={!form.title}>保存</Button>
        </div>
      </div>
    </div>}

    {/* 分类管理弹窗 */}
    {catDialog && <div className="fixed inset-0 z-50 flex items-center justify-center"><div className="fixed inset-0 bg-black/50" onClick={() => setCatDialog(false)} />
      <div className="relative z-50 w-full max-w-md bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
        <h2 className="text-lg font-semibold mb-4 flex items-center gap-2"><FolderPlus className="h-5 w-5 text-emerald-500" />分类管理</h2>
        <div className="space-y-2 max-h-60 overflow-y-auto">
          {categories.map(name => {
            const cat = categoryList.find(c => c.name === name)
            const hasArticles = articles.some(a => a.category === name)
            return (
              <div key={cat?.id || name} className="flex items-center justify-between px-3 py-2 rounded-lg border border-gray-200 dark:border-gray-800">
                <div className="flex items-center gap-2 min-w-0">
                  <div className="w-3 h-3 rounded-full shrink-0" style={{ backgroundColor: cat?.color || '#6b7280' }} />
                  <span className="text-sm truncate">{name}</span>
                  <span className="text-[10px] text-muted-foreground">{hasArticles ? '有文章' : '空'}</span>
                </div>
                <div className="flex gap-1 shrink-0">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6"
                    onClick={() => setCatForm({ id: cat?.id || '', name, description: cat?.description || '', sort_order: cat?.sort_order || 0, color: cat?.color || '' })}
                    title="编辑"
                  >
                    <Pencil className="h-3 w-3" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6 text-red-400"
                    onClick={() => {
                      if (!confirm(`删除分类「${name}」？引用该分类的文章分类将被清空`)) return
                      handleDeleteCategory({ id: cat?.id || '', name, sort_order: 0 } as Category)
                    }}
                    title="删除"
                    disabled={!cat?.id}
                  >
                    <Trash2 className="h-3 w-3" />
                  </Button>
                </div>
              </div>
            )
          })}
        </div>
        <div className="space-y-2 mt-4 border-t pt-4">
          <Input placeholder="新分类名称" value={catForm.name} onChange={e => setCatForm({ ...catForm, name: e.target.value })} />
          <Input placeholder="描述(可选)" value={catForm.description} onChange={e => setCatForm({ ...catForm, description: e.target.value })} />
          <div className="flex gap-2">
            <input
              type="color"
              value={catForm.color || '#10b981'}
              onChange={e => setCatForm({ ...catForm, color: e.target.value })}
              className="w-8 h-8 rounded cursor-pointer border-0"
            />
            <span className="text-xs text-muted-foreground self-center">选择颜色</span>
          </div>
          <Button size="sm" className="w-full" onClick={handleSaveCategory} disabled={!catForm.name.trim()}>
            <Plus className="h-4 w-4 mr-1" />{catForm.id ? '更新分类' : '添加分类'}
          </Button>
        </div>
        <div className="flex justify-end mt-4"><Button variant="outline" onClick={() => setCatDialog(false)}>关闭</Button></div>
      </div>
    </div>}

    {/* 自动沉淀策略弹窗 */}
    {precipOpen && <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div className="fixed inset-0 bg-black/50" onClick={() => setPrecipOpen(false)} />
      <div className="relative z-50 w-full max-w-3xl max-h-[92vh] overflow-y-auto bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h2 className="text-lg font-semibold flex items-center gap-2"><Settings2 className="h-5 w-5 text-indigo-500" />自动沉淀策略</h2>
            <p className="text-xs text-muted-foreground mt-0.5">定义从运维事件(工单/告警/会话)自动沉淀为知识库文章的规则</p>
          </div>
          <div className="flex gap-2">
            {precipForm === null && <Button size="sm" onClick={() => setPrecipForm({ name: '', source: 'itsm', category: categories[0] || '', title_template: '', content_template: '', enabled: true, auto_publish: false })}><Plus className="h-4 w-4 mr-1" />新建策略</Button>}
            <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setPrecipOpen(false)}>✕</Button>
          </div>
        </div>

        {precipLoading && precipRules.length === 0 ? <div className="py-10 text-center text-muted-foreground"><Loader2 className="h-6 w-6 mx-auto animate-spin" /></div> :
          precipForm !== null ? (
            <div className="space-y-3">
              <Input placeholder="策略名称 *" value={precipForm.name || ''} onChange={e => setPrecipForm({ ...precipForm, name: e.target.value })} />
              <div className="flex gap-2">
                <select className="flex h-10 flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm" value={precipForm.source || 'itsm'} onChange={e => setPrecipForm({ ...precipForm, source: e.target.value })}>
                  {PRECIP_SOURCES.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
                </select>
                <select className="flex h-10 flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm" value={precipForm.category || ''} onChange={e => setPrecipForm({ ...precipForm, category: e.target.value })}>
                  {categories.map(c => <option key={c} value={c}>{c}</option>)}
                </select>
              </div>
              <Input placeholder="标题模板, 支持 {{变量}} 占位, 如 关于{{对象}}的{{事件}}处理" value={precipForm.title_template || ''} onChange={e => setPrecipForm({ ...precipForm, title_template: e.target.value })} />
              <textarea className="flex min-h-[160px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono" value={precipForm.content_template || ''} onChange={e => setPrecipForm({ ...precipForm, content_template: e.target.value })} placeholder="正文模板(Markdown), 支持 {{变量}} 占位" />
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!precipForm.enabled} onChange={e => setPrecipForm({ ...precipForm, enabled: e.target.checked })} />启用</label>
              <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={!!precipForm.auto_publish} onChange={e => setPrecipForm({ ...precipForm, auto_publish: e.target.checked })} />沉淀后自动发布(否则存为草稿)</label>
              <div className="flex justify-end gap-2 pt-2">
                <Button variant="outline" onClick={() => setPrecipForm(null)}>取消</Button>
                <Button onClick={savePrecip} disabled={precipSaving}>{precipSaving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : null}保存</Button>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              {precipRules.length === 0 && <p className="text-sm text-muted-foreground text-center py-8">暂无沉淀策略, 点击右上角"新建策略"</p>}
              {precipRules.map(r => (
                <div key={r.id} className="flex items-center justify-between px-3 py-2.5 rounded-lg border border-gray-200 dark:border-gray-800">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium truncate">{r.name}</span>
                      <Badge variant="outline" className="text-[10px]">{PRECIP_SOURCES.find(s => s.value === r.source)?.label || r.source}</Badge>
                      {r.auto_publish && <Badge variant="secondary" className="text-[10px]">自动发布</Badge>}
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-0.5 truncate">落库到「{r.category || '未指定'}」 · {r.enabled ? '已启用' : '已停用'}</p>
                  </div>
                  <div className="flex gap-1 shrink-0">
                    <Button variant="outline" size="sm" className="h-7 text-[11px]" onClick={() => runPrecip(r.id || '')} disabled={precipRunId === r.id}>{precipRunId === r.id ? <Loader2 className="h-3 w-3 mr-0.5 animate-spin" /> : null}运行</Button>
                    <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => setPrecipForm({ ...r })} title="编辑"><Pencil className="h-3 w-3" /></Button>
                    <Button variant="ghost" size="icon" className="h-7 w-7 text-red-400" onClick={async () => { if (confirm('删除该策略?') && r.id) { await deletePrecipitationRule(r.id); await loadPrecip() } }} title="删除"><Trash2 className="h-3 w-3" /></Button>
                  </div>
                </div>
              ))}
            </div>
          )}
      </div>
    </div>}
  </div>
}
