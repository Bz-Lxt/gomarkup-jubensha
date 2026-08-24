import { AlertCircle, Info, Sparkles } from 'lucide-react'

import { Avatar } from '@/components/ui/Avatar'
import { cn, formatTime } from '@/lib/utils'
import type { Message } from '@/types'

export interface ChatItem extends Message {
  /** 乐观气泡：本地已插入但服务端尚未确认 */
  pending?: boolean
  /** 发送失败，允许重试 */
  failed?: boolean
}

export function MessageBubble({ msg, mine }: { msg: ChatItem; mine: boolean }) {
  // 系统消息居中胶囊，无头像。它描述的是房间发生的事，不属于任何人。
  if (msg.msg_type === 'SYSTEM') {
    return (
      <div className="my-1 flex animate-bubble-in justify-center">
        <span className="inline-flex max-w-[85%] items-center gap-1.5 rounded-full border border-info/25 bg-info/10 px-3 py-1 text-[11px] text-info">
          <Info className="size-3 shrink-0" aria-hidden />
          <span className="truncate">{msg.content}</span>
        </span>
      </div>
    )
  }

  const isTag = msg.msg_type === 'TAG'

  return (
    <div
      className={cn('flex animate-bubble-in items-end gap-2', mine ? 'flex-row-reverse' : 'flex-row')}
    >
      {!mine && <Avatar name={msg.sender_name} avatar={msg.sender_avatar} size="sm" />}

      <div className={cn('flex max-w-[76%] flex-col gap-1', mine ? 'items-end' : 'items-start')}>
        {!mine && <span className="px-1 text-[11px] text-ink-faint">{msg.sender_name}</span>}

        <div
          className={cn(
            'relative rounded-2xl px-3.5 py-2 text-sm leading-relaxed',
            mine
              ? 'bg-brand-grad text-white'
              : 'border border-hairline bg-raised text-ink',
            // 标签消息给一层特殊描边，让「一键发标签」在流里一眼可辨
            isTag && 'ring-1 ring-inset ring-white/25',
            msg.pending && 'opacity-55',
            msg.failed && 'bg-danger/20 text-danger ring-1 ring-danger/40',
          )}
        >
          {isTag && (
            <span className="mb-0.5 flex items-center gap-1 text-[11px] font-semibold opacity-90">
              <Sparkles className="size-3" aria-hidden />
              玩家标签
            </span>
          )}
          <span className="whitespace-pre-wrap break-words">{msg.content}</span>
        </div>

        <span className="flex items-center gap-1 px-1 text-[10px] text-ink-faint">
          {msg.failed ? (
            <>
              <AlertCircle className="size-3 text-danger" aria-hidden />
              <span className="text-danger">发送失败</span>
            </>
          ) : msg.pending ? (
            '发送中…'
          ) : (
            formatTime(msg.created_at)
          )}
        </span>
      </div>
    </div>
  )
}
