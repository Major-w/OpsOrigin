'use client'

import { useEffect, useState } from 'react'
import { useKnowledgeStore } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, BookOpen, Search } from 'lucide-react'

const CATEGORIES = ['常见问题', '变更操作', '故障处理', '最佳实践', '参考文档']

export default function KnowledgePage() {
  const { articles, fetch, create, update, remove } = useKnowledgeStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' })
  const [viewArticle, setViewArticle] = useState<any>(null)
  const [filterCat, setFilterCat] = useState('')
  const [searchQ, setSearchQ] = useState('')

  useEffect(() => { fetch(filterCat) }, [filterCat])

  const handleSave = async () => {
    const data = { ...form, tags: form.tags.split(',').map(t => t.trim()).filter(Boolean) }
    if (editId) await update(editId, data)
    else await create(data)
    setDialogOpen(false); setEditId(null); setForm({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' })
    fetch(filterCat)
  }

  const handleEdit = (a: any) => {
    setEditId(a.id); setForm({ title: a.title, content: a.content, category: a.category, tags: (a.tags || []).join(','), author: a.author })
    setDialogOpen(true)
  }

  const filtered = articles.filter(a => !searchQ || a.title.includes(searchQ) || a.content.includes(searchQ))

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">知识管理</h1><p className="text-sm text-muted-foreground">运维知识库</p></div>
        <Button onClick={() => { setEditId(null); setForm({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' }); setDialogOpen(true) }}><Plus className="h-4 w-4" />新建文章</Button>
      </div>

      <div className="flex gap-2 items-center">
        <div className="relative flex-1 max-w-sm"><Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" /><Input className="pl-8" placeholder="搜索..." value={searchQ} onChange={e => setSearchQ(e.target.value)} /></div>
        <div className="flex gap-1">{(['', ...CATEGORIES]).map(c => <Button key={c} variant={filterCat === c ? 'default' : 'outline'} size="sm" onClick={() => setFilterCat(c)}>{c || '全部'}</Button>)}</div>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        <div className="lg:col-span-1">
          <Card><CardHeader className="py-3"><CardTitle className="text-sm">文章列表</CardTitle></CardHeader>
            <CardContent className="p-0">
              {filtered.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无文章。</p> : (
                <div className="flex flex-col">
                  {filtered.map(a => (
                    <div key={a.id} onClick={() => setViewArticle(a)}
                      className={`px-4 py-3 border-b last:border-0 cursor-pointer hover:bg-accent ${viewArticle?.id === a.id ? 'bg-accent border-l-2 border-l-emerald-500' : ''}`}>
                      <div className="flex items-center justify-between"><span className="text-sm font-medium truncate">{a.title}</span>
                        <div className="flex gap-1 shrink-0"><Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); handleEdit(a) }}><span className="text-xs">✎</span></Button><Button variant="ghost" size="icon" className="h-6 w-6" onClick={e => { e.stopPropagation(); remove(a.id); setViewArticle(null) }}><Trash2 className="h-3 w-3" /></Button></div>
                      </div>
                      <div className="flex gap-1 mt-1"><Badge variant="secondary" className="text-xs">{a.category}</Badge><span className="text-xs text-muted-foreground">{a.views} 浏览 · {a.author}</span></div>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
        <div className="lg:col-span-2">
          {viewArticle ? (
            <Card><CardHeader className="py-3"><CardTitle className="text-lg">{viewArticle.title}</CardTitle>
              <p className="text-xs text-muted-foreground">{viewArticle.category} · {viewArticle.author} · {formatTime(viewArticle.created_at)} · {viewArticle.views} 浏览</p>
              {(viewArticle.tags || []).length > 0 && <div className="flex gap-1 mt-1">{(viewArticle.tags || []).map((t: string) => <Badge key={t} variant="outline" className="text-xs">{t}</Badge>)}</div>}
            </CardHeader><CardContent><div className="prose text-sm whitespace-pre-wrap">{viewArticle.content}</div></CardContent></Card>
          ) : (
            <Card className="h-full flex items-center justify-center min-h-[400px]"><CardContent className="text-center text-muted-foreground"><BookOpen className="h-10 w-10 mx-auto mb-3 opacity-30" /><p>选择一篇文章阅读</p></CardContent></Card>
          )}
        </div>
      </div>

      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editId ? '编辑文章' : '新建文章'}>
        <div className="space-y-3 max-h-[70vh] overflow-y-auto">
          <div><label className="text-sm font-medium">标题 *</label><Input value={form.title} onChange={e => setForm(f => ({ ...f, title: e.target.value }))} /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">分类</label><select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.category} onChange={e => setForm(f => ({ ...f, category: e.target.value }))}>{CATEGORIES.map(c => <option key={c} value={c}>{c}</option>)}</select></div>
            <div><label className="text-sm font-medium">标签 (逗号分隔)</label><Input value={form.tags} onChange={e => setForm(f => ({ ...f, tags: e.target.value }))} placeholder="linux,mysql" /></div>
          </div>
          <div><label className="text-sm font-medium">内容</label><textarea className="flex h-48 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={form.content} onChange={e => setForm(f => ({ ...f, content: e.target.value }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.title}>{editId ? '保存' : '创建'}</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
