import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}

/**
 * 头像色板。长度必须与后端 service/auth.go 的 avatarPaletteSize 一致（8）。
 *
 * 后端下发的头像标识形如 "local:3"，前端映射到渐变 + 昵称首字，纯 CSS 渲染。
 * 刻意不接 DiceBear / Gravatar：那类外部服务会让内网或离线环境下头像全部裂图。
 */
export const AVATAR_PALETTE = [
  'from-violet-500 to-fuchsia-500',
  'from-sky-500 to-cyan-400',
  'from-emerald-500 to-teal-400',
  'from-amber-500 to-orange-500',
  'from-rose-500 to-pink-500',
  'from-indigo-500 to-blue-500',
  'from-lime-500 to-green-500',
  'from-purple-500 to-indigo-500',
] as const

export function avatarGradient(avatar: string, fallbackSeed = ''): string {
  const m = /^local:(\d+)$/.exec(avatar)
  let idx: number
  if (m?.[1] !== undefined) {
    idx = Number(m[1]) % AVATAR_PALETTE.length
  } else {
    let sum = 0
    for (const ch of fallbackSeed) sum = (sum * 31 + ch.charCodeAt(0)) >>> 0
    idx = sum % AVATAR_PALETTE.length
  }
  return AVATAR_PALETTE[idx] ?? AVATAR_PALETTE[0]
}

/** 取昵称首字作为头像文字。中文取首字，英文取首字母大写。 */
export function initial(name: string): string {
  const trimmed = name.trim()
  if (!trimmed) return '?'
  const first = [...trimmed][0] ?? '?'
  return /[a-z]/i.test(first) ? first.toUpperCase() : first
}

/**
 * 把秒数格式化为倒计时文案。分级与 DesignSpec §4.4 一致。
 *
 * 注意：这里只做「秒数 → 文案」的纯格式化。是否处于危险态一律以后端下发的
 * at_risk 为准，前端不重新实现判定逻辑，否则两端阈值必然分叉。
 */
export function formatCountdown(totalSeconds: number): string {
  if (totalSeconds <= 0) return '已开局'
  const d = Math.floor(totalSeconds / 86400)
  const h = Math.floor((totalSeconds % 86400) / 3600)
  const m = Math.floor((totalSeconds % 3600) / 60)
  const s = totalSeconds % 60
  if (d > 0) return `${d}天${h}小时`
  if (h > 0) return `${h}小时${String(m).padStart(2, '0')}分`
  if (m > 0) return `${m}分${String(s).padStart(2, '0')}秒`
  return `${s}秒`
}

/**
 * 格式化后端下发的时间字符串。
 *
 * 后端已经统一按东八区序列化（timeutil.In），因此这里直接读取本地时间部分，
 * 不做二次时区换算——再换一次就会出现 KB [Go][TZ] 记录的那种 8 小时错位。
 */
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}月${d.getDate()}日 ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/** 为 datetime-local 输入框生成默认值：默认「今天此刻 + N 小时」，取整到 30 分。 */
export function defaultStartAt(hoursAhead = 4): string {
  const d = new Date(Date.now() + hoursAhead * 3600_000)
  d.setMinutes(d.getMinutes() >= 30 ? 30 : 0, 0, 0)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

/**
 * 把 datetime-local 的值转成带偏移的 ISO 串。
 *
 * datetime-local 给出的是「无时区的墙上时间」。直接 new Date(v).toISOString()
 * 会按浏览器本地时区解释再转 UTC，用户在非东八区时区的浏览器上开车，
 * 开局时间会整体偏移。显式带上本地偏移量，让后端拿到无歧义的时刻。
 */
export function localInputToISO(value: string): string {
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const offsetMin = -d.getTimezoneOffset()
  const sign = offsetMin >= 0 ? '+' : '-'
  const abs = Math.abs(offsetMin)
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}` +
    `T${pad(d.getHours())}:${pad(d.getMinutes())}:00` +
    `${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`
  )
}

/** 生成客户端消息幂等 ID。重试同一条消息时复用，服务端据此去重。 */
export function newClientMsgID(): string {
  if (globalThis.crypto?.randomUUID) return globalThis.crypto.randomUUID()
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** 房间状态 → 视觉语义。at_risk 优先级高于 RECRUITING 本身。 */
export type StatusTone = 'brand' | 'danger' | 'success' | 'info' | 'muted'

export function statusTone(status: string, atRisk = false): StatusTone {
  if (status === 'RECRUITING') return atRisk ? 'danger' : 'brand'
  if (status === 'LOCKED' || status === 'CONFIRMED') return 'success'
  if (status === 'IN_PROGRESS') return 'info'
  return 'muted'
}

export const TONE_DOT: Record<StatusTone, string> = {
  brand: 'bg-brand',
  danger: 'bg-danger',
  success: 'bg-success',
  info: 'bg-info',
  muted: 'bg-ink-faint',
}

export const TONE_TEXT: Record<StatusTone, string> = {
  brand: 'text-brand',
  danger: 'text-danger',
  success: 'text-success',
  info: 'text-info',
  muted: 'text-ink-muted',
}

export const SEAT_DOT: Record<string, string> = {
  MALE: 'bg-sky-400',
  FEMALE: 'bg-pink-400',
  ANY: 'bg-violet-400',
}
