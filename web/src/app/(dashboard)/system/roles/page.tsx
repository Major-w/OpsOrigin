
import { useEffect, useMemo, useState } from 'react'
import { useIAMStore, type Role, type Permission } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Button, Tag, Space, Popconfirm } from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, LockOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import { formatTime } from '@/lib/utils'
import { validateName, sanitizeName, NAME_RULE_TEXT } from '@/lib/validate'

export default function SystemRolesPage() {
  const { roles, permissions, fetchRoles, fetchPermissions, createRole, updateRole, deleteRole } = useIAMStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ name: '', display_name: '', description: '' })
  const [nameError, setNameError] = useState<string | null>(null)
  const [selectedPerms, setSelectedPerms] = useState<string[]>([])
  const [pendingRole, setPendingRole] = useState<Role | null>(null)

  useEffect(() => {
    fetchRoles()
    fetchPermissions()
  }, [])

  const permissionsByCategory = useMemo(() => {
    const map: Record<string, Permission[]> = {}
    for (const p of permissions) {
      if (!map[p.category]) map[p.category] = []
      map[p.category].push(p)
    }
    return map
  }, [permissions])

  const openCreate = () => {
    setEditId(null)
    setForm({ name: '', display_name: '', description: '' })
    setSelectedPerms([])
    setDialogOpen(true)
  }

  const openEdit = (role: Role) => {
    setEditId(role.id)
    setForm({ name: role.name, display_name: role.display_name, description: role.description || '' })
    setSelectedPerms([...(role.permissions || [])])
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!editId) {
      const err = validateName(form.name, '角色标识')
      if (err) { alert(err); return }
    }
    if (editId) {
      await updateRole(editId, {
        display_name: form.display_name,
        description: form.description,
        permissions: selectedPerms,
      })
    } else {
      await createRole({
        name: form.name,
        display_name: form.display_name || form.name,
        description: form.description,
        permissions: selectedPerms,
      })
    }
    setDialogOpen(false)
  }

  const handleDelete = async () => {
    if (!pendingRole || pendingRole.is_built_in) return
    await deleteRole(pendingRole.id)
    setPendingRole(null)
  }

  const togglePerm = (perm: string) => {
    setSelectedPerms(prev =>
      prev.includes(perm) ? prev.filter(p => p !== perm) : [...prev, perm]
    )
  }

  const permNameMap = useMemo(() => {
    const m: Record<string, Permission> = {}
    for (const p of permissions) m[p.name] = p
    return m
  }, [permissions])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">角色管理</h1>
          <p className="text-sm text-muted-foreground">管理系统角色,针对功能权限进行细粒度授权</p>
        </div>
        <Button size="middle" icon={<PlusOutlined />} onClick={openCreate}>添加角色</Button>
      </div>

      {/* 权限清单概览 */}
      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">平台权限点 ({permissions.length})</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-1.5">
            {permissions.map(p => (
              <Tag key={p.name} style={{ fontSize: 10 }} title={p.description}>
                <span className="text-muted-foreground">{p.category}</span>
                <span className="font-mono">{p.name}</span>
              </Tag>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* 角色卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {roles.map(role => (
          <Card key={role.id} className="hover:border-blue-500/30 transition-colors">
            <CardHeader className="py-3 pb-2">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 min-w-0">
                  <SafetyCertificateOutlined className={role.is_built_in ? 'text-amber-500' : 'text-blue-500'} />
                  <span className="text-sm font-medium truncate">{role.display_name || role.name}</span>
                  {role.is_built_in && (
                    <Tag style={{ fontSize: 10 }} className="shrink-0">
                      <LockOutlined />内置
                    </Tag>
                  )}
                </div>
                <Space size={2} className="shrink-0">
                  <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<EditOutlined />} onClick={() => openEdit(role)} title="编辑" />
                  {!role.is_built_in && (
                    <Popconfirm title="确定要删除此角色吗?" onConfirm={handleDelete} okText="确定" cancelText="取消">
                      <Button type="text" size="middle" danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" icon={<DeleteOutlined />} onClick={() => setPendingRole(role)} title="删除" />
                    </Popconfirm>
                  )}
                </Space>
              </div>
            </CardHeader>
            <CardContent className="space-y-2">
              <p className="text-xs text-muted-foreground">
                {role.description || '无描述'} · <span className="font-mono">{role.name}</span>
              </p>
              <div className="flex items-center justify-between text-xs">
                <span className="text-muted-foreground">权限数:</span>
                <span className="font-medium">{role.permissions?.length || 0}</span>
              </div>
              <div className="space-y-1">
                <div className="flex flex-wrap gap-1">
                  {(role.permissions || []).slice(0, 5).map(p => {
                    const meta = permNameMap[p]
                    return (
                      <Tag key={p} color="default" style={{ fontSize: 10 }} title={meta?.description || p}>
                        {p}
                      </Tag>
                    )
                  })}
                  {(role.permissions || []).length > 5 && (
                    <Tag color="default" style={{ fontSize: 10 }}>+{(role.permissions || []).length - 5}</Tag>
                  )}
                </div>
              </div>
              <p className="text-[10px] text-muted-foreground/60 pt-1 border-t">更新于 {formatTime(role.updated_at)}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 创建/编辑对话框 */}
      <Modal
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        title={editId ? '编辑角色' : '添加角色'}
        footer={
          <Space>
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={handleSave} disabled={!editId && !form.name}>{editId ? '保存' : '创建'}</Button>
          </Space>
        }
      >
        <div className="space-y-4 max-h-[70vh] overflow-y-auto">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">角色标识 *</label>
              <Input
                value={form.name}
                onChange={e => { const v = sanitizeName(e.target.value); setForm(f => ({ ...f, name: v })); setNameError(validateName(v, '角色标识')) }}
                placeholder="如 dba / sre"
                disabled={!!editId}
              />
              {editId ? <p className="text-[10px] text-muted-foreground mt-0.5">角色标识创建后不可修改</p> : nameError ? <p className="text-[11px] text-destructive mt-0.5">{nameError}</p> : <p className="text-[11px] text-muted-foreground mt-0.5">{NAME_RULE_TEXT}</p>}
            </div>
            <div>
              <label className="text-sm font-medium">展示名称</label>
              <Input
                value={form.display_name}
                onChange={e => setForm(f => ({ ...f, display_name: e.target.value }))}
                placeholder="如 数据库管理员"
              />
            </div>
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input
              value={form.description}
              onChange={e => setForm(f => ({ ...f, description: e.target.value }))}
              placeholder="角色职责说明"
            />
          </div>
          <div>
            <div className="flex items-center justify-between mb-2">
              <label className="text-sm font-medium">权限分配</label>
              <span className="text-xs text-muted-foreground">已选 {selectedPerms.length}/{permissions.length}</span>
            </div>
            <div className="space-y-3 border rounded-md p-3 max-h-80 overflow-y-auto">
              {Object.entries(permissionsByCategory).map(([category, perms]) => {
                const allSelected = perms.every(p => selectedPerms.includes(p.name))
                const someSelected = perms.some(p => selectedPerms.includes(p.name))
                return (
                  <div key={category}>
                    <div className="flex items-center gap-2 mb-1.5">
                      <input
                        type="checkbox"
                        checked={allSelected}
                        ref={el => { if (el) el.indeterminate = !allSelected && someSelected }}
                        onChange={e => {
                          if (e.target.checked) {
                            setSelectedPerms(prev => [...new Set([...prev, ...perms.map(p => p.name)])])
                          } else {
                            setSelectedPerms(prev => prev.filter(p => !perms.find(pp => pp.name === p)))
                          }
                        }}
                        className="rounded"
                      />
                      <span className="text-xs font-medium">{category}</span>
                      <span className="text-[10px] text-muted-foreground">({perms.length})</span>
                    </div>
                    <div className="grid grid-cols-2 gap-1 pl-5">
                      {perms.map(p => (
                        <label key={p.name} className="flex items-center gap-1.5 text-xs cursor-pointer py-0.5" title={p.description}>
                          <input
                            type="checkbox"
                            checked={selectedPerms.includes(p.name)}
                            onChange={() => togglePerm(p.name)}
                            className="rounded"
                          />
                          <span className="font-mono">{p.name}</span>
                        </label>
                      ))}
                    </div>
                  </div>
                )
              })}
            </div>
          </div>
        </div>
      </Modal>
    </div>
  )
}
