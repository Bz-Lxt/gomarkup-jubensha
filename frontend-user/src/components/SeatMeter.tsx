import { cn, SEAT_DOT } from '@/lib/utils'
import type { SeatBucket } from '@/types'

/**
 * SeatMeter 是席位点阵：已占为实心（按席位类别着色），空位为描边空心。
 *
 * 为什么不用进度条：用户真正要回答的问题是「还差几个人」，点阵可以直接数，
 * 进度条只能给出模糊的比例感。DesignSpec §4.1。
 */
export function SeatMeter({
  seats,
  capacity,
  className,
}: {
  seats: SeatBucket[]
  capacity: number
  className?: string
}) {
  // 把三类席位摊平成一排点：先男席、再女席、最后不限席，顺序固定，
  // 这样同一辆车在墙上和详情页看到的点阵排列完全一致。
  const dots: { key: string; filled: boolean; gender: string }[] = []
  for (const b of seats) {
    for (let i = 0; i < b.quota; i++) {
      dots.push({ key: `${b.gender}-${i}`, filled: i < b.taken, gender: b.gender })
    }
  }

  // 席位配额之和理论上恒等于 capacity（数据库 CHECK 保证）。
  // 万一账目异常，宁可少画也不要画出比总数还多的点。
  const visible = dots.slice(0, capacity)

  return (
    <div
      className={cn('flex flex-wrap items-center gap-1.5', className)}
      role="img"
      aria-label={`共 ${capacity} 席，已占 ${visible.filter((d) => d.filled).length} 席`}
    >
      {visible.map((d) => (
        <span
          key={d.key}
          className={cn(
            'size-2.5 rounded-full transition',
            d.filled
              ? SEAT_DOT[d.gender] ?? 'bg-violet-400'
              : 'border border-dashed border-white/25 bg-transparent',
          )}
        />
      ))}
    </div>
  )
}

/** SeatLegend 用文字复述各类席位余量，保证不单靠颜色传达信息（DesignSpec §6）。 */
export function SeatLegend({ seats, className }: { seats: SeatBucket[]; className?: string }) {
  const active = seats.filter((s) => s.quota > 0)
  if (active.length === 0) return null
  return (
    <div className={cn('flex flex-wrap items-center gap-x-3 gap-y-1 text-xs', className)}>
      {active.map((s) => (
        <span key={s.gender} className="inline-flex items-center gap-1.5">
          <span className={cn('size-1.5 rounded-full', SEAT_DOT[s.gender] ?? 'bg-violet-400')} />
          <span className="text-ink-muted">
            {s.label}
            <span className="tnum ml-1 text-ink">
              {s.taken}/{s.quota}
            </span>
          </span>
        </span>
      ))}
    </div>
  )
}
