import { useEffect } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { Loader2 } from 'lucide-react'

import { Layout } from '@/components/Layout'
import { Toaster } from '@/components/ui/Toaster'
import { AuthPage } from '@/pages/Auth'
import { CreateRoomPage } from '@/pages/CreateRoom'
import { MinePage } from '@/pages/Mine'
import { ProfilePage } from '@/pages/Profile'
import { RoomDetailPage } from '@/pages/RoomDetail'
import { WallPage } from '@/pages/Wall'
import { useAuth } from '@/store/auth'

/** 需要登录的页面。未登录时带上 from，登录后跳回原处。 */
function RequireAuth({ children }: { children: React.ReactNode }) {
  const user = useAuth((s) => s.user)
  const booting = useAuth((s) => s.booting)
  const loc = useLocation()

  // booting 期间不能判定为未登录，否则刷新页面会把已登录用户闪一下踢到登录页。
  if (booting) return <FullScreenLoader />
  if (!user) return <Navigate to="/login" state={{ from: loc.pathname }} replace />
  return <>{children}</>
}

function FullScreenLoader() {
  return (
    <div className="grid min-h-[60vh] place-items-center">
      <Loader2 className="size-6 animate-spin text-ink-faint" aria-label="加载中" />
    </div>
  )
}

export default function App() {
  const restore = useAuth((s) => s.restore)

  useEffect(() => {
    void restore()
  }, [restore])

  return (
    <>
      <Toaster />
      <Routes>
        <Route element={<Layout />}>
          {/* 墙对未登录用户开放：先看到内容才有注册动力 */}
          <Route path="/" element={<WallPage />} />
          <Route path="/rooms/:id" element={<RoomDetailPage />} />
          <Route
            path="/create"
            element={
              <RequireAuth>
                <CreateRoomPage />
              </RequireAuth>
            }
          />
          <Route
            path="/mine"
            element={
              <RequireAuth>
                <MinePage />
              </RequireAuth>
            }
          />
          <Route
            path="/me"
            element={
              <RequireAuth>
                <ProfilePage />
              </RequireAuth>
            }
          />
        </Route>

        <Route path="/login" element={<AuthPage mode="login" />} />
        <Route path="/register" element={<AuthPage mode="register" />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </>
  )
}
