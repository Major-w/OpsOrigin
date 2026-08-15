'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { RefreshCw, ShieldAlert, CheckCircle2, GitMerge, Trash2 } from 'lucide-react'
import { cn } from '@/lib/utils'

interface QualityIssue {
  ci_id?: string
  ci_name?: string
  type_id?: string
  dimension: string
  severity: string
  message: string
  relation_id?: string
}

interface QualityReport {
  tenant_id: string
  scanned_at: string
  score: number
  by_type: { type_id: string; name: string; total: number; completeness: number; issue_count: number }[]
  issues: QualityIssue[]
}

const sevColor: Record<string, string> = {
  high: 'bg-red-100 text-red-700 dark:bg-red-500/20 dark:text-red-400',
  medium: 'bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-400',
  low: 'bg-green-100 text-green-700 dark:bg-green-500/20 dark:text-green-400',
}

const dimLabel: Record<string, string> = { completeness: '完整性', uniqueness: '唯一性', consistency: '一致性' }

export default function CMDBQualityPage() {
  const [report, setReport] = useState<QualityReport | null>(null)
  const [loading, setLoading] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      setReport(await apiFetch<QualityReport>('/cmdb/quality'))
    } catch (e: any) {
      toast.error('质量扫描失败: ' + e.message)
    } finally { setLoading(false) }
  }

  useEffect(() => { load() }, [])

  const fix = async (issue: QualityIssue) => {
    if (issue.dimension === 'uniqueness') {
      toast.info('重复 IP/名称请使用实例管理的「疑似重复」做调解合并')
      return
    }
    if (!issue.relation_id) return
    if (!confirm(`修复：${issue.message}？`)) return
    try {
      const res = await apiFetch<{ fixed: boolean; dimension: string }>('/cmdb/quality/fix', {
        method: 'POST', body: JSON.stringify({ dimension: issue.dimension, relation_id: issue.relation_id }),
      })
      toast.success(res.fixed ? '已修复孤儿关系' : '无需处理')
      load()
    } catch (e: any) { toast.error('修复失败: ' + e.message) }
  }

  const scoreColor = (s: number) => s >= 85 ? 'text-emerald-600' : s >= 60 ? 'text-amber-600' : 'text-red-600'

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><ShieldAlert className="h-6 w-6" />数据质量中心</h1>
          <p className="text-sm text-muted-foreground">完整性 / 唯一性 / 一致性扫描 — 一键修复孤儿关系</p>
        </div>
        <Button variant="outline" onClick={load} disabled={loading}><RefreshCw className={cn('h-4 w-4 mr-1', loading && 'animate-spin')} />重新扫描</Button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
        {/* 综合分 */}
        <Card>
          <CardContent className="p-6 flex items-center gap-4">
            <div className={cn('text-5xl font-bold', scoreColor(report?.score || 0))}>{report ? report.score.toFixed(0) : '—'}</div>
            <div>
              <p className="text-sm font-medium">数据质量综合分</p>
              <p className="text-xs text-muted-foreground">{report ? `${report.issues.length} 个问题` : '扫描中...'}</p>
            </div>
          </CardContent>
        </Card>

        {/* 按类型质量 */}
        <Card className="lg:col-span-2">
          <CardHeader className="py-3"><CardTitle className="text-sm">各模型类型质量</CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {report?.by_type.map(t => (
              <div key={t.type_id} className="flex items-center gap-3 text-xs">
                <span className="w-32 truncate font-medium">{t.name}</span>
                <div className="flex-1 h-2 bg-muted rounded-full overflow-hidden">
                  <div className={cn('h-full', t.completeness >= 85 ? 'bg-emerald-500' : t.completeness >= 60 ? 'bg-amber-500' : 'bg-red-500')} style={{ width: `${t.completeness}%` }} />
                </div>
                <span className="w-16 text-right text-muted-foreground">{t.completeness.toFixed(0)}%</span>
                <Badge variant={t.issue_count > 0 ? 'destructive' : 'secondary'} className="text-[10px] w-20 justify-center">{t.issue_count} 问题</Badge>
              </div>
            ))}
            {report && report.by_type.length === 0 && <p className="text-xs text-muted-foreground">暂无模型类型数据</p>}
          </CardContent>
        </Card>
      </div>

      {/* 问题清单 */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">问题清单（{report?.issues.length || 0}）</CardTitle></CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead><tr className="border-b text-left text-muted-foreground">
                <th className="pb-2 font-medium px-4 py-2">维度</th>
                <th className="pb-2 font-medium px-4 py-2">严重度</th>
                <th className="pb-2 font-medium px-4 py-2">问题</th>
                <th className="pb-2 font-medium px-4 py-2">操作</th>
              </tr></thead>
              <tbody>
                {report?.issues.map((issue, i) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="px-4 py-2"><Badge variant="outline" className="text-[10px]">{dimLabel[issue.dimension] || issue.dimension}</Badge></td>
                    <td className="px-4 py-2"><Badge className={cn('text-[10px]', sevColor[issue.severity] || sevColor.low)}>{issue.severity}</Badge></td>
                    <td className="px-4 py-2 text-xs">{issue.message}</td>
                    <td className="px-4 py-2">
                      {issue.dimension === 'consistency' && issue.relation_id ? (
                        <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => fix(issue)}><Trash2 className="h-3 w-3 mr-1" />删除</Button>
                      ) : issue.dimension === 'uniqueness' ? (
                        <Button variant="outline" size="sm" className="h-6 text-xs" onClick={() => fix(issue)}><GitMerge className="h-3 w-3 mr-1" />合并</Button>
                      ) : (
                        <span className="text-xs text-muted-foreground flex items-center gap-1"><CheckCircle2 className="h-3 w-3" />自动</span>
                      )}
                    </td>
                  </tr>
                ))}
                {report && report.issues.length === 0 && (
                  <tr><td colSpan={4} className="text-center py-6 text-muted-foreground">🎉 数据质量良好，无问题</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
