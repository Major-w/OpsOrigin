
import { useEffect, useState, useMemo } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { useDeployStore, type DeployApp, type AppType, type ReleaseTicket } from '@/stores/deploy'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Badge, Input, Select, Tag, message } from 'antd'
import { ArrowLeft, GitBranch, Server, Globe, Terminal, Heart, Save, Power, Rocket, RotateCcw, Check, Ban, Play, Clock, CheckCircle, XCircle, Loader2, Boxes } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
const APP_TYPES: { value: AppType; label: string }[] = [
  { value: 'java', label: 'Java' }, { value: 'go', label: 'Go' }, { value: 'python', label: 'Python' },
  { value: 'nodejs', label: 'Node.js' }, { value: 'static', label: '静态' }, { value: 'container', label: '容器' },
]
const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }
const ENV_COLORS: Record<string, string> = {
  dev: 'bg-blue-500/10 text-blue-600 border-blue-500/20',
  test: 'bg-amber-500/10 text-amber-600 border-amber-500/20',
  staging: 'bg-purple-500/10 text-purple-600 border-purple-500/20',
  prod: 'bg-red-500/10 text-red-600 border-red-500/20',
}

const STATUS_META: Record<string, { label: string; color: string }> = {
  draft: { label: '草稿', color: 'bg-slate-500/10 text-slate-600 border-slate-500/20' },
  pending_approval: { label: '待审批', color: 'bg-amber-500/10 text-amber-600 border-amber-500/20' },
  approved: { label: '已审批', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20' },
  deploying: { label: '发布中', color: 'bg-cyan-500/10 text-cyan-600 border-cyan-500/20' },
  succeeded: { label: '已成功', color: 'bg-blue-500/10 text-blue-600 border-blue-500/20' },
  failed: { label: '已失败', color: 'bg-red-500/10 text-red-600 border-red-500/20' },
  rolled_back: { label: '已回滚', color: 'bg-orange-500/10 text-orange-600 border-orange-500/20' },
  rejected: { label: '已驳回', color: 'bg-red-500/10 text-red-600 border-red-500/20' },
}

export default function AppDetailPage() {
  const params = useParams()
  const navigate = useNavigate()
  const appId = String(params.id || '')
  const { apps, releases, fetchApps, fetchReleases, updateApp, approveRelease, rejectRelease, deployRelease, rollbackRelease } = useDeployStore()

  const [editing, setEditing] = useState(false)
  const [activeTab, setActiveTab] = useState<'info' | 'config' | 'releases'>('info')
  const [form, setForm] = useState<Partial<DeployApp>>({})

  useEffect(() => {
    fetchApps()
    fetchReleases()
  }, [])

  const app = apps.find(a => a.id === appId)
  const appReleases = useMemo(() => releases.filter(r => r.app_id === appId), [releases, appId])

  useEffect(() => {
    if (app && !editing) setForm({ ...app })
  }, [app, editing])

  if (!app) {
    return (
      <div className="p-6">
        <Button type="text" size="middle" onClick={() => navigate('/deploy/apps')}><ArrowLeft className="h-3.5 w-3.5 mr-1" />返回应用列表</Button>
        <div className="flex items-center justify-center h-64 text-muted-foreground text-sm">应用不存在或已删除</div>
      </div>
    )
  }

  const handleSave = async () => {
    const ok = await updateApp(app.id, form)
    if (ok) setEditing(false)
  }

  const toggleStatus = async () => {
    const newStatus = app.status === 'enabled' ? 'disabled' : 'enabled'
    await updateApp(app.id, { status: newStatus })
    message.success(newStatus === 'enabled' ? '应用已启用' : '应用已禁用')
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" onClick={() => navigate('/deploy/apps')} title="返回"><ArrowLeft className="h-4 w-4" /></Button>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="text-2xl font-bold">{app.name}</h1>
              <Tag className="text-[10px]">{APP_TYPES.find(t => t.value === app.type)?.label || app.type}</Tag>
              <Tag className={cn("text-[10px]", app.status === 'enabled' ? 'text-blue-600 border-blue-500/20' : 'text-muted-foreground')}>
                {app.status === 'enabled' ? '启用' : '禁用'}
              </Tag>
            </div>
            <p className="text-xs text-muted-foreground mt-0.5 font-mono">{app.code}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button onClick={toggleStatus}><Power className="h-3.5 w-3.5 mr-1" />{app.status === 'enabled' ? '禁用' : '启用'}</Button>
          {editing ? (
            <>
              <Button onClick={() => { setEditing(false); setForm({ ...app }) }}>取消</Button>
              <Button type="primary" onClick={handleSave}><Save className="h-3.5 w-3.5 mr-1" />保存</Button>
            </>
          ) : (
            <Button type="primary" onClick={() => setEditing(true)}><Save className="h-3.5 w-3.5 mr-1" />编辑</Button>
          )}
        </div>
      </div>

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        {app.envs.map(env => (
          <Card key={env}>
            <CardHeader className="py-3"><CardTitle className={cn("text-xs flex items-center gap-1", ENV_COLORS[env].split(' ')[1])}>
              <span className={cn("w-1.5 h-1.5 rounded-full", ENV_COLORS[env].split(' ')[0].replace('/10', ''))} />
              {ENV_LABELS[env]}
            </CardTitle></CardHeader>
            <CardContent className="pt-0">
              <span className="text-sm font-mono font-bold">{app.current_versions[env as keyof typeof app.current_versions] || '-'}</span>
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="flex items-center gap-1 border-b">
        {([['info', '基本信息'], ['config', '发布配置'], ['releases', '发布记录']] as const).map(([key, label]) => (
          <button key={key} onClick={() => setActiveTab(key)}
            className={cn("px-4 py-2 text-sm font-medium border-b-2 transition-colors",
              activeTab === key ? 'border-blue-500 text-blue-600' : 'border-transparent text-muted-foreground hover:text-foreground')}>
            {label}
            {key === 'releases' && appReleases.length > 0 && <span className="ml-1 text-[10px]">({appReleases.length})</span>}
          </button>
        ))}
      </div>

      {activeTab === 'info' && (
        <Card>
          <CardContent className="p-4 space-y-3">
            <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
              <Field label="应用名称" value={app.name} editing={editing} formKey="name" form={form} setForm={setForm} />
              <Field label="应用代码" value={app.code} editing={editing} formKey="code" form={form} setForm={setForm} mono />
              <div>
                <label className="text-xs text-muted-foreground">类型</label>
                {editing ? (
                  <Select className="mt-1 w-full" value={form.type || 'java'} onChange={v => setForm(f => ({ ...f, type: v as AppType }))} options={APP_TYPES.map(t => ({ label: t.label, value: t.value }))} />
                ) : <p className="text-sm font-medium mt-1">{APP_TYPES.find(t => t.value === app.type)?.label || app.type}</p>}
              </div>
              <Field label="负责人" value={app.owner} editing={editing} formKey="owner" form={form} setForm={setForm} />
              <div>
                <label className="text-xs text-muted-foreground">状态</label>
                <p className="text-sm font-medium mt-1">{app.status === 'enabled' ? '启用' : '禁用'}</p>
              </div>
              <div>
                <label className="text-xs text-muted-foreground">创建时间</label>
                <p className="text-xs text-muted-foreground mt-1">{formatTime(app.created_at)}</p>
              </div>
            </div>
            <div>
              <label className="text-xs text-muted-foreground">描述</label>
              {editing ? (
                <Input.TextArea className="mt-1" rows={2} value={form.description || ''} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} />
              ) : <p className="text-sm mt-1">{app.description || '-'}</p>}
            </div>
            <div>
              <label className="text-xs text-muted-foreground">标签</label>
              <div className="flex flex-wrap gap-1.5 mt-1">
                {Object.entries(app.labels || {}).map(([k, v]) => (
                  <Tag key={k} className="text-[10px]">{k}: {v}</Tag>
                ))}
                {Object.keys(app.labels || {}).length === 0 && <span className="text-xs text-muted-foreground">-</span>}
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {activeTab === 'config' && (
        <Card>
          <CardContent className="p-4 space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <Field label="代码仓库" value={app.repo_url} editing={editing} formKey="repo_url" form={form} setForm={setForm} mono icon={<GitBranch className="h-3 w-3" />} />
              <Field label="分支" value={app.branch} editing={editing} formKey="branch" form={form} setForm={setForm} mono />
              <Field label="构建命令" value={app.build_cmd} editing={editing} formKey="build_cmd" form={form} setForm={setForm} mono icon={<Terminal className="h-3 w-3" />} />
              <Field label="产物路径" value={app.artifact_path} editing={editing} formKey="artifact_path" form={form} setForm={setForm} mono />
              <Field label="部署脚本" value={app.deploy_script} editing={editing} formKey="deploy_script" form={form} setForm={setForm} mono icon={<Server className="h-3 w-3" />} />
              <Field label="目标端口" value={String(app.target_port)} editing={editing} formKey="target_port" form={form} setForm={setForm} mono icon={<Globe className="h-3 w-3" />} />
              <Field label="健康检查" value={app.health_check_url} editing={editing} formKey="health_check_url" form={form} setForm={setForm} mono icon={<Heart className="h-3 w-3" />} />
              <div className="col-span-2 mt-2 pt-2 border-t">
                <p className="text-[10px] font-medium text-violet-600 mb-1.5">容器模式配置（目标环境 mode=container 时使用）</p>
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <Field label="镜像地址（不含 tag）" value={app.image_url} editing={editing} formKey="image_url" form={form} setForm={setForm} mono icon={<Boxes className="h-3 w-3" />} />
                  <Field label="命名空间" value={app.namespace} editing={editing} formKey="namespace" form={form} setForm={setForm} mono />
                  <Field label="工作负载（如 deployment/myapp）" value={app.workload} editing={editing} formKey="workload" form={form} setForm={setForm} mono />
                  <Field label="容器名（set image 用）" value={app.container_name} editing={editing} formKey="container_name" form={form} setForm={setForm} mono />
                  <Field label="副本数" value={String(app.replicas)} editing={editing} formKey="replicas" form={form} setForm={setForm} mono />
                </div>
                <div className="mt-3">
                  <label className="text-xs text-muted-foreground">原始 YAML（可选，优先于 set image 直接 apply）</label>
                  {editing ? (
                    <Input.TextArea className="mt-1 font-mono" rows={4} value={form.manifest || ''} onChange={e => setForm(f => ({ ...f, manifest: e.target.value }))} placeholder="apiVersion: apps/v1&#10;kind: Deployment&#10;..." />
                  ) : <pre className="text-xs mt-1 bg-muted/30 rounded p-2 whitespace-pre-wrap">{app.manifest || '-'}</pre>}
                </div>
              </div>
              <div>
                <label className="text-xs text-muted-foreground">发布环境</label>
                <div className="flex gap-1.5 mt-1">
                  {app.envs.map(env => <span key={env} className={cn("text-[10px] px-2 py-0.5 rounded border", ENV_COLORS[env])}>{ENV_LABELS[env]}</span>)}
                </div>
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {activeTab === 'releases' && (
        <div className="space-y-2">
          {appReleases.length === 0 && <div className="text-center text-sm text-muted-foreground py-12">暂无发布记录</div>}
          {appReleases.map(rel => {
            const sm = STATUS_META[rel.status]
            return (
              <Card key={rel.id} className="hover:border-blue-500/20 transition-colors">
                <CardContent className="p-3">
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-3 min-w-0">
                      <Tag className={cn("text-[10px]", sm.color)}>{sm.label}</Tag>
                      <div className="min-w-0">
                        <p className="text-sm font-medium truncate">{rel.title}</p>
                        <div className="flex items-center gap-2 text-[10px] text-muted-foreground mt-0.5">
                          <span className={cn("px-1 py-0.5 rounded border", ENV_COLORS[rel.env])}>{ENV_LABELS[rel.env]}</span>
                          <span className="font-mono">{rel.version}</span>
                          <span>·</span>
                          <span>{rel.commit_id}</span>
                          <span>·</span>
                          <span>{rel.operator}</span>
                          <span>·</span>
                          <span>{formatTime(rel.created_at)}</span>
                        </div>
                      </div>
                    </div>
                    <div className="flex gap-1 shrink-0">
                      {rel.status === 'pending_approval' && (
                        <>
                          <Button type="text" size="middle" className="text-blue-500" onClick={() => approveRelease(rel.id)} title="审批通过"><Check className="h-3 w-3" /></Button>
                          <Button type="text" size="middle" danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => rejectRelease(rel.id, '驳回')} title="驳回"><Ban className="h-3 w-3" /></Button>
                        </>
                      )}
                      {rel.status === 'approved' && <Button type="text" size="middle" className="text-cyan-500" onClick={() => deployRelease(rel.id)} title="执行"><Play className="h-3 w-3" /></Button>}
                      {(rel.status === 'succeeded' || rel.status === 'rolled_back') && <Button type="text" size="middle" className="text-orange-500" onClick={() => rollbackRelease(rel.id)} title="回滚"><RotateCcw className="h-3 w-3" /></Button>}
                    </div>
                  </div>
                  {rel.commit_msg && <p className="text-xs text-muted-foreground mt-1.5 font-mono truncate">{rel.commit_msg}</p>}
                  {(rel.steps || []).length > 0 && (
                    <div className="flex items-center gap-1 mt-2 pt-2 border-t">
                      {(rel.steps || []).map((step, i) => {
                        const color = step.status === 'succeeded' ? 'bg-blue-500' : step.status === 'failed' ? 'bg-red-500' : step.status === 'running' ? 'bg-cyan-500 animate-pulse' : step.status === 'skipped' ? 'bg-muted' : 'bg-muted/30'
                        return <div key={step.id} className="flex items-center gap-1">
                          <div className={cn("w-1.5 h-1.5 rounded-full", color)} title={`${step.name}: ${step.status}`} />
                          {i < (rel.steps || []).length - 1 && <div className="w-4 h-px bg-muted" />}
                        </div>
                      })}
                      <span className="text-[10px] text-muted-foreground ml-2">{(rel.steps || []).filter(s => s.status === 'succeeded').length}/{(rel.steps || []).length} 步完成</span>
                    </div>
                  )}
                </CardContent>
              </Card>
            )
          })}
        </div>
      )}
    </div>
  )
}

function Field({ label, value, editing, formKey, form, setForm, mono, icon }: {
  label: string; value: string; editing: boolean; formKey: string; form: any; setForm: any; mono?: boolean; icon?: React.ReactNode
}) {
  return (
    <div>
      <label className="text-xs text-muted-foreground flex items-center gap-1">{icon}{label}</label>
      {editing ? (
        <Input className={cn("mt-1", mono && 'font-mono')} value={form[formKey] || ''} onChange={e => setForm((f: any) => ({ ...f, [formKey]: e.target.value }))} />
      ) : <p className={cn("text-sm mt-1", mono && 'font-mono', !value && 'text-muted-foreground/40')}>{value || '-'}</p>}
    </div>
  )
}
