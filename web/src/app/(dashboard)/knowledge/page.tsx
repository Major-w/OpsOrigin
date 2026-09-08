
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useKnowledgeStore, type PrecipitationRule } from '@/stores/knowledge'
import { Card, CardContent } from '@/components/ui/card'
import { Button, Select, Tag, Input, Popconfirm, Modal } from 'antd'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, BookOpen, Pencil, FolderPlus, Upload, Loader2, CheckCircle2, Bookmark, Bell, Settings2, ThumbsUp, MessageSquare, CalendarOff, Eye } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth'
import { getMessage } from '@/lib/message'

const getKBUser = () => useAuthStore.getState().user?.username || 'anonymous'

const VIEW_PERM_BADGES: Record<string, string> = { team: '团队', owner: '仅自己' }

/** 判断文章是否已过期 */
const isExpired = (expiresAt?: string | null) => {
  if (!expiresAt) return false
  return new Date(expiresAt) < new Date()
}

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
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const store = useKnowledgeStore()
  const {
    articles, fetch, remove, categories, fetchCategories,
    importFile,
    listBookmarks, listFollows, listFollowUpdates, markFollowUpdateRead,
    listPrecipitationRules, savePrecipitationRule, deletePrecipitationRule, runPrecipitationRule,
    getStats,
  } = store
  const [importing, setImporting] = useState(false)
  const importRef = useRef<HTMLInputElement | null>(null)

  // 筛选条件 URL 参数化(?q=/?cat=): 支持刷新/分享/外链直达(如 AI 对话页知识范围 chip 跳转)
  const [filterCat, setFilterCat] = useState(() => searchParams.get('cat') || '')
  const [searchQ, setSearchQ] = useState(() => searchParams.get('q') || '')
  useEffect(() => {
    const p = new URLSearchParams()
    if (searchQ.trim()) p.set('q', searchQ.trim())
    if (filterCat) p.set('cat', filterCat)
    setSearchParams(p, { replace: true })
  }, [searchQ, filterCat, setSearchParams])

  // 视图过滤: 全部 / 我的收藏 / 我关注的
  const [view, setView] = useState<'all' | 'bookmarks' | 'follows'>('all')
  // 排序: 最近更新(默认) / 最近创建 / 浏览量 / 标题
  const [sortBy, setSortBy] = useState<'updated' | 'created' | 'views' | 'title'>('updated')
  const [bookmarkIds, setBookmarkIds] = useState<string[]>([])
  const [followIds, setFollowIds] = useState<string[]>([])
  const [followUpdateIds, setFollowUpdateIds] = useState<string[]>([])

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
  // 加载文章社交统计
  const [articleStats, setArticleStats] = useState<Record<string, { likes: number; comments: number }>>({})
  useEffect(() => {
    if (articles.length === 0) return
    articles.forEach(async a => {
      const s = await getStats(a.id, getKBUser())
      setArticleStats(prev => ({ ...prev, [a.id]: { likes: s.likes, comments: 0 } }))
    })
  }, [articles, getStats])
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

  const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (e.target) e.target.value = ''
    if (!file) return
    setImporting(true)
    try {
      const res = await importFile(file, categories[0] || '')
      if (res.imported > 0) getMessage().success(`成功导入 ${res.imported} 篇`)
      if (res.errors.length > 0) getMessage().error(`导入错误 ${res.errors.length} 条: ${res.errors[0]}`)
    } catch (err: any) {
      getMessage().error(`导入失败: ${err.message}`)
    } finally {
      setImporting(false)
    }
  }

  const savePrecip = async () => {
    if (!precipForm || !precipForm.name?.trim()) { getMessage().warning('请填写策略名称'); return }
    setPrecipSaving(true)
    try {
      await savePrecipitationRule(precipForm)
      setPrecipForm(null)
      await loadPrecip()
    } catch (e: any) {
      getMessage().error('保存失败: ' + e.message)
    }
    setPrecipSaving(false)
  }

  const runPrecip = async (id: string) => {
    setPrecipRunId(id)
    await runPrecipitationRule(id, {})
    setPrecipRunId('')
  }

  // 列表过滤(搜索/视图) + 排序
  const visible = articles.filter(a => {
    if (searchQ && !(a.title.includes(searchQ) || (a.content || '').includes(searchQ))) return false
    if (view === 'bookmarks' && !bookmarkIds.includes(a.id)) return false
    if (view === 'follows' && !followIds.includes(a.id)) return false
    return true
  }).sort((a, b) => {
    if (sortBy === 'views') return (b.views || 0) - (a.views || 0)
    if (sortBy === 'title') return a.title.localeCompare(b.title, 'zh')
    if (sortBy === 'created') return (b.created_at || '').localeCompare(a.created_at || '')
    return (b.updated_at || b.created_at || '').localeCompare(a.updated_at || a.created_at || '')
  })

  return <div className="p-6 space-y-4">
    <input ref={importRef} type="file" accept=".md,.markdown,.txt,.json,.pdf,.docx" className="hidden" onChange={handleImportFile} />

    <div className="flex items-center justify-between">
      <div><h1 className="text-2xl font-bold">知识库</h1><p className="text-xs text-muted-foreground">运维文档、故障处理、最佳实践，AI自动检索辅助问答</p></div>
      <div className="flex gap-2">
        <Button size="middle" type="default" onClick={() => importRef.current?.click()} disabled={importing}>
          {importing ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Upload className="h-4 w-4 mr-1" />}导入
        </Button>
        <Button size="middle" type="default" onClick={() => navigate('/knowledge/categories')}><FolderPlus className="h-4 w-4 mr-1" />分类管理</Button>
        <Button size="middle" type="default" onClick={() => setPrecipOpen(true)}><Settings2 className="h-4 w-4 mr-1" />沉淀策略</Button>
        <Button size="middle" onClick={() => navigate('/knowledge/new')}><Plus className="h-4 w-4 mr-1" />新建文章</Button>
      </div>
    </div>

    <div className="flex gap-2 flex-wrap items-center">
      {/* 视图过滤: 全部 / 我的收藏 / 我关注的 */}
      <div className="flex gap-1 rounded-lg border border-border p-1 bg-background">
        <Button type={view === 'all' ? 'primary' : 'text'} size="middle" className="h-7 text-xs" onClick={() => setView('all')}>全部</Button>
        <Button type={view === 'bookmarks' ? 'primary' : 'text'} size="middle" className="h-7 text-xs gap-1" onClick={() => setView('bookmarks')}>
          <Bookmark className="h-3 w-3" />我的收藏{bookmarkIds.length > 0 && <span className="ml-0.5 text-[10px]">{bookmarkIds.length}</span>}
        </Button>
        <Button type={view === 'follows' ? 'primary' : 'text'} size="middle" className="h-7 text-xs gap-1" onClick={() => setView('follows')}>
          <Bell className="h-3 w-3" />我关注的{followIds.length > 0 && <span className="ml-0.5 text-[10px]">{followIds.length}</span>}
        </Button>
      </div>
      {categories.map(c => <Button key={c} type={filterCat === c ? 'primary' : 'default'} size="middle" onClick={() => setFilterCat(c)}>{c}</Button>)}
      <div className="flex items-center gap-2 ml-auto">
        <Select
          className="w-32"
          value={sortBy}
          onChange={v => setSortBy(v as 'updated' | 'created' | 'views' | 'title')}
          options={[
            { value: 'updated', label: '最近更新' },
            { value: 'created', label: '最近创建' },
            { value: 'views', label: '浏览最多' },
            { value: 'title', label: '标题排序' },
          ]}
        />
        <Input className="w-44 h-8" placeholder="搜索标题/正文..." allowClear value={searchQ} onChange={e => setSearchQ(e.target.value)} />
      </div>
    </div>

    <div className="grid gap-3 md:grid-cols-2">
      {visible.length === 0 ? <Card className="col-span-2"><CardContent className="py-10 text-center text-muted-foreground"><BookOpen className="h-10 w-10 mx-auto mb-2 opacity-30" /><p>暂无文章</p></CardContent></Card> :
        visible.map(a => {
          const hasUpdate = view === 'follows' && followUpdateIds.includes(a.id)
          return <Card key={a.id} className="cursor-pointer hover:border-primary/50" onClick={() => { navigate(`/knowledge/${a.id}`); if (hasUpdate) markFollowUpdateRead(a.id, getKBUser()) }}>
            <CardContent className="p-4">
              <div className="flex items-start justify-between gap-2">
                <h3 className="font-medium text-sm truncate flex-1 flex items-center gap-1">
                  {hasUpdate && <Bell className="h-3 w-3 text-amber-500 shrink-0" />}
                  {a.title}
                </h3>
                <div className="flex items-center gap-1 shrink-0">
                  {a.status === 'published'
                    ? <Tag color="success"><CheckCircle2 className="h-2.5 w-2.5 mr-0.5" />已发布</Tag>
                    : <Tag color="warning">草稿</Tag>}
                  <Button type="text" size="middle" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={e => { e.stopPropagation(); navigate(`/knowledge/${a.id}`) }}><Pencil className="h-3 w-3" /></Button>
                  {/* 删除必须阻断冒泡, 否则点击会先触发整卡 navigate 跳详情, Popconfirm 弹层被吞 */}
                  <span onClick={e => e.stopPropagation()}>
                    <Popconfirm title="确定删除该文章？" description="删除后不可恢复" onConfirm={() => { void remove(a.id) }}>
                      <Button type="text" size="middle" className="h-6 w-6 text-red-400"><Trash2 className="h-3 w-3" /></Button>
                    </Popconfirm>
                  </span>
                </div>
              </div>
              <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{a.content ? stripMarkdown(a.content).slice(0, 120) : ''}</p>
              <div className="flex gap-2 mt-2 items-center flex-wrap">
                <Tag>{a.category}</Tag>
                {(a.tags || []).slice(0, 3).map(t => (
                  <Tag key={t} className="text-[10px] text-blue-600 dark:text-blue-400 bg-blue-500/10 border-blue-500/20">{t}</Tag>
                ))}
                <span className="text-[10px] text-muted-foreground">v{a.version}</span>
                <span className="text-[10px] text-muted-foreground">更新 {formatTime(a.updated_at || a.created_at)}</span>
                {isExpired(a.expires_at) && (
                  <Tag color="error" className="gap-0.5 text-[10px]">
                    <CalendarOff className="h-2.5 w-2.5" />已过期
                  </Tag>
                )}
                {a.view_permission && a.view_permission !== 'all' && (
                  <span className="text-[10px] text-muted-foreground">{VIEW_PERM_BADGES[a.view_permission] || a.view_permission}</span>
                )}
                <div className="flex items-center gap-2 ml-auto shrink-0">
                  {a.author && <span className="text-[10px] text-muted-foreground max-w-[80px] truncate" title={a.author}>{a.author}</span>}
                  {(a.views || 0) > 0 && (
                    <span className="flex items-center gap-0.5 text-[10px] text-muted-foreground" title="浏览量">
                      <Eye className="h-3 w-3" />{a.views}
                    </span>
                  )}
                  {articleStats[a.id]?.likes > 0 && (
                    <span className="flex items-center gap-0.5 text-[10px] text-muted-foreground">
                      <ThumbsUp className="h-2.5 w-2.5" />{articleStats[a.id].likes}
                    </span>
                  )}
                </div>
              </div>
            </CardContent>
          </Card>
        })}
    </div>

    {/* 自动沉淀策略弹窗（antd Modal，主题 token 自动适配深浅色） */}
    <Modal
      open={precipOpen}
      onCancel={() => setPrecipOpen(false)}
      width={760}
      footer={null}
      title={(
        <div className="flex items-center gap-2 text-base"><Settings2 className="h-4 w-4 text-indigo-500" />自动沉淀策略
          <span className="text-xs font-normal text-muted-foreground">定义从运维事件(工单/告警/会话)自动沉淀为知识库文章的规则</span>
        </div>
      )}
      styles={{ body: { maxHeight: '72vh', overflowY: 'auto' } }}
    >
        {precipForm === null && (
          <div className="flex justify-end mb-3">
            <Button size="small" onClick={() => setPrecipForm({ name: '', source: 'itsm', category: categories[0] || '', title_template: '', content_template: '', enabled: true, auto_publish: false })}><Plus className="h-4 w-4 mr-1" />新建策略</Button>
          </div>
        )}
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
                <Button type="default" onClick={() => setPrecipForm(null)}>取消</Button>
                <Button onClick={savePrecip} disabled={precipSaving}>{precipSaving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : null}保存</Button>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              {precipRules.length === 0 && <p className="text-sm text-muted-foreground text-center py-8">暂无沉淀策略, 点击上方"新建策略"</p>}
              {precipRules.map(r => (
                <div key={r.id} className="flex items-center justify-between px-3 py-2.5 rounded-lg border border-border">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-medium truncate">{r.name}</span>
                       <Tag>{PRECIP_SOURCES.find(s => s.value === r.source)?.label || r.source}</Tag>
                       {r.auto_publish && <Tag color="default">自动发布</Tag>}
                    </div>
                    <p className="text-[10px] text-muted-foreground mt-0.5 truncate">落库到「{r.category || '未指定'}」 · {r.enabled ? '已启用' : '已停用'}</p>
                  </div>
                  <div className="flex gap-1 shrink-0">
                    <Button type="default" size="middle" className="h-7 text-[11px]" onClick={() => runPrecip(r.id || '')} disabled={precipRunId === r.id}>{precipRunId === r.id ? <Loader2 className="h-3 w-3 mr-0.5 animate-spin" /> : null}运行</Button>
                    <Button type="text" size="middle" className="h-7 w-7 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => setPrecipForm({ ...r })} title="编辑"><Pencil className="h-3 w-3" /></Button>
                    <Popconfirm title="确定删除该策略？" onConfirm={async () => { if (r.id) { await deletePrecipitationRule(r.id); await loadPrecip() } }}>
                      <Button type="text" size="middle" className="h-7 w-7 text-red-400" title="删除"><Trash2 className="h-3 w-3" /></Button>
                    </Popconfirm>
                  </div>
                </div>
              ))}
            </div>
          )}
    </Modal>
  </div>
}
