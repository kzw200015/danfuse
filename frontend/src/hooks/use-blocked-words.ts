import { useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { listBlockedWords } from '@/api/blocked-words'

export const blockedWordKeys = {
  list: ['blocked-words'] as const,
}

/** 全部屏蔽词，新加的在前 */
export function useBlockedWords() {
  return useQuery({ queryKey: blockedWordKeys.list, queryFn: listBlockedWords })
}

/** 返回一个函数，让屏蔽词列表失效、重新加载。新增、删除成功后调用 */
export function useReloadBlockedWords() {
  const queryClient = useQueryClient()
  return useCallback(
    () => queryClient.invalidateQueries({ queryKey: blockedWordKeys.list }),
    [queryClient],
  )
}
