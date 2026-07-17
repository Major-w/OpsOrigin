'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { formatTime, apiFetch } from '@/lib/utils'
import { Plus, Trash2 } from 'lucide-react'

export default function CMDBSearchPage() {
  const [results, setResults] = useState<any[]>([])
  const [searchQ, setSearchQ] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSearch = async () => {
    setLoading(true)
    try {
      const items = await apiFetch<any[]>('/cmdb/ci?limit=200')
      let filtered = items
      if (searchQ) {
        filtered = items.filter(i =>
          (i.name && i.name.toLowerCase().includes(searchQ.toLowerCase())) ||
          (i.description && i.description.toLowerCase().includes(searchQ.toLowerCase())) ||
          (i.type_id && i.type_id.includes(searchQ))
        )
      }
      if (typeFilter) {
        filtered = filtered.filter(i => i.type_id === typeFilter)
      }
      setResults(filtered)
    } catch { setResults([]) } finally { setLoading(false) }
  }

  return (
    <div className="p-6 space-y-4">
      <div><h1 className="text-2xl font-bold">搜索查询</h1><p className="text-sm text-muted-foreground">全量资产检索与导出</p></div>
      <div className="flex gap-2">
        <Input className="max-w-sm" placeholder="搜索资产名称/描述/类型..." value={searchQ} onChange={e => setSearchQ(e.target.value)} onKeyDown={e => e.key === 'Enter' && handleSearch()} />
        <select className="h-10 rounded-md border border-input bg-background px-3 py-2 text-sm" value={typeFilter} onChange={e => setTypeFilter(e.target.value)}>
          <option value="">所有类型</option>
          {['server','web-server','database','switch','loadbalancer','dns-server'].map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        <Button onClick={handleSearch}>搜索</Button>
      </div>
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">结果 ({results.length})</CardTitle></CardHeader>
        <CardContent className="p-0">
          {loading ? <p className="px-4 py-4 text-sm text-muted-foreground">搜索中...</p> : results.length === 0 ? <p className="px-4 py-4 text-sm text-muted-foreground">输入关键词开始搜索。</p> : (
            <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground"><th className="pb-2 px-4">名称</th><th className="pb-2 px-4">类型</th><th className="pb-2 px-4">状态</th><th className="pb-2 px-4">更新</th></tr></thead>
              <tbody>{results.map(i => (
                <tr key={i.id} className="border-b last:border-0"><td className="py-2 px-4 font-medium">{i.name}</td><td className="py-2 px-4"><Badge variant="secondary" className="text-xs">{i.type_id}</Badge></td><td className="py-2 px-4"><Badge className="text-xs">{i.status}</Badge></td><td className="py-2 px-4 text-muted-foreground text-xs">{formatTime(i.updated_at)}</td></tr>
              ))}</tbody>
            </table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
