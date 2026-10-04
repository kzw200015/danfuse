import { useQuery } from '@tanstack/react-query'

import { getSettings } from '@/api/settings'

/** 只读的配置。改配置要重启服务，页面打开期间不会变，取一次就够了 */
export function useSettings() {
  return useQuery({ queryKey: ['settings'], queryFn: getSettings, staleTime: Infinity })
}
