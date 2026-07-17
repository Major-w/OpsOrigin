'use client'

import { useEffect, useMemo, useState } from 'react'
import { useIAMStore, type Role, type Permission } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Shield, Plus, Trash2, Edit, Lock } from 'lucide-react'
import { formatTime } from '@/lib/utils'

export default function SystemRolesPage() {
  const { roles, permissions, fetchRoles, fetchPermissions, createRole, updateRole, deleteRole } = useIAMStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ name: '', display_name: '', description: '' })
  const [selectedPerms, setSelectedPerms] = useState<string[]>([])

  useEffect(() => {
    fetchRoles()
    fetchPermissions()
  }, [])

  // 权限按 category 分组
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

  const handleDelete = async (role: Role) => {
    if (role.is_built_in) return
    if (!confirm(`确定删除角色「${role.display_name || role.name}」?`)) return
    await deleteRole(role.id)
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
        <Button size="sm" onClick={openCreate}><Plus className="h-4 w-4 mr-1" />添加角色</Button>
      </div>

      {/* 权限清单概览 */}
      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">平台权限点 ({permissions.length})</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex flex-wrap gap-1.5">
            {permissions.map(p => (
              <Badge key={p.name} variant="outline" className="text-[10px] gap-1" title={p.description}>
                <span className="text-muted-foreground">{p.category}</span>
                <span className="font-mono">{p.name}</span>
              </Badge>
            ))}
          </div>
        </CardContent>
      </Card>

      {/* 角色卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        {roles.map(role => (
          <Card key={role.id} className="hover:border-emerald-500/30 transition-colors">
            <CardHeader className="py-3 pb-2">
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2 min-w-0">
                  <Shield className={`h-4 w-4 shrink-0 ${role.is_built_in ? 'text-amber-500' : 'text-emerald-500'}`} />
                  <span className="text-sm font-medium truncate">{role.display_name || role.name}</span>
                  {role.is_built_in && (
                    <Badge variant="outline" className="text-[10px] shrink-0 gap-0.5">
                      <Lock className="h-2 w-2" />内置
                    </Badge>
                  )}
                </div>
                <div className="flex gap-0.5 shrink-0">
                  <Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => openEdit(role)} title="编辑">
                    <Edit className="h-3 w-3" />
                  </Button>
                  {!role.is_built_in && (
                    <Button variant="ghost" size="icon" className="h-6 w-6 text-destructive" onClick={() => handleDelete(role)} title="删除">
                      <Trash2 className="h-3 w-3" />
                    </Button>
                  )}
                </div>
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
                      <Badge key={p} variant="secondary" className="text-[10px]" title={meta?.description || p}>
                        {p}
                      </Badge>
                    )
                  })}
                  {(role.permissions || []).length > 5 && (
                    <Badge variant="secondary" className="text-[10px]">+{(role.permissions || []).length - 5}</Badge>
                  )}
                </div>
              </div>
              <p className="text-[10px] text-muted-foreground/60 pt-1 border-t">更新于 {formatTime(role.updated_at)}</p>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 创建/编辑对话框 */}
      <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)} title={editId ? '编辑角色' : '添加角色'}>
        <div className="space-y-4 max-h-[70vh] overflow-y-auto">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-sm font-medium">角色标识 *</label>
              <Input
                value={form.name}
                onChange={e => setForm(f => ({ ...f, name: e.target.value }))}
                placeholder="如 dba / sre"
                disabled={!!editId}
              />
              {editId && <p className="text-[10px] text-muted-foreground mt-0.5">角色标识创建后不可修改</p>}
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
        <DialogFooter>
          <Button variant="outline" onClick={() => setDialogOpen(false)}>取消</Button>
          <Button onClick={handleSave} disabled={!editId && !form.name}>{editId ? '保存' : '创建'}</Button>
        </DialogFooter>
      </Dialog>
    </div>
  )
}
