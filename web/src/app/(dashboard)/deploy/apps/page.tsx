
import { useEffect, useState, useMemo, useRef } from 'react'
import { useNavigate } from 'react-router-dom'
import { useDeployStore, type DeployApp, type AppType, type DeployEnv } from '@/stores/deploy'
import { ProTable, type ProColumns, type ActionType } from '@ant-design/pro-components'
import { StatisticCardGroup, StatisticCard } from '@/components/ui/statistic-card'
import { Button, Modal, Input, Select, Tag, Popconfirm, message } from 'antd'
import { AppWindow, Plus, Trash2, Pencil, GitBranch, Code2, Server, Box, Container, FileType, Power, FileText } from 'lucide-react'
import { cn, formatTime } from '@/lib/utils'
import { getMessage } from '@/lib/message'

const APP_TYPES: { value: AppType; label: string; icon: any; color: string }[] = [
  { value: 'java', label: 'Java', icon: Box, color: 'text-orange-500' },
  { value: 'go', label: 'Go', icon: Server, color: 'text-cyan-500' },
  { value: 'python', label: 'Python', icon: Code2, color: 'text-blue-500' },
  { value: 'nodejs', label: 'Node.js', icon: FileType, color: 'text-blue-500' },
  { value: 'static', label: '静态', icon: FileText, color: 'text-slate-500' },
  { value: 'container', label: '容器', icon: Container, color: 'text-purple-500' },
]

const TYPE_BADGE: Record<string, string> = {
  java: 'bg-orange-500/10 text-orange-600 dark:text-orange-400 border-orange-500/20',
  go: 'bg-cyan-500/10 text-cyan-600 dark:text-cyan-400 border-cyan-500/20',
  python: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  nodejs: 'bg-blue-500/10 text-blue-600 dark:text-blue-400 border-blue-500/20',
  static: 'bg-slate-500/10 text-slate-600 dark:text-slate-400 border-slate-500/20',
  container: 'bg-purple-500/10 text-purple-600 dark:text-purple-400 border-purple-500/20',
}

const ENV_LABELS: Record<string, string> = { dev: '开发', test: '测试', staging: '预发', prod: '生产' }
const ENV_COLORS: Record<string, string> = {
  dev: 'bg-blue-500/10 text-blue-600 border-blue-500/20',
  test: 'bg-amber-500/10 text-amber-600 border-amber-500/20',
  staging: 'bg-purple-500/10 text-purple-600 border-purple-500/20',
  prod: 'bg-red-500/10 text-red-600 border-red-500/20',
}

