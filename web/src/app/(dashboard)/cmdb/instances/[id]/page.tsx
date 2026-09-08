
import { useEffect, useState, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Link } from 'react-router-dom'
import { useCMDBStore, type CIModel, type AttributeChange, type CIRelation } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Table, Tag, Input, Modal, Select, Tabs, Popconfirm } from 'antd'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import TopologyGraph from '@/components/TopologyGraph'
import { SourceBadge, ProvenanceSummary } from '@/components/cmdb/SourceBadge'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Pencil, Trash2, Unlink, AlertTriangle, Archive, ArchiveRestore, ChevronLeft, Plus } from 'lucide-react'
import { getMessage } from '@/lib/message'

interface TopologyNode { id: string; name: string; type: string }
interface TopologyEdge { source: string; target: string; type: string }

// 模型级关系定义（与 instances 列表页 / models 页声明一致）
type Cardinality = 'one_to_one' | 'one_to_many' | 'many_to_many'
interface RelationDef {
  name: string
  relation_type: string
  target_model: string
  cardinality: Cardinality
  reverse_name?: string
  description?: string
}
const cardinalityLabel = (c: string) =>
  c === 'one_to_one' ? '1:1 一对一' : c === 'one_to_many' ? '1:N 一对多' : 'M:N 多对多'

interface ModelInfo { id: string; name: string; category: string; parent_id?: string; abstract?: boolean; fields?: any[]; relations?: RelationDef[] }

