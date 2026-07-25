'use client'

import { useEffect, useState } from 'react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { useScriptStore } from '@/stores/script'
import { useAgentStore } from '@/stores/agent'
import { apiFetch } from '@/lib/utils'
import { toast } from '@/stores/toast'
import { Download, Play, CheckCircle, XCircle, Terminal } from 'lucide-react'

export default function PatchPage() {
  const { scripts, fetch: fetchScripts } = useScriptStore()
  const { agents, fetch: fetchAgents } = useAgentStore()

  const [scriptId, setScriptId] = useState('')
  const [agentIds, setAgentIds] = useState<string[]>([])
  const [params, setParams] = useState('')
  const [running, setRunning] = useState(false)
  const [results, setResults] = useState<any[]>([])
  const [patchLog, setPatchLog] = useState<{ agent: string; status: string; output: string; time: string }[]>([])

  useEffect(() => { fetchScripts(); fetchAgents() }, [])

  const run = async () => {
    if (!scriptId || agentIds.length === 0) { toast.error('请选择补丁脚本和目标主机'); return }
    setRunning(true); setResults([])
    const newLog: typeof patchLog = []
    for (const aid of agentIds) {
      try {
        const r = await apiFetch<any>(`/scripts/${scriptId}/execute`, {
          method: 'POST',
          body: JSON.stringify({ agent_id: aid, timeout: 900, params: parseParams(params) }),
        })
        newLog.push({ agent: agents.find(a=>a.id===aid)?.name||aid.slice(0,8), status: r.exit_code===0?'success':'failed', output: r.stdout||r.stderr||'', time: new Date().toLocaleTimeString() })
      } catch (e: any) {
        newLog.push({ agent: agents.find(a=>a.id===aid)?.name||aid.slice(0,8), status: 'error', output: e.message, time: new Date().toLocaleTimeString() })
      }
    }
    setPatchLog(prev => [...newLog, ...prev].slice(0, 50))
    setRunning(false)
    toast.success(`补丁执行完成: ${newLog.filter(l=>l.status==='success').length}/${newLog.length} 成功`)
  }

  const parseParams = (p: string) => { try { return JSON.parse(p) } catch { return {} } }
  const onlineAgents = agents.filter(a => a.status === 'online')

  return <div className="p-6 space-y-4">
    <div><h1 className="text-xl font-bold">补丁安装</h1><p className="text-xs text-muted-foreground">选择补丁脚本在指定主机上批量执行</p></div>

    <div className="grid md:grid-cols-2 gap-4">
      <Card><CardHeader className="py-3"><CardTitle className="text-sm">执行配置</CardTitle></CardHeader>
        <CardContent className="space-y-3">
          <div><label className="text-xs font-medium">补丁脚本</label>
            <select className="flex h-8 w-full rounded-md border border-input bg-background px-2 text-xs mt-1" value={scriptId} onChange={e=>setScriptId(e.target.value)}>
              <option value="">选择脚本</option>{scripts.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}
            </select>
          </div>
          <div><label className="text-xs font-medium">参数 (JSON)</label><Input value={params} onChange={e=>setParams(e.target.value)} placeholder='{"version":"2.1.0"}' className="h-8 mt-1 text-xs font-mono"/></div>
          <div><label className="text-xs font-medium">目标主机 ({agentIds.length} 已选)</label>
            <div className="max-h-32 overflow-y-auto border rounded-md mt-1 p-1">
              {onlineAgents.length===0?<p className="text-[10px] text-muted-foreground p-2">无在线Agent</p>:
              onlineAgents.map(a=><label key={a.id} className="flex items-center gap-1.5 text-[11px] cursor-pointer hover:bg-accent rounded px-1 py-0.5">
                <input type="checkbox" checked={agentIds.includes(a.id)} onChange={()=>setAgentIds(prev=>prev.includes(a.id)?prev.filter(i=>i!==a.id):[...prev,a.id])}/>
                {a.name||a.id.slice(0,8)} ({a.address})
              </label>)}
            </div>
          </div>
          <Button onClick={run} disabled={running||!scriptId||agentIds.length===0}><Play className="h-4 w-4 mr-1"/>{running?'执行中...':'批量执行'}</Button>
        </CardContent>
      </Card>

      <Card><CardHeader className="py-3"><CardTitle className="text-sm">执行历史</CardTitle></CardHeader>
        <CardContent>
          {patchLog.length===0?<p className="text-xs text-muted-foreground">暂无记录</p>:
          <div className="space-y-1 max-h-72 overflow-y-auto">{
            patchLog.map((l, i) => <div key={i} className="flex items-start gap-2 text-xs p-1.5 rounded bg-muted/30">
              {l.status==='success'?<CheckCircle className="h-3 w-3 text-emerald-500 mt-0.5 shrink-0"/>:<XCircle className="h-3 w-3 text-red-500 mt-0.5 shrink-0"/>}
              <div className="min-w-0"><span className="font-medium">{l.agent}</span> <span className="text-muted-foreground">{l.time}</span>
                {l.output && <pre className="text-[10px] mt-0.5 truncate max-w-[300px]">{l.output}</pre>}
              </div>
            </div>)
          }</div>}
        </CardContent>
      </Card>
    </div>
  </div>
}
