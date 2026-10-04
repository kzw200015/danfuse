import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// 未开启 vitest globals 时 Testing Library 不会自动卸载组件，需手动清理
afterEach(cleanup)

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
