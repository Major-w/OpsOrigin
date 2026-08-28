
import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Select } from 'antd'
import { formatTime, apiFetch, apiList } from '@/lib/utils'
import { Plus, Trash2 } from 'lucide-react'

export default function CMDBSearchPage() {
  const [results, setResults] = useState<any[]>([])
  const [searchQ, setSearchQ] = useState('')
  const [typeFilter, setTypeFilter] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSearch = async () => {
    setLoading(true)
    try {
      const items = await apiList<any>('/cmdb/ci?limit=200')
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
        <Select className="max-w-[200px]" value={typeFilter || undefined} onChange={v => setTypeFilter(v || '')} options={[{ value: '', label: '所有类型' }, ...['server','web-server','database','switch','loadbalancer','dns-server'].map(t => ({ value: t, label: t }))]} />
        <Button onClick={handleSearch}>搜索</Button>
      </div>
      <Card>
        <CardHeader className="py-3"><CardTitle className="text-sm">结果 ({results.length})</CardTitle></CardHeader>
        <CardContent className="p-0">
          {loading ? <p className="px-4 py-4 text-sm text-muted-foreground">搜索中...</p> : results.length === 0 ? <p className="px-4 py-4 text-sm text-muted-foreground">输入关键词开始搜索。</p> : (
            <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground"><th className="pb-2 px-4">名称</th><th className="pb-2 px-4">类型</th><th className="pb-2 px-4">状态</th><th className="pb-2 px-4">更新</th></tr></thead>
              <tbody>{results.map(i => (
                <tr key={i.id} className="border-b last:border-0"><td className="py-2 px-4 font-medium">{i.name}</td><td className="py-2 px-4">                <Tag className="text-xs">{i.type_id}</Tag></td><td className="py-2 px-4">                <Tag className="text-xs">{i.status}</Tag></td><td className="py-2 px-4 text-muted-foreground text-xs">{formatTime(i.updated_at)}</td></tr>
              ))}</tbody>
            </table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
