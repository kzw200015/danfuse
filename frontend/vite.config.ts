import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // 后端地址可在 .env.local 中通过 API_PROXY_TARGET 覆盖
  const env = loadEnv(mode, process.cwd(), '')

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      // 直接使用 tsconfig 中的 paths 作为别名，无需在此重复配置
      tsconfigPaths: true,
    },
    server: {
      proxy: {
        // 开发环境把 /api 转发到后端，避免跨域
        '/api': env.API_PROXY_TARGET || 'http://localhost:8080',
      },
    },
    build: {
      // 产物直接输出到后端，由 backend/web/embed.go 内嵌进二进制
      outDir: '../backend/web/static/dist',
      // outDir 在项目目录之外，Vite 默认不清空，需要显式开启，免得旧的带哈希文件越积越多
      emptyOutDir: true,
      // 页面已按路由懒加载；主 chunk 里几乎都是首屏必需的依赖（React、React Router、TanStack Query、
      // 顶栏弹出层用的 Base UI），拆成几个 chunk 首屏要下载的总量不变，所以放宽警告线而不拆分
      chunkSizeWarningLimit: 600,
    },
  }
})
