import { redirect, type RouteObject } from 'react-router'

import App from '@/App'

export const routes: RouteObject[] = [
  {
    path: '/',
    Component: App,
    // 首次进入时需等待懒加载页面就绪，期间渲染此组件；未声明时开发环境会告警
    HydrateFallback: () => null,
    children: [
      // 只做重定向；显式声明不渲染任何东西，否则 React Router 会警告叶子路由没有 element
      { index: true, loader: () => redirect('/catalog'), element: null },
      {
        // 选中的剧、季（季面板）、集（集面板）
        path: 'catalog/:seriesId?/:seasonId?/:episodeId?',
        lazy: async () => ({ Component: (await import('@/views/CatalogView')).default }),
      },
      {
        path: 'sync',
        lazy: async () => ({ Component: (await import('@/views/SyncView')).default }),
      },
    ],
  },
]
