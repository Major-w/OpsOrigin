'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { toast } from '@/stores/toast'
import { Bot, Plus, Trash2, Save, Wrench, Brain, Settings2, Terminal, Wand2, AlertCircle, CheckCircle2, Loader2, ChevronRight, ShieldCheck, Database, Download, RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { useAIStore, type ApprovalMode } from '@/stores/ai'
const APPROVAL_MODES: { value: ApprovalMode; label: string; desc: string }[] = [
  { value: 'sensitive_only', label: '仅敏感操作询问', desc: '默认安全策略：script_run 等写/执行类操作弹窗确认，只读操作直接执行' },
  { value: 'always_ask', label: '每次询问', desc: '所有 AI 操作执行前都须人工确认，最严格' },
  { value: 'always_allow', label: '始终允许', desc: 'AI 操作直接执行不询问（高风险，仅建议开发/测试环境）' },
]

const DEFAULT_SENSITIVE_TOOLS = ['script_run', 'ticket_create']

// 知识库分类兜底(后端无分类元数据接口, 从文章去重拉取; 失败时用此列表)
const FALLBACK_CATEGORIES = ['常见问题', '变更操作', '故障处理', '最佳实践', '参考文档']

// 内建工具(对模型不可见), 不出现在 Agent 工具选择列表
const BUILTIN_TOOLS = new Set(['llm_chat', 'http_get'])

// AgentSkill 前端表单类型
interface SkillFormItem { name: string; description: string; instructions: string }

const EMPTY_SKILL: SkillFormItem = { name: '', description: '', instructions: '' }

// SkillsEditor 可复用能力编辑器(名称/用途/使用说明), 创建与编辑 Agent 共用。
function SkillsEditor({ value, onChange }: { value: SkillFormItem[]; onChange: (v: SkillFormItem[]) => void }) {
  const update = (i: number, patch: Partial<SkillFormItem>) => {
    onChange(value.map((s, idx) => idx === i ? { ...s, ...patch } : s))
  }
  return (
    <div className="space-y-2">
      {value.map((s, i) => (
        <div key={i} className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.08)] p-3 space-y-2">
          <div className="flex gap-2">
            <Input placeholder="技能名称 (如: 日志分析)" value={s.name} onChange={e => update(i, { name: e.target.value })} className="flex-1" />
            <Button variant="ghost" size="icon" className="h-8 w-8 shrink-0" onClick={() => onChange(value.filter((_, idx) => idx !== i))}>
              <Trash2 className="h-3.5 w-3.5 text-red-400" />
            </Button>
          </div>
          <Input placeholder="用途描述 (一句话说明该技能解决什么问题)" value={s.description} onChange={e => update(i, { description: e.target.value })} />
          <textarea
            placeholder="使用说明 (触发条件与执行步骤, 将注入系统提示词)"
            value={s.instructions}
            onChange={e => update(i, { instructions: e.target.value })}
            rows={2}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-xs resize-none"
          />
        </div>
      ))}
      <Button variant="outline" size="sm" onClick={() => onChange([...value, { ...EMPTY_SKILL }])}>
        <Plus className="h-3.5 w-3.5 mr-1" />添加技能
      </Button>
    </div>
  )
}

