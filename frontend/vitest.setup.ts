import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

// 未开启 vitest globals 时 Testing Library 不会自动卸载组件，需手动清理
afterEach(cleanup)
