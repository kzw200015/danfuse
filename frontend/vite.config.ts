import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import vueDevTools from 'vite-plugin-vue-devtools'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // 后端地址可在 .env.local 中通过 API_PROXY_TARGET 覆盖
  const env = loadEnv(mode, process.cwd(), '')

  return {
    plugins: [vue(), tailwindcss(), vueDevTools()],
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
  }
})
