import { useSyncExternalStore } from 'react'

/**
 * 订阅浏览器媒体查询。jsdom 或无 matchMedia 环境返回 defaultValue（默认 false），避免影响现有桌面端测试。
 */
export function useMediaQuery(query: string, defaultValue = false) {
  return useSyncExternalStore(
    (onStoreChange) => {
      if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
        return () => {}
      }
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onStoreChange)
      return () => mql.removeEventListener('change', onStoreChange)
    },
    () => {
      if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
        return defaultValue
      }
      return window.matchMedia(query).matches
    },
    () => defaultValue,
  )
}
