import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Plus, Ticket } from 'lucide-react'

import { RoomCardSkeleton, RoomCardView } from '@/components/RoomCard'
import { Button } from '@/components/ui/Button'
import { api } from '@/lib/api'

export function MinePage() {
  const navigate = useNavigate()
  const { data, isLoading } = useQuery({
    queryKey: ['mine'],
    queryFn: () => api.mine(),
    refetchInterval: 30_000,
  })

  const cards = data?.items ?? []
  // 我的车按开局时间正序：最近要开的排最前，这是用户最需要盯的。
  const sorted = [...cards].sort((a, b) => a.snapshot.seconds_left - b.snapshot.seconds_left)
  const active = sorted.filter((c) => !['COMPLETED', 'EXPIRED', 'CANCELLED'].includes(c.snapshot.status))
  const done = sorted.filter((c) => ['COMPLETED', 'EXPIRED', 'CANCELLED'].includes(c.snapshot.status))

  return (
    <div className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <span className="grid size-11 place-items-center rounded-xl bg-brand-grad shadow-glow">
          <Ticket className="size-5 text-white" aria-hidden />
        </span>
        <div>
          <h1 className="text-lg font-bold tracking-tight text-ink">我的车</h1>
          <p className="text-xs text-ink-muted">你开的和你上的车都在这里</p>
        </div>
      </div>

      {isLoading ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <RoomCardSkeleton />
          <RoomCardSkeleton />
          <RoomCardSkeleton />
        </div>
      ) : cards.length === 0 ? (
        <div className="card flex flex-col items-center gap-3 p-12 text-center">
          <Ticket className="size-7 text-ink-faint" aria-hidden />
          <div>
            <p className="text-sm text-ink-muted">你还没上过任何车</p>
            <p className="mt-1 text-xs text-ink-faint">去墙上找一辆，或者自己开一辆</p>
          </div>
          <div className="flex gap-2">
            <Button variant="subtle" onClick={() => navigate('/')}>
              逛拼车墙
            </Button>
            <Button onClick={() => navigate('/create')} className="gap-1.5">
              <Plus className="size-4" aria-hidden />
              开车
            </Button>
          </div>
        </div>
      ) : (
        <>
          {active.length > 0 && (
            <section className="flex flex-col gap-3">
              <h2 className="text-sm font-semibold text-ink">
                进行中
                <span className="tnum ml-2 text-xs font-normal text-ink-faint">{active.length}</span>
              </h2>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                {active.map((c) => (
                  <RoomCardView key={c.room.id} card={c} />
                ))}
              </div>
            </section>
          )}

          {done.length > 0 && (
            <section className="flex flex-col gap-3">
              <h2 className="text-sm font-semibold text-ink">
                已收工
                <span className="tnum ml-2 text-xs font-normal text-ink-faint">{done.length}</span>
              </h2>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
                {done.map((c) => (
                  <RoomCardView key={c.room.id} card={c} />
                ))}
              </div>
            </section>
          )}
        </>
      )}
    </div>
  )
}
