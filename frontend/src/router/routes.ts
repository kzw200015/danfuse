import { redirect, type RouteObject } from 'react-router'

import App from '@/App'

export const routes: RouteObject[] = [
  {
    path: '/',
    Component: App,
    // 首次进入时需等待懒加载页面就绪，期间渲染此组件；未声明时开发环境会告警
    HydrateFallback: () => null,
    children: [
      { index: true, loader: () => redirect('/catalog') },
      {
        path: 'catalog',
        lazy: async () => ({ Component: (await import('@/views/CatalogView')).default }),
      },
      {
        path: 'sync',
        lazy: async () => ({ Component: (await import('@/views/SyncView')).default }),
      },
    ],
  },
]