export default function CIInstanceDetailPage() {
  const params = useParams()
  const ciID = typeof params.id === 'string' ? params.id : Array.isArray(params.id) ? params.id[0] : ''
  const { update, remove, getAudit, fetchRelations, createRelation, deleteRelation } = useCMDBStore()
  const canDelete = useCan('cmdb:delete')
  const canWrite = useCan('cmdb:write')
  const navigate = useNavigate()

  const [selectedCI, setSelectedCI] = useState<CIModel | null>(null)
  const [loading, setLoading] = useState(true)
  const [detailTab, setDetailTab] = useState('attrs')
  const [auditLog, setAuditLog] = useState<AttributeChange[]>([])
  const [ciRelations, setCiRelations] = useState<CIRelation[]>([])
  const [topology, setTopology] = useState<{ nodes: TopologyNode[]; edges: TopologyEdge[] }>({ nodes: [], edges: [] })
  const [topoLoading, setTopoLoading] = useState(false)
  const [relDialogOpen, setRelDialogOpen] = useState(false)
  const [relForm, setRelForm] = useState({ source_id: '', target_id: '', relation_type: 'depends_on' })
  const [impactResult, setImpactResult] = useState<{ source_ci: string; affected_ci: string[]; has_cycle: boolean } | null>(null)
  const [driftList, setDriftList] = useState<{ id: string; field: string; expected: string; actual: string; created_at: string }[]>([])
  const [editAttrsOpen, setEditAttrsOpen] = useState(false)
  const [attrForm, setAttrForm] = useState<Record<string, string>>({})
  // 删除实例二次确认（需输入实例名称）
  const [delOpen, setDelOpen] = useState(false)
  const [delSubmitting, setDelSubmitting] = useState(false)
  // 引用型字段的目标模型实例缓存
  const [refOptions, setRefOptions] = useState<Record<string, { id: string; name: string; attributes?: Record<string, any> }[]>>({})
  const [relSubmitting, setRelSubmitting] = useState(false)
  const [models, setModels] = useState<ModelInfo[]>([])
  const [relCreateForm, setRelCreateForm] = useState<Record<number, string>>({})
  const modelDef = models.find(m => m.id === selectedCI?.type_id)
  // 关联 CI 名称缓存（避免关系/影响列表只显示裸 ID）
  const ciNameCache = useRef<Record<string, string>>({})
  const [ciNames, setCiNames] = useState<Record<string, string>>({})
  const nameOf = (id: string) => ciNameCache.current[id] || ciNames[id] || id.slice(0, 8)
  const resolveCINames = async (ids: string[]) => {
    const todo = Array.from(new Set(ids.filter(id => id && id !== ciID && !ciNameCache.current[id])))
    if (todo.length === 0) return
    const got: Record<string, string> = {}
    await Promise.all(todo.map(async id => {
      try {
        const c = await apiFetch<{ id: string; name: string }>(`/cmdb/ci/${id}`)
        if (c?.name) got[id] = c.name
      } catch { /* 忽略单个解析失败 */ }
    }))
    if (Object.keys(got).length) {
      ciNameCache.current = { ...ciNameCache.current, ...got }
      setCiNames(prev => ({ ...prev, ...got }))
    }
  }
  // 运维联动: 本 CI 的告警(活跃/历史)
  const [ciAlerts, setCiAlerts] = useState<any[]>([])
  const [ciAlertHistory, setCiAlertHistory] = useState<any[]>([])
  const [opsLoading, setOpsLoading] = useState(false)
  const loadOps = async () => {
    if (!ciID) return
    setOpsLoading(true)
    try {
      const [active, hist] = await Promise.all([
        apiList<any>(`/monitor/alerts?ci_id=${encodeURIComponent(ciID)}`),
        apiList<any>(`/monitor/alerts?status=history&ci_id=${encodeURIComponent(ciID)}`),
      ])
      setCiAlerts(active || [])
      setCiAlertHistory(hist || [])
    } catch { setCiAlerts([]); setCiAlertHistory([]) } finally { setOpsLoading(false) }
  }

  const handleDrift = async (id: string) => {
    if (!id) { setDriftList([]); return }
    try { setDriftList(await apiList<{ id: string; field: string; expected: string; actual: string; created_at: string }>(`/cmdb/drift?ci_id=${id}`)) }
    catch { setDriftList([]) }
  }
  const resolveDrift = async (id: string) => {
    try {
      await apiFetch(`/cmdb/drift/${id}/resolve`, { method: 'POST' })
      getMessage().success('已标记解决')
      if (selectedCI) handleDrift(selectedCI.id)
    } catch (e: any) { getMessage().error('操作失败: ' + e.message) }
  }

  const ensureRefOptions = async (refModel: string) => {
    if (!refModel || refOptions[refModel]) return
    try {
      const list = await apiList<{ id: string; name: string; attributes?: Record<string, any> }>(`/cmdb/ci?type_id=${refModel}&limit=200`)
      setRefOptions(s => ({ ...s, [refModel]: list }))
    } catch { /* 忽略 */ }
  }

  const handleArchive = async (id: string) => {
    try { await apiFetch(`/cmdb/ci/${id}/archive`, { method: 'POST' }); getMessage().success('已归档'); loadCI() }
    catch (e: any) { getMessage().error('归档失败: ' + e.message) }
  }
  const handleRestore = async (id: string) => {
    try { await apiFetch(`/cmdb/ci/${id}/restore`, { method: 'POST' }); getMessage().success('已恢复'); loadCI() }
    catch (e: any) { getMessage().error('恢复失败: ' + e.message) }
  }

  const loadCI = async () => {
    setLoading(true)
    try {
      const detail = await apiFetch<CIModel>('/cmdb/ci/' + ciID)
      setSelectedCI(detail)
      const [audit, rels] = await Promise.all([
        getAudit(ciID),
        apiList<CIRelation>('/cmdb/relation?ci_id=' + encodeURIComponent(ciID)),
      ])
      setAuditLog(audit || [])
      const relsFiltered = (rels || []).filter((r: CIRelation) => r.source_id === ciID || r.target_id === ciID)
      setCiRelations(relsFiltered)
      resolveCINames(relsFiltered.flatMap(r => [r.source_id, r.target_id]))
    } catch (e: any) {
      getMessage().error('加载配置项失败: ' + e.message)
    } finally { setLoading(false) }
  }

  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000').then(list => setModels(list)).catch(() => setModels([]))
    if (ciID) loadCI()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ciID])

  // 页面加载即预热告警/漂移计数（供头部概要条 + tab 徽标使用）；进入对应页签时再刷新
  useEffect(() => { if (ciID) { loadOps(); handleDrift(ciID) } /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [ciID])
  useEffect(() => { if (detailTab === 'ops') loadOps(); else if (detailTab === 'drift' && ciID) handleDrift(ciID) }, [detailTab, ciID])

  // 模型关系定义驱动: 加载引用目标实例选项
  useEffect(() => {
    const md = models.find(m => m.id === selectedCI?.type_id)
    if (!md?.relations) return
    md.relations.forEach(def => ensureRefOptions(def.target_model))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedCI, models])

  const handleTopology = async (id: string) => {
    setTopoLoading(true); setDetailTab('topo')
    try {
      const data = await apiFetch<any>(`/cmdb/topology/${id}`)
      if (data?.nodes) {
        setTopology(data)
        resolveCINames(data.nodes.map((n: any) => n.id))
      } else {
        const relNodes = new Set<string>()
        const relEdges: TopologyEdge[] = []
        ciRelations.forEach(r => { relNodes.add(r.source_id); relNodes.add(r.target_id); relEdges.push({ source: r.source_id, target: r.target_id, type: r.relation_type }) })
        setTopology({
          nodes: Array.from(relNodes).map(nid => ({ id: nid, name: nid.slice(0, 8), type: 'host' })),
          edges: relEdges,
        })
        resolveCINames(Array.from(relNodes))
      }
    } catch { setTopology({ nodes: [], edges: [] }) } finally { setTopoLoading(false) }
  }

  const handleImpact = async (id: string) => {
    setDetailTab('impact'); setTopoLoading(true)
    try { const result = await apiFetch<any>(`/cmdb/impact/${id}`); setImpactResult(result) }
    catch { setImpactResult(null) } finally { setTopoLoading(false) }
  }

  const handleCreateRel = async () => {
    if (relSubmitting || !relForm.source_id || !relForm.target_id) return
    setRelSubmitting(true)
    try {
      await createRelation(relForm)
      setRelDialogOpen(false)
      fetchRelations()
      if (selectedCI) loadCI()
    } finally { setRelSubmitting(false) }
  }

  const handleCreateDefRel = async (def: RelationDef, targetId?: string) => {
    if (!targetId || !selectedCI || relSubmitting) return
    setRelSubmitting(true)
    try {
      await createRelation({ source_id: selectedCI.id, target_id: targetId, relation_type: def.relation_type })
      fetchRelations()
      loadCI()
      getMessage().success(`已建立关系：${def.name}`)
    } catch (e: any) { getMessage().error('建立失败: ' + e.message) } finally { setRelSubmitting(false) }
  }

  const handleDeleteRel = async (id: string) => {
    await deleteRelation(id)
    fetchRelations()
    if (selectedCI) loadCI()
  }

  const handleEditAttrs = () => {
    if (!selectedCI) return
    const attrs: Record<string, string> = {}
    for (const [k, v] of Object.entries(selectedCI.attributes || {})) {
      attrs[k] = typeof v === 'string' ? v : JSON.stringify(v)
    }
    setAttrForm(attrs); setEditAttrsOpen(true)
  }

  const handleSaveAttrs = async () => {
    if (!selectedCI) return
    const parsed: Record<string, any> = {}
    for (const [k, v] of Object.entries(attrForm)) {
      try { parsed[k] = JSON.parse(v) } catch { parsed[k] = v }
    }
    await update(selectedCI.id, { ...selectedCI, name: selectedCI.name, description: selectedCI.description, attributes: parsed, labels: selectedCI.labels, channel: 100 })
    setEditAttrsOpen(false)
    loadCI()
    getMessage().success('属性已更新')
  }

  const statusVariant = (s: string): string => {
    if (s === 'active') return 'success'
    if (s === 'inactive') return 'default'
    if (s === 'archived') return 'warning'
    return 'error'
  }

  const sevVariant = (s: string): string => {
    if (s === 'critical') return 'error'
    if (s === 'warning') return 'warning'
    return 'default'
  }

  if (loading) {
    return <div className="p-6"><p className="text-sm text-muted-foreground">加载中...</p></div>
  }
  if (!selectedCI) {
    return (
      <div className="p-6">
        <Link to="/cmdb/instances" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-blue-600 mb-4">
          <ChevronLeft className="h-4 w-4" />返回实例列表
        </Link>
        <Card className="min-h-[300px] flex items-center justify-center">
          <CardContent className="text-center text-muted-foreground">未找到该配置项，可能已被删除。</CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link to="/cmdb/instances" className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-blue-600">
            <ChevronLeft className="h-4 w-4" />返回实例列表
          </Link>
          <div>
            <h1 className="text-2xl font-bold">{selectedCI.name}</h1>
            <p className="text-xs text-muted-foreground">ID: {selectedCI.id.slice(0, 12)}... · {selectedCI.type_id} · {selectedCI.status}</p>
          </div>
        </div>
        <div className="flex gap-1">
          <Button type="default" size="middle" onClick={handleEditAttrs} disabled={!canWrite}>编辑属性</Button>
          {selectedCI.status === 'archived'
            ? <Button type="default" size="middle" onClick={() => handleRestore(selectedCI.id)}><ArchiveRestore className="h-3 w-3" />恢复</Button>
            : <Button type="default" size="middle" onClick={() => handleArchive(selectedCI.id)}><Archive className="h-3 w-3" />归档</Button>}
          {canDelete && <Button type="default" size="middle" danger onClick={() => setDelOpen(true)} title="删除该配置项"><Trash2 className="h-3 w-3" /></Button>}
        </div>
      </div>

      {/* 概要统计条: 一键直达对应页签 */}
      <div className="grid grid-cols-2 sm:grid-cols-5 gap-2">
        <button type="button" onClick={() => setDetailTab('ops')} className="rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-blue-500 hover:shadow-sm">
          <p className="text-lg font-semibold leading-none text-destructive">{ciAlerts.length}</p>
          <p className="text-[11px] text-muted-foreground mt-1">活跃告警</p>
        </button>
        <button type="button" onClick={() => setDetailTab('ops')} className="rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-blue-500 hover:shadow-sm">
          <p className="text-lg font-semibold leading-none">{ciAlertHistory.length}</p>
          <p className="text-[11px] text-muted-foreground mt-1">历史告警</p>
        </button>
        <button type="button" onClick={() => setDetailTab('rels')} className="rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-blue-500 hover:shadow-sm">
          <p className="text-lg font-semibold leading-none">{ciRelations.length}</p>
          <p className="text-[11px] text-muted-foreground mt-1">关联关系</p>
        </button>
        <button type="button" onClick={() => setDetailTab('drift')} className="rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-blue-500 hover:shadow-sm">
          <p className={`text-lg font-semibold leading-none ${driftList.length > 0 ? 'text-amber-500' : ''}`}>{driftList.length}</p>
          <p className="text-[11px] text-muted-foreground mt-1">配置漂移</p>
        </button>
        <button type="button" onClick={() => setDetailTab('audit')} className="rounded-lg border border-border bg-card p-3 text-left transition-colors hover:border-blue-500 hover:shadow-sm">
          <p className="text-lg font-semibold leading-none">{auditLog.length}</p>
          <p className="text-[11px] text-muted-foreground mt-1">变更审计</p>
        </button>
      </div>

      <Card>
        <CardHeader className="py-3">
          <CardTitle className="text-sm">配置项详情</CardTitle>
        </CardHeader>
        <CardContent>
          <Tabs activeKey={detailTab} onChange={setDetailTab} items={[
            { key: 'attrs', label: '属性', children: (
              <div className="mt-4 space-y-3">
                <div className="grid grid-cols-2 gap-2 text-sm">
                  <div><span className="text-muted-foreground">名称:</span> {selectedCI.name}</div>
                  <div><span className="text-muted-foreground">类型:</span> <Tag>{selectedCI.type_id}</Tag></div>
                  <div><span className="text-muted-foreground">状态:</span> <Tag color={statusVariant(selectedCI.status)}>{selectedCI.status}</Tag></div>
                  <div><span className="text-muted-foreground">信道:</span> {selectedCI.channel}</div>
                  <div className="col-span-2"><span className="text-muted-foreground">描述:</span> {selectedCI.description || '无'}</div>
                  <div className="col-span-2"><span className="text-muted-foreground">创建:</span> {formatTime(selectedCI.created_at)}</div>
                  <div className="col-span-2"><span className="text-muted-foreground">更新:</span> {formatTime(selectedCI.updated_at)}</div>
                </div>
                {(selectedCI as any)?.aggregates && Object.keys((selectedCI as any).aggregates).length > 0 && (
                  <div className="border rounded-md p-3 bg-muted/20 space-y-1">
                    <p className="text-xs font-medium text-muted-foreground">聚合统计（自动计算）</p>
                    <div className="flex flex-wrap gap-2">
                      {Object.entries((selectedCI as any).aggregates).map(([k, v]) => (
                        <span key={k} className="text-xs"><span className="text-muted-foreground">{k}:</span> <strong>{String(v)}</strong></span>
                      ))}
                    </div>
                  </div>
                )}
                <ProvenanceSummary meta={selectedCI.attribute_meta} />
                {Object.keys(selectedCI.attributes || {}).length > 0 && (
                  <div>
                    <h4 className="text-sm font-medium mb-2">自定义属性</h4>
                    <table className="w-full text-sm border">
                      <thead><tr className="bg-muted"><th className="px-2 py-1 text-left text-xs">属性</th><th className="px-2 py-1 text-left text-xs">值</th><th className="px-2 py-1 text-left text-xs">来源</th><th className="px-2 py-1 text-left text-xs">访问</th></tr></thead>
                      <tbody>
                        {Object.entries(selectedCI.attributes || {}).map(([k, v]) => {
                          const meta = selectedCI.attribute_meta?.[k]
                          const low = meta ? meta.confidence < 0.5 : false
                          const access = (selectedCI as any).field_access?.[k]
                          return (
                            <tr key={k} className={`border-t ${low ? 'bg-amber-50 dark:bg-amber-500/5' : ''}`}>
                              <td className="px-2 py-1 text-xs font-medium">{k}</td>
                              <td className="px-2 py-1 text-xs">{typeof v === 'object' ? JSON.stringify(v) : String(v)}</td>
                              <td className="px-2 py-1"><SourceBadge meta={meta} /></td>
                              <td className="px-2 py-1">{access === 'readonly' && <Tag className="text-[9px] text-amber-500">只读</Tag>}</td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                )}
                {Object.keys(selectedCI.labels || {}).length > 0 && (
                  <div>
                    <h4 className="text-sm font-medium mb-2">标签</h4>
                    <div className="flex gap-1 flex-wrap">
                      {Object.entries(selectedCI.labels || {}).map(([k, v]) => (
                        <Tag key={k} className="text-xs">{k}={v}</Tag>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            ) },
            { key: 'rels', label: `关系 (${ciRelations.length})`, children: (
              <div className="mt-4">
                {modelDef?.relations && modelDef.relations.length > 0 && (
                  <div className="space-y-2 mb-4">
                    <p className="text-xs font-medium text-muted-foreground">可建立的关系</p>
                    {modelDef.relations.map((def, i) => {
                      const opts = refOptions[def.target_model] || []
                      return (
                        <div key={i} className="flex items-center gap-2">
                          <span className="text-xs font-medium w-24 truncate">{def.name}</span>
                          <span className="text-[10px] text-muted-foreground shrink-0">→ {def.target_model}</span>
                          <Tag className="text-[9px] shrink-0">{cardinalityLabel(def.cardinality)}</Tag>
                          <select className="flex-1 h-8 text-xs border rounded-md bg-background px-2"
                            value={relCreateForm[i] || ''} onChange={e => setRelCreateForm(f => ({ ...f, [i]: e.target.value }))}>
                            <option value="">选择 {def.target_model} 实例...</option>
                            {opts.map(o => <option key={o.id} value={o.id}>{o.name}</option>)}
                          </select>
                          <Button size="middle" type="default" disabled={!relCreateForm[i] || relSubmitting}
                            onClick={() => handleCreateDefRel(def, relCreateForm[i])}>建立</Button>
                        </div>
                      )
                    })}
                  </div>
                )}

                {ciRelations.length === 0 ? <p className="text-sm text-muted-foreground">无关联关系。</p> : (
                  <div className="space-y-4">
                    <Table
                      size="small"
                      rowKey="id"
                      dataSource={ciRelations.map(rel => {
                        const outgoing = rel.source_id === selectedCI?.id
                        const def = modelDef?.relations?.find(d => d.relation_type === rel.relation_type)
                        return {
                          id: rel.id,
                          dir: outgoing ? 'out' : 'in',
                          peerId: outgoing ? rel.target_id : rel.source_id,
                          relName: def ? (outgoing ? def.name : (def.reverse_name || def.name)) : rel.relation_type,
                          unmodeled: !def,
                          type: rel.relation_type,
                        }
                      })}
                      columns={[
                        { title: '方向', dataIndex: 'dir', width: 96, render: (v: string) => (v === 'out'
                          ? <Tag color="blue" className="text-[10px]">我 → 目标</Tag>
                          : <Tag className="text-[10px]">来源 → 我</Tag>) },
                        { title: '关系', dataIndex: 'relName', render: (v: string, r: any) => (
                          <span className="text-xs font-medium">{v}{r.unmodeled && <Tag className="text-[9px] ml-1">未建模</Tag>}</span>) },
                        { title: '关联资产', dataIndex: 'peerId', render: (peerId: string) => (
                          <Link to={`/cmdb/instances/${peerId}`} className="text-xs hover:text-blue-600 hover:underline">{nameOf(peerId)}</Link>) },
                        { title: '类型', dataIndex: 'type', width: 130, render: (v: string) => <Tag className="text-[10px]">{v}</Tag> },
                        { title: '', key: 'op', width: 44, render: (_: unknown, r: any) => (canDelete
                          ? (
                            // 轻量行内删除档: Popconfirm 即可（契约两档: 行内=Popconfirm / 重破坏=ConfirmDialog+requireText）
                            <Popconfirm title="解除该关联关系？" description="仅解除当前关系，不影响两端配置项。" onConfirm={() => handleDeleteRel(r.id)}>
                              <Button type="text" icon={<Unlink className="h-3 w-3" />} className="h-5 w-5 p-0" />
                            </Popconfirm>
                          )
                          : null) },
                      ]}
                      pagination={false}
                    />
                    <Button type="default" size="middle" className="mt-2" onClick={() => { if (selectedCI) setRelForm(f => ({ ...f, source_id: selectedCI.id })); setRelDialogOpen(true) }}><Plus className="h-3 w-3 mr-1" />自定义关系（未建模）</Button>
                  </div>
                )}
              </div>
            ) },
            { key: 'audit', label: `审计 (${auditLog.length})`, children: (
              <div className="mt-4">
                {auditLog.length === 0 ? <p className="text-sm text-muted-foreground">无变更记录。</p> : (
                  <Table
                    size="small"
                    rowKey="_i"
                    dataSource={auditLog.map((ch, i) => ({ ...ch, _i: i }))}
                    columns={[
                      { title: '属性', dataIndex: 'attr_name', width: 150, render: (v: string) => <span className="text-xs font-medium">{v}</span> },
                      { title: '旧值', dataIndex: 'old_value', ellipsis: true, render: (v: unknown) => <span className="text-xs text-muted-foreground">{v === null || v === undefined || v === '' ? '—' : String(v).slice(0, 120)}</span> },
                      { title: '新值', dataIndex: 'new_value', ellipsis: true, render: (v: unknown) => <span className="text-xs">{v === null || v === undefined || v === '' ? '—' : String(v).slice(0, 120)}</span> },
                      { title: '信道', dataIndex: 'channel', width: 90, render: (v: string) => <Tag className="text-[10px]">{v}</Tag> },
                      {
                        title: '时间', dataIndex: 'changed_at', width: 170,
                        sorter: (a: any, b: any) => String(a.changed_at || '').localeCompare(String(b.changed_at || '')),
                        render: (v: string) => <span className="text-xs text-muted-foreground">{formatTime(v)}</span>,
                      },
                    ]}
                    pagination={{ pageSize: 10, showSizeChanger: true, showTotal: (t: number) => `共 ${t} 条` }}
                  />
                )}
              </div>
            ) },
            { key: 'topo', label: '拓扑', children: (
              <div className="mt-4">
                {topoLoading ? <p className="text-sm text-muted-foreground">加载中...</p> : topology.nodes.length === 0 ? <p className="text-sm text-muted-foreground">无拓扑数据。</p> : (
                  <div className="border rounded-lg bg-muted/20" style={{ minHeight: 300 }}>
                    <TopologyGraph data={topology} width={700} height={320} />
                  </div>
                )}
              </div>
            ) },
            { key: 'impact', label: <><AlertTriangle className="w-3 h-3 mr-1" />影响</>, children: (
              <div className="mt-4">
                {topoLoading ? <p className="text-sm text-muted-foreground">分析中...</p> : !impactResult ? <p className="text-sm text-muted-foreground">无法分析影响范围。</p> : (
                  <div className="space-y-3">
                    <div className="flex items-center gap-2">
                      <AlertTriangle className="h-4 w-4 text-amber-400" />
                      <span className="text-sm font-medium">影响分析: {selectedCI.name}</span>
                      {impactResult.has_cycle && <Tag color="error" className="text-xs">检测到拓扑环</Tag>}
                    </div>
                    <p className="text-sm">深度影响 <strong>{impactResult.affected_ci?.length || 0}</strong> 个资产:</p>
                    <div className="flex flex-wrap gap-1">
                      {(impactResult.affected_ci || []).map(ciId => (
                        <Link key={ciId} to={`/cmdb/instances/${ciId}`}><Tag className="text-xs cursor-pointer hover:border-blue-500">{nameOf(ciId)}</Tag></Link>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            ) },
            { key: 'ops', label: `运维联动 (${ciAlerts.length})`, children: (
              <div className="mt-4 space-y-4">
                {opsLoading ? <p className="text-sm text-muted-foreground">加载中...</p> : (
                  <>
                    <div className="flex items-center gap-3 text-xs text-muted-foreground">
                      <span>活跃告警 <strong className="text-destructive">{ciAlerts.length}</strong></span>
                      <span>历史告警 <strong>{ciAlertHistory.length}</strong></span>
                    </div>
                    <div>
                      <p className="text-xs font-medium text-muted-foreground mb-1">活跃告警</p>
                      {ciAlerts.length === 0 ? <p className="text-sm text-muted-foreground">当前无活跃告警 ✓</p> : (
                        <div className="space-y-2">
                          {ciAlerts.map(a => (
                            <div key={a.id} className="border rounded-md p-3">
                              <div className="flex items-center gap-2">
                                <Tag color={sevVariant(a.severity)} className="text-[10px]">{a.severity}</Tag>
                                <span className="text-sm font-medium truncate">{a.title}</span>
                                {a.is_suppressed && <Tag className="text-[9px]">已抑制</Tag>}
                              </div>
                              <p className="text-xs text-muted-foreground mt-1 truncate">{a.rule_name}{a.description ? ' · ' + a.description : ''}</p>
                              <p className="text-[10px] text-muted-foreground mt-0.5">触发: {a.triggered_at?.slice(0, 19)?.replace('T', ' ')}</p>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                    <div>
                      <p className="text-xs font-medium text-muted-foreground mb-1">历史告警</p>
                      {ciAlertHistory.length === 0 ? <p className="text-sm text-muted-foreground">无历史告警</p> : (
                        <div className="space-y-1">
                          {ciAlertHistory.slice(0, 20).map(a => (
                            <div key={a.id} className="flex items-center gap-2 text-xs border-b last:border-0 py-1">
                              <Tag color={sevVariant(a.severity)} className="text-[9px] shrink-0">{a.severity}</Tag>
                              <span className="flex-1 truncate">{a.title}</span>
                              <span className="text-[10px] text-muted-foreground shrink-0">{a.resolved_at?.slice(0, 10)}</span>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  </>
                )}
              </div>
            ) },
            { key: 'drift', label: `漂移 (${driftList.length})`, children: (
              <div className="mt-4">
                {driftList.length === 0 ? <p className="text-sm text-muted-foreground">无配置漂移（发现值均与 CMDB 一致）</p> : (
                  <div className="space-y-2">
                    {driftList.map(d => (
                      <div key={d.id} className="border rounded-md p-3 text-xs flex items-center gap-3">
                        <div className="flex-1 min-w-0">
                          <p className="font-medium">{d.field}</p>
                          <p className="text-muted-foreground truncate">CMDB: <span className="text-blue-600">{d.expected}</span> → 发现: <span className="text-amber-600">{d.actual}</span></p>
                          <p className="text-[10px] text-muted-foreground">{d.created_at?.slice(0, 19)?.replace('T', ' ')}</p>
                        </div>
                        <Button type="default" size="middle" className="h-6 text-xs shrink-0" onClick={() => resolveDrift(d.id)}>已处理</Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ) },
          ]} />
        </CardContent>
      </Card>

      <Modal open={editAttrsOpen} onCancel={() => setEditAttrsOpen(false)} title="编辑自定义属性" footer={null}>
        <div className="space-y-3 max-h-[60vh] overflow-y-auto">
          {Object.entries(attrForm).map(([k, v]) => {
            const readonly = (selectedCI as any)?.field_access?.[k] === 'readonly'
            return (
              <div key={k} className="flex gap-2 items-center">
                <input className="flex-1 text-xs rounded-md border border-input bg-background px-2 py-1.5" value={k} disabled={readonly} onChange={e => {
                  const newAttrs = { ...attrForm }; delete newAttrs[k]; newAttrs[e.target.value] = v; setAttrForm(newAttrs)
                }} placeholder="属性名" />
                <input className="flex-1 text-xs rounded-md border border-input bg-background px-2 py-1.5" value={v} disabled={readonly} onChange={e => setAttrForm(f => ({ ...f, [k]: e.target.value }))} placeholder="值" />
                {readonly ? <span className="w-8 shrink-0 text-[9px] text-amber-500 text-center">只读</span> : <Button type="text" icon={<Trash2 className="h-3 w-3" />} className="h-8 w-8 shrink-0" onClick={() => { const newAttrs = { ...attrForm }; delete newAttrs[k]; setAttrForm(newAttrs) }} />}
              </div>
            )
          })}
          <Button type="default" size="middle" onClick={() => setAttrForm(f => ({ ...f, '': '' }))}><Plus className="h-3 w-3 mr-1" />添加属性</Button>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button type="default" onClick={() => setEditAttrsOpen(false)}>取消</Button><Button type="primary" onClick={handleSaveAttrs}>保存</Button></div>
      </Modal>

      <Modal open={relDialogOpen} onCancel={() => setRelDialogOpen(false)} title="新建关联" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">源资产</label>
            <Select value={relForm.source_id || undefined} onChange={v => setRelForm(f => ({ ...f, source_id: v }))} className="w-full mt-1" options={[{ value: '', label: '选择资产' }, { value: selectedCI?.id, label: `${selectedCI?.name}（当前）` }]} />
          </div>
          <div><label className="text-sm font-medium">目标资产</label>
            <Select value={relForm.target_id || undefined} onChange={v => setRelForm(f => ({ ...f, target_id: v }))} className="w-full mt-1" options={[{ value: '', label: '选择资产' }]} />
          </div>
          <div><label className="text-sm font-medium">关联类型</label>
            <Select value={relForm.relation_type} onChange={v => setRelForm(f => ({ ...f, relation_type: v }))} className="w-full mt-1" options={[{ value: 'depends_on', label: 'depends_on' }, { value: 'connects_to', label: 'connects_to' }, { value: 'contains', label: 'contains' }, { value: 'backup_of', label: 'backup_of' }]} />
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button type="default" onClick={() => setRelDialogOpen(false)} disabled={relSubmitting}>取消</Button><Button type="primary" onClick={handleCreateRel} disabled={!relForm.source_id || !relForm.target_id || relSubmitting}>创建{relSubmitting ? '…' : ''}</Button></div>
      </Modal>

      {/* 重破坏操作: 需输入实例全名二次确认（契约: ConfirmDialog + requireText） */}
      <ConfirmDialog
        open={delOpen}
        onClose={() => setDelOpen(false)}
        title="确认删除配置项"
        description={`将永久删除配置项「${selectedCI?.name || ''}」及其全部关系与审计记录，此操作不可撤销。`}
        confirmLabel="永久删除"
        loading={delSubmitting}
        requireText={selectedCI?.name || ''}
        onConfirm={async () => {
          setDelSubmitting(true)
          const ok = await remove(selectedCI?.id || '')
          if (ok) {
            setDelOpen(false)
            navigate('/cmdb/instances')
          }
          setDelSubmitting(false)
        }}
      />
    </div>
  )
}
