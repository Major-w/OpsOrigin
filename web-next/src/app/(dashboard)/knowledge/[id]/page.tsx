'use client'

import { useEffect, useState } from 'react'
import { useParams, useRouter } from 'next/navigation'
import { useKnowledgeStore } from '@/stores/knowledge'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { formatTime } from '@/lib/utils'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { ChevronLeft, Save, Sparkles, Loader2, Trash2 } from 'lucide-react'

const CATEGORIES = ['常见问题', '变更操作', '故障处理', '最佳实践', '参考文档']

export default function KnowledgeEditorPage() {
  const params = useParams(); const router = useRouter()
  const isNew = params.id === 'new'
  const { articles, fetch, create, update, remove } = useKnowledgeStore()
  const [form, setForm] = useState({ title: '', content: '', category: '常见问题', tags: '', author: 'admin' })
  const [saving, setSaving] = useState(false)
  const [aiGenerating, setAiGenerating] = useState(false)
  const [aiPrompt, setAiPrompt] = useState('')
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    fetch()
    if (!isNew && !loaded) {
      const a = articles.find(a => a.id === params.id)
      if (a) { setForm({ title: a.title, content: a.content, category: a.category, tags: (a.tags||[]).join(','), author: a.author }); setLoaded(true) }
    }
  }, [articles, params.id])

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
          <select className="flex h-8 rounded-md border border-input bg-background px-2 text-xs" value={form.category} onChange={e => setForm({...form, category: e.target.value})}>{CATEGORIES.map(c => <option key={c}>{c}</option>)}</select>
          <Input placeholder="标签,逗号分隔" value={form.tags} onChange={e => setForm({...form, tags: e.target.value})} className="h-8 text-xs"/>
        </div>
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
    </div>
  </div>
}
