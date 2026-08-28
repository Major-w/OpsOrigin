
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Popconfirm } from 'antd'
import { Database, Plus, Trash2, Pencil, Search } from 'lucide-react'
import { apiFetch, apiList } from '@/lib/utils'
import { message } from 'antd'
interface FieldDict {
  id: string
  key: string
  label: string
  options: { value: string; label: string }[]
  created_at: string
  updated_at: string
}

export default function FormDictsPage() {
  const [dicts, setDicts] = useState<FieldDict[]>([])
  const [loading, setLoading] = useState(false)
  const [keyword, setKeyword] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState({ key: '', label: '', options: '' })
  const [saving, setSaving] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)

  const load = async () => {
    setLoading(true)
    try {
      const data = await apiList<FieldDict>('/itsm/form-dicts')
      setDicts(data || [])
    } catch {
      message.error('加载数据字典失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { load() }, [])

  const filtered = dicts.filter(d =>
    !keyword || d.key.toLowerCase().includes(keyword.toLowerCase()) ||
    d.label.toLowerCase().includes(keyword.toLowerCase())
  )

  const openCreate = () => {
    setEditingId(null)
    setForm({ key: '', label: '', options: '' })
    setDialogOpen(true)
  }

  const openEdit = (dict: FieldDict) => {
    setEditingId(dict.id)
    setForm({
      key: dict.key,
      label: dict.label,
      options: dict.options.map(o => `${o.value}:${o.label}`).join('\n'),
    })
    setDialogOpen(true)
  }

  const save = async () => {
    if (!form.key.trim() || !form.label.trim()) {
      message.error('请填写字典键和标签')
      return
    }
    setSaving(true)
    try {
      const options = form.options.split('\n').filter(l => l.trim()).map(line => {
        const [value, label] = line.split(':')
        return { value: value?.trim() || '', label: label?.trim() || value?.trim() || '' }
      })
      await apiFetch('/itsm/form-dicts', {
        method: 'POST',
        body: JSON.stringify({ key: form.key, label: form.label, options }),
      })
      message.success(editingId ? '更新成功' : '创建成功')
      setDialogOpen(false)
      load()
    } catch (e: any) {
      message.error(`保存失败: ${e.message}`)
    } finally {
      setSaving(false)
    }
  }

  const remove = async (id: string) => {
    setDeleteTarget(id)
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await apiFetch(`/itsm/form-dicts/${deleteTarget}`, { method: 'DELETE' })
      message.success('删除成功')
      load()
    } catch (e: any) {
      message.error(`删除失败: ${e.message}`)
    }
    setDeleteTarget(null)
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">数据字典</h1>
          <p className="text-sm text-muted-foreground mt-1">管理表单下拉选项的预定义值</p>
        </div>
        <Button type="primary" onClick={openCreate}>
          <Plus className="w-4 h-4 mr-1" />
          新建字典
        </Button>
      </div>

      <div className="flex items-center gap-2">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
          <Input
            placeholder="搜索字典键或标签..."
            value={keyword}
            onChange={e => setKeyword(e.target.value)}
            className="pl-9"
          />
        </div>
        <Button type="default" onClick={load} disabled={loading}>
          刷新
        </Button>
      </div>

      {loading ? (
        <div className="text-center py-12 text-muted-foreground">加载中...</div>
      ) : filtered.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center">
            <Database className="w-12 h-12 mx-auto text-muted-foreground mb-4" />
            <p className="text-muted-foreground">暂无数据字典</p>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {filtered.map(dict => (
            <Card key={dict.id} className="hover:shadow-md transition-shadow">
              <CardHeader className="pb-2">
                <div className="flex items-start justify-between">
                  <div>
                    <CardTitle className="text-base">{dict.label}</CardTitle>
                    <p className="text-xs text-muted-foreground mt-1">键: {dict.key}</p>
                  </div>
                  <div className="flex gap-1">
                    <Button type="text" size="middle" onClick={() => openEdit(dict)}>
                      <Pencil className="w-3 h-3" />
                    </Button>
                    <Popconfirm title="确认删除" description="确定删除此数据字典？删除后不可恢复。" onConfirm={confirmDelete} okText="确认" cancelText="取消">
                      <Button type="text" size="middle" danger onClick={() => remove(dict.id)}>
                        <Trash2 className="w-3 h-3" />
                      </Button>
                    </Popconfirm>
                  </div>
                </div>
              </CardHeader>
              <CardContent>
                <div className="flex flex-wrap gap-1">
                  {dict.options.slice(0, 5).map((opt, i) => (
                    <Tag key={i} color="default" className="text-xs">
                      {opt.label}
                    </Tag>
                  ))}
                  {dict.options.length > 5 && (
                    <Tag color="default" className="text-xs">
                      +{dict.options.length - 5} 更多
                    </Tag>
                  )}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={editingId ? '编辑数据字典' : '新建数据字典'} footer={null}>
        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium">字典键</label>
            <Input
              value={form.key}
              onChange={e => setForm(f => ({ ...f, key: e.target.value }))}
              placeholder="如: priority, status"
              disabled={!!editingId}
            />
          </div>
          <div>
            <label className="text-sm font-medium">标签</label>
            <Input
              value={form.label}
              onChange={e => setForm(f => ({ ...f, label: e.target.value }))}
              placeholder="如: 优先级, 状态"
            />
          </div>
          <div>
            <label className="text-sm font-medium">选项（每行一个，格式: value:label）</label>
            <Input.TextArea
              rows={4}
              value={form.options}
              onChange={e => setForm(f => ({ ...f, options: e.target.value }))}
              placeholder={"low:低\nmedium:中\nhigh:高"}
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button onClick={() => setDialogOpen(false)}>取消</Button>
            <Button type="primary" onClick={save} disabled={saving}>
              {saving ? '保存中...' : '保存'}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  )
}
