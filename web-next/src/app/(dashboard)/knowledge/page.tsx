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
import { Plus, Trash2, BookOpen, Search, Pencil, Sparkles, FolderPlus, Upload, Loader2, CheckCircle2, FileText } from 'lucide-react'
import { cn } from '@/lib/utils'

export default function KnowledgeListPage() {
  const router = useRouter()
  const store = useKnowledgeStore()
  const { articles, fetch, create, remove, categories, fetchCategories, saveCategory, deleteCategory, importArticles } = store
  const [dialogOpen, setDialogOpen] = useState(false)
  const [catDialog, setCatDialog] = useState(false)
  const [catForm, setCatForm] = useState({ name: '', description: '', sort_order: 0 })
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
    await saveCategory(catForm)
    setCatForm({ name: '', description: '', sort_order: 0 })
  }

  // 文件导入: .md/.txt → 单篇; .json → 数组批量
  const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!file) return
    setImporting(true)
    try {
      const text = await file.text()
      const ext = file.name.split('.').pop()?.toLowerCase()
      let items: { title: string; content: string; category?: string; tags?: string[] }[] = []
      if (ext === 'json') {
        const parsed = JSON.parse(text)
        if (!Array.isArray(parsed)) throw new Error('JSON 文件需为文章数组')
        items = parsed.map((x: any) => ({ title: String(x.title || x.name || ''), content: String(x.content || ''), category: x.category, tags: x.tags }))
      } else {
        // md/txt: 文件名作标题(去掉扩展名)
        items = [{ title: file.name.replace(/\.(md|txt|markdown)$/i, ''), content: text, category: form.category || categories[0] || '' }]
      }
      items = items.filter(x => x.title && x.content)
      if (items.length === 0) throw new Error('没有可导入的有效文章')
      const res = await importArticles(items)
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
    <input ref={importRef} type="file" accept=".md,.markdown,.txt,.json" className="hidden" onChange={handleImportFile} />

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
            <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{a.content?.slice(0, 120)}</p>
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
            const cat = articles.find(a => a.category === name)
            return (
              <div key={name} className="flex items-center justify-between px-3 py-2 rounded-lg border border-gray-200 dark:border-gray-800">
                <div className="flex items-center gap-2">
                  <FileText className="h-3.5 w-3.5 text-muted-foreground" />
                  <span className="text-sm">{name}</span>
                  <span className="text-[10px] text-muted-foreground">{cat ? '有文章' : '空'}</span>
                </div>
              </div>
            )
          })}
        </div>
        <div className="space-y-2 mt-4 border-t pt-4">
          <Input placeholder="新分类名称" value={catForm.name} onChange={e => setCatForm({ ...catForm, name: e.target.value })} />
          <Input placeholder="描述(可选)" value={catForm.description} onChange={e => setCatForm({ ...catForm, description: e.target.value })} />
          <Button size="sm" className="w-full" onClick={handleSaveCategory} disabled={!catForm.name.trim()}>
            <Plus className="h-4 w-4 mr-1" />添加分类
          </Button>
        </div>
        <div className="flex justify-end mt-4"><Button variant="outline" onClick={() => setCatDialog(false)}>关闭</Button></div>
      </div>
    </div>}
  </div>
}
