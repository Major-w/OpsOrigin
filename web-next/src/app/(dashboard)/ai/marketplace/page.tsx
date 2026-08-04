'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { apiFetch, cn } from '@/lib/utils'
import { toast } from '@/stores/toast'
import {
  Store, RefreshCw, Download, Upload, Trash2, Package, User, Tag, Search,
} from 'lucide-react'

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
  checksum: string
  created_at: string
  updated_at: string
}

export default function MarketplacePage() {
  const [packages, setPackages] = useState<SkillPackage[]>([])
  const [loading, setLoading] = useState(false)
  const [category, setCategory] = useState('')
  const [keyword, setKeyword] = useState('')

  // 导入对话框
  const [importOpen, setImportOpen] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importFile, setImportFile] = useState<File | null>(null)

  // 安装对话框(可自定义名称)
  const [installTarget, setInstallTarget] = useState<SkillPackage | null>(null)
  const [installName, setInstallName] = useState('')
  const [installing, setInstalling] = useState(false)

  const loadAll = async () => {
    setLoading(true)
    try {
      const q = category ? `?category=${encodeURIComponent(category)}` : ''
      setPackages(await apiFetch<SkillPackage[]>(`/ai/skills${q}`))
    } catch (e: any) {
      toast.error(`加载技能市场失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { loadAll() }, [category])

  // 类别下拉选项(含全部 + 已加载包中的去重类别)
  const categoryOptions = useMemo(() => {
    const set = new Set<string>()
    for (const p of packages) if (p.category) set.add(p.category)
    const opts = [{ value: '', label: '全部类别' }]
    for (const c of Array.from(set)) opts.push({ value: c, label: c })
    return opts
  }, [packages])

  // 关键词过滤(前端二次过滤: 名称/作者/描述/标签)
  const visible = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    if (!kw) return packages
    return packages.filter(p =>
      p.name.toLowerCase().includes(kw) ||
      (p.author || '').toLowerCase().includes(kw) ||
      (p.description || '').toLowerCase().includes(kw) ||
      (p.tags || []).some(t => t.toLowerCase().includes(kw)),
    )
  }, [packages, keyword])

  const install = async () => {
    if (!installTarget) return
    setInstalling(true)
    try {
      const ag = await apiFetch<any>(`/ai/skills/${installTarget.id}/install`, {
        method: 'POST',
        body: JSON.stringify({ name: installName || undefined }),
      })
      toast.success(`已安装为数字员工「${ag.name}」`)
      setInstallTarget(null)
      setInstallName('')
    } catch (e: any) {
      toast.error(`安装失败: ${e.message}`)
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
      toast.success('技能包已导出')
    } catch (e: any) {
      toast.error(`导出失败: ${e.message}`)
    }
  }

  const removePkg = async (p: SkillPackage) => {
    if (!confirm(`确认下架技能包「${p.name}」？`)) return
    try {
      await apiFetch(`/ai/skills/${p.id}`, { method: 'DELETE' })
      toast.success('已下架')
      loadAll()
    } catch (e: any) {
      toast.error(`下架失败: ${e.message}`)
    }
  }

  const doImport = async () => {
    if (!importFile) return toast.error('请选择技能包 JSON 文件')
    setImporting(true)
    try {
      const text = await importFile.text()
      const pkg = await apiFetch<SkillPackage>('/ai/skills/import', {
        method: 'POST',
        body: JSON.stringify({ data: text }),
      })
      toast.success(`已导入技能包「${pkg.name}」`)
      setImportOpen(false)
      setImportFile(null)
      loadAll()
    } catch (e: any) {
      toast.error(`导入失败: ${e.message}`)
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
          <Button variant="outline" size="sm" onClick={() => loadAll()} disabled={loading}>
            <RefreshCw className={cn('w-4 h-4', loading && 'animate-spin')} /> 刷新
          </Button>
          <Button variant="primary" size="sm" onClick={() => setImportOpen(true)}>
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
        <Select options={categoryOptions} value={category} onChange={e => setCategory(e.target.value)} className="w-44" />
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
                      <Badge variant="outline" className="shrink-0 text-[10px]">v{p.version}</Badge>
                    </div>
                    {p.author && (
                      <p className="text-xs text-muted-foreground mt-1 flex items-center gap-1">
                        <User className="w-3 h-3" /> {p.author}
                      </p>
                    )}
                  </div>
                  {p.category && (
                    <Badge className="shrink-0 bg-primary/10 text-primary border-primary/20">{p.category}</Badge>
                  )}
                </div>

                {p.description && (
                  <p className="text-sm text-muted-foreground line-clamp-2">{p.description}</p>
                )}

                <div className="flex flex-wrap gap-1.5">
                  {(p.tags || []).slice(0, 4).map(t => (
                    <Badge key={t} variant="secondary" className="text-[10px] flex items-center gap-0.5">
                      <Tag className="w-2.5 h-2.5" /> {t}
                    </Badge>
                  ))}
                  {p.tools && p.tools.length > 0 && (
                    <Badge variant="secondary" className="text-[10px]">🔧 {p.tools.length} 工具</Badge>
                  )}
                  {p.agent.skills && p.agent.skills.length > 0 && (
                    <Badge variant="secondary" className="text-[10px]">✨ {p.agent.skills.length} 技能</Badge>
                  )}
                </div>

                <div className="flex items-center justify-between pt-1">
                  <span className="text-xs text-muted-foreground flex items-center gap-1">
                    <Download className="w-3 h-3" /> {p.downloads} 次安装
                  </span>
                  <div className="flex items-center gap-1.5">
                    <Button variant="outline" size="sm" onClick={() => exportPkg(p)} title="导出 JSON">
                      <Download className="w-3.5 h-3.5" />
                    </Button>
                    <Button variant="ghost" size="sm" className="text-red-500" onClick={() => removePkg(p)} title="下架">
                      <Trash2 className="w-3.5 h-3.5" />
                    </Button>
                    <Button
                      variant="primary"
                      size="sm"
                      onClick={() => { setInstallTarget(p); setInstallName('') }}
                    >
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
      <Dialog open={importOpen} onClose={() => setImportOpen(false)} title="导入技能包">
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setImportOpen(false)}>取消</Button>
          <Button variant="primary" onClick={doImport} disabled={importing || !importFile}>
            {importing ? '导入中…' : '导入'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* 安装对话框 */}
      <Dialog open={!!installTarget} onClose={() => setInstallTarget(null)} title="安装技能包">
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
            <DialogFooter>
              <Button variant="outline" onClick={() => setInstallTarget(null)}>取消</Button>
              <Button variant="primary" onClick={install} disabled={installing}>
                {installing ? '安装中…' : '确认安装'}
              </Button>
            </DialogFooter>
          </>
        )}
      </Dialog>
    </div>
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