export default function AISettingsPage() {
  const { agents, fetchAgents, createAgent } = useAIStore()
  const { theme } = useThemeStore()
  const [activeTab, setActiveTab] = useState('agents')
  const [form, setForm] = useState<{ name: string; model_name: string; system_prompt: string; knowledge_categories: string[]; tools: string[]; skills: SkillFormItem[] }>({ name: '', model_name: '', system_prompt: '', knowledge_categories: [], tools: [], skills: [] })
  const [sysConfig, setSysConfig] = useState<any>(null)
  const [llmForm, setLlmForm] = useState({ api_key: '', model: '', base_url: '', timeout: 60 })
  const [modelPool, setModelPool] = useState<{ name: string; model: string; base_url: string; weight: number; healthy: boolean }[]>([])
  const [datasetStats, setDatasetStats] = useState<{ total: number; preference_up: number; preference_down: number; disposal_cases: number } | null>(null)
  const [datasetLoading, setDatasetLoading] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [editForm, setEditForm] = useState<{ name: string; model_name: string; system_prompt: string; knowledge_categories: string[]; tools: string[]; skills: SkillFormItem[] }>({ name: '', model_name: '', system_prompt: '', knowledge_categories: [], tools: [], skills: [] })
  const [tools, setTools] = useState<any[]>([])
  const [testing, setTesting] = useState(false)
  const [testStatus, setTestStatus] = useState<string | null>(null)
  // 自定义 MCP 工具
  const [customTools, setCustomTools] = useState<any[]>([])
  const [ctSaving, setCtSaving] = useState(false)
  const [ctForm, setCtForm] = useState({
    name: '', description: '', type: 'http', enabled: true,
    parameters: [{ name: '', type: 'string', description: '', required: false }],
    http_method: 'GET', http_url: '', http_headers: [{ k: '', v: '' }], timeout_sec: 30,
    cli_command: '',
  })
  // 知识库分类(从文章去重拉取, 兜底硬编码)
  const [kbCategories, setKbCategories] = useState<string[]>(FALLBACK_CATEGORIES)
  // 可选工具(排除内建): 供 Agent 工具白名单勾选
  const selectableTools = tools.filter(t => !BUILTIN_TOOLS.has(t.name))

  // —— 审批策略状态 ——
  const { getApprovalPolicy, putApprovalPolicy } = useAIStore()
  const [approvalMode, setApprovalMode] = useState<ApprovalMode>('sensitive_only')
  const [sensitiveTools, setSensitiveTools] = useState<string[]>(DEFAULT_SENSITIVE_TOOLS)
  const [askTimeout, setAskTimeout] = useState(60)
  const [policySaving, setPolicySaving] = useState(false)
  // 作用范围: global(默认) / agent(按 Agent 覆盖)
  const [approvalScope, setApprovalScope] = useState<'global' | 'agent'>('global')
  const [approvalAgentId, setApprovalAgentId] = useState('')

  useEffect(() => { fetchAgents(); loadTools(); loadSysConfig(); loadApprovalPolicy(); loadKbCategories(); loadCustomTools(); loadModelPool() }, [])
  const loadModelPool = async () => {
    try {
      const r = await fetch('/api/v1/ai/models')
      if (r.ok) setModelPool(await r.json())
    } catch {}
  }

  const loadDatasetStats = async () => {
    setDatasetLoading(true)
    try {
      const r = await fetch('/api/v1/ai/dataset?format=json')
      if (!r.ok) throw new Error(`导出失败: ${r.status}`)
      const d = await r.json()
      setDatasetStats(d.summary)
    } catch (e: any) {
      toast.error(`获取数据集统计失败: ${e.message}`)
    } finally {
      setDatasetLoading(false)
    }
  }

  const downloadDataset = async () => {
    setDatasetLoading(true)
    try {
      const r = await fetch('/api/v1/ai/dataset?format=jsonl')
      if (!r.ok) throw new Error(`导出失败: ${r.status}`)
      const blob = await r.blob()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = 'ai-dataset.ndjson'
      a.click()
      URL.revokeObjectURL(url)
      toast.success('数据集已导出')
    } catch (e: any) {
      toast.error(`导出失败: ${e.message}`)
    } finally {
      setDatasetLoading(false)
    }
  }
  
  const loadCustomTools = async () => {
    try {
      const r = await fetch('/api/v1/ai/tools/custom')
      if (r.ok) setCustomTools(await r.json())
    } catch {}
  }

  const handleCreateCustomTool = async () => {
    const f = ctForm
    if (!f.name.trim()) return toast.error('请输入工具名（小写字母开头）')
    if (f.type === 'http' && !f.http_url.trim()) return toast.error('HTTP 工具必须填写 URL')
    if (f.type === 'cli' && !f.cli_command.trim()) return toast.error('CLI 工具必须填写命令模板')
    setCtSaving(true)
    try {
      const r = await fetch('/api/v1/ai/tools/custom', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: f.name.trim(),
          description: f.description,
          type: f.type,
          enabled: f.enabled,
          parameters: f.parameters.filter(p => p.name.trim()),
          http_method: f.http_method,
          http_url: f.http_url,
          http_headers: Object.fromEntries(f.http_headers.filter(h => h.k.trim()).map(h => [h.k.trim(), h.v])),
          timeout_sec: f.timeout_sec,
          cli_command: f.cli_command,
        }),
      })
      if (!r.ok) { const d = await r.json().catch(() => ({})); toast.error(d?.error || '创建失败'); return }
      toast.success('自定义工具已创建并注册')
      setCtForm({ name: '', description: '', type: 'http', enabled: true, parameters: [{ name: '', type: 'string', description: '', required: false }], http_method: 'GET', http_url: '', http_headers: [{ k: '', v: '' }], timeout_sec: 30, cli_command: '' })
      loadCustomTools()
    } catch { toast.error('创建失败') } finally { setCtSaving(false) }
  }

  const handleToggleCustomTool = async (t: any) => {
    try {
      const r = await fetch(`/api/v1/ai/tools/custom/${t.id}`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ...t, enabled: !t.enabled }),
      })
      if (!r.ok) { const d = await r.json().catch(() => ({})); toast.error(d?.error || '更新失败'); return }
      toast.success(t.enabled ? '工具已禁用' : '工具已启用')
      loadCustomTools()
    } catch { toast.error('更新失败') }
  }

  const handleDeleteCustomTool = async (id: string) => {
    if (!confirm('删除该自定义工具？删除后 AI 将无法调用。')) return
    try {
      await fetch(`/api/v1/ai/tools/custom/${id}`, { method: 'DELETE' })
      toast.success('已删除')
      loadCustomTools()
    } catch { toast.error('删除失败') }
  }
  
  const loadKbCategories = async () => {
    try {
      const r = await fetch('/api/v1/knowledge?limit=500')
      if (r.ok) {
        const d = await r.json()
        const list = Array.isArray(d) ? d : d?.items || []
        const cats = Array.from(new Set(list.map((a: any) => a.category).filter(Boolean) as string[]))
        if (cats.length > 0) setKbCategories(cats)
      }
    } catch {}
  }
  
  const loadSysConfig = async () => {
    try {
      const r = await fetch('/api/v1/system/config')
      if (r.ok) { const d = await r.json(); setSysConfig(d?.config || d) }
    } catch {}
  }
  
  const loadTools = async () => {
    try {
      const r = await fetch('/api/v1/ai/tools')
      if (r.ok) { const d = await r.json(); setTools(Array.isArray(d) ? d : d?.tools || []) }
    } catch {}
  }

  const loadApprovalPolicy = async (agentId?: string) => {
    const p = await getApprovalPolicy(agentId)
    if (p) {
      setApprovalMode(p.mode || 'sensitive_only')
      setSensitiveTools(p.sensitive_tools?.length ? p.sensitive_tools : DEFAULT_SENSITIVE_TOOLS)
      setAskTimeout(p.ask_timeout_sec || 60)
    }
  }

  const handleSaveApprovalPolicy = async () => {
    setPolicySaving(true)
    // agent 级: 携带 agent_id 保存覆盖策略; 全局: 不带 agent_id
    const payload: any = { mode: approvalMode, sensitive_tools: sensitiveTools, ask_timeout_sec: askTimeout }
    if (approvalScope === 'agent' && approvalAgentId) payload.agent_id = approvalAgentId
    await putApprovalPolicy(payload)
    setPolicySaving(false)
  }

  const handleCreateAgent = async () => {
    if (!form.name.trim()) return toast.error('请输入 Agent 名称')
    setSaving(true)
    const ok = await createAgent({
      name: form.name,
      model_name: form.model_name,
      system_prompt: form.system_prompt,
      knowledge_categories: form.knowledge_categories,
      tools: form.tools,
      skills: form.skills.filter(s => s.name.trim()),
    })
    setSaving(false)
    if (ok) { setForm({ name: '', model_name: '', system_prompt: '', knowledge_categories: [], tools: [], skills: [] }); fetchAgents() }
  }

  const handleDeleteAgent = async (id: string) => {
    if (!confirm('确定删除该 Agent？')) return
    await fetch(`/api/v1/ai/agents/${id}`, { method: 'DELETE' })
    toast.success('Agent 已删除')
    fetchAgents()
  }

  const handleTestConnection = async () => {
    setTesting(true); setTestStatus(null)
    try {
      const r = await fetch('/api/v1/ai/chat', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ prompt: 'ping', reasoning: false, stream: false }) })
      const d = await r.json()
      setTestStatus(d.content || d.thinking ? '✅ 连接成功！LLM 正常响应' : '❌ ' + (d.error || '未知错误'))
    } catch { setTestStatus('❌ 网络错误，请检查 Base URL') }
    setTesting(false)
  }

  const handleSaveEdit = async (id: string) => {
    setSaving(true)
    await fetch(`/api/v1/ai/agents/${id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: editForm.name,
        model_name: editForm.model_name,
        system_prompt: editForm.system_prompt,
        knowledge_categories: editForm.knowledge_categories,
        tools: editForm.tools,
        skills: editForm.skills.filter(s => s.name.trim()),
      }),
    })
    toast.success('Agent 已更新')
    setSaving(false); setEditing(null); fetchAgents()
  }

  const handleUpdateLLM = async (key: string, value: string) => {
    setSaving(true)
    try {
      await fetch('/api/v1/system/config', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ updates: { [`OPSNEXUS_LLM_${key.toUpperCase()}`]: key === 'timeout' ? String(parseInt(value)||60) : value } }),
      })
      toast.success(`${key} 已更新，即时生效`)
      loadSysConfig()
    } catch { toast.error('保存失败') }
    setSaving(false)
  }

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Settings2 className="h-6 w-6 text-emerald-500" />
            AI 智能体配置
          </h1>
          <p className="text-sm text-muted-foreground">管理 Agent、提示词、模型和 MCP 工具</p>
        </div>
      </div>

      {/* 模型池 datalist: 为 Agent 表单的模型名提供下拉建议 */}
      <datalist id="model-pool">
        {modelPool.map(m => (
          <option key={m.name} value={m.name}>{m.model}</option>
        ))}
      </datalist>
      <Tabs value={activeTab} onValueChange={setActiveTab} className="flex-1 flex flex-col min-h-0">
        <TabsList className="mb-4">
          <TabsTrigger value="agents"><Bot className="h-4 w-4 mr-1.5" />智能体</TabsTrigger>
          <TabsTrigger value="prompts"><Wand2 className="h-4 w-4 mr-1.5" />提示词</TabsTrigger>
          <TabsTrigger value="model"><Brain className="h-4 w-4 mr-1.5" />模型</TabsTrigger>
          <TabsTrigger value="tools"><Wrench className="h-4 w-4 mr-1.5" />MCP 工具</TabsTrigger>
          <TabsTrigger value="approval"><ShieldCheck className="h-4 w-4 mr-1.5" />审批策略</TabsTrigger>
        </TabsList>

        {/* 智能体管理 */}
        <TabsContent value="agents" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">创建智能体</CardTitle><CardDescription>定义 Agent 的名称、模型、系统提示词和知识库绑定</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              <Input placeholder="Agent 名称 (如: 运维助手)" value={form.name} onChange={e => setForm({...form, name: e.target.value})} />
              <Input list="model-pool" placeholder="模型名 (如: deepseek-chat / provider 名, 留空使用默认)" value={form.model_name} onChange={e => setForm({...form, model_name: e.target.value})} />
              <textarea placeholder="系统提示词, 定义 Agent 的行为和角色..." value={form.system_prompt} onChange={e => setForm({...form, system_prompt: e.target.value})} rows={4} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 resize-none" />
              {/* 知识库绑定 */}
              <div>
                <label className="text-xs font-medium mb-1.5 block">
                  绑定知识库分类 <span className="text-muted-foreground font-normal">(留空 = 检索全部知识库)</span>
                </label>
                <div className="flex gap-2 flex-wrap">
                  {kbCategories.map(cat => (
                    <label key={cat} className={cn(
                      'flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border cursor-pointer transition-colors',
                      form.knowledge_categories.includes(cat)
                        ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400'
                        : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
                    )}>
                      <input type="checkbox"
                        checked={form.knowledge_categories.includes(cat)}
                        onChange={e => {
                          setForm(f => ({
                            ...f,
                            knowledge_categories: e.target.checked
                              ? [...f.knowledge_categories, cat]
                              : f.knowledge_categories.filter(c => c !== cat),
                          }))
                        }}
                        className="accent-emerald-500" />
                      {cat}
                    </label>
                  ))}
                </div>
                <p className="text-[11px] text-muted-foreground mt-1.5">绑定后，该 Agent 的 RAG 检索与 knowledge_search 工具仅在这些分类内查询（PRD 5.2.6）。</p>
              </div>
              {/* MCP 工具选择 */}
              <div>
                <label className="text-xs font-medium mb-1.5 block">
                  可用 MCP 工具 <span className="text-muted-foreground font-normal">(留空 = 全部)</span>
                </label>
                {selectableTools.length === 0 ? (
                  <p className="text-xs text-muted-foreground">暂无可用工具。</p>
                ) : (
                  <div className="flex gap-2 flex-wrap">
                    {selectableTools.map(t => (
                      <label key={t.name} className={cn(
                        'flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border cursor-pointer transition-colors',
                        form.tools.includes(t.name)
                          ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400'
                          : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
                      )}>
                        <input type="checkbox"
                          checked={form.tools.includes(t.name)}
                          onChange={e => {
                            setForm(f => ({
                              ...f,
                              tools: e.target.checked ? [...f.tools, t.name] : f.tools.filter(x => x !== t.name),
                            }))
                          }}
                          className="accent-emerald-500" />
                        <code className="font-mono">{t.name}</code>
                      </label>
                    ))}
                  </div>
                )}
                <p className="text-[11px] text-muted-foreground mt-1.5">未勾选的工具对模型不可见（不可调用）；script_run 等执行类工具仍受审批策略保护。</p>
              </div>
              {/* Skills 定义 */}
              <div>
                <label className="text-xs font-medium mb-1.5 block">定义 Skills（可复用能力）</label>
                <SkillsEditor value={form.skills} onChange={skills => setForm(f => ({ ...f, skills }))} />
                <p className="text-[11px] text-muted-foreground mt-1.5">技能将以"能力指令"注入系统提示词，模型在相关场景下遵循执行（PRD 5.2.2）。</p>
              </div>
              <Button onClick={handleCreateAgent} disabled={saving} className="w-full">
                {saving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />} 创建 Agent
              </Button>
            </CardContent>
          </Card>

          <div className="space-y-2">
            <h3 className="text-sm font-medium">已有智能体 ({agents.length})</h3>
            {agents.length === 0 ? (
              <p className="text-xs text-muted-foreground p-4 text-center">暂无 Agent，上方创建一个</p>
            ) : (
              agents.map((a: any) => (
                <Card key={a.id} className="hover:shadow-sm transition-shadow">
                  <CardContent className="p-4">
                    {editing === a.id ? (
                      <div className="space-y-2">
                        <Input value={editForm.name} onChange={e => setEditForm({...editForm, name: e.target.value})} placeholder="名称" />
                        <Input list="model-pool" value={editForm.model_name} onChange={e => setEditForm({...editForm, model_name: e.target.value})} placeholder="模型名" />
                        <textarea rows={3} value={editForm.system_prompt} onChange={e => setEditForm({...editForm, system_prompt: e.target.value})} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none" placeholder="系统提示词" />
                        {/* 编辑态: 知识库绑定 */}
                        <div>
                          <label className="text-xs font-medium mb-1.5 block">绑定知识库分类 <span className="text-muted-foreground font-normal">(留空 = 全部)</span></label>
                          <div className="flex gap-2 flex-wrap">
                            {kbCategories.map(cat => (
                              <label key={cat} className={cn(
                                'flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border cursor-pointer transition-colors',
                                editForm.knowledge_categories.includes(cat)
                                  ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400'
                                  : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
                              )}>
                                <input type="checkbox"
                                  checked={editForm.knowledge_categories.includes(cat)}
                                  onChange={e => {
                                    setEditForm(f => ({
                                      ...f,
                                      knowledge_categories: e.target.checked
                                        ? [...f.knowledge_categories, cat]
                                        : f.knowledge_categories.filter(c => c !== cat),
                                    }))
                                  }}
                                  className="accent-emerald-500" />
                                {cat}
                              </label>
                            ))}
                          </div>
                        </div>
                        {/* 编辑态: MCP 工具选择 */}
                        <div>
                          <label className="text-xs font-medium mb-1.5 block">可用 MCP 工具 <span className="text-muted-foreground font-normal">(留空 = 全部)</span></label>
                          {selectableTools.length === 0 ? (
                            <p className="text-xs text-muted-foreground">暂无可用工具。</p>
                          ) : (
                            <div className="flex gap-2 flex-wrap">
                              {selectableTools.map(t => (
                                <label key={t.name} className={cn(
                                  'flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border cursor-pointer transition-colors',
                                  editForm.tools.includes(t.name)
                                    ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400'
                                    : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
                                )}>
                                  <input type="checkbox"
                                    checked={editForm.tools.includes(t.name)}
                                    onChange={e => {
                                      setEditForm(f => ({
                                        ...f,
                                        tools: e.target.checked ? [...f.tools, t.name] : f.tools.filter(x => x !== t.name),
                                      }))
                                    }}
                                    className="accent-emerald-500" />
                                  <code className="font-mono">{t.name}</code>
                                </label>
                              ))}
                            </div>
                          )}
                        </div>
                        {/* 编辑态: Skills */}
                        <div>
                          <label className="text-xs font-medium mb-1.5 block">定义 Skills（可复用能力）</label>
                          <SkillsEditor value={editForm.skills} onChange={skills => setEditForm(f => ({ ...f, skills }))} />
                        </div>
                        <div className="flex gap-2">
                          <Button size="sm" onClick={() => handleSaveEdit(a.id)} disabled={saving}><Save className="h-3.5 w-3.5 mr-1" />保存</Button>
                          <Button size="sm" variant="outline" onClick={() => setEditing(null)}>取消</Button>
                        </div>
                      </div>
                    ) : (
                      <div className="flex items-center justify-between">
                        <div className="flex-1 min-w-0">
                          <div className="flex items-center gap-2">
                            <Bot className="h-4 w-4 text-emerald-500 shrink-0" />
                            <span className="font-medium text-sm truncate">{a.name}</span>
                            <Badge variant={a.status === 'active' ? 'default' : 'secondary'} className="text-[10px]">{a.status}</Badge>
                          </div>
                          {a.system_prompt && <p className="text-xs text-muted-foreground mt-1 truncate">{a.system_prompt.slice(0, 80)}</p>}
                          <div className="flex items-center gap-2 mt-0.5 flex-wrap">
                            {a.model_name && <p className="text-[10px] text-muted-foreground">模型: {a.model_name}</p>}
                            {a.knowledge_categories?.length > 0 && (
                              <div className="flex gap-1 flex-wrap">
                                {a.knowledge_categories.map((c: string) => (
                                  <Badge key={c} variant="outline" className="text-[9px] text-blue-600 dark:text-blue-400">📚 {c}</Badge>
                                ))}
                              </div>
                            )}
                            {a.tools?.length > 0 && (
                              <Badge variant="outline" className="text-[9px] text-emerald-600 dark:text-emerald-400">🔧 {a.tools.length} 工具</Badge>
                            )}
                            {a.skills?.length > 0 && (
                              <Badge variant="outline" className="text-[9px] text-purple-600 dark:text-purple-400">✨ {a.skills.length} 技能</Badge>
                            )}
                          </div>
                        </div>
                        <div className="flex gap-1 shrink-0">
                          <Button variant="ghost" size="sm" onClick={() => { setEditing(a.id); setEditForm({ name: a.name, model_name: a.model_name || '', system_prompt: a.system_prompt || '', knowledge_categories: a.knowledge_categories || [], tools: a.tools || [], skills: (a.skills || []).map((s: any) => ({ name: s.name || '', description: s.description || '', instructions: s.instructions || '' })) }) }}><Save className="h-3.5 w-3.5" /></Button>
                          <Button variant="ghost" size="sm" onClick={() => handleDeleteAgent(a.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                        </div>
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))
            )}
          </div>
        </TabsContent>

        {/* 提示词模板 */}
        <TabsContent value="prompts" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">默认系统提示词</CardTitle><CardDescription>当 Agent 未单独设置 system_prompt 时使用此默认值</CardDescription></CardHeader>
            <CardContent className="space-y-3">
              <textarea
                defaultValue="你是 OpsOrigin 智能运维助手,帮助用户完成主机巡检、故障排查、报告生成等运维任务。回答应简洁、专业、可操作。"
                rows={5}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono ring-offset-background resize-none"
              />
              <p className="text-xs text-muted-foreground">
                提示：各 Agent 的独立提示词请在「智能体」Tab 中创建 Agent 时填写 system_prompt 字段，
                或在 Agent 卡片中点击编辑。此处显示的是引擎默认回退提示词（只读参考）。
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">提示词最佳实践</CardTitle></CardHeader>
            <CardContent className="space-y-2 text-sm text-muted-foreground">
              <p>✅ 明确定义角色： "你是一位资深 Linux 运维工程师..."</p>
              <p>✅ 限制输出格式： "使用 Markdown 格式回答，代码块标注语言"</p>
              <p>✅ 指定行为边界： "只回答运维相关问题，拒绝无关请求"</p>
              <p>✅ 结合工具能力： "你可以使用 llm_chat 和 http_get 工具辅助回答"</p>
              <p>⚠️ system_prompt 变更后即时生效，无需重启 Core</p>
            </CardContent>
          </Card>
        </TabsContent>

        {/* 模型配置 */}
        <TabsContent value="model" className="flex-1 overflow-auto space-y-4">
          {/* 模型池(多模型路由 P3): provider 状态一览 */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base flex items-center gap-2"><Brain className="h-4 w-4" />模型池（多模型路由）</CardTitle>
              <CardDescription>按权重路由 + 健康检查 + 失败自动降级到下一个可用 provider。Agent 的模型名可填 provider 名或具体模型。</CardDescription>
            </CardHeader>
            <CardContent>
              {modelPool.length === 0 ? (
                <p className="text-xs text-muted-foreground py-2">未配置附加 provider，当前使用主 LLM 后端。</p>
              ) : (
                <div className="space-y-2">
                  {modelPool.map(m => (
                    <div key={m.name} className="flex items-center justify-between gap-3 rounded-lg border border-border p-2.5">
                      <div className="flex items-center gap-2 min-w-0">
                        <span className={cn('h-2 w-2 rounded-full shrink-0', m.healthy ? 'bg-emerald-500' : 'bg-red-500')} />
                        <span className="text-sm font-medium">{m.name}</span>
                        <span className="text-xs text-muted-foreground truncate">{m.model}</span>
                      </div>
                      <div className="flex items-center gap-2 shrink-0 text-[11px] text-muted-foreground">
                        <Badge variant={m.healthy ? 'success' : 'destructive'} className="text-[10px]">{m.healthy ? '健康' : '降级'}</Badge>
                        <span>权重 {m.weight}</span>
                      </div>
                    </div>
                  ))}
                </div>
              )}
              <p className="text-[11px] text-muted-foreground pt-2">修改下方 LLM 配置后热生效：模型池随之热更新，故障中的 provider 保留降级状态，不会因配置刷新而误恢复。</p>
            </CardContent>
          </Card>

          {/* 训练数据集导出(P3-1 反馈驱动微调): 人机反馈 + 处置案例 → JSONL */}
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-base flex items-center gap-2"><Database className="h-4 w-4" />训练数据集（反馈驱动微调）</CardTitle>
              <CardDescription>将用户反馈（点赞/点踩 + 对话上下文）与处置案例（知识库）导出为标准 JSONL 微调数据集，用于持续改进模型。</CardDescription>
            </CardHeader>
            <CardContent>
              <div className="flex items-center justify-between gap-3">
                <div className="text-xs text-muted-foreground">
                  样本构成：点赞偏好（preference_up）· 点踩偏好（preference_down）· 处置案例（disposal_case）
                </div>
                <div className="flex gap-2 shrink-0">
                  <Button size="sm" variant="outline" className="gap-1" onClick={loadDatasetStats} disabled={datasetLoading}>
                    <RefreshCw className={cn('h-3.5 w-3.5', datasetLoading && 'animate-spin')} />查看统计
                  </Button>
                  <Button size="sm" className="gap-1" onClick={downloadDataset} disabled={datasetLoading || !datasetStats?.total}>
                    <Download className="h-3.5 w-3.5" />导出 JSONL
                  </Button>
                </div>
              </div>
              {datasetStats && datasetStats.total > 0 && (
                <div className="mt-3 grid grid-cols-4 gap-2">
                  <div className="rounded-lg bg-muted/50 px-3 py-2"><div className="text-[11px] text-muted-foreground">总样本</div><div className="text-lg font-semibold">{datasetStats.total}</div></div>
                  <div className="rounded-lg bg-muted/50 px-3 py-2"><div className="text-[11px] text-muted-foreground">点赞偏好</div><div className="text-lg font-semibold text-emerald-500">{datasetStats.preference_up}</div></div>
                  <div className="rounded-lg bg-muted/50 px-3 py-2"><div className="text-[11px] text-muted-foreground">点踩偏好</div><div className="text-lg font-semibold text-red-500">{datasetStats.preference_down}</div></div>
                  <div className="rounded-lg bg-muted/50 px-3 py-2"><div className="text-[11px] text-muted-foreground">处置案例</div><div className="text-lg font-semibold text-blue-500">{datasetStats.disposal_cases}</div></div>
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader><CardTitle className="text-base">LLM 后端配置</CardTitle><CardDescription>修改后即时热生效，无需重启 Core</CardDescription></CardHeader>
            <CardContent className="space-y-4">
              <div>
                <label className="text-xs font-medium mb-1 block">API Key</label>
                <div className="flex gap-2">
                  <Input type="password" placeholder="sk-..." value={llmForm.api_key} onChange={e => setLlmForm({...llmForm, api_key: e.target.value})} className="font-mono text-xs" />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('api_key', llmForm.api_key)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">模型名称</label>
                <div className="flex gap-2">
                  <Input placeholder="deepseek-chat" value={llmForm.model} onChange={e => setLlmForm({...llmForm, model: e.target.value})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('model', llmForm.model)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">API Base URL</label>
                <div className="flex gap-2">
                  <Input placeholder="https://api.deepseek.com" value={llmForm.base_url} onChange={e => setLlmForm({...llmForm, base_url: e.target.value})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('base_url', llmForm.base_url)} disabled={saving}>保存</Button>
                </div>
              </div>
              <div>
                <label className="text-xs font-medium mb-1 block">超时 (秒)</label>
                <div className="flex gap-2">
                  <Input type="number" placeholder="60" value={llmForm.timeout} onChange={e => setLlmForm({...llmForm, timeout: parseInt(e.target.value)||60})} />
                  <Button size="sm" variant="outline" onClick={() => handleUpdateLLM('timeout', String(llmForm.timeout))} disabled={saving}>保存</Button>
                </div>
              </div>
              <div className={cn("flex items-center gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-emerald-500/5 border border-emerald-500/10' : 'bg-emerald-50 border border-emerald-200')}>
                <CheckCircle2 className="h-4 w-4 text-emerald-500" />
                <span>修改后即时热生效，已连接的 Agent 无需重启</span>
              </div>
              <div className="flex items-center gap-2 pt-2">
                <Button variant="outline" size="sm" onClick={handleTestConnection} disabled={testing}>
                  {testing ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : null} 测试连接
                </Button>
                {testStatus && <span className="text-xs">{testStatus}</span>}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* MCP 工具 */}
        <TabsContent value="tools" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader><CardTitle className="text-base">已注册 MCP 工具</CardTitle><CardDescription>Agent 引擎可调用的工具列表</CardDescription></CardHeader>
            <CardContent>
              {tools.length === 0 ? (
                <div className="text-center py-8 text-muted-foreground">
                  <Wrench className="h-8 w-8 mx-auto mb-2 opacity-30" />
                  <p className="text-sm">暂无注册工具</p>
                </div>
              ) : (
                <div className="space-y-2">
                  {tools.map((t: any, i: number) => (
                    <div key={i} className={cn("flex items-center justify-between p-3 rounded-lg", theme === 'dark' ? 'bg-white/[0.02] border border-white/[0.04]' : 'bg-gray-50 border border-gray-100')}>
                      <div>
                        <div className="flex items-center gap-2">
                          <Terminal className="h-4 w-4 text-emerald-500" />
                          <span className="font-medium text-sm">{t.name}</span>
                          {t.description && <span className="text-xs text-muted-foreground">— {t.description}</span>}
                        </div>
                      </div>
                      <Badge variant="outline" className="text-[10px]">MCP</Badge>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          {/* 自定义工具注册 (PRD 5.2.2) */}
          <Card>
            <CardHeader>
              <CardTitle className="text-base flex items-center gap-2"><Terminal className="h-4 w-4 text-emerald-500" />自定义 MCP 工具</CardTitle>
              <CardDescription>注册 HTTP API 或本地 CLI 为 AI 可调用的工具，保存后即时生效</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              {/* 新建表单 */}
              <div className={cn("rounded-lg border p-4 space-y-3", theme === 'dark' ? 'border-white/[0.06] bg-white/[0.02]' : 'border-gray-200 bg-gray-50/60')}>
                <p className="text-xs font-medium">新建自定义工具</p>
                <div className="grid grid-cols-2 gap-2">
                  <Input placeholder="工具名 (如: weather_query)" value={ctForm.name} onChange={e => setCtForm({ ...ctForm, name: e.target.value })} />
                  <Input placeholder="描述 (告诉 AI 何时使用)" value={ctForm.description} onChange={e => setCtForm({ ...ctForm, description: e.target.value })} />
                </div>
                <div className="flex items-center gap-4">
                  <div className="flex gap-2">
                    {(['http', 'cli'] as const).map(tp => (
                      <label key={tp} className={cn('flex items-center gap-1.5 text-xs px-3 py-1.5 rounded-md border cursor-pointer', ctForm.type === tp ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-500/40 dark:bg-emerald-500/10 dark:text-emerald-400' : 'border-gray-200 text-muted-foreground dark:border-[rgba(255,255,255,0.08)]')}>
                        <input type="radio" checked={ctForm.type === tp} onChange={() => setCtForm({ ...ctForm, type: tp })} className="accent-emerald-500" />
                        {tp === 'http' ? 'HTTP API' : 'CLI 命令'}
                      </label>
                    ))}
                  </div>
                  <label className="flex items-center gap-1.5 text-xs text-muted-foreground cursor-pointer">
                    <input type="checkbox" checked={ctForm.enabled} onChange={e => setCtForm({ ...ctForm, enabled: e.target.checked })} className="accent-emerald-500" />
                    创建后立即启用
                  </label>
                </div>

                {/* HTTP 配置 */}
                {ctForm.type === 'http' ? (
                  <div className="space-y-2">
                    <div className="flex gap-2">
                      <select value={ctForm.http_method} onChange={e => setCtForm({ ...ctForm, http_method: e.target.value })} className="h-10 rounded-md border border-input bg-background px-2 text-sm">
                        {['GET', 'POST', 'PUT', 'DELETE', 'PATCH'].map(m => <option key={m} value={m}>{m}</option>)}
                      </select>
                      <Input placeholder="URL 模板, 支持 {param} 占位符 (如 https://api.example.com/hosts/{name})" value={ctForm.http_url} onChange={e => setCtForm({ ...ctForm, http_url: e.target.value })} className="flex-1" />
                      <Input type="number" min={1} max={120} value={ctForm.timeout_sec} onChange={e => setCtForm({ ...ctForm, timeout_sec: parseInt(e.target.value) || 30 })} className="w-20" title="超时(秒)" />
                    </div>
                    {/* Headers */}
                    <div className="space-y-1.5">
                      <p className="text-[11px] text-muted-foreground">请求头（可选）</p>
                      {ctForm.http_headers.map((h, i) => (
                        <div key={i} className="flex gap-2">
                          <Input placeholder="Header 名" value={h.k} onChange={e => { const arr = [...ctForm.http_headers]; arr[i] = { ...arr[i], k: e.target.value }; setCtForm({ ...ctForm, http_headers: arr }) }} className="w-48" />
                          <Input placeholder="值" value={h.v} onChange={e => { const arr = [...ctForm.http_headers]; arr[i] = { ...arr[i], v: e.target.value }; setCtForm({ ...ctForm, http_headers: arr }) }} className="flex-1" />
                          <Button variant="ghost" size="icon" className="h-10 w-8 shrink-0" onClick={() => setCtForm({ ...ctForm, http_headers: ctForm.http_headers.filter((_, idx) => idx !== i) })}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                        </div>
                      ))}
                      <Button variant="outline" size="sm" onClick={() => setCtForm({ ...ctForm, http_headers: [...ctForm.http_headers, { k: '', v: '' }] })}><Plus className="h-3.5 w-3.5 mr-1" />加请求头</Button>
                    </div>
                  </div>
                ) : (
                  <div className="space-y-2">
                    <Input placeholder="CLI 命令模板, 支持 {param} (如: df -h | grep {mount})" value={ctForm.cli_command} onChange={e => setCtForm({ ...ctForm, cli_command: e.target.value })} />
                    <div className={cn("flex items-start gap-2 text-[11px] p-2.5 rounded-lg", theme === 'dark' ? 'bg-red-500/5 border border-red-500/10' : 'bg-red-50 border border-red-200')}>
                      <AlertCircle className="h-3.5 w-3.5 text-red-500 shrink-0 mt-0.5" />
                      <span className="text-red-600 dark:text-red-400">CLI 工具在服务器本地以 shell 执行，存在命令注入风险。仅建议管理员在可信环境使用。</span>
                    </div>
                  </div>
                )}

                {/* 参数 Schema */}
                <div className="space-y-1.5">
                  <p className="text-[11px] text-muted-foreground">参数定义（供 LLM 识别调用参数）</p>
                  {ctForm.parameters.map((p, i) => (
                    <div key={i} className="flex gap-2 items-center">
                      <Input placeholder="参数名" value={p.name} onChange={e => { const arr = [...ctForm.parameters]; arr[i] = { ...arr[i], name: e.target.value }; setCtForm({ ...ctForm, parameters: arr }) }} className="w-36" />
                      <select value={p.type} onChange={e => { const arr = [...ctForm.parameters]; arr[i] = { ...arr[i], type: e.target.value }; setCtForm({ ...ctForm, parameters: arr }) }} className="h-10 rounded-md border border-input bg-background px-2 text-sm w-28">
                        <option value="string">string</option>
                        <option value="number">number</option>
                        <option value="boolean">boolean</option>
                      </select>
                      <Input placeholder="描述" value={p.description} onChange={e => { const arr = [...ctForm.parameters]; arr[i] = { ...arr[i], description: e.target.value }; setCtForm({ ...ctForm, parameters: arr }) }} className="flex-1" />
                      <label className="flex items-center gap-1 text-[11px] text-muted-foreground whitespace-nowrap cursor-pointer">
                        <input type="checkbox" checked={p.required} onChange={e => { const arr = [...ctForm.parameters]; arr[i] = { ...arr[i], required: e.target.checked }; setCtForm({ ...ctForm, parameters: arr }) }} className="accent-emerald-500" />必填
                      </label>
                      <Button variant="ghost" size="icon" className="h-10 w-8 shrink-0" onClick={() => setCtForm({ ...ctForm, parameters: ctForm.parameters.filter((_, idx) => idx !== i) })}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                    </div>
                  ))}
                  <Button variant="outline" size="sm" onClick={() => setCtForm({ ...ctForm, parameters: [...ctForm.parameters, { name: '', type: 'string', description: '', required: false }] })}><Plus className="h-3.5 w-3.5 mr-1" />加参数</Button>
                </div>

                <Button onClick={handleCreateCustomTool} disabled={ctSaving} className="w-full">
                  {ctSaving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Plus className="h-4 w-4 mr-1" />}注册工具
                </Button>
              </div>

              {/* 已注册列表 */}
              <div className="space-y-1.5">
                <p className="text-xs font-medium">已注册自定义工具 ({customTools.length})</p>
                {customTools.length === 0 ? (
                  <p className="text-xs text-muted-foreground p-3 text-center">暂无自定义工具</p>
                ) : (
                  customTools.map(t => (
                    <div key={t.id} className={cn("flex items-center justify-between p-3 rounded-lg", theme === 'dark' ? 'bg-white/[0.02] border border-white/[0.04]' : 'bg-gray-50 border border-gray-100')}>
                      <div className="min-w-0">
                        <div className="flex items-center gap-2">
                          <code className="text-sm font-medium text-emerald-600 dark:text-emerald-400">{t.name}</code>
                          <Badge variant={t.type === 'http' ? 'info' : 'warning'} className="text-[9px]">{t.type === 'http' ? 'HTTP' : 'CLI'}</Badge>
                          {!t.enabled && <Badge variant="secondary" className="text-[9px]">已禁用</Badge>}
                        </div>
                        {t.description && <p className="text-[11px] text-muted-foreground truncate mt-0.5">{t.description}</p>}
                        {t.type === 'http' ? (
                          <p className="text-[10px] text-muted-foreground truncate mt-0.5">{t.http_method} {t.http_url}</p>
                        ) : (
                          <p className="text-[10px] text-muted-foreground truncate mt-0.5">{t.cli_command}</p>
                        )}
                      </div>
                      <div className="flex items-center gap-2 shrink-0">
                        <button
                          onClick={() => handleToggleCustomTool(t)}
                          className={cn('relative w-9 h-5 rounded-full transition-colors', t.enabled ? 'bg-emerald-500' : 'bg-gray-300 dark:bg-gray-600')}
                          title={t.enabled ? '禁用' : '启用'}
                        >
                          <span className={cn('absolute top-0.5 h-4 w-4 rounded-full bg-white transition-all', t.enabled ? 'left-[18px]' : 'left-0.5')} />
                        </button>
                        <Button variant="ghost" size="sm" onClick={() => handleDeleteCustomTool(t.id)}><Trash2 className="h-3.5 w-3.5 text-red-400" /></Button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        {/* 审批策略 */}
        <TabsContent value="approval" className="flex-1 overflow-auto space-y-4">
          <Card>
            <CardHeader>
              <CardTitle className="text-base">敏感操作审批策略</CardTitle>
              <CardDescription>控制 AI 执行 script_run 等敏感操作时的确认方式（PRD 5.2.5）</CardDescription>
            </CardHeader>
            <CardContent className="space-y-5">
              {/* 作用范围: 全局 / 指定 Agent */}
              <div>
                <label className="text-sm font-medium mb-2 block">作用范围</label>
                <div className="flex gap-2">
                  <button
                    onClick={() => { setApprovalScope('global'); loadApprovalPolicy() }}
                    className={cn(
                      'flex-1 text-xs px-3 py-2 rounded-md border transition-colors text-left',
                      approvalScope === 'global'
                        ? 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400'
                        : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                    )}
                  >
                    <span className="font-medium">🌐 全局默认</span>
                    <span className="block text-[10px] opacity-60 mt-0.5">所有 Agent 未单独配置时生效</span>
                  </button>
                  <button
                    onClick={() => { setApprovalScope('agent'); if (approvalAgentId) loadApprovalPolicy(approvalAgentId) }}
                    className={cn(
                      'flex-1 text-xs px-3 py-2 rounded-md border transition-colors text-left',
                      approvalScope === 'agent'
                        ? 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:bg-emerald-500/10 dark:border-emerald-500/30 dark:text-emerald-400'
                        : 'border-gray-200 text-muted-foreground hover:border-gray-300 dark:border-gray-700 dark:hover:border-gray-600',
                    )}
                  >
                    <span className="font-medium">🤖 按 Agent 覆盖</span>
                    <span className="block text-[10px] opacity-60 mt-0.5">为指定智能体单独配置策略</span>
                  </button>
                </div>
                {approvalScope === 'agent' && (
                  <select
                    className="flex h-9 w-full mt-2 rounded-md border border-input bg-background px-3 py-1.5 text-sm"
                    value={approvalAgentId}
                    onChange={e => { setApprovalAgentId(e.target.value); if (e.target.value) loadApprovalPolicy(e.target.value) }}
                  >
                    <option value="">选择 Agent…</option>
                    {agents.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
                  </select>
                )}
              </div>

              {/* 模式选择 */}
              <div>
                <label className="text-sm font-medium mb-2 block">策略模式</label>
                <div className="space-y-2">
                  {APPROVAL_MODES.map(m => (
                    <label
                      key={m.value}
                      className={cn(
                        'flex items-start gap-3 p-3 rounded-lg border cursor-pointer transition-colors',
                        approvalMode === m.value
                          ? 'border-emerald-300 bg-emerald-50 dark:border-emerald-500/40 dark:bg-emerald-500/10'
                          : 'border-gray-200 dark:border-[rgba(255,255,255,0.06)] hover:border-gray-300',
                      )}
                    >
                      <input type="radio" name="approval-mode" value={m.value}
                        checked={approvalMode === m.value}
                        onChange={() => setApprovalMode(m.value)}
                        className="mt-0.5" />
                      <div>
                        <div className="text-sm font-medium">{m.label}</div>
                        <div className="text-xs text-muted-foreground mt-0.5">{m.desc}</div>
                      </div>
                    </label>
                  ))}
                </div>
              </div>

              {/* 敏感工具名单 */}
              <div>
                <label className="text-sm font-medium mb-2 block">敏感工具名单（sensitive_only 模式生效）</label>
                <div className="flex gap-2 flex-wrap">
                  {DEFAULT_SENSITIVE_TOOLS.concat(sensitiveTools.filter(t => !DEFAULT_SENSITIVE_TOOLS.includes(t)))
                    .filter((v, i, arr) => arr.indexOf(v) === i)
                    .map(tool => (
                      <label key={tool} className="flex items-center gap-1.5 text-xs px-2 py-1 rounded border border-gray-200 dark:border-[rgba(255,255,255,0.08)] cursor-pointer">
                        <input type="checkbox"
                          checked={sensitiveTools.includes(tool)}
                          onChange={e => {
                            if (e.target.checked) setSensitiveTools(prev => [...prev, tool])
                            else setSensitiveTools(prev => prev.filter(t => t !== tool))
                          }} />
                        <code className="font-mono">{tool}</code>
                      </label>
                    ))}
                </div>
                <p className="text-[11px] text-muted-foreground mt-1.5">只读工具（cmdb_query / knowledge_search / http_get）始终直接执行，不受此配置影响。</p>
              </div>

              {/* 超时 */}
              <div className="max-w-xs">
                <label className="text-sm font-medium mb-1 block">确认超时（秒）</label>
                <input type="number" min={10} max={600} value={askTimeout}
                  onChange={e => setAskTimeout(parseInt(e.target.value) || 60)}
                  className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" />
                <p className="text-[11px] text-muted-foreground mt-1.5">超时未响应视为拒绝（fail-closed），AI 会停止该操作并报告现状。</p>
              </div>

              <div className="flex items-center gap-3 pt-1">
                <Button onClick={handleSaveApprovalPolicy} disabled={policySaving}>
                  {policySaving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Save className="h-4 w-4 mr-1" />}保存策略
                </Button>
                <span className="text-xs text-muted-foreground">保存后即时生效，无需重启</span>
              </div>

              <div className={cn("flex items-start gap-2 text-xs p-3 rounded-lg", theme === 'dark' ? 'bg-blue-500/5 border border-blue-500/10' : 'bg-blue-50 border border-blue-200')}>
                <CheckCircle2 className="h-4 w-4 text-blue-500 shrink-0 mt-0.5" />
                <div>
                  <p className="font-medium text-blue-600 dark:text-blue-400">执行链路说明</p>
                  <p className="text-muted-foreground mt-0.5">AI 请求执行脚本 → 不在免审批白名单时触发本策略 → 需要确认时聊天界面弹出确认框 → 批准后执行 / 拒绝或超时则停止并向 AI 说明。未获交互式确认的脚本仍会创建 ITSM 变更单兜底。</p>
                </div>
              </div>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
