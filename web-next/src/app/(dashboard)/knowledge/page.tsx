'use client'

import { useEffect, useRef, useState } from 'react'
import { useRouter } from 'next/navigation'
import { useKnowledgeStore, type Category } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { formatTime } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Plus, Trash2, BookOpen, Search, Pencil, Sparkles, FolderPlus, Upload, Loader2, CheckCircle2 } from 'lucide-react'
import { cn } from '@/lib/utils'

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

export default function KnowledgeListPage() {
  const router = useRouter()
  const store = useKnowledgeStore()
  const { articles, fetch, create, remove, categories, categoryList, fetchCategories, saveCategory, updateCategory, deleteCategory, importArticles, importFile } = store
  const [dialogOpen, setDialogOpen] = useState(false)
  const [catDialog, setCatDialog] = useState(false)
  const [catForm, setCatForm] = useState({ id: '', name: '', description: '', sort_order: 0, color: '' })
  const [form, setForm] = useState({ title: '', content: '', category: '', tags: '', author: 'admin', status: 'draft' })
  const [filterCat, setFilterCat] = useState('')
  const [searchQ, setSearchQ] = useState('')
  const [importing, setImporting] = useState(false)
  const importRef = useRef<HTMLInputElement | null>(null)

  useEffect(() => { fetch(filterCat) }, [filterCat])
  useEffect(() => { fetchCategories() }, [fetchCategories])

  const handleSave = async () => {
    const data = { ...form, tags: form.tags.split(',').map((t: string) => t.trim()).filter(Boolean) }
    await create(data)
    setDialogOpen(false)
    setForm({ title: '', content: '', category: categories[0] || '', tags: '', author: 'admin', status: 'draft' })
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

  // 文件导入: 上传原始文件, 后端解析 .md/.txt/.json/.pdf/.docx
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

  const filtered = articles.filter(a => !searchQ || a.title.includes(searchQ) || (a.content || '').includes(searchQ))

  return <div className="p-6 space-y-4">
    {/* 隐藏文件输入 */}
    <input ref={importRef} type="file" accept=".md,.markdown,.txt,.json,.pdf,.docx" className="hidden" onChange={handleImportFile} />

    <div className="flex items-center justify-between">
      <div><h1 className="text-xl font-bold">知识库</h1><p className="text-xs text-muted-foreground">运维文档、故障处理、最佳实践，AI自动检索辅助问答</p></div>
      <div className="flex gap-2">
        <Button size="sm" variant="outline" onClick={() => importRef.current?.click()} disabled={importing}>
          {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Upload className="h-4 w-4 mr-1" />}导入
        </Button>
        <Button size="sm" variant="outline" onClick={() => setCatDialog(true)}><FolderPlus className="h-4 w-4 mr-1" />分类管理</Button>
        <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1" />新建文章</Button>
        <Button size="sm" variant="outline" onClick={() => router.push('/knowledge/new')}><Sparkles className="h-4 w-4 mr-1" />AI写作</Button>
      </div>
    </div>

    <div className="flex gap-2 flex-wrap">
      <Button variant={filterCat === '' ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat('')}>全部</Button>
      {categories.map(c => <Button key={c} variant={filterCat === c ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat(c)}>{c}</Button>)}
      <Input className="w-48 h-8 ml-auto" placeholder="搜索..." value={searchQ} onChange={e => setSearchQ(e.target.value)}/>
    </div>

    <div className="grid gap-3 md:grid-cols-2">
      {filtered.length === 0 ? <Card className="col-span-2"><CardContent className="py-10 text-center text-muted-foreground"><BookOpen className="h-10 w-10 mx-auto mb-2 opacity-30" /><p>暂无文章</p></CardContent></Card> :
        filtered.map(a => <Card key={a.id} className="cursor-pointer hover:border-primary/50" onClick={() => router.push(`/knowledge/${a.id}`)}>
          <CardContent className="p-4">
            <div className="flex items-start justify-between gap-2">
              <h3 className="font-medium text-sm truncate flex-1">{a.title}</h3>
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
        </Card>)}
    </div>

    {/* 新建文章弹窗 */}
    {dialogOpen && <div className="fixed inset-0 z-50 flex items-center justify-center"><div className="fixed inset-0 bg-black/50" onClick={() => setDialogOpen(false)} />
      <div className="relative z-50 w-full max-w-xl bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
        <h2 className="text-lg font-semibold mb-4">新建文章</h2>
        <div className="space-y-3">
          <Input placeholder="标题" value={form.title} onChange={e => setForm({ ...form, title: e.target.value })} />
          <div className="flex gap-2">
            <select className="flex h-10 flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.category} onChange={e => setForm({ ...form, category: e.target.value })}>
              {categories.map(c => <option key={c} value={c}>{c}</option>)}
            </select>
            <select className="flex h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.status} onChange={e => setForm({ ...form, status: e.target.value })}>
              <option value="draft">草稿</option>
              <option value="published">发布</option>
            </select>
          </div>
          <textarea className="flex h-40 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.content} onChange={e => setForm({ ...form, content: e.target.value })} placeholder="文章内容 (Markdown)" />
          <Input placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({ ...form, tags: e.target.value })} />
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.title}>保存</Button></div>
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
  </div>
}
