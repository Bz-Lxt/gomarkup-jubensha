import { forwardRef, type ButtonHTMLAttributes } from 'react'
import { Loader2 } from 'lucide-react'

import { cn } from '@/lib/utils'

type Variant = 'primary' | 'danger' | 'ghost' | 'outline' | 'subtle'
type Size = 'sm' | 'md' | 'lg'

interface Props extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant
  size?: Size
  loading?: boolean
  /** 禁用时展示的原因。DesignSpec §5.4：禁用态必须说明为什么。 */
  disabledReason?: string
}

const VARIANTS: Record<Variant, string> = {
  primary:
    'bg-brand-grad text-white shadow-glow hover:brightness-110 active:brightness-95 disabled:shadow-none',
  danger:
    'bg-danger-grad text-white shadow-glow-danger hover:brightness-110 active:brightness-95 disabled:shadow-none',
  outline: 'border border-hairline bg-transparent text-ink hover:bg-raised',
  subtle: 'bg-raised text-ink hover:bg-raised/70 border border-hairline',
  ghost: 'bg-transparent text-ink-muted hover:bg-raised hover:text-ink',
}

const SIZES: Record<Size, string> = {
  // 最小 44px 触控区（DesignSpec §6）。sm 只用于纯图标按钮，仍保证 36px+。
  sm: 'h-9 px-3 text-xs gap-1.5 rounded-lg',
  md: 'h-11 px-4 text-sm gap-2 rounded-xl',
  lg: 'h-12 px-6 text-base gap-2 rounded-xl',
}

export const Button = forwardRef<HTMLButtonElement, Props>(function Button(
  { variant = 'primary', size = 'md', loading, disabled, disabledReason, className, children, ...rest },
  ref,
) {
  const isDisabled = disabled || loading
  return (
    <button
      ref={ref}
      disabled={isDisabled}
      title={isDisabled && disabledReason ? disabledReason : rest.title}
      aria-busy={loading || undefined}
      className={cn(
        'inline-flex select-none items-center justify-center font-medium transition',
        'disabled:cursor-not-allowed disabled:opacity-45',
        VARIANTS[variant],
        SIZES[size],
        className,
      )}
      {...rest}
    >
      {loading && <Loader2 className="size-4 animate-spin" aria-hidden />}
      {children}
    </button>
  )
})
