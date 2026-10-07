import '@testing-library/jest-dom/vitest'

import { cleanup, configure } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// 每个文件的第一个用例要现加载懒加载的页面，并行跑满时 findBy 默认的 1 秒偶尔不够
configure({ asyncUtilTimeout: 3000 })

// 未开启 vitest globals 时 Testing Library 不会自动卸载组件，需手动清理；
// 每个用例结束后换回真实时间（轮询的用例用了假时间），清掉 mock 的实现与调用记录
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.resetAllMocks()
})

// jsdom 没有实现 matchMedia，Toaster（sonner）跟随系统主题时会用到；一律按不匹配处理
window.matchMedia = (query: string) => ({
  matches: false,
  media: query,
  onchange: null,
  addEventListener: () => {},
  removeEventListener: () => {},
  addListener: () => {},
  removeListener: () => {},
  dispatchEvent: () => false,
})

// jsdom 没有实现 scrollIntoView，目录页的列表会把选中行滚进可见区域；测试里不需要滚动
Element.prototype.scrollIntoView = () => {}
