import { useState } from 'react'
import { useIsMutating, useMutation } from '@tanstack/react-query'
import { Loader2Icon } from 'lucide-react'
import { toast } from 'sonner'

import { deleteBinding, updateBindingOffset, type Binding } from '@/api/bindings'
import type { SeasonBinding } from '@/api/season-bindings'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { bindingKeys } from '@/hooks/use-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { formatAgo, formatDateTime, formatDuration } from '@/lib/time'
import { cn } from '@/lib/utils'

import BindingDanmakuDialog from './BindingDanmakuDialog'
import BindingFilesPopover from './BindingFilesPopover'
import { FileBindingActions, LinkBindingActions } from './BindingSourceActions'
import { durationMismatch, MAX_OFFSET, parseOffset } from './catalog'
import { seasonBindingTag } from './season-binding'
import { SourceLink, StatusBadge } from './shared'

const invalidOffset = `偏移必须是 -${MAX_OFFSET} 到 ${MAX_OFFSET} 之间的秒数，小数最多三位`

/**
 * 一个绑定的卡片：状态、弹幕源标题、来源标签、弹幕条数（点开是保存的弹幕）、上次拉取时间、与本集时长的对比；偏移输入框、删除。
 * 按弹幕源的形态：贴链接建的，标题链接到原页面，可以重新拉取、清空后重新拉取；
 * 用弹幕文件建的，标签点开是文件列表，可以追加文件、重新解析，没有时长与拉取时间。
 * 季绑定建出的绑定带一个写着季绑定名称的标签（只用来显示，不是链接）。
 * 操作成功用 toast；失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
export default function BindingCard({
  binding,
  episodeDuration,
  seasonBindings,
}: {
  binding: Binding
  /** 本集的时长，秒 */
  episodeDuration: number | null
  /** 这一季的季绑定，按 binding.seasonBindingId 查出季绑定的名称 */
  seasonBindings: SeasonBinding[]
}) {
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const showError = (e: Error) => setError(e.message)
  // 卡片显示最新的绑定
  const reload = useReloadSeries()

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
    mutationKey: bindingKeys.write(binding.id),
    mutationFn: () => deleteBinding(binding.id),
    onMutate: clearError,
    onSuccess: () => {
      toast.success('已删除绑定')
      return reload()
    },
    onError: showError,
  })
  const busy = useIsMutating({ mutationKey: bindingKeys.write(binding.id) }) > 0
  const refetching = useIsMutating({ mutationKey: bindingKeys.refetch(binding.id) }) > 0

  function commitOffset(text: string) {
    const offset = parseOffset(text)
    if (offset === null) {
      setError(invalidOffset)
    } else if (offset !== binding.offset) {
      saveOffset.mutate(offset)
    }
  }

  const actions = { binding, busy, onStart: clearError, onError: showError }
  const dead = binding.status === 'dead'
  const tag = seasonBindingTag(binding.seasonBindingId, seasonBindings)
  return (
    <article
      aria-label={binding.title}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        <StatusBadge dead={dead} deadTitle="上次拉取时弹幕源已不存在；已保存的弹幕仍照常输出" />
        <div className="min-w-0 flex-1">
          {binding.kind === 'file' ? (
            <>
              <span className="font-medium break-all">{binding.title}</span>
              <div className="text-xs text-muted-foreground">
                <BindingFilesPopover binding={binding} />
              </div>
            </>
          ) : (
            <>
              <SourceLink href={binding.sourceUrl}>{binding.title}</SourceLink>
              <div className="text-xs text-muted-foreground">{binding.sourceLabel}</div>
            </>
          )}
        </div>
        {tag && (
          <Badge variant="secondary" className="max-w-48" title={tag.title}>
            <span className="truncate">{tag.text}</span>
          </Badge>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <BindingDanmakuDialog binding={binding} />
        {binding.lastFetchedAt && (
          <span title={formatDateTime(binding.lastFetchedAt)}>
            上次拉取 {formatAgo(binding.lastFetchedAt)}
          </span>
        )}
        {binding.kind === 'link' && (
          <DurationCompare source={binding.duration} episode={episodeDuration} />
        )}
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
          {binding.kind === 'file' ? (
            <FileBindingActions {...actions} />
          ) : (
            <LinkBindingActions {...actions} />
          )}
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
              「{binding.title}」的 {binding.danmakuCount.toLocaleString()} 条弹幕
              {binding.kind === 'file' && '和上传的弹幕文件'}会一起删除，无法恢复。
            </p>
          </ConfirmButton>
        </div>
      </div>
      {refetching && (
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
