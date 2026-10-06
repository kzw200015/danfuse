import { useRef } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { FilePlusIcon, Loader2Icon, RefreshCwIcon } from 'lucide-react'
import { toast } from 'sonner'

import { appendBindingFiles, refetchBinding, reparseBinding, type Binding } from '@/api/bindings'
import { isApiStatus } from '@/api/request'
import { ConfirmButton } from '@/components/ConfirmButton'
import { Button } from '@/components/ui/button'
import { bindingKeys } from '@/hooks/use-bindings'
import { useElapsed } from '@/hooks/use-elapsed'
import { useReloadSeries } from '@/hooks/use-series'

import { appendFilesMessage, DANMAKU_FILE_ACCEPT } from './catalog'
import { takeFiles } from './shared'

/**
 * 绑定卡片上按弹幕源的形态不同的那组操作。变更都带 bindingKeys.write 前缀，卡片据此在进行中时禁用其他操作；
 * 失败交给卡片显示（onError），开始时清掉卡片上的上一条提示（onStart）。
 */
interface ActionsProps {
  binding: Binding
  /** 这个绑定上有改动弹幕的操作在进行 */
  busy: boolean
  onStart: () => void
  onError: (e: Error) => void
}

/** 贴链接建的绑定：重新拉取、清空后重新拉取。进行中按钮上显示已用秒数，拉取的提示由卡片显示 */
export function LinkBindingActions({ binding, busy, onStart, onError }: ActionsProps) {
  const reload = useReloadSeries()
  const refetch = useMutation({
    mutationKey: bindingKeys.refetch(binding.id),
    mutationFn: (clear: boolean) => refetchBinding(binding.id, clear),
    onMutate: onStart,
    onSuccess: ({ added }, clear) => {
      const n = added.toLocaleString()
      toast.success(
        clear ? `已清空并重新拉取，共 ${n} 条弹幕` : added > 0 ? `新增 ${n} 条弹幕` : '没有新弹幕',
      )
      return reload()
    },
    onError: (e) => {
      onError(e)
      // 弹幕源不存在时后端已把绑定标为失效，重新加载这部剧，让失效状态显示出来
      if (isApiStatus(e, 422)) return reload()
    },
  })
  const elapsed = useElapsed(refetch.isPending)
  const refetching = refetch.isPending && !refetch.variables
  const clearing = refetch.isPending && refetch.variables

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        disabled={busy}
        title="只插入新弹幕，不删除已有的弹幕"
        onClick={() => refetch.mutate(false)}
      >
        {refetching ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
        {refetching ? `拉取中 ${elapsed}s` : '重新拉取'}
      </Button>
      <ConfirmButton
        trigger={
          <Button variant="ghost" size="sm" disabled={busy}>
            {clearing && <Loader2Icon className="animate-spin" />}
            {clearing ? `拉取中 ${elapsed}s` : '清空后重新拉取'}
          </Button>
        }
        title="清空后重新拉取？"
        confirmLabel="清空并重新拉取"
        onConfirm={() => refetch.mutate(true)}
      >
        <p>先完整拉取一遍，成功后替换现有弹幕。</p>
        <p className="font-medium text-destructive">
          B 站上已经删除、或已经滑出滚动窗口的弹幕会永久丢失。
        </p>
        <p>拉取失败时不做任何改动。</p>
      </ConfirmButton>
    </>
  )
}

/**
 * 用弹幕文件建的绑定：追加文件（选好文件就上传）、重新解析。都在本地解析，很快，不显示已用秒数。
 * 成功后连同弹出层里的文件列表一起刷新。
 */
export function FileBindingActions({ binding, busy, onStart, onError }: ActionsProps) {
  const reload = useReloadSeries()
  const queryClient = useQueryClient()
  const input = useRef<HTMLInputElement>(null)
  const refresh = () =>
    Promise.all([
      reload(),
      queryClient.invalidateQueries({ queryKey: bindingKeys.files(binding.id) }),
    ])

  const append = useMutation({
    mutationKey: bindingKeys.write(binding.id),
    mutationFn: (files: File[]) => appendBindingFiles(binding.id, files),
    onMutate: onStart,
    onSuccess: (result) => {
      toast.success(appendFilesMessage(result))
      return refresh()
    },
    onError,
  })
  // 变量是重新解析之前的条数，提示里对比
  const reparse = useMutation({
    mutationKey: bindingKeys.write(binding.id),
    mutationFn: (_before: number) => reparseBinding(binding.id),
    onMutate: onStart,
    onSuccess: ({ danmakuCount }, before) => {
      toast.success(
        `重新解析完成，共 ${danmakuCount.toLocaleString()} 条弹幕（之前 ${before.toLocaleString()} 条）`,
      )
      return refresh()
    },
    onError,
  })

  return (
    <>
      <input
        ref={input}
        type="file"
        multiple
        accept={DANMAKU_FILE_ACCEPT}
        className="hidden"
        aria-label="追加文件"
        onChange={(e) => {
          const files = takeFiles(e.target)
          if (files.length > 0) append.mutate(files)
        }}
      />
      <Button
        variant="outline"
        size="sm"
        disabled={busy}
        title="加入更多弹幕文件；绑定里已有的文件跳过"
        onClick={() => input.current?.click()}
      >
        {append.isPending ? <Loader2Icon className="animate-spin" /> : <FilePlusIcon />}
        追加文件
      </Button>
      <Button
        variant="ghost"
        size="sm"
        disabled={busy}
        title="按保存的弹幕文件重新解析，替换现有弹幕"
        onClick={() => reparse.mutate(binding.danmakuCount)}
      >
        {reparse.isPending && <Loader2Icon className="animate-spin" />}
        重新解析
      </Button>
    </>
  )
}
