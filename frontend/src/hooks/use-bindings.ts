import { keepPreviousData, useInfiniteQuery, useQuery } from '@tanstack/react-query'

import { listBindingDanmaku, listBindingFiles } from '@/api/bindings'

/**
 * 查询键：绑定里的弹幕文件 ['binding-files', id]，绑定保存的弹幕 ['binding-danmaku', id, 内容版本, 跳到的时间]。
 * 绑定本身在剧详情里，没有单独的查询。
 * 变更键：一个绑定上会改动弹幕的操作（重新拉取、追加文件、重新解析、删除）都带 ['bindings', id] 前缀，
 * 卡片据此在任何一个进行中时禁用其他操作；重新拉取另带 'refetch'，卡片据此显示拉取的提示。
 */
export const bindingKeys = {
  files: (id: number) => ['binding-files', id] as const,
  danmaku: (id: number, contentVersion: number, fromMs: number | null) =>
    ['binding-danmaku', id, contentVersion, fromMs] as const,
  write: (id: number) => ['bindings', id] as const,
  refetch: (id: number) => ['bindings', id, 'refetch'] as const,
}

/** 用弹幕文件建的绑定里的文件；enabled 为 false 时不取（弹出层打开时才取） */
export function useBindingFiles(id: number, enabled: boolean) {
  return useQuery({ queryKey: bindingKeys.files(id), queryFn: () => listBindingFiles(id), enabled })
}

/**
 * 绑定保存的弹幕，按弹幕源时间升序分页加载，fromMs 不为 null 时从这个时间开始；enabled 为 false 时不取（对话框打开时才取）。
 * 键里带着内容版本，版本一变就是另一份数据，同一个键的数据不会过时，因此不重新请求（staleTime 为无穷）。
 * 换键（跳转、版本变了）时，新的一页到达之前保留上一份数据（isPlaceholderData 为 true）。
 */
export function useBindingDanmaku(
  id: number,
  contentVersion: number,
  fromMs: number | null,
  enabled: boolean,
) {
  return useInfiniteQuery({
    queryKey: bindingKeys.danmaku(id, contentVersion, fromMs),
    queryFn: ({ pageParam }) => listBindingDanmaku(id, fromMs, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next ?? undefined,
    staleTime: Infinity,
    placeholderData: keepPreviousData,
    enabled,
  })
}
