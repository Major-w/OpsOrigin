import { useMemo, useState } from 'react'
import { Button } from 'antd'
import { Server, AlertTriangle, Link2, Unlink, ExternalLink, HardDrive } from 'lucide-react'
import { cn } from '@/lib/utils'

// ============================================================================
// 机柜 U 位视图
// 中间为 42→1 的 U 位列表（左右双编号），占用块按设备高度合并展示，
// 空位可点击绑定设备；右侧展示选中设备的明细。
// ============================================================================

export interface RackDevice {
  id: string
  name: string
  typeId?: string
  ip?: string
  startUnit: number
  height: number
  critical: number
  warning: number
  status?: string
}

interface Props {
  rackName: string
  maxUnits: number
  devices: RackDevice[]
  /** 点击空白 U 位 → 绑定设备 */
  onBind?: (unit: number) => void
  /** 解绑某设备 */
  onUnbind?: (dev: RackDevice) => void
  /** 跳转设备详情 */
  onOpenDevice?: (dev: RackDevice) => void
  className?: string
}

const TYPE_LABELS: Record<string, string> = {
  server: '服务器', host: '主机', storage: '存储', firewall: '防火墙',
  load_balancer: '负载均衡', router: '路由器', switch: '交换机',
  database: '数据库', middleware: '中间件', app: '应用',
}

