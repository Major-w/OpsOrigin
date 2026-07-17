'use client'

import { useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { apiFetch } from '@/lib/utils'
import { AlertTriangle, Search, Server, Database, Shield, ArrowRight, RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import { useThemeStore } from '@/stores/theme'

interface ImpactResult {
  sourceCI: string
  sourceType: string
  impactLevel: 'critical' | 'high' | 'medium' | 'low'
  affectedCIs: { id: string; name: string; type: string; relation: string; depth: number }[]
  affectedServices: string[]
  suggestedActions: string[]
}

const MOCK_CI_LIST = [
  { id: '1', name: 'db-master', type: 'database' },
  { id: '2', name: 'web-01', type: 'web-server' },
  { id: '3', name: 'lb-01', type: 'loadbalancer' },
  { id: '4', name: 'app-01', type: 'application' },
  { id: '5', name: 'firewall-01', type: 'firewall' },
  { id: '6', name: 'switch-01', type: 'switch' },
]

const MOCK_IMPACTS: Record<string, ImpactResult> = {
  '1': {
    sourceCI: 'db-master', sourceType: 'database', impactLevel: 'critical',
    affectedCIs: [
      { id: '4', name: 'app-01', type: 'application', relation: '读写', depth: 1 },
      { id: '7', name: 'app-02', type: 'application', relation: '读写', depth: 1 },
      { id: '2', name: 'web-01', type: 'web-server', relation: '调用', depth: 2 },
      { id: '8', name: 'web-02', type: 'web-server', relation: '调用', depth: 2 },
      { id: '3', name: 'lb-01', type: 'loadbalancer', relation: '负载均衡', depth: 3 },
    ],
    affectedServices: ['用户登录', '订单查询', '数据报表', 'API网关'],
    suggestedActions: ['立即切换到 db-slave', '通知业务方', '启动应急响应', '检查备份状态']
  },
  '2': {
    sourceCI: 'web-01', sourceType: 'web-server', impactLevel: 'high',
    affectedCIs: [
      { id: '3', name: 'lb-01', type: 'loadbalancer', relation: '负载均衡', depth: 1 },
    ],
    affectedServices: ['用户登录', '商品浏览'],
    suggestedActions: ['检查负载均衡健康检查', '确认流量切换', '通知前端团队']
  },
  '3': {
    sourceCI: 'lb-01', sourceType: 'loadbalancer', impactLevel: 'critical',
    affectedCIs: [
      { id: '2', name: 'web-01', type: 'web-server', relation: '负载均衡', depth: 1 },
      { id: '8', name: 'web-02', type: 'web-server', relation: '负载均衡', depth: 1 },
      { id: '4', name: 'app-01', type: 'application', relation: '调用', depth: 2 },
      { id: '7', name: 'app-02', type: 'application', relation: '调用', depth: 2 },
    ],
    affectedServices: ['所有对外服务', 'API网关', '用户门户'],
    suggestedActions: ['紧急切换备用链路', '通知所有业务方', '启动灾备预案']
  },
}

export default function CMDBImpactPage() {
  const [selectedCI, setSelectedCI] = useState('')
  const [result, setResult] = useState<ImpactResult | null>(null)
  const [search, setSearch] = useState('')
  const { theme } = useThemeStore()

  const analyzeImpact = () => {
    if (!selectedCI) return
    const impact = MOCK_IMPACTS[selectedCI] || {
      sourceCI: MOCK_CI_LIST.find(c => c.id === selectedCI)?.name || '',
      sourceType: MOCK_CI_LIST.find(c => c.id === selectedCI)?.type || '',
      impactLevel: 'low' as const,
      affectedCIs: [],
      affectedServices: [],
      suggestedActions: ['暂无受影响的CI', '建议检查相关依赖']
    }
    setResult(impact)
  }

  const filteredCIs = MOCK_CI_LIST.filter(c => !search || c.name.toLowerCase().includes(search.toLowerCase()) || c.type.toLowerCase().includes(search.toLowerCase()))

  const levelColors: Record<string, string> = {
    critical: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400',
    high: 'bg-orange-100 text-orange-700 dark:bg-orange-500/20 dark:text-orange-400',
    medium: 'bg-yellow-100 text-yellow-700 dark:bg-yellow-500/20 dark:text-yellow-400',
    low: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400',
  }

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">影响分析</h1>
          <p className="text-sm text-muted-foreground">给定某 CI 故障，自动分析受影响的上下游 CI 及业务</p>
        </div>
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
                {filteredCIs.map(ci => (
                  <button key={ci.id} onClick={() => setSelectedCI(ci.id)} className={cn("w-full text-left px-3 py-2 rounded-md text-xs flex items-center gap-2 transition-colors", selectedCI === ci.id ? "bg-emerald-100 dark:bg-emerald-500/20 text-emerald-700 dark:text-emerald-400" : "hover:bg-gray-100 dark:hover:bg-gray-800")}>
                    <Server className="h-3.5 w-3.5" />
                    <div>
                      <div className="font-medium">{ci.name}</div>
                      <div className="text-muted-foreground text-[10px]">{ci.type}</div>
                    </div>
                  </button>
                ))}
              </div>
            </CardContent>
          </Card>
          <Button onClick={analyzeImpact} disabled={!selectedCI} className="w-full"><AlertTriangle className="h-4 w-4" />开始分析</Button>
        </div>

        {/* 右侧：分析结果 */}
        <div className="flex-1 space-y-4">
          {result ? (
            <>
              <div className="flex items-center gap-4">
                <Card className="flex-1">
                  <CardContent className="p-4 flex items-center gap-4">
                    <div className="h-12 w-12 rounded-lg bg-red-100 dark:bg-red-500/20 flex items-center justify-center"><AlertTriangle className="h-6 w-6 text-red-600 dark:text-red-400" /></div>
                    <div>
                      <p className="text-sm font-medium">{result.sourceCI}</p>
                      <p className="text-xs text-muted-foreground">{result.sourceType}</p>
                    </div>
                    <Badge className={cn("ml-auto text-xs", levelColors[result.impactLevel])}>
                      {result.impactLevel === 'critical' ? '严重' : result.impactLevel === 'high' ? '高' : result.impactLevel === 'medium' ? '中' : '低'}影响
                    </Badge>
                  </CardContent>
                </Card>
                <Card className="w-48">
                  <CardContent className="p-4 text-center">
                    <p className="text-3xl font-bold text-red-600">{result.affectedCIs.length}</p>
                    <p className="text-xs text-muted-foreground mt-1">受影响 CI</p>
                  </CardContent>
                </Card>
                <Card className="w-48">
                  <CardContent className="p-4 text-center">
                    <p className="text-3xl font-bold text-orange-600">{result.affectedServices.length}</p>
                    <p className="text-xs text-muted-foreground mt-1">受影响服务</p>
                  </CardContent>
                </Card>
              </div>

              {/* 影响链路 */}
              <Card>
                <CardHeader className="py-3"><CardTitle className="text-sm">影响链路（按距离排序）</CardTitle></CardHeader>
                <CardContent className="p-0">
                  <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                      <thead><tr className="border-b text-left text-muted-foreground">
                        <th className="pb-2 font-medium px-4 py-2">CI 名称</th>
                        <th className="pb-2 font-medium px-4 py-2">类型</th>
                        <th className="pb-2 font-medium px-4 py-2">关系</th>
                        <th className="pb-2 font-medium px-4 py-2">距离</th>
                      </tr></thead>
                      <tbody>
                        {result.affectedCIs.map((ci, i) => (
                          <tr key={ci.id} className="border-b last:border-0">
                            <td className="px-4 py-2 font-medium">{ci.name}</td>
                            <td className="px-4 py-2"><Badge variant="outline" className="text-[10px]">{ci.type}</Badge></td>
                            <td className="px-4 py-2 text-xs">{ci.relation}</td>
                            <td className="px-4 py-2">
                              <div className="flex items-center gap-1">
                                {Array.from({ length: ci.depth }).map((_, j) => (
                                  <ArrowRight key={j} className="h-3 w-3 text-muted-foreground" />
                                ))}
                                <span className="text-xs font-medium">{ci.depth} 跳</span>
                              </div>
                            </td>
                          </tr>
                        ))}
                        {result.affectedCIs.length === 0 && (
                          <tr><td colSpan={4} className="text-center py-4 text-muted-foreground">无受影响的 CI</td></tr>
                        )}
                      </tbody>
                    </table>
                  </div>
                </CardContent>
              </Card>

              <div className="grid grid-cols-2 gap-4">
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">受影响业务服务</CardTitle></CardHeader>
                  <CardContent>
                    <div className="flex flex-wrap gap-1.5">
                      {result.affectedServices.map(s => (
                        <Badge key={s} variant="secondary" className="text-xs">{s}</Badge>
                      ))}
                      {result.affectedServices.length === 0 && <p className="text-xs text-muted-foreground">无受影响服务</p>}
                    </div>
                  </CardContent>
                </Card>
                <Card>
                  <CardHeader className="py-3"><CardTitle className="text-sm">建议操作</CardTitle></CardHeader>
                  <CardContent>
                    <div className="space-y-1.5">
                      {result.suggestedActions.map((a, i) => (
                        <div key={i} className="flex items-start gap-2 text-xs">
                          <span className="text-emerald-500 mt-0.5">•</span>
                          <span>{a}</span>
                        </div>
                      ))}
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
