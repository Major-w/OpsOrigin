
import { useEffect, useState, useRef } from 'react'
import { useFileStore, type FileItem } from '@/stores/file'
import { useAgentStore } from '@/stores/agent'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Badge, Input, Modal, Tag, Checkbox } from 'antd'
import { apiFetch } from '@/lib/utils'
import { Upload, Trash2, Send, FileText, HardDrive, RefreshCw, X, CheckCircle, Loader2 } from 'lucide-react'
import { getMessage } from '@/lib/message'

function fmtSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

export default function SoftwarePage() {
  const { files, loading, fetch, upload, remove } = useFileStore()
  const { agents, fetch: fetchAgents } = useAgentStore()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [distFile, setDistFile] = useState<FileItem | null>(null)
  const [selectedAgents, setSelectedAgents] = useState<string[]>([])
  const [distPath, setDistPath] = useState('/tmp/')
  const [distributing, setDistributing] = useState(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => { fetch(); fetchAgents('') }, [])

  const handleUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0]; if (!f) return
    await upload(f); if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleDistribute = async () => {
    if (!distFile || selectedAgents.length === 0) return
    setDistributing(true)
    try {
      const fileUrl = `/api/v1/files/${distFile.id}`
      let ok = 0, errs: string[] = []
      for (const aid of selectedAgents) {
        try {
          await apiFetch(`/agents/${aid}/download-tasks`, {
            method: 'POST',
            body: JSON.stringify({
              action: 'download',
              file_id: distFile.id,
              file_url: fileUrl,
              file_path: `${distPath}/${distFile.original_name}`.replace(/\/+/g, '/'),
              expected_checksum: distFile.checksum,
              checksum_algo: distFile.checksum_algo,
              timeout: 600,
            }),
          })
          ok++
        } catch (e: any) { errs.push(e.message) }
      }
      getMessage().success(`已分发到 ${ok} 个节点` + (errs.length ? `, ${errs.length} 失败` : ''))
      setDialogOpen(false)
    } finally { setDistributing(false) }
  }

  const onlineAgents = agents.filter(a => a.type === 'agent' && a.status === 'online')

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">文件分发</h1>
          <p className="text-sm text-muted-foreground">上传文件并批量分发到 Agent 节点</p>
        </div>
        <div className="flex gap-2">
          <Button onClick={fetch} disabled={loading}>
            <RefreshCw className={`h-4 w-4 mr-1 ${loading ? 'animate-spin' : ''}`} />刷新
          </Button>
          <Button type="primary" onClick={() => fileInputRef.current?.click()}>
            <Upload className="h-4 w-4 mr-1" />上传文件
          </Button>
          <input ref={fileInputRef} type="file" className="hidden" onChange={handleUpload} />
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground">文件总数</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{files.length}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><HardDrive className="h-3 w-3 inline mr-1" />总大小</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{fmtSize(files.reduce((s, f) => s + f.size_bytes, 0))}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-xs text-muted-foreground"><Send className="h-3 w-3 inline mr-1" />可分发节点</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-blue-500">{onlineAgents.length}</span></CardContent></Card>
      </div>

      <Card>
        <CardContent className="p-0">
          {files.length === 0 ? (
            <div className="py-16 text-center space-y-3">
              <FileText className="h-12 w-12 mx-auto text-muted-foreground/30" />
              <p className="text-sm text-muted-foreground">暂无文件，请上传文件后开始分发</p>
              <Button onClick={() => fileInputRef.current?.click()}>
                <Upload className="h-3 w-3 mr-1" />上传文件
              </Button>
            </div>
          ) : (
            <table className="w-full text-sm">
              <thead className="bg-muted/40">
                <tr>
                  <th className="text-left p-3 font-medium">文件名</th>
                  <th className="text-left p-3 font-medium">大小</th>
                  <th className="text-left p-3 font-medium">校验和 (SHA256)</th>
                  <th className="text-left p-3 font-medium">上传时间</th>
                  <th className="text-right p-3 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {files.map(f => (
                  <tr key={f.id} className="border-t hover:bg-muted/30">
                    <td className="p-3 font-mono text-xs">{f.original_name}</td>
                    <td className="p-3 text-xs text-muted-foreground">{fmtSize(f.size_bytes)}</td>
                    <td className="p-3 text-[10px] font-mono text-muted-foreground max-w-[200px] truncate" title={f.checksum}>{f.checksum?.substring(0, 16)}...</td>
                    <td className="p-3 text-xs text-muted-foreground">{f.created_at ? new Date(f.created_at).toLocaleString() : '-'}</td>
                    <td className="p-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <Button type="text" size="middle" onClick={() => { setDistFile(f); setDialogOpen(true); setDistPath(`/tmp/${f.original_name}`); setSelectedAgents([]) }}>
                          <Send className="h-3 w-3 mr-1" />分发
                        </Button>
                        <Button type="text" danger size="middle" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => remove(f.id)}>
                          <Trash2 className="h-3 w-3" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>

      <Modal title="分发文件到节点" open={dialogOpen} onCancel={() => setDialogOpen(false)} footer={null}>
        {distFile && (
          <div className="space-y-4">
            <div className="flex items-center gap-2 text-sm">
              <FileText className="h-4 w-4" />
              <span className="font-medium">{distFile.original_name}</span>
              <Tag className="text-[10px]">{fmtSize(distFile.size_bytes)}</Tag>
            </div>
            <div>
              <label className="text-sm font-medium">目标路径</label>
              <Input value={distPath} onChange={e => setDistPath(e.target.value)} placeholder="/opt/app/file.tar.gz" className="mt-1" />
            </div>
            <div>
              <label className="text-sm font-medium mb-2 block">目标节点 ({selectedAgents.length} 选中)</label>
              <div className="max-h-48 overflow-auto border rounded-md">
                {onlineAgents.length === 0 ? (
                  <p className="p-4 text-sm text-muted-foreground">无在线 Agent 节点</p>
                ) : (
                  onlineAgents.map(a => (
                    <label key={a.id} className="flex items-center gap-2 px-3 py-2 hover:bg-muted/30 cursor-pointer border-b last:border-b-0">
                      <Checkbox checked={selectedAgents.includes(a.id)} onChange={e => {
                        if (e.target.checked) setSelectedAgents(s => [...s, a.id])
                        else setSelectedAgents(s => s.filter(x => x !== a.id))
                      }} />
                      <span className="text-sm">{a.name || a.id} · {a.address}</span>
                      <Tag className="text-[10px] ml-auto">{a.version || '-'}</Tag>
                    </label>
                  ))
                )}
              </div>
            </div>
            <div className="flex justify-end gap-2 mt-4">
              <Button onClick={() => setDialogOpen(false)}><X className="h-3 w-3 mr-1" />取消</Button>
              <Button type="primary" onClick={handleDistribute} disabled={selectedAgents.length === 0 || distributing}>
                {distributing ? <Loader2 className="h-3 w-3 mr-1 animate-spin" /> : <Send className="h-3 w-3 mr-1" />}
                {distributing ? '分发中...' : `分发到 ${selectedAgents.length} 节点`}
              </Button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  )
}
