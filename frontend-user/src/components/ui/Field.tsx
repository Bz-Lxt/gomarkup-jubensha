import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from 'react'

import { cn } from '@/lib/utils'

interface WrapProps {
  label?: string
  hint?: string
  error?: string
  children: ReactNode
  className?: string
}

export function Field({ label, hint, error, children, className }: WrapProps) {
  return (
    <div className={className}>
      {label && <span className="label">{label}</span>}
      {children}
      {error ? (
        <p className="mt-1 text-xs text-danger">{error}</p>
      ) : (
        hint && <p className="mt-1 text-xs text-ink-faint">{hint}</p>
      )}
    </div>
  )
}

export function Input({ className, ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return <input className={cn('field', className)} {...rest} />
}

export function Textarea({ className, ...rest }: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={cn('field resize-none', className)} {...rest} />
}

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={cn('field appearance-none pr-9', className)} {...rest}>
      {children}
    </select>
  )
}

/** NumberStepper 比裸 number input 好用得多：手机上不用调起数字键盘就能改人数。 */
export function NumberStepper({
  value,
  onChange,
  min = 0,
  max = 12,
  label,
}: {
  value: number
  onChange: (v: number) => void
  min?: number
  max?: number
  label: string
}) {
  const clamp = (v: number) => Math.max(min, Math.min(max, v))
  return (
    <div className="flex items-center justify-between rounded-xl border border-hairline bg-raised px-3 py-2">
      <span className="text-sm text-ink-muted">{label}</span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          onClick={() => onChange(clamp(value - 1))}
          disabled={value <= min}
          className="size-8 rounded-lg border border-hairline text-ink transition hover:bg-surface disabled:opacity-35"
          aria-label={`减少${label}`}
        >
          −
        </button>
        <span className="tnum w-8 text-center text-sm font-semibold text-ink">{value}</span>
        <button
          type="button"
          onClick={() => onChange(clamp(value + 1))}
          disabled={value >= max}
          className="size-8 rounded-lg border border-hairline text-ink transition hover:bg-surface disabled:opacity-35"
          aria-label={`增加${label}`}
        >
          +
        </button>
      </div>
    </div>
  )
}
