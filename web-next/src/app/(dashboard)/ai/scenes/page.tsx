'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { useSceneTemplates, type SceneTemplate } from '@/stores/aiWorkforce'
import { useAIStore } from '@/stores/ai'
import {
  Shield, TrendingDown, Siren, Package, RefreshCw, Sparkles, Wand2,
  Wrench, BookOpen, ListChecks,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

const ICONS: Record<string, LucideIcon> = { Shield, TrendingDown, Siren }

// 场景类别 → 主题色(与 CMDB 五大分类保持同一套语义色)
const CATEGORY_THEME: Record<string, { ring: string; glow: string; text: string; chip: string }> = {
  安全: { ring: 'ring-red-500/30', glow: 'from-red-500/15', text: 'text-red-500', chip: 'bg-red-500/10 text-red-600 dark:text-red-400' },
  容量: { ring: 'ring-amber-500/30', glow: 'from-amber-500/15', text: 'text-amber-500', chip: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' },
  应急: { ring: 'ring-rose-500/30', glow: 'from-rose-500/15', text: 'text-rose-500', chip: 'bg-rose-500/10 text-rose-600 dark:text-rose-400' },
}
const DEFAULT_THEME = { ring: 'ring-primary/30', glow: 'from-primary/15', text: 'text-primary', chip: 'bg-primary/10 text-primary' }

function themeOf(category: string) {
  return CATEGORY_THEME[category] || DEFAULT_THEME
}

export default function ScenesPage() {
  const { sceneTemplates, sceneLaunching, loadSceneTemplates, instantiateScene } = useSceneTemplates()
  const agents = useAIStore(s => s.agents)
  const fetchAgents = useAIStore(s => s.fetchAgents)

  const [preview, setPreview] = useState<SceneTemplate | null>(null)
  const [target, setTarget] = useState<SceneTemplate | null>(null)
  const [name, setName] = useState('')

  useEffect(() => {
    loadSceneTemplates()
    fetchAgents()
  }, [loadSceneTemplates, fetchAgents])

  // 已启用的场景: 该 Agent 名称以「场景名 (序号)」或「场景名」形式存在
  const launchedCount = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const s of sceneTemplates) {
      counts[s.id] = agents.filter((a: { name?: string }) =>
        a.name === s.name || (a.name || '').startsWith(`${s.name} (`),
      ).length
    }
    return counts
  }, [sceneTemplates, agents])

  const openLaunch = (s: SceneTemplate) => {
    setTarget(s)
    setName('')
  }

  const confirmLaunch = async () => {
    if (!target) return
    const ok = await instantiateScene(target.id, name.trim())
    if (ok) {
      setTarget(null)
      await fetchAgents()
    }
  }

  return (
    <div className="p-6 space-y-5">
      {/* 页头 */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Package className="h-6 w-6 text-primary" />
            场景包
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            按业务场景把数字员工 + 主动巡检 + 推荐工单打包, 一键启用整套能力, 而不是逐个搭建
          </p>
        </div>
        <button
          onClick={() => { loadSceneTemplates(); fetchAgents() }}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          <RefreshCw className="h-4 w-4" />刷新
        </button>
      </div>

      {/* 场景卡片网格 */}
      {sceneTemplates.length === 0 ? (
        <Card><CardContent className="py-16 text-center text-muted-foreground">暂无场景包</CardContent></Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {sceneTemplates.map(s => {
            const Icon = ICONS[s.icon] || Package
            const theme = themeOf(s.category)
            const launched = launchedCount[s.id] || 0
            const isLaunching = sceneLaunching.includes(s.id)
            return (
              <div
                key={s.id}
                className={cn(
                  'group relative overflow-hidden rounded-xl border border-border bg-card',
                  'transition-all duration-300 ease-out hover:-translate-y-1 hover:shadow-lg',
                  'hover:ring-1', theme.ring,
                )}
              >
                <div className={cn(
                  'pointer-events-none absolute inset-0 bg-gradient-to-br to-transparent opacity-0',
                  'transition-opacity duration-300 group-hover:opacity-100', theme.glow,
                )} />
                <div className="relative p-5 space-y-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex items-center gap-3 min-w-0">
                      <div className={cn('shrink-0 h-11 w-11 rounded-lg grid place-items-center', theme.chip)}>
                        <Icon className="h-5 w-5" />
                      </div>
                      <div className="min-w-0">
                        <div className="font-semibold truncate">{s.name}</div>
                        <div className={cn('text-xs', theme.text)}>{s.category}</div>
                      </div>
                    </div>
                    {launched > 0 && (
                      <Badge variant="success" className="shrink-0">已启用 {launched}</Badge>
                    )}
                  </div>

                  <p className="text-sm text-muted-foreground line-clamp-2 min-h-[2.5rem]">{s.description}</p>

                  <div className="flex flex-wrap gap-1.5">
                    {(s.tags || []).map(tag => (
                      <span key={tag} className="px-2 py-0.5 rounded text-[11px] bg-muted text-muted-foreground">{tag}</span>
                    ))}
                  </div>

                  <div className="flex items-center gap-4 text-[11px] text-muted-foreground pt-1">
                    <span className="inline-flex items-center gap-1">
                      <Wand2 className="h-3 w-3" />{(s.inspection_template_ids?.length || 0) + 1} 项巡检
                    </span>
                    {s.suggested_catalog && s.suggested_catalog.length > 0 && (
                      <span className="inline-flex items-center gap-1">
                        <ListChecks className="h-3 w-3" />{s.suggested_catalog.length} 类工单
                      </span>
                    )}
                    {s.knowledge_categories && s.knowledge_categories.length > 0 && (
                      <span className="inline-flex items-center gap-1">
                        <BookOpen className="h-3 w-3" />{s.knowledge_categories.length} 个知识
                      </span>
                    )}
                  </div>

                  <div className="flex items-center gap-2 pt-2">
                    <Button
                      size="sm"
                      className="flex-1 gap-1"
                      disabled={isLaunching}
                      onClick={() => openLaunch(s)}
                    >
                      <Sparkles className="h-3.5 w-3.5" />
                      {isLaunching ? '启用中…' : '一键启用'}
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setPreview(s)}>
                      场景明细
                    </Button>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* 场景明细 */}
      <Dialog open={!!preview} onClose={() => setPreview(null)} title={preview ? `${preview.name} · 场景明细` : ''}>
        {preview && (
          <div className="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
            <div>
              <div className="text-xs text-muted-foreground mb-1">场景描述</div>
              <p className="text-sm">{preview.description}</p>
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-1">包含的数字员工</div>
              <Badge variant="secondary">{preview.agent_template_id}</Badge>
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-1">启用的巡检</div>
              <div className="flex flex-wrap gap-1.5">
                {(preview.inspection_template_ids || []).map(t => <Badge key={t} variant="outline">{t}</Badge>)}
                <Badge variant="info">岗位自带关联巡检</Badge>
              </div>
            </div>
            {preview.suggested_catalog && preview.suggested_catalog.length > 0 && (
              <div>
                <div className="text-xs text-muted-foreground mb-1">推荐的标准化工单类型</div>
                <div className="space-y-2">
                  {preview.suggested_catalog.map((c, i) => (
                    <div key={i} className="rounded-md border border-border p-2.5">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium">{c.name}</span>
                        {c.category && <Badge variant="outline">{c.category}</Badge>}
                      </div>
                      {c.description && <div className="text-xs text-muted-foreground mt-0.5">{c.description}</div>}
                    </div>
                  ))}
                </div>
              </div>
            )}
            {preview.knowledge_categories && preview.knowledge_categories.length > 0 && (
              <div>
                <div className="text-xs text-muted-foreground mb-1">建议绑定知识分类</div>
                <div className="flex flex-wrap gap-1.5">
                  {preview.knowledge_categories.map(c => <Badge key={c} variant="info">{c}</Badge>)}
                </div>
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setPreview(null)}>关闭</Button>
          {preview && (
            <Button onClick={() => { const s = preview; setPreview(null); openLaunch(s) }}>
              <Sparkles className="h-4 w-4 mr-1" />一键启用
            </Button>
          )}
        </DialogFooter>
      </Dialog>

      {/* 启用确认 */}
      <Dialog open={!!target} onClose={() => setTarget(null)} title={`启用「${target?.name || ''}」场景`}>
        {target && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              将创建数字员工「{target.name}」岗位 Agent，并自动启用 (target.inspection_template_ids?.length || 0) + 1 项巡检。
              推荐的标准化工单类型可查看场景明细, 启用后可到 ITSM 服务目录手动启用。
            </p>
            <div>
              <label className="text-xs text-muted-foreground">场景名称（留空使用默认名）</label>
              <Input className="mt-1" value={name} onChange={e => setName(e.target.value)} placeholder={target.name} />
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setTarget(null)}>取消</Button>
          <Button onClick={confirmLaunch}>
            <Sparkles className="h-4 w-4 mr-1" />确认启用
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}