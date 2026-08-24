import { cn, avatarGradient, initial } from '@/lib/utils'

interface Props {
  name: string
  avatar?: string
  size?: 'xs' | 'sm' | 'md' | 'lg'
  online?: boolean
  ring?: boolean
  className?: string
}

const SIZES = {
  xs: 'size-6 text-[10px]',
  sm: 'size-8 text-xs',
  md: 'size-10 text-sm',
  lg: 'size-14 text-lg',
} as const

/**
 * Avatar 用「渐变底 + 首字」纯 CSS 渲染，不加载任何远程图片。
 * 色板索引由后端下发的 `local:N` 决定，因此同一个人在任何设备上颜色一致。
 */
export function Avatar({ name, avatar = '', size = 'md', online, ring, className }: Props) {
  return (
    <span className={cn('relative inline-flex shrink-0', className)}>
      <span
        className={cn(
          'inline-flex items-center justify-center rounded-full bg-gradient-to-br font-semibold text-white',
          avatarGradient(avatar, name),
          SIZES[size],
          ring && 'ring-2 ring-base',
        )}
        aria-hidden
      >
        {initial(name)}
      </span>
      {online !== undefined && (
        <span
          className={cn(
            'absolute -bottom-0.5 -right-0.5 size-3 rounded-full border-2 border-surface',
            online ? 'bg-success' : 'bg-ink-faint',
          )}
          title={online ? '在线' : '不在线'}
        />
      )}
      <span className="sr-only">{name}</span>
    </span>
  )
}