export default function AppsPage() {
  const navigate = useNavigate()
  const { apps, loading, fetchApps, deleteApp, createApp } = useDeployStore()
  const actionRef = useRef<ActionType>()
  const [confirmDelete, setConfirmDelete] = useState<DeployApp | null>(null)
  const [createOpen, setCreateOpen] = useState(false)
  const [form, setForm] = useState({ name: '', code: '', type: 'java' as AppType, repo_url: '', branch: 'main', build_cmd: '', description: '', owner: '', envs: ['dev'] as DeployEnv[] })
  const [selectedRowKeys, setSelectedRowKeys] = useState<React.Key[]>([])

  useEffect(() => { fetchApps() }, [])

  const stats = useMemo(() => ({
    total: apps.length,
    enabled: apps.filter(a => a.status === 'enabled').length,
    disabled: apps.filter(a => a.status === 'disabled').length,
    java: apps.filter(a => a.type === 'java').length,
    prod: apps.filter(a => a.current_versions.prod).length,
  }), [apps])

  const typeMeta = (t: string) => APP_TYPES.find(at => at.value === t)

  const toggleEnv = (env: DeployEnv) => {
    setForm(f => ({ ...f, envs: f.envs.includes(env) ? f.envs.filter(e => e !== env) : [...f.envs, env] }))
  }

  const handleCreate = async () => {
    if (!form.name.trim() || !form.code.trim()) { getMessage().error('应用名称和代码不能为空'); return }
    const id = await createApp(form)
    if (id) { setCreateOpen(false); setForm({ name: '', code: '', type: 'java', repo_url: '', branch: 'main', build_cmd: '', description: '', owner: '', envs: ['dev'] as DeployEnv[] }) }
  }

  const handleConfirmDelete = async () => {
    if (!confirmDelete) return
    const ok = await deleteApp(confirmDelete.id)
    if (ok) setConfirmDelete(null)
  }

  const columns: ProColumns<DeployApp>[] = [
    {
      title: '应用名称',
      dataIndex: 'name',
      fixed: 'left',
      width: 240,
      render: (_, app) => {
        const tm = typeMeta(app.type)
        const Icon = tm?.icon || AppWindow
        return (
          <div className="flex items-center gap-2 min-w-0">
            <Icon className={cn('h-4 w-4 shrink-0', tm?.color)} />
            <span className="text-sm font-medium truncate cursor-pointer hover:text-blue-500"
              onClick={(e) => { e.stopPropagation(); navigate(`/deploy/apps/${app.id}`) }}>
              {app.name}
            </span>
            <span className="text-[10px] text-muted-foreground/60 font-mono">{app.code}</span>
          </div>
        )
      },
      fieldProps: { placeholder: '搜索应用名称/代码' },
    },
    {
      title: '类型',
      dataIndex: 'type',
      width: 100,
      filters: APP_TYPES.map(t => ({ text: t.label, value: t.value })),
      onFilter: (value, record) => record.type === value,
      render: (_, app) => {
        const tm = typeMeta(app.type)
        return <Tag className={cn("text-[10px] px-1.5 py-0", TYPE_BADGE[app.type])}>{tm?.label || app.type}</Tag>
      },
    },
    {
      title: '环境',
      dataIndex: 'envs',
      width: 160,
      search: false,
      render: (_, app) => (
        <div className="flex gap-1">
          {app.envs.map(env => (
            <span key={env} className={cn("text-[10px] px-1.5 py-0.5 rounded border", ENV_COLORS[env])} title={ENV_LABELS[env]}>{ENV_LABELS[env]}</span>
          ))}
        </div>
      ),
    },
    {
      title: '生产版本',
      dataIndex: 'current_versions',
      width: 100,
      search: false,
      render: (_, app) => (
        <span className="text-xs font-mono text-muted-foreground">
          {app.current_versions.prod ? app.current_versions.prod : <span className="text-muted-foreground/40">-</span>}
        </span>
      ),
    },
    {
      title: '负责人',
      dataIndex: 'owner',
      width: 80,
      render: (_, app) => <span className="text-xs text-muted-foreground">{app.owner || '-'}</span>,
    },
    {
      title: '更新时间',
      dataIndex: 'updated_at',
      width: 160,
      search: false,
      sorter: (a, b) => new Date(a.updated_at).getTime() - new Date(b.updated_at).getTime(),
      render: (_, app) => <span className="text-xs text-muted-foreground/70 hidden md:block">{formatTime(app.updated_at)}</span>,
    },
    {
      title: '操作',
      valueType: 'option',
      fixed: 'right',
      width: 80,
      render: (_, app) => [
        <button
          key="edit"
          className="h-6 w-6 inline-flex items-center justify-center rounded text-gray-500 hover:text-gray-900 hover:bg-gray-100 dark:text-gray-400 dark:hover:text-white dark:hover:bg-white/5 cursor-pointer"
          onClick={(e) => { e.stopPropagation(); navigate(`/deploy/apps/${app.id}`) }}
          title="编辑"
        >
          <Pencil className="h-3 w-3" />
        </button>,
        <Popconfirm key="delete" title="确认删除该应用？" onConfirm={() => deleteApp(app.id)}>
          <button
            className="h-6 w-6 inline-flex items-center justify-center rounded text-red-400 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-500/10 cursor-pointer"
            onClick={(e) => e.stopPropagation()}
            title="删除"
          >
            <Trash2 className="h-3 w-3" />
          </button>
        </Popconfirm>,
      ],
    },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">应用管理</h1>
          <p className="text-sm text-muted-foreground">管理应用版本发布配置、构建脚本与环境策略</p>
        </div>
      </div>

      <StatisticCardGroup loading={loading} columns={5}>
        <StatisticCard title="应用总数" value={stats.total} icon={<AppWindow className="h-3 w-3" />} />
        <StatisticCard title="启用" value={stats.enabled} icon={<Power className="h-3 w-3" />} className="text-blue-500" />
        <StatisticCard title="Java" value={stats.java} icon={<Box className="h-3 w-3" />} className="text-orange-500" />
        <StatisticCard title="已上线生产" value={stats.prod} icon={<GitBranch className="h-3 w-3" />} className="text-red-500" />
        <StatisticCard title="已禁用" value={stats.disabled} icon={<Power className="h-3 w-3" />} className="text-muted-foreground" />
      </StatisticCardGroup>

      <ProTable<DeployApp>
        actionRef={actionRef}
        columns={columns}
        dataSource={apps}
        loading={loading}
        rowKey="id"
        search={{ labelWidth: 'auto', defaultCollapsed: false }}
        pagination={{ defaultPageSize: 20, showSizeChanger: true, showTotal: (total) => `共 ${total} 条` }}
        rowSelection={{ selectedRowKeys, onChange: setSelectedRowKeys }}
        toolBarRender={() => [
          selectedRowKeys.length > 0 && (
            <Popconfirm
              key="bulk-delete"
              title={`确认删除选中的 ${selectedRowKeys.length} 个应用？`}
              onConfirm={async () => {
                for (const key of selectedRowKeys) { await deleteApp(key as string) }
                setSelectedRowKeys([])
              }}
              okText="确认"
              cancelText="取消"
            >
              <Button danger size="middle">
                <Trash2 className="h-3.5 w-3.5 mr-1" />批量删除 ({selectedRowKeys.length})
              </Button>
            </Popconfirm>
          ),
          <Button key="new" type="primary" size="middle" onClick={() => setCreateOpen(true)}>
            <Plus className="h-3.5 w-3.5 mr-1" />新建应用
          </Button>,
        ].filter(Boolean)}
        options={{ density: true, fullScreen: true, reload: () => fetchApps(), setting: true }}
        headerTitle={false}
      />

      <Modal title="新建应用" open={createOpen} onCancel={() => setCreateOpen(false)} footer={null}>
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-xs font-medium">应用名称 *</label><Input className="mt-1" value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="用户中心服务" /></div>
            <div><label className="text-xs font-medium">应用代码 *</label><Input className="mt-1 font-mono" value={form.code} onChange={e => setForm(f => ({ ...f, code: e.target.value }))} placeholder="user-center" /></div>
            <div>
              <label className="text-xs font-medium">类型</label>
              <Select className="mt-1 w-full" value={form.type} onChange={v => setForm(f => ({ ...f, type: v as AppType }))} options={APP_TYPES.map(t => ({ label: t.label, value: t.value }))} />
            </div>
            <div><label className="text-xs font-medium">负责人</label><Input className="mt-1" value={form.owner} onChange={e => setForm(f => ({ ...f, owner: e.target.value }))} placeholder="zhangsan" /></div>
            <div className="col-span-2"><label className="text-xs font-medium">代码仓库 URL</label><Input className="mt-1 font-mono" value={form.repo_url} onChange={e => setForm(f => ({ ...f, repo_url: e.target.value }))} placeholder="https://git.example.com/ops/app.git" /></div>
            <div><label className="text-xs font-medium">分支</label><Input className="mt-1 font-mono" value={form.branch} onChange={e => setForm(f => ({ ...f, branch: e.target.value }))} /></div>
            <div><label className="text-xs font-medium">构建命令</label><Input className="mt-1 font-mono" value={form.build_cmd} onChange={e => setForm(f => ({ ...f, build_cmd: e.target.value }))} placeholder="mvn clean package" /></div>
            <div className="col-span-2">
              <label className="text-xs font-medium">发布环境</label>
              <div className="mt-1 flex gap-2">
                {(['dev', 'test', 'staging', 'prod'] as DeployEnv[]).map(env => (
                  <button key={env} type="button" onClick={() => toggleEnv(env)}
                    className={cn("px-3 py-1 rounded-md text-xs border transition-colors",
                      form.envs.includes(env) ? ENV_COLORS[env] : 'border-input text-muted-foreground hover:bg-muted/50')}>
                    {ENV_LABELS[env]}
                  </button>
                ))}
              </div>
            </div>
            <div className="col-span-2"><label className="text-xs font-medium">描述</label><Input.TextArea className="mt-1" rows={2} value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} /></div>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button onClick={() => setCreateOpen(false)}>取消</Button>
            <Button type="primary" onClick={handleCreate} disabled={!form.name.trim() || !form.code.trim()}><Plus className="h-3.5 w-3.5 mr-1" />创建</Button>
          </div>
        </div>
      </Modal>

      <Modal title="确认删除应用" open={!!confirmDelete} onCancel={() => setConfirmDelete(null)} footer={null}>
        {confirmDelete && (
          <div className="space-y-3">
            <p className="text-sm text-muted-foreground">确认删除应用 <span className="font-medium text-foreground">{confirmDelete.name}</span>（{confirmDelete.code}）?</p>
            <p className="text-xs text-muted-foreground/70">关联的发布单将保留但无法再发布新版本。</p>
            <div className="flex justify-end gap-2 pt-2">
              <Button onClick={() => setConfirmDelete(null)}>取消</Button>
              <Button danger size="middle" onClick={handleConfirmDelete}><Trash2 className="h-3.5 w-3.5 mr-1" />确认删除</Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
