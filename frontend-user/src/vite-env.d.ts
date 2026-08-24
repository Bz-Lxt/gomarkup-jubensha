/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 后端 HTTP 基址。留空表示同源，由 nginx 反代（生产默认）。 */
  readonly VITE_API_BASE?: string
  /** 后端 WS 基址。留空表示同源，协议随页面自动升级为 wss。 */
  readonly VITE_WS_BASE?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
