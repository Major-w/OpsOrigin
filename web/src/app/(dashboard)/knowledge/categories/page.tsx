
import { useEffect, useState } from 'react'
import { useKnowledgeStore, type Category } from '@/stores/knowledge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Popconfirm } from 'antd'
import { Plus, Trash2, Pencil, FolderPlus, GripVertical, Palette } from 'lucide-react'
import { cn } from '@/lib/utils'

const DEFAULT_COLOR = '#3B82F6'

export default function KnowledgeCategoriesPage() {
  const { categoryList, fetchCategories, saveCategory, updateCategory, deleteCategory } =
    useKnowledgeStore()

  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<Partial<Category>>({
    id: '',
    name: '',
    description: '',
    sort_order: 0,
    color: DEFAULT_COLOR,
  })
  const [searchQ, setSearchQ] = useState('')
  const [pendingDelete, setPendingDelete] = useState<Category | null>(null)

  useEffect(() => {
    fetchCategories()
  }, [fetchCategories])

  const isEditing = !!form.id

  const openCreate = () => {
    setForm({ id: '', name: '', description: '', sort_order: categoryList.length, color: DEFAULT_COLOR })
    setDialogOpen(true)
  }

  const openEdit = (cat: Category) => {
    setForm({ ...cat })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!form.name?.trim()) return
    const payload = {
      name: form.name!.trim(),
      description: form.description || '',
      sort_order: form.sort_order ?? 0,
      color: form.color || DEFAULT_COLOR,
    }
    if (isEditing && form.id) {
      await updateCategory(form.id, payload)
    } else {
      await saveCategory(payload)
    }
    setDialogOpen(false)
  }

  const handleDelete = async () => {
    if (!pendingDelete) return
    await deleteCategory(pendingDelete.id)
    setPendingDelete(null)
  }

  const sorted = [...categoryList]
    .sort((a, b) => (a.sort_order ?? 0) - (b.sort_order ?? 0))
    .filter(
      c =>
        !searchQ ||
        c.name.includes(searchQ) ||
        (c.description || '').includes(searchQ),
    )

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">知识分类管理</h1>
          <p className="text-xs text-muted-foreground">管理知识库分类结构，支持自定义颜色和排序</p>
        </div>
        <div className="flex gap-2">
          <Input
            className="w-48"
            placeholder="搜索分类..."
            value={searchQ}
            onChange={e => setSearchQ(e.target.value)}
          />
          <Button size="middle" onClick={openCreate}>
            <Plus className="h-4 w-4 mr-1" />
            新建分类
          </Button>
        </div>
      </div>

      {sorted.length === 0 ? (
        <Card>
          <CardContent className="py-12 text-center text-muted-foreground">
            <FolderPlus className="h-10 w-10 mx-auto mb-2 opacity-30" />
            <p>{searchQ ? '没有匹配的分类' : '暂无分类，点击上方按钮新建'}</p>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-3 md:grid-cols-2 lg:grid-cols-3">
          {sorted.map(cat => (
            <Card key={cat.id} className="group hover:border-primary/50 transition-colors">
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <GripVertical className="h-3.5 w-3.5 opacity-0 group-hover:opacity-40 transition-opacity cursor-grab shrink-0" />
                  <div
                    className="w-3 h-3 rounded-full shrink-0"
                    style={{ backgroundColor: cat.color || DEFAULT_COLOR }}
                  />
                  <span className="truncate flex-1">{cat.name}</span>
                  <Tag color="default" className="font-mono">
                    #{cat.sort_order ?? 0}
                  </Tag>
                </CardTitle>
              </CardHeader>
              <CardContent className="pt-0">
                <p className="text-xs text-muted-foreground line-clamp-2 mb-3">
                  {cat.description || '暂无描述'}
                </p>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <Palette className="h-3 w-3 text-muted-foreground" />
                    <div
                      className="w-4 h-4 rounded border border-border"
                      style={{ backgroundColor: cat.color || DEFAULT_COLOR }}
                    />
                    <span className="text-[10px] text-muted-foreground font-mono">
                      {cat.color || DEFAULT_COLOR}
                    </span>
                  </div>
                  <Tag>
                    0 篇文章
                  </Tag>
                </div>
                <div className="flex gap-1 mt-3 pt-3 border-t border-border/50 opacity-0 group-hover:opacity-100 transition-opacity">
                  <Button
                    type="text"
                    size="middle"
                    className="h-7 text-xs flex-1"
                    onClick={() => openEdit(cat)}
                  >
                    <Pencil className="h-3 w-3 mr-1" />
                    编辑
                  </Button>
                  <Popconfirm title={`删除分类「${cat.name}」？引用该分类的文章分类将被清空`} onConfirm={handleDelete}>
                    <Button
                      type="text"
                      size="middle"
                      className="h-7 text-xs text-red-400 hover:text-red-500"
                    >
                      <Trash2 className="h-3 w-3 mr-1" />
                      删除
                    </Button>
                  </Popconfirm>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Modal
        title={isEditing ? '编辑分类' : '新建分类'}
        open={dialogOpen}
        onCancel={() => setDialogOpen(false)}
        footer={<><Button type="default" onClick={() => setDialogOpen(false)}>取消</Button><Button onClick={handleSave} disabled={!form.name?.trim()}>{isEditing ? '保存修改' : '创建分类'}</Button></>}
      >
        <div className="space-y-4">
          <div>
            <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
              分类名称 <span className="text-red-500">*</span>
            </label>
            <Input
              placeholder="请输入分类名称"
              value={form.name || ''}
              onChange={e => setForm({ ...form, name: e.target.value })}
            />
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
              描述
            </label>
            <textarea
              className="flex min-h-[80px] w-full rounded-md border border-input bg-background px-3 py-2 text-sm resize-none"
              placeholder="分类描述（可选）"
              value={form.description || ''}
              onChange={e => setForm({ ...form, description: e.target.value })}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                排序权重
              </label>
              <Input
                type="number"
                min={0}
                value={form.sort_order ?? 0}
                onChange={e =>
                  setForm({ ...form, sort_order: parseInt(e.target.value) || 0 })
                }
              />
            </div>
            <div>
              <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                颜色
              </label>
              <div className="flex items-center gap-2">
                <input
                  type="color"
                  value={form.color || DEFAULT_COLOR}
                  onChange={e => setForm({ ...form, color: e.target.value })}
                  className="w-9 h-9 rounded cursor-pointer border-0 p-0"
                />
                <Input
                  placeholder="#3B82F6"
                  value={form.color || ''}
                  onChange={e => setForm({ ...form, color: e.target.value })}
                  className="flex-1 font-mono text-xs"
                />
              </div>
            </div>
          </div>
        </div>
      </Modal>
    </div>
  )
}
