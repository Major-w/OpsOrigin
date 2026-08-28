
import { useEffect, useState, useMemo } from 'react'
import { useOnCallStore } from '@/stores/oncall'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Tag, Button } from 'antd'
import { ChevronLeft, ChevronRight, Calendar, User, Clock } from 'lucide-react'
import { formatDateTime } from '@/lib/itsm-constants'

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六']
const ROLE_COLORS: Record<string, string> = {
  primary: 'bg-blue-500/15 text-blue-700 dark:bg-blue-400/20 dark:text-blue-300 border-blue-200 dark:border-blue-500/30',
  backup: 'bg-violet-500/15 text-violet-700 dark:bg-violet-400/20 dark:text-violet-300 border-violet-200 dark:border-violet-500/30',
}

function getDaysInMonth(year: number, month: number) {
  return new Date(year, month + 1, 0).getDate()
}

function getFirstDayOfWeek(year: number, month: number) {
  return new Date(year, month, 1).getDay()
}

export default function OnCallCalendarPage() {
  const { shifts, fetchShifts } = useOnCallStore()
  const [currentDate, setCurrentDate] = useState(new Date())

  useEffect(() => { fetchShifts() }, [])

  const year = currentDate.getFullYear()
  const month = currentDate.getMonth()
  const daysInMonth = getDaysInMonth(year, month)
  const firstDay = getFirstDayOfWeek(year, month)
  const today = new Date()

  const grid = useMemo(() => {
    const totalCells = Math.ceil((firstDay + daysInMonth) / 7) * 7
    const cells: (number | null)[] = []
    for (let i = 0; i < totalCells; i++) {
      const day = i - firstDay + 1
      cells.push(day >= 1 && day <= daysInMonth ? day : null)
    }
    return cells
  }, [firstDay, daysInMonth])

  const monthShifts = useMemo(() => {
    const monthStart = new Date(year, month, 1)
    const monthEnd = new Date(year, month + 1, 0, 23, 59, 59)
    return shifts.filter(s => {
      const start = new Date(s.start_time)
      const end = new Date(s.end_time)
      return start <= monthEnd && end >= monthStart
    })
  }, [shifts, year, month])

  const getShiftsForDay = (day: number) => {
    const dayStart = new Date(year, month, day, 0, 0, 0)
    const dayEnd = new Date(year, month, day, 23, 59, 59)
    return monthShifts.filter(s => {
      const start = new Date(s.start_time)
      const end = new Date(s.end_time)
      return start <= dayEnd && end >= dayStart
    })
  }

  const prevMonth = () => setCurrentDate(new Date(year, month - 1, 1))
  const nextMonth = () => setCurrentDate(new Date(year, month + 1, 1))

  const isToday = (day: number) =>
    today.getFullYear() === year && today.getMonth() === month && today.getDate() === day

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Calendar className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-2xl font-bold">On-Call 日历</h1>
        </div>
        <div className="flex items-center gap-2">
          <Button type="default" size="middle" onClick={prevMonth}>
            <ChevronLeft className="h-4 w-4" />
          </Button>
          <span className="text-sm font-medium min-w-[120px] text-center">{year}年{month + 1}月</span>
          <Button type="default" size="middle" onClick={nextMonth}>
            <ChevronRight className="h-4 w-4" />
          </Button>
        </div>
      </div>

      <Card>
        <CardContent className="p-0">
          <div className="grid grid-cols-7 border-b">
            {WEEKDAYS.map(d => (
              <div key={d} className="p-2 text-center text-xs font-medium text-muted-foreground border-r last:border-r-0">
                {d}
              </div>
            ))}
          </div>
          <div className="grid grid-cols-7">
            {grid.map((day, i) => (
              <div
                key={i}
                className={`min-h-[100px] p-1.5 border-r border-b last:border-r-0 ${
                  day === null ? 'bg-muted/30' : ''
                } ${day !== null && isToday(day) ? 'ring-2 ring-primary/50 ring-inset' : ''}`}
              >
                {day !== null && (
                  <>
                    <div className={`text-xs font-medium mb-1 ${isToday(day) ? 'text-primary font-bold' : 'text-muted-foreground'}`}>
                      {day}
                    </div>
                    <div className="space-y-0.5">
                      {getShiftsForDay(day).map(s => (
                        <div
                          key={s.id}
                          className={`text-[10px] leading-tight rounded px-1 py-0.5 border font-medium truncate ${ROLE_COLORS[s.role] || ROLE_COLORS.primary}`}
                          title={`${s.user_name} (${s.role}) ${formatDateTime(s.start_time)} - ${formatDateTime(s.end_time)}`}
                        >
                          {s.user_name}
                        </div>
                      ))}
                    </div>
                  </>
                )}
              </div>
            ))}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-base flex items-center gap-2">
            <Clock className="h-4 w-4" />
            本月值班安排
            <Tag color="default" className="ml-1">{monthShifts.length}</Tag>
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {monthShifts.length === 0 ? (
            <p className="px-4 py-6 text-sm text-muted-foreground text-center">本月暂无值班安排。</p>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b text-left text-muted-foreground">
                  <th className="pb-2 px-4">值班人</th>
                  <th className="pb-2 px-4">角色</th>
                  <th className="pb-2 px-4">开始时间</th>
                  <th className="pb-2 px-4">结束时间</th>
                  <th className="pb-2 px-4">联系方式</th>
                </tr>
              </thead>
              <tbody>
                {monthShifts.map(s => {
                  const isActive = new Date(s.end_time) >= today && new Date(s.start_time) <= today
                  return (
                    <tr key={s.id} className={`border-b last:border-0 ${isActive ? 'bg-blue-500/5' : ''}`}>
                      <td className="py-2 px-4 font-medium">
                        <div className="flex items-center gap-2">
                          <User className="h-3 w-3 text-muted-foreground" />
                          {s.user_name}
                          {isActive && <Tag color="success" className="text-xs">当前</Tag>}
                        </div>
                      </td>
                      <td className="py-2 px-4">
                        <Tag color={s.role === 'primary' ? 'blue' : 'default'} className="text-xs">{s.role}</Tag>
                      </td>
                      <td className="py-2 px-4 text-xs text-muted-foreground">{formatDateTime(s.start_time)}</td>
                      <td className="py-2 px-4 text-xs text-muted-foreground">{formatDateTime(s.end_time)}</td>
                      <td className="py-2 px-4 text-xs text-muted-foreground">{s.phone || s.email || '-'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
