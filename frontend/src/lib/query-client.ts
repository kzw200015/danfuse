import { QueryClient } from '@tanstack/react-query'

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // 失败时直接展示统一响应的 message，不自动重试
      retry: false,
    },
  },
})
