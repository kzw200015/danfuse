import { useQuery } from '@tanstack/react-query'

import { listBindingFiles } from '@/api/bindings'

/**
 * 查询键：绑定里的弹幕文件 ['binding-files', id]。绑定本身在剧详情里，没有单独的查询。
 * 变更键：一个绑定上会改动弹幕的操作（重新拉取、追加文件、重新解析、删除）都带 ['bindings', id] 前缀，
 * 卡片据此在任何一个进行中时禁用其他操作；重新拉取另带 'refetch'，卡片据此显示拉取的提示。
 */
export const bindingKeys = {
  files: (id: number) => ['binding-files', id] as const,
  write: (id: number) => ['bindings', id] as const,
  refetch: (id: number) => ['bindings', id, 'refetch'] as const,
}

/** 用弹幕文件建的绑定里的文件；enabled 为 false 时不取（弹出层打开时才取） */
export function useBindingFiles(id: number, enabled: boolean) {
  return useQuery({ queryKey: bindingKeys.files(id), queryFn: () => listBindingFiles(id), enabled })
}
