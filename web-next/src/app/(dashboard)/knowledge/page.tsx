'use client'

import { useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { useKnowledgeStore } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, BookOpen, Search, Pencil, Sparkles } from 'lucide-react'

const CATEGORIES = ['常见问题', '变更操作', '故障处理', '最佳实践', '参考文档']

export default function KnowledgeListPage() {
  const router = useRouter()
  const { articles, fetch, create, remove } = useKnowledgeStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' })
  const [filterCat, setFilterCat] = useState('')
  const [searchQ, setSearchQ] = useState('')
  const [viewArticle, setViewArticle] = useState<any>(null)

  useEffect(() => { fetch(filterCat) }, [filterCat])

  const handleSave = async () => {
    const data = { ...form, tags: form.tags.split(',').map((t: string) => t.trim()).filter(Boolean) }
    await create(data)
    setDialogOpen(false)
    setForm({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' })
  }

  const filtered = articles.filter(a => !searchQ || a.title.includes(searchQ) || (a.content||'').includes(searchQ))

  return <div className="p-6 space-y-4">
    <div className="flex items-center justify-between">
      <div><h1 className="text-xl font-bold">知识库</h1><p className="text-xs text-muted-foreground">运维文档、故障处理、最佳实践，AI自动检索辅助问答</p></div>
      <div className="flex gap-2">
        <Button size="sm" onClick={() => setDialogOpen(true)}><Plus className="h-4 w-4 mr-1"/>新建文章</Button>
        <Button size="sm" variant="outline" onClick={() => router.push('/knowledge/new')}><Sparkles className="h-4 w-4 mr-1"/>AI写作</Button>
      </div>
    </div>

    <div className="flex gap-2 flex-wrap">
      <Button variant={filterCat === '' ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat('')}>全部</Button>
      {CATEGORIES.map(c => <Button key={c} variant={filterCat === c ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat(c)}>{c}</Button>)}
      <Input className="w-48 h-8 ml-auto" placeholder="搜索..." value={searchQ} onChange={e => setSearchQ(e.target.value)}/>
    </div>

    <div className="grid gap-3 md:grid-cols-2">
      {filtered.length === 0 ? <Card className="col-span-2"><CardContent className="py-10 text-center text-muted-foreground"><BookOpen className="h-10 w-10 mx-auto mb-2 opacity-30"/><p>暂无文章</p></CardContent></Card> :
      filtered.map(a => <Card key={a.id} className="cursor-pointer hover:border-primary/50" onClick={() => router.push(`/knowledge/${a.id}`)}>
        <CardContent className="p-4">
          <div className="flex items-start justify-between gap-2">
            <h3 className="font-medium text-sm truncate flex-1">{a.title}</h3>
            <div className="flex gap-1 shrink-0">
              <Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); router.push(`/knowledge/${a.id}`) }}><Pencil className="h-3 w-3"/></Button>
              <Button variant="ghost" size="icon" className="h-6 w-6 text-red-400" onClick={e => { e.stopPropagation(); confirm('删除?') && remove(a.id) }}><Trash2 className="h-3 w-3"/></Button>
            </div>
          </div>
          <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{a.content?.slice(0, 120)}</p>
          <div className="flex gap-2 mt-2"><Badge variant="outline" className="text-[10px]">{a.category}</Badge><span className="text-[10px] text-muted-foreground">{formatTime(a.created_at)}</span></div>
        </CardContent>
      </Card>)}
    </div>

    {viewArticle && <div className="fixed inset-0 z-50 flex items-center justify-center"><div className="fixed inset-0 bg-black/50" onClick={() => setViewArticle(null)}/>
      <div className="relative z-50 w-full max-w-2xl bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg max-h-[80vh] overflow-y-auto">
        <h2 className="text-lg font-bold mb-3">{viewArticle.title}</h2>
        <div className="text-sm whitespace-pre-wrap">{viewArticle.content}</div>
        <Button variant="outline" className="mt-4" onClick={() => setViewArticle(null)}>关闭</Button>
      </div>
    </div>}

    {dialogOpen && <div className="fixed inset-0 z-50 flex items-center justify-center"><div className="fixed inset-0 bg-black/50" onClick={() => setDialogOpen(false)}/>
      <div className="relative z-50 w-full max-w-xl bg-white dark:bg-gray-900 rounded-xl border p-6 shadow-lg">
        <h2 className="text-lg font-semibold mb-4">新建文章</h2>
        <div className="space-y-3">
          <Input placeholder="标题" value={form.title} onChange={e => setForm({...form, title: e.target.value})}/>
          <select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.category} onChange={e => setForm({...form, category: e.target.value})}>{CATEGORIES.map(c => <option key={c}>{c}</option>)}</select>
          <textarea className="flex h-40 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.content} onChange={e => setForm({...form, content: e.target.value})} placeholder="文章内容 (Markdown)"/>
          <Input placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({...form, tags: e.target.value})}/>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.title}>保存</Button></div>
      </div>
    </div>}
  </div>
}
