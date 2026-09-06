
import { useEffect, useState, useRef, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button, Input, Select, Tag, Space, Tabs, Popconfirm, Switch, Modal, message } from 'antd'
import { ReloadOutlined, PlusOutlined, DeleteOutlined, EditOutlined, ApiOutlined, SettingOutlined, SaveOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { Bot, Plus, Trash2, Save, Wrench, Brain, Settings2, Terminal, Wand2, AlertCircle, CheckCircle2, Loader2, ChevronRight, ShieldCheck, Database, Download, RefreshCw, Sparkles, Copy, Pencil, Star, Search, Package, Eye, Rocket } from 'lucide-react'
import RoleTemplatesPanel from '@/components/ai/role-templates-panel'

import { cn, apiFetch, apiFetchRaw, apiList } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'
import { useAIStore, type ApprovalMode } from '@/stores/ai'
import { getMessage } from '@/lib/message'
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

// L3-1 微调任务项(数据集 -> 微调 -> 产物注册模型池)

const EMPTY_SKILL: SkillFormItem = { name: '', description: '', instructions: '' }

// —— 提示词库 ——
// 提示词 = 可复用的一段「文本」(人设/约束/输出格式), 支持 {{变量}} 占位, 复制即用;
// 技能包 = 可移植的「运行单元」(Agent 完整配置 + 依赖工具), 安装即上岗。两者边界见页面底部说明卡片。
interface PromptTemplate {
  id: string
  name: string
  scene?: string
  description?: string
  content: string
  variables?: string[]
  builtin: boolean
  is_default: boolean
  usage: number
  created_at: string
  updated_at: string
}

interface PromptForm {
  id: string
  name: string
  scene: string
  description: string
  content: string
  is_default: boolean
}

const EMPTY_PROMPT: PromptForm = { id: '', name: '', scene: '通用', description: '', content: '', is_default: false }

// 预置场景, 与后端 seed 口径一致; 用户自建的新场景会自动并入筛选条
const PROMPT_SCENES = ['通用', '故障排查', '变更评审', '告警降噪', '知识沉淀', '容量规划']

// 解析 {{变量}} 占位(去重保序), 与后端 promptVarRe 同口径: 允许中英文/数字/下划线
function parsePromptVars(content: string): string[] {
  const re = /\{\{\s*([\u4e00-\u9fa5A-Za-z0-9_]+)\s*\}\}/g
  const out: string[] = []
  const seen = new Set<string>()
  let m: RegExpExecArray | null
  while ((m = re.exec(content)) !== null) {
    if (seen.has(m[1])) continue
    seen.add(m[1])
    out.push(m[1])
  }
  return out
}

// 本地渲染(仅用于编辑器内预览, 不计引用次数); 真正插入 Agent 时走后端 render 端点
function renderPromptLocal(content: string, vars: Record<string, string>): string {
  return content.replace(/\{\{\s*([\u4e00-\u9fa5A-Za-z0-9_]+)\s*\}\}/g, (raw, name: string) => vars[name] || raw)
}

/** 提示词正文需要更宽的编辑区，基础 Dialog 固定 max-w-lg，这里按同一视觉规范派生宽版弹层 */
function WideDialog({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  return (
    <Modal open={open} onCancel={onClose} title={title} width={768} footer={null}>
      {children}
    </Modal>
  )
}

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
            <Button type="text" size="middle" className="h-8 w-8 shrink-0 text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => onChange(value.filter((_, idx) => idx !== i))}>
              <Trash2 className="h-3.5 w-3.5" />
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
      <Button size="middle" onClick={() => onChange([...value, { ...EMPTY_SKILL }])}>
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
  const [modelPool, setModelPool] = useState<{ name: string; model: string; base_url: string; weight: number; healthy: boolean }[]>([])
  // 进化闭环概览
  const [evoStats, setEvoStats] = useState<{ feedback_up: number; feedback_down: number; memories: number; disposal_cases: number } | null>(null)
  // 主动进化: 全局策略 + 数据源 + 手动执行
  const [evoSources, setEvoSources] = useState<{ id: string; name: string; type: string; source: string; enabled: boolean; interval_hours: number; target_category: string; min_score: number; last_run_at: string | null; created_at: string }[]>([])
  const [evoForm, setEvoForm] = useState({ name: '', type: 'rss', source: '', enabled: true, interval_hours: 0, target_category: '', min_score: 0 })
  const [evoFetching, setEvoFetching] = useState(false)
  const [evoResults, setEvoResults] = useState<{ name: string; type: string; source: string; status: string; message: string }[] | null>(null)
  // 保存/恢复页面滚动位置，防止因状态更新导致的页面位置跳变
  const scrollRef = useRef(0)
  useEffect(() => {
    scrollRef.current = window.scrollY
    return () => {
      window.scrollTo(0, scrollRef.current)
    }
  }, [])

  // 在 saving 状态变化或关键 API 请求前后保持滚位不变
  useEffect(() => {
    const handleScroll = () => {
      scrollRef.current = window.scrollY
    }
    window.addEventListener('scroll', handleScroll, { passive: true })
    return () => window.removeEventListener('scroll', handleScroll)
  }, [])

  const [evoPolicy, setEvoPolicy] = useState<{ enabled: boolean; interval_hours: number; target_category: string; min_score: number; blacklist: string[]; updated_at: string } | null>(null)
  const [policyForm, setPolicyForm] = useState({ enabled: true, interval_hours: '6', target_category: '知识获取', min_score: '0.5', blacklist: '' })
  const [editingSrc, setEditingSrc] = useState<string | null>(null)
  const [srcDraft, setSrcDraft] = useState({ interval_hours: '', target_category: '', min_score: '', enabled: true })
  const [editing, setEditing] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [editForm, setEditForm] = useState<{ name: string; model_name: string; system_prompt: string; knowledge_categories: string[]; tools: string[]; skills: SkillFormItem[] }>({ name: '', model_name: '', system_prompt: '', knowledge_categories: [], tools: [], skills: [] })
  const [tools, setTools] = useState<any[]>([])
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

  useEffect(() => { fetchAgents(); loadTools(); loadApprovalPolicy(); loadKbCategories(); loadModelPool(); loadPrompts(); loadEvolutionStats(); loadEvoSources(); loadEvoPolicy() }, [])
  const loadModelPool = async () => {
    try {
      const d = await apiFetch<any>('/ai/models')
      setModelPool(Array.isArray(d) ? d : [])
    } catch {}
  }

  const [pendingEvoId, setPendingEvoId] = useState<string | null>(null)
  const [pendingPrompt, setPendingPrompt] = useState<PromptTemplate | null>(null)
  const [pendingDefaultPrompt, setPendingDefaultPrompt] = useState<PromptTemplate | null>(null)
  const [pendingAgentId, setPendingAgentId] = useState<string | null>(null)

  // —— 提示词库: 列表 / 编辑 / 默认提示词热更新 / 插入 Agent ——
  const [prompts, setPrompts] = useState<PromptTemplate[]>([])
  const [promptLoading, setPromptLoading] = useState(false)
  const [promptScene, setPromptScene] = useState('')
  const [promptKeyword, setPromptKeyword] = useState('')
  const [promptDialogOpen, setPromptDialogOpen] = useState(false)
  const [promptForm, setPromptForm] = useState<PromptForm>({ ...EMPTY_PROMPT })
  const [promptSaving, setPromptSaving] = useState(false)
  const [promptVars, setPromptVars] = useState<Record<string, string>>({})
  const [promptPreview, setPromptPreview] = useState<string | null>(null)
  // 默认系统提示词单独占一张置顶卡片, 草稿态与列表解耦, 保存即热生效
  const [defaultDraft, setDefaultDraft] = useState('')

  const defaultPrompt = prompts.find(p => p.is_default) || null
  const defaultDirty = !!defaultPrompt && defaultDraft.trim() !== defaultPrompt.content.trim()
  // 场景筛选项 = 预置场景 ∪ 用户自建场景
  const promptScenes = Array.from(new Set([...PROMPT_SCENES, ...prompts.map(p => p.scene || '').filter(Boolean)]))
  const visiblePrompts = prompts.filter(p => {
    if (p.is_default) return false // 默认提示词已在置顶卡片单独展示
    if (promptScene && p.scene !== promptScene) return false
    if (!promptKeyword.trim()) return true
    const k = promptKeyword.trim().toLowerCase()
    return [p.name, p.description, p.scene, p.content].some(x => (x || '').toLowerCase().includes(k))
  })

  const loadEvolutionStats = async () => {
    try {
      const d = await apiFetch<any>('/ai/evolution/stats')
      setEvoStats(d)
    } catch {}
  }

  const loadEvoSources = async () => {
    try {
      const d = await apiFetch<any>('/ai/evolution/sources')
      if (Array.isArray(d)) setEvoSources(d)
    } catch {}
  }

  const loadEvoPolicy = async () => {
    try {
      const p = await apiFetch<any>('/ai/evolution/policy')
      setEvoPolicy(p)
      setPolicyForm({
        enabled: p.enabled,
        interval_hours: String(p.interval_hours || 6),
        target_category: p.target_category || '知识获取',
        min_score: String(p.min_score ?? 0.5),
        blacklist: (p.blacklist || []).join(', '),
      })
    } catch {}
  }

  const saveEvoPolicy = async () => {
    try {
      await apiFetch('/ai/evolution/policy', {
        method: 'PUT',
        body: JSON.stringify({
          enabled: policyForm.enabled,
          interval_hours: parseInt(policyForm.interval_hours) || 6,
          target_category: policyForm.target_category.trim(),
          min_score: parseFloat(policyForm.min_score) || 0,
          blacklist: policyForm.blacklist.split(/[,，]/).map(s => s.trim()).filter(Boolean),
        }),
      })
      getMessage().success('全局策略已保存，即时生效')
      loadEvoPolicy()
    } catch (e: any) { getMessage().error('保存失败: ' + e.message) }
  }

  const startEditSource = (s: typeof evoSources[number]) => {
    setEditingSrc(s.id)
    setSrcDraft({
      interval_hours: String(s.interval_hours || 0),
      target_category: s.target_category || '',
      min_score: String(s.min_score || 0),
      enabled: s.enabled,
    })
  }

  const saveSourceEdit = async (id: string) => {
    try {
      await apiFetch(`/ai/evolution/sources/${id}`, {
        method: 'PUT',
        body: JSON.stringify({
          enabled: srcDraft.enabled,
          interval_hours: parseInt(srcDraft.interval_hours) || 0,
          target_category: srcDraft.target_category.trim(),
          min_score: parseFloat(srcDraft.min_score) || 0,
        }),
      })
      getMessage().success('已更新')
      setEditingSrc(null)
      loadEvoSources()
    } catch (e: any) { getMessage().error('更新失败: ' + e.message) }
  }

  const saveEvoSource = async () => {
    if (!evoForm.name.trim() || !evoForm.source.trim()) { getMessage().error('请填写名称与源地址'); return }
    try {
      await apiFetch('/ai/evolution/sources', {
        method: 'POST',
        body: JSON.stringify(evoForm),
      })
      getMessage().success('数据源已添加')
      setEvoForm({ name: '', type: 'rss', source: '', enabled: true, interval_hours: 0, target_category: '', min_score: 0 })
      loadEvoSources()
    } catch (e: any) { getMessage().error('添加失败: ' + e.message) }
  }

  const deleteEvoSource = async () => {
    const id = pendingEvoId
    if (!id) return
    try {
      await apiFetch(`/ai/evolution/sources/${id}`, { method: 'DELETE' })
      getMessage().success('已删除')
      loadEvoSources()
    } catch (e: any) { getMessage().error('删除失败: ' + e.message) }
    setPendingEvoId(null)
  }

  const runEvoFetch = async () => {
    setEvoFetching(true)
    setEvoResults(null)
    try {
      const d = await apiFetch<any>('/ai/evolution/fetch', { method: 'POST' })
      setEvoResults(d.results || [])
      getMessage().success('主动进化执行完成')
    } catch (e: any) { getMessage().error('执行失败: ' + e.message) }
    setEvoFetching(false)
  }

  const loadPrompts = async () => {
    setPromptLoading(true)
    try {
      const list = await apiList<PromptTemplate>('/ai/prompts')
      setPrompts(list)
      const def = list.find(p => p.is_default)
      setDefaultDraft(def?.content || '')
    } catch (e: any) {
      getMessage().error(`加载提示词失败: ${e.message}`)
    } finally { setPromptLoading(false) }
  }

  const openPromptDialog = (p?: PromptTemplate, asCopy = false) => {
    if (!p) {
      setPromptForm({ ...EMPTY_PROMPT })
    } else if (asCopy) {
      // 另存副本: 丢弃 id 与默认标记, 预置提示词由此派生可自由改写的自建版本
      setPromptForm({ id: '', name: `${p.name} 副本`, scene: p.scene || '通用', description: p.description || '', content: p.content, is_default: false })
    } else {
      setPromptForm({ id: p.id, name: p.name, scene: p.scene || '通用', description: p.description || '', content: p.content, is_default: p.is_default })
    }
    setPromptVars({})
    setPromptPreview(null)
    setPromptDialogOpen(true)
  }

  const submitPrompt = async () => {
    if (!promptForm.name.trim()) return getMessage().error('请填写提示词名称')
    if (!promptForm.content.trim()) return getMessage().error('请填写提示词正文')
    setPromptSaving(true)
    try {
      const body = JSON.stringify({
        name: promptForm.name.trim(),
        scene: promptForm.scene.trim(),
        description: promptForm.description.trim(),
        content: promptForm.content,
        is_default: promptForm.is_default,
      })
      if (promptForm.id) await apiFetch(`/ai/prompts/${promptForm.id}`, { method: 'PUT', body })
      else await apiFetch('/ai/prompts', { method: 'POST', body })
      getMessage().success(promptForm.is_default ? '已保存并设为默认，立即热生效' : '提示词已保存')
      setPromptDialogOpen(false)
      loadPrompts()
    } catch (e: any) { getMessage().error(`保存失败: ${e.message}`) } finally { setPromptSaving(false) }
  }

  const saveDefaultPrompt = async () => {
    if (!defaultPrompt) return
    if (!defaultDraft.trim()) return getMessage().error('默认系统提示词不能为空')
    setPromptSaving(true)
    try {
      await apiFetch(`/ai/prompts/${defaultPrompt.id}`, {
        method: 'PUT',
        body: JSON.stringify({
          name: defaultPrompt.name, scene: defaultPrompt.scene || '通用',
          description: defaultPrompt.description || '', content: defaultDraft, is_default: true,
        }),
      })
      getMessage().success('默认系统提示词已保存，立即热生效（无需重启 Core）')
      loadPrompts()
    } catch (e: any) { getMessage().error(`保存失败: ${e.message}`) } finally { setPromptSaving(false) }
  }

  const setAsDefault = async () => {
    const p = pendingDefaultPrompt
    if (!p) return
    try {
      await apiFetch(`/ai/prompts/${p.id}`, {
        method: 'PUT',
        body: JSON.stringify({ name: p.name, scene: p.scene || '', description: p.description || '', content: p.content, is_default: true }),
      })
      getMessage().success('已设为默认，立即热生效')
      loadPrompts()
    } catch (e: any) { getMessage().error(`操作失败: ${e.message}`) }
    setPendingDefaultPrompt(null)
  }

  const deletePrompt = async () => {
    const p = pendingPrompt
    if (!p) return
    try {
      await apiFetch(`/ai/prompts/${p.id}`, { method: 'DELETE' })
      getMessage().success('提示词已删除')
      loadPrompts()
    } catch (e: any) { getMessage().error(`删除失败: ${e.message}`) }
    setPendingPrompt(null)
  }

  const copyPromptContent = async (p: PromptTemplate) => {
    try {
      await navigator.clipboard.writeText(p.content)
      getMessage().success('提示词正文已复制到剪贴板')
    } catch { getMessage().error('复制失败，请手动选择文本') }
  }

  // 插入 Agent 系统提示词: 走后端 render 端点(计入引用次数), 未填变量的占位原样保留待人工补全
  const insertPromptInto = async (target: 'create' | 'edit', id: string) => {
    if (!id) return
    try {
      const r = await apiFetch<{ content: string }>(`/ai/prompts/${id}/render`, { method: 'POST', body: JSON.stringify({ variables: {} }) })
      const text = r?.content || ''
      if (!text) return getMessage().error('提示词内容为空')
      const merge = (prev: string) => (prev.trim() ? `${prev.trimEnd()}\n\n${text}` : text)
      if (target === 'create') setForm(f => ({ ...f, system_prompt: merge(f.system_prompt) }))
      else setEditForm(f => ({ ...f, system_prompt: merge(f.system_prompt) }))
      getMessage().success('已插入提示词，{{变量}} 占位请按需替换为实际值')
      loadPrompts()
    } catch (e: any) { getMessage().error(`插入失败: ${e.message}`) }
  }

  const loadKbCategories = async () => {
    try {
      const d = await apiFetch<any>('/knowledge?limit=500')
      const list = Array.isArray(d) ? d : d?.items || []
      const cats = Array.from(new Set(list.map((a: any) => a.category).filter(Boolean) as string[]))
      if (cats.length > 0) setKbCategories(cats)
    } catch {}
  }
  
  const loadTools = async () => {
    try {
      const d = await apiFetch<any>('/ai/tools')
      setTools(Array.isArray(d) ? d : d?.tools || [])
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
    if (!form.name.trim()) return getMessage().error('请输入 Agent 名称')
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

  const handleDeleteAgent = async () => {
    const id = pendingAgentId
    if (!id) return
    await apiFetch(`/ai/agents/${id}`, { method: 'DELETE' })
    getMessage().success('Agent 已删除')
    fetchAgents()
    setPendingAgentId(null)
  }

  const handleSaveEdit = async (id: string) => {
    setSaving(true)
    await apiFetch(`/ai/agents/${id}`, {
      method: 'PUT',
      body: JSON.stringify({
        name: editForm.name,
        model_name: editForm.model_name,
        system_prompt: editForm.system_prompt,
        knowledge_categories: editForm.knowledge_categories,
        tools: editForm.tools,
        skills: editForm.skills.filter(s => s.name.trim()),
      }),
    })
    getMessage().success('Agent 已更新')
    setSaving(false); setEditing(null); fetchAgents()
  }

  // —— Tab 内容组件(访问父级状态/函数) ——
  function AgentsContent() {
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader><CardTitle className="text-base">创建智能体</CardTitle><CardDescription>定义 Agent 的名称、模型、系统提示词和知识库绑定</CardDescription></CardHeader>
          <CardContent className="space-y-3">
            <Input placeholder="Agent 名称 (如: 运维助手)" value={form.name} onChange={e => setForm({...form, name: e.target.value})} />
            <Input list="model-pool" placeholder="模型名 (如: deepseek-chat / provider 名, 留空使用默认)" value={form.model_name} onChange={e => setForm({...form, model_name: e.target.value})} />
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label className="text-xs font-medium">系统提示词 <span className="text-muted-foreground font-normal">(留空 = 使用默认系统提示词)</span></label>
                <select
                  value=""
                  onChange={e => { insertPromptInto('create', e.target.value); e.currentTarget.value = '' }}
                  className="text-[11px] h-6 rounded-md border border-input bg-background px-1.5 text-muted-foreground focus-visible:outline-none"
                >
                  <option value="">＋ 从提示词库插入…</option>
                  {prompts.map(p => (
                    <option key={p.id} value={p.id}>{p.scene ? `[${p.scene}] ` : ''}{p.name}</option>
                  ))}
                </select>
              </div>
              <textarea placeholder="系统提示词, 定义 Agent 的行为和角色..." value={form.system_prompt} onChange={e => setForm({...form, system_prompt: e.target.value})} rows={4} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm ring-offset-background placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 resize-none" />
            </div>
            <div>
              <label className="text-xs font-medium mb-1.5 block">
                绑定知识库分类 <span className="text-muted-foreground font-normal">(留空 = 检索全部知识库)</span>
              </label>
              <div className="flex gap-2 flex-wrap">
                {kbCategories.map(cat => (
                  <label key={cat} className={cn(
                    'flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border cursor-pointer transition-colors',
                    form.knowledge_categories.includes(cat)
                      ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
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
                      className="accent-blue-500" />
                    {cat}
                  </label>
                ))}
              </div>
              <p className="text-[11px] text-muted-foreground mt-1.5">绑定后，该 Agent 的 RAG 检索与 knowledge_search 工具仅在这些分类内查询（PRD 5.2.6）。</p>
            </div>
            <div>
              <div className="flex items-center justify-between mb-1.5">
                <label className="text-xs font-medium">
                  可用 MCP 工具 <span className="text-muted-foreground font-normal">(留空 = 全部)</span>
                </label>
                <Link to="/ai/mcp" className="text-[11px] text-blue-600 dark:text-blue-400 hover:underline flex items-center gap-1">
                  <Wrench className="h-3 w-3" />管理工具
                </Link>
              </div>
              {selectableTools.length === 0 ? (
                <p className="text-xs text-muted-foreground">暂无可用工具，先到「MCP 工具」页注册。</p>
              ) : (
                <div className="flex gap-2 flex-wrap">
                  {selectableTools.map(t => (
                    <label key={t.name} className={cn(
                      'flex items-center gap-1.5 text-xs px-2.5 py-1.5 rounded-md border cursor-pointer transition-colors',
                      form.tools.includes(t.name)
                        ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
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
                        className="accent-blue-500" />
                      <code className="font-mono">{t.name}</code>
                    </label>
                  ))}
                </div>
              )}
              <p className="text-[11px] text-muted-foreground mt-1.5">未勾选的工具对模型不可见（不可调用）；script_run 等执行类工具仍受审批策略保护。</p>
            </div>
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
                      <div>
                        <div className="flex items-center justify-between mb-1.5">
                          <label className="text-xs font-medium">系统提示词</label>
                          <select
                            value=""
                            onChange={e => { insertPromptInto('edit', e.target.value); e.currentTarget.value = '' }}
                            className="text-[11px] h-6 rounded-md border border-input bg-background px-1.5 text-muted-foreground focus-visible:outline-none"
                          >
                            <option value="">＋ 从提示词库插入…</option>
                            {prompts.map(p => (
                              <option key={p.id} value={p.id}>{p.scene ? `[${p.scene}] ` : ''}{p.name}</option>
                            ))}
                          </select>
                        </div>
                        <textarea rows={3} value={editForm.system_prompt} onChange={e => setEditForm({...editForm, system_prompt: e.target.value})} className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none" placeholder="系统提示词" />
                      </div>
                      <div>
                        <label className="text-xs font-medium mb-1.5 block">绑定知识库分类 <span className="text-muted-foreground font-normal">(留空 = 全部)</span></label>
                        <div className="flex gap-2 flex-wrap">
                          {kbCategories.map(cat => (
                            <label key={cat} className={cn(
                              'flex items-center gap-1.5 text-[11px] px-2 py-1 rounded-md border cursor-pointer transition-colors',
                              editForm.knowledge_categories.includes(cat)
                                ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
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
                                className="accent-blue-500" />
                              {cat}
                            </label>
                          ))}
                        </div>
                      </div>
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
                                  ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
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
                                  className="accent-blue-500" />
                                <code className="font-mono">{t.name}</code>
                              </label>
                            ))}
                          </div>
                        )}
                      </div>
                      <div>
                        <label className="text-xs font-medium mb-1.5 block">定义 Skills（可复用能力）</label>
                        <SkillsEditor value={editForm.skills} onChange={skills => setEditForm(f => ({ ...f, skills }))} />
                      </div>
                      <div className="flex gap-2">
                        <Button size="middle" onClick={() => handleSaveEdit(a.id)} disabled={saving}><Save className="h-3.5 w-3.5 mr-1" />保存</Button>
                        <Button size="middle" onClick={() => setEditing(null)}>取消</Button>
                      </div>
                    </div>
                  ) : (
                    <div className="flex items-center justify-between">
                      <div className="flex-1 min-w-0">
                        <div className="flex items-center gap-2">
                          <Bot className="h-4 w-4 text-blue-500 shrink-0" />
                          <span className="font-medium text-sm truncate">{a.name}</span>
                          <Tag color={a.status === 'active' ? 'default' : 'default'} className="text-[10px]">{a.status}</Tag>
                        </div>
                        {a.system_prompt && <p className="text-xs text-muted-foreground mt-1 truncate">{a.system_prompt.slice(0, 80)}</p>}
                        <div className="flex items-center gap-2 mt-0.5 flex-wrap">
                          {a.model_name && <p className="text-[10px] text-muted-foreground">模型: {a.model_name}</p>}
                          {a.knowledge_categories?.length > 0 && (
                            <div className="flex gap-1 flex-wrap">
                              {a.knowledge_categories.map((c: string) => (
                                <Tag key={c} color="blue" className="text-[9px]">📚 {c}</Tag>
                              ))}
                            </div>
                          )}
                          {a.tools?.length > 0 && (
                            <Tag color="green" className="text-[9px]">🔧 {a.tools.length} 工具</Tag>
                          )}
                          {a.skills?.length > 0 && (
                            <Tag color="purple" className="text-[9px]">✨ {a.skills.length} 技能</Tag>
                          )}
                        </div>
                      </div>
                      <div className="flex gap-1 shrink-0">
                        <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => { setEditing(a.id); setEditForm({ name: a.name, model_name: a.model_name || '', system_prompt: a.system_prompt || '', knowledge_categories: a.knowledge_categories || [], tools: a.tools || [], skills: (a.skills || []).map((s: any) => ({ name: s.name || '', description: s.description || '', instructions: s.instructions || '' })) }) }}><Save className="h-3.5 w-3.5" /></Button>
                        <Button type="text" size="middle" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => setPendingAgentId(a.id)}><Trash2 className="h-3.5 w-3.5" /></Button>
                      </div>
                    </div>
                  )}
                </CardContent>
              </Card>
            ))
          )}
        </div>
      </div>
    )
  }

  function PromptsContent() {
    return (
      <div className="space-y-4">
        <Card className="border-blue-200 dark:border-blue-500/25">
          <CardHeader className="pb-2">
            <div className="flex items-start justify-between gap-3">
              <div>
                <CardTitle className="text-base flex items-center gap-2">
                  <Star className="h-4 w-4 text-blue-500 fill-blue-500" />
                  默认系统提示词
                </CardTitle>
                <CardDescription>所有未单独配置 system_prompt 的 Agent 与对话的兜底人设，保存后立即热生效，无需重启 Core</CardDescription>
              </div>
              <Button size="middle" onClick={saveDefaultPrompt} disabled={promptSaving || !defaultDirty || !defaultPrompt}>
                {promptSaving ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <Save className="h-3.5 w-3.5 mr-1" />}
                {defaultDirty ? '保存并生效' : '已是最新'}
              </Button>
            </div>
          </CardHeader>
          <CardContent className="space-y-2">
            {promptLoading && prompts.length === 0 ? (
              <div className="h-28 flex items-center justify-center text-xs text-muted-foreground">
                <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />加载提示词库…
              </div>
            ) : !defaultPrompt ? (
              <p className="text-xs text-muted-foreground py-6 text-center">未找到默认提示词，可在下方任意提示词上点击「设为默认」。</p>
            ) : (
              <>
                <textarea
                  value={defaultDraft}
                  onChange={e => setDefaultDraft(e.target.value)}
                  rows={6}
                  className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono leading-relaxed ring-offset-background resize-y focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40"
                />
                <div className="flex items-center justify-between text-[11px] text-muted-foreground">
                  <span>{defaultDraft.length} 字符 · 被引用 {defaultPrompt.usage} 次</span>
                  {defaultDirty && <span className="text-amber-600 dark:text-amber-400">有未保存的修改</span>}
                </div>
              </>
            )}
          </CardContent>
        </Card>

        <div className="flex items-center gap-2 flex-wrap">
          <div className="relative flex-1 min-w-[200px]">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
            <Input
              placeholder="搜索提示词名称、描述或正文…"
              value={promptKeyword}
              onChange={e => setPromptKeyword(e.target.value)}
              className="pl-8 h-9"
            />
          </div>
          <Button type="default" size="middle" onClick={loadPrompts} disabled={promptLoading}>
            <RefreshCw className={cn('h-3.5 w-3.5 mr-1', promptLoading && 'animate-spin')} />刷新
          </Button>
          <Button size="middle" onClick={() => openPromptDialog()}>
            <Plus className="h-3.5 w-3.5 mr-1" />新建提示词
          </Button>
        </div>

        <div className="flex gap-1.5 flex-wrap">
          <button
            onClick={() => setPromptScene('')}
            className={cn(
              'text-xs px-2.5 py-1 rounded-md border transition-colors',
              promptScene === ''
                ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
                : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
            )}
          >全部 ({prompts.filter(p => !p.is_default).length})</button>
          {promptScenes.map(s => {
            const n = prompts.filter(p => !p.is_default && p.scene === s).length
            return (
              <button
                key={s}
                onClick={() => setPromptScene(promptScene === s ? '' : s)}
                className={cn(
                  'text-xs px-2.5 py-1 rounded-md border transition-colors',
                  promptScene === s
                    ? 'border-blue-300 bg-blue-50 text-blue-700 dark:border-blue-500/40 dark:bg-blue-500/10 dark:text-blue-400'
                    : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)] text-muted-foreground hover:border-gray-300',
                )}
              >{s} ({n})</button>
            )
          })}
        </div>

        {visiblePrompts.length === 0 ? (
          <Card><CardContent className="py-10 text-center text-xs text-muted-foreground">
            {promptKeyword || promptScene ? '没有匹配的提示词，换个关键字或场景试试。' : '提示词库为空，点击右上角「新建提示词」添加。'}
          </CardContent></Card>
        ) : (
          <div className="grid gap-3 md:grid-cols-2">
            {visiblePrompts.map(p => {
              const vars = p.variables || parsePromptVars(p.content)
              return (
                <Card key={p.id} className="transition-all hover:shadow-md hover:-translate-y-0.5">
                  <CardContent className="p-4 space-y-2">
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <div className="flex items-center gap-1.5 flex-wrap">
                          <Wand2 className="h-3.5 w-3.5 text-blue-500 shrink-0" />
                          <span className="font-medium text-sm truncate">{p.name}</span>
                          {p.scene && <Tag className="text-[9px]">{p.scene}</Tag>}
                          {p.builtin && <Tag className="text-[9px]">预置</Tag>}
                        </div>
                        {p.description && <p className="text-[11px] text-muted-foreground mt-1 line-clamp-2">{p.description}</p>}
                      </div>
                      <span className="text-[10px] text-muted-foreground shrink-0">引用 {p.usage}</span>
                    </div>

                    <pre className="text-[11px] font-mono text-muted-foreground bg-gray-50 dark:bg-[rgba(255,255,255,0.03)] rounded-md p-2 max-h-20 overflow-hidden whitespace-pre-wrap break-all">
                      {p.content.slice(0, 220)}{p.content.length > 220 ? '…' : ''}
                    </pre>

                    {vars.length > 0 && (
                      <div className="flex gap-1 flex-wrap">
                        {vars.map(v => (
                          <code key={v} className="text-[10px] px-1.5 py-0.5 rounded bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-400 font-mono">{'{{'}{v}{'}}'}</code>
                        ))}
                      </div>
                    )}

                    <div className="flex items-center gap-1 pt-1">
                      <Button type="text" size="middle" className="h-7 px-2 text-xs" onClick={() => openPromptDialog(p)}>
                        <Pencil className="h-3 w-3" />
                      </Button>
                      <Button type="text" size="middle" className="h-7 px-2 text-xs" onClick={() => openPromptDialog(p, true)}>
                        <Copy className="h-3 w-3" />
                      </Button>
                      <Button type="text" size="middle" className="h-7 px-2 text-xs" onClick={() => copyPromptContent(p)}>
                        <Copy className="h-3 w-3" />
                      </Button>
                      <Button type="text" size="middle" className="h-7 px-2 text-xs" title="设为默认系统提示词" onClick={() => setPendingDefaultPrompt(p)}>
                        <Star className="h-3 w-3" />
                      </Button>
                      <Button
                        type="text" size="middle" className="h-7 px-2"
                        title={p.builtin ? '预置提示词不可删除，可另存副本后修改' : '删除'}
                        disabled={p.builtin}
                        onClick={() => setPendingPrompt(p)}
                      >
                        <Trash2 className={cn('h-3 w-3', p.builtin ? 'text-muted-foreground' : 'text-red-400')} />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2"><AlertCircle className="h-4 w-4 text-blue-500" />提示词 与 技能包，该用哪个？</CardTitle>
            <CardDescription>两者不是同一层的东西：一个是「文本」，一个是「运行单元」。</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-3 md:grid-cols-2 text-xs">
            <div className="rounded-lg border border-blue-200 dark:border-blue-500/25 p-3 space-y-1.5">
              <div className="flex items-center gap-1.5 font-medium text-sm"><Wand2 className="h-4 w-4 text-blue-500" />提示词（本页）</div>
              <p className="text-muted-foreground">一段可复用的<strong className="text-foreground">文本</strong>：人设、约束、输出格式、排查话术。</p>
              <ul className="text-muted-foreground space-y-0.5 list-disc pl-4">
                <li>支持 <code className="font-mono">{'{{变量}}'}</code> 占位，插入时按需替换</li>
                <li>不绑定模型 / 工具 / 知识库，复制粘贴即可用</li>
                <li>可设为全局默认，保存即热生效</li>
              </ul>
              <p className="text-blue-700 dark:text-blue-400 pt-1">👉 只想复用一段话术，选提示词</p>
            </div>
            <div className="rounded-lg border border-purple-200 dark:border-purple-500/25 p-3 space-y-1.5">
              <div className="flex items-center gap-1.5 font-medium text-sm"><Package className="h-4 w-4 text-purple-500" />技能包（技能市场）</div>
              <p className="text-muted-foreground">一个可移植的<strong className="text-foreground">运行单元</strong>：数字员工的完整配置。</p>
              <ul className="text-muted-foreground space-y-0.5 list-disc pl-4">
                <li>模型 + 系统提示词 + 知识库分类 + 工具白名单 + 技能指令</li>
                <li>连同依赖的自定义 MCP 工具一起打包，安装即上岗</li>
                <li>可导出 JSON 跨环境迁移，带版本与评分</li>
              </ul>
              <div className="pt-1">
                <Link to="/ai/marketplace" className="text-purple-600 dark:text-purple-400 hover:underline inline-flex items-center gap-1">
                  👉 想把整个数字员工搬到另一套环境，去技能市场 <ChevronRight className="h-3 w-3" />
                </Link>
              </div>
            </div>
            <p className="md:col-span-2 text-[11px] text-muted-foreground">
              依赖方向是单向的：技能包内会<strong className="text-foreground">内嵌</strong>提示词文本，而提示词不感知技能包。
              因此改提示词库不会影响已安装的技能包，需要同步时在技能包里重新插入即可。
            </p>
          </CardContent>
        </Card>
      </div>
    )
  }

  function ApprovalContent() {
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">敏感操作审批策略</CardTitle>
            <CardDescription>控制 AI 执行 script_run 等敏感操作时的确认方式（PRD 5.2.5）</CardDescription>
          </CardHeader>
          <CardContent className="space-y-5">
            <div>
              <label className="text-sm font-medium mb-2 block">作用范围</label>
              <div className="flex gap-2">
                <button
                  onClick={() => { setApprovalScope('global'); loadApprovalPolicy() }}
                  className={cn(
                    'flex-1 text-xs px-3 py-2 rounded-md border transition-colors text-left',
                    approvalScope === 'global'
                      ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/10 dark:border-blue-500/30 dark:text-blue-400'
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
                      ? 'bg-blue-50 border-blue-200 text-blue-700 dark:bg-blue-500/10 dark:border-blue-500/30 dark:text-blue-400'
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

            <div>
              <label className="text-sm font-medium mb-2 block">策略模式</label>
              <div className="space-y-2">
                {APPROVAL_MODES.map(m => (
                  <label
                    key={m.value}
                    className={cn(
                      'flex items-start gap-3 p-3 rounded-lg border cursor-pointer transition-colors',
                      approvalMode === m.value
                        ? 'border-blue-300 bg-blue-50 dark:border-blue-500/40 dark:bg-blue-500/10'
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
      </div>
    )
  }

  function EvolutionContent() {
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2"><RefreshCw className="h-4 w-4" />进化闭环</CardTitle>
            <CardDescription>经验 → 记忆 → 知识/技能 → 更好回答 的自循环，反馈（点赞/点踩）作为回路信号。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
              <div className="rounded-lg border p-3 text-center"><p className="text-2xl font-bold">{evoStats?.feedback_up ?? '-'}</p><p className="text-xs text-muted-foreground">👍 点赞反馈</p></div>
              <div className="rounded-lg border p-3 text-center"><p className="text-2xl font-bold">{evoStats?.feedback_down ?? '-'}</p><p className="text-xs text-muted-foreground">👎 点踩反馈</p></div>
              <div className="rounded-lg border p-3 text-center"><p className="text-2xl font-bold">{evoStats?.memories ?? '-'}</p><p className="text-xs text-muted-foreground">🧠 长期记忆</p></div>
              <div className="rounded-lg border p-3 text-center"><p className="text-2xl font-bold">{evoStats?.disposal_cases ?? '-'}</p><p className="text-xs text-muted-foreground">📚 处置案例沉淀</p></div>
            </div>
            <div className="rounded-lg bg-muted/40 p-3 text-xs space-y-1.5">
              <p className="font-medium">闭环链路</p>
              <p>① 工单完成 → 处置经验写入长期记忆（experienceSink）</p>
              <p>② 知识沉淀策略将高价值会话/工单自动沉淀为知识库文章</p>
              <p>③ 记忆/知识在对话中注入 → 更好回答（记忆 top-K 命中即复用）</p>
              <p>④ 反馈（👍/👎）入库 → 参与训练数据集导出（微调）与闭环评估</p>
              <p className="text-muted-foreground">注：长期记忆当前为进程内存态、重启清空；需要持久化可加 DB 落盘（后续增强）。</p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2"><Settings2 className="h-4 w-4" />主动进化策略（全局默认）</CardTitle>
            <CardDescription>各数据源可独立覆盖下列默认值（留空/0 即继承全局）。黑名单命中任一关键词的提炼结果直接丢弃。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-center gap-2">
              <label className="flex items-center gap-1.5 text-xs cursor-pointer">
                <input type="checkbox" checked={policyForm.enabled} onChange={e => setPolicyForm({ ...policyForm, enabled: e.target.checked })} />
                启用主动进化（关闭后自动循环与手动执行均跳过）
              </label>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">默认执行频率（小时）</p>
                <Input className="h-9 text-xs" type="number" min={1} value={policyForm.interval_hours} onChange={e => setPolicyForm({ ...policyForm, interval_hours: e.target.value })} />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">默认目标分类</p>
                <Input className="h-9 text-xs" placeholder="知识获取" value={policyForm.target_category} onChange={e => setPolicyForm({ ...policyForm, target_category: e.target.value })} />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">最低提炼分数（0~1）</p>
                <Input className="h-9 text-xs" type="number" min={0} max={1} step={0.05} value={policyForm.min_score} onChange={e => setPolicyForm({ ...policyForm, min_score: e.target.value })} />
              </div>
            </div>
            <div className="space-y-1">
              <p className="text-xs text-muted-foreground">黑名单关键词（逗号分隔，命中标题或正文即丢弃）</p>
              <Input className="h-9 text-xs font-mono" placeholder="如: 广告, 招聘, 营销" value={policyForm.blacklist} onChange={e => setPolicyForm({ ...policyForm, blacklist: e.target.value })} />
            </div>
            <div className="flex items-center gap-2">
              <Button size="middle" type="default" onClick={saveEvoPolicy}><Save className="h-3.5 w-3.5 mr-1" />保存全局策略</Button>
              {evoPolicy?.updated_at && <p className="text-[11px] text-muted-foreground">上次更新 {new Date(evoPolicy.updated_at).toLocaleString('zh-CN')}</p>}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-base flex items-center gap-2"><Rocket className="h-4 w-4" />主动进化（互联网 → 本地知识）</CardTitle>
            <CardDescription>定期从 RSS/URL 数据源抓取内容，经 LLM 提炼（按分数与黑名单过滤）后自动沉淀为本地知识库文章（需已配置模型 API Key）。</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <div className="flex items-end gap-2 flex-wrap">
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">名称</p>
                <Input className="w-36 h-9 text-xs" placeholder="如: 运维博客" value={evoForm.name} onChange={e => setEvoForm({ ...evoForm, name: e.target.value })} />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">类型</p>
                <select className="flex h-9 rounded-md border border-input bg-background px-2 text-xs" value={evoForm.type} onChange={e => setEvoForm({ ...evoForm, type: e.target.value })}>
                  <option value="rss">RSS</option>
                  <option value="url">URL</option>
                  <option value="keyword">关键词(暂不支持)</option>
                </select>
              </div>
              <div className="space-y-1 flex-1 min-w-[240px]">
                <p className="text-xs text-muted-foreground">源地址</p>
                <Input className="h-9 text-xs font-mono" placeholder="https://example.com/feed.xml" value={evoForm.source} onChange={e => setEvoForm({ ...evoForm, source: e.target.value })} />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-muted-foreground">覆盖频率(h)/分类/分数(留空继承全局)</p>
                <div className="flex items-center gap-1">
                  <Input className="w-16 h-9 text-xs" type="number" min={0} placeholder="6" value={evoForm.interval_hours || ''} onChange={e => setEvoForm({ ...evoForm, interval_hours: parseInt(e.target.value) || 0 })} />
                  <Input className="w-24 h-9 text-xs" placeholder="分类" value={evoForm.target_category} onChange={e => setEvoForm({ ...evoForm, target_category: e.target.value })} />
                  <Input className="w-16 h-9 text-xs" type="number" min={0} max={1} step={0.05} placeholder="0.5" value={evoForm.min_score || ''} onChange={e => setEvoForm({ ...evoForm, min_score: parseFloat(e.target.value) || 0 })} />
                </div>
              </div>
              <label className="flex items-center gap-1.5 text-xs pb-2.5 cursor-pointer">
                <input type="checkbox" checked={evoForm.enabled} onChange={e => setEvoForm({ ...evoForm, enabled: e.target.checked })} />启用
              </label>
              <Button size="middle" type="default" onClick={saveEvoSource}><Plus className="h-3.5 w-3.5 mr-1" />添加数据源</Button>
            </div>

            {evoSources.length === 0 ? (
              <p className="text-xs text-muted-foreground py-2">暂无数据源。添加后（需 LLM 已配置）按策略频率自动提炼，也可点「立即执行」手动触发。</p>
            ) : (
              <div className="space-y-2">
                {evoSources.map(s => (
                  <div key={s.id} className="rounded-lg border border-border p-2.5 space-y-2">
                    <div className="flex items-center justify-between gap-2">
                      <div className="min-w-0 flex-1 flex items-center gap-2">
                        <Tag color={s.enabled ? 'green' : 'default'} className="text-[10px]">{s.enabled ? '启用' : '停用'}</Tag>
                        <Tag className="text-[10px]">{s.type}</Tag>
                        <span className="text-xs font-medium truncate">{s.name}</span>
                        <span className="text-[11px] text-muted-foreground truncate">{s.source}</span>
                      </div>
                      <div className="flex items-center gap-1 shrink-0">
                        <Button type="text" className="h-6 w-6 text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => startEditSource(s)} title="编辑策略"><Pencil className="h-3.5 w-3.5" /></Button>
                        <Button type="text" className="h-6 w-6 text-red-400" onClick={() => setPendingEvoId(s.id)} title="删除"><Trash2 className="h-3.5 w-3.5" /></Button>
                      </div>
                    </div>
                    {editingSrc === s.id ? (
                      <div className="flex items-end gap-2 flex-wrap">
                        <div className="space-y-1">
                          <p className="text-[10px] text-muted-foreground">频率(h, 0=全局)</p>
                          <Input className="w-20 h-8 text-xs" type="number" min={0} value={srcDraft.interval_hours} onChange={e => setSrcDraft({ ...srcDraft, interval_hours: e.target.value })} />
                        </div>
                        <div className="space-y-1">
                          <p className="text-[10px] text-muted-foreground">目标分类</p>
                          <Input className="w-28 h-8 text-xs" placeholder="继承全局" value={srcDraft.target_category} onChange={e => setSrcDraft({ ...srcDraft, target_category: e.target.value })} />
                        </div>
                        <div className="space-y-1">
                          <p className="text-[10px] text-muted-foreground">最低分(0=全局)</p>
                          <Input className="w-20 h-8 text-xs" type="number" min={0} max={1} step={0.05} value={srcDraft.min_score} onChange={e => setSrcDraft({ ...srcDraft, min_score: e.target.value })} />
                        </div>
                        <label className="flex items-center gap-1.5 text-xs pb-2 cursor-pointer">
                          <input type="checkbox" checked={srcDraft.enabled} onChange={e => setSrcDraft({ ...srcDraft, enabled: e.target.checked })} />启用
                        </label>
                        <Button size="middle" className="h-8" onClick={() => saveSourceEdit(s.id)}><Save className="h-3.5 w-3.5 mr-1" />保存</Button>
                        <Button size="middle" type="text" className="h-8" onClick={() => setEditingSrc(null)}>取消</Button>
                      </div>
                    ) : (
                      <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                        <span>频率: {s.interval_hours > 0 ? `${s.interval_hours}h` : '全局'}</span>
                        <span>分类: {s.target_category || '全局'}</span>
                        <span>最低分: {s.min_score > 0 ? s.min_score : '全局'}</span>
                        {s.last_run_at && <span className="ml-auto">上次执行 {new Date(s.last_run_at).toLocaleString('zh-CN')}</span>}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}

            <div className="flex items-center gap-2">
              <Button size="middle" onClick={runEvoFetch} disabled={evoFetching}>
                {evoFetching ? <Loader2 className="h-3.5 w-3.5 mr-1 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5 mr-1" />}
                {evoFetching ? '执行中…' : '立即执行'}
              </Button>
              <p className="text-[11px] text-muted-foreground">按策略沉淀至目标分类（tag: evolution），低于最低分或命中黑名单的内容自动丢弃</p>
            </div>

            {evoResults && (
              <div className="space-y-1.5">
                {evoResults.length === 0 && <p className="text-xs text-muted-foreground">无启用的数据源</p>}
                {evoResults.map((r, i) => (
                  <div key={i} className="flex items-start gap-2 text-xs rounded-lg border border-border p-2">
                    <Tag color={r.status === 'ok' ? 'green' : 'red'} className="text-[10px] shrink-0 mt-0.5">{r.status === 'ok' ? '成功' : '失败'}</Tag>
                    <div className="min-w-0">
                      <p className="font-medium">{r.name} <span className="text-muted-foreground font-normal">({r.type})</span></p>
                      <p className="text-muted-foreground break-all">{r.message}</p>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="p-6 h-[calc(100vh-120px)] flex flex-col">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Settings2 className="h-6 w-6 text-blue-500" />
            AI 智能体配置
          </h1>
          <p className="text-sm text-muted-foreground">管理 Agent、提示词、模型与审批策略；MCP 工具的注册与启停请前往「MCP 工具」页</p>
        </div>
      </div>

      {/* 模型池 datalist: 为 Agent 表单的模型名提供下拉建议 */}
      <datalist id="model-pool">
        {modelPool.map(m => (
          <option key={m.name} value={m.name}>{m.model}</option>
        ))}
      </datalist>
      <Tabs activeKey={activeTab} onChange={setActiveTab} className="flex-1 flex flex-col min-h-0" items={[
        {
          key: 'agents',
          label: <span><Bot className="h-4 w-4 mr-1.5 inline" />我的智能体</span>,
          children: <AgentsContent />,
        },
        {
          key: 'digital',
          label: <span><Sparkles className="h-4 w-4 mr-1.5 inline" />数字员工</span>,
          children: <RoleTemplatesPanel />,
        },
        {
          key: 'prompts',
          label: <span><Wand2 className="h-4 w-4 mr-1.5 inline" />提示词</span>,
          children: <PromptsContent />,
        },
        {
          key: 'approval',
          label: <span><ShieldCheck className="h-4 w-4 mr-1.5 inline" />审批策略</span>,
          children: <ApprovalContent />,
        },
        {
          key: 'evolution',
          label: <span><RefreshCw className="h-4 w-4 mr-1.5 inline" />进化</span>,
          children: <EvolutionContent />,
        },
      ]} />

      {/* 提示词编辑器: 正文 + 变量实时解析 + 本地预览(不计引用次数) */}
      <WideDialog
        open={promptDialogOpen}
        onClose={() => setPromptDialogOpen(false)}
        title={promptForm.id ? '编辑提示词' : '新建提示词'}
      >
        <div className="space-y-3">
          <div className="grid gap-3 md:grid-cols-2">
            <div>
              <label className="text-xs font-medium mb-1.5 block">名称 <span className="text-red-400">*</span></label>
              <Input placeholder="如: 故障排查（分层定位）" value={promptForm.name} onChange={e => setPromptForm(f => ({ ...f, name: e.target.value }))} />
            </div>
            <div>
              <label className="text-xs font-medium mb-1.5 block">场景</label>
              <Input list="prompt-scenes" placeholder="如: 故障排查（可自定义）" value={promptForm.scene} onChange={e => setPromptForm(f => ({ ...f, scene: e.target.value }))} />
              <datalist id="prompt-scenes">
                {promptScenes.map(s => <option key={s} value={s} />)}
              </datalist>
            </div>
          </div>

          <div>
            <label className="text-xs font-medium mb-1.5 block">用途描述</label>
            <Input placeholder="一句话说明这条提示词解决什么问题" value={promptForm.description} onChange={e => setPromptForm(f => ({ ...f, description: e.target.value }))} />
          </div>

          <div>
            <div className="flex items-center justify-between mb-1.5">
              <label className="text-xs font-medium">正文 <span className="text-red-400">*</span></label>
              <span className="text-[11px] text-muted-foreground">
                用 <code className="font-mono text-blue-600 dark:text-blue-400">{'{{变量名}}'}</code> 标记待填内容，插入时按需替换
              </span>
            </div>
            <textarea
              value={promptForm.content}
              onChange={e => setPromptForm(f => ({ ...f, content: e.target.value }))}
              rows={12}
              placeholder={'你是一位资深 SRE，负责快速定位线上故障。\n\n现象: {{现象}}\n涉及系统: {{系统}}\n\n请按以下结构回答:\n1. ...'}
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono leading-relaxed resize-y focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/40"
            />
          </div>

          {/* 变量解析 + 预览取值 */}
          {parsePromptVars(promptForm.content).length > 0 && (
            <div className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.08)] p-3 space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium">检测到 {parsePromptVars(promptForm.content).length} 个变量</span>
                <Button type="default" size="middle" className="h-7 text-xs" onClick={() => setPromptPreview(renderPromptLocal(promptForm.content, promptVars))}>
                  <Eye className="h-3 w-3 mr-1" />预览渲染
                </Button>
              </div>
              <div className="grid gap-2 md:grid-cols-2">
                {parsePromptVars(promptForm.content).map(v => (
                  <div key={v} className="flex items-center gap-2">
                    <code className="text-[10px] px-1.5 py-0.5 rounded bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-400 font-mono shrink-0">{v}</code>
                    <Input
                      placeholder="预览用示例值（不会保存）"
                      value={promptVars[v] || ''}
                      onChange={e => setPromptVars(prev => ({ ...prev, [v]: e.target.value }))}
                      className="h-7 text-xs"
                    />
                  </div>
                ))}
              </div>
              {promptPreview !== null && (
                <pre className="text-[11px] font-mono bg-gray-50 dark:bg-[rgba(255,255,255,0.03)] rounded-md p-2.5 max-h-48 overflow-auto whitespace-pre-wrap">{promptPreview}</pre>
              )}
            </div>
          )}

          <label className={cn(
            'flex items-start gap-2 text-xs p-2.5 rounded-lg border cursor-pointer transition-colors',
            promptForm.is_default
              ? 'border-blue-300 bg-blue-50 dark:border-blue-500/40 dark:bg-blue-500/10'
              : 'border-gray-200 dark:border-[rgba(255,255,255,0.08)]',
          )}>
            <input
              type="checkbox"
              checked={promptForm.is_default}
              onChange={e => setPromptForm(f => ({ ...f, is_default: e.target.checked }))}
              className="accent-blue-500 mt-0.5"
            />
            <span>
              <span className="font-medium">设为默认系统提示词</span>
              <span className="text-muted-foreground block mt-0.5">
                租户内唯一：勾选后原默认自动摘牌，所有未单独配置 system_prompt 的 Agent 与对话立即改用这条（热生效）。
              </span>
            </span>
          </label>

          {promptForm.id && prompts.find(p => p.id === promptForm.id)?.builtin && (
            <p className="text-[11px] text-amber-600 dark:text-amber-400 flex items-start gap-1.5">
              <AlertCircle className="h-3.5 w-3.5 shrink-0 mt-px" />
              这是预置提示词：正文可以改，但不能删除。若想保留原版，建议用「另存副本」派生自建版本。
            </p>
          )}

          <div className="flex justify-end gap-2 pt-1">
            <Button type="default" onClick={() => setPromptDialogOpen(false)}>取消</Button>
            <Button onClick={submitPrompt} disabled={promptSaving}>
              {promptSaving ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Save className="h-4 w-4 mr-1" />}保存
            </Button>
          </div>
        </div>
      </WideDialog>

      {/* 删除 / 默认确认弹窗 */}
      <Popconfirm
        title="删除数据源"
        description="删除该数据源？"
        onConfirm={deleteEvoSource}
        okText="删除"
        cancelText="取消"
        open={!!pendingEvoId}
        onCancel={() => setPendingEvoId(null)}
      >
        <span />
      </Popconfirm>
      <Popconfirm
        title="删除提示词"
        description={pendingPrompt ? `确定删除提示词「${pendingPrompt.name}」？` : undefined}
        onConfirm={deletePrompt}
        okText="删除"
        cancelText="取消"
        open={!!pendingPrompt}
        onCancel={() => setPendingPrompt(null)}
      >
        <span />
      </Popconfirm>
      <Popconfirm
        title="设为默认系统提示词"
        description={pendingDefaultPrompt ? `将「${pendingDefaultPrompt.name}」设为默认系统提示词？所有未单独配置 system_prompt 的 Agent 与对话都会改用它。` : undefined}
        onConfirm={setAsDefault}
        okText="设为默认"
        cancelText="取消"
        open={!!pendingDefaultPrompt}
        onCancel={() => setPendingDefaultPrompt(null)}
      >
        <span />
      </Popconfirm>
      <Popconfirm
        title="删除 Agent"
        description="确定删除该 Agent？此操作不可恢复。"
        onConfirm={handleDeleteAgent}
        okText="删除"
        cancelText="取消"
        open={!!pendingAgentId}
        onCancel={() => setPendingAgentId(null)}
      >
        <span />
      </Popconfirm>
    </div>
  )
}
