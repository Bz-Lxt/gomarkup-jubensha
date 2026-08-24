import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import App from '@/App'
import '@/index.css'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 席位变动靠 WebSocket 推送，不需要窗口聚焦就重取——那会在用户
      // 来回切标签页时打出一串无意义的请求。
      refetchOnWindowFocus: false,
      staleTime: 5_000,
      // 业务拒绝（4xx）重试毫无意义：车满了再请求十次还是满的。
      // 只对网络错误与 5xx 重试一次。
      retry: (count, error) => {
        const status = (error as { status?: number }).status ?? 0
        if (status >= 400 && status < 500) return false
        return count < 1
      },
    },
  },
})

const root = document.getElementById('root')
if (!root) throw new Error('找不到 #root 挂载点')

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
