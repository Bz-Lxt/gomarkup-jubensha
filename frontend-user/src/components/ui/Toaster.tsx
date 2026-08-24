import { AlertTriangle, CheckCircle2, Info, X, XCircle } from 'lucide-react'

import { cn } from '@/lib/utils'
import { useToast, type ToastTone } from '@/store/toast'

const TONES: Record<ToastTone, { cls: string; Icon: typeof Info }> = {
  success: { cls: 'border-success/40 text-success', Icon: CheckCircle2 },
  error: { cls: 'border-danger/45 text-danger', Icon: XCircle },
  warn: { cls: 'border-amber-500/45 text-amber-400', Icon: AlertTriangle },
  info: { cls: 'border-info/40 text-info', Icon: Info },
}

export function Toaster() {
  const items = useToast((s) => s.items)
  const dismiss = useToast((s) => s.dismiss)

  return (
    // pointer-events-none 让整个容器不挡住底下的内容，只有卡片本身可点。
    <div
      className="pointer-events-none fixed inset-x-0 top-3 z-50 flex flex-col items-center gap-2 px-4"
      role="region"
      aria-label="通知"
    >
      {items.map((t) => {
        const { cls, Icon } = TONES[t.tone]
        return (
          <div
            key={t.id}
            role={t.tone === 'error' ? 'alert' : 'status'}
            className={cn(
              'pointer-events-auto flex w-full max-w-md animate-slide-down items-start gap-3 rounded-xl border bg-surface/95 px-4 py-3 shadow-card backdrop-blur',
              cls,
            )}
          >
            <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="text-sm font-medium text-ink">{t.title}</p>
              {t.desc && <p className="mt-0.5 text-xs text-ink-muted">{t.desc}</p>}
            </div>
            <button
              onClick={() => dismiss(t.id)}
              className="rounded p-0.5 text-ink-faint transition hover:text-ink"
              aria-label="关闭通知"
            >
              <X className="size-4" />
            </button>
          </div>
        )
      })}
    </div>
  )
}
