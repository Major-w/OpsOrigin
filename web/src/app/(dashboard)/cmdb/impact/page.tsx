
import { useEffect, useState, Fragment } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Tabs, Select } from 'antd'
import { apiFetch, apiList } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { AlertTriangle, Search, Server, ArrowRight, RefreshCw, Route, XCircle } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

// 后端 /api/v1/cmdb/impact/{id} 响应（snake_case，与后端契约一致）
interface ImpactAPI {
  source_ci: string
  affected_ci: string[]
  depth: number
  has_cycle: boolean
  affected_ci_details?: { id: string; name: string; type_id: string; criticality?: string }[]
  business_services?: { ci_id: string; name: string; criticality?: string }[]
  impact_level?: string
}

interface CIRow {
  id: string
  name: string
  type_id: string
}

// 展示视图（由后端结构映射而来）
interface ImpactView {
  sourceCI: string
  sourceType: string
  impactLevel: string
  affectedCIs: { id: string; name: string; type: string; criticality: string }[]
  affectedServices: string[]
  hasCycle: boolean
  depth: number
}

// 路径查询：后端 /api/v1/cmdb/path?from=&to= 响应（无向 BFS 最短路径）
interface PathNode {
  id: string
  name?: string
  type_id?: string
}
interface PathEdge {
  source: string
  target: string
  relation_type: string
  direction: string // forward: 正向(source->target) / reverse: 反向(实际关系为 target->source)
}
interface PathResult {
  found: boolean
  path: PathNode[]
  edges: PathEdge[]
}

const RELATION_LABELS: Record<string, string> = {
  depends_on: '依赖于',
  connects_to: '连接到',
  contains: '包含',
  backup_of: '备份',
  deployed_on: '部署于',
  calls: '调用',
  belongs_to: '隶属于',
  part_of: '组成部分',
}
const relLabel = (k: string) => RELATION_LABELS[k] || k

