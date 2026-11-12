
import { useEffect, useMemo, useState } from 'react'
import { useIAMStore, type Department, type DeptImportResult } from '@/stores/iam'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Modal, Input, Select, Button, Table, Tag, Space, Popconfirm, Upload, Alert } from 'antd'
import { PlusOutlined, EditOutlined, DeleteOutlined, SearchOutlined, ApartmentOutlined, SyncOutlined, ImportOutlined, DownloadOutlined } from '@ant-design/icons'
import { useCan } from '@/stores/permissions'
import type { ColumnsType } from 'antd/es/table'
import type { UploadFile } from 'antd/es/upload/interface'

/** 部门节点来源标签 */
const SOURCE_TAG: Record<string, { color: string; label: string }> = {
  local: { color: 'blue', label: '本地' },
  ldap: { color: 'purple', label: '目录同步' },
}

type DeptRow = Department & { children?: DeptRow[] }

/** 平铺部门列表 → antd Table 树形行（children 递归；孤儿节点上提到根防凭空消失） */
function buildTreeRows(depts: Department[]): DeptRow[] {
  const byId = new Map(depts.map(d => [d.id, { ...d }] as [string, DeptRow]))
  const roots: DeptRow[] = []
  for (const node of byId.values()) {
    const parent = node.parent_id ? byId.get(node.parent_id) : undefined
    if (parent && parent.id !== node.id) {
      ;(parent.children ||= []).push(node)
    } else {
      roots.push(node)
    }
  }
  return roots
}

