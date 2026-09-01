
import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button, Input, Tag, Select, Space, Modal, Popconfirm,  Tooltip } from 'antd'
import { ReloadOutlined, PlusOutlined, DeleteOutlined, EditOutlined, EyeOutlined, DownloadOutlined, StarOutlined, ThunderboltOutlined, CheckCircleOutlined, CloudOutlined, ToolOutlined } from '@ant-design/icons'
import { apiFetch, cn } from '@/lib/utils'
import {
  Store, RefreshCw, Download, Upload, Trash2, Package, User, Search,
  Heart, Star, GitBranch,
} from 'lucide-react'
import { getMessage } from '@/lib/message'

// 技能市场 (L4: MCP 打包共享, 数字员工 + 依赖工具打包成可分享的市场形态)
interface AgentPayload {
  name: string
  model_name?: string
  system_prompt?: string
  knowledge_categories?: string[]
  tools?: string[]
  skills?: { name: string; description?: string; instructions?: string }[]
}
interface CustomToolPayload {
  name: string
  description: string
  type: string
  enabled: boolean
}
interface SkillPackage {
  id: string
  tenant_id: string
  name: string
  version: string
  author?: string
  description?: string
  category?: string
  tags?: string[]
  agent: AgentPayload
  tools?: CustomToolPayload[]
  downloads: number
  rating_avg?: number
  rating_count?: number
  checksum: string
  created_at: string
  updated_at: string
}

