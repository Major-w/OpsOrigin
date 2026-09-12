
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Select, Table, Popconfirm, Empty } from 'antd'
import { apiFetch, apiList, formatTime } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { getMessage } from '@/lib/message'
import { Camera, GitCompare, Trash2, Eye, RefreshCw, AlertTriangle } from 'lucide-react'

interface SnapshotCI {
  id: string
  name: string
  type_id: string
  status: string
  attrs: Record<string, any>
}
interface SnapshotRel {
  source_id: string
  target_id: string
  relation_type: string
}
interface Snapshot {
  id: string
  tenant_id: string
  name: string
  ci_count: number
  rel_count: number
  data: { cis: SnapshotCI[]; rels: SnapshotRel[] }
  created_at: string
}

interface DriftField { field: string; old: any; new: any }
interface DriftReport {
  base_name: string
  target_name: string
  target_is_live: boolean
  ci_added: SnapshotCI[]
  ci_removed: SnapshotCI[]
  ci_changed: { id: string; name: string; type_id: string; fields: DriftField[] }[]
  rel_added: SnapshotRel[]
  rel_removed: SnapshotRel[]
  summary: { ci_added: number; ci_removed: number; ci_changed: number; rel_added: number; rel_removed: number }
}

const RELATION_LABELS: Record<string, string> = {
  depends_on: '依赖于', connects_to: '连接到', contains: '包含', backup_of: '备份',
  deployed_on: '部署于', calls: '调用', belongs_to: '隶属于', part_of: '组成部分',
}
const relLabel = (k: string) => RELATION_LABELS[k] || k

