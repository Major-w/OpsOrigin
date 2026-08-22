'use client'

import { useEffect, useMemo, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { toast } from '@/stores/toast'
import { Plus, Trash2, Terminal, Wrench, AlertCircle, Loader2, Search, RefreshCw, Boxes, Puzzle, Globe } from 'lucide-react'
import { cn, apiFetch, apiList } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'

// 引擎内建工具: 对模型不可见的底层能力, 不参与 Agent 白名单勾选
const BUILTIN_TOOLS = new Set(['llm_chat', 'http_get'])

interface RegisteredTool {
  name: string
  description?: string
  parameters?: Record<string, { type: string; description?: string; required?: boolean }>
}

interface CustomTool {
  id: string
  name: string
  description?: string
  type: 'http' | 'cli'
  enabled: boolean
  http_method?: string
  http_url?: string
  cli_command?: string
  parameters?: { name: string; type: string; description?: string; required?: boolean }[]
}

type ToolOrigin = 'builtin' | 'platform' | 'custom'

const ORIGIN_META: Record<ToolOrigin, { label: string; desc: string; icon: any; badge: 'secondary' | 'info' | 'success' }> = {
  builtin: { label: '引擎内建', desc: '模型调用底层能力，不对外暴露给 Agent 白名单', icon: Boxes, badge: 'secondary' },
  platform: { label: '平台工具', desc: '平台各模块注册的业务能力（CMDB / 工单 / 脚本 / 知识库）', icon: Puzzle, badge: 'info' },
  custom: { label: '自定义工具', desc: '本页注册的 HTTP API 或本地 CLI，保存即生效', icon: Terminal, badge: 'success' },
}

const EMPTY_FORM = {
  name: '', description: '', type: 'http' as 'http' | 'cli', enabled: true,
  parameters: [{ name: '', type: 'string', description: '', required: false }],
  http_method: 'GET', http_url: '', http_headers: [{ k: '', v: '' }], timeout_sec: 30,
  cli_command: '',
}

export default function MCPToolsPage() {
  const { theme } = useThemeStore()
  const [tools, setTools] = useState<RegisteredTool[]>([])
  const [customTools, setCustomTools] = useState<CustomTool[]>([])
  const [loading, setLoading] = useState(true)
  const [keyword, setKeyword] = useState('')
  const [originFilter, setOriginFilter] = useState<ToolOrigin | 'all'>('all')
  const [saving, setSaving] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [form, setForm] = useState({ ...EMPTY_FORM })
  const [pendingDelete, setPendingDelete] = useState<CustomTool | null>(null)

  const load = async () => {
    setLoading(true)
    try {
      const [reg, custom] = await Promise.all([
        apiList<RegisteredTool>('/ai/tools'),
        apiList<CustomTool>('/ai/tools/custom'),
      ])
      setTools(reg)
      setCustomTools(custom)
    } catch (e: any) {
      toast.error(`加载失败: ${e.message}`)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  // 注册表里同一个自定义工具会与 /ai/tools/custom 重复出现,
  // 这里按名字归并来源, 保证总览列表每个工具只出现一次。
  const customNames = useMemo(() => new Set(customTools.map(t => t.name)), [customTools])
  const originOf = (name: string): ToolOrigin =>
    BUILTIN_TOOLS.has(name) ? 'builtin' : customNames.has(name) ? 'custom' : 'platform'

  const visibleTools = useMemo(() => {
    const kw = keyword.trim().toLowerCase()
    return tools
      .map(t => ({ ...t, origin: originOf(t.name) }))
      .filter(t => originFilter === 'all' || t.origin === originFilter)
      .filter(t => !kw || t.name.toLowerCase().includes(kw) || (t.description || '').toLowerCase().includes(kw))
      .sort((a, b) => a.origin.localeCompare(b.origin) || a.name.localeCompare(b.name))
  }, [tools, customNames, keyword, originFilter])

  const counts = useMemo(() => {
    const c: Record<ToolOrigin, number> = { builtin: 0, platform: 0, custom: 0 }
    tools.forEach(t => { c[originOf(t.name)]++ })
    return c
  }, [tools, customNames])

  const handleCreate = async () => {
    if (!form.name.trim()) return toast.error('请输入工具名（小写字母开头）')
    if (form.type === 'http' && !form.http_url.trim()) return toast.error('HTTP 工具必须填写 URL')
    if (form.type === 'cli' && !form.cli_command.trim()) return toast.error('CLI 工具必须填写命令模板')
    setSaving(true)
    try {
      await apiFetch('/ai/tools/custom', {
        method: 'POST',
        body: JSON.stringify({
          name: form.name.trim(),
          description: form.description,
          type: form.type,
          enabled: form.enabled,
          parameters: form.parameters.filter(p => p.name.trim()),
          http_method: form.http_method,
          http_url: form.http_url,
          http_headers: Object.fromEntries(form.http_headers.filter(h => h.k.trim()).map(h => [h.k.trim(), h.v])),
          timeout_sec: form.timeout_sec,
          cli_command: form.cli_command,
        }),
      })
      toast.success('自定义工具已注册，AI 即刻可调用')
      setForm({ ...EMPTY_FORM })
      setShowForm(false)
      load()
    } catch (e: any) {
      toast.error(`创建失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const handleToggle = async (t: CustomTool) => {
    try {
      await apiFetch(`/ai/tools/custom/${t.id}`, { method: 'PUT', body: JSON.stringify({ ...t, enabled: !t.enabled }) })
      toast.success(t.enabled ? '工具已禁用' : '工具已启用')
      load()
    } catch (e: any) { toast.error(`更新失败: ${e.message}`) }
  }

  const handleDelete = async () => {
    if (!pendingDelete) return
    const t = pendingDelete
    try {
      await apiFetch(`/ai/tools/custom/${t.id}`, { method: 'DELETE' })
      toast.success('已删除')
      load()
    } catch (e: any) { toast.error(`删除失败: ${e.message}`) }
    finally { setPendingDelete(null) }
  }

  const surface = theme === 'dark' ? 'bg-white/[0.02] border border-white/[0.04]' : 'bg-gray-50 border border-gray-100'

  return (
    <div className="h-full flex flex-col p-6 gap-4 overflow-auto">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold flex items-center gap-2"><Wrench className="h-5 w-5 text-emerald-500" />MCP 工具</h1>
          <p className="text-sm text-muted-foreground mt-1">
            统一维护 AI 可调用的工具能力。数字员工在「智能体工作台」中只做勾选，工具的注册、启停与删除都在本页完成。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={load} disabled={loading}>
          <RefreshCw className={cn('h-3.5 w-3.5 mr-1', loading && 'animate-spin')} />刷新
        </Button>
      </div>

      {/* 来源分布 */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        {(Object.keys(ORIGIN_META) as ToolOrigin[]).map(k => {
          const meta = ORIGIN_META[k]
          const Icon = meta.icon
          const active = originFilter === k
          return (
            <button
              key={k}
              onClick={() => setOriginFilter(active ? 'all' : k)}
              className={cn(
                'text-left rounded-xl p-4 transition-colors',
                active ? 'border border-emerald-400 bg-emerald-50 dark:border-emerald-500/40 dark:bg-emerald-500/10' : surface,
              )}
            >
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 text-sm font-medium"><Icon className="h-4 w-4 text-emerald-500" />{meta.label}</div>
                <span className="text-lg font-semibold tabular-nums">{counts[k]}</span>
              </div>
              <p className="text-[11px] text-muted-foreground mt-1.5 leading-relaxed">{meta.desc}</p>
            </button>
          )
        })}
      </div>

      {/* 工具总览 */}
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle className="text-base">已注册工具总览</CardTitle>
            <CardDescription>
              {originFilter === 'all' ? `共 ${tools.length} 个工具` : `已筛选「${ORIGIN_META[originFilter].label}」`}
              ，Agent 引擎运行时可直接调用
            </CardDescription>
          </div>
          <div className="relative w-64">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
            <Input placeholder="搜索工具名或描述" value={keyword} onChange={e => setKeyword(e.target.value)} className="pl-8 h-9" />
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="py-10 text-center text-muted-foreground text-sm"><Loader2 className="h-5 w-5 mx-auto mb-2 animate-spin" />加载中…</div>
          ) : visibleTools.length === 0 ? (
            <div className="text-center py-10 text-muted-foreground">
              <Wrench className="h-8 w-8 mx-auto mb-2 opacity-30" />
              <p className="text-sm">没有匹配的工具</p>
            </div>
          ) : (
            <div className="space-y-2">
              {visibleTools.map(t => {
                const meta = ORIGIN_META[t.origin]
                const paramCount = Object.keys(t.parameters || {}).length
                return (
                  <div key={t.name} className={cn('flex items-start justify-between gap-4 p-3 rounded-lg', surface)}>
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <code className="text-sm font-medium text-emerald-600 dark:text-emerald-400">{t.name}</code>
                        <Badge variant={meta.badge} className="text-[9px]">{meta.label}</Badge>
                        {paramCount > 0 && <span className="text-[10px] text-muted-foreground">{paramCount} 个参数</span>}
                      </div>
                      {t.description && <p className="text-xs text-muted-foreground mt-1">{t.description}</p>}
                      {paramCount > 0 && (
                        <div className="flex flex-wrap gap-1 mt-1.5">
                          {Object.entries(t.parameters || {}).map(([k, p]) => (
                            <span key={k} className="text-[10px] px-1.5 py-0.5 rounded border border-gray-200 dark:border-white/[0.08] text-muted-foreground">
                              {k}: {p.type}{p.required ? '*' : ''}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 自定义工具管理 */}
      <Card>
        <CardHeader className="flex-row items-center justify-between space-y-0">
          <div>
            <CardTitle className="text-base flex items-center gap-2"><Terminal className="h-4 w-4 text-emerald-500" />自定义工具</CardTitle>
            <CardDescription>把外部 HTTP API 或本机 CLI 接入为 AI 能力，注册后即时生效，无需重启</CardDescription>
          </div>
          <Button size="sm" onClick={() => setShowForm(v => !v)}>
            <Plus className="h-3.5 w-3.5 mr-1" />{showForm ? '收起表单' : '注册工具'}
          </Button>
        </CardHeader>
        <CardContent className="space-y-4">
          {showForm && (
            <div className={cn('rounded-lg border p-4 space-y-3', theme === 'dark' ? 'border-white/[0.06] bg-white/[0.02]' : 'border-gray-200 bg-gray-50/60')}>
              <div className="grid grid-cols-2 gap-2">
                <Input placeholder="工具名 (如: weather_query)" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
                <Input placeholder="描述 (告诉 AI 何时使用)" value={form.description} onChange={e => setForm({ ...form, description: e.target.value })} />
              </div>
              <div className="flex items-center gap-4">
                <div className="flex gap-2">
                  {(['http', 'cli'] as const).map(tp => (
                    <label key={tp} className={cn('flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border cursor-pointer', form.type === tp ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400' : 'border-gray-200 text-muted-foreground dark:border-[rgba(255,255,255,0.08)]')}>
                      <input type="radio" checked={form.type === tp} onChange={() => setForm({ ...form, type: tp })} className="accent-emerald-500" />
                      {tp === 'http' ? <><Globe className="h-3 w-3" />HTTP API</> : <><Terminal className="h-3 w-3" />CLI 命令</>}
                    </label>
                  ))}
                </div>
                <label className="flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer">
                  <input type="checkbox" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} className="accent-emerald-500" />
                  创建后立即启用
                </label>
              </div>

              {form.type === 'http' ? (
                <div className="space-y-2">
                  <div className="flex gap-2">
                    <select value={form.http_method} onChange={e => setForm({ ...form, http_method: e.target.value })} className="h-10 rounded-md border border-input bg-background px-2 text-sm">
                      {['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(m => <option key={m} value={m}>{m}</option>)}
                    </select>
                    <Input placeholder="URL 模板, 支持 {param} 占位符 (如 https://api.example.com/hosts/{name})" value={form.http_url} onChange={e => setForm({ ...form, http_url: e.target.value })} className="flex-1" />
                    <Input type="number" min={1} max={120} value={form.timeout_sec} onChange={e => setForm({ ...form, timeout_sec: parseInt(e.target.value) || 30 })} className="w-20" title="超时(秒)" />
                  </div>
                  <div className="space-y-1.5">
                    <p className="text-[11px] text-muted-foreground">请求头（可选）</p>
                    {form.http_headers.map((h, i) => (
                      <div key={i} className="flex gap-2">
                        <Input placeholder="Header 名" value={h.k} onChange={e => { const arr = [...form.http_headers]; arr[i] = { ...arr[i], k: e.target.value }; setForm({ ...form, http_headers: arr }) }} className="w-48" />
                        <Input placeholder="值" value={h.v} onChange={e => { const arr = [...form.http_headers]; arr[i] = { ...arr[i], v: e.target.value }; setForm({ ...form, http_headers: arr }) }} className="flex-1" />
                        <Button variant="ghost" size="icon" className="h-10 w-8 shrink-0" onClick={() => setForm({ ...form, http_headers: form.http_headers.filter((_, idx) => idx !== i) })}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                      </div>
                    ))}
                    <Button variant="outline" size="sm" onClick={() => setForm({ ...form, http_headers: [...form.http_headers, { k: '', v: '' }] })}><Plus className="h-3.5 w-3.5 mr-1" />加请求头</Button>
                  </div>
                </div>
              ) : (
                <div className="space-y-2">
                  <Input placeholder="CLI 命令模板, 支持 {param} (如: df -h | grep {mount})" value={form.cli_command} onChange={e => setForm({ ...form, cli_command: e.target.value })} />
                  <div className={cn('flex items-start gap-2 text-[11px] p-2.5 rounded-lg', theme === 'dark' ? 'bg-red-500/5 border border-red-500/10' : 'bg-red-50 border border-red-200')}>
                    <AlertCircle className="h-3.5 w-3.5 text-red-500 shrink-0 mt-0.5" />
                    <span className="text-red-600 dark:text-red-400">CLI 工具在服务器本地以 shell 执行，存在命令注入风险。仅建议管理员在可信环境使用，并对参数做枚举约束。</span>
                  </div>
                </div>
              )}

              <div className="space-y-1.5">
                <p className="text-[11px] text-muted-foreground">参数定义（供 LLM 识别调用参数）</p>
                {form.parameters.map((p, i) => (
                  <div key={i} className="flex gap-2 items-center">
                    <Input placeholder="参数名" value={p.name} onChange={e => { const arr = [...form.parameters]; arr[i] = { ...arr[i], name: e.target.value }; setForm({ ...form, parameters: arr }) }} className="w-36" />
                    <select value={p.type} onChange={e => { const arr = [...form.parameters]; arr[i] = { ...arr[i], type: e.target.value }; setForm({ ...form, parameters: arr }) }} className="h-10 rounded-md border border-input bg-background px-2 text-sm w-28">
                      <option value="string">string</option>
                      <option value="number">number</option>
                      <option value="boolean">boolean</option>
                    </select>
                    <Input placeholder="描述" value={p.description} onChange={e => { const arr = [...form.parameters]; arr[i] = { ...arr[i], description: e.target.value }; setForm({ ...form, parameters: arr }) }} className="flex-1" />
                    <label className="flex items-center gap-1 text-[11px] text-muted-foreground whitespace-nowrap cursor-pointer">
                      <input type="checkbox" checked={p.required} onChange={e => { const arr = [...form.parameters]; arr[i] = { ...arr[i], required: e.target.checked }; setForm({ ...form, parameters: arr }) }} className="accent-emerald-500" />必填
                    </label>
                    <Button variant="ghost" size="icon" className="h-10 w-8 shrink-0" onClick={() => setForm({ ...form, parameters: form.parameters.filter((_, idx) => idx !== i) })}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                  </div>
                ))}
                <Button variant="outline" size="sm" onClick={() => setForm({ ...form, parameters: [...form.parameters, { name: '', type: 'string', description: '', required: false }] })}><Plus className="h-3.5 w-3.5 mr-1" />加参数</Button>
              </div>

              <Button onClick={handleCreate} disabled={saving} className="w-full">
                {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}注册工具
              </Button>
            </div>
          )}

          <div className="space-y-1.5">
            {customTools.length === 0 ? (
              <p className="text-xs text-muted-foreground p-6 text-center">还没有自定义工具，点右上角「注册工具」接入第一个外部能力</p>
            ) : (
              customTools.map(t => (
                <div key={t.id} className={cn('flex items-center justify-between p-3 rounded-lg', surface)}>
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <code className="text-sm font-medium text-emerald-600 dark:text-emerald-400">{t.name}</code>
                      <Badge variant={t.type === 'http' ? 'info' : 'warning'} className="text-[9px]">{t.type === 'http' ? 'HTTP' : 'CLI'}</Badge>
                      {!t.enabled && <Badge variant="secondary" className="text-[9px]">已禁用</Badge>}
                    </div>
                    {t.description && <p className="text-[11px] text-muted-foreground truncate mt-0.5">{t.description}</p>}
                    <p className="text-[10px] text-muted-foreground truncate mt-0.5">
                      {t.type === 'http' ? `${t.http_method} ${t.http_url}` : t.cli_command}
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      onClick={() => handleToggle(t)}
                      className={cn('relative w-9 h-5 rounded-full transition-colors', t.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600')}
                      title={t.enabled ? '禁用' : '启用'}
                    >
                      <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all', t.enabled ? 'left-[18px]' : 'left-0.5')} />
                    </button>
                    <Button variant="ghost" size="sm" onClick={() => setPendingDelete(t)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                  </div>
                </div>
              ))
            )}
          </div>
        </CardContent>
      </Card>

      <ConfirmDialog
        open={!!pendingDelete}
        title="删除自定义工具"
        description={pendingDelete ? `删除自定义工具「${pendingDelete.name}」？删除后 AI 将无法调用，引用该工具的数字员工需自行调整白名单。` : ''}
        confirmLabel="删除"
        onClose={() => setPendingDelete(null)}
        onConfirm={handleDelete}
      />
    </div>
  )
}
