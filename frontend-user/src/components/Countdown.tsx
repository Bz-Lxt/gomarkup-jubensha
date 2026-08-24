import { AlertTriangle, Clock } from 'lucide-react'

import { useCountdown } from '@/hooks/useCountdown'
import { cn, formatCountdown } from '@/lib/utils'
import type { SlotSnapshot } from '@/types'

/**
 * CountdownBar 是卡片上的倒计时条，也是「再不来车就炸了」这句需求的落点。
 *
 * 危险态判定完全依赖后端下发的 snapshot.at_risk，前端不重算阈值。
 * 后端 model.AtRiskWindow 改了，这里自动跟着变，不存在两端分叉的可能。
 */
export function CountdownBar({
  snapshot,
  className,
  compact,
}: {
  snapshot: SlotSnapshot
  className?: string
  compact?: boolean
}) {
  const left = useCountdown(snapshot.seconds_left)
  const danger = snapshot.at_risk
  const done = left <= 0

  const Icon = danger ? AlertTriangle : Clock

  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-xl border px-3 py-2',
        danger
          ? 'border-danger/40 bg-danger/10 text-danger'
          : done
            ? 'border-hairline bg-raised text-ink-muted'
            : 'border-hairline bg-raised text-ink-muted',
        className,
      )}
    >
      <Icon className={cn('size-4 shrink-0', danger && 'animate-pulse')} aria-hidden />
      <div className="min-w-0 flex-1">
        {/* polite 而非 assertive：读屏软件不该每秒打断用户（DesignSpec §6） */}
        <p className="truncate text-xs" aria-live="polite">
          {compact ? (
            <span className={cn('tnum font-mono font-semibold', danger ? 'text-danger' : 'text-ink')}>
              {formatCountdown(left)}
            </span>
          ) : (
            <>
              <span
                className={cn('tnum font-mono font-semibold', danger ? 'text-danger' : 'text-ink')}
              >
                {formatCountdown(left)}
              </span>
              <span className="mx-1.5 text-ink-faint">·</span>
              <span>{snapshot.risk_hint}</span>
            </>
          )}
        </p>
      </div>
    </div>
  )
}
