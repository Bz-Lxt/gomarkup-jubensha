import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Car, Filter, Plus, RefreshCw, Search, Wifi } from 'lucide-react'

import { RoomCardSkeleton, RoomCardView } from '@/components/RoomCard'
import { Button } from '@/components/ui/Button'
import { Input, Select } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { ApiError, api } from '@/lib/api'
import { RoomSocket, type ConnState } from '@/lib/ws'
import { cn } from '@/lib/utils'
import { useAuth } from '@/store/auth'
import { toast } from '@/store/toast'
import type { RoomCard, SeatGender } from '@/types'

/** 看板分列。列的划分是产品决策，不是后端状态的机械枚举。 */
const COLUMNS = [
  { key: 'RECRUITING', title: '招募中', hint: '还能上车', statuses: ['RECRUITING'] },
  { key: 'LOCKED', title: '已满员 / 已成行', hint: '等开局', statuses: ['LOCKED', 'CONFIRMED'] },
  { key: 'IN_PROGRESS', title: '开局中', hint: '正在玩', statuses: ['IN_PROGRESS'] },
  {
    key: 'DONE',
    title: '已收工',
    hint: '结束 / 炸车',
    statuses: ['COMPLETED', 'EXPIRED', 'CANCELLED'],
  },
] as const

