
import { cn } from '@/lib/utils'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'

interface StatisticCardProps {
  title: string
  value: number | string
  icon?: React.ReactNode
  suffix?: string
  description?: string
  trend?: {
    value: number
    isUp: boolean
  }
  color?: string
  loading?: boolean
  className?: string
}

export function StatisticCard({
  title,
  value,
  icon,
  suffix,
  description,
  trend,
  color = '#3B82F6',
  loading = false,
  className,
}: StatisticCardProps) {
  if (loading) {
    return (
      <Card className={className}>
        <CardContent className="p-4">
          <Skeleton className="h-4 w-16 mb-2" />
          <Skeleton className="h-8 w-24 mb-2" />
          <Skeleton className="h-3 w-full" />
        </CardContent>
      </Card>
    )
  }

  return (
    <Card className={cn("relative overflow-hidden", className)}>
      {/* 背景渐变 */}
      <div
        className="absolute inset-0 opacity-5"
        style={{ background: `linear-gradient(135deg, ${color}, transparent)` }}
      />

      <CardContent className="p-4 relative">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <p className="text-xs font-medium text-muted-foreground mb-1">{title}</p>
            <div className="flex items-baseline gap-1">
              <span className="text-2xl font-bold tabular-nums" style={{ color }}>
                {typeof value === 'number' ? value.toLocaleString() : value}
              </span>
              {suffix && <span className="text-sm text-muted-foreground">{suffix}</span>}
            </div>
          </div>
          {icon && (
            <div
              className="p-2 rounded-lg"
              style={{ backgroundColor: `${color}15`, color }}
            >
              {icon}
            </div>
          )}
        </div>

        {(description || trend) && (
          <div className="mt-2 flex items-center gap-2">
            {trend && (
              <span className={cn(
                "text-xs font-medium",
                trend.isUp ? "text-green-600" : "text-red-600"
              )}>
                {trend.isUp ? '↑' : '↓'} {Math.abs(trend.value)}%
              </span>
            )}
            {description && (
              <span className="text-xs text-muted-foreground">{description}</span>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

// 统计卡片组
interface StatisticCardGroupProps {
  stats?: Array<{
    title: string
    value: number | string
    icon?: React.ReactNode
    suffix?: string
    description?: string
    trend?: { value: number; isUp: boolean }
    color?: string
  }>
  loading?: boolean
  columns?: 2 | 3 | 4 | 5
  className?: string
  children?: React.ReactNode
}

export function StatisticCardGroup({
  stats,
  loading = false,
  columns = 4,
  className,
  children,
}: StatisticCardGroupProps) {
  const gridCols = {
    2: 'grid-cols-2',
    3: 'grid-cols-3',
    4: 'grid-cols-4',
    5: 'grid-cols-2 md:grid-cols-3 lg:grid-cols-5',
  }

  return (
    <div className={cn("grid gap-4", gridCols[columns], className)}>
      {stats ? stats.map((stat, index) => (
        <StatisticCard key={index} {...stat} loading={loading} />
      )) : children}
    </div>
  )
}
