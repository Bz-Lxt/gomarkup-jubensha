import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ArrowDown, History, RotateCcw, Send, Sparkles, WifiOff } from 'lucide-react'

import { MessageBubble, type ChatItem } from '@/components/chat/MessageBubble'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import type { ConnState } from '@/lib/ws'
import type { TagOption } from '@/types'

interface Props {
  items: ChatItem[]
  state: ConnState
  truncated: boolean
  myUserID: number
  onSend: (content: string, msgType?: 'TEXT' | 'TAG', tagCode?: string) => void
  onRetry: (clientMsgID: string) => void
  className?: string
}

const MAX_LEN = 500

export function ChatPanel({
  items,
  state,
  truncated,
  myUserID,
  onSend,
  onRetry,
  className,
}: Props) {
  const [draft, setDraft] = useState('')
  const scrollRef = useRef<HTMLDivElement>(null)
  const [pinned, setPinned] = useState(true)

  // 标签目录由后端供给，前端不维护一份枚举，避免两端文案分叉。
  const { data: tags } = useQuery({
    queryKey: ['tags'],
    queryFn: () => api.tagCatalog(),
    staleTime: 30 * 60_000,
  })

  // 只有用户本来就贴在底部时才自动滚动。否则他正在往上翻历史，
  // 新消息把视图强拽到底是非常粗暴的体验。
  useLayoutEffect(() => {
    if (!pinned) return
    const el = scrollRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [items, pinned])

  useEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const onScroll = () => {
      const gap = el.scrollHeight - el.scrollTop - el.clientHeight
      setPinned(gap < 80)
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  const submit = () => {
    const text = draft.trim()
    if (!text) return
    onSend(text, 'TEXT')
    setDraft('')
    setPinned(true)
  }

  const sendTag = (t: TagOption) => {
    onSend(t.phrase, 'TAG', t.code)
    setPinned(true)
  }

  const disconnected = state === 'reconnecting' || state === 'closed'

  return (
    <div className={cn('card relative flex min-h-0 flex-col overflow-hidden', className)}>
      {/* ── 连接状态条：断线必须让用户知道，不能静默 ── */}
      {disconnected && (
        <div className="flex animate-slide-down items-center gap-2 border-b border-amber-500/30 bg-amber-500/10 px-4 py-2 text-xs text-amber-400">
          <WifiOff className="size-3.5 shrink-0 animate-pulse" aria-hidden />
          <span>连接已断开，正在自动重连…重连后会补齐这段时间的消息</span>
        </div>
      )}

      {/* ── 消息流 ── */}
      <div ref={scrollRef} className="flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {truncated && (
          <div className="flex items-center gap-2 py-1 text-[11px] text-ink-faint">
            <span className="h-px flex-1 bg-hairline" />
            <span className="inline-flex items-center gap-1">
              <History className="size-3" aria-hidden />
              离线消息较多，仅显示最近部分
            </span>
            <span className="h-px flex-1 bg-hairline" />
          </div>
        )}

        {items.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
            <Sparkles className="size-6 text-ink-faint" aria-hidden />
            <p className="text-sm text-ink-muted">还没有人说话</p>
            <p className="text-xs text-ink-faint">
              发个玩家标签打破沉默，让大家知道你想玩什么风格
            </p>
          </div>
        ) : (
          items.map((m) => (
            <div key={m.seq > 0 ? `s${m.seq}` : `c${m.client_msg_id}`}>
              <MessageBubble msg={m} mine={m.sender_id === myUserID || m.pending === true} />
              {m.failed && (
                <div className="mt-1 flex justify-end">
                  <button
                    onClick={() => onRetry(m.client_msg_id)}
                    className="inline-flex items-center gap-1 rounded-lg border border-danger/40 px-2 py-1 text-[11px] text-danger transition hover:bg-danger/10"
                  >
                    <RotateCcw className="size-3" aria-hidden />
                    重发
                  </button>
                </div>
              )}
            </div>
          ))
        )}
      </div>

      {/* 用户翻历史时给一个回到底部的入口，而不是强行滚动 */}
      {!pinned && (
        <button
          onClick={() => {
            setPinned(true)
            const el = scrollRef.current
            if (el) el.scrollTop = el.scrollHeight
          }}
          className="absolute bottom-32 right-4 inline-flex items-center gap-1 rounded-full border border-hairline bg-raised/95 px-3 py-1.5 text-xs text-ink shadow-card backdrop-blur transition hover:bg-raised"
        >
          <ArrowDown className="size-3.5" aria-hidden />
          回到最新
        </button>
      )}

      {/* ── 标签快发轨：需求点名的一级交互，不藏进菜单 ── */}
      <div className="border-t border-hairline px-3 py-2">
        <div className="flex gap-1.5 overflow-x-auto pb-0.5">
          {(tags ?? []).map((t) => (
            <button
              key={t.code}
              onClick={() => sendTag(t)}
              title={t.phrase}
              className="shrink-0 rounded-full border border-hairline bg-raised px-3 py-1.5 text-xs text-ink-muted transition hover:border-brand/45 hover:text-brand active:scale-95"
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {/* ── 输入区 ── */}
      <div className="border-t border-hairline p-3">
        <div className="flex items-end gap-2">
          <div className="relative flex-1">
            <textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value.slice(0, MAX_LEN))}
              onKeyDown={(e) => {
                // Enter 发送、Shift+Enter 换行。中文输入法组合期间
                // 不能拦截 Enter，否则会把候选词直接发出去。
                if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  submit()
                }
              }}
              rows={1}
              placeholder="说点什么…（Enter 发送，Shift+Enter 换行）"
              className="field max-h-28 min-h-[44px] resize-none py-3 pr-14"
              aria-label="消息输入框"
            />
            {draft.length > MAX_LEN * 0.8 && (
              <span className="tnum absolute bottom-2 right-3 text-[10px] text-ink-faint">
                {draft.length}/{MAX_LEN}
              </span>
            )}
          </div>
          <Button onClick={submit} disabled={!draft.trim()} className="h-11 px-4" aria-label="发送">
            <Send className="size-4" aria-hidden />
          </Button>
        </div>
      </div>
    </div>
  )
}
