import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  ArrowLeft,
  BadgeCheck,
  CalendarClock,
  Crown,
  Lock,
  LogOut,
  MapPin,
  ShieldCheck,
  Trash2,
  UserMinus,
} from 'lucide-react'

import { ChatPanel } from '@/components/chat/ChatPanel'
import { CountdownBar } from '@/components/Countdown'
import { SeatLegend, SeatMeter } from '@/components/SeatMeter'
import { Avatar } from '@/components/ui/Avatar'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { useRoomChat } from '@/hooks/useRoomChat'
import { ApiError, api } from '@/lib/api'
import { cn, formatDateTime, statusTone } from '@/lib/utils'
import { useAuth } from '@/store/auth'
import { toast } from '@/store/toast'
import type { RoomStatusData, SeatGender, SlotSnapshot } from '@/types'

export function RoomDetailPage() {
  const { id } = useParams<{ id: string }>()
  const roomID = Number(id)
  const user = useAuth((s) => s.user)
  const navigate = useNavigate()
  const qc = useQueryClient()

  const [seatModal, setSeatModal] = useState(false)
  const [kickTarget, setKickTarget] = useState<{ id: number; name: string } | null>(null)
  const [confirmDanger, setConfirmDanger] = useState<'leave' | 'cancel' | 'lock' | null>(null)

  const roomKey = ['room', roomID] as const
  const { data: card, isLoading } = useQuery({
    queryKey: roomKey,
    queryFn: ({ signal }) => api.room(roomID, signal),
    enabled: Number.isFinite(roomID) && roomID > 0,
  })

  /**
   * 席位快照到达时就地更新缓存中的 snapshot，而不是整页重取。
   *
   * 抢位场景下席位每秒可能变好几次，每次都重取详情（含成员列表 + 用户资料）
   * 会把后端打成筛子。成员构成变化由 room.status 帧触发的重取兜住。
   */
  const onSlot = useCallback(
    (snap: SlotSnapshot) => {
      if (snap.room_id !== roomID) return
      qc.setQueryData(roomKey, (prev: typeof card) =>
        prev ? { ...prev, snapshot: snap } : prev,
      )
      // 成员进出会改变头像列表，这里补一次轻量重取。
      qc.invalidateQueries({ queryKey: roomKey })
    },
    [qc, roomID, roomKey],
  )

  const onStatus = useCallback(
    (data: RoomStatusData) => {
      qc.invalidateQueries({ queryKey: roomKey })
      if (data.reason) toast.info(data.status_label, data.reason)
    },
    [qc, roomKey],
  )

  const onWSError = useCallback(
    (err: { code: string; message: string }) => {
      if (err.code === 'NOT_ROOM_MEMBER') {
        toast.warn('只有上车的人才能进这个聊天室')
        navigate('/')
        return
      }
      // 限流类错误不打扰用户：他自己刷太快，界面上已经能感知。
      if (err.code !== 'RATE_LIMITED') toast.error(err.message)
    },
    [navigate],
  )

  const chat = useRoomChat({ roomID, onSlot, onStatus, onError: onWSError })

  // 详情页打开即清零该房间未读，墙上的角标要跟着消失。
  useEffect(() => {
    if (card?.am_on_car) qc.invalidateQueries({ queryKey: ['wall'] })
  }, [card?.am_on_car, qc])

  const mutate = <T,>(fn: () => Promise<T>, okMsg: string) =>
    fn()
      .then((res) => {
        toast.success(okMsg)
        qc.invalidateQueries({ queryKey: roomKey })
        qc.invalidateQueries({ queryKey: ['wall'] })
        return res
      })
      .catch((err) => {
        if (err instanceof ApiError) {
          if (err.isSlotConflict) qc.invalidateQueries({ queryKey: roomKey })
          toast.error(err.message)
        }
        throw err
      })

  const joinMut = useMutation({
    mutationFn: (seat: SeatGender) => api.join(roomID, seat),
    onSuccess: (res) => {
      setSeatModal(false)
      toast.success(res.idempotent ? '你已经在这辆车上了' : '上车成功', res.snapshot.headline)
      qc.invalidateQueries({ queryKey: roomKey })
      qc.invalidateQueries({ queryKey: ['wall'] })
    },
    onError: (err) => {
      if (err instanceof ApiError) {
        if (err.isSlotConflict) qc.invalidateQueries({ queryKey: roomKey })
        toast.error(err.message)
      }
    },
  })

  const leaveMut = useMutation({
    mutationFn: () => mutate(() => api.leave(roomID), '已退车'),
    onSettled: () => setConfirmDanger(null),
  })
  const lockMut = useMutation({
    mutationFn: () => mutate(() => api.lockRoom(roomID), '已锁车，不再接受新成员'),
    onSettled: () => setConfirmDanger(null),
  })
  const cancelMut = useMutation({
    mutationFn: () => mutate(() => api.cancelRoom(roomID, '车主解散'), '已解散这辆车'),
    onSettled: () => setConfirmDanger(null),
  })
  const confirmMut = useMutation({
    mutationFn: () => mutate(() => api.confirm(roomID), '占位已确认'),
  })
  const kickMut = useMutation({
    mutationFn: (userID: number) => mutate(() => api.kick(roomID, userID, '车主移出'), '已移出该成员'),
    onSettled: () => setKickTarget(null),
  })

  if (isLoading) {
    return (
      <div className="grid gap-4 lg:grid-cols-[minmax(0,380px)_minmax(0,1fr)]">
        <div className="skeleton h-96" />
        <div className="skeleton h-96" />
      </div>
    )
  }

  if (!card) {
    return (
      <div className="card flex flex-col items-center gap-3 p-12 text-center">
        <p className="text-sm text-ink-muted">这辆车不存在，或者已经被删掉了</p>
        <Link to="/">
          <Button variant="subtle">回拼车墙</Button>
        </Link>
      </div>
    )
  }

  const { room, snapshot } = card
  const tone = statusTone(snapshot.status, snapshot.at_risk)
  const canJoin = snapshot.accepts_join && !card.am_on_car
  const isPending = card.my_status === 'PENDING'

  const joinBlockReason = card.am_on_car
    ? '你已经在这辆车上了'
    : snapshot.status !== 'RECRUITING'
      ? `这辆车${snapshot.status_label}`
      : snapshot.remaining <= 0
        ? '席位已满'
        : snapshot.seconds_left <= 0
          ? '已过开局时间'
          : ''

  return (
    <div className="flex flex-col gap-4">
      <Link
        to="/"
        className="inline-flex w-fit items-center gap-1.5 text-sm text-ink-muted transition hover:text-ink"
      >
        <ArrowLeft className="size-4" aria-hidden />
        回拼车墙
      </Link>

      <div className="grid min-h-0 gap-4 lg:grid-cols-[minmax(0,390px)_minmax(0,1fr)]">
        {/* ─────────────── 左栏：车况 ─────────────── */}
        <div className="flex flex-col gap-4">
          <section className="card p-5">
            <div className="mb-3 flex flex-wrap items-center gap-1.5">
              <Badge tone={room.room_type === 'SCRIPT' ? 'brand' : 'info'}>{card.type_name}</Badge>
              {room.theme && <Badge>{room.theme}</Badge>}
              <Badge tone="muted">难度 {'★'.repeat(Math.max(1, Math.min(5, room.difficulty)))}</Badge>
              <Badge tone={tone} className="ml-auto">
                {snapshot.status_label}
              </Badge>
            </div>

            <h1 className="text-xl font-bold tracking-tight text-ink">{room.script_name}</h1>
            <p className="mt-1 text-sm text-ink-muted">{room.title}</p>

            <dl className="mt-4 flex flex-col gap-2 text-sm">
              <div className="flex items-start gap-2">
                <MapPin className="mt-0.5 size-4 shrink-0 text-ink-faint" aria-hidden />
                <dd className="text-ink-muted">
                  {room.city} · {room.venue_name}
                  {room.address && <span className="text-ink-faint"> · {room.address}</span>}
                </dd>
              </div>
              <div className="flex items-start gap-2">
                <CalendarClock className="mt-0.5 size-4 shrink-0 text-ink-faint" aria-hidden />
                <dd className="text-ink-muted">{formatDateTime(room.start_at)} 开局</dd>
              </div>
            </dl>

            {room.notes && (
              <p className="mt-3 rounded-xl border border-hairline bg-raised p-3 text-xs leading-relaxed text-ink-muted">
                {room.notes}
              </p>
            )}

            {/* 席位区 */}
            <div className="mt-4 border-t border-hairline pt-4">
              <div className="flex items-end justify-between gap-3">
                <div>
                  <p
                    className={cn(
                      'tnum font-mono text-3xl font-bold leading-none',
                      snapshot.at_risk
                        ? 'text-danger'
                        : snapshot.remaining === 0
                          ? 'text-success'
                          : 'text-ink',
                    )}
                  >
                    {snapshot.headline}
                  </p>
                  <p className="mt-1.5 text-xs text-ink-faint">
                    最低 {snapshot.min_viable} 人成行
                    {snapshot.viable ? ' · 已达成行线' : ' · 还没达线'}
                  </p>
                </div>
                <SeatMeter
                  seats={snapshot.seats}
                  capacity={snapshot.capacity}
                  className="max-w-[110px] justify-end"
                />
              </div>
              <SeatLegend seats={snapshot.seats} className="mt-3" />
              <CountdownBar snapshot={snapshot} className="mt-3" />
            </div>

            {/* 行动区 */}
            <div className="mt-4 flex flex-wrap gap-2">
              {canJoin ? (
                <Button
                  variant={snapshot.at_risk ? 'danger' : 'primary'}
                  onClick={() => {
                    if (!user) {
                      navigate('/login')
                      return
                    }
                    const open = snapshot.seats.filter((s) => s.remaining > 0)
                    if (open.length === 1 && open[0]) joinMut.mutate(open[0].gender)
                    else setSeatModal(true)
                  }}
                  loading={joinMut.isPending}
                  className="flex-1"
                >
                  上车
                </Button>
              ) : (
                !card.am_on_car && (
                  <Button disabled disabledReason={joinBlockReason} className="flex-1">
                    {joinBlockReason || '不能上车'}
                  </Button>
                )
              )}

              {isPending && (
                <Button
                  variant="primary"
                  onClick={() => confirmMut.mutate()}
                  loading={confirmMut.isPending}
                  className="flex-1 gap-1.5"
                >
                  <BadgeCheck className="size-4" aria-hidden />
                  确认占位
                </Button>
              )}

              {card.am_on_car && !card.am_owner && (
                <Button
                  variant="outline"
                  onClick={() => setConfirmDanger('leave')}
                  className="gap-1.5"
                >
                  <LogOut className="size-4" aria-hidden />
                  退车
                </Button>
              )}

              {card.am_owner && snapshot.status === 'RECRUITING' && (
                <>
                  <Button
                    variant="subtle"
                    onClick={() => setConfirmDanger('lock')}
                    className="gap-1.5"
                  >
                    <Lock className="size-4" aria-hidden />
                    锁车
                  </Button>
                  <Button
                    variant="outline"
                    onClick={() => setConfirmDanger('cancel')}
                    className="gap-1.5 text-danger"
                  >
                    <Trash2 className="size-4" aria-hidden />
                    解散
                  </Button>
                </>
              )}
            </div>

            {isPending && (
              <p className="mt-2 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-400">
                你的位子是「占位中」，超时未确认会被自动释放
              </p>
            )}
          </section>

          {/* 成员列表 */}
          <section className="card p-5">
            <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold text-ink">
              车上的人
              <span className="tnum text-xs font-normal text-ink-faint">
                {card.members.length}/{snapshot.capacity}
              </span>
              <span className="ml-auto inline-flex items-center gap-1 text-[11px] font-normal text-success">
                <span className="size-1.5 rounded-full bg-success" />
                {chat.online.length} 人在线
              </span>
            </h2>

            <ul className="flex flex-col gap-2">
              {card.members.map((m) => (
                <li
                  key={m.member_id}
                  className="flex items-center gap-3 rounded-xl border border-hairline bg-raised px-3 py-2"
                >
                  <Avatar
                    name={m.user.nickname}
                    avatar={m.user.avatar}
                    size="sm"
                    online={chat.online.includes(m.user.id)}
                  />
                  <div className="min-w-0 flex-1">
                    <p className="flex items-center gap-1.5 truncate text-sm text-ink">
                      {m.user.nickname}
                      {m.is_owner && <Crown className="size-3.5 shrink-0 text-amber-400" aria-hidden />}
                    </p>
                    <p className="flex flex-wrap items-center gap-1 text-[11px] text-ink-faint">
                      <span>{m.seat_label}</span>
                      <span>·</span>
                      <span>{m.status_label}</span>
                      {m.status === 'PENDING' && m.hold_seconds_left > 0 && (
                        <span className="tnum text-amber-400">
                          · {Math.ceil(m.hold_seconds_left / 60)} 分钟内确认
                        </span>
                      )}
                    </p>
                  </div>
                  {(m.user.tags ?? []).slice(0, 2).map((t) => (
                    <Badge key={t} className="hidden sm:inline-flex">
                      {t}
                    </Badge>
                  ))}
                  {card.am_owner && !m.is_owner && (
                    <button
                      onClick={() => setKickTarget({ id: m.user.id, name: m.user.nickname })}
                      className="rounded-lg p-1.5 text-ink-faint transition hover:bg-danger/15 hover:text-danger"
                      aria-label={`移出 ${m.user.nickname}`}
                      title="移出这位成员"
                    >
                      <UserMinus className="size-4" />
                    </button>
                  )}
                </li>
              ))}
            </ul>
          </section>
        </div>

        {/* ─────────────── 右栏：弹幕聊天 ─────────────── */}
        {card.am_on_car ? (
          <ChatPanel
            items={chat.items}
            state={chat.state}
            truncated={chat.truncated}
            myUserID={user?.id ?? 0}
            onSend={chat.send}
            onRetry={chat.retry}
            className="h-[calc(100vh-13rem)] min-h-[520px]"
          />
        ) : (
          <div className="card flex min-h-[420px] flex-col items-center justify-center gap-3 p-10 text-center">
            <ShieldCheck className="size-8 text-ink-faint" aria-hidden />
            <div>
              <p className="text-sm text-ink">聊天室只对车上的人开放</p>
              <p className="mt-1 text-xs text-ink-faint">
                上车后立刻开聊，可以一键发送你的玩家标签
              </p>
            </div>
            {canJoin && (
              <Button onClick={() => setSeatModal(true)} loading={joinMut.isPending}>
                上车并进入聊天室
              </Button>
            )}
          </div>
        )}
      </div>

      {/* ── 选席位 ── */}
      <Modal
        open={seatModal}
        onClose={() => setSeatModal(false)}
        title="选一个席位上车"
        desc={`${room.script_name} · ${snapshot.headline}`}
        size="sm"
      >
        <div className="flex flex-col gap-2">
          {snapshot.seats.map((s) => (
            <button
              key={s.gender}
              disabled={s.remaining <= 0}
              onClick={() => joinMut.mutate(s.gender)}
              className={cn(
                'flex items-center justify-between rounded-xl border border-hairline bg-raised px-4 py-3 text-left transition',
                s.remaining > 0
                  ? 'hover:border-brand/50 hover:bg-brand/10'
                  : 'cursor-not-allowed opacity-40',
              )}
            >
              <span className="text-sm text-ink">{s.label}</span>
              <span className="tnum text-xs text-ink-muted">
                {s.remaining > 0 ? `还剩 ${s.remaining} 个` : '已满'}
              </span>
            </button>
          ))}
        </div>
      </Modal>

      {/* ── 危险操作二次确认 ── */}
      <Modal
        open={confirmDanger !== null}
        onClose={() => setConfirmDanger(null)}
        title={
          confirmDanger === 'leave' ? '确定退车？' : confirmDanger === 'lock' ? '确定锁车？' : '确定解散这辆车？'
        }
        desc={
          confirmDanger === 'leave'
            ? '退车后你的席位会立刻释放给其他人，需要重新抢。'
            : confirmDanger === 'lock'
              ? '锁车后不再接受新成员。若有人退车，会自动回到招募中。'
              : '解散后这辆车会从墙上消失，所有成员都会收到通知。此操作不可撤销。'
        }
        size="sm"
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmDanger(null)}>
              再想想
            </Button>
            <Button
              variant={confirmDanger === 'lock' ? 'primary' : 'danger'}
              loading={leaveMut.isPending || lockMut.isPending || cancelMut.isPending}
              onClick={() => {
                if (confirmDanger === 'leave') leaveMut.mutate()
                else if (confirmDanger === 'lock') lockMut.mutate()
                else cancelMut.mutate()
              }}
            >
              确定
            </Button>
          </>
        }
      />

      <Modal
        open={kickTarget !== null}
        onClose={() => setKickTarget(null)}
        title={`把 ${kickTarget?.name ?? ''} 移出这辆车？`}
        desc="对方会收到系统消息，席位立刻释放。"
        size="sm"
        footer={
          <>
            <Button variant="ghost" onClick={() => setKickTarget(null)}>
              取消
            </Button>
            <Button
              variant="danger"
              loading={kickMut.isPending}
              onClick={() => kickTarget && kickMut.mutate(kickTarget.id)}
            >
              移出
            </Button>
          </>
        }
      />
    </div>
  )
}
