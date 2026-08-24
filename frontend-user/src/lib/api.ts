import type {
  AuthResult,
  Backfill,
  CreateRoomInput,
  JoinResult,
  Message,
  MetaEnums,
  RoomCard,
  SeatGender,
  SlotAudit,
  SlotSnapshot,
  StateLog,
  TagOption,
  TokenPair,
  UnreadCount,
  User,
  WallResult,
} from '@/types'

/**
 * API_BASE 在构建期由 Vite 烘焙（docker-compose 通过 build args 传入）。
 * 留空则走同源相对路径，交给 nginx 反代——这是生产默认路径。
 */
export const API_BASE = (import.meta.env.VITE_API_BASE ?? '').replace(/\/+$/, '')
export const WS_BASE = (import.meta.env.VITE_WS_BASE ?? '').replace(/\/+$/, '')

const ACCESS_KEY = 'jbs.access'
const REFRESH_KEY = 'jbs.refresh'

/**
 * ApiError 携带后端的机器可读 code。
 *
 * 前端所有分支判断都读 code，展示则用 message —— 绝不在前端拼错误文案，
 * 否则同一个失败在不同页面会有不同说法。
 */
export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly details: Record<string, unknown>

  constructor(code: string, message: string, status: number, details: Record<string, unknown> = {}) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.details = details
  }

  /** 席位类冲突：调用方应当立刻刷新房间快照，因为服务端状态已经变了。 */
  get isSlotConflict(): boolean {
    return (
      this.code === 'SLOT_FULL' ||
      this.code === 'SEAT_GENDER_FULL' ||
      this.code === 'ROOM_NOT_RECRUITING' ||
      this.code === 'ALREADY_ON_BOARD'
    )
  }
}

export const tokens = {
  access: () => localStorage.getItem(ACCESS_KEY) ?? '',
  refresh: () => localStorage.getItem(REFRESH_KEY) ?? '',
  save(pair: TokenPair) {
    localStorage.setItem(ACCESS_KEY, pair.access_token)
    localStorage.setItem(REFRESH_KEY, pair.refresh_token)
  },
  clear() {
    localStorage.removeItem(ACCESS_KEY)
    localStorage.removeItem(REFRESH_KEY)
  },
}

/** 会话彻底失效时广播，由 auth store 监听并跳登录页。 */
export const SESSION_EXPIRED_EVENT = 'jbs:session-expired'

interface Envelope<T> {
  ok: boolean
  data: T
  error: { code: string; message: string; details: Record<string, unknown> } | null
}

/**
 * refreshInFlight 让并发的 401 只触发一次刷新。
 *
 * 没有它的话，首屏并行发出的 4 个请求会同时 401，进而并发调用 /auth/refresh。
 * 后发的刷新请求会拿着已被轮换的 refresh token，直接把用户踢下线。
 */
let refreshInFlight: Promise<boolean> | null = null