export function RackUnitView({ rackName, maxUnits, devices, onBind, onUnbind, onOpenDevice, className }: Props) {
  const [active, setActive] = useState<string | null>(devices[0]?.id ?? null)

  // unit → 起始该 U 的设备（只在设备顶部落一个块）
  const { startsAt, coverAt, used } = useMemo(() => {
    const starts = new Map<number, RackDevice>()
    const cover = new Map<number, RackDevice>()
    let count = 0
    for (const d of devices) {
      const h = Math.max(1, d.height)
      for (let i = 0; i < h; i++) {
        const u = d.startUnit + i
        if (u < 1 || u > maxUnits) continue
        cover.set(u, d)
        count++
      }
      starts.set(d.startUnit + h - 1, d)
    }
    return { startsAt: starts, coverAt: cover, used: count }
  }, [devices, maxUnits])

  const activeDev = devices.find(d => d.id === active) ?? null
  const units = Array.from({ length: maxUnits }, (_, i) => maxUnits - i) // 上到下 42→1

  return (
    <div className={cn('grid grid-cols-1 gap-4 md:grid-cols-[150px_1fr_240px]', className)}>
      {/* 左：U 位概览 */}
      <div className="rounded-lg border border-blue-200 bg-blue-50/70 p-3 dark:border-blue-500/30 dark:bg-blue-500/10">
        <p className="text-xs text-muted-foreground">U 位概览</p>
        <p className="mt-1 text-3xl font-bold leading-none text-blue-600 dark:text-blue-300">{maxUnits}</p>
        <p className="mt-1 text-[11px] text-muted-foreground">U 位总数</p>
        <div className="mt-3 space-y-1.5 text-[11px]">
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">已使用</span>
            <span className="font-medium text-amber-600 dark:text-amber-400">{used}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-muted-foreground">未使用</span>
            <span className="font-medium text-emerald-600 dark:text-emerald-400">{Math.max(0, maxUnits - used)}</span>
          </div>
        </div>
        <div className="mt-3 h-2 overflow-hidden rounded-full bg-muted">
          <div className="h-full rounded-full bg-amber-500 transition-all" style={{ width: `${maxUnits ? Math.round(used / maxUnits * 100) : 0}%` }} />
        </div>
        <p className="mt-2 text-[11px] text-muted-foreground">{rackName}</p>
      </div>

      {/* 中：U 位列表 */}
      <div className="max-h-[52vh] overflow-y-auto rounded-lg border border-border bg-muted/20 p-1.5">
        {units.map(u => {
          const start = startsAt.get(u)
          if (start) {
            const selected = start.id === active
            const alert = start.critical > 0
            return (
              <div
                key={u}
                onClick={() => setActive(start.id)}
                className={cn(
                  'mb-0.5 flex items-center gap-1.5 rounded-md border-2 px-1.5 py-1 cursor-pointer transition-colors',
                  alert ? 'border-red-400 bg-red-500/10' : 'border-slate-500/60 bg-slate-800/85',
                  selected && 'ring-2 ring-blue-500/70',
                )}
              >
                <UnitNo n={u} />
                <div className="flex min-w-0 flex-1 flex-col px-1">
                  <span className="truncate text-[11px] font-medium text-slate-100">{start.name}</span>
                  <span className="truncate text-[10px] text-slate-400">
                    {start.ip || TYPE_LABELS[start.typeId || ''] || start.typeId || '设备'}
                    {start.height > 1 ? ` · ${start.height}U` : ''}
                  </span>
                </div>
                {alert && (
                  <span className="mr-1 inline-flex shrink-0 items-center gap-0.5 text-[10px] text-red-400">
                    <AlertTriangle className="h-3 w-3" />{start.critical}
                  </span>
                )}
                <UnitNo n={u} />
              </div>
            )
          }
          // 被上方设备占用的中间行 → 不重复渲染（块已覆盖视觉高度，这里用细条占位保持编号连续）
          if (coverAt.has(u)) {
            return (
              <div key={u} className="flex items-center gap-1.5 px-1.5">
                <span className="w-5 shrink-0 text-right text-[10px] tabular-nums text-muted-foreground">{u}</span>
                <div className="h-[3px] flex-1 rounded bg-slate-500/25" />
                <span className="w-5 shrink-0 text-[10px] tabular-nums text-muted-foreground">{u}</span>
              </div>
            )
          }
          return (
            <div key={u} className="group flex items-center gap-1.5 px-1.5 py-[1px]">
              <UnitNo n={u} />
              <button
                onClick={() => onBind?.(u)}
                className="flex flex-1 items-center justify-center gap-1 rounded border border-dashed border-border py-1 text-[10px] text-muted-foreground transition-colors hover:border-blue-400 hover:bg-blue-500/10 hover:text-blue-600 dark:hover:text-blue-300"
              >
                <Link2 className="h-3 w-3 opacity-0 transition-opacity group-hover:opacity-100" />
                点击绑定设备
              </button>
              <UnitNo n={u} />
            </div>
          )
        })}
      </div>

      {/* 右：设备明细 */}
      <div className="rounded-lg border border-border p-3">
        <p className="text-xs font-medium">设备信息</p>
        {activeDev ? (
          <div className="mt-2 space-y-2 text-[11px]">
            <div className="flex items-center gap-2">
              <span className={cn('flex h-7 w-7 items-center justify-center rounded',
                activeDev.critical > 0 ? 'bg-red-500/15 text-red-500' : 'bg-blue-500/15 text-blue-500')}>
                <Server className="h-3.5 w-3.5" />
              </span>
              <div className="min-w-0">
                <p className="truncate font-medium">{activeDev.name}</p>
                <p className="text-muted-foreground">U{activeDev.startUnit}
                  {activeDev.height > 1 ? ` - U${activeDev.startUnit + activeDev.height - 1}` : ''}</p>
              </div>
            </div>
            <Row k="类型" v={TYPE_LABELS[activeDev.typeId || ''] || activeDev.typeId || '-'} />
            <Row k="IP 地址" v={activeDev.ip || '-'} />
            <Row k="高度" v={`${activeDev.height}U`} />
            <Row k="状态" v={activeDev.status || '-'} />
            <Row k="紧急告警" v={String(activeDev.critical)} tone={activeDev.critical > 0 ? 'danger' : undefined} />
            <Row k="告警" v={String(activeDev.warning)} tone={activeDev.warning > 0 ? 'warn' : undefined} />
            <div className="flex flex-wrap gap-1.5 pt-1">
              {onOpenDevice && (
                <Button size="small" type="primary" icon={<ExternalLink className="h-3 w-3" />} onClick={() => onOpenDevice(activeDev)}>详情</Button>
              )}
              {onUnbind && (
                <Button size="small" danger icon={<Unlink className="h-3 w-3" />} onClick={() => onUnbind(activeDev)}>解绑</Button>
              )}
            </div>
          </div>
        ) : (
          <div className="mt-4 flex flex-col items-center gap-2 text-[11px] text-muted-foreground">
            <HardDrive className="h-6 w-6 opacity-40" />
            点击左侧任一设备查看明细
          </div>
        )}
      </div>
    </div>
  )
}

function UnitNo({ n }: { n: number }) {
  return <span className="w-5 shrink-0 text-center text-[10px] tabular-nums text-muted-foreground">{n}</span>
}

function Row({ k, v, tone }: { k: string; v: string; tone?: 'danger' | 'warn' }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <span className="shrink-0 text-muted-foreground">{k}</span>
      <span className={cn('truncate font-medium',
        tone === 'danger' ? 'text-red-500' : tone === 'warn' ? 'text-amber-500' : '')} title={v}>{v}</span>
    </div>
  )
}
