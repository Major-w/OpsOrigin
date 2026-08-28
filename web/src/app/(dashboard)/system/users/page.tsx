
import { useEffect, useState } from 'react'
import { useUserStore, type User } from '@/stores/user'
import { useIAMStore, type Role } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Select, Button, Table, Tag, Space, Popconfirm } from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, SearchOutlined, SafetyOutlined } from '@ant-design/icons'
import { formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import type { ColumnsType } from 'antd/es/table'

export default function SystemUsersPage() {
  const { users, fetch, create, update, remove } = useUserStore()
  const { roles, fetchRoles, fetchUserRoles, assignUserRoles } = useIAMStore()
  const canWrite = useCan('user:write')

  const [searchQ, setSearchQ] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ username: '', email: '', role: 'viewer', status: 'active', password: '' })

  const [roleDialogUser, setRoleDialogUser] = useState<User | null>(null)
  const [assignedRoleIds, setAssignedRoleIds] = useState<string[]>([])
  const [pendingUser, setPendingUser] = useState<User | null>(null)

  useEffect(() => {
    fetch()
    fetchRoles()
  }, [])

  const filtered = users.filter(u =>
    u.username.toLowerCase().includes(searchQ.toLowerCase()) ||
    (u.email || '').toLowerCase().includes(searchQ.toLowerCase())
  )

  const openCreate = () => {
    setEditId(null)
    setForm({ username: '', email: '', role: 'viewer', status: 'active', password: '' })
    setDialogOpen(true)
  }

  const openEdit = (user: User) => {
    setEditId(user.id)
    setForm({ username: user.username, email: user.email || '', role: user.role, status: user.status, password: '' })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (editId) {
      await update(editId, {
        email: form.email,
        role: form.role,
        status: form.status,
      })
    } else {
      if (!form.password) {
        return
      }
      await create({
        username: form.username,
        password: form.password,
        email: form.email,
        role: form.role,
      })
    }
    setDialogOpen(false)
  }

  const handleDelete = async () => {
    if (!pendingUser) return
    await remove(pendingUser.id)
    setPendingUser(null)
  }

  const openRoleAssign = async (user: User) => {
    setRoleDialogUser(user)
    const ur = await fetchUserRoles(user.id)
    setAssignedRoleIds(ur?.role_ids || [])
  }

  const toggleRole = (roleId: string) => {
    setAssignedRoleIds(prev =>
      prev.includes(roleId) ? prev.filter(r => r !== roleId) : [...prev, roleId]
    )
  }

  const handleSaveRoles = async () => {
    if (!roleDialogUser) return
    const ok = await assignUserRoles(roleDialogUser.id, assignedRoleIds)
    if (ok) setRoleDialogUser(null)
  }

  const roleMap = new Map(roles.map(r => [r.id, r]))

  const columns: ColumnsType<User> = [
    {
      title: '用户名',
      dataIndex: 'username',
      key: 'username',
      render: (text: string) => <span style={{ fontWeight: 500 }}>{text}</span>,
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      key: 'email',
      render: (text: string) => text || '-',
    },
    {
      title: '主角色',
      dataIndex: 'role',
      key: 'role',
      render: (text: string) => <Tag>{text}</Tag>,
    },
    {
      title: '状态',
      dataIndex: 'status',
      key: 'status',
      render: (status: string) => (
        <Tag color={status === 'active' ? 'success' : 'default'}>
          {status === 'active' ? '启用' : '禁用'}
        </Tag>
      ),
    },
    {
      title: '最近登录',
      dataIndex: 'last_login',
      key: 'last_login',
      render: (text: string) => text ? formatTime(text) : '-',
    },
    {
      title: '操作',
      key: 'actions',
      align: 'right',
      render: (_: unknown, record: User) => (
        <Space size="middle">
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<SafetyOutlined />} onClick={() => openRoleAssign(record)} title="分配角色" disabled={!canWrite} />
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<EditOutlined />} onClick={() => openEdit(record)} title="编辑" disabled={!canWrite} />
          <Popconfirm title="删除用户" description={`确定删除用户「${record.username}」?`} onConfirm={() => { setPendingUser(record); handleDelete() }} okText="删除" cancelText="取消" disabled={!canWrite}>
            <Button type="text" size="middle" danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" icon={<DeleteOutlined />} title="删除" disabled={!canWrite} />
          </Popconfirm>
        </Space>
      ),
    },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">用户管理</h1>
          <p className="text-sm text-muted-foreground">管理系统用户、主角色与权限分配</p>
        </div>
        <Button size="middle" icon={<PlusOutlined />} onClick={openCreate} disabled={!canWrite}>添加用户</Button>
      </div>

      <div className="flex gap-2">
        <div className="relative flex-1 max-w-xs">
          <Input prefix={<SearchOutlined />} placeholder="搜索用户名/邮箱..." value={searchQ} onChange={e => setSearchQ(e.target.value)} />
        </div>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">用户列表 ({filtered.length})</CardTitle>
        </CardHeader>
        <CardContent>
          <Table columns={columns} dataSource={filtered} rowKey="id" size="middle" pagination={{ pageSize: 20 }} locale={{ emptyText: users.length === 0 ? '暂无用户(后端未连接数据库时仅登录态可用 admin/admin123)' : '无匹配结果' }} />
        </CardContent>
      </Card>

      {/* 创建/编辑用户 */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={editId ? '编辑用户' : '添加用户'} footer={null}>
        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium">用户名 *</label>
            <Input value={form.username} onChange={e => setForm(f => ({ ...f, username: e.target.value }))} disabled={!!editId} placeholder="用户名" />
          </div>
          {!editId && (
            <div>
              <label className="text-sm font-medium">密码 *</label>
              <Input.Password value={form.password} onChange={e => setForm(f => ({ ...f, password: e.target.value }))} placeholder="初始密码" />
            </div>
          )}
          <div>
            <label className="text-sm font-medium">邮箱</label>
            <Input value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} placeholder="user@example.com" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">主角色(登录态)</label>
              <Select
                value={form.role}
                onChange={v => setForm(f => ({ ...f, role: v }))}
                options={[
                  { value: 'admin', label: '管理员 (admin)' },
                  { value: 'operator', label: '操作员 (operator)' },
                  { value: 'viewer', label: '只读 (viewer)' },
                ]}
              />
            </div>
            <div>
              <label className="text-sm font-medium">状态</label>
              <Select
                value={form.status}
                onChange={v => setForm(f => ({ ...f, status: v }))}
                options={[
                  { value: 'active', label: '启用' },
                  { value: 'inactive', label: '禁用' },
                ]}
              />
            </div>
          </div>
          <p className="text-xs text-muted-foreground bg-muted/30 rounded p-2">
            主角色用于 JWT 鉴权与中间件权限校验。如需更细粒度授权,请使用「分配角色」按钮为用户附加 IAM 自定义角色。
          </p>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleSave} disabled={!canWrite || (!editId && (!form.username || !form.password))}>
            {editId ? '保存' : '创建'}
          </Button>
        </div>
      </Modal>

      {/* 角色分配 */}
      <Modal open={!!roleDialogUser} onCancel={() => setRoleDialogUser(null)} title={`分配角色 - ${roleDialogUser?.username || ''}`} footer={null}>
        <div className="space-y-3">
          <p className="text-sm text-muted-foreground">为该用户分配 IAM 角色(可多选),角色聚合的权限将决定该用户可访问的功能。</p>
          <div className="space-y-1.5 border rounded-md p-3 max-h-72 overflow-auto">
            {roles.length === 0 ? (
              <p className="text-sm text-muted-foreground text-center py-4">暂无可分配角色</p>
            ) : roles.map(role => (
              <label key={role.id} className="flex items-start gap-2 p-2 rounded hover:bg-muted/50 cursor-pointer">
                <input
                  type="checkbox"
                  checked={assignedRoleIds.includes(role.id)}
                  onChange={() => toggleRole(role.id)}
                  className="rounded mt-0.5"
                />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{role.display_name || role.name}</span>
                    <Tag>{role.name}</Tag>
                    {role.is_built_in && <Tag>内置</Tag>}
                  </div>
                  <p className="text-xs text-muted-foreground mt-0.5">{role.description || '无描述'}</p>
                  <p className="text-[10px] text-muted-foreground mt-0.5">{role.permissions?.length || 0} 个权限</p>
                </div>
              </label>
            ))}
          </div>
          {assignedRoleIds.length > 0 && (
            <div className="text-xs text-muted-foreground">
              已选 {assignedRoleIds.length} 个角色,
              聚合权限: {[...new Set(assignedRoleIds.flatMap(rid => roleMap.get(rid)?.permissions || []))].length} 个
            </div>
          )}
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setRoleDialogUser(null)}>取消</Button>
          <Button type="primary" onClick={handleSaveRoles} disabled={!canWrite}>保存分配</Button>
        </div>
      </Modal>
    </div>
  )
}
