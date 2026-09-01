
import { useEffect, useState, useRef } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { Link } from 'react-router-dom'
import { useCMDBStore, type CIModel, type AttributeChange, type CIRelation } from '@/stores/cmdb'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Select, Tabs, Popconfirm, message } from 'antd'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import TopologyGraph from '@/components/TopologyGraph'
import { SourceBadge, ProvenanceSummary } from '@/components/cmdb/SourceBadge'
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

  const handleDrift = async (id: string) => {
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
      setCiRelations((rels || []).filter((r: CIRelation) => r.source_id === ciID || r.target_id === ciID))
    } catch (e: any) {
      getMessage().error('加载配置项失败: ' + e.message)
    } finally { setLoading(false) }
  }

  useEffect(() => {
    apiList<ModelInfo>('/cmdb/model-type?limit=1000').then(list => setModels(list)).catch(() => setModels([]))
    if (ciID) loadCI()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ciID])

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
      if (data?.nodes) setTopology(data)
      else {
        const relNodes = new Set<string>()
        const relEdges: TopologyEdge[] = []
        ciRelations.forEach(r => { relNodes.add(r.source_id); relNodes.add(r.target_id); relEdges.push({ source: r.source_id, target: r.target_id, type: r.relation_type }) })
        setTopology({
          nodes: Array.from(relNodes).map(nid => ({ id: nid, name: nid.slice(0, 8), type: 'host' })),
          edges: relEdges,
        })
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
                    {(() => {
                      const outgoing = ciRelations.filter(r => r.source_id === selectedCI?.id)
                      const incoming = ciRelations.filter(r => r.target_id === selectedCI?.id)
                      const displayName = (rel: typeof ciRelations[number], dir: 'out' | 'in') => {
                        const def = modelDef?.relations?.find(d => d.relation_type === rel.relation_type)
                        if (!def) return { name: rel.relation_type, unmodeled: true }
                        return { name: dir === 'out' ? def.name : (def.reverse_name || def.name), unmodeled: false }
                      }
                      return (
                        <>
                          {outgoing.length > 0 && (
                            <div>
                              <p className="text-xs font-medium text-muted-foreground mb-1">我 → 其它（{outgoing.length}）</p>
                              <table className="w-full text-sm">
                                <tbody>
                                  {outgoing.map(rel => (
                                    <tr key={rel.id} className="border-b last:border-0">
                                      <td className="py-1 text-xs w-28 font-medium">{displayName(rel, 'out').name}{displayName(rel, 'out').unmodeled && <Tag className="text-[9px] ml-1">未建模</Tag>}</td>
                                      <td className="py-1 text-xs text-muted-foreground">{rel.target_id.slice(0, 8)}</td>
                                      <td className="py-1 text-xs"><Tag className="text-[10px]">{rel.relation_type}</Tag></td>
                                      <td className="py-1 text-right">{canDelete && <Button type="text" icon={<Unlink className="h-3 w-3" />} className="h-5 w-5 p-0" onClick={() => handleDeleteRel(rel.id)} />}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                          {incoming.length > 0 && (
                            <div>
                              <p className="text-xs font-medium text-muted-foreground mb-1">其它 → 我（{incoming.length}）</p>
                              <table className="w-full text-sm">
                                <tbody>
                                  {incoming.map(rel => (
                                    <tr key={rel.id} className="border-b last:border-0">
                                      <td className="py-1 text-xs w-28 font-medium">{displayName(rel, 'in').name}{displayName(rel, 'in').unmodeled && <Tag className="text-[9px] ml-1">未建模</Tag>}</td>
                                      <td className="py-1 text-xs text-muted-foreground">{rel.source_id.slice(0, 8)}</td>
                                      <td className="py-1 text-xs"><Tag className="text-[10px]">{rel.relation_type}</Tag></td>
                                      <td className="py-1 text-right">{canDelete && <Button type="text" icon={<Unlink className="h-3 w-3" />} className="h-5 w-5 p-0" onClick={() => handleDeleteRel(rel.id)} />}</td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </>
                      )
                    })()}
                    <Button type="default" size="middle" className="mt-2" onClick={() => { if (selectedCI) setRelForm(f => ({ ...f, source_id: selectedCI.id })); setRelDialogOpen(true) }}><Plus className="h-3 w-3 mr-1" />自定义关系（未建模）</Button>
                  </div>
                )}
              </div>
            ) },
            { key: 'audit', label: `审计 (${auditLog.length})`, children: (
              <div className="mt-4">
                {auditLog.length === 0 ? <p className="text-sm text-muted-foreground">无变更记录。</p> : (
                  <table className="w-full text-sm">
                    <thead><tr className="border-b text-muted-foreground"><th className="pb-2 text-left text-xs">属性</th><th className="pb-2 text-left text-xs">旧值</th><th className="pb-2 text-left text-xs">新值</th><th className="pb-2 text-left text-xs">信道</th><th className="pb-2 text-left text-xs">时间</th></tr></thead>
                    <tbody>
                      {auditLog.map((ch, i) => (
                        <tr key={i} className="border-b last:border-0">
                          <td className="py-1 text-xs font-medium">{ch.attr_name}</td>
                          <td className="py-1 text-xs text-muted-foreground">{String(ch.old_value || '').slice(0, 40)}</td>
                          <td className="py-1 text-xs">{String(ch.new_value || '').slice(0, 40)}</td>
                          <td className="py-1 text-xs"><Tag className="text-[10px]">{ch.channel}</Tag></td>
                          <td className="py-1 text-xs text-muted-foreground">{formatTime(ch.changed_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
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
                        <Link key={ciId} to={`/cmdb/instances/${ciId}`}><Tag className="text-xs cursor-pointer hover:border-blue-500">{ciId.slice(0, 8)}</Tag></Link>
                      ))}
                    </div>
                  </div>
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

      <Popconfirm
        title="确认删除配置项"
        description={`将永久删除配置项「${selectedCI?.name || ''}」，此操作不可撤销。请输入实例名称以确认删除。`}
        onConfirm={async () => {
          setDelSubmitting(true)
          const ok = await remove(selectedCI?.id || '')
          if (ok) {
            setDelOpen(false)
            navigate('/cmdb/instances')
          }
          setDelSubmitting(false)
        }}
        open={delOpen}
        onCancel={() => setDelOpen(false)}
        okButtonProps={{ loading: delSubmitting }}
      />
    </div>
  )
}
