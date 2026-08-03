'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { useAgentTemplates, type AgentTemplate } from '@/stores/aiWorkforce'
import { useAIStore } from '@/stores/ai'
import {
  Database, Network, Shield, GitBranch, Siren, TrendingDown, Clock,
  Bot, RefreshCw, UserPlus, Sparkles, Wrench, BookOpen, CheckCircle2,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'

// 模板 icon 名 → 组件映射（后端只下发名称，前端负责视觉呈现）
const ICONS: Record<string, LucideIcon> = {
  Database, Network, Shield, GitBranch, Siren, TrendingDown, Clock,
}

// 岗位类别 → 主题色（与 CMDB 五大分类保持同一套语义色）
const ROLE_THEME: Record<string, { ring: string; glow: string; text: string; chip: string }> = {
  数据库资源: { ring: 'ring-sky-500/30', glow: 'from-sky-500/15', text: 'text-sky-500', chip: 'bg-sky-500/10 text-sky-600 dark:text-sky-400' },
  基础设施资源: { ring: 'ring-violet-500/30', glow: 'from-violet-500/15', text: 'text-violet-500', chip: 'bg-violet-500/10 text-violet-600 dark:text-violet-400' },
  业务资源: { ring: 'ring-emerald-500/30', glow: 'from-emerald-500/15', text: 'text-emerald-500', chip: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' },
  中间件资源: { ring: 'ring-amber-500/30', glow: 'from-amber-500/15', text: 'text-amber-500', chip: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' },
  应用资源: { ring: 'ring-rose-500/30', glow: 'from-rose-500/15', text: 'text-rose-500', chip: 'bg-rose-500/10 text-rose-600 dark:text-rose-400' },
}
const DEFAULT_THEME = { ring: 'ring-primary/30', glow: 'from-primary/15', text: 'text-primary', chip: 'bg-primary/10 text-primary' }

function themeOf(role: string) {
  return ROLE_THEME[role] || DEFAULT_THEME
}

export default function DigitalEmployeesPage() {
  const { templates, loading, loadTemplates, instantiateTemplate } = useAgentTemplates()
  const agents = useAIStore(s => s.agents)
  const fetchAgents = useAIStore(s => s.fetchAgents)

  const [preview, setPreview] = useState<AgentTemplate | null>(null)
  const [hireTarget, setHireTarget] = useState<AgentTemplate | null>(null)
  const [hireName, setHireName] = useState('')
  const [hiring, setHiring] = useState(false)
  const [roleFilter, setRoleFilter] = useState<string>('')

  useEffect(() => {
    loadTemplates()
    fetchAgents()
  }, [loadTemplates, fetchAgents])

  const roles = useMemo(() => {
    const set = new Set(templates.map(t => t.role))
    return Array.from(set)
  }, [templates])

  const visible = useMemo(
    () => (roleFilter ? templates.filter(t => t.role === roleFilter) : templates),
    [templates, roleFilter],
  )

  // 已上岗判断：Agent 名称与模板名同名或以「模板名 (n)」形式存在
  const hiredCount = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const t of templates) {
      counts[t.id] = agents.filter((a: { name?: string }) =>
        a.name === t.name || (a.name || '').startsWith(`${t.name} (`),
      ).length
    }
    return counts
  }, [templates, agents])

  const openHire = (t: AgentTemplate) => {
    setHireTarget(t)
    setHireName('')
  }

  const confirmHire = async () => {
    if (!hireTarget) return
    setHiring(true)
    const ok = await instantiateTemplate(hireTarget.id, hireName.trim())
    setHiring(false)
    if (ok) {
      setHireTarget(null)
      await fetchAgents()
    }
  }

  return (
    <div className="p-6 space-y-5">
      {/* 页头 */}
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Sparkles className="h-6 w-6 text-primary" />
            数字员工
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            预置岗位 Agent 矩阵，一键上岗即可在对话与团队编排中使用，无需从空白开始搭建
          </p>
        </div>
        <button
          onClick={() => { loadTemplates(); fetchAgents() }}
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
        >
          <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
        </button>
      </div>

      {/* 岗位类别筛选 */}
      <div className="flex flex-wrap items-center gap-2">
        <button
          onClick={() => setRoleFilter('')}
          className={cn(
            'px-3 py-1 rounded-full text-xs border transition-all duration-200',
            roleFilter === '' ? 'bg-primary text-primary-foreground border-primary' : 'border-border hover:border-primary/50 text-muted-foreground hover:text-foreground',
          )}
        >
          全部 {templates.length}
        </button>
        {roles.map(r => (
          <button
            key={r}
            onClick={() => setRoleFilter(r === roleFilter ? '' : r)}
            className={cn(
              'px-3 py-1 rounded-full text-xs border transition-all duration-200',
              roleFilter === r ? 'bg-primary text-primary-foreground border-primary' : 'border-border hover:border-primary/50 text-muted-foreground hover:text-foreground',
            )}
          >
            {r}
          </button>
        ))}
      </div>

      {/* 岗位卡片网格 */}
      {loading && templates.length === 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-52 rounded-xl border border-border bg-muted/30 animate-pulse" />
          ))}
        </div>
      ) : visible.length === 0 ? (
        <Card><CardContent className="py-16 text-center text-muted-foreground">暂无岗位模板</CardContent></Card>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {visible.map(t => {
            const Icon = ICONS[t.icon] || Bot
            const theme = themeOf(t.role)
            const hired = hiredCount[t.id] || 0
            return (
              <div
                key={t.id}
                className={cn(
                  'group relative overflow-hidden rounded-xl border border-border bg-card',
                  'transition-all duration-300 ease-out hover:-translate-y-1 hover:shadow-lg',
                  'hover:ring-1', theme.ring,
                )}
              >
                {/* 悬停时浮现的柔光背景 */}
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
                        <div className="font-semibold truncate">{t.name}</div>
                        <div className={cn('text-xs', theme.text)}>{t.role}</div>
                      </div>
                    </div>
                    {hired > 0 && (
                      <Badge variant="success" className="shrink-0 gap-1">
                        <CheckCircle2 className="h-3 w-3" />已上岗 {hired}
                      </Badge>
                    )}
                  </div>

                  <p className="text-sm text-muted-foreground line-clamp-2 min-h-[2.5rem]">{t.description}</p>

                  <div className="flex flex-wrap gap-1.5">
                    {(t.tags || []).map(tag => (
                      <span key={tag} className="px-2 py-0.5 rounded text-[11px] bg-muted text-muted-foreground">{tag}</span>
                    ))}
                  </div>

                  <div className="flex items-center gap-4 text-[11px] text-muted-foreground pt-1">
                    <span className="inline-flex items-center gap-1">
                      <Wrench className="h-3 w-3" />{t.tools?.length ? `${t.tools.length} 个工具` : '全部工具'}
                    </span>
                    <span className="inline-flex items-center gap-1">
                      <BookOpen className="h-3 w-3" />{t.knowledge_categories?.length ? t.knowledge_categories.join('/') : '全部知识'}
                    </span>
                  </div>

                  <div className="flex items-center gap-2 pt-2">
                    <Button size="sm" className="flex-1 gap-1" onClick={() => openHire(t)}>
                      <UserPlus className="h-3.5 w-3.5" />一键上岗
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => setPreview(t)}>查看职责</Button>
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* 职责预览 */}
      <Dialog open={!!preview} onClose={() => setPreview(null)} title={preview ? `${preview.name} · 岗位说明` : ''}>
        {preview && (
          <div className="space-y-4 max-h-[60vh] overflow-y-auto pr-1">
            <div>
              <div className="text-xs text-muted-foreground mb-1">岗位描述</div>
              <p className="text-sm">{preview.description}</p>
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-1">默认模型</div>
              <Badge variant="secondary">{preview.model_name}</Badge>
            </div>
            <div>
              <div className="text-xs text-muted-foreground mb-1">工作口径（系统提示）</div>
              <pre className="text-xs whitespace-pre-wrap bg-muted/50 rounded-md p-3 leading-relaxed">{preview.system_prompt}</pre>
            </div>
            {!!preview.tools?.length && (
              <div>
                <div className="text-xs text-muted-foreground mb-1">可用工具</div>
                <div className="flex flex-wrap gap-1.5">
                  {preview.tools.map(t => <Badge key={t} variant="outline">{t}</Badge>)}
                </div>
              </div>
            )}
            {!!preview.knowledge_categories?.length && (
              <div>
                <div className="text-xs text-muted-foreground mb-1">绑定知识库分类</div>
                <div className="flex flex-wrap gap-1.5">
                  {preview.knowledge_categories.map(c => <Badge key={c} variant="info">{c}</Badge>)}
                </div>
              </div>
            )}
            {!!preview.skills?.length && (
              <div>
                <div className="text-xs text-muted-foreground mb-1">预置能力</div>
                <div className="space-y-2">
                  {preview.skills.map(s => (
                    <div key={s.name} className="rounded-md border border-border p-2.5">
                      <div className="text-sm font-medium">{s.name}</div>
                      <div className="text-xs text-muted-foreground mt-0.5">{s.description}</div>
                      <div className="text-xs mt-1.5 leading-relaxed">{s.instructions}</div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setPreview(null)}>关闭</Button>
          {preview && (
            <Button onClick={() => { const t = preview; setPreview(null); openHire(t) }}>
              <UserPlus className="h-4 w-4 mr-1" />一键上岗
            </Button>
          )}
        </DialogFooter>
      </Dialog>

      {/* 上岗确认 */}
      <Dialog open={!!hireTarget} onClose={() => setHireTarget(null)} title="让数字员工上岗">
        {hireTarget && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">
              将以「{hireTarget.name}」模板创建一个可用的 Agent，创建后可在 AI 对话与团队编排中直接选用，也可再做个性化调整。
            </p>
            <div>
              <label className="text-xs text-muted-foreground">员工名称（留空使用模板默认名）</label>
              <Input
                className="mt-1"
                value={hireName}
                onChange={e => setHireName(e.target.value)}
                placeholder={hireTarget.name}
              />
            </div>
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setHireTarget(null)}>取消</Button>
          <Button onClick={confirmHire} disabled={hiring}>
            {hiring ? '创建中…' : '确认上岗'}
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
