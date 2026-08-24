import { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Car, Sparkles } from 'lucide-react'

import { Button } from '@/components/ui/Button'
import { Field, Input } from '@/components/ui/Field'
import { ApiError, api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useAuth } from '@/store/auth'
import { toast } from '@/store/toast'

type Mode = 'login' | 'register'

/**
 * 演示账号。用户名取自 backend/internal/seed/seed.go 的 demoUsers[0]，
 * 口令须与同文件的 DemoPassword 一致；改动种子数据时这里必须同步，
 * 否则「快速体验」按钮会填出一个不存在的账号。
 */
const DEMO = { username: 'alice', password: 'jbs12345' }

export function AuthPage({ mode: initial }: { mode: Mode }) {
  const [mode, setMode] = useState<Mode>(initial)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [nickname, setNickname] = useState('')
  const [city, setCity] = useState('')
  const [tags, setTags] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const login = useAuth((s) => s.login)
  const register = useAuth((s) => s.register)
  const navigate = useNavigate()

  const { data: tagCatalog } = useQuery({
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

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setErr('')
    setBusy(true)
    try {
      if (mode === 'login') {
        await login(username.trim(), password)
      } else {
        await register({
          username: username.trim(),
          password,
          nickname: nickname.trim() || undefined,
          city: city.trim() || undefined,
          tags,
        })
      }
      toast.success(mode === 'login' ? '欢迎回来' : '账号建好了，去墙上找车吧')
      navigate('/')
    } catch (e2) {
      // 表单错误就地展示，不用 toast：用户的注意力在表单上。
      setErr(e2 instanceof ApiError ? e2.message : '操作失败，稍后再试')
    } finally {
      setBusy(false)
    }
  }

  const fillDemo = () => {
    setMode('login')
    setUsername(DEMO.username)
    setPassword(DEMO.password)
    setErr('')
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
    <div className="mx-auto flex min-h-[calc(100vh-9rem)] max-w-md flex-col justify-center gap-6 py-8">
      <div className="text-center">
        <span className="mx-auto grid size-14 place-items-center rounded-2xl bg-brand-grad shadow-glow">
          <Car className="size-7 text-white" aria-hidden />
        </span>
        <h1 className="mt-4 text-2xl font-bold tracking-tight text-ink">野生拼车墙</h1>
        <p className="mt-1.5 text-sm text-ink-muted">
          三缺一别再群里刷屏了，上墙找人，实时看谁上车
        </p>
      </div>

      <div className="card p-6">
        {/* 模式切换用分段控件而不是两个页面：注册/登录来回跳会丢输入 */}
        <div className="mb-5 grid grid-cols-2 gap-1 rounded-xl border border-hairline bg-raised p-1">
          {(['login', 'register'] as const).map((m) => (
            <button
              key={m}
              onClick={() => {
                setMode(m)
                setErr('')
              }}
              className={cn(
                'h-9 rounded-lg text-sm transition',
                mode === m ? 'bg-brand-grad text-white' : 'text-ink-muted hover:text-ink',
              )}
            >
              {m === 'login' ? '登录' : '注册'}
            </button>
          ))}
        </div>

        <form onSubmit={submit} className="flex flex-col gap-4">
          <Field label="用户名" hint={mode === 'register' ? '3-24 位字母、数字或下划线' : undefined}>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              placeholder="例如 alice"
              required
            />
          </Field>

          <Field
            label="密码"
            hint={mode === 'register' ? '至少 8 位，需同时包含字母和数字' : undefined}
          >
            <Input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              placeholder="••••••••"
              required
            />
          </Field>

          {mode === 'register' && (
            <>
              <div className="grid grid-cols-2 gap-3">
                <Field label="昵称（选填）">
                  <Input
                    value={nickname}
                    onChange={(e) => setNickname(e.target.value)}
                    placeholder="墙上显示的名字"
                  />
                </Field>
                <Field label="常玩城市（选填）">
                  <Input
                    value={city}
                    onChange={(e) => setCity(e.target.value)}
                    placeholder="例如 杭州"
                  />
                </Field>
              </div>

              <Field
                label="玩家标签"
                hint={`最多选 ${maxTags} 个，车主据此判断你的风格（已选 ${tags.length}）`}
              >
                <div className="flex flex-wrap gap-1.5">
                  {(tagCatalog ?? []).map((t) => {
                    const on = tags.includes(t.code)
                    return (
                      <button
                        key={t.code}
                        type="button"
                        onClick={() => toggleTag(t.code)}
                        disabled={!on && tags.length >= maxTags}
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
            </>
          )}

          {err && (
            <p
              role="alert"
              className="rounded-xl border border-danger/40 bg-danger/10 px-3 py-2 text-xs text-danger"
            >
              {err}
            </p>
          )}

          <Button type="submit" size="lg" loading={busy} className="mt-1">
            {mode === 'login' ? '登录' : '注册并开始找车'}
          </Button>
        </form>

        <button
          onClick={fillDemo}
          className="mt-4 flex w-full items-center justify-center gap-1.5 text-xs text-ink-faint transition hover:text-brand"
        >
          <Sparkles className="size-3.5" aria-hidden />
          用演示账号快速体验（{DEMO.username} / {DEMO.password}）
        </button>
      </div>

      <p className="text-center text-xs text-ink-faint">
        不想登录也可以先
        <Link to="/" className="mx-1 text-brand hover:underline">
          逛逛拼车墙
        </Link>
        ，上车时再登录
      </p>
    </div>
  )
}