export default function MarketplacePage() {
  const [packages, setPackages] = useState<SkillPackage[]>([])
  const [loading, setLoading] = useState(false)
  const [category, setCategory] = useState('')
  const [keyword, setKeyword] = useState('')
  const [sort, setSort] = useState<'updated' | 'rating' | 'downloads'>('updated')
  const [favOnly, setFavOnly] = useState(false)
  const [favIds, setFavIds] = useState<Set<string>>(new Set())

  // 导入对话框
  const [importOpen, setImportOpen] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importFile, setImportFile] = useState<File | null>(null)

  // 安装对话框(可自定义名称)
  const [installTarget, setInstallTarget] = useState<SkillPackage | null>(null)
  const [installName, setInstallName] = useState('')
  const [installing, setInstalling] = useState(false)

  // 评分对话框
  const [rateTarget, setRateTarget] = useState<SkillPackage | null>(null)
  const [rateHover, setRateHover] = useState(0)
  const [myScore, setMyScore] = useState(0)
  const [rateSubmitting, setRateSubmitting] = useState(false)

  // 版本列表对话框
  const [versionsTarget, setVersionsTarget] = useState<SkillPackage | null>(null)
  const [versions, setVersions] = useState<SkillPackage[]>([])
  const [pendingUnpublish, setPendingUnpublish] = useState<SkillPackage | null>(null)
  const [versionsLoading, setVersionsLoading] = useState(false)

  const loadAll = async () => {
    setLoading(true)
    try {
      const q = category ? `?category=${encodeURIComponent(category)}` : ''
      setPackages(await apiFetch<SkillPackage[]>(`/ai/skills${q}`))
    } catch (e: any) {
      getMessage().error(`加载技能市场失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  const loadFavorites = async () => {
    try {
      const list = await apiFetch<SkillPackage[]>('/ai/skills/favorites')
      setFavIds(new Set(list.map(p => p.id)))
    } catch (e: any) {
      // 收藏接口依赖用户上下文, 失败不影响主列表
    }
  }

  useEffect(() => { loadAll() }, [category])
  useEffect(() => { loadFavorites() }, [])

  // 类别下拉选项(含全部 + 已加载包中的去重类别)
  const categoryOptions = useMemo(() => {
    const set = new Set<string>()
    for (const p of packages) if (p.category) set.add(p.category)
    const opts = [{ value: '', label: '全部类别' }]
    for (const c of Array.from(set)) opts.push({ value: c, label: c })
    return opts
  }, [packages])

  const sortOptions = [
    { value: 'updated', label: '最近更新' },
    { value: 'rating', label: '评分最高' },
    { value: 'downloads', label: '安装最多' },
  ]

  // 关键词 + 收藏过滤 + 排序
  const visible = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    let list = packages.filter(p => {
      if (favOnly && !favIds.has(p.id)) return false
      if (!kw) return true
      return (
        p.name.toLowerCase().includes(kw) ||
        (p.author || '').toLowerCase().includes(kw) ||
        (p.description || '').toLowerCase().includes(kw) ||
        (p.tags || []).some(t => t.toLowerCase().includes(kw))
      )
    })
    list = [...list].sort((a, b) => {
      if (sort === 'rating') return (b.rating_avg || 0) - (a.rating_avg || 0)
      if (sort === 'downloads') return b.downloads - a.downloads
      return new Date(b.updated_at).getTime() - new Date(a.updated_at).getTime()
    })
    return list
  }, [packages, keyword, favOnly, favIds, sort])

  const install = async () => {
    if (!installTarget) return
    setInstalling(true)
    try {
      const ag = await apiFetch<any>(`/ai/skills/${installTarget.id}/install`, {
        method: 'POST',
        body: JSON.stringify({ name: installName || undefined }),
      })
      getMessage().success(`已安装为数字员工「${ag.name}」`)
      setInstallTarget(null)
      setInstallName('')
    } catch (e: any) {
      getMessage().error(`安装失败: ${e.message}`)
    } finally {
      setInstalling(false)
    }
  }

  const exportPkg = async (p: SkillPackage) => {
    try {
      const data = await apiFetch<any>(`/ai/skills/${p.id}/export`)
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${slugify(p.name)}.json`
      a.click()
      URL.revokeObjectURL(url)
      getMessage().success('技能包已导出')
    } catch (e: any) {
      getMessage().error(`导出失败: ${e.message}`)
    }
  }

  const removePkg = async () => {
    if (!pendingUnpublish) return
    const p = pendingUnpublish
    try {
      await apiFetch(`/ai/skills/${p.id}`, { method: 'DELETE' })
      getMessage().success('已下架')
      setFavIds(prev => {
        const n = new Set(prev); n.delete(p.id); return n
      })
      loadAll()
    } catch (e: any) {
      getMessage().error(`下架失败: ${e.message}`)
    } finally {
      setPendingUnpublish(null)
    }
  }

  // 收藏切换(乐观更新)
  const toggleFav = async (p: SkillPackage) => {
    const prev = favIds.has(p.id)
    setFavIds(prevIds => {
      const n = new Set(prevIds)
      if (prev) n.delete(p.id); else n.add(p.id)
      return n
    })
    try {
      await apiFetch<{ favorited: boolean }>(`/ai/skills/${p.id}/favorite`, { method: 'POST' })
    } catch (e: any) {
      // 失败回滚
      setFavIds(prevIds => {
        const n = new Set(prevIds)
        if (prev) n.add(p.id); else n.delete(p.id)
        return n
      })
      getMessage().error(`收藏操作失败: ${e.message}`)
    }
  }

  // 打开评分对话框并预取当前用户的评分
  const openRate = async (p: SkillPackage) => {
    setRateTarget(p)
    setMyScore(0)
    try {
      const r = await apiFetch<{ avg: number; count: number; my_score: number }>(`/ai/skills/${p.id}/rating`)
      setMyScore(r.my_score)
    } catch { /* 忽略, 用默认 */ }
  }

  const submitRate = async (score: number) => {
    if (!rateTarget) return
    setRateSubmitting(true)
    try {
      await apiFetch(`/ai/skills/${rateTarget.id}/rate`, {
        method: 'POST',
        body: JSON.stringify({ score }),
      })
      setMyScore(score)
      getMessage().success(`已评 ${score} 星`)
      loadAll()
    } catch (e: any) {
      getMessage().error(`评分失败: ${e.message}`)
    } finally {
      setRateSubmitting(false)
    }
  }

  // 打开版本列表
  const openVersions = async (p: SkillPackage) => {
    setVersionsTarget(p)
    setVersionsLoading(true)
    setVersions([])
    try {
      setVersions(await apiFetch<SkillPackage[]>(`/ai/skills/${p.id}/versions`))
    } catch (e: any) {
      getMessage().error(`加载版本失败: ${e.message}`)
    } finally {
      setVersionsLoading(false)
    }
  }

  const doImport = async () => {
    if (!importFile) return getMessage().error('请选择技能包 JSON 文件')
    setImporting(true)
    try {
      const text = await importFile.text()
      const pkg = await apiFetch<SkillPackage>('/ai/skills/import', {
        method: 'POST',
        body: JSON.stringify({ data: text }),
      })
      getMessage().success(`已导入技能包「${pkg.name}」`)
      setImportOpen(false)
      setImportFile(null)
      loadAll()
    } catch (e: any) {
      getMessage().error(`导入失败: ${e.message}`)
    } finally {
      setImporting(false)
    }
  }

  return (
    <div className="p-6 space-y-6">
      {/* 头部 */}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold flex items-center gap-2">
            <Store className="w-5 h-5 text-primary" /> 技能市场
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            把数字员工（提示词 / 技能 / 工具）打包成可分享的技能包，浏览、安装、复用团队沉淀的运维能力。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="middle" onClick={() => loadAll()} disabled={loading}>
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} /> 刷新
          </Button>
          <Button size="middle" type="primary" onClick={() => setImportOpen(true)}>
            <Upload className="w-4 h-4" /> 导入技能包
          </Button>
        </div>
      </div>

      {/* 过滤栏 */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative w-64">
          <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-9"
            placeholder="搜索名称 / 作者 / 标签"
            value={keyword}
            onChange={e => setKeyword(e.target.value)}
          />
        </div>
        <Select options={categoryOptions} value={category} onChange={v => setCategory(v)} className="w-44" />
        <Select options={sortOptions} value={sort} onChange={v => setSort(v as any)} className="w-36" />
        <Button
          type={favOnly ? 'primary' : 'default'}
          size="middle"
          onClick={() => setFavOnly(v => !v)}
          className={cn(favOnly && 'bg-rose-500 hover:bg-rose-600 border-rose-500')}
        >
          <Heart className={cn('w-4 h-4', favOnly && 'fill-current')} />
          {favOnly ? '仅看收藏' : '收藏'}
        </Button>
        <span className="text-sm text-muted-foreground">共 {visible.length} 个技能包</span>
      </div>

      {/* 卡片网格 */}
      {visible.length === 0 ? (
        <Card>
          <CardContent className="py-16 text-center text-muted-foreground">
            <Package className="w-10 h-10 mx-auto mb-3 opacity-40" />
            <p>技能市场暂无内容。</p>
            <p className="text-xs mt-1">在「数字员工」页点击「发布到市场」即可上架第一个技能包。</p>
          </CardContent>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {visible.map(p => (
            <Card key={p.id} className="group hover:shadow-md transition-shadow">
              <CardContent className="p-5 space-y-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <h3 className="font-semibold truncate">{p.name}</h3>
                       <Tag className="shrink-0 text-[10px]">v{p.version}</Tag>
                    </div>
                    {p.author && (
                      <p className="text-xs text-muted-foreground mt-1 flex items-center gap-1">
                        <User className="w-3 h-3" /> {p.author}
                      </p>
                    )}
                  </div>
                  {p.category && (
                     <Tag className="shrink-0 bg-primary/10 text-primary border-primary/20">{p.category}</Tag>
                  )}
                </div>

                {p.description && (
                  <p className="text-sm text-muted-foreground line-clamp-2">{p.description}</p>
                )}

                <div className="flex flex-wrap gap-1.5">
                  {(p.tags || []).slice(0, 4).map(t => (
                     <Tag key={t} className="text-[10px] flex items-center gap-0.5">
                      <Tag className="w-2.5 h-2.5" /> {t}
                    </Tag>
                  ))}
                  {p.tools && p.tools.length > 0 && (
                     <Tag className="text-[10px]">🔧 {p.tools.length} 工具</Tag>
                  )}
                  {p.agent.skills && p.agent.skills.length > 0 && (
                     <Tag className="text-[10px]">✨ {p.agent.skills.length} 技能</Tag>
                  )}
                </div>

                {/* 评分 + 安装数 */}
                <div className="flex items-center justify-between pt-1">
                  <Stars value={p.rating_avg || 0} count={p.rating_count || 0} />
                  <span className="text-xs text-muted-foreground flex items-center gap-1">
                    <Download className="w-3 h-3" /> {p.downloads} 次安装
                  </span>
                </div>

                <div className="flex items-center justify-between gap-1.5">
                  <div className="flex items-center gap-1.5">
                     <Button
                       type="text"
                      size="middle"
                      className={cn('text-rose-500', favIds.has(p.id) && 'bg-rose-500/10')}
                      onClick={() => toggleFav(p)}
                      title={favIds.has(p.id) ? '取消收藏' : '收藏'}
                    >
                      <Heart className={cn('w-3.5 h-3.5', favIds.has(p.id) && 'fill-current')} />
                    </Button>
                    <Button type="text" size="middle" className="text-amber-500" onClick={() => openRate(p)} title="评分">
                      <Star className="w-3.5 h-3.5" />
                    </Button>
                    <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => openVersions(p)} title="版本列表">
                      <GitBranch className="w-3.5 h-3.5" />
                    </Button>
                  </div>
                  <div className="flex items-center gap-1.5">
                     <Button size="middle" onClick={() => exportPkg(p)} title="导出 JSON">
                      <Download className="w-3.5 h-3.5" />
                    </Button>
                     <Button type="text" size="middle" className="text-red-500" onClick={() => setPendingUnpublish(p)} title="下架">
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                     <Button type="primary" size="middle" onClick={() => { setInstallTarget(p); setInstallName('') }}>
                      安装
                    </Button>
                  </div>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* 导入对话框 */}
      <Modal open={importOpen} onCancel={() => setImportOpen(false)} title="导入技能包" footer={
        <Space>
          <Button onClick={() => setImportOpen(false)}>取消</Button>
          <Button type="primary" onClick={doImport} disabled={importing || !importFile}>
            {importing ? '导入中…' : '开始导入'}
          </Button>
        </Space>
      }>
        <p className="text-sm text-muted-foreground mb-3">
          选择从市场导出的技能包 JSON 文件，导入后进入本租户目录，可一键安装为数字员工。
        </p>
        <input
          type="file"
          accept="application/json,.json"
          onChange={e => setImportFile(e.target.files?.[0] || null)}
          className="block w-full text-sm file:mr-3 file:py-1.5 file:px-3 file:rounded-md file:border-0 file:bg-primary/10 file:text-primary hover:file:bg-primary/15 cursor-pointer"
        />
        {importFile && <p className="text-xs text-muted-foreground mt-2">已选择：{importFile.name}</p>}
      </Modal>

      {/* 安装对话框 */}
      <Modal open={!!installTarget} onCancel={() => setInstallTarget(null)} title="安装技能包" footer={
        <Space>
          <Button onClick={() => setInstallTarget(null)}>取消</Button>
          <Button type="primary" onClick={install} disabled={installing}>
            {installing ? '安装中…' : '安装'}
          </Button>
        </Space>
      }>
        {installTarget && (
          <>
            <p className="text-sm text-muted-foreground mb-3">
              即将把技能包「<span className="text-foreground font-medium">{installTarget.name}</span>」安装为一个新的数字员工。
              {installTarget.tools && installTarget.tools.length > 0 && (
                <span className="block mt-1">依赖的 {installTarget.tools.length} 个自定义工具将按需重建。</span>
              )}
            </p>
            <label className="text-sm text-muted-foreground">数字员工名称（可选，留空自动加「 (市场)」后缀）</label>
            <Input
              className="mt-1.5"
              placeholder={`${installTarget.agent.name} (市场)`}
              value={installName}
              onChange={e => setInstallName(e.target.value)}
            />
          </>
        )}
      </Modal>

      {/* 评分对话框 */}
      <Modal open={!!rateTarget} onCancel={() => setRateTarget(null)} title="为技能包评分" footer={
        <Space>
          <Button onClick={() => setRateTarget(null)}>关闭</Button>
        </Space>
      }>
        {rateTarget && (
          <>
            <p className="text-sm text-muted-foreground mb-1">
              为「<span className="text-foreground font-medium">{rateTarget.name}</span>」打分（1~5 星）
            </p>
            <div className="flex items-center gap-3 py-3">
              <Stars
                value={myScore || rateTarget.rating_avg || 0}
                count={rateTarget.rating_count || 0}
                onRate={(s) => { setRateHover(s); submitRate(s) }}
                hover={rateHover}
                onHover={setRateHover}
                interactive
              />
            </div>
            <p className="text-xs text-muted-foreground">
              {myScore > 0 ? `你已评 ${myScore} 星` : '点击星星提交评分'} · 当前均值 {rateTarget.rating_avg?.toFixed(1) || '—'}（{rateTarget.rating_count || 0} 人）
            </p>
          </>
        )}
      </Modal>

      {/* 版本列表对话框 */}
      <Modal open={!!versionsTarget} onCancel={() => setVersionsTarget(null)} title="版本列表" footer={
        <Space>
          <Button onClick={() => setVersionsTarget(null)}>关闭</Button>
        </Space>
      }>
        {versionsTarget && (
          <>
            <p className="text-sm text-muted-foreground mb-3">
              「<span className="text-foreground font-medium">{versionsTarget.name}</span>」的全部版本，可选择历史版本安装。
            </p>
            {versionsLoading ? (
              <p className="text-sm text-muted-foreground py-6 text-center">加载中…</p>
            ) : versions.length === 0 ? (
              <p className="text-sm text-muted-foreground py-6 text-center">暂无版本</p>
            ) : (
              <div className="space-y-2 max-h-80 overflow-auto">
                {versions.map(v => (
                  <div key={v.id} className="flex items-center justify-between gap-2 rounded-lg border p-3">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="font-medium">v{v.version}</span>
                         <Tag className="text-[10px]">{(v.rating_avg || 0).toFixed(1)} ★</Tag>
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5">
                        {v.downloads} 次安装 · {new Date(v.updated_at).toLocaleDateString()}
                        {v.id === versionsTarget.id && ' · 当前'}
                      </p>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                       <Button size="middle" onClick={() => exportPkg(v)} title="导出该版本">
                        <Download className="w-3.5 h-3.5" />
                      </Button>
                      <Button
                        type="primary"
                        size="middle"
                        onClick={() => {
                          setVersionsTarget(null)
                          setInstallTarget(v)
                          setInstallName('')
                        }}
                      >
                        安装此版本
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </Modal>

      <Popconfirm
        title="下架技能包"
        description={pendingUnpublish ? `确认下架技能包「${pendingUnpublish.name}」？` : ''}
        onConfirm={removePkg}
        okText="下架"
        cancelText="取消"
        open={!!pendingUnpublish}
        onCancel={() => setPendingUnpublish(null)}
      >
        <span />
      </Popconfirm>
    </div>
  )
}

// 星级显示 / 交互组件。
// value: 当前均值(展示) 或 待提交分(交互); count: 评分人数; interactive: 可点击打分。
function Stars({
  value, count, onRate, hover, onHover, interactive,
}: {
  value: number
  count?: number
  onRate?: (s: number) => void
  hover?: number
  onHover?: (s: number) => void
  interactive?: boolean
}) {
  const display = (hover && onHover) ? hover : Math.round(value)
  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex">
        {[1, 2, 3, 4, 5].map(i => {
          const lit = i <= display
          return (
            <button
              key={i}
              type="button"
              disabled={!interactive}
              className={cn(
                'text-base leading-none transition-transform',
                lit ? 'text-amber-400' : 'text-muted-foreground/30',
                interactive && 'cursor-pointer hover:scale-125',
              )}
              onClick={() => interactive && onRate?.(i)}
              onMouseEnter={() => onHover?.(i)}
              onMouseLeave={() => onHover?.(0)}
            >
              ★
            </button>
          )
        })}
      </span>
      {count !== undefined && (
        <span className="text-xs text-muted-foreground">
          {value ? value.toFixed(1) : '—'}({count})
        </span>
      )}
    </span>
  )
}

function slugify(name: string): string {
  const s = name.toLowerCase().trim()
  let out = ''
  for (const r of s) {
    if ((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) out += r
    else if (r === ' ' || r === '-' || r === '_') out += '-'
  }
  out = out.replace(/-+/g, '-').replace(/^-|-$/g, '')
  return out || 'skill-package'
}
