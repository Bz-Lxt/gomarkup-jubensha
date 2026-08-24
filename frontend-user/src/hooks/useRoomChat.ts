import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '@/lib/api'
import { RoomSocket, type ConnState } from '@/lib/ws'
import { newClientMsgID } from '@/lib/utils'
import type { ChatItem } from '@/components/chat/MessageBubble'
import type {
  Backfill,
  Envelope,
  HelloData,
  Message,
  PresenceData,
  RoomStatusData,
  SlotSnapshot,
  WSErrorData,
} from '@/types'

interface Options {
  roomID: number
  /** 席位快照变更回调：房间详情页据此实时更新席位区 */
  onSlot?: (snap: SlotSnapshot) => void
  onStatus?: (data: RoomStatusData) => void
  onError?: (err: WSErrorData) => void
}

interface ChatController {
  items: ChatItem[]
  state: ConnState
  online: number[]
  /** 补齐时被截断：客户端落后太多，只拿到最近 N 条 */
  truncated: boolean
  send: (content: string, msgType?: 'TEXT' | 'TAG', tagCode?: string) => void
  retry: (clientMsgID: string) => void
}

/**
 * useRoomChat 管理房内聊天的完整生命周期。
 *
 * 三件事必须做对，否则「离线消息」这个需求点就是假的：
 *
 * 1. **游标持久化**。lastSeq 记录本地已收到的最大房内序号。它必须活过
 *    重连（存在 ref 里而非 state 的闭包里），否则每次重连都从 0 拉，
 *    要么重复要么爆量。
 * 2. **每次连接都补齐**。onOpen 里发 chat.pull，不是只在首次挂载时发。
 * 3. **按 seq 去重合并**。补齐区间与实时推送必然有重叠（补齐请求发出后、
 *    响应回来前到达的新消息），不去重就会出现重复气泡。
 */