export default function CMDBImpactPage() {
  const canRead = useCan('cmdb:read')
  const [ciList, setCiList] = useState<CIRow[]>([])
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<'impact' | 'path'>('impact')

  // 影响分析
  const [selectedCI, setSelectedCI] = useState('')
  const [analyzing, setAnalyzing] = useState(false)
  const [result, setResult] = useState<ImpactView | null>(null)
  const [search, setSearch] = useState('')

  // 路径查询
  const [pathFrom, setPathFrom] = useState('')
  const [pathTo, setPathTo] = useState('')
  const [pathLoading, setPathLoading] = useState(false)
  const [pathResult, setPathResult] = useState<PathResult | null>(null)
  const { theme } = useThemeStore()

  useEffect(() => {
    apiList<{ id: string; name: string; type_id: string }>('/cmdb/ci?limit=500')
      .then(items => setCiList(items))
      .catch(() => setCiList([]))
      .finally(() => setLoading(false))
  }, [])

  const analyzeImpact = async () => {
    if (!selectedCI) return
    setAnalyzing(true)
    try {
      const data = await apiFetch<ImpactAPI>(`/cmdb/impact/${selectedCI}`)
      const source = data.affected_ci_details?.find(d => d.id === data.source_ci)
      const services = (data.business_services || []).map(s => s.name)
      setResult({
        sourceCI: source?.name || data.source_ci,
        sourceType: source?.type_id || '',
        impactLevel: data.impact_level || 'low',
        affectedCIs: (data.affected_ci_details || []).map(d => ({
          id: d.id, name: d.name || d.id, type: d.type_id, criticality: d.criticality || '',
        })),
        affectedServices: services,
        hasCycle: data.has_cycle,
        depth: data.depth,
      })
    } catch (e: any) {
      setResult(null)
    } finally {
      setAnalyzing(false)
    }
  }

  const queryPath = async () => {
    if (!pathFrom || !pathTo) return
    setPathLoading(true)
    setPathResult(null)
    try {
      const data = await apiFetch<PathResult>(`/cmdb/path?from=${encodeURIComponent(pathFrom)}&to=${encodeURIComponent(pathTo)}`)
      setPathResult(data)
    } catch (e: any) {
      setPathResult(null)
    } finally {
      setPathLoading(false)
    }
  }

  const filteredCIs = ciList.filter(c => !search
    || c.name?.toLowerCase().includes(search.toLowerCase())
    || c.type_id?.toLowerCase().includes(search.toLowerCase()))

  const ciOptions = ciList.map(c => ({ value: c.id, label: `${c.name || c.id} (${c.type_id})` }))

  if (!canRead) {
    return (
      <div className="p-6">
        <Card><CardContent className="flex flex-col items-center justify-center gap-2 py-16 text-center">
          <AlertTriangle className="h-8 w-8 text-muted-foreground" />
          <p className="text-sm font-medium">无访问权限</p>
          <p className="text-xs text-muted-foreground">当前账号缺少「查看 CMDB 资源（cmdb:read）」权限，无法使用影响分析。</p>
        </CardContent></Card>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">影响分析 / 依赖路径</h1>
          <p className="text-sm text-muted-foreground">给定某 CI 故障自动分析受影响的上下游 CI 与业务；或查询任意两个 CI 之间的最短依赖路径</p>
        </div>
        <Button type="default" size="middle" onClick={() => setSearch('')}><RefreshCw className="h-3.5 w-3.5 mr-1" />刷新</Button>
      </div>

      <Tabs activeKey={tab} onChange={v => setTab(v as 'impact' | 'path')} items={[
        {
          key: 'impact',
          label: <><AlertTriangle className="h-3.5 w-3.5 mr-1" />影响分析</>,
          children: (
            <div className="flex gap-4 mt-4">
              {/* 左侧：选择故障CI */}
              <div className="w-72 space-y-4 shrink-0">
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">选择故障 CI</CardTitle></CardHeader>
                  <CardContent className="space-y-2">
                    <div className="relative">
                      <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
                      <Input placeholder="搜索 CI..." value={search} onChange={e => setSearch(e.target.value)} className="pl-8 h-8 text-xs" />
                    </div>
                    <div className="space-y-1 max-h-[300px] overflow-y-auto">
                      {loading && <p className="text-xs text-muted-foreground py-4 text-center">加载中...</p>}
                      {!loading && filteredCIs.map(ci => (
                        <button key={ci.id} onClick={() => setSelectedCI(ci.id)} className={cn("w-full text-left px-3 py-2 rounded-md text-xs flex items-center gap-2 transition-colors", selectedCI === ci.id ? "bg-blue-100 dark:bg-blue-500/20 text-blue-700 dark:text-blue-400" : "hover:bg-gray-100 dark:hover:bg-gray-800")}>
                          <Server className="h-3.5 w-3.5 shrink-0" />
                          <div className="min-w-0">
                            <div className="font-medium truncate">{ci.name || ci.id}</div>
                            <div className="text-muted-foreground text-[10px] truncate">{ci.type_id}</div>
                          </div>
                        </button>
                      ))}
                      {!loading && filteredCIs.length === 0 && <p className="text-xs text-muted-foreground py-4 text-center">无匹配 CI</p>}
                    </div>
                  </CardContent>
                </Card>
                <Button onClick={analyzeImpact} disabled={!selectedCI || analyzing} className="w-full">
                  <AlertTriangle className="h-4 w-4 mr-1" />{analyzing ? '分析中...' : '开始分析'}
                </Button>
              </div>

              {/* 右侧：分析结果 */}
              <div className="flex-1 space-y-4">
                {result ? (
                  <>
                    <div className="flex items-center gap-4">
                      <Card className="flex-1">
                        <CardContent className="p-4 flex items-center gap-4">
                          <div className="h-12 w-12 rounded-lg bg-red-100 dark:bg-red-500/20 flex items-center justify-center"><AlertTriangle className="h-6 w-6 text-red-600 dark:text-red-400" /></div>
                          <div className="min-w-0">
                            <p className="text-sm font-medium truncate">{result.sourceCI}</p>
                            <p className="text-xs text-muted-foreground">{result.sourceType || '未知类型'}</p>
                          </div>
                          <Tag color={result.impactLevel === 'critical' ? 'error' : result.impactLevel === 'high' ? 'orange' : result.impactLevel === 'medium' ? 'warning' : 'success'} className="ml-auto text-xs">
                            {levelLabel[result.impactLevel] || '低'}影响
                          </Tag>
                        </CardContent>
                      </Card>
                      <Card className="w-44">
                        <CardContent className="p-4 text-center">
                          <p className="text-3xl font-bold text-red-600">{result.affectedCIs.length}</p>
                          <p className="text-xs text-muted-foreground mt-1">受影响 CI</p>
                        </CardContent>
                      </Card>
                      <Card className="w-44">
                        <CardContent className="p-4 text-center">
                          <p className="text-3xl font-bold text-orange-600">{result.affectedServices.length}</p>
                          <p className="text-xs text-muted-foreground mt-1">受影响业务</p>
                        </CardContent>
                      </Card>
                    </div>

                    {result.hasCycle && (
                      <div className="text-xs text-amber-600 bg-amber-50 dark:bg-amber-500/10 border border-amber-200 dark:border-amber-500/20 rounded-md px-3 py-2">
                        检测到拓扑环路（深度 {result.depth}），请先检查依赖关系是否成环
                      </div>
                    )}

                    {/* 受影响 CI 明细 */}
                    <Card>
                      <CardHeader className="py-3"><CardTitle className="text-sm">受影响 CI 明细（深度 {result.depth}）</CardTitle></CardHeader>
                      <CardContent className="p-0">
                        <div className="overflow-x-auto">
                          <table className="w-full text-sm">
                            <thead><tr className="border-b text-left text-muted-foreground">
                              <th className="pb-2 font-medium px-4 py-2">CI 名称</th>
                              <th className="pb-2 font-medium px-4 py-2">类型</th>
                              <th className="pb-2 font-medium px-4 py-2">业务关键性</th>
                            </tr></thead>
                            <tbody>
                              {result.affectedCIs.map(ci => (
                                <tr key={ci.id} className="border-b last:border-0">
                                  <td className="px-4 py-2 font-medium">{ci.name}</td>
                                  <td className="px-4 py-2"><Tag className="text-[10px]">{ci.type}</Tag></td>
                                  <td className="px-4 py-2">
                                    {ci.criticality ? (
                                      <Tag color={ci.criticality === 'critical' ? 'error' : ci.criticality === 'high' ? 'orange' : ci.criticality === 'medium' ? 'warning' : 'success'} className="text-[10px]">{levelLabel[ci.criticality] || ci.criticality}</Tag>
                                    ) : <span className="text-xs text-muted-foreground">—</span>}
                                  </td>
                                </tr>
                              ))}
                              {result.affectedCIs.length === 0 && (
                                <tr><td colSpan={3} className="text-center py-4 text-muted-foreground">无受影响的 CI</td></tr>
                              )}
                            </tbody>
                          </table>
                        </div>
                      </CardContent>
                    </Card>

                    <div className="grid grid-cols-2 gap-4">
                      <Card>
                        <CardHeader className="py-3"><CardTitle className="text-sm">受影响业务（按关键性排序）</CardTitle></CardHeader>
                        <CardContent>
                          <div className="flex flex-wrap gap-1.5">
                            {result.affectedServices.map(s => (
                              <Tag key={s} className="text-xs">{s}</Tag>
                            ))}
                            {result.affectedServices.length === 0 && <p className="text-xs text-muted-foreground">未关联业务（在「业务资源」分类下创建业务并建立依赖链）</p>}
                          </div>
                        </CardContent>
                      </Card>
                      <Card>
                        <CardHeader className="py-3"><CardTitle className="text-sm">使用说明</CardTitle></CardHeader>
                        <CardContent>
                          <div className="space-y-1.5 text-xs text-muted-foreground">
                            <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>影响分析沿依赖链（depends_on / hosts / connects）双向遍历，深度 3。</span></div>
                            <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>「业务」模型（business_service）带 business_criticality 字段，故障时按关键性聚合影响等级。</span></div>
                            <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>内置模型：服务器 / 网络设备 / 数据库 / 中间件 / 应用 / 业务，启动时自动创建。</span></div>
                          </div>
                        </CardContent>
                      </Card>
                    </div>
                  </>
                ) : (
                  <Card className="h-[400px] flex items-center justify-center">
                    <div className="text-center text-muted-foreground">
                      <AlertTriangle className="h-12 w-12 mx-auto mb-3 opacity-30" />
                      <p>请在左侧选择一个故障 CI，然后点击「开始分析」</p>
                    </div>
                  </Card>
                )}
              </div>
            </div>
          ),
        },
        {
          key: 'path',
          label: <><Route className="h-3.5 w-3.5 mr-1" />依赖路径</>,
          children: (
            <div className="mt-4 space-y-4">
              <Card>
                <CardHeader className="py-3"><CardTitle className="text-sm">最短依赖路径查询</CardTitle></CardHeader>
                <CardContent className="space-y-4">
                  <div className="grid grid-cols-1 md:grid-cols-[1fr_auto_1fr_auto] gap-3 items-end">
                    <div>
                      <p className="text-xs text-muted-foreground mb-1">起点 CI</p>
                      <Select
                        showSearch
                        className="w-full"
                        placeholder="选择起点 CI"
                        value={pathFrom || undefined}
                        onChange={v => setPathFrom(v)}
                        options={ciOptions}
                        filterOption={(input, option) => String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
                      />
                    </div>
                    <ArrowRight className="h-4 w-4 text-muted-foreground hidden md:block" />
                    <div>
                      <p className="text-xs text-muted-foreground mb-1">终点 CI</p>
                      <Select
                        showSearch
                        className="w-full"
                        placeholder="选择终点 CI"
                        value={pathTo || undefined}
                        onChange={v => setPathTo(v)}
                        options={ciOptions}
                        filterOption={(input, option) => String(option?.label ?? '').toLowerCase().includes(input.toLowerCase())}
                      />
                    </div>
                    <Button type="primary" onClick={queryPath} disabled={!pathFrom || !pathTo || pathLoading}>
                      {pathLoading ? '查询中...' : '查询路径'}
                    </Button>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    沿关系链（depends_on / connects_to / contains 等）做无向最短路径遍历；箭头方向表示实际关系指向，「反向」代表路径方向与存储关系方向相反。
                  </p>
                </CardContent>
              </Card>

              {pathResult && (
                <Card>
                  <CardHeader className="py-3">
                    <CardTitle className="text-sm flex items-center gap-2">
                      {pathResult.found ? <><Route className="h-4 w-4 text-blue-500" />找到路径（{pathResult.path.length} 个节点 / {pathResult.edges.length} 跳）</> : <><XCircle className="h-4 w-4 text-muted-foreground" />未找到连通路径</>}
                    </CardTitle>
                  </CardHeader>
                  <CardContent>
                    {pathResult.found ? (
                      <div className="flex flex-wrap items-center gap-2">
                        {pathResult.path.map((n, i) => (
                          <Fragment key={n.id}>
                            <div className="px-3 py-2 rounded-lg border bg-background text-sm min-w-[140px] max-w-[220px]">
                              <div className="font-medium truncate">{n.name || n.id}</div>
                              <div className="text-[10px] text-muted-foreground truncate">{n.type_id}</div>
                            </div>
                            {i < pathResult.path.length - 1 && (
                              <div className="flex flex-col items-center text-[10px] text-muted-foreground shrink-0">
                                <ArrowRight className="h-4 w-4" />
                                <span className="mt-0.5 flex items-center gap-1">
                                  {relLabel(pathResult.edges[i].relation_type)}
                                  {pathResult.edges[i].direction === 'reverse' && <span className="text-amber-500">·反向</span>}
                                </span>
                              </div>
                            )}
                          </Fragment>
                        ))}
                      </div>
                    ) : (
                      <p className="text-sm text-muted-foreground">两个 CI 之间在当前关系网络中不连通，或其中一方不存在。</p>
                    )}
                  </CardContent>
                </Card>
              )}
            </div>
          ),
        },
      ]} />
    </div>
  )
}

const levelLabel: Record<string, string> = {
  critical: '严重', high: '高', medium: '中', low: '低',
}