export default function SystemDepartmentsPage() {
  const { departments, fetchDepartments, createDepartment, updateDepartment, deleteDepartment, importDepartments } = useIAMStore()
  const canWrite = useCan('user:write')

  const [searchQ, setSearchQ] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState({ name: '', parent_id: '', description: '' })
  const [submitting, setSubmitting] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState<DeptImportResult | null>(null)

  useEffect(() => {
    fetchDepartments()
  }, [])

  /** CSV 上传（手动发请求，beforeUpload 拦截 antd 自动上传） */
  const handleImportUpload = async (file: UploadFile) => {
    if (!file.originFileObj) return false
    setImporting(true)
    try {
      const result = await importDepartments(file.originFileObj)
      if (result) setImportResult(result)
    } finally {
      setImporting(false)
    }
    return false // 阻断 antd 默认上传
  }

  /** 下载导入模板 */
  const downloadTemplate = () => {
    const csv = '\uFEFFpath,description\n总部/华东/门店A,示例：路径式部门链，/ 分层\n总部/华东/门店B,\n总部/华南,\n'
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'departments-template.csv'
    a.click()
    URL.revokeObjectURL(a.href)
  }

  // 过滤命中时保留祖先链（保证树形结构可展示）
  const filtered = useMemo(() => {
    const q = searchQ.trim().toLowerCase()
    if (!q) return departments
    const keep = new Set<string>()
    const byId = new Map(departments.map(d => [d.id, d]))
    for (const d of departments) {
      if (d.name.toLowerCase().includes(q) || (d.description || '').toLowerCase().includes(q)) {
        let cur: Department | undefined = d
        while (cur) {
          keep.add(cur.id)
          cur = cur.parent_id ? byId.get(cur.parent_id) : undefined
        }
      }
    }
    return departments.filter(d => keep.has(d.id))
  }, [departments, searchQ])

  const treeRows = useMemo(() => buildTreeRows(filtered), [filtered])

  const openCreate = (parent?: Department) => {
    setEditId(null)
    setForm({ name: '', parent_id: parent?.id || '', description: '' })
    setDialogOpen(true)
  }

  const openEdit = (dept: Department) => {
    setEditId(dept.id)
    setForm({ name: dept.name, parent_id: dept.parent_id || '', description: dept.description || '' })
    setDialogOpen(true)
  }

  const handleSave = async () => {
    if (!form.name.trim() || submitting) return
    setSubmitting(true)
    try {
      const data = { name: form.name.trim(), parent_id: form.parent_id || undefined, description: form.description.trim() || undefined }
      const ok = editId ? await updateDepartment(editId, data) : await createDepartment(data)
      if (ok) setDialogOpen(false)
    } finally {
      setSubmitting(false)
    }
  }

  const handleDelete = async (dept: Department) => {
    if (submitting) return
    setSubmitting(true)
    try {
      await deleteDepartment(dept.id)
    } finally {
      setSubmitting(false)
    }
  }

  // 上级候选：排除自己与自己的子孙（防环），ldap 来源也可作父级
  const parentCandidates = useMemo(() => {
    if (!editId) return departments
    const banned = new Set<string>([editId])
    let grew = true
    while (grew) {
      grew = false
      for (const d of departments) {
        if (d.parent_id && banned.has(d.parent_id) && !banned.has(d.id)) {
          banned.add(d.id)
          grew = true
        }
      }
    }
    return departments.filter(d => !banned.has(d.id))
  }, [departments, editId])

  const parentName = (id?: string) => (id ? departments.find(d => d.id === id)?.name || '-' : '-')

  const columns: ColumnsType<Department> = [
    {
      title: '部门名称',
      dataIndex: 'name',
      key: 'name',
      render: (text: string, record: Department) => (
        <span className="font-medium inline-flex items-center gap-2">
          <ApartmentOutlined className="text-muted-foreground" />
          {text}
          {record.source === 'ldap' && <Tag color="purple">目录</Tag>}
        </span>
      ),
    },
    {
      title: '上级部门',
      dataIndex: 'parent_id',
      key: 'parent_id',
      render: (id: string) => parentName(id),
    },
    {
      title: '成员数',
      dataIndex: 'member_count',
      key: 'member_count',
      width: 90,
      render: (n?: number) => (n ? <Tag color="blue">{n}</Tag> : <span className="text-muted-foreground">0</span>),
    },
    {
      title: '说明',
      dataIndex: 'description',
      key: 'description',
      render: (text?: string) => text || '-',
    },
    {
      title: '操作',
      key: 'actions',
      align: 'right',
      width: 160,
      render: (_: unknown, record: Department) => (
        <Space size="middle">
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<PlusOutlined />} onClick={() => openCreate(record)} title="添加子部门" disabled={!canWrite} />
          <Button type="text" size="middle" className="text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white" icon={<EditOutlined />} onClick={() => openEdit(record)} title="编辑" disabled={!canWrite} />
          <Popconfirm title="删除部门" description={`确定删除部门「${record.name}」？有子部门时会被拒绝。`} onConfirm={() => handleDelete(record)} okText="删除" cancelText="取消" disabled={!canWrite}>
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
          <h1 className="text-2xl font-bold">组织架构</h1>
          <p className="text-sm text-muted-foreground">部门树维护与 AD/LDAP 目录镜像；成员经用户「部门/门店」字段关联，供 ITSM 提单人属性派单等场景消费</p>
        </div>
        <Space>
          <Upload accept=".csv" maxCount={1} showUploadList={false} beforeUpload={(_, fileList) => handleImportUpload(fileList[0] as UploadFile)} disabled={!canWrite}>
            <Button size="middle" icon={<ImportOutlined />} loading={importing} disabled={!canWrite}>CSV 导入</Button>
          </Upload>
          <Button size="middle" type="text" icon={<DownloadOutlined />} onClick={downloadTemplate}>模板</Button>
          <Button size="middle" icon={<PlusOutlined />} onClick={() => openCreate()} disabled={!canWrite}>添加部门</Button>
        </Space>
      </div>

      <div className="flex gap-2">
        <div className="relative flex-1 max-w-xs">
          <Input prefix={<SearchOutlined />} placeholder="搜索部门/说明..." value={searchQ} onChange={e => setSearchQ(e.target.value)} allowClear />
        </div>
        <div className="flex items-center text-xs text-muted-foreground gap-1">
          <SyncOutlined /> AD 同步的用户部门值会在「触发同步」时自动归档到这里（标记为目录）
        </div>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">部门树 ({departments.length})</CardTitle>
        </CardHeader>
        <CardContent>
          <Table
            columns={columns}
            dataSource={treeRows}
            rowKey="id"
            size="middle"
            pagination={false}
            expandable={{ defaultExpandAllRows: true }}
            locale={{ emptyText: departments.length === 0 ? '暂无部门。可手工添加，或在系统管理触发 AD/LDAP 同步后自动归档目录中的部门。' : '无匹配结果' }}
          />
        </CardContent>
      </Card>

      {/* 创建/编辑部门 */}
      <Modal open={dialogOpen} onCancel={() => setDialogOpen(false)} title={editId ? '编辑部门' : '添加部门'} footer={null}>
        <div className="space-y-4">
          <div>
            <label className="text-sm font-medium">部门名称 *</label>
            <Input value={form.name} onChange={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="如 门店A / 华东区 / 门店A-IT" />
            <p className="text-xs text-muted-foreground mt-1">名称即用户「部门/门店」字段的匹配值（ITSM 决策表按名称联动），需与目录属性值保持一致</p>
          </div>
          <div>
            <label className="text-sm font-medium">上级部门</label>
            <Select className="w-full" allowClear placeholder="不选=根节点"
              value={form.parent_id || undefined}
              onChange={v => setForm(f => ({ ...f, parent_id: v || '' }))}
              options={parentCandidates.map(d => ({ value: d.id, label: d.name }))}
              showSearch optionFilterProp="label" />
          </div>
          <div>
            <label className="text-sm font-medium">说明</label>
            <Input value={form.description} onChange={e => setForm(f => ({ ...f, description: e.target.value }))} placeholder="部门用途说明（可选）" />
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4">
          <Button onClick={() => setDialogOpen(false)}>取消</Button>
          <Button type="primary" onClick={handleSave} loading={submitting} disabled={!canWrite || !form.name.trim()}>
            {editId ? '保存' : '创建'}
          </Button>
        </div>
      </Modal>

      {/* 导入结果 */}
      <Modal open={!!importResult} onCancel={() => setImportResult(null)} footer={<Button type="primary" onClick={() => setImportResult(null)}>知道了</Button>} title="部门导入结果" width={560}>
        {importResult && (
          <div className="space-y-3">
            <Alert
              type={importResult.failed > 0 ? 'warning' : 'success'}
              message={`新建 ${importResult.created} 个，复用 ${importResult.reused} 个，失败 ${importResult.failed} 行`}
              description={importResult.failed === 0 ? undefined : '失败行可修正 CSV 后重新导入，不影响已成功的部分'}
              showIcon
            />
            {(importResult.errors?.length ?? 0) > 0 && (
              <ul className="text-xs space-y-1 max-h-56 overflow-auto">
                {importResult.errors!.map(e => (
                  <li key={e.row} className="text-red-500">第 {e.row} 行：{e.message}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </Modal>

      {/* 删除确认走列表行内 Popconfirm（契约：ConfirmDialog 模式，禁原生 confirm） */}
    </div>
  )
}
