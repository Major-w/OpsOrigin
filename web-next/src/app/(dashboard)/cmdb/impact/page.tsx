'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { apiFetch, apiList } from '@/lib/utils'
import { useCan } from '@/stores/permissions'
import { AlertTriangle, Search, Server, ArrowRight, RefreshCw } from 'lucide-react'
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

const levelColors: Record<string, string> = {
  critical: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400',
  high: 'bg-orange-100 text-orange-700 dark:bg-orange-500/20 dark:text-orange-400',
  medium: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-500/20 dark:text-yellow-400',
  low: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400',
}

const levelLabel: Record<string, string> = {
  critical: '严重', high: '高', medium: '中', low: '低',
}

export default function CMDBImpactPage() {
  const canRead = useCan('cmdb:read')
  const [ciList, setCiList] = useState<CIRow[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedCI, setSelectedCI] = useState('')
  const [analyzing, setAnalyzing] = useState(false)
  const [result, setResult] = useState<ImpactView | null>(null)
  const [search, setSearch] = useState('')
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

  const filteredCIs = ciList.filter(c => !search
    || c.name?.toLowerCase().includes(search.toLowerCase())
    || c.type_id?.toLowerCase().includes(search.toLowerCase()))

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
          <h1 className="text-2xl font-bold">影响分析</h1>
          <p className="text-sm text-muted-foreground">给定某 CI 故障，自动分析受影响的上下游 CI 及业务（服务建模：沿依赖链向上聚合业务服务）</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => setSearch('')}><RefreshCw className="h-3.5 w-3.5 mr-1" />刷新</Button>
      </div>

      <div className="flex gap-4">
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
                  <button key={ci.id} onClick={() => setSelectedCI(ci.id)} className={cn("w-full text-left px-3 py-2 rounded-md text-xs flex items-center gap-2 transition-colors", selectedCI === ci.id ? "bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-400" : "hover:bg-gray-100 dark:hover:bg-gray-800")}>
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
                    <Badge className={cn("ml-auto text-xs", levelColors[result.impactLevel] || levelColors.low)}>
                      {levelLabel[result.impactLevel] || '低'}影响
                    </Badge>
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
                    <p className="text-xs text-muted-foreground mt-1">受影响业务服务</p>
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
                            <td className="px-4 py-2"><Badge variant="outline" className="text-[10px]">{ci.type}</Badge></td>
                            <td className="px-4 py-2">
                              {ci.criticality ? (
                                <Badge className={cn("text-[10px]", levelColors[ci.criticality] || levelColors.low)}>{levelLabel[ci.criticality] || ci.criticality}</Badge>
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
                  <CardHeader className="py-3"><CardTitle className="text-sm">受影响业务服务（按关键性排序）</CardTitle></CardHeader>
                  <CardContent>
                    <div className="flex flex-wrap gap-1.5">
                      {result.affectedServices.map(s => (
                        <Badge key={s} variant="secondary" className="text-xs">{s}</Badge>
                      ))}
                      {result.affectedServices.length === 0 && <p className="text-xs text-muted-foreground">未关联业务服务（在「业务资源」分类下创建业务服务并建立依赖链）</p>}
                    </div>
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">使用说明</CardTitle></CardHeader>
                  <CardContent>
                    <div className="space-y-1.5 text-xs text-muted-foreground">
                      <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>影响分析沿依赖链（depends_on / hosts / connects）双向遍历，深度 3。</span></div>
                      <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>「业务服务」模型（business_service）带 business_criticality 字段，故障时按关键性聚合影响等级。</span></div>
                      <div className="flex items-start gap-2"><ArrowRight className="h-3 w-3 mt-0.5 shrink-0" /><span>内置模型：服务器 / 网络设备 / 数据库 / 中间件 / 应用 / 业务服务，启动时自动创建。</span></div>
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
    </div>
  )
}
