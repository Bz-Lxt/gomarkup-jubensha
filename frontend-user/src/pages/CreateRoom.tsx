import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useMutation, useQuery } from '@tanstack/react-query'
import { AlertTriangle, Car } from 'lucide-react'

import { SeatMeter } from '@/components/SeatMeter'
import { Button } from '@/components/ui/Button'
import { Field, Input, NumberStepper, Select, Textarea } from '@/components/ui/Field'
import { ApiError, api } from '@/lib/api'
import { cn, defaultStartAt, localInputToISO } from '@/lib/utils'
import { toast } from '@/store/toast'
import type { RoomType, SeatGender } from '@/types'

export function CreateRoomPage() {
  const navigate = useNavigate()

  const [title, setTitle] = useState('')
  const [scriptName, setScriptName] = useState('')
  const [venueName, setVenueName] = useState('')
  const [city, setCity] = useState('')
  const [address, setAddress] = useState('')
  const [roomType, setRoomType] = useState<RoomType>('SCRIPT')
  const [theme, setTheme] = useState('')
  const [difficulty, setDifficulty] = useState(3)
  const [notes, setNotes] = useState('')
  const [startAt, setStartAt] = useState(defaultStartAt(4))
  const [male, setMale] = useState(3)
  const [female, setFemale] = useState(3)
  const [any, setAny] = useState(0)
  const [minViable, setMinViable] = useState(5)
  const [ownerSeat, setOwnerSeat] = useState<SeatGender>('ANY')
  const [err, setErr] = useState('')

  const { data: enums } = useQuery({
    queryKey: ['enums'],
    queryFn: () => api.enums(),
    staleTime: 30 * 60_000,
  })
  const { data: cities } = useQuery({
    queryKey: ['cities'],
    queryFn: () => api.cities(),
    staleTime: 10 * 60_000,
  })

  const capacity = male + female + any

  // 车主必须坐在一个有配额的席位上，否则后端会直接拒绝。
  // 这里在提交前就纠正，而不是让用户吃一个 422。
  const ownerSeatOptions = useMemo(() => {
    const opts: { code: SeatGender; label: string; quota: number }[] = [
      { code: 'MALE', label: '男角色席', quota: male },
      { code: 'FEMALE', label: '女角色席', quota: female },
      { code: 'ANY', label: '不限角色席', quota: any },
    ]
    return opts.filter((o) => o.quota > 0)
  }, [male, female, any])

  const effectiveOwnerSeat: SeatGender =
    ownerSeatOptions.some((o) => o.code === ownerSeat)
      ? ownerSeat
      : (ownerSeatOptions[0]?.code ?? 'ANY')

  const seatPreview = [
    { gender: 'MALE', label: '男角色席', quota: male, taken: 0, remaining: male },
    { gender: 'FEMALE', label: '女角色席', quota: female, taken: 0, remaining: female },
    { gender: 'ANY', label: '不限角色席', quota: any, taken: 0, remaining: any },
  ] as const

  const localErrors: string[] = []
  if (capacity < 2) localErrors.push('总人数至少 2 人')
  if (capacity > 12) localErrors.push('总人数最多 12 人')
  if (minViable > capacity) localErrors.push('最低成行人数不能超过总人数')
  if (ownerSeatOptions.length === 0) localErrors.push('至少要配置一类席位')

  const createMut = useMutation({
    mutationFn: () =>
      api.createRoom({
        title: title.trim(),
        script_name: scriptName.trim(),
        venue_name: venueName.trim(),
        city: city.trim(),
        address: address.trim(),
        room_type: roomType,
        difficulty,
        theme,
        notes: notes.trim(),
        start_at: localInputToISO(startAt),
        male_seats: male,
        female_seats: female,
        any_seats: any,
        min_viable: minViable,
        owner_seat: effectiveOwnerSeat,
      }),
    onSuccess: (card) => {
      toast.success('车开好了', '已经上墙，等人来坐')
      navigate(`/rooms/${card.room.id}`)
    },
    onError: (e) => setErr(e instanceof ApiError ? e.message : '创建失败，稍后再试'),
  })

  return (
    <div className="mx-auto max-w-2xl">
      <div className="mb-5 flex items-center gap-3">
        <span className="grid size-11 place-items-center rounded-xl bg-brand-grad shadow-glow">
          <Car className="size-5 text-white" aria-hidden />
        </span>
        <div>
          <h1 className="text-lg font-bold tracking-tight text-ink">开一辆车</h1>
          <p className="text-xs text-ink-muted">发到墙上，缺的人让系统帮你找</p>
        </div>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setErr('')
          if (localErrors.length > 0) return
          createMut.mutate()
        }}
        className="flex flex-col gap-4"
      >
        <section className="card flex flex-col gap-4 p-5">
          <h2 className="text-sm font-semibold text-ink">这是什么局</h2>

          <div className="grid grid-cols-2 gap-3">
            <Field label="局类型">
              <Select
                value={roomType}
                onChange={(e) => setRoomType(e.target.value as RoomType)}
              >
                {(enums?.room_types ?? []).map((t) => (
                  <option key={t.code} value={t.code}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="主题">
              <Select value={theme} onChange={(e) => setTheme(e.target.value)}>
                <option value="">不指定</option>
                {(enums?.themes ?? []).map((t) => (
                  <option key={t.code} value={t.code}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>

          <Field label="剧本 / 主题名" hint="墙上卡片的主标题">
            <Input
              value={scriptName}
              onChange={(e) => setScriptName(e.target.value)}
              placeholder="例如 年轮 / 恐怖童谣"
              required
              maxLength={40}
            />
          </Field>

          <Field label="一句话招募语" hint="2-40 字，让人一眼知道这车什么气质">
            <Input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="例如 周六晚硬核推理，求两个能扛的"
              required
              maxLength={40}
            />
          </Field>

          <Field label="难度">
            <div className="flex gap-1.5">
              {[1, 2, 3, 4, 5].map((n) => (
                <button
                  key={n}
                  type="button"
                  onClick={() => setDifficulty(n)}
                  className={cn(
                    'h-10 flex-1 rounded-xl border text-sm transition',
                    difficulty === n
                      ? 'border-brand/50 bg-brand/15 text-brand'
                      : 'border-hairline bg-raised text-ink-muted hover:text-ink',
                  )}
                  aria-label={`难度 ${n} 星`}
                >
                  {'★'.repeat(n)}
                </button>
              ))}
            </div>
          </Field>
        </section>

        <section className="card flex flex-col gap-4 p-5">
          <h2 className="text-sm font-semibold text-ink">在哪、什么时候</h2>

          <div className="grid grid-cols-2 gap-3">
            <Field label="城市">
              <Input
                value={city}
                onChange={(e) => setCity(e.target.value)}
                placeholder="例如 杭州"
                required
                maxLength={20}
                list="city-list"
              />
              <datalist id="city-list">
                {(cities ?? []).map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
            </Field>
            <Field label="店名">
              <Input
                value={venueName}
                onChange={(e) => setVenueName(e.target.value)}
                placeholder="例如 迷雾剧本社"
                required
                maxLength={40}
              />
            </Field>
          </div>

          <Field label="详细地址（选填）">
            <Input
              value={address}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="例如 拱墅区莫干山路 100 号 3 楼"
              maxLength={80}
            />
          </Field>

          <Field label="开局时间" hint="至少比现在晚 15 分钟，最多 60 天以内">
            <Input
              type="datetime-local"
              value={startAt}
              onChange={(e) => setStartAt(e.target.value)}
              required
            />
          </Field>
        </section>

        <section className="card flex flex-col gap-4 p-5">
          <div>
            <h2 className="text-sm font-semibold text-ink">要几个人</h2>
            <p className="mt-1 text-xs text-ink-faint">
              席位按剧本角色需求划分。不限角色席任何人都能坐。
            </p>
          </div>

          <div className="grid gap-2 sm:grid-cols-3">
            <NumberStepper label="男角色席" value={male} onChange={setMale} max={12} />
            <NumberStepper label="女角色席" value={female} onChange={setFemale} max={12} />
            <NumberStepper label="不限角色席" value={any} onChange={setAny} max={12} />
          </div>

          <div className="flex items-center justify-between rounded-xl border border-hairline bg-raised px-4 py-3">
            <div>
              <p className="text-sm text-ink">
                总共 <span className="tnum font-mono font-bold">{capacity}</span> 人
              </p>
              <p className="mt-0.5 text-xs text-ink-faint">这就是卡片上「N缺M」的 N</p>
            </div>
            <SeatMeter seats={[...seatPreview]} capacity={capacity} className="max-w-[130px] justify-end" />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <Field label="最低成行人数" hint="到点没凑够就算炸车">
              <NumberStepper
                label="最少"
                value={minViable}
                onChange={setMinViable}
                min={1}
                max={Math.max(1, capacity)}
              />
            </Field>
            <Field label="你自己坐哪个席位">
              <Select
                value={effectiveOwnerSeat}
                onChange={(e) => setOwnerSeat(e.target.value as SeatGender)}
              >
                {ownerSeatOptions.map((o) => (
                  <option key={o.code} value={o.code}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>

          <Field label="补充说明（选填）" hint="最多 200 字">
            <Textarea
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              rows={3}
              maxLength={200}
              placeholder="例如 新手友好，会带；AA 制人均 128；迟到超过 20 分钟不等"
            />
          </Field>
        </section>

        {(localErrors.length > 0 || err) && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-xl border border-danger/40 bg-danger/10 px-4 py-3 text-xs text-danger"
          >
            <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
            <ul className="flex flex-col gap-0.5">
              {localErrors.map((e2) => (
                <li key={e2}>{e2}</li>
              ))}
              {err && <li>{err}</li>}
            </ul>
          </div>
        )}

        <div className="flex gap-2 pb-8">
          <Button type="button" variant="ghost" onClick={() => navigate(-1)}>
            取消
          </Button>
          <Button
            type="submit"
            size="lg"
            className="flex-1"
            loading={createMut.isPending}
            disabled={localErrors.length > 0}
            disabledReason={localErrors[0]}
          >
            发到拼车墙
          </Button>
        </div>
      </form>
    </div>
  )
}
