import { createBrowserRouter } from 'react-router'

import App from '@/App'

const router = createBrowserRouter(
  [
    {
      path: '/',
      Component: App,
      // 首次进入时需等待懒加载页面就绪，期间渲染此组件；未声明时开发环境会告警
      HydrateFallback: () => null,
      children: [
        {
          index: true,
          lazy: async () => ({ Component: (await import('@/views/HomeView')).default }),
        },
        {
          path: 'users',
          lazy: async () => ({ Component: (await import('@/views/UsersView')).default }),
        },
      ],
    },
  ],
  { basename: import.meta.env.BASE_URL },
)

export default router
