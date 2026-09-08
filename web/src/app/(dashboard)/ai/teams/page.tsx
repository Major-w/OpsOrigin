
import { useEffect, useState, useCallback } from 'react'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { Button, Input, Tag, Space, Modal, Popconfirm, Select } from 'antd'
import { useTeams, type AgentTeam, type TeamMode, type TeamRole, type CreateTeamPayload, type TeamMember } from '@/stores/ai'
import { Users, Plus, RefreshCw, Trash2, Edit, Bot, Settings2 } from 'lucide-react'

// ----------------------------- 标签 / 样式映射 -----------------------------

const modeLabels: Record<TeamMode, string> = {
  supervisor: '主管调度',
  parallel: '并行协作',
  sequential: '顺序流转',
  debate: '辩论裁决',
}
const modeBadge: Record<TeamMode, string> = {
  supervisor: 'default',
  parallel: 'blue',
  sequential: 'orange',
  debate: 'red',
}
const roleLabels: Record<TeamRole, string> = {
  supervisor: '主管',
  member: '成员',
  finalizer: '终结者',
}
const roleBadge: Record<TeamRole, string> = {
  supervisor: 'blue',
  member: 'default',
  finalizer: 'green',
}

// ----------------------------- 表单类型 -----------------------------

interface MemberForm {
  agent_id: string
  agent_name: string
  role: TeamRole
  can_finalize: boolean
  persona: string
  system_prompt: string
  enable_tools: boolean
  model_name: string
}

interface TeamForm {
  id?: string
  name: string
  description: string
  mode: TeamMode
  system_prompt: string
  enable_tools: boolean
  member_timeout_sec: number
  member_retries: number
  debate_rounds: number
  supervisor_verify: boolean
  members: MemberForm[]
}

function emptyForm(): TeamForm {
  return {
    name: '',
    description: '',
    mode: 'supervisor',
    system_prompt: '',
    enable_tools: false,
    member_timeout_sec: 90,
    member_retries: 1,
    debate_rounds: 2,
    supervisor_verify: false,
    members: [
      { agent_id: '', agent_name: '', role: 'member', can_finalize: false, persona: '', system_prompt: '', enable_tools: false, model_name: '' },
    ],
  }
}

function teamToForm(team: AgentTeam): TeamForm {
  return {
    id: team.id,
    name: team.name,
    description: team.description,
    mode: team.mode,
    system_prompt: team.system_prompt,
    enable_tools: team.enable_tools,
    member_timeout_sec: team.member_timeout_sec || 90,
    member_retries: team.member_retries || 1,
    debate_rounds: team.debate_rounds || 2,
    supervisor_verify: team.supervisor_verify || false,
    members: team.members.length > 0
      ? team.members.map((m: TeamMember) => ({
          agent_id: m.agent_id,
          agent_name: m.agent_name,
          role: m.role,
          can_finalize: m.can_finalize,
          persona: m.persona,
          system_prompt: m.system_prompt,
          enable_tools: m.enable_tools,
          model_name: m.model_name || '',
        }))
      : emptyForm().members,
  }
}

// ----------------------------- 页面 -----------------------------

