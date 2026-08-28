
import { useEffect, useState } from 'react'
import { useIAMStore, type Group } from '@/stores/iam'
import { useUserStore } from '@/stores/user'
import { Card, CardContent } from '@/components/ui/card'
import { Modal, Input, Button, Tag, Popconfirm } from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, SearchOutlined, UserAddOutlined } from '@ant-design/icons'
import { Users } from 'lucide-react'
import { formatTime } from '@/lib/utils'
import { validateName, sanitizeName, NAME_RULE_TEXT } from '@/lib/validate'

export default function SystemGroupsPage() {
  const { groups, fetchGroups, createGroup, updateGroup, deleteGroup } = useIAMStore()
  const { users, fetch: fetchUsers } = useUserStore()

  const [searchQ, setSearchQ] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ name: '', description: '', user_ids: [] as string[] })
  const [nameError, setNameError] = useState<string | null>(null)
  const [memberSearch, setMemberSearch] = useState('')

  useEffect(() => {
    fetchGroups()
    fetchUsers()
  }, [])

  const filtered = groups.filter(g =>
    g.name.toLowerCase().includes(searchQ.toLowerCase()) ||
    (g.description || '').toLowerCase().includes(searchQ.toLowerCase())
  )

  const openCreate = () => {
    setEditId(null)
    setForm({ name: '', description: '', user_ids: [] })
    setMemberSearch('')
    setDialogOpen(true)
  }

  const openEdit = (group: Group) => {
    setEditId(group.id)
    setForm({ name: group.name, description: group.description || '', user_ids: [...(group.user_ids || [])] })
    setMemberSearch('')
    setDialogOpen(true)
  }

  const handleSave = async () => {
    const err = validateName(form.name, '用户组名称')
    if (err) { alert(err); return }
    if (editId) {
      await updateGroup(editId, { name: form.name, description: form.description, user_ids: form.user_ids })
    } else {
      await createGroup({ name: form.name, description: form.description, user_ids: form.user_ids })
    }
    setDialogOpen(false)
  }

  const toggleMember = (userId: string) => {
    setForm(f => ({
      ...f,
      user_ids: f.user_ids.includes(userId)
        ? f.user_ids.filter(m => m !== userId)
        : [...f.user_ids, userId]
    }))
  }

  const removeMember = (userId: string) => {
    setForm(f => ({ ...f, user_ids: f.user_ids.filter(m => m !== userId) }))
  }

  // 用户 ID -> 用户对象映射
  const userMap = new Map(users.map(u => [u.id, u]))
  const selectedMembers = form.user_ids.map(id => userMap.get(id)).filter(Boolean)
  const availableUsers = users.filter(u =>
    u.username.toLowerCase().includes(memberSearch.toLowerCase()) &&
    !form.user_ids.includes(u.id)
  )

  // 组成员展示名
  const memberName = (uid: string) => userMap.get(uid)?.username || uid.slice(0, 8)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">用户组管理</h1>
          <p className="text-sm text-muted-foreground">管理用户分组,按集合管理用户</p>
        </div>
        <Button type="primary" size="middle" onClick={openCreate}><PlusOutlined className="mr-1" />新建用户组</Button>
      </div>

      <div className="flex gap-2">
        <div className="relative flex-1 max-w-xs">
          <SearchOutlined className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
          <Input className="pl-9 h-9 text-xs" placeholder="搜索用户组..." value={searchQ} onChange={e => setSearchQ(e.target.value)} />
        </div>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {filtered.length === 0 ? (
          <Card className="col-span-full"><CardContent className="py-12 text-center text-muted-foreground">
            <Users className="h-10 w-10 mx-auto mb-3 opacity-30" />
            <p>{groups.length === 0 ? '暂无用户组' : '无匹配结果'}</p>
          </CardContent></Card>
        ) : filtered.map(group => (
          <Card key={group.id} className="hover:border-blue-500/20 transition-colors">
            <CardContent className="p-4">
              <div className="flex items-start justify-between mb-3">
                <div className="flex items-center gap-2">
                  <div className="w-9 h-9 rounded-lg bg-blue-500/10 flex items-center justify-center">
                    <Users className="h-4 w-4 text-blue-500" />
                  </div>
                  <div>
                    <h3 className="text-sm font-semibold">{group.name}</h3>
                    <p className="text-xs text-muted-foreground">{group.description || '无描述'}</p>
                  </div>
                </div>
                <div className="flex gap-0.5">
                  <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<EditOutlined />} onClick={() => openEdit(group)} title="编辑" />
                  <Popconfirm
                    title="删除用户组"
                    description={`确定删除用户组「${group.name}」?`}
                    onConfirm={() => deleteGroup(group.id)}
                    okText="删除"
                    cancelText="取消"
                  >
                    <Button type="text" size="middle" icon={<DeleteOutlined />} danger className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" title="删除" />
                  </Popconfirm>
                </div>
              </div>

              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-muted-foreground">成员 ({group.user_ids?.length || 0})</span>
                </div>
                <div className="flex flex-wrap gap-1">
                  {(group.user_ids || []).length === 0 ? (
                    <span className="text-xs text-muted-foreground/50">暂无成员</span>
                  ) : (group.user_ids || []).slice(0, 8).map(uid => (
                    <Tag key={uid} className="text-[10px]">{memberName(uid)}</Tag>
                  ))}
                  {(group.user_ids || []).length > 8 && (
                    <Tag className="text-[10px]">+{(group.user_ids || []).length - 8}</Tag>
                  )}
                </div>
              </div>

              <div className="mt-3 pt-2 border-t">
                <span className="text-[10px] text-muted-foreground/60">创建于 {formatTime(group.created_at)}</span>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      {/* 新建/编辑对话框 */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={editId ? '编辑用户组' : '新建用户组'} footer={null} width={640}>
        <div className="space-y-4 max-h-[70vh] overflow-y-auto">
          <div>
            <label className="text-sm font-medium">组名称 *</label>
            <Input value={form.name} onChange={e => { const v = sanitizeName(e.target.value); setForm(f => ({ ...f, name: v })); setNameError(validateName(v, '用户组名称')) }} placeholder="ops_team 或 ops-team" />
            {nameError ? <p className="text-[11px] text-destructive mt-1">{nameError}</p> : <p className="text-[11px] text-muted-foreground mt-1">{NAME_RULE_TEXT}</p>}
          </div>
          <div>
            <label className="text-sm font-medium">描述</label>
            <Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} placeholder="用户组用途说明" />
          </div>

          <div>
            <label className="text-sm font-medium">成员管理</label>
            <div className="mt-2 space-y-2">
              {/* 已选成员 */}
              {selectedMembers.length > 0 && (
                <div className="flex flex-wrap gap-1 p-2 rounded-md bg-muted/30 border">
                  {selectedMembers.map(u => (
                    <Tag key={u!.id} closable onClose={() => removeMember(u!.id)} className="text-xs">
                      {u!.username}
                    </Tag>
                  ))}
                </div>
              )}

              {/* 添加成员搜索 */}
              <div className="relative">
                <UserAddOutlined className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
                <Input
                  className="pl-9 h-8 text-xs"
                  placeholder="搜索用户名添加..."
                  value={memberSearch}
                  onChange={e => setMemberSearch(e.target.value)}
                />
              </div>

              {memberSearch && availableUsers.length > 0 && (
                <div className="border rounded-md max-h-32 overflow-y-auto">
                  {availableUsers.map(u => (
                    <button
                      key={u.id}
                      className="w-full text-left px-3 py-1.5 text-xs hover:bg-muted/50 flex items-center gap-2"
                      onClick={() => { toggleMember(u.id); setMemberSearch('') }}
                    >
                      <div className="w-5 h-5 rounded-full bg-muted flex items-center justify-center text-[10px] font-medium">{u.username[0].toUpperCase()}</div>
                      <span>{u.username}</span>
                      {u.email && <span className="text-muted-foreground text-[10px]">{u.email}</span>}
                    </button>
                  ))}
                </div>
              )}
              {memberSearch && availableUsers.length === 0 && (
                <p className="text-xs text-muted-foreground text-center py-2">无匹配用户</p>
              )}

              {users.length === 0 && (
                <p className="text-xs text-muted-foreground bg-muted/30 rounded p-2">
                  暂无用户可选(后端未连接数据库时,用户列表为空)
                </p>
              )}
            </div>
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleSave} disabled={!form.name}>{editId ? '保存' : '创建'}</Button>
        </div>
      </Modal>

    </div>
  )
}
