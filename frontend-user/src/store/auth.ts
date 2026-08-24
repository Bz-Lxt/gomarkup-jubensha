import { create } from 'zustand'

import { ApiError, SESSION_EXPIRED_EVENT, api, tokens } from '@/lib/api'
import type { User } from '@/types'

interface AuthState {
  user: User | null
  /** booting 为真表示还在用已有令牌换取用户信息，此时不应把用户踢到登录页 */
  booting: boolean
  login: (username: string, password: string) => Promise<void>
  register: (input: {
    username: string
    password: string
    nickname?: string
    city?: string
    phone?: string
    tags?: string[]
  }) => Promise<void>
  logout: () => void
  /** 用本地令牌恢复会话。应用启动时调用一次。 */
  restore: () => Promise<void>
  setUser: (u: User) => void
}

export const useAuth = create<AuthState>((set) => ({
  user: null,
  booting: true,

  login: async (username, password) => {
    const res = await api.login(username, password)
    tokens.save(res.tokens)
    set({ user: res.user, booting: false })
  },

  register: async (input) => {
    const res = await api.register(input)
    tokens.save(res.tokens)
    set({ user: res.user, booting: false })
  },

  logout: () => {
    tokens.clear()
    set({ user: null, booting: false })
  },

  restore: async () => {
    if (!tokens.access() && !tokens.refresh()) {
      set({ user: null, booting: false })
      return
    }
    try {
      const user = await api.me()
      set({ user, booting: false })
    } catch (err) {
      // 令牌失效就静默降级为未登录。这里不弹错误提示：用户只是隔了几天
      // 再打开页面，看到「登录已过期」的红色 toast 是纯粹的噪音。
      if (err instanceof ApiError) tokens.clear()
      set({ user: null, booting: false })
    }
  },

  setUser: (u) => set({ user: u }),
}))

/**
 * 会话彻底失效时（刷新令牌也过期），api 层会广播事件。
 * 在这里统一清空用户态，由路由守卫负责跳转，避免在 api 层直接操作 history。
 */
window.addEventListener(SESSION_EXPIRED_EVENT, () => {
  useAuth.setState({ user: null, booting: false })
})
