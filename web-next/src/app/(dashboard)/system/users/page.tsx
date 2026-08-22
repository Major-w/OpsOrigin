'use client'

import { useEffect, useState } from 'react'
import { useUserStore, type User } from '@/stores/user'
import { useIAMStore, type Role } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Select } from '@/components/ui/select'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Plus, Trash2, Edit, Search, Shield } from 'lucide-react'
import { formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'

export default function SystemUsersPage() {
  const { users, fetch, create, update, remove } = useUserStore()
  const { roles, fetchRoles, fetchUserRoles, assignUserRoles } = useIAMStore()
  const canWrite = useCan('user:write')

  const [searchQ, setSearchQ] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ username: '', email: '', role: 'viewer', status: 'active', password: '' })

  // 角色分配
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

  // 用户名 -> 角色展示缓存(简单:用 assignedRoleIds 配合)
  const roleMap = new Map(roles.map(r => [r.id, r]))

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">用户管理</h1>
          <p className="text-sm text-muted-foreground">管理系统用户、主角色与权限分配</p>
        </div>
        <Button size="sm" onClick={openCreate} disabled={!canWrite}><Plus className="h-4 w-4 mr-1" />添加用户</Button>
      </div>

      <div className="flex gap-2">
        <div className="relative flex-1 max-w-xs">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input className="pl-9 h-9 text-xs" placeholder="搜索用户名/邮箱..." value={searchQ} onChange={e => setSearchQ(e.target.value)} />
        </div>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">用户列表 ({filtered.length})</CardTitle>
        </CardHeader>
        <CardContent>
          {filtered.length === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              {users.length === 0 ? '暂无用户(后端未连接数据库时仅登录态可用 admin/admin123)' : '无匹配结果'}
            </p>
          ) : (
            <div className="border rounded-md overflow-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b bg-muted/50">
                    <th className="text-left p-3 font-medium">用户名</th>
                    <th className="text-left p-3 font-medium">邮箱</th>
                    <th className="text-left p-3 font-medium">主角色</th>
                    <th className="text-left p-3 font-medium">状态</th>
                    <th className="text-left p-3 font-medium">最近登录</th>
                    <th className="text-right p-3 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(u => (
                    <tr key={u.id} className="border-b last:border-0 hover:bg-muted/30">
                      <td className="p-3 font-medium">{u.username}</td>
                      <td className="p-3 text-muted-foreground">{u.email || '-'}</td>
                      <td className="p-3"><Badge variant="outline">{u.role}</Badge></td>
                      <td className="p-3">
                        <Badge variant={u.status === 'active' ? 'success' : 'secondary'}>
                          {u.status === 'active' ? '启用' : '禁用'}
                        </Badge>
                      </td>
                      <td className="p-3 text-muted-foreground text-xs">{u.last_login ? formatTime(u.last_login) : '-'}</td>
                      <td className="p-3 text-right">
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => openRoleAssign(u)} title="分配角色" disabled={!canWrite}>
                          <Shield className="h-3 w-3" />
                        </Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={() => openEdit(u)} title="编辑" disabled={!canWrite}>
                          <Edit className="h-3 w-3" />
                        </Button>
                        <Button variant="ghost" size="icon" className="h-7 w-7 text-destructive" onClick={() => setPendingUser(u)} title="删除" disabled={!canWrite}>
                          <Trash2 className="h-3 w-3" />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 创建/编辑用户 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editId ? '编辑用户' : '添加用户'}>
        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium">用户名 *</label>
            <Input value={form.username} onChange={e => setForm(f => ({ ...f, username: e.target.value }))} disabled={!!editId} placeholder="用户名" />
          </div>
          {!editId && (
            <div>
              <label className="text-sm font-medium">密码 *</label>
              <Input type="password" value={form.password} onChange={e => setForm(f => ({ ...f, password: e.target.value }))} placeholder="初始密码" />
            </div>
          )}
          <div>
            <label className="text-sm font-medium">邮箱</label>
            <Input type="email" value={form.email} onChange={e => setForm(f => ({ ...f, email: e.target.value }))} placeholder="user@example.com" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">主角色(登录态)</label>
              <Select
                value={form.role}
                onChange={e => setForm(f => ({ ...f, role: e.target.value }))}
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
                onChange={e => setForm(f => ({ ...f, status: e.target.value }))}
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleSave} disabled={!canWrite || (!editId && (!form.username || !form.password))}>
            {editId ? '保存' : '创建'}
          </Button>
        </DialogFooter>
      </Dialog>

      {/* 角色分配 */}
      <Dialog open={!!roleDialogUser} onClose={() => setRoleDialogUser(null)} title={`分配角色 - ${roleDialogUser?.username || ''}`}>
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
                    <Badge variant="outline" className="text-[10px]">{role.name}</Badge>
                    {role.is_built_in && <Badge variant="secondary" className="text-[10px]">内置</Badge>}
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setRoleDialogUser(null)}>取消</Button>
          <Button onClick={handleSaveRoles} disabled={!canWrite}>保存分配</Button>
        </DialogFooter>
      </Dialog>

      {/* 删除用户确认 */}
      <ConfirmDialog
        open={!!pendingUser}
        title="删除用户"
        description={`确定删除用户「${pendingUser?.username}」?`}
        confirmLabel="删除"
        onClose={() => setPendingUser(null)}
        onConfirm={handleDelete}
      />
    </div>
  )
}
