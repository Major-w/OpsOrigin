
import { useEffect, useState } from 'react'
import { useUserStore, type User, type UserImportResult } from '@/stores/user'
import { useIAMStore, type Role } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Select, Button, Table, Tag, Space, Popconfirm, AutoComplete, Upload, Alert } from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, SearchOutlined, SafetyOutlined, ImportOutlined, DownloadOutlined } from '@ant-design/icons'
import { formatTime, apiFetch } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import type { ColumnsType } from 'antd/es/table'
import type { UploadFile } from 'antd/es/upload/interface'

interface PasswordPolicy {
  min_len: number
  min_classes: number
  max_age_days: number
}

export default function SystemUsersPage() {
  const { users, fetch, create, update, remove, importUsers } = useUserStore()
  const { roles, fetchRoles, fetchUserRoles, assignUserRoles, departments, fetchDepartments } = useIAMStore()
  const canWrite = useCan('user:write')

  const [searchQ, setSearchQ] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ username: '', email: '', department: '', role: 'viewer', status: 'active', password: '' })
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<UserImportResult | null>(null)
  const [pwdPolicy, setPwdPolicy] = useState<PasswordPolicy | null>(null)

  const [roleDialogUser, setRoleDialogUser] = useState<User | null>(null)
  const [assignedRoleIds, setAssignedRoleIds] = useState<string[]>([])
  const [pendingUser, setPendingUser] = useState<User | null>(null)

  useEffect(() => {
    fetch()
    fetchRoles()
    fetchDepartments()
    apiFetch<PasswordPolicy>('/users/password-policy').then(setPwdPolicy).catch(() => setPwdPolicy(null))
  }, [])

  const filtered = users.filter(u =>
    u.username.toLowerCase().includes(searchQ.toLowerCase()) ||
    (u.email || '').toLowerCase().includes(searchQ.toLowerCase())
  )

  const openCreate = () => {
    setEditId(null)
    setForm({ username: '', email: '', department: '', role: 'viewer', status: 'active', password: '' })
    setDialogOpen(true)
  }

  const openEdit = (user: User) => {
    setEditId(user.id)
    setForm({ username: user.username, email: user.email || '', department: user.department || '', role: user.role, status: user.status, password: '' })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (editId) {
      await update(editId, {
        email: form.email,
        department: form.department,
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
        department: form.department,
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

  /** CSV 上传（手动发请求，beforeUpload 拦截 antd 自动上传） */
  const handleImportUpload = async (file: UploadFile) => {
    if (!file.originFileObj) return false
    setImporting(true)
    try {
      const result = await importUsers(file.originFileObj)
      if (result) setImportResult(result)
    } finally {
      setImporting(false)
    }
    return false // 阻断 antd 默认上传
  }

  /** 下载导入模板 */
  const downloadTemplate = () => {
    const csv = '\uFEFFusername,email,department,role,status,password\nzhangsan,zhang@example.com,门店A,operator,active,\nlisi,,门店B,viewer,,\n'
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'users-template.csv'
    a.click()
    URL.revokeObjectURL(a.href)
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
      render: (text: string) => <span className="font-medium">{text}</span>,
    },
    {
      title: '邮箱',
      dataIndex: 'email',
      key: 'email',
      render: (text: string) => text || '-',
    },
    {
      title: '部门',
      dataIndex: 'department',
      key: 'department',
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
        <Space>
          <Upload accept=".csv" maxCount={1} showUploadList={false} beforeUpload={(_, fileList) => handleImportUpload(fileList[0] as UploadFile)} disabled={!canWrite}>
            <Button size="middle" icon={<ImportOutlined />} loading={importing} disabled={!canWrite}>CSV 导入</Button>
          </Upload>
          <Button size="middle" type="text" icon={<DownloadOutlined />} onClick={downloadTemplate}>模板</Button>
          <Button size="middle" icon={<PlusOutlined />} onClick={openCreate} disabled={!canWrite}>添加用户</Button>
        </Space>
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
              <p className="text-xs text-muted-foreground mt-1">
                策略要求：至少 {pwdPolicy?.min_len ?? 8} 位、{pwdPolicy?.min_classes ?? 2} 类字符（小写/大写/数字/符号）
              </p>
            </div>
          )}
          <div>
            <label className="text-sm font-medium">邮箱</label>
            <Input value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} placeholder="user@example.com" />
          </div>
          <div>
            <label className="text-sm font-medium">部门/门店</label>
            <AutoComplete
              className="w-full"
              value={form.department}
              onChange={v => setForm(f => ({ ...f, department: v }))}
              options={departments.map(d => ({ value: d.name }))}
              placeholder="如 门店A（组织架构候选 + 自由输入；ITSM 提单人属性派单用，LDAP 可自动同步）"
            />
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

      {/* 导入结果（初始密码仅本次展示，提示管理员分发） */}
      <Modal open={!!importResult} onCancel={() => setImportResult(null)} footer={<Button type="primary" onClick={() => setImportResult(null)}>知道了</Button>} title="用户导入结果" width={640}>
        {importResult && (
          <div className="space-y-3">
            <Alert
              type={importResult.failed > 0 ? 'warning' : 'success'}
              message={`新建 ${importResult.created} 个，跳过 ${importResult.skipped} 个（已存在），失败 ${importResult.failed} 行`}
              showIcon
            />
            {(importResult.accounts?.length ?? 0) > 0 && (
              <div>
                <p className="text-xs font-medium mb-1 text-orange-500">自动生成的初始密码（仅本次显示，请及时分发给用户）：</p>
                <ul className="text-xs space-y-1 max-h-48 overflow-auto border rounded p-2">
                  {importResult.accounts!.filter(a => a.initial_password).map(a => (
                    <li key={a.username} className="font-mono">{a.username}: <span className="font-bold">{a.initial_password}</span></li>
                  ))}
                </ul>
              </div>
            )}
            {(importResult.errors?.length ?? 0) > 0 && (
              <ul className="text-xs space-y-1 max-h-40 overflow-auto">
                {importResult.errors!.map(e => (
                  <li key={e.row} className="text-red-500">第 {e.row} 行：{e.message}</li>
                ))}
              </ul>
            )}
          </div>
        )}
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
