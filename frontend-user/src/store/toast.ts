import { create } from 'zustand'

export type ToastTone = 'success' | 'error' | 'info' | 'warn'

export interface Toast {
  id: number
  tone: ToastTone
  title: string
  desc?: string
}

interface ToastState {
  items: Toast[]
  push: (t: Omit<Toast, 'id'>) => void
  dismiss: (id: number) => void
}

let seq = 0

export const useToast = create<ToastState>((set) => ({
  items: [],
  push: (t) => {
    const id = ++seq
    set((s) => ({ items: [...s.items, { ...t, id }] }))
    // 错误留久一点：用户需要时间读完「车已经满了，晚了一步」并决定下一步。
    const ttl = t.tone === 'error' ? 5000 : 3000
    setTimeout(() => {
      set((s) => ({ items: s.items.filter((i) => i.id !== id) }))
    }, ttl)
  },
  dismiss: (id) => set((s) => ({ items: s.items.filter((i) => i.id !== id) })),
}))

export const toast = {
  success: (title: string, desc?: string) => useToast.getState().push({ tone: 'success', title, desc }),
  error: (title: string, desc?: string) => useToast.getState().push({ tone: 'error', title, desc }),
  info: (title: string, desc?: string) => useToast.getState().push({ tone: 'info', title, desc }),
  warn: (title: string, desc?: string) => useToast.getState().push({ tone: 'warn', title, desc }),
}