export default function TeamsPage() {
  const { teams, loading, loadTeams, createTeam, updateTeam, deleteTeam } = useTeams()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<TeamForm>(emptyForm())
  const [pendingDeleteId, setPendingDeleteId] = useState<string | null>(null)

  useEffect(() => {
    loadTeams()
  }, [loadTeams])

  const openCreate = useCallback(() => {
    setForm(emptyForm())
    setDialogOpen(true)
  }, [])

  const openEdit = useCallback((team: AgentTeam) => {
    setForm(teamToForm(team))
    setDialogOpen(true)
  }, [])

  const updateMember = (i: number, patch: Partial<MemberForm>) => {
    setForm(f => ({
      ...f,
      members: f.members.map((m, idx) => (idx === i ? { ...m, ...patch } : m)),
    }))
  }
  const addMember = () => {
    setForm(f => ({
      ...f,
      members: [
        ...f.members,
        { agent_id: '', agent_name: '', role: 'member', can_finalize: false, persona: '', system_prompt: '', enable_tools: false, model_name: '' },
      ],
    }))
  }
  const removeMember = (i: number) => {
    setForm(f => ({ ...f, members: f.members.filter((_, idx) => idx !== i) }))
  }

  const handleSave = async () => {
    if (!form.name.trim()) return
    const payload: CreateTeamPayload = {
      name: form.name.trim(),
      description: form.description.trim(),
      mode: form.mode,
      system_prompt: form.system_prompt,
      enable_tools: form.enable_tools,
      member_timeout_sec: form.member_timeout_sec,
      member_retries: form.member_retries,
      debate_rounds: form.debate_rounds,
      supervisor_verify: form.supervisor_verify,
      members: form.members.map(m => ({
        agent_id: m.agent_id.trim(),
        agent_name: m.agent_name.trim(),
        role: m.role,
        can_finalize: m.can_finalize,
        persona: m.persona,
        system_prompt: m.system_prompt,
        enable_tools: m.enable_tools,
        model_name: m.model_name.trim(),
      })),
    }
    if (form.id) {
      await updateTeam(form.id, payload)
    } else {
      await createTeam(payload)
    }
    setDialogOpen(false)
    setForm(emptyForm())
  }

  const handleDelete = async () => {
    if (!pendingDeleteId) return
    await deleteTeam(pendingDeleteId)
    setPendingDeleteId(null)
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2">
            <Users className="h-6 w-6 text-blue-500" />
            多智能体团队
          </h1>
          <p className="text-sm text-muted-foreground">编排多个 AI 代理协同完成复杂运维任务（主管调度 / 并行 / 顺序 / 辩论）</p>
        </div>
        <div className="flex gap-2">
          <Button type="default" size="middle" onClick={() => loadTeams()} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新
          </Button>
          <Button size="middle" onClick={openCreate}><Plus className="h-4 w-4 mr-1" />新建团队</Button>
        </div>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">团队列表（{teams.length}）</CardTitle>
          <CardDescription>点击「编辑」修改团队配置，包含模式与成员编排</CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          {loading ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">加载中...</p>
          ) : teams.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted-foreground">暂无团队，点击「新建团队」开始创建。</p>
          ) : (
            <div className="flex flex-col">
              {teams.map((team: AgentTeam) => (
                <div
                  key={team.id}
                  className="border-b last:border-0 px-4 py-3 hover:bg-accent transition-colors flex items-center justify-between gap-3"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <Bot className="h-4 w-4 text-blue-500 shrink-0" />
                    <div className="min-w-0">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium truncate">{team.name}</span>
                        <Tag color={modeBadge[team.mode]} className="text-[10px]">{modeLabels[team.mode]}</Tag>
                      </div>
                      <p className="text-xs text-muted-foreground mt-0.5 truncate">{team.description || '无描述'}</p>
                      <div className="flex gap-1.5 mt-1 flex-wrap">
                        {team.members.slice(0, 6).map((m, i) => (
                          <Tag key={`${m.agent_id || i}-${m.agent_name}`} color={roleBadge[m.role]} className="text-[10px]">
                            {m.agent_name || roleLabels[m.role]}
                          </Tag>
                        ))}
                        {team.members.length > 6 && (
                          <Tag className="text-[10px]">+{team.members.length - 6}</Tag>
                        )}
                        {team.members.length === 0 && (
                          <Tag className="text-[10px]">无成员</Tag>
                        )}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-[10px] text-muted-foreground font-mono hidden md:inline">{team.id.slice(0, 8)}</span>
                    <Button size="middle" onClick={() => openEdit(team)}>
                      <Edit className="h-3.5 w-3.5 mr-1" />编辑
                    </Button>
                    <Button
                      type="text" size="middle" className="h-7 w-7"
                      onClick={() => setPendingDeleteId(team.id)}
                    >
                      <Trash2 className="h-3 w-3 text-muted-foreground hover:text-destructive" />
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 新建 / 编辑团队 Dialog */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={form.id ? '编辑团队' : '新建团队'} footer={
        <Space>
          <Button onClick={() => setDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleSave} disabled={!form.name.trim()}>{form.id ? '保存修改' : '创建团队'}</Button>
        </Space>
      }>
        <div className="max-h-[72vh] overflow-y-auto pr-1 space-y-4">
          {/* 基础信息 */}
          <div className="space-y-3">
            <div>
              <label className="text-sm font-medium">团队名称 *</label>
              <Input
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="例如：平台交付团队"
                onKeyDown={e => e.key === 'Enter' && !form.id && handleSave()}
              />
            </div>
            <div>
              <label className="text-sm font-medium">描述</label>
              <Input
                value={form.description}
                onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
                placeholder="一句话描述团队用途"
              />
            </div>
            <div>
              <label className="text-sm font-medium">协作模式</label>
              <Select
                value={form.mode}
                onChange={value => setForm(f => ({ ...f, mode: value as TeamMode }))}
                options={[
                  { value: 'supervisor', label: '主管调度（supervisor）' },
                  { value: 'parallel', label: '并行协作（parallel）' },
                  { value: 'sequential', label: '顺序流转（sequential）' },
                  { value: 'debate', label: '辩论裁决（debate）' },
                ]}
                className="w-full"
              />
            </div>
            <div>
              <label className="text-sm font-medium">团队系统提示词</label>
              <Input.TextArea
                value={form.system_prompt}
                onChange={e => setForm(f => ({ ...f, system_prompt: e.target.value }))}
                placeholder="团队整体的目标与约束（可选）"
                rows={3}
              />
            </div>

            {/* 团队级编排配置 */}
            <div className="border rounded-lg p-3 space-y-2">
              <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wide">编排参数</label>
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="text-[11px] text-muted-foreground">超时(秒)</label>
                  <Input
                    type="number" min={10} max={600}
                    value={form.member_timeout_sec}
                    onChange={e => setForm(f => ({ ...f, member_timeout_sec: parseInt(e.target.value) || 90 }))}
                  />
                </div>
                <div>
                  <label className="text-[11px] text-muted-foreground">重试次数</label>
                  <Input
                    type="number" min={0} max={5}
                    value={form.member_retries}
                    onChange={e => setForm(f => ({ ...f, member_retries: parseInt(e.target.value) || 1 }))}
                  />
                </div>
              </div>
              {form.mode === 'debate' && (
                <div>
                  <label className="text-[11px] text-muted-foreground">辩论轮数</label>
                  <Input
                    type="number" min={1} max={10}
                    value={form.debate_rounds}
                    onChange={e => setForm(f => ({ ...f, debate_rounds: parseInt(e.target.value) || 2 }))}
                  />
                </div>
              )}
              <div className="flex items-center gap-3 flex-wrap">
                <label className="flex items-center gap-1 text-xs cursor-pointer">
                  <input type="checkbox" checked={form.enable_tools} onChange={e => setForm(f => ({ ...f, enable_tools: e.target.checked }))} />
                  团队启用工具调用
                </label>
                <label className="flex items-center gap-1 text-xs cursor-pointer">
                  <input type="checkbox" checked={form.supervisor_verify} onChange={e => setForm(f => ({ ...f, supervisor_verify: e.target.checked }))} />
                  主管审核修订
                </label>
              </div>
            </div>
          </div>

          {/* 成员编辑 */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-sm font-medium flex items-center gap-1">
                <Settings2 className="h-3.5 w-3.5" />成员（{form.members.length}）
              </label>
              <Button size="middle" onClick={addMember}><Plus className="h-3.5 w-3.5 mr-1" />添加成员</Button>
            </div>

            {form.members.map((m, i) => (
              <div key={i} className="rounded-lg border border-gray-200 dark:border-[rgba(255,255,255,0.06)] p-3 space-y-2 relative">
                <div className="flex items-center gap-2">
                  <Input
                    className="flex-1"
                    value={m.agent_name}
                    onChange={e => updateMember(i, { agent_name: e.target.value })}
                    placeholder="成员名称"
                  />
                  <Select
                    value={m.role}
                    onChange={value => updateMember(i, { role: value as TeamRole })}
                    options={[
                      { value: 'supervisor', label: '主管' },
                      { value: 'member', label: '成员' },
                      { value: 'finalizer', label: '终结者' },
                    ]}
                    className="w-28"
                  />
                  <label className="flex items-center gap-1 text-xs text-muted-foreground shrink-0" title="是否可终结对话">
                    <input
                      type="checkbox"
                      checked={m.can_finalize}
                      onChange={e => updateMember(i, { can_finalize: e.target.checked })}
                    />
                    可终结
                  </label>
                  {form.members.length > 1 && (
                    <Button type="text" size="middle" className="h-7 w-7 text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => removeMember(i)}>
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  )}
                </div>
                <Input
                  value={m.agent_id}
                  onChange={e => updateMember(i, { agent_id: e.target.value })}
                  placeholder="agent_id（关联后端代理标识）"
                />
                <div>
                  <label className="text-[11px] text-muted-foreground">人设（persona）</label>
                  <Input.TextArea
                    className="min-h-[52px]"
                    value={m.persona}
                    onChange={e => updateMember(i, { persona: e.target.value })}
                    placeholder="该成员的角色定位与口吻"
                    rows={2}
                  />
                </div>
                <div>
                  <label className="text-[11px] text-muted-foreground">系统提示词（system_prompt）</label>
                  <Input.TextArea
                    className="min-h-[52px]"
                    value={m.system_prompt}
                    onChange={e => updateMember(i, { system_prompt: e.target.value })}
                    placeholder="该成员专属的指令与约束"
                    rows={2}
                  />
                </div>
                <div className="flex items-center gap-3 flex-wrap">
                  <label className="flex items-center gap-1 text-xs cursor-pointer">
                    <input type="checkbox" checked={m.enable_tools} onChange={e => updateMember(i, { enable_tools: e.target.checked })} />
                    启用工具调用
                  </label>
                  <div className="flex items-center gap-1">
                    <label className="text-[11px] text-muted-foreground shrink-0">模型</label>
                    <Input
                      className="h-7 w-32 text-xs"
                      value={m.model_name}
                      onChange={e => updateMember(i, { model_name: e.target.value })}
                      placeholder="覆盖默认模型"
                    />
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>

        <Popconfirm
          title="删除团队"
          description="确认删除该团队？此操作不可撤销。"
          onConfirm={handleDelete}
          okText="删除"
          cancelText="取消"
          open={!!pendingDeleteId}
          onCancel={() => setPendingDeleteId(null)}
        >
          <span />
        </Popconfirm>
      </Modal>
    </div>
  )
}
