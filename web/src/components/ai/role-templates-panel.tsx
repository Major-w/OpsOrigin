
import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Select } from '@/components/ui/select'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { cn, apiFetch } from '@/lib/utils'
import { useAIStore, useAgentTemplates, useWorkforceStore, type AgentTemplate } from '@/stores/ai'
import {
  Database, Network, Shield, GitBranch, Siren, TrendingDown, Clock,
  Bot, RefreshCw, UserPlus, Sparkles, Wrench, BookOpen, CheckCircle2, Brain, Trash2, Store,
} from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { getMessage } from '@/lib/message'

// P0-1 长期记忆条目
interface ExperienceMemory {
  id: string
  agent_id?: string
  category?: string
  problem: string
  resolution: string
  outcome: 'succeeded' | 'false_positive'
  source?: string
  hit_count: number
}

// 模板 icon 名 → 组件映射（后端只下发名称，前端负责视觉呈现）
const ICONS: Record<string, LucideIcon> = {
  Database, Network, Shield, GitBranch, Siren, TrendingDown, Clock,
}

// 岗位类别 → 主题色（与 CMDB 五大分类保持同一套语义色）
const ROLE_THEME: Record<string, { ring: string; glow: string; text: string; chip: string }> = {
  数据库资源: { ring: 'ring-sky-500/30', glow: 'from-sky-500/15', text: 'text-sky-500', chip: 'bg-sky-500/10 text-sky-600 dark:text-sky-400' },
  基础设施资源: { ring: 'ring-violet-500/30', glow: 'from-violet-500/15', text: 'text-violet-500', chip: 'bg-violet-500/10 text-violet-600 dark:text-violet-400' },
  业务资源: { ring: 'ring-blue-500/30', glow: 'from-blue-500/15', text: 'text-blue-500', chip: 'bg-blue-500/10 text-blue-600 dark:text-blue-400' },
  中间件资源: { ring: 'ring-amber-500/30', glow: 'from-amber-500/15', text: 'text-amber-500', chip: 'bg-amber-500/10 text-amber-600 dark:text-amber-400' },
  应用资源: { ring: 'ring-rose-500/30', glow: 'from-rose-500/15', text: 'text-rose-500', chip: 'bg-rose-500/10 text-rose-600 dark:text-rose-400' },
  容器资源: { ring: 'ring-teal-500/30', glow: 'from-teal-500/15', text: 'text-teal-500', chip: 'bg-teal-500/10 text-teal-600 dark:text-teal-400' },
  云资源: { ring: 'ring-cyan-500/30', glow: 'from-cyan-500/15', text: 'text-cyan-500', chip: 'bg-cyan-500/10 text-cyan-600 dark:text-cyan-400' },
}
const DEFAULT_THEME = { ring: 'ring-primary/30', glow: 'from-primary/15', text: 'text-primary', chip: 'bg-primary/10 text-primary' }

function themeOf(role: string) {
  return ROLE_THEME[role] || DEFAULT_THEME
}

