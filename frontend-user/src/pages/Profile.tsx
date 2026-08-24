import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'

import { Avatar } from '@/components/ui/Avatar'
import { Button } from '@/components/ui/Button'
import { Field, Input, Textarea } from '@/components/ui/Field'
import { ApiError, api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useAuth } from '@/store/auth'
import { toast } from '@/store/toast'

export function ProfilePage() {
  const user = useAuth((s) => s.user)
  const setUser = useAuth((s) => s.setUser)

  const [nickname, setNickname] = useState(user?.nickname ?? '')
  const [city, setCity] = useState(user?.city ?? '')
  const [bio, setBio] = useState(user?.bio ?? '')
  const [tags, setTags] = useState<string[]>(user?.tags ?? [])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const { data: catalog } = useQuery({
    queryKey: ['tags'],
    queryFn: () => api.tagCatalog(),
    staleTime: 30 * 60_000,
  })
  const { data: enums } = useQuery({
    queryKey: ['enums'],
    queryFn: () => api.enums(),
    staleTime: 30 * 60_000,
  })
  const maxTags = enums?.max_tags ?? 3

  if (!user) return null

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      const updated = await api.updateMe({
        nickname: nickname.trim(),
        city: city.trim(),
        bio: bio.trim(),
        tags,
      })
      setUser(updated)
      toast.success('资料已更新')
    } catch (e2) {
      setErr(e2 instanceof ApiError ? e2.message : '保存失败，稍后再试')
    } finally {
      setBusy(false)
    }
  }

  const toggleTag = (code: string) => {
    setTags((prev) =>
      prev.includes(code)
        ? prev.filter((t) => t !== code)
        : prev.length >= maxTags
          ? prev
          : [...prev, code],
    )
  }

  return (
    <div className="mx-auto max-w-xl">
      <section className="card mb-4 flex items-center gap-4 p-5">
        <Avatar name={user.nickname} avatar={user.avatar} size="lg" />
        <div className="min-w-0">
          <h1 className="truncate text-lg font-bold tracking-tight text-ink">{user.nickname}</h1>
          <p className="text-xs text-ink-muted">@{user.username}</p>
          <p className="tnum mt-1 text-xs text-ink-faint">信用分 {user.reputation}</p>
        </div>
      </section>

      <form onSubmit={save} className="card flex flex-col gap-4 p-5">
        <h2 className="text-sm font-semibold text-ink">编辑资料</h2>

        <div className="grid grid-cols-2 gap-3">
          <Field label="昵称">
            <Input value={nickname} onChange={(e) => setNickname(e.target.value)} maxLength={24} />
          </Field>
          <Field label="常玩城市">
            <Input value={city} onChange={(e) => setCity(e.target.value)} maxLength={20} />
          </Field>
        </div>

        <Field label="个人简介" hint="车主会看这个决定要不要让你上车">
          <Textarea
            value={bio}
            onChange={(e) => setBio(e.target.value)}
            rows={3}
            maxLength={200}
            placeholder="例如 玩过 60+ 本，擅长推理，不怕恐怖，会带新人"
          />
        </Field>

        <Field label="玩家标签" hint={`最多 ${maxTags} 个（已选 ${tags.length}）`}>
          <div className="flex flex-wrap gap-1.5">
            {(catalog ?? []).map((t) => {
              const on = tags.includes(t.code)
              return (
                <button
                  key={t.code}
                  type="button"
                  onClick={() => toggleTag(t.code)}
                  disabled={!on && tags.length >= maxTags}
                  title={t.phrase}
                  className={cn(
                    'rounded-full border px-3 py-1.5 text-xs transition',
                    on
                      ? 'border-brand/50 bg-brand/15 text-brand'
                      : 'border-hairline bg-raised text-ink-muted hover:text-ink disabled:opacity-40',
                  )}
                >
                  {t.label}
                </button>
              )
            })}
          </div>
        </Field>

        {err && (
          <p role="alert" className="rounded-xl border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger">
            {err}
          </p>
        )}

        <Button type="submit" loading={busy} className="self-start">
          保存
        </Button>
      </form>
    </div>
  )
}
