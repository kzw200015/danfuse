import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ExternalLinkIcon, Loader2Icon, RefreshCwIcon } from 'lucide-react'
import { toast } from 'sonner'

import { deleteBinding, refetchBinding, updateBindingOffset, type Binding } from '@/api/bindings'
import { ApiError } from '@/api/request'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useElapsed } from '@/hooks/use-elapsed'
import { seriesKeys } from '@/hooks/use-series'
import { formatAgo, formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'

import { durationMismatch, formatDuration, MAX_OFFSET, parseOffset } from './catalog'

const invalidOffset = `偏移必须是 -${MAX_OFFSET} 到 ${MAX_OFFSET} 之间的秒数，小数最多三位`

/**
 * 一个绑定的卡片：状态、弹幕源标题（链接到来源）、来源标签、弹幕条数、上次拉取时间、与本集时长的对比；
 * 偏移输入框，重新拉取、清空后重新拉取、删除。
 * 操作成功用 toast；失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
export default function BindingCard({
  binding,
  episodeDuration,
}: {
  binding: Binding
  /** 本集的时长，秒 */
  episodeDuration: number | null
}) {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const showError = (e: Error) => setError(e.message)
  // 剧详情的键以剧列表的键为前缀，一起刷新：卡片显示最新的绑定，各处的绑定统计随之更新
  const reload = () => queryClient.invalidateQueries({ queryKey: seriesKeys.list })

  const refetch = useMutation({
    mutationFn: (clear: boolean) => refetchBinding(binding.id, clear),
    onMutate: clearError,
    onSuccess: ({ added }, clear) => {
      const n = added.toLocaleString()
      toast.success(
        clear ? `已清空并重新拉取，共 ${n} 条弹幕` : added > 0 ? `新增 ${n} 条弹幕` : '没有新弹幕',
      )
      return reload()
    },
    onError: (e) => {
      showError(e)
      // 弹幕源不存在时后端已把绑定标为失效，重新加载这部剧，让失效状态显示出来
      if (e instanceof ApiError && e.status === 422) return reload()
    },
  })
  const saveOffset = useMutation({
    mutationFn: (offset: number) => updateBindingOffset(binding.id, offset),
    onMutate: clearError,
    onSuccess: ({ offset }) => {
      toast.success(`偏移已改为 ${offset} 秒`)
      return reload()
    },
    onError: showError,
  })
  const remove = useMutation({
    mutationFn: () => deleteBinding(binding.id),
    onMutate: clearError,
    onSuccess: () => {
      toast.success('已删除绑定')
      return reload()
    },
    onError: showError,
  })
  const elapsed = useElapsed(refetch.isPending)
  const busy = refetch.isPending || remove.isPending
  const refetching = refetch.isPending && !refetch.variables
  const clearing = refetch.isPending && refetch.variables

  function commitOffset(text: string) {
    const offset = parseOffset(text)
    if (offset === null) {
      setError(invalidOffset)
    } else if (offset !== binding.offset) {
      saveOffset.mutate(offset)
    }
  }

  const dead = binding.status === 'dead'
  return (
    <article
      aria-label={binding.title}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        {dead ? (
          <Badge variant="destructive" title="上次拉取时弹幕源已不存在；已保存的弹幕仍照常输出">
            失效
          </Badge>
        ) : (
          <Badge variant="outline" className="border-emerald-600/30 text-emerald-700">
            正常
          </Badge>
        )}
        <div className="min-w-0 flex-1">
          <a
            href={binding.sourceUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 font-medium break-all hover:underline"
          >
            {binding.title}
            <ExternalLinkIcon className="size-3 shrink-0 opacity-50" />
          </a>
          <div className="text-xs text-muted-foreground">{binding.sourceLabel}</div>
        </div>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>弹幕 {binding.danmakuCount.toLocaleString()} 条</span>
        {binding.lastFetchedAt && (
          <span title={formatDateTime(binding.lastFetchedAt)}>
            上次拉取 {formatAgo(binding.lastFetchedAt)}
          </span>
        )}
        <DurationCompare source={binding.duration} episode={episodeDuration} />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {/* 保存成功、重新加载后偏移变了，用新的值重新初始化输入框 */}
        <OffsetInput
          key={binding.offset}
          offset={binding.offset}
          disabled={busy || saveOffset.isPending}
          onCommit={commitOffset}
        />
        <div className="ml-auto flex gap-1">
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
          <ConfirmButton
            trigger={
              <Button variant="ghost" size="sm" className="text-destructive" disabled={busy}>
                {remove.isPending && <Loader2Icon className="animate-spin" />}
                删除
              </Button>
            }
            title="删除这个绑定？"
            confirmLabel="删除"
            onConfirm={() => remove.mutate()}
          >
            <p>
              「{binding.title}」的 {binding.danmakuCount.toLocaleString()}{' '}
              条弹幕会一起删除，无法恢复。
            </p>
          </ConfirmButton>
        </div>
      </div>
      {refetch.isPending && (
        <p className="text-xs text-muted-foreground">正在拉取全部弹幕，最长约 25 秒…</p>
      )}
      {error && <ErrorNote onClose={clearError}>{error}</ErrorNote>}
    </article>
  )
}

/** 偏移输入框：回车或失焦时交给 onCommit，由调用方校验后保存；输入不合法时标红 */
function OffsetInput({
  offset,
  disabled,
  onCommit,
}: {
  offset: number
  disabled: boolean
  onCommit: (text: string) => void
}) {
  const [text, setText] = useState(String(offset))
  return (
    <label
      className="flex items-center gap-1 text-xs text-muted-foreground"
      title="正数表示弹幕延后，负数表示提前"
    >
      偏移
      <Input
        aria-label="偏移（秒）"
        className="h-7 w-20 text-right tabular-nums"
        inputMode="decimal"
        aria-invalid={parseOffset(text) === null}
        value={text}
        disabled={disabled}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => onCommit(text)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') e.currentTarget.blur()
        }}
      />
      秒
    </label>
  )
}

/** 弹幕源时长与本集时长的对比，相差 3 秒以上时标出 */
function DurationCompare({ source, episode }: { source: number; episode: number | null }) {
  const diff = durationMismatch(source, episode)
  return (
    <span className="tabular-nums">
      弹幕源 {formatDuration(source)} / 本集 {formatDuration(episode)}
      {diff !== null && (
        <span className="text-amber-600">
          （弹幕源{diff > 0 ? '长' : '短'} {Math.abs(diff)} 秒）
        </span>
      )}
    </span>
  )
}
