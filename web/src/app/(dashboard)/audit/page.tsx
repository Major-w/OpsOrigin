
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useAuditStore, type AuditEvent } from '@/stores/audit'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag, Button, Input, Modal } from 'antd'
import { formatTime } from '@/lib/utils'
import { cn } from '@/lib/utils'
import { Search, RotateCcw, ChevronLeft, ChevronRight, Download, FileText, ShieldCheck, Loader2, Eye } from 'lucide-react'

const ACTOR_TYPES = ['HUMAN', 'AGENT', 'SYSTEM', 'API', 'SCHEDULER']

export default function AuditPage() {
  const { events, total, loading, fetch, fetchEventTypes } = useAuditStore()

  // —— 筛选状态 ——
  const [q, setQ] = useState('')
  const [eventType, setEventType] = useState('')
  const [actorType, setActorType] = useState('')
  const [success, setSuccess] = useState('')
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [page, setPage] = useState(1)
  const [eventTypes, setEventTypes] = useState<string[]>([])

  // —— 详情弹窗 ——
  const [detail, setDetail] = useState<AuditEvent | null>(null)

  const pageSize = 50

  const load = useCallback(async (p: number) => {
    await fetch({
      q: q || undefined,
      event_type: eventType || undefined,
      actor_type: actorType || undefined,
      success: success || undefined,
      from: from || undefined,
      to: to || undefined,
      page: p,
      page_size: pageSize,
    })
  }, [q, eventType, actorType, success, from, to, fetch])

  useEffect(() => { load(1) }, [load])
  useEffect(() => {
    fetchEventTypes().then(setEventTypes)
  }, [fetchEventTypes])

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  const handleSearch = () => { setPage(1); load(1) }
  const handleReset = () => {
    setQ(''); setEventType(''); setActorType(''); setSuccess(''); setFrom(''); setTo('')
    setPage(1)
    load(1)
  }

  // —— CSV 导出(当前筛选结果, 最多导出当前页) ——
  const handleExport = () => {
    if (events.length === 0) return
    const header = ['时间', '事件类型', '操作主体类型', '操作主体', '目标', '动作', '结果', '错误', 'IP', 'Trace ID']
    const rows = events.map(e => [
      formatTime(e.timestamp),
      e.event_type,
      e.actor?.type || '',
      e.actor?.display_name || e.actor?.id || '',
      e.target?.name || `${e.target?.type || ''}/${e.target?.id || ''}`,
      e.action || '',
      e.success ? '成功' : '失败',
      e.error || '',
      e.ip_address || '',
      e.trace_id || '',
    ])
    const csv = '\uFEFF' + [header, ...rows].map(r => r.map(v => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(a.href)
  }

  // 表格列(移动端隐藏次要列)
  const actorLabel = (e: AuditEvent) => e.actor?.display_name || e.actor?.id || '-'
  const targetLabel = (e: AuditEvent) => e.target?.name || (e.target?.type && e.target?.id ? `${e.target.type}/${e.target.id.slice(0, 8)}` : '-')

  const detailRows = useMemo(() => {
    if (!detail) return []
    const rows: [string, string][] = [
      ['时间', formatTime(detail.timestamp)],
      ['Trace ID', detail.trace_id || '-'],
      ['事件类型', detail.event_type],
      ['操作主体', `${detail.actor?.type || ''} ${detail.actor?.display_name || detail.actor?.id || ''}`],
      ['目标', targetLabel(detail)],
      ['动作', detail.action || '-'],
      ['结果', detail.success ? '成功' : '失败'],
      ['IP 地址', detail.ip_address || '-'],
      ['User-Agent', detail.user_agent || '-'],
    ]
    if (detail.error) rows.push(['错误信息', detail.error])
    return rows
  }, [detail])

  const detailJSON = useMemo(() => {
    if (!detail) return ''
    const obj: any = {}
    if (detail.request !== undefined) obj.request = detail.request
    if (detail.response !== undefined) obj.response = detail.response
    if (detail.error) obj.error = detail.error
    return Object.keys(obj).length ? JSON.stringify(obj, null, 2) : ''
  }, [detail])

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold flex items-center gap-2"><ShieldCheck className="h-6 w-6 text-blue-500" />审计日志</h1>
          <p className="text-sm text-muted-foreground">全链路操作追踪与安全审计</p>
        </div>
        <Button type="default" size="middle" onClick={handleExport} disabled={events.length === 0}>
          <Download className="h-4 w-4 mr-1" />导出 CSV
        </Button>
      </div>

      {/* 筛选栏 */}
      <Card>
        <CardContent className="p-3 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative flex-1 min-w-[200px]">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
              <Input
                placeholder="搜索动作 / 目标名称 / Trace ID..."
                value={q}
                onChange={e => setQ(e.target.value)}
                onKeyDown={e => e.key === 'Enter' && handleSearch()}
                className="pl-8"
              />
            </div>
            <select value={eventType} onChange={e => setEventType(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm max-w-[160px]">
              <option value="">全部类型</option>
              {eventTypes.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
            <select value={actorType} onChange={e => setActorType(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm max-w-[140px]">
              <option value="">全部主体</option>
              {ACTOR_TYPES.map(t => <option key={t} value={t}>{t}</option>)}
            </select>
            <select value={success} onChange={e => setSuccess(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm max-w-[110px]">
              <option value="">全部结果</option>
              <option value="true">成功</option>
              <option value="false">失败</option>
            </select>
            <input type="date" value={from} onChange={e => setFrom(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm" title="开始日期" />
            <span className="text-xs text-muted-foreground">至</span>
            <input type="date" value={to} onChange={e => setTo(e.target.value)} className="h-10 rounded-md border border-input bg-background px-2 text-sm" title="结束日期" />
            <Button size="middle" onClick={handleSearch} disabled={loading}>
              {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin mr-1" /> : <Search className="h-3.5 w-3.5 mr-1" />}查询
            </Button>
            <Button size="middle" type="default" onClick={handleReset}><RotateCcw className="h-3.5 w-3.5 mr-1" />重置</Button>
          </div>
          <p className="text-[11px] text-muted-foreground">共 {total} 条记录（当前第 {page} / {totalPages} 页）</p>
        </CardContent>
      </Card>

      {/* 事件表格 */}
      <Card>
        <CardHeader className="pb-2"><CardTitle className="text-base flex items-center gap-2"><FileText className="h-4 w-4 text-blue-500" />事件历史</CardTitle></CardHeader>
        <CardContent>
          {loading ? (
            <p className="text-sm text-muted-foreground py-8 text-center flex items-center justify-center gap-2"><Loader2 className="h-4 w-4 animate-spin" />加载中...</p>
          ) : events.length === 0 ? (
            <p className="text-sm text-muted-foreground py-8 text-center">暂无匹配的审计事件。</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b text-left text-muted-foreground">
                    <th className="pb-2 font-medium">时间</th>
                    <th className="pb-2 font-medium">事件类型</th>
                    <th className="pb-2 font-medium">操作主体</th>
                    <th className="pb-2 font-medium hidden md:table-cell">目标</th>
                    <th className="pb-2 font-medium">动作</th>
                    <th className="pb-2 font-medium">结果</th>
                    <th className="pb-2 font-medium">详情</th>
                  </tr>
                </thead>
                <tbody>
                  {events.map(e => (
                    <tr key={e.id} className="border-b last:border-0 hover:bg-gray-50 dark:hover:bg-white/[0.02] cursor-pointer transition-colors" onClick={() => setDetail(e)}>
                      <td className="py-2 text-xs text-muted-foreground whitespace-nowrap">{formatTime(e.timestamp)}</td>
                      <td className="py-2"><Tag color="default">{e.event_type}</Tag></td>
                      <td className="py-2">
                        <div className="flex items-center gap-1.5">
                          <Tag color={e.actor?.type === 'HUMAN' ? 'default' : 'default'}>{e.actor?.type || '-'}</Tag>
                          <span className="text-xs truncate max-w-[120px]">{actorLabel(e)}</span>
                        </div>
                      </td>
                      <td className="py-2 text-xs text-muted-foreground hidden md:table-cell truncate max-w-[160px]">{targetLabel(e)}</td>
                      <td className="py-2 font-mono text-xs truncate max-w-[180px]">{e.action || '-'}</td>
                      <td className="py-2"><Tag color={e.success ? 'success' : 'error'}>{e.success ? '成功' : '失败'}</Tag></td>
                      <td className="py-2"><Eye className="h-3.5 w-3.5 text-muted-foreground" /></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {/* 分页 */}
          {total > pageSize && (
            <div className="flex items-center justify-between pt-4">
              <Button type="default" size="middle" onClick={() => { setPage(p => p - 1); load(page - 1) }} disabled={page <= 1}>
                <ChevronLeft className="h-3.5 w-3.5 mr-1" />上一页
              </Button>
              <span className="text-xs text-muted-foreground">{page} / {totalPages}</span>
              <Button type="default" size="middle" onClick={() => { setPage(p => p + 1); load(page + 1) }} disabled={page >= totalPages}>
                下一页<ChevronRight className="h-3.5 w-3.5 ml-1" />
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 详情弹窗 */}
      <Modal title="审计事件详情" open={!!detail} onCancel={() => setDetail(null)} footer={<Button type="default" onClick={() => setDetail(null)}>关闭</Button>}>
        {detail && (
          <div className="space-y-3 text-sm">
            <div className="grid grid-cols-2 gap-x-4 gap-y-2">
              {detailRows.map(([k, v]) => (
                <div key={k} className={cn('col-span-2', k === '动作' || k === '错误信息' || k === 'User-Agent' ? '' : 'md:col-span-1')}>
                  <span className="text-xs text-muted-foreground">{k}：</span>
                  <span className="text-xs break-all">{v}</span>
                </div>
              ))}
            </div>
            {detailJSON && (
              <div>
                <span className="text-xs text-muted-foreground">请求 / 响应数据：</span>
                <pre className="mt-1 p-2 rounded bg-gray-100 dark:bg-gray-800 text-[11px] overflow-x-auto whitespace-pre-wrap max-h-48 overflow-y-auto">{detailJSON}</pre>
              </div>
            )}
          </div>
        )}
        </Modal>
    </div>
  )
}
