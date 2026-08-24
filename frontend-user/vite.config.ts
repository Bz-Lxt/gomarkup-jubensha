import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: { '@': path.resolve(__dirname, './src') },
  },
  server: {
    host: true,
    port: 5173,
    // 本地 npm run dev 时把 /api 与 /ws 代理到后端容器暴露的端口。
    // 生产构建走 nginx 反代，不用这里（见 nginx.conf）。
    proxy: {
      '/api': { target: 'http://127.0.0.1:31810', changeOrigin: true },
      '/ws': { target: 'ws://127.0.0.1:31810', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
})