export default function CMDBSnapshotsPage() {
  const canRead = useCan('cmdb:read')
  const canWrite = useCan('cmdb:write')
  const [snapshots, setSnapshots] = useState<Snapshot[]>([])
  const [loading, setLoading] = useState(true)

  // 捕获
  const [captureOpen, setCaptureOpen] = useState(false)
  const [captureName, setCaptureName] = useState('')
  const [capturing, setCapturing] = useState(false)

  // 对比
  const [compareBase, setCompareBase] = useState<Snapshot | null>(null)
  const [compareTarget, setCompareTarget] = useState<string>('') // snapshot id 或 '__live__'
  const [comparing, setComparing] = useState(false)
  const [report, setReport] = useState<DriftReport | null>(null)

  // 查看明细
  const [viewSnap, setViewSnap] = useState<Snapshot | null>(null)

  const load = async () => {
    setLoading(true)
    try {
      const data = await apiFetch<{ snapshots: Snapshot[] }>('/cmdb/snapshots')
      setSnapshots(data.snapshots || [])
    } catch (e: any) {
      getMessage().error('加载快照失败: ' + e.message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { if (canRead) load() }, [canRead])

  const doCapture = async () => {
    setCapturing(true)
    try {
      await apiFetch('/cmdb/snapshots' + (captureName ? '?name=' + encodeURIComponent(captureName) : ''), { method: 'POST' })
      getMessage().success('已捕获基线快照')
      setCaptureOpen(false)
      setCaptureName('')
      await load()
    } catch (e: any) {
      getMessage().error('捕获失败: ' + e.message)
    } finally {
      setCapturing(false)
    }
  }

  const doDelete = async (id: string) => {
    try {
      await apiFetch('/cmdb/snapshots/' + id, { method: 'DELETE' })
      getMessage().success('已删除快照')
      await load()
    } catch (e: any) {
      getMessage().error('删除失败: ' + e.message)
    }
  }

  const openCompare = (base: Snapshot) => {
    setCompareBase(base)
    setCompareTarget('')
    setReport(null)
  }

  const doCompare = async () => {
    if (!compareBase) return
    setComparing(true)
    setReport(null)
    try {
      const body: any = { base_id: compareBase.id }
      if (compareTarget && compareTarget !== '__live__') body.target_id = compareTarget
      const data = await apiFetch<DriftReport>('/cmdb/snapshots/compare', {
        method: 'POST',
        body: JSON.stringify(body),
      })
      setReport(data)
    } catch (e: any) {
      getMessage().error('对比失败: ' + e.message)
    } finally {
      setComparing(false)
    }
  }

  const compareTargetOptions = [
    { value: '__live__', label: '当前实时（与最新 CMDB 对比）' },
    ...snapshots.filter(s => compareBase && s.id !== compareBase.id).map(s => ({ value: s.id, label: s.name })),
  ]

  if (!canRead) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <Camera className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「查看 CMDB 资源（cmdb:read）」权限，无法使用基线快照。</p>
        </CardContent></Card>
      </div>
    )
  }

  const columns = [
    { title: '名称', dataIndex: 'name', key: 'name', render: (v: string) => <span className="font-medium">{v}</span> },
    { title: 'CI 数', dataIndex: 'ci_count', key: 'ci_count', width: 90, render: (v: number) => <Tag>{v}</Tag> },
    { title: '关系数', dataIndex: 'rel_count', key: 'rel_count', width: 90, render: (v: number) => <Tag>{v}</Tag> },
    { title: '创建时间', dataIndex: 'created_at', key: 'created_at', width: 180, render: (v: string) => <span className="text-xs text-muted-foreground">{formatTime(v)}</span> },
    {
      title: '操作', key: 'op', width: 230, render: (_: any, row: Snapshot) => (
        <div className="flex items-center gap-2">
          <Button size="small" type="text" onClick={() => setViewSnap(row)}><Eye className="h-3.5 w-3.5 mr-1" />查看</Button>
          <Button size="small" type="text" disabled={!canWrite} onClick={() => openCompare(row)}><GitCompare className="h-3.5 w-3.5 mr-1" />对比</Button>
          <Popconfirm title="确认删除该快照？" onConfirm={() => doDelete(row.id)} okText="删除" cancelText="取消">
            <Button size="small" type="text" danger disabled={!canWrite}><Trash2 className="h-3.5 w-3.5 mr-1" />删除</Button>
          </Popconfirm>
        </div>
      ),
    },
  ]

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">CMDB 基线快照</h1>
          <p className="text-sm text-muted-foreground">捕获全量 CI 与关系作为基线，对比两份快照（或快照 vs 当前实时）以发现配置漂移</p>
        </div>
        <div className="flex items-center gap-2">
          <Button type="default" size="middle" onClick={load}><RefreshCw className="h-3.5 w-3.5 mr-1" />刷新</Button>
          <Button type="primary" size="middle" disabled={!canWrite} onClick={() => setCaptureOpen(true)}><Camera className="h-3.5 w-3.5 mr-1" />捕获快照</Button>
        </div>
      </div>

      <Card>
        <CardContent className="p-0">
          <Table
            rowKey="id"
            loading={loading}
            columns={columns}
            dataSource={snapshots}
            pagination={{ pageSize: 10, hideOnSinglePage: true }}
            locale={{ emptyText: <Empty description="暂无快照，点击右上角「捕获快照」创建基线" /> }}
          />
        </CardContent>
      </Card>

      {/* 捕获快照 */}
      <Modal
        open={captureOpen}
        title="捕获基线快照"
        onCancel={() => setCaptureOpen(false)}
        onOk={doCapture}
        confirmLoading={capturing}
        okText="捕获"
        cancelText="取消"
      >
        <p className="text-xs text-muted-foreground mb-2">将抓取当前租户下全部 CI 与关系并落库，作为后续漂移对比的基线。</p>
        <Input
          placeholder="快照名称（留空自动生成）"
          value={captureName}
          onChange={e => setCaptureName(e.target.value)}
          maxLength={60}
        />
      </Modal>

      {/* 对比快照 */}
      <Modal
        open={!!compareBase}
        title={`配置漂移对比 · 基线：${compareBase?.name || ''}`}
        onCancel={() => { setCompareBase(null); setReport(null) }}
        footer={[
          <Button key="close" onClick={() => { setCompareBase(null); setReport(null) }}>关闭</Button>,
          <Button key="cmp" type="primary" disabled={!compareTarget} loading={comparing} onClick={doCompare}>开始对比</Button>,
        ]}
        width={760}
      >
        <div className="space-y-4">
          <div>
            <p className="text-xs text-muted-foreground mb-1">对比目标</p>
            <Select className="w-full" placeholder="选择对比目标" value={compareTarget || undefined} onChange={v => setCompareTarget(v)} options={compareTargetOptions} />
          </div>

          {report && (
            <div className="space-y-4 max-h-[60vh] overflow-y-auto">
              <div className="flex items-center gap-2 text-sm">
                <Tag color="blue">{report.base_name}</Tag>
                <span className="text-muted-foreground">→</span>
                <Tag color={report.target_is_live ? 'green' : 'blue'}>{report.target_name}{report.target_is_live ? '（实时）' : ''}</Tag>
              </div>

              <div className="grid grid-cols-5 gap-2 text-center">
                {[
                  { k: '新增 CI', v: report.summary.ci_added, c: 'text-emerald-600' },
                  { k: '删除 CI', v: report.summary.ci_removed, c: 'text-red-600' },
                  { k: '变更 CI', v: report.summary.ci_changed, c: 'text-amber-600' },
                  { k: '新增关系', v: report.summary.rel_added, c: 'text-emerald-600' },
                  { k: '删除关系', v: report.summary.rel_removed, c: 'text-red-600' },
                ].map(x => (
                  <div key={x.k} className="border rounded-md py-2">
                    <div className={`text-xl font-bold ${x.c}`}>{x.v}</div>
                    <div className="text-[11px] text-muted-foreground">{x.k}</div>
                  </div>
                ))}
              </div>

              {report.summary.ci_added + report.summary.ci_removed + report.summary.ci_changed + report.summary.rel_added + report.summary.rel_removed === 0 && (
                <div className="text-center text-sm text-emerald-600 py-6">两份状态完全一致，未发生配置漂移。</div>
              )}

              {report.ci_added.length > 0 && (
                <DriftBlock title="新增 CI" color="emerald">
                  {report.ci_added.map(c => <li key={c.id} className="text-xs"><span className="font-medium">{c.name}</span> <span className="text-muted-foreground">({c.type_id})</span></li>)}
                </DriftBlock>
              )}
              {report.ci_removed.length > 0 && (
                <DriftBlock title="删除 CI" color="red">
                  {report.ci_removed.map(c => <li key={c.id} className="text-xs"><span className="font-medium">{c.name}</span> <span className="text-muted-foreground">({c.type_id})</span></li>)}
                </DriftBlock>
              )}
              {report.ci_changed.length > 0 && (
                <DriftBlock title="变更 CI" color="amber">
                  {report.ci_changed.map(c => (
                    <li key={c.id} className="text-xs py-1">
                      <span className="font-medium">{c.name}</span> <span className="text-muted-foreground">({c.type_id})</span>
                      <ul className="ml-3 mt-0.5 space-y-0.5">
                        {c.fields.map((f, i) => (
                          <li key={i} className="text-[11px]">
                            <span className="text-muted-foreground">{f.field}:</span> <span className="text-red-500 line-through">{f.old ?? '∅'}</span> → <span className="text-emerald-600">{f.new ?? '∅'}</span>
                          </li>
                        ))}
                      </ul>
                    </li>
                  ))}
                </DriftBlock>
              )}
              {report.rel_added.length > 0 && (
                <DriftBlock title="新增关系" color="emerald">
                  {report.rel_added.map((r, i) => <li key={i} className="text-xs">{r.source_id} <span className="text-blue-500">—{relLabel(r.relation_type)}→</span> {r.target_id}</li>)}
                </DriftBlock>
              )}
              {report.rel_removed.length > 0 && (
                <DriftBlock title="删除关系" color="red">
                  {report.rel_removed.map((r, i) => <li key={i} className="text-xs">{r.source_id} <span className="text-blue-500">—{relLabel(r.relation_type)}→</span> {r.target_id}</li>)}
                </DriftBlock>
              )}
            </div>
          )}
        </div>
      </Modal>

      {/* 查看明细 */}
      <Modal
        open={!!viewSnap}
        title={`快照明细 · ${viewSnap?.name || ''}`}
        onCancel={() => setViewSnap(null)}
        footer={[<Button key="close" onClick={() => setViewSnap(null)}>关闭</Button>]}
        width={680}
      >
        {viewSnap && (
          <div className="space-y-3 max-h-[60vh] overflow-y-auto">
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <span>CI 数：<b className="text-foreground">{viewSnap.ci_count}</b></span>
              <span>关系数：<b className="text-foreground">{viewSnap.rel_count}</b></span>
              <span>创建于：{formatTime(viewSnap.created_at)}</span>
            </div>
            <div>
              <p className="text-xs font-medium mb-1.5">CI 列表</p>
              <div className="border rounded-md divide-y max-h-[40vh] overflow-y-auto">
                {viewSnap.data.cis.map(c => (
                  <div key={c.id} className="flex items-center justify-between px-3 py-1.5 text-xs">
                    <span className="font-medium truncate">{c.name}</span>
                    <span className="text-muted-foreground shrink-0 ml-2">{c.type_id} · {c.status}</span>
                  </div>
                ))}
                {viewSnap.data.cis.length === 0 && <p className="text-xs text-muted-foreground px-3 py-2">无 CI</p>}
              </div>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}

function DriftBlock({ title, color, children }: { title: string; color: 'emerald' | 'red' | 'amber'; children: React.ReactNode }) {
  const cls = color === 'emerald' ? 'border-emerald-300 dark:border-emerald-500/30'
    : color === 'red' ? 'border-red-300 dark:border-red-500/30'
    : 'border-amber-300 dark:border-amber-500/30'
  return (
    <div className={`border rounded-md p-3 ${cls}`}>
      <p className="text-xs font-medium mb-1.5 flex items-center gap-1.5"><AlertTriangle className="h-3.5 w-3.5" />{title}</p>
      <ul className="space-y-0.5">{children}</ul>
    </div>
  )
}
