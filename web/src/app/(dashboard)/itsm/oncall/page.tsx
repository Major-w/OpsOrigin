
import { useEffect, useState } from 'react'
import { useOnCallStore } from '@/stores/oncall'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button, Tag, Input, Modal, Tabs } from 'antd'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, Calendar, Users, Bot } from 'lucide-react'

export default function OnCallPage() {
  const { shifts, rotations, currentOncall, fetchShifts, addShift, fetchRotations, createRotation, deleteRotation } = useOnCallStore()
  const [shiftDialog, setShiftDialog] = useState(false)
  const [shiftForm, setShiftForm] = useState({ user_name: '', role: 'primary', start_time: '', end_time: '', phone: '', email: '' })
  const [rotDialog, setRotDialog] = useState(false)
  const [rotForm, setRotForm] = useState({ name: '', members: '', cycle_days: 7, labels: '' })

  useEffect(() => { fetchShifts(); fetchRotations() }, [])

  const handleAddShift = async () => {
    await addShift(shiftForm)
    setShiftDialog(false); setShiftForm({ user_name: '', role: 'primary', start_time: '', end_time: '', phone: '', email: '' })
  }

  const handleCreateRotation = async () => {
    const labels: Record<string, string> = {}
    rotForm.labels.split(',').map(s => s.trim()).filter(Boolean).forEach(pair => {
      const [k, v] = pair.split('='); if (k && v) labels[k] = v
    })
    await createRotation({ ...rotForm, labels: Object.keys(labels).length ? labels : undefined, members: rotForm.members.split(',').map(s => s.trim()).filter(Boolean) })
    setRotDialog(false); setRotForm({ name: '', members: '', cycle_days: 7, labels: '' })
  }

  const now = new Date()
  const currentShifts = shifts.filter(s => new Date(s.end_time) >= now && new Date(s.start_time) <= now)
  const displayName = (m: string) => m.startsWith('agent:') ? m.slice(6) : m

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">值班管理</h1><p className="text-sm text-muted-foreground">排班与轮值</p></div>
        <div className="flex gap-2">
          <Button type="default" size="middle" onClick={() => setShiftDialog(true)}><Plus className="h-4 w-4" />添加值班</Button>
          <Button type="primary" size="middle" onClick={() => setRotDialog(true)}><Plus className="h-4 w-4" />创建轮值组</Button>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">总班次</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{shifts.length}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">当前在值</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-blue-500">{currentShifts.length}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">轮值组</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{rotations.length}</span></CardContent></Card>
      </div>

      <Tabs defaultActiveKey="shifts" items={[
        {
          key: 'shifts',
          label: <span className="inline-flex items-center gap-1.5"><Calendar className="h-4 w-4" />值班表 ({shifts.length})</span>,
          children: <div className="mt-4"><Card><CardContent className="p-0">
            {shifts.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无值班安排。</p> : (
              <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">
                <th className="pb-2 px-4">值班人</th><th className="pb-2 px-4">角色</th><th className="pb-2 px-4">开始</th><th className="pb-2 px-4">结束</th><th className="pb-2 px-4">联系方式</th>
              </tr></thead><tbody>
                {shifts.map(s => (
                  <tr key={s.id} className={`border-b last:border-0 ${new Date(s.end_time) >= now && new Date(s.start_time) <= now ? 'bg-blue-500/5' : ''}`}>
                    <td className="py-2 px-4 font-medium">{s.user_name}{new Date(s.end_time) >= now && new Date(s.start_time) <= now ? <Tag color="success" className="text-xs ml-1">当前</Tag> : null}</td>
                    <td className="py-2 px-4"><Tag color={s.role === 'primary' ? 'blue' : 'default'} className="text-xs">{s.role}</Tag></td>
                    <td className="py-2 px-4 text-xs">{formatTime(s.start_time)}</td>
                    <td className="py-2 px-4 text-xs">{formatTime(s.end_time)}</td>
                    <td className="py-2 px-4 text-xs text-muted-foreground">{s.phone || s.email || '-'}</td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </CardContent></Card></div>,
        },
        {
          key: 'rotations',
          label: <span className="inline-flex items-center gap-1.5"><Users className="h-4 w-4" />轮值组 ({rotations.length})</span>,
          children: <div className="mt-4"><Card><CardContent className="p-0">
            {rotations.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无轮值组。</p> : (
              <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">
                <th className="pb-2 px-4">名称</th><th className="pb-2 px-4">成员</th><th className="pb-2 px-4">周期(天)</th><th className="pb-2 px-4">当前当班</th><th className="pb-2 px-4">告警匹配</th><th className="pb-2 px-4 w-16"></th>
              </tr></thead><tbody>
                {rotations.map(r => (
                  <tr key={r.id} className="border-b last:border-0">
                    <td className="py-2 px-4 font-medium">{r.name}</td>
                    <td className="py-2 px-4"><div className="flex gap-1 flex-wrap">{r.members.map(m => (
                      <Tag key={m} color={m.startsWith('agent:') ? 'default' : 'default'} className="text-xs">
                        {m.startsWith('agent:') ? <><Bot className="h-3 w-3 mr-1" />{displayName(m)}</> : m}
                      </Tag>
                    ))}</div></td>
                    <td className="py-2 px-4 text-muted-foreground">{r.cycle_days}</td>
                    <td className="py-2 px-4">
                      {currentOncall[r.id]
                        ? <Tag color="success" className="text-xs gap-1">{currentOncall[r.id].startsWith('agent:') ? <Bot className="h-3 w-3" /> : null}{displayName(currentOncall[r.id])}</Tag>
                        : <span className="text-xs text-muted-foreground">-</span>}
                    </td>
                    <td className="py-2 px-4">
                      {r.labels && Object.keys(r.labels).length
                        ? <div className="flex gap-1 flex-wrap">{Object.entries(r.labels).map(([k, v]) => <Tag key={k} color="default" className="text-xs text-muted-foreground">{k}={v}</Tag>)}</div>
                        : <span className="text-xs text-muted-foreground">全部告警</span>}
                    </td>
                    <td className="py-2 px-4"><Button type="text" size="middle" className="text-red-400 hover:text-red-600 dark:text-red-400 dark:hover:text-red-300" onClick={() => deleteRotation(r.id)}><Trash2 className="h-3 w-3" /></Button></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </CardContent></Card></div>,
        },
      ]} />

      <Modal open={shiftDialog} onCancel={() => setShiftDialog(false)} title="添加值班" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">值班人 *</label><Input value={shiftForm.user_name} onChange={e => setShiftForm(f => ({ ...f, user_name: e.target.value }))} placeholder="张三" /></div>
          <div><label className="text-sm font-medium">角色</label><select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={shiftForm.role} onChange={e => setShiftForm(f => ({ ...f, role: e.target.value }))}><option value="primary">主值班</option><option value="backup">备值班</option></select></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">开始时间</label><Input type="datetime-local" value={shiftForm.start_time} onChange={e => setShiftForm(f => ({ ...f, start_time: e.target.value }))} /></div>
            <div><label className="text-sm font-medium">结束时间</label><Input type="datetime-local" value={shiftForm.end_time} onChange={e => setShiftForm(f => ({ ...f, end_time: e.target.value }))} /></div>
          </div>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button onClick={() => setShiftDialog(false)}>取消</Button><Button type="primary" onClick={handleAddShift} disabled={!shiftForm.user_name}>添加</Button></div>
      </Modal>

      <Modal open={rotDialog} onCancel={() => setRotDialog(false)} title="创建轮值组" footer={null}>
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称 *</label><Input value={rotForm.name} onChange={e => setRotForm(f => ({ ...f, name: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">成员 (逗号分隔, 数字员工用 agent:ID)</label><Input value={rotForm.members} onChange={e => setRotForm(f => ({ ...f, members: e.target.value }))} placeholder="张三,李四,agent:值班巡检员" /></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">轮换周期(天)</label><Input type="number" value={rotForm.cycle_days} onChange={e => setRotForm(f => ({ ...f, cycle_days: parseInt(e.target.value) || 7 }))} /></div>
            <div><label className="text-sm font-medium">告警匹配 (key=value,逗号分隔)</label><Input value={rotForm.labels} onChange={e => setRotForm(f => ({ ...f, labels: e.target.value }))} placeholder="component=database" /></div>
          </div>
          <p className="text-xs text-muted-foreground">告警按 labels 路由到当班成员；数字员工(agent:)当班时将自动接管告警并创建工单。</p>
        </div>
        <div className="flex justify-end gap-2 mt-4"><Button onClick={() => setRotDialog(false)}>取消</Button><Button type="primary" onClick={handleCreateRotation} disabled={!rotForm.name}>创建</Button></div>
      </Modal>
    </div>
  )
}
