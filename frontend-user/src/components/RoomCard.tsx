import { Link } from 'react-router-dom'
import { Crown, MapPin, MessageSquare, Users } from 'lucide-react'

import { CountdownBar } from '@/components/Countdown'
import { SeatLegend, SeatMeter } from '@/components/SeatMeter'
import { Avatar } from '@/components/ui/Avatar'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { cn, statusTone } from '@/lib/utils'
import type { RoomCard as Card } from '@/types'

interface Props {
  card: Card
  onJoin?: (card: Card) => void
  joining?: boolean
}

/**
 * RoomCard 遵循 DesignSpec §4.1 的四段式结构：
 * 元信息 → 标题 → 席位主标（最大元素）→ 倒计时 → 成员与主行动。
 */
export function RoomCardView({ card, onJoin, joining }: Props) {
  const { room, snapshot } = card
  const tone = statusTone(snapshot.status, snapshot.at_risk)
  const dead = snapshot.status === 'EXPIRED' || snapshot.status === 'CANCELLED'

  // 禁用原因必须具体。「不能上车」是没用的提示，用户会反复点。
  const joinBlockReason = (() => {
    if (card.am_on_car) return '你已经在这辆车上了'
    if (snapshot.status !== 'RECRUITING') return `这辆车${snapshot.status_label}，不再接受上车`
    if (snapshot.remaining <= 0) return '席位已满（含占位中）'
    if (snapshot.seconds_left <= 0) return '已过开局时间'
    return ''
  })()

  return (
    <article
      className={cn(
        'card group flex flex-col gap-3 p-4 transition',
        snapshot.at_risk && 'border-danger/35 shadow-glow-danger',
        dead && 'opacity-60',
        !dead && 'hover:border-white/15',
      )}
    >
      {/* ── 元信息行 ── */}
      <div className="flex items-start justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge tone={room.room_type === 'SCRIPT' ? 'brand' : 'info'}>{card.type_name}</Badge>
          {room.theme && <Badge>{room.theme}</Badge>}
          <Badge tone="muted">{'★'.repeat(Math.max(1, Math.min(5, room.difficulty)))}</Badge>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {card.unread > 0 && (
            <span className="inline-flex items-center gap-1 rounded-full bg-brand-grad px-2 py-0.5 text-[11px] font-semibold text-white">
              <MessageSquare className="size-3" aria-hidden />
              {card.unread > 99 ? '99+' : card.unread}
            </span>
          )}
          <Badge tone={tone}>{snapshot.status_label}</Badge>
        </div>
      </div>

      {/* ── 标题 ── */}
      <Link to={`/rooms/${room.id}`} className="group/link block">
        <h3 className="truncate text-[15px] font-semibold text-ink transition group-hover/link:text-brand">
          {room.script_name}
        </h3>
        <p className="mt-0.5 flex items-center gap-1 truncate text-xs text-ink-muted">
          <MapPin className="size-3 shrink-0" aria-hidden />
          <span className="truncate">
            {room.city} · {room.venue_name}
          </span>
        </p>
      </Link>

      {/* ── 席位主标：卡片上最大的元素 ── */}
      <div className="flex items-end justify-between gap-3">
        <div>
          <p
            className={cn(
              'tnum font-mono text-[26px] font-bold leading-none tracking-tight',
              snapshot.at_risk ? 'text-danger' : snapshot.remaining === 0 ? 'text-success' : 'text-ink',
            )}
          >
            {snapshot.headline}
          </p>
          <SeatLegend seats={snapshot.seats} className="mt-2" />
        </div>
        <SeatMeter
          seats={snapshot.seats}
          capacity={snapshot.capacity}
          className="max-w-[92px] justify-end"
        />
      </div>

      {/* ── 倒计时 ── */}
      <CountdownBar snapshot={snapshot} />

      {/* ── 成员 + 主行动 ── */}
      <div className="mt-auto flex items-center justify-between gap-3 pt-1">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex -space-x-2">
            {card.members.slice(0, 4).map((m) => (
              <Avatar
                key={m.member_id}
                name={m.user.nickname}
                avatar={m.user.avatar}
                size="sm"
                ring
              />
            ))}
          </div>
          {card.members.length > 4 && (
            <span className="tnum inline-flex items-center gap-0.5 text-xs text-ink-muted">
              <Users className="size-3" aria-hidden />+{card.members.length - 4}
            </span>
          )}
          {card.am_owner && (
            <Badge tone="brand" icon={<Crown className="size-3" />} className="ml-0.5">
              我的车
            </Badge>
          )}
        </div>

        {card.am_on_car ? (
          <Link to={`/rooms/${room.id}`}>
            <Button size="sm" variant="subtle">
              进聊天室
            </Button>
          </Link>
        ) : (
          <Button
            size="sm"
            variant={snapshot.at_risk ? 'danger' : 'primary'}
            loading={joining}
            disabled={!!joinBlockReason}
            disabledReason={joinBlockReason}
            onClick={() => onJoin?.(card)}
          >
            上车
          </Button>
        )}
      </div>

      {/* 禁用原因显式写出来，而不是只放在 title 里——手机上没有 hover */}
      {joinBlockReason && !card.am_on_car && (
        <p className="-mt-1 text-right text-[11px] text-ink-faint">{joinBlockReason}</p>
      )}
    </article>
  )
}

/** 骨架卡：加载态用它保持布局稳定，避免内容到位时页面整体跳动。 */
export function RoomCardSkeleton() {
  return (
    <div className="card flex flex-col gap-3 p-4">
      <div className="flex gap-2">
        <div className="skeleton h-5 w-16" />
        <div className="skeleton h-5 w-20" />
      </div>
      <div className="skeleton h-5 w-2/3" />
      <div className="skeleton h-3 w-1/2" />
      <div className="skeleton h-8 w-24" />
      <div className="skeleton h-9 w-full" />
      <div className="flex items-center justify-between">
        <div className="skeleton size-8 rounded-full" />
        <div className="skeleton h-9 w-20" />
      </div>
    </div>
  )
}
