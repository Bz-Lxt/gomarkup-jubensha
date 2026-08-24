import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { Car, LogOut, Plus, Ticket, User as UserIcon } from 'lucide-react'

import { Avatar } from '@/components/ui/Avatar'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/utils'
import { useAuth } from '@/store/auth'

const NAV = [
  { to: '/', label: '拼车墙', Icon: Car, end: true },
  { to: '/mine', label: '我的车', Icon: Ticket, end: false },
]

export function Layout() {
  const user = useAuth((s) => s.user)
  const logout = useAuth((s) => s.logout)
  const navigate = useNavigate()

  return (
    <div className="flex min-h-full flex-col">
      <header className="sticky top-0 z-30 border-b border-hairline bg-base/80 backdrop-blur-xl">
        <div className="mx-auto flex h-16 max-w-[1600px] items-center gap-3 px-4 sm:gap-6 sm:px-6">
          <NavLink to="/" className="flex shrink-0 items-center gap-2">
            <span className="grid size-9 place-items-center rounded-xl bg-brand-grad shadow-glow">
              <Car className="size-5 text-white" aria-hidden />
            </span>
            <span className="hidden text-sm font-semibold tracking-tight text-ink sm:block">
              野生拼车墙
            </span>
          </NavLink>

          <nav className="flex items-center gap-1">
            {NAV.map(({ to, label, Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    'inline-flex h-10 items-center gap-1.5 rounded-xl px-3 text-sm transition',
                    isActive
                      ? 'bg-raised text-ink'
                      : 'text-ink-muted hover:bg-raised/60 hover:text-ink',
                  )
                }
              >
                <Icon className="size-4" aria-hidden />
                {label}
              </NavLink>
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-2">
            {/* 窄屏下文字被隐藏，只剩图标。没有 aria-label 的话这就是一个
                无名按钮，读屏用户完全不知道它做什么。 */}
            <Button
              size="sm"
              onClick={() => navigate('/create')}
              className="gap-1.5"
              aria-label="开车"
            >
              <Plus className="size-4" aria-hidden />
              <span className="hidden sm:inline">开车</span>
            </Button>

            {user ? (
              <div className="flex items-center gap-1">
                <NavLink
                  to="/me"
                  className="flex items-center gap-2 rounded-xl px-1.5 py-1 transition hover:bg-raised"
                >
                  <Avatar name={user.nickname} avatar={user.avatar} size="sm" />
                  <span className="hidden max-w-24 truncate text-sm text-ink sm:block">
                    {user.nickname}
                  </span>
                </NavLink>
                <button
                  onClick={() => {
                    logout()
                    navigate('/login')
                  }}
                  className="rounded-xl p-2.5 text-ink-faint transition hover:bg-raised hover:text-ink"
                  aria-label="退出登录"
                  title="退出登录"
                >
                  <LogOut className="size-4" />
                </button>
              </div>
            ) : (
              <Button
                size="sm"
                variant="outline"
                onClick={() => navigate('/login')}
                aria-label="登录"
              >
                <UserIcon className="size-4" aria-hidden />
                <span className="hidden sm:inline">登录</span>
              </Button>
            )}
          </div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-[1600px] flex-1 px-4 py-5 sm:px-6">
        <Outlet />
      </main>

      <footer className="border-t border-hairline px-4 py-5 text-center text-xs text-ink-faint">
        本项目全部数据为本地演示数据，未接入任何外部第三方服务
      </footer>
    </div>
  )
}