export function WallPage() {
  const user = useAuth((s) => s.user)
  const navigate = useNavigate()
  const qc = useQueryClient()

  const [city, setCity] = useState('')
  const [roomType, setRoomType] = useState('')
  const [keyword, setKeyword] = useState('')
  const [debounced, setDebounced] = useState('')
  const [mobileCol, setMobileCol] = useState<string>('RECRUITING')
  const [wsState, setWsState] = useState<ConnState>('connecting')
  const [joinTarget, setJoinTarget] = useState<RoomCard | null>(null)

  // 搜索防抖：每敲一个字就打一次接口，在中文输入法下会打出十几个请求。
  useEffect(() => {
    const id = setTimeout(() => setDebounced(keyword.trim()), 350)
    return () => clearTimeout(id)
  }, [keyword])

  const { data: cities } = useQuery({
    queryKey: ['cities'],
    queryFn: () => api.cities(),
    staleTime: 10 * 60_000,
  })
  const { data: enums } = useQuery({
    queryKey: ['enums'],
    queryFn: () => api.enums(),
    staleTime: 30 * 60_000,
  })

  const wallKey = ['wall', city, roomType, debounced] as const
  const { data, isLoading, isFetching, refetch } = useQuery({
    queryKey: wallKey,
    queryFn: ({ signal }) =>
      api.wall({ city, room_type: roomType, q: debounced, limit: 60 }, signal),
    // 席位变动靠 WS 实时推送，这里的轮询只是兜底（WS 挂了也不至于看到僵死数据）。
    refetchInterval: 60_000,
  })

  // 墙级 WebSocket：任何房间的席位变动都会推到这里，收到就让列表失效重取。
  // 直接用推送里的快照原地更新单张卡片会更省流量，但墙上还涉及成员头像、
  // 未读数等推送里没有的字段，重取一次更可靠也更简单。
  useEffect(() => {
    const sock = new RoomSocket({
      path: '/ws/wall',
      auth: false,
      onState: setWsState,
      onFrame: (env) => {
        if (env.type === 'room.slot' || env.type === 'room.status') {
          qc.invalidateQueries({ queryKey: ['wall'] })
        }
      },
    })
    return () => sock.close()
  }, [qc])

  const joinMut = useMutation({
    mutationFn: ({ id, seat }: { id: number; seat: SeatGender }) => api.join(id, seat),
    onSuccess: (res, vars) => {
      setJoinTarget(null)
      if (res.idempotent) {
        toast.info('你本来就在这辆车上', '直接进聊天室吧')
      } else {
        toast.success('上车成功', res.snapshot.headline)
      }
      qc.invalidateQueries({ queryKey: ['wall'] })
      navigate(`/rooms/${vars.id}`)
    },
    onError: (err) => {
      if (!(err instanceof ApiError)) return
      // 抢位失败是高频正常场景，必须立刻刷新墙，让用户看到真实的最新席位。
      if (err.isSlotConflict) qc.invalidateQueries({ queryKey: ['wall'] })
      if (err.code === 'UNAUTHORIZED' || err.status === 401) {
        toast.warn('先登录才能上车')
        navigate('/login')
        return
      }
      toast.error(err.message)
    },
  })

  const grouped = useMemo(() => {
    const map = new Map<string, RoomCard[]>()
    for (const col of COLUMNS) map.set(col.key, [])
    for (const card of data?.items ?? []) {
      const col = COLUMNS.find((c) => (c.statuses as readonly string[]).includes(card.snapshot.status))
      if (col) map.get(col.key)!.push(card)
    }
    // 招募中按「最紧急」排前面：先炸车预警，再按开局时间。
    const recruiting = map.get('RECRUITING')
    if (recruiting) {
      recruiting.sort((a, b) => {
        if (a.snapshot.at_risk !== b.snapshot.at_risk) return a.snapshot.at_risk ? -1 : 1
        return a.snapshot.seconds_left - b.snapshot.seconds_left
      })
    }
    return map
  }, [data])

  const onJoin = (card: RoomCard) => {
    if (!user) {
      toast.warn('先登录才能上车')
      navigate('/login')
      return
    }
    const open = card.snapshot.seats.filter((s) => s.remaining > 0)
    // 只剩一类席位可选时不必弹窗，直接上车——少一次点击。
    if (open.length === 1 && open[0]) {
      joinMut.mutate({ id: card.room.id, seat: open[0].gender })
      return
    }
    setJoinTarget(card)
  }

  return (
    <div className="flex flex-col gap-4">
      {/* ── 筛选栏 ── */}
      <div className="card flex flex-wrap items-end gap-3 p-4">
        <div className="relative min-w-[180px] flex-1">
          <Search
            className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-ink-faint"
            aria-hidden
          />
          <Input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜剧本名、店名、主题…"
            className="pl-9"
            aria-label="搜索"
          />
        </div>

        <Select
          value={city}
          onChange={(e) => setCity(e.target.value)}
          className="w-32"
          aria-label="城市"
        >
          <option value="">全部城市</option>
          {(cities ?? []).map((c) => (
            <option key={c} value={c}>
              {c}
            </option>
          ))}
        </Select>

        <Select
          value={roomType}
          onChange={(e) => setRoomType(e.target.value)}
          className="w-32"
          aria-label="局类型"
        >
          <option value="">全部类型</option>
          {(enums?.room_types ?? []).map((t) => (
            <option key={t.code} value={t.code}>
              {t.label}
            </option>
          ))}
        </Select>

        <Button variant="subtle" onClick={() => refetch()} loading={isFetching} className="gap-1.5">
          <RefreshCw className="size-4" aria-hidden />
          刷新
        </Button>

        {/* 实时连接状态：让用户知道席位数字是活的 */}
        <span
          className={cn(
            'inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1.5 text-xs',
            wsState === 'open'
              ? 'border-success/35 bg-success/10 text-success'
              : 'border-amber-500/35 bg-amber-500/10 text-amber-400',
          )}
          title={wsState === 'open' ? '席位变动实时推送中' : '实时推送已断开，正在重连'}
        >
          <Wifi className={cn('size-3.5', wsState !== 'open' && 'animate-pulse')} aria-hidden />
          {wsState === 'open' ? '实时' : '重连中'}
        </span>
      </div>

      {/* ── 手机端列切换 Tab：四列硬塞在手机上没法读（DesignSpec §4.2） ── */}
      <div className="flex gap-1.5 overflow-x-auto md:hidden">
        {COLUMNS.map((c) => (
          <button
            key={c.key}
            onClick={() => setMobileCol(c.key)}
            className={cn(
              'shrink-0 rounded-xl px-3 py-2 text-xs transition',
              mobileCol === c.key
                ? 'bg-brand-grad text-white'
                : 'border border-hairline bg-raised text-ink-muted',
            )}
          >
            {c.title}
            <span className="tnum ml-1.5 opacity-70">{grouped.get(c.key)?.length ?? 0}</span>
          </button>
        ))}
      </div>

      {/* ── 看板 ── */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        {COLUMNS.map((col) => {
          const cards = grouped.get(col.key) ?? []
          return (
            <section
              key={col.key}
              className={cn(
                'flex min-w-0 flex-col gap-3',
                // 手机端只显示选中的那一列
                mobileCol === col.key ? 'flex' : 'hidden md:flex',
              )}
            >
              <header className="flex items-baseline justify-between gap-2 px-1">
                <h2 className="text-sm font-semibold text-ink">
                  {col.title}
                  <span className="tnum ml-2 text-xs font-normal text-ink-faint">
                    {cards.length}
                  </span>
                </h2>
                <span className="text-[11px] text-ink-faint">{col.hint}</span>
              </header>

              <div className="flex flex-col gap-3">
                {isLoading ? (
                  <>
                    <RoomCardSkeleton />
                    <RoomCardSkeleton />
                  </>
                ) : cards.length === 0 ? (
                  <EmptyColumn
                    isRecruiting={col.key === 'RECRUITING'}
                    onCreate={() => navigate('/create')}
                  />
                ) : (
                  cards.map((card) => (
                    <RoomCardView
                      key={card.room.id}
                      card={card}
                      onJoin={onJoin}
                      joining={joinMut.isPending && joinMut.variables?.id === card.room.id}
                    />
                  ))
                )}
              </div>
            </section>
          )
        })}
      </div>

      {/* ── 选席位弹窗 ── */}
      <Modal
        open={!!joinTarget}
        onClose={() => setJoinTarget(null)}
        title="选一个席位上车"
        desc={
          joinTarget
            ? `${joinTarget.room.script_name} · ${joinTarget.snapshot.headline}`
            : undefined
        }
        size="sm"
      >
        <div className="flex flex-col gap-2">
          {joinTarget?.snapshot.seats.map((s) => (
            <button
              key={s.gender}
              disabled={s.remaining <= 0}
              onClick={() => joinMut.mutate({ id: joinTarget.room.id, seat: s.gender })}
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
          {joinTarget?.snapshot.seats.every((s) => s.quota === 0) && (
            <p className="text-sm text-ink-muted">这辆车没有配置席位，联系车主处理。</p>
          )}
        </div>
      </Modal>
    </div>
  )
}

/** 空态给出路，而不是一句「暂无数据」（DesignSpec §5.3）。 */
function EmptyColumn({
  isRecruiting,
  onCreate,
}: {
  isRecruiting: boolean
  onCreate: () => void
}) {
  return (
    <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-hairline px-4 py-10 text-center">
      {isRecruiting ? (
        <>
          <Car className="size-7 text-ink-faint" aria-hidden />
          <div>
            <p className="text-sm text-ink-muted">这里还没人开车</p>
            <p className="mt-1 text-xs text-ink-faint">你来开第一辆，缺的人让墙帮你找</p>
          </div>
          <Button size="sm" onClick={onCreate} className="gap-1.5">
            <Plus className="size-4" aria-hidden />
            开一辆车
          </Button>
        </>
      ) : (
        <>
          <Filter className="size-6 text-ink-faint" aria-hidden />
          <p className="text-xs text-ink-faint">暂时没有这个状态的车</p>
        </>
      )}
    </div>
  )
}
