import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'
import type { StatusTone } from '@/lib/utils'

const TONES: Record<StatusTone, string> = {
  brand: 'border-brand/35 bg-brand/12 text-brand',
  danger: 'border-danger/40 bg-danger/12 text-danger',
  success: 'border-success/35 bg-success/12 text-success',
  info: 'border-info/35 bg-info/12 text-info',
  muted: 'border-hairline bg-raised text-ink-muted',
}

interface Props {
  tone?: StatusTone
  icon?: ReactNode
  children: ReactNode
  className?: string
}

export function Badge({ tone = 'muted', icon, children, className }: Props) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-[11px] font-medium leading-5',
        TONES[tone],
        className,
      )}
    >
      {icon}
      {children}
    </span>
  )
}
