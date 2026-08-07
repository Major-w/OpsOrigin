'use client'

// 员工自助门户 —— 三门户的「员工自助」形态：
// 服务目录浏览(分类卡片) + 一键申请(跳工单页自动套用目录 scope) + 我的工单(进度查询)。
import { useEffect, useState, useCallback, useMemo } from 'react'
import { useRouter } from 'next/navigation'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { apiList } from '@/lib/utils'
import { Package, ChevronRight, Loader2, FolderTree } from 'lucide-react'

interface ServiceItem {
  id: string
  name: string
  description?: string
  category: string
  parent_id?: string
  level?: number
  sort_order?: number
  process_id?: string
  enabled: boolean
}

const statusLabels: Record<string, string> = {
  draft: '草稿', pending_review: '待审批', approved: '已审批', rejected: '已驳回',
  executing: '执行中', succeeded: '已完成', failed: '已失败', rolled_back: '已回滚',
  closed: '已关闭', withdrawn: '已撤回', reopened: '已重开',
}
const riskLabels: Record<string, string> = { critical: '致命', high: '高危', medium: '中危', low: '低危' }
const riskColor: Record<string, string> = { critical: 'bg-red-500', high: 'bg-amber-500', medium: 'bg-blue-500', low: 'bg-emerald-500' }
const categoryLabels: Record<string, string> = { change: '变更申请', incident: '故障报修', problem: '问题反馈', request: '服务请求' }

export default function ITSMPortalPage() {
  const router = useRouter()
  const [services, setServices] = useState<ServiceItem[]>([])
  const [loading, setLoading] = useState(true)

  // 门户只拉「已上线 且 当前用户在服务对象范围内」的目录项：
  // 过滤在服务端完成(catalog/visible)，前端拿不到越权目录，避免可见范围靠前端自觉。
  // 「我的工单」已收敛到工单列表页的「我的工单」筛选，门户只聚焦服务申请。
  const fetchAll = useCallback(async () => {
    setLoading(true)
    try {
      const svc = await apiList<ServiceItem>('/itsm/catalog/visible')
      setServices(svc || [])
    } catch { /* ignore */ }
    setLoading(false)
  }, [])

  useEffect(() => { fetchAll() }, [fetchAll])

  const apply = (item: ServiceItem) => {
    router.push(`/itsm/tickets?catalog=${item.id}`)
  }

  // 多级目录：把门户可见的扁平列表构造成树；父级不在可见集合(被下线/无权限)的子项提升为一级，避免服务丢失。
  interface PortalNode { item: ServiceItem; children: PortalNode[]; depth: number }
  const buildPortalTree = (src: ServiceItem[]): PortalNode[] => {
    const byId = new Map(src.map(s => [s.id, s]))
    const map = new Map<string, PortalNode>()
    src.forEach(s => map.set(s.id, { item: s, children: [], depth: 0 }))
    const roots: PortalNode[] = []
    src.forEach(s => {
      const node = map.get(s.id)!
      if (s.parent_id && byId.has(s.parent_id)) map.get(s.parent_id)!.children.push(node)
      else roots.push(node)
    })
    const depthOf = (it: ServiceItem): number => {
      let d = 0; let cur = it.parent_id
      while (cur && byId.has(cur)) { d++; cur = byId.get(cur)!.parent_id }
      return d
    }
    const sortNodes = (ns: PortalNode[]) => {
      ns.sort((a, b) => (a.item.sort_order || 0) - (b.item.sort_order || 0) || a.item.name.localeCompare(b.item.name))
      ns.forEach(n => { n.depth = depthOf(n.item); sortNodes(n.children) })
    }
    sortNodes(roots)
    return roots
  }
  const tree = useMemo(() => buildPortalTree(services), [services])

  const renderTreeNode = (node: PortalNode): JSX.Element => {
    const it = node.item
    const isFolder = node.children.length > 0
    if (isFolder) {
      return (
        <div key={it.id} style={{ marginLeft: `${node.depth * 18}px` }}>
          <div className="flex items-center gap-2 py-2 px-2 mt-1 rounded-md bg-muted/40">
            <FolderTree className="h-4 w-4 text-primary" />
            <span className="text-sm font-semibold">{it.name}</span>
            <span className="text-[10px] text-muted-foreground">{node.children.length} 项</span>
          </div>
          <div className="space-y-2 mt-1">{node.children.map(c => renderTreeNode(c))}</div>
        </div>
      )
    }
    return (
      <div key={it.id} style={{ marginLeft: `${node.depth * 18}px` }}>
        <div role="button" tabIndex={0}
          onClick={() => apply(it)}
          onKeyDown={e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); apply(it) } }}
          className="border rounded-lg p-4 cursor-pointer hover:border-primary/60 hover:shadow-md hover:-translate-y-0.5 transition-all duration-200 group focus:outline-none focus:ring-2 focus:ring-primary/40">
          <div className="flex items-start justify-between">
            <span className={`mt-1 h-2 w-2 rounded-full ${riskColor[it.category === 'change' ? 'medium' : 'low']}`} />
            <Badge variant="outline" className="text-[10px]">{it.process_id ? '含流程' : '直通'}</Badge>
          </div>
          <h5 className="font-medium text-sm mt-1 group-hover:text-primary transition-colors">{it.name}</h5>
          {it.description && <p className="text-xs text-muted-foreground mt-1 line-clamp-2">{it.description}</p>}
          <Button size="sm" variant="outline" className="mt-3 w-full pointer-events-none group-hover:border-primary/60 group-hover:text-primary">
            {it.process_id ? '发起流程' : '发起申请'} <ChevronRight className="h-3 w-3 group-hover:translate-x-0.5 transition-transform" />
          </Button>
        </div>
      </div>
    )
  }

  return (
    <div className="p-6 space-y-5">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">自助服务门户</h1>
          <p className="text-sm text-muted-foreground">浏览服务目录发起申请（我的工单请在「ITSM 工单」页用「我的工单」筛选查看）</p>
        </div>
      </div>

      {/* 服务目录（多级） */}
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm flex items-center gap-1.5"><Package className="h-3.5 w-3.5" />服务目录</CardTitle></CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-10 text-muted-foreground text-sm"><Loader2 className="h-4 w-4 animate-spin mr-2" />加载中…</div>
          ) : services.length === 0 ? (
            <p className="text-sm text-muted-foreground py-6 text-center">暂无可用服务，请联系管理员在「服务目录定义」中配置。</p>
          ) : (
            <div className="space-y-1">{tree.map(n => renderTreeNode(n))}</div>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