export default function RoleTemplatesPanel() {
  const { templates, loading, loadTemplates, instantiateTemplate } = useAgentTemplates()
  const agents = useAIStore(s => s.agents)
  const fetchAgents = useAIStore(s => s.fetchAgents)
  // 巡检模板目录: 用于展示关联巡检名称(与巡检页共用同一 store, 不重复加载逻辑)
  const inspectionTemplates = useWorkforceStore(s => s.inspectionTemplates)
  const loadInspectionTemplates = useWorkforceStore(s => s.loadInspectionTemplates)

  const [preview, setPreview] = useState<AgentTemplate | null>(null)
  // —— P0-1 长期记忆 ——
  const [memories, setMemories] = useState<ExperienceMemory[]>([])
  const [memAgent, setMemAgent] = useState('')
  const [memForm, setMemForm] = useState({ agent_id: '', problem: '', resolution: '' })
  const [memSaving, setMemSaving] = useState(false)

  const loadMemories = async (agentId?: string) => {
    try {
      const q = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
      setMemories(await apiFetch<ExperienceMemory[]>(`/ai/memory${q}`))
    } catch (e: any) { getMessage().error(`加载记忆失败: ${e.message}`) }
  }
  const addMemory = async () => {
    if (!memForm.problem.trim() || !memForm.resolution.trim()) return getMessage().error('请填写问题与处置方案')
    setMemSaving(true)
    try {
      await apiFetch('/ai/memory', { method: 'POST', body: JSON.stringify(memForm) })
      getMessage().success('经验已记录，后续同类问题将自动复用')
      setMemForm({ agent_id: '', problem: '', resolution: '' })
      loadMemories(memAgent)
    } catch (e: any) { getMessage().error(`记录失败: ${e.message}`) } finally { setMemSaving(false) }
  }
  const deleteMemory = async (id: string) => {
    try { await apiFetch(`/ai/memory/${id}`, { method: 'DELETE' }); setMemories(ms => ms.filter(m => m.id !== id)) }
    catch (e: any) { getMessage().error(`删除失败: ${e.message}`) }
  }

  const [hireTarget, setHireTarget] = useState<AgentTemplate | null>(null)
  const [hireName, setHireName] = useState('')
  const [withInspection, setWithInspection] = useState(true)
  const [hiring, setHiring] = useState(false)
  const [roleFilter, setRoleFilter] = useState<string>('')

  // —— 发布到技能市场 (L4: MCP 打包共享, 数字员工 + 依赖工具打包成市场形态) ——
  const [publishOpen, setPublishOpen] = useState(false)
  const [publishAgent, setPublishAgent] = useState('')
  const [publishMeta, setPublishMeta] = useState({ name: '', version: '1.0.0', author: '', description: '', category: '', tags: '' })
  const [publishing, setPublishing] = useState(false)

  const doPublish = async () => {
    if (!publishAgent) return getMessage().error('请选择要发布的数字员工')
    setPublishing(true)
    try {
      await apiFetch('/ai/skills/publish', {
        method: 'POST',
        body: JSON.stringify({
          agent_id: publishAgent,
          name: publishMeta.name || undefined,
          version: publishMeta.version || '1.0.0',
          author: publishMeta.author || undefined,
          description: publishMeta.description || undefined,
          category: publishMeta.category || undefined,
          tags: publishMeta.tags ? publishMeta.tags.split(/[,\s]+/).filter(Boolean) : undefined,
        }),
      })
      getMessage().success('已发布到技能市场，可在「技能市场」页浏览与安装')
      setPublishOpen(false)
      setPublishAgent('')
      setPublishMeta({ name: '', version: '1.0.0', author: '', description: '', category: '', tags: '' })
    } catch (e: any) {
      getMessage().error(`发布失败: ${e.message}`)
    } finally {
      setPublishing(false)
    }
  }

  useEffect(() => {
    loadTemplates()
    loadInspectionTemplates()
    fetchAgents()
    loadMemories()
  }, [loadTemplates, loadInspectionTemplates, fetchAgents])

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
    setWithInspection(true)
  }

  const confirmHire = async () => {
    if (!hireTarget) return
    setHiring(true)
    const ok = await instantiateTemplate(hireTarget.id, hireName.trim(), withInspection)
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
            预置岗位 Agent 矩阵，一键上岗即可在对话与团队编排中使用，无需从空白开始搭建。
            <span className="text-primary/80">「上岗」本质是按模板创建一个 Agent，与「智能体工作台 → 我的智能体」从空白创建的入口殊途同归，上岗后的员工可在我的智能体页继续编辑。</span>
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => { loadTemplates(); fetchAgents() }}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            <RefreshCw className={cn('h-4 w-4', loading && 'animate-spin')} />刷新
          </button>
          <Button size="sm" variant="outline" className="gap-1" onClick={() => setPublishOpen(true)}>
            <Store className="h-3.5 w-3.5" />发布到市场
          </Button>
        </div>
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

      {/* P0-1 长期记忆: Agent 经验跨会话复用 */}
      <Card>
        <CardContent className="pt-4 space-y-3">
          <div className="flex items-center justify-between gap-3 flex-wrap">
            <div>
              <div className="text-sm font-medium flex items-center gap-2"><Brain className="h-4 w-4 text-primary" />长期记忆</div>
              <p className="text-xs text-muted-foreground mt-0.5">处置成功的问题自动沉淀为经验，下次同类巡检/对话直接复用（越用越聪明）。也可手动记录。</p>
            </div>
            <select className="h-9 rounded-md border border-input bg-background px-2 text-xs max-w-[200px]" value={memAgent} onChange={e => { setMemAgent(e.target.value); loadMemories(e.target.value) }}>
              <option value="">全部 Agent</option>
              {agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
            </select>
          </div>

          <div className="flex gap-2 items-end flex-wrap">
            <div className="flex-1 min-w-[160px]">
              <label className="text-xs text-muted-foreground">问题（自动抽取关键词）</label>
              <Input className="mt-1" value={memForm.problem} onChange={e => setMemForm(f => ({ ...f, problem: e.target.value }))} placeholder="如: 数据库连接数接近上限" />
            </div>
            <div className="flex-1 min-w-[160px]">
              <label className="text-xs text-muted-foreground">处置方案</label>
              <Input className="mt-1" value={memForm.resolution} onChange={e => setMemForm(f => ({ ...f, resolution: e.target.value }))} placeholder="如: 扩容连接池并排查慢查询" />
            </div>
            <div className="w-32">
              <label className="text-xs text-muted-foreground">绑定 Agent（可选）</label>
              <select className="mt-1 h-10 w-full rounded-md border border-input bg-background px-2 text-xs" value={memForm.agent_id} onChange={e => setMemForm(f => ({ ...f, agent_id: e.target.value }))}>
                <option value="">通用经验</option>
                {agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
              </select>
            </div>
            <Button size="sm" className="gap-1" onClick={addMemory} disabled={memSaving}>
              <Brain className="h-3.5 w-3.5" />记录经验
            </Button>
          </div>

          {memories.length === 0 ? (
            <p className="text-xs text-muted-foreground py-2">暂无记忆。巡检发现转工单处置成功后会自动沉淀到这里。</p>
          ) : (
            <div className="space-y-2">
              {memories.map(m => (
                <div key={m.id} className="flex items-center gap-3 rounded-lg border border-border p-2.5">
                  <div className="flex-1 min-w-0">
                    <div className="text-sm font-medium truncate">{m.problem}</div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      处置: {m.resolution} · {m.agent_id ? `绑定 ${agents.find(a => a.id === m.agent_id)?.name || m.agent_id}` : '通用'} · {m.source === 'disposal_case' ? '工单沉淀' : '手动'}
                    </div>
                  </div>
                  <Badge variant={m.outcome === 'false_positive' ? 'secondary' : 'success'} className="text-[10px] shrink-0">
                    {m.outcome === 'false_positive' ? '误报' : '成功'}
                  </Badge>
                  <span className="text-[11px] text-muted-foreground shrink-0" title="被复用次数">复用 {m.hit_count}</span>
                  <Button size="sm" variant="ghost" className="h-6 w-6 shrink-0" onClick={() => deleteMemory(m.id)}><Trash2 className="h-3 w-3" /></Button>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

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
            {hireTarget.inspection_template_id && (
              <div className="flex items-center justify-between gap-3 rounded-lg border border-border p-3">
                <div className="min-w-0">
                  <div className="text-sm font-medium">同时创建关联巡检任务</div>
                  <div className="text-xs text-muted-foreground mt-0.5">
                    自动启用「{inspectionTemplates.find(t => t.id === hireTarget.inspection_template_id)?.name || '关联巡检'}」，让数字员工上岗即开始主动看护
                  </div>
                </div>
                <Switch checked={withInspection} onCheckedChange={setWithInspection} />
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => setHireTarget(null)}>取消</Button>
          <Button onClick={confirmHire} disabled={hiring}>
            {hiring ? '创建中…' : '确认上岗'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* 发布到技能市场 (L4: MCP 打包共享, 数字员工 + 依赖工具打包成市场形态) */}
      <Dialog open={publishOpen} onClose={() => setPublishOpen(false)} title="发布到技能市场">
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">
            把一个数字员工（提示词 / 技能 / 工具）打包成技能包上架，团队可在「技能市场」一键安装复用。
          </p>
          <div>
            <label className="text-xs text-muted-foreground">数字员工</label>
            <Select
              className="mt-1"
              options={[
                { value: '', label: '请选择…' },
                ...agents.map((a: { id: string; name: string }) => ({ value: a.id, label: a.name })),
              ]}
              value={publishAgent}
              onChange={e => setPublishAgent(e.target.value)}
            />
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs text-muted-foreground">技能包名称（可选，留空用员工名）</label>
              <Input className="mt-1" value={publishMeta.name} onChange={e => setPublishMeta(m => ({ ...m, name: e.target.value }))} placeholder="如 MySQL 慢查询巡检员" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">版本</label>
              <Input className="mt-1" value={publishMeta.version} onChange={e => setPublishMeta(m => ({ ...m, version: e.target.value }))} placeholder="1.0.0" />
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div>
              <label className="text-xs text-muted-foreground">作者</label>
              <Input className="mt-1" value={publishMeta.author} onChange={e => setPublishMeta(m => ({ ...m, author: e.target.value }))} placeholder="运维团队" />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">类别</label>
              <Input className="mt-1" value={publishMeta.category} onChange={e => setPublishMeta(m => ({ ...m, category: e.target.value }))} placeholder="数据库 / 中间件 / 通用" />
            </div>
          </div>
          <div>
            <label className="text-xs text-muted-foreground">简介</label>
            <Input className="mt-1" value={publishMeta.description} onChange={e => setPublishMeta(m => ({ ...m, description: e.target.value }))} placeholder="一句话说明这个技能包解决什么" />
          </div>
          <div>
            <label className="text-xs text-muted-foreground">标签（空格或逗号分隔）</label>
            <Input className="mt-1" value={publishMeta.tags} onChange={e => setPublishMeta(m => ({ ...m, tags: e.target.value }))} placeholder="mysql 巡检 慢查询" />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setPublishOpen(false)}>取消</Button>
          <Button variant="primary" onClick={doPublish} disabled={publishing}>
            {publishing ? '发布中…' : '发布'}
          </Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
