'use client'

import { useEffect, useState } from 'react'
import { useOnCallStore } from '@/stores/oncall'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Dialog, DialogFooter } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { formatTime } from '@/lib/utils'
import { Plus, Trash2, Calendar, Users } from 'lucide-react'

export default function OnCallPage() {
  const { shifts, rotations, fetchShifts, addShift, fetchRotations, createRotation, deleteRotation } = useOnCallStore()
  const [shiftDialog, setShiftDialog] = useState(false)
  const [shiftForm, setShiftForm] = useState({ user_name: '', role: 'primary', start_time: '', end_time: '', phone: '', email: '' })
  const [rotDialog, setRotDialog] = useState(false)
  const [rotForm, setRotForm] = useState({ name: '', members: '', cycle_days: 7 })

  useEffect(() => { fetchShifts(); fetchRotations() }, [])

  const handleAddShift = async () => {
    await addShift(shiftForm)
    setShiftDialog(false); setShiftForm({ user_name: '', role: 'primary', start_time: '', end_time: '', phone: '', email: '' })
  }

  const handleCreateRotation = async () => {
    await createRotation({ ...rotForm, members: rotForm.members.split(',').map(s => s.trim()).filter(Boolean) })
    setRotDialog(false); setRotForm({ name: '', members: '', cycle_days: 7 })
  }

  const now = new Date()
  const currentShifts = shifts.filter(s => new Date(s.end_time) >= now && new Date(s.start_time) <= now)

  return (
    <div className="p-6 space-y-4">
      <div className="flex items-center justify-between">
        <div><h1 className="text-2xl font-bold">值班管理</h1><p className="text-sm text-muted-foreground">排班与轮值</p></div>
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => setShiftDialog(true)}><Plus className="h-4 w-4" />添加值班</Button>
          <Button size="sm" onClick={() => setRotDialog(true)}><Plus className="h-4 w-4" />创建轮值组</Button>
        </div>
      </div>

      <div className="grid grid-cols-3 gap-3">
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">总班次</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{shifts.length}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">当前在值</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold text-emerald-500">{currentShifts.length}</span></CardContent></Card>
        <Card><CardHeader className="py-3"><CardTitle className="text-sm text-muted-foreground">轮值组</CardTitle></CardHeader><CardContent className="pt-0"><span className="text-2xl font-bold">{rotations.length}</span></CardContent></Card>
      </div>

      <Tabs defaultValue="shifts">
        <TabsList>
          <TabsTrigger value="shifts"><Calendar className="h-3 w-3 mr-1" />值班表 ({shifts.length})</TabsTrigger>
          <TabsTrigger value="rotations"><Users className="h-3 w-3 mr-1" />轮值组 ({rotations.length})</TabsTrigger>
        </TabsList>

        <TabsContent value="shifts" className="mt-4">
          <Card><CardContent className="p-0">
            {shifts.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无值班安排。</p> : (
              <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">
                <th className="pb-2 px-4">值班人</th><th className="pb-2 px-4">角色</th><th className="pb-2 px-4">开始</th><th className="pb-2 px-4">结束</th><th className="pb-2 px-4">联系方式</th>
              </tr></thead><tbody>
                {shifts.map(s => (
                  <tr key={s.id} className={`border-b last:border-0 ${new Date(s.end_time) >= now && new Date(s.start_time) <= now ? 'bg-emerald-500/5' : ''}`}>
                    <td className="py-2 px-4 font-medium">{s.user_name}{new Date(s.end_time) >= now && new Date(s.start_time) <= now ? <Badge variant="success" className="text-xs ml-1">当前</Badge> : null}</td>
                    <td className="py-2 px-4"><Badge variant={s.role === 'primary' ? 'default' : 'outline'} className="text-xs">{s.role}</Badge></td>
                    <td className="py-2 px-4 text-xs">{formatTime(s.start_time)}</td>
                    <td className="py-2 px-4 text-xs">{formatTime(s.end_time)}</td>
                    <td className="py-2 px-4 text-xs text-muted-foreground">{s.phone || s.email || '-'}</td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </CardContent></Card>
        </TabsContent>

        <TabsContent value="rotations" className="mt-4">
          <Card><CardContent className="p-0">
            {rotations.length === 0 ? <p className="px-4 py-8 text-sm text-muted-foreground text-center">暂无轮值组。</p> : (
              <table className="w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">
                <th className="pb-2 px-4">名称</th><th className="pb-2 px-4">成员</th><th className="pb-2 px-4">周期(天)</th><th className="pb-2 px-4 w-16"></th>
              </tr></thead><tbody>
                {rotations.map(r => (
                  <tr key={r.id} className="border-b last:border-0">
                    <td className="py-2 px-4 font-medium">{r.name}</td>
                    <td className="py-2 px-4"><div className="flex gap-1 flex-wrap">{r.members.map(m => <Badge key={m} variant="outline" className="text-xs">{m}</Badge>)}</div></td>
                    <td className="py-2 px-4 text-muted-foreground">{r.cycle_days}</td>
                    <td className="py-2 px-4"><Button variant="ghost" size="icon" className="h-6 w-6" onClick={() => deleteRotation(r.id)}><Trash2 className="h-3 w-3" /></Button></td>
                  </tr>
                ))}</tbody>
              </table>
            )}
          </CardContent></Card>
        </TabsContent>
      </Tabs>

      <Dialog open={shiftDialog} onClose={() => setShiftDialog(false)} title="添加值班">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">值班人 *</label><Input value={shiftForm.user_name} onChange={e => setShiftForm(f => ({ ...f, user_name: e.target.value }))} placeholder="张三" /></div>
          <div><label className="text-sm font-medium">角色</label><select className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm" value={shiftForm.role} onChange={e => setShiftForm(f => ({ ...f, role: e.target.value }))}><option value="primary">主值班</option><option value="backup">备值班</option></select></div>
          <div className="grid grid-cols-2 gap-3">
            <div><label className="text-sm font-medium">开始时间</label><Input type="datetime-local" value={shiftForm.start_time} onChange={e => setShiftForm(f => ({ ...f, start_time: e.target.value }))} /></div>
            <div><label className="text-sm font-medium">结束时间</label><Input type="datetime-local" value={shiftForm.end_time} onChange={e => setShiftForm(f => ({ ...f, end_time: e.target.value }))} /></div>
          </div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setShiftDialog(false)}>取消</Button><Button onClick={handleAddShift} disabled={!shiftForm.user_name}>添加</Button></DialogFooter>
      </Dialog>

      <Dialog open={rotDialog} onClose={() => setRotDialog(false)} title="创建轮值组">
        <div className="space-y-3">
          <div><label className="text-sm font-medium">名称 *</label><Input value={rotForm.name} onChange={e => setRotForm(f => ({ ...f, name: e.target.value }))} /></div>
          <div><label className="text-sm font-medium">成员 (逗号分隔)</label><Input value={rotForm.members} onChange={e => setRotForm(f => ({ ...f, members: e.target.value }))} placeholder="张三,李四,王五" /></div>
          <div><label className="text-sm font-medium">轮换周期(天)</label><Input type="number" value={rotForm.cycle_days} onChange={e => setRotForm(f => ({ ...f, cycle_days: parseInt(e.target.value) || 7 }))} /></div>
        </div>
        <DialogFooter><Button variant="outline" onClick={() => setRotDialog(false)}>取消</Button><Button onClick={handleCreateRotation} disabled={!rotForm.name}>创建</Button></DialogFooter>
      </Dialog>
    </div>
  )
}
