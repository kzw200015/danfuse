import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // 后端地址可在 .env.local 中通过 API_PROXY_TARGET 覆盖
  const env = loadEnv(mode, process.cwd(), '')
  const target = env.API_PROXY_TARGET || 'http://localhost:8080'

  return {
    plugins: [react(), tailwindcss()],
    resolve: {
      // 直接使用 tsconfig 中的 paths 作为别名，无需在此重复配置
      tsconfigPaths: true,
    },
    server: {
      proxy: {
        // 开发环境把 /api 转发到后端，避免跨域
        '/api': target,
        // 设置弹出层按当前页面的地址拼插件地址，开发时也要能用
        '/dandanplay': target,
      },
    },
    build: {
      // 产物直接输出到后端，由 backend/web/embed.go 内嵌进二进制
      outDir: '../backend/web/static/dist',
      // outDir 在项目目录之外，Vite 默认不清空，需要显式开启，免得旧的带哈希文件越积越多
      emptyOutDir: true,
      // 管理界面只在内网使用，页面已按路由懒加载，首屏依赖都在主 chunk；
      // 拆出 vendor chunk 不减少首屏下载量，所以放宽警告线而不拆分
      chunkSizeWarningLimit: 600,
    },
  }
})
