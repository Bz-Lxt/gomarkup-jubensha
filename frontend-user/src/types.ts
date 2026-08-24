// 与后端 internal/model 一一对应的类型定义。
//
// 手写而非代码生成：这个项目的 DTO 数量有限，引入 openapi-generator 一类的
// 工具链带来的构建复杂度高于收益。代价是改后端 struct 时必须同步改这里，
// 因此每个类型都注明了来源文件。

/** 来源：model.RoomStatus */
export type RoomStatus =
  | 'DRAFT'
  | 'RECRUITING'
  | 'LOCKED'
  | 'CONFIRMED'
  | 'IN_PROGRESS'
  | 'COMPLETED'
  | 'EXPIRED'
  | 'CANCELLED'

/** 来源：model.MemberStatus。空串表示「我不在这辆车上」 */
export type MemberStatus = '' | 'PENDING' | 'JOINED' | 'CHECKED_IN' | 'RELEASED' | 'LEFT' | 'KICKED'

/** 来源：model.SeatGender */
export type SeatGender = '' | 'MALE' | 'FEMALE' | 'ANY'

/** 来源：model.RoomType */
export type RoomType = 'SCRIPT' | 'ESCAPE'

/** 来源：model.MsgType */
export type MsgType = 'TEXT' | 'TAG' | 'SYSTEM'

/** 来源：model.PublicProfile */
export interface PublicProfile {
  id: number
  username: string
  nickname: string
  avatar: string
  city: string
  reputation: number
  tags: string[]
}

/** 来源：model.User */
export interface User extends PublicProfile {
  phone: string
  bio: string
  created_at: string
  updated_at: string
}

/** 来源：jwtutil.TokenPair */
export interface TokenPair {
  access_token: string
  refresh_token: string
  expires_in: number
}

/** 来源：service.AuthResult */
export interface AuthResult {
  user: User
  tokens: TokenPair
}

/** 来源：model.SeatBucket */
export interface SeatBucket {
  gender: SeatGender
  label: string
  quota: number
  taken: number
  remaining: number
}

/** 来源：model.SlotSnapshot —— 席位真相的唯一来源，前端不自行推导 */
export interface SlotSnapshot {
  room_id: number
  status: RoomStatus
  status_label: string
  capacity: number
  min_viable: number
  joined_count: number
  pending_count: number
  occupied: number
  remaining: number
  seats: SeatBucket[]
  headline: string
  seat_detail: string
  risk_hint: string
  at_risk: boolean
  viable: boolean
  accepts_join: boolean
  start_at: string
  seconds_left: number
}

/** 来源：model.Room */
export interface Room {
  id: number
  owner_id: number
  title: string
  script_name: string
  venue_name: string
  city: string
  address: string
  room_type: RoomType
  difficulty: number
  theme: string
  notes: string
  start_at: string
  capacity: number
  min_viable: number
  joined_count: number
  pending_count: number
  male_seats: number
  female_seats: number
  any_seats: number
  male_taken: number
  female_taken: number
  any_taken: number
  status: RoomStatus
  msg_seq: number
  created_at: string
  updated_at: string
}

/** 来源：model.MemberView */
export interface MemberView {
  member_id: number
  status: MemberStatus
  status_label: string
  seat_gender: SeatGender
  seat_label: string
  is_owner: boolean
  hold_seconds_left: number
  joined_at: string | null
  user: PublicProfile
  online: boolean
}

/** 来源：service.RoomCard */
export interface RoomCard {
  room: Room
  snapshot: SlotSnapshot
  owner: PublicProfile
  members: MemberView[]
  my_status: MemberStatus
  am_owner: boolean
  am_on_car: boolean
  my_seat: SeatGender
  tags: string[]
  type_name: string
  unread: number
}

/** 来源：service.JoinResult。idempotent 为真表示「你本来就在车上」，不是新占位 */
export interface JoinResult {
  room: Room
  snapshot: SlotSnapshot
  member: RoomMember
  idempotent: boolean
}

/** 来源：model.RoomMember */
export interface RoomMember {
  id: number
  room_id: number
  user_id: number
  seat_gender: SeatGender
  status: MemberStatus
  is_owner: boolean
  hold_expires_at: string | null
  joined_at: string | null
  left_at: string | null
  created_at: string
  updated_at: string
}

/** 来源：handler.SlotHandler.Audit。drift 必须恒为 0 */
export interface SlotAudit {
  room_id: number
  aggregate_counts: number
  actual_members: number
  drift: number
  consistent: boolean
}

/** 来源：service.WallResult */
export interface WallResult {
  items: RoomCard[]
  total: number
  limit: number
  offset: number
}

/** 来源：model.Message */
export interface Message {
  id: number
  room_id: number
  seq: number
  sender_id: number | null
  msg_type: MsgType
  content: string
  tag_code: string
  sender_name: string
  sender_avatar: string
  client_msg_id: string
  created_at: string
}

/** 来源：model.Backfill */
export interface Backfill {
  messages: Message[]
  from_seq: number
  to_seq: number
  latest_seq: number
  truncated: boolean
  total_gap: number
  unread_hint: number
}

/** 来源：model.StateLog */
export interface StateLog {
  id: number
  room_id: number
  member_id: number | null
  scope: string
  from_status: string
  to_status: string
  event: string
  actor_id: number | null
  reason: string
  created_at: string
}

/** 来源：service.TagOption */
export interface TagOption {
  code: string
  label: string
  phrase: string
}

export interface EnumOption {
  code: string
  label: string
}

/** 来源：handler.RoomHandler.Meta */
export interface MetaEnums {
  seat_genders: EnumOption[]
  room_types: EnumOption[]
  statuses: EnumOption[]
  themes: EnumOption[]
  max_tags: number
}

/** 来源：service.UnreadCount */
export interface UnreadCount {
  room_id: number
  unread: number
}

/** 来源：service.CreateRoomInput */
export interface CreateRoomInput {
  title: string
  script_name: string
  venue_name: string
  city: string
  address: string
  room_type: RoomType
  difficulty: number
  theme: string
  notes: string
  start_at: string
  male_seats: number
  female_seats: number
  any_seats: number
  min_viable: number
  owner_seat: SeatGender
}

// ------------------------------------------------------------------ WS 帧

/** 来源：model.Envelope */
export interface Envelope<T = unknown> {
  type: string
  data: T
}

/** 来源：model.HelloData */
export interface HelloData {
  room_id: number
  user_id: number
  latest_seq: number
  cursor_seq: number
  server_time: string
}

/** 来源：model.PresenceData */
export interface PresenceData {
  room_id: number
  count: number
  users: number[]
}

/** 来源：model.RoomStatusData */
export interface RoomStatusData {
  room_id: number
  status: RoomStatus
  status_label: string
  event: string
  reason: string
}

/** 来源：model.WSErrorData */
export interface WSErrorData {
  code: string
  message: string
}