async function doRefresh(): Promise<boolean> {
  const rt = tokens.refresh()
  if (!rt) return false
  try {
    const res = await fetch(`${API_BASE}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: rt }),
    })
    const body = (await res.json()) as Envelope<AuthResult>
    if (!res.ok || !body.ok) return false
    tokens.save(body.data.tokens)
    return true
  } catch {
    return false
  }
}

function refreshOnce(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null
    })
  }
  return refreshInFlight
}

interface RequestOptions {
  method?: string
  body?: unknown
  auth?: boolean
  signal?: AbortSignal
  /** 内部使用：标记这是刷新令牌后的重试，避免无限递归 */
  _retried?: boolean
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, auth = true, signal } = opts

  const headers: Record<string, string> = {}
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const at = tokens.access()
  if (auth && at) headers['Authorization'] = `Bearer ${at}`

  let res: Response
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError('NETWORK_ERROR', '网络好像断了，检查一下连接再试', 0)
  }

  // 204 或空响应体：不要盲目 res.json()，那会抛 SyntaxError 掩盖真实结果。
  const text = await res.text()
  let envelope: Envelope<T> | null = null
  if (text) {
    try {
      envelope = JSON.parse(text) as Envelope<T>
    } catch {
      throw new ApiError('BAD_RESPONSE', '服务端返回了无法解析的内容', res.status)
    }
  }

  if (res.ok && envelope?.ok) return envelope.data

  const code = envelope?.error?.code ?? 'INTERNAL_ERROR'
  const message = envelope?.error?.message ?? '请求失败了，稍后再试'

  // access token 过期：静默刷新一次并重放原请求。
  // 只对 TOKEN_EXPIRED 做重放；TOKEN_INVALID 说明令牌本身是坏的，重试无意义。
  if (res.status === 401 && code === 'TOKEN_EXPIRED' && auth && !opts._retried) {
    if (await refreshOnce()) {
      return request<T>(path, { ...opts, _retried: true })
    }
    tokens.clear()
    window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT))
  } else if (res.status === 401 && auth) {
    tokens.clear()
    window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT))
  }

  throw new ApiError(code, message, res.status, envelope?.error?.details ?? {})
}

// ------------------------------------------------------------------- 端点

export interface WallQuery {
  city?: string
  room_type?: string
  theme?: string
  q?: string
  status?: string
  joinable?: boolean
  limit?: number
  offset?: number
}

function qs(params: Record<string, string | number | boolean | undefined>): string {
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === '' || v === false) continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

export const api = {
  register: (body: {
    username: string
    password: string
    phone?: string
    nickname?: string
    city?: string
    tags?: string[]
  }) => request<AuthResult>('/api/auth/register', { method: 'POST', body, auth: false }),

  login: (username: string, password: string) =>
    request<AuthResult>('/api/auth/login', {
      method: 'POST',
      body: { username, password },
      auth: false,
    }),

  me: () => request<User>('/api/users/me'),

  updateMe: (body: {
    nickname?: string
    city?: string
    bio?: string
    phone?: string
    tags?: string[]
  }) => request<User>('/api/users/me', { method: 'PATCH', body }),

  tagCatalog: () => request<TagOption[]>('/api/meta/tags', { auth: false }),
  enums: () => request<MetaEnums>('/api/meta/enums', { auth: false }),
  cities: () => request<string[]>('/api/meta/cities', { auth: false }),

  wall: (query: WallQuery = {}, signal?: AbortSignal) =>
    request<WallResult>(`/api/rooms${qs({ ...query })}`, { signal }),

  mine: () => request<WallResult>('/api/rooms/mine'),
  unread: () => request<UnreadCount[]>('/api/rooms/unread'),

  room: (id: number, signal?: AbortSignal) => request<RoomCard>(`/api/rooms/${id}`, { signal }),
  roomHistory: (id: number) => request<StateLog[]>(`/api/rooms/${id}/history`),
  createRoom: (body: CreateRoomInput) =>
    request<RoomCard>('/api/rooms', { method: 'POST', body }),

  // 抢位类端点返回 SlotSnapshot（或含 snapshot 的 JoinResult），而不是完整
  // RoomCard：临界区内只碰席位账目，不去重查成员与用户资料。
  join: (id: number, seat: SeatGender) =>
    request<JoinResult>(`/api/rooms/${id}/join`, { method: 'POST', body: { seat_gender: seat } }),
  leave: (id: number) => request<SlotSnapshot>(`/api/rooms/${id}/leave`, { method: 'POST' }),
  confirm: (id: number, userID?: number) =>
    request<SlotSnapshot>(`/api/rooms/${id}/confirm`, {
      method: 'POST',
      body: { user_id: userID ?? 0 },
    }),
  kick: (id: number, userID: number, reason?: string) =>
    request<SlotSnapshot>(`/api/rooms/${id}/kick`, {
      method: 'POST',
      body: { user_id: userID, reason: reason ?? '' },
    }),
  lockRoom: (id: number) => request<SlotSnapshot>(`/api/rooms/${id}/lock`, { method: 'POST' }),
  cancelRoom: (id: number, reason?: string) =>
    request<SlotSnapshot>(`/api/rooms/${id}/cancel`, {
      method: 'POST',
      body: { reason: reason ?? '' },
    }),
  audit: (id: number) => request<SlotAudit>(`/api/rooms/${id}/audit`),

  messages: (id: number, since = 0) =>
    request<Backfill>(`/api/rooms/${id}/messages${qs({ since })}`),
  sendMessage: (
    id: number,
    body: { content: string; msg_type: string; tag_code?: string; client_msg_id: string },
  ) => request<Message>(`/api/rooms/${id}/messages`, { method: 'POST', body }),
  ack: (id: number, seq: number) =>
    request<unknown>(`/api/rooms/${id}/read`, { method: 'POST', body: { seq } }),
}