export function useRoomChat({ roomID, onSlot, onStatus, onError }: Options): ChatController {
  const [items, setItems] = useState<ChatItem[]>([])
  const [state, setState] = useState<ConnState>('connecting')
  const [online, setOnline] = useState<number[]>([])
  const [truncated, setTruncated] = useState(false)

  const lastSeqRef = useRef(0)
  const socketRef = useRef<RoomSocket | null>(null)
  // 回调放进 ref：否则父组件每次渲染产生的新函数都会重建 WebSocket，
  // 表现为聊天室每隔几百毫秒断线重连一次。
  const cbRef = useRef({ onSlot, onStatus, onError })
  cbRef.current = { onSlot, onStatus, onError }

  /** 合并消息：按 seq 去重并保持升序。同时用 client_msg_id 替换乐观气泡。 */
  const merge = useCallback((incoming: Message[]) => {
    if (incoming.length === 0) return
    setItems((prev) => {
      // 键空间按前缀显式分区：已确认消息用 s<seq>，乐观气泡用 c<client_msg_id>，
      // 与 ChatPanel 里 React key 的取法保持一致。
      // 不要退回「用正负号区分两类键」的写法：乐观气泡的 id 本身是负数
      // （-Date.now()），取负后变正，导致清理分支永不成立，服务端回执到达后
      // 旧气泡不被移除——同一条消息会出现两次，其中一条永远停在「发送中」。
      const byKey = new Map<string, ChatItem>()
      for (const m of prev) {
        byKey.set(m.seq > 0 ? `s${m.seq}` : `c${m.client_msg_id}`, m)
      }

      for (const m of incoming) {
        byKey.set(`s${m.seq}`, m)
        // 服务端确认了这条消息 → 移除对应的本地乐观气泡。
        if (m.client_msg_id) byKey.delete(`c${m.client_msg_id}`)
        if (m.seq > lastSeqRef.current) lastSeqRef.current = m.seq
      }

      return [...byKey.values()].sort((a, b) => {
        // 乐观气泡（seq=0）永远排在最后，因为它们是刚发出去的。
        if (a.seq === 0 && b.seq === 0) return a.created_at.localeCompare(b.created_at)
        if (a.seq === 0) return 1
        if (b.seq === 0) return -1
        return a.seq - b.seq
      })
    })
  }, [])

  const handleFrame = useCallback(
    (env: Envelope) => {
      switch (env.type) {
        case 'hello': {
          const d = env.data as HelloData
          // 服务端告知当前最新序号。若本地游标还落后，补齐请求会把差值取回。
          if (d.cursor_seq > lastSeqRef.current) lastSeqRef.current = d.cursor_seq
          break
        }
        case 'chat.message':
          merge([env.data as Message])
          break
        case 'chat.backfill': {
          const bf = env.data as Backfill
          setTruncated(bf.truncated)
          merge(bf.messages)
          if (bf.latest_seq > lastSeqRef.current) lastSeqRef.current = bf.latest_seq
          break
        }
        case 'room.slot':
          cbRef.current.onSlot?.(env.data as SlotSnapshot)
          break
        case 'room.status':
          cbRef.current.onStatus?.(env.data as RoomStatusData)
          break
        case 'presence':
          setOnline((env.data as PresenceData).users ?? [])
          break
        case 'error':
          cbRef.current.onError?.(env.data as WSErrorData)
          break
        default:
          // pong 等无需处理的帧直接忽略。
          break
      }
    },
    [merge],
  )

  // 首屏历史走 HTTP：比等 WS 握手完成再拉一轮更快出内容。
  useEffect(() => {
    let alive = true
    setItems([])
    setTruncated(false)
    lastSeqRef.current = 0

    api
      .messages(roomID, 0)
      .then((bf) => {
        if (!alive) return
        setTruncated(bf.truncated)
        merge(bf.messages)
        if (bf.latest_seq > lastSeqRef.current) lastSeqRef.current = bf.latest_seq
      })
      .catch(() => {
        // 首屏拉取失败不致命：WS 连上后的补齐会把消息带回来。
      })

    return () => {
      alive = false
    }
  }, [roomID, merge])

  useEffect(() => {
    const sock = new RoomSocket({
      path: `/ws/rooms/${roomID}`,
      onFrame: handleFrame,
      onState: setState,
      onOpen: (send) => {
        // 关键：每次连接建立都补齐，断线期间的消息才不会丢。
        send('chat.pull', { last_seen_seq: lastSeqRef.current })
        send('presence.query', {})
      },
    })
    socketRef.current = sock
    return () => {
      sock.close()
      socketRef.current = null
    }
  }, [roomID, handleFrame])

  /** 上报已读水位。防抖交给调用方，这里只管发。 */
  useEffect(() => {
    if (items.length === 0) return
    const maxSeq = lastSeqRef.current
    if (maxSeq <= 0) return
    const id = setTimeout(() => {
      socketRef.current?.send('chat.ack', { seq: maxSeq })
    }, 800)
    return () => clearTimeout(id)
  }, [items])

  const doSend = useCallback(
    (content: string, msgType: 'TEXT' | 'TAG', tagCode: string, clientMsgID: string) => {
      const sock = socketRef.current
      const ok = sock?.send('chat.send', {
        content,
        msg_type: msgType,
        tag_code: tagCode,
        client_msg_id: clientMsgID,
      })

      // WS 不可用时回落到 HTTP。同一个 client_msg_id 让服务端幂等去重，
      // 因此「WS 其实发出去了但前端以为没发」也不会产生重复消息。
      if (!ok) {
        api
          .sendMessage(roomID, {
            content,
            msg_type: msgType,
            tag_code: tagCode,
            client_msg_id: clientMsgID,
          })
          .then((m) => merge([m]))
          .catch(() => {
            setItems((prev) =>
              prev.map((i) =>
                i.client_msg_id === clientMsgID ? { ...i, pending: false, failed: true } : i,
              ),
            )
          })
      }
    },
    [roomID, merge],
  )

  const send = useCallback(
    (content: string, msgType: 'TEXT' | 'TAG' = 'TEXT', tagCode = '') => {
      const trimmed = content.trim()
      if (!trimmed) return
      const clientMsgID = newClientMsgID()

      // 乐观插入：抢位与聊天都是「点了必须立刻有反馈」的交互。
      const optimistic: ChatItem = {
        id: -Date.now(),
        room_id: roomID,
        seq: 0,
        sender_id: null,
        msg_type: msgType,
        content: trimmed,
        tag_code: tagCode,
        sender_name: '我',
        sender_avatar: '',
        client_msg_id: clientMsgID,
        created_at: new Date().toISOString(),
        pending: true,
      }
      setItems((prev) => [...prev, optimistic])
      doSend(trimmed, msgType, tagCode, clientMsgID)
    },
    [roomID, doSend],
  )

  const retry = useCallback(
    (clientMsgID: string) => {
      const target = items.find((i) => i.client_msg_id === clientMsgID)
      if (!target) return
      setItems((prev) =>
        prev.map((i) =>
          i.client_msg_id === clientMsgID ? { ...i, failed: false, pending: true } : i,
        ),
      )
      doSend(
        target.content,
        target.msg_type === 'TAG' ? 'TAG' : 'TEXT',
        target.tag_code,
        clientMsgID,
      )
    },
    [items, doSend],
  )

  return { items, state, online, truncated, send, retry }
}
