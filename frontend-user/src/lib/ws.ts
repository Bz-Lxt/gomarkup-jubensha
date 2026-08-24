import { WS_BASE, tokens } from '@/lib/api'
import type { Envelope } from '@/types'

export type ConnState = 'connecting' | 'open' | 'reconnecting' | 'closed'

interface SocketOptions {
  /** 相对路径，如 `/ws/rooms/12` 或 `/ws/wall` */
  path: string
  onFrame: (env: Envelope) => void
  onState: (state: ConnState) => void
  /**
   * 每次连接建立后调用，用于发送补齐请求。
   * 传入的 send 只在本次连接有效。
   */
  onOpen?: (send: (type: string, data?: unknown) => void) => void
  /** 是否携带 access token（墙连接允许匿名） */
  auth?: boolean
}

function resolveURL(path: string): string {
  if (WS_BASE) return `${WS_BASE}${path}`
  // 同源部署：由 nginx 把 /ws 反代到后端，协议随页面自动升级为 wss。
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${path}`
}

/**
 * RoomSocket 是带自动重连的 WebSocket 封装。
 *
 * 三个必须处理好的现实问题：
 *
 * 1. **重连退避**。断线通常是服务重启或网络抖动，此时所有客户端会同时重连。
 *    固定 1s 重试会在服务刚起来的瞬间造成一次自我 DDoS，因此用指数退避
 *    + 随机抖动（jitter），把重连打散开。
 * 2. **补齐时机**。补齐请求必须在 onopen 之后发，而且每次重连都要重发——
 *    只在首次连接时拉一次，第二次断线期间的消息就永久丢了。
 * 3. **主动关闭与被动断开的区分**。用户离开页面时 close() 不应触发重连，
 *    否则组件卸载后仍有连接在后台反复重建。
 */
export class RoomSocket {
  private ws: WebSocket | null = null
  private attempt = 0
  private timer: ReturnType<typeof setTimeout> | null = null
  private disposed = false
  private readonly opts: SocketOptions

  constructor(opts: SocketOptions) {
    this.opts = opts
    this.connect()
  }

  private connect(): void {
    if (this.disposed) return

    let url = resolveURL(this.opts.path)
    if (this.opts.auth !== false) {
      const at = tokens.access()
      if (!at) {
        // 没有令牌就不要发起连接：服务端必然 401，反而触发一轮无意义的退避重连。
        this.opts.onState('closed')
        return
      }
      // 浏览器的 WebSocket API 不支持自定义请求头，因此令牌只能走 query。
      // 后端的 OptionalAuth/RequireAuth 中间件同时接受 access_token 查询参数。
      url += `${url.includes('?') ? '&' : '?'}access_token=${encodeURIComponent(at)}`
    }

    this.opts.onState(this.attempt === 0 ? 'connecting' : 'reconnecting')

    let ws: WebSocket
    try {
      ws = new WebSocket(url)
    } catch {
      this.scheduleReconnect()
      return
    }
    this.ws = ws

    ws.onopen = () => {
      this.attempt = 0
      this.opts.onState('open')
      this.opts.onOpen?.((type, data) => this.send(type, data))
    }

    ws.onmessage = (ev) => {
      if (typeof ev.data !== 'string') return
      try {
        this.opts.onFrame(JSON.parse(ev.data) as Envelope)
      } catch {
        // 单条坏帧不应拖垮整个连接，丢弃继续。
      }
    }

    ws.onerror = () => {
      // onerror 之后浏览器一定会触发 onclose，重连逻辑统一放在那里，
      // 避免同一次断开被处理两次、退避计数翻倍。
    }

    ws.onclose = () => {
      this.ws = null
      if (this.disposed) return
      this.scheduleReconnect()
    }
  }

  private scheduleReconnect(): void {
    if (this.disposed) return
    this.opts.onState('reconnecting')

    // 指数退避：0.5s → 1s → 2s → 4s → 8s，上限 15s；叠加 ±30% 抖动。
    const base = Math.min(500 * 2 ** this.attempt, 15_000)
    const jitter = base * (0.7 + Math.random() * 0.6)
    this.attempt += 1

    if (this.timer) clearTimeout(this.timer)
    this.timer = setTimeout(() => this.connect(), jitter)
  }

  send(type: string, data: unknown = {}): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false
    this.ws.send(JSON.stringify({ type, data }))
    return true
  }

  close(): void {
    this.disposed = true
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    // 1000 = 正常关闭。带上原因便于服务端日志区分「用户离开」与「异常断开」。
    this.ws?.close(1000, 'client leaving')
    this.ws = null
    this.opts.onState('closed')
  }
}
