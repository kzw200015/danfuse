import { useId, useState } from 'react'
import { useIsMutating, useMutation } from '@tanstack/react-query'
import { Loader2Icon, LockIcon } from 'lucide-react'
import { Link } from 'react-router'
import { toast } from 'sonner'

import { deleteBinding, updateBindingOffset, type Binding } from '@/api/bindings'
import { bindingExportUrl } from '@/api/export'
import type { Season } from '@/api/series'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { InfoTip } from '@/components/InfoTip'
import { Badge } from '@/components/ui/badge'
import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { bindingKeys } from '@/hooks/use-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { formatAgo, formatDateTime, formatDuration } from '@/lib/time'
import { cn } from '@/lib/utils'

import BindingDanmakuDialog from './BindingDanmakuDialog'
import BindingFilesPopover from './BindingFilesPopover'
import { FileBindingActions, LinkBindingActions } from './BindingSourceActions'
import { durationMismatch, MAX_OFFSET, parseOffset } from './catalog'
import ExportButton from './ExportButton'
import { seasonBindingTag } from './season-binding'
import { SourceLink, StatusBadge, useCatalogPath } from './shared'

const derivedHint =
  '季绑定建出的绑定不能单独删除或追加文件：要去掉或换掉这一集，编辑季绑定的集号对应、合集或文件夹'

const invalidOffset = `偏移必须是 -${MAX_OFFSET} 到 ${MAX_OFFSET} 之间的秒数，小数最多三位`

/**
 * 一个绑定的卡片：状态、弹幕源标题、来源标签、弹幕条数（点开是保存的弹幕）、上次拉取时间、与本集时长的对比；偏移输入框、
 * 导出 XML（浏览器直接下载，没有弹幕时是一份空的 XML，所以不禁用）、删除。
 * 按弹幕源的形态：贴链接建的，标题链接到原页面，可以重新拉取、清空后重新拉取；
 * 用弹幕文件建的，标签点开是文件列表，可以追加文件、重新解析，没有时长与拉取时间。
 * 季绑定建出的绑定和手动的排在一起，带一个锁形的季绑定名称标签；不能单独删除，删除的位置换成"在季绑定里改 →"，
 * 两处都跳到季面板。
 * 操作成功用 toast；失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
export default function BindingCard({
  binding,
  episodeDuration,
  season,
  seriesId,
}: {
  binding: Binding
  /** 本集的时长，秒 */
  episodeDuration: number | null
  /** 本集所在的季，按 binding.seasonBindingId 查出季绑定的名称 */
  season: Season
  seriesId: number
}) {
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const showError = (e: Error) => setError(e.message)
  // 卡片显示最新的绑定
  const reload = useReloadSeries()
  const path = useCatalogPath()

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
    if (saveOffset.isPending) return
    const offset = parseOffset(text)
    if (offset === null) {
      setError(invalidOffset)
    } else if (offset !== binding.offset) {
      saveOffset.mutate(offset)
    }
  }

  const actions = { binding, busy, onStart: clearError, onError: showError }
  const dead = binding.status === 'dead'
  const tag = seasonBindingTag(binding.seasonBindingId, season, seriesId, path)
  return (
    <article
      aria-label={binding.title}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex flex-wrap items-start gap-2">
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
          <Badge
            variant="secondary"
            className="max-w-full sm:max-w-48"
            title={tag.title}
            render={<Link to={tag.to} />}
          >
            <LockIcon className="shrink-0" />
            <span className="truncate">{tag.text}</span>
          </Badge>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <BindingDanmakuDialog binding={binding} />
        {binding.lastFetchedAt && <FetchedTime at={binding.lastFetchedAt} />}
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
        <div className="ml-auto flex flex-wrap justify-end gap-1.5">
          {binding.kind === 'file' ? (
            <FileBindingActions {...actions} />
          ) : (
            <LinkBindingActions {...actions} />
          )}
          <ExportButton
            href={bindingExportUrl(binding.id)}
            title="导出这个绑定保存的弹幕（按偏移校正）"
          />
          {tag ? (
            <Link
              to={tag.to}
              className={buttonVariants({
                variant: 'ghost',
                size: 'sm',
                className: 'text-muted-foreground',
              })}
              title={derivedHint}
            >
              在季绑定里改 →
            </Link>
          ) : (
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
          )}
        </div>
      </div>
      {refetching && (
        <p className="text-xs text-muted-foreground">正在拉取全部弹幕，最长约 25 秒…</p>
      )}
      {error && <ErrorNote onClose={clearError}>{error}</ErrorNote>}
    </article>
  )
}

/** 正在输入负号或小数点的中间态，不提前标红 */
function isIntermediateOffset(text: string) {
  const s = text.trim()
  return s === '-' || s === '+' || s === '.' || s === '-.' || s === '+.'
}

/** 偏移输入框：回车、失焦或点保存时交给 onCommit；支持 ± 切换正负号，适配无负号的移动端数字键盘 */
function OffsetInput({
  offset,
  disabled,
  onCommit,
}: {
  offset: number
  disabled: boolean
  onCommit: (text: string) => void
}) {
  const inputId = useId()
  const [text, setText] = useState(String(offset))
  const parsed = parseOffset(text)
  const dirty = parsed !== null && parsed !== offset
  const invalid = parsed === null && !isIntermediateOffset(text)

  const flipSign = () => {
    const trimmed = text.trim()
    if (!trimmed) {
      setText('-')
      return
    }
    if (trimmed.startsWith('-')) setText(trimmed.slice(1))
    else if (trimmed.startsWith('+')) setText(`-${trimmed.slice(1)}`)
    else setText(`-${trimmed}`)
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
      <label htmlFor={inputId}>偏移</label>
      <Button
        type="button"
        variant="outline"
        size="xs"
        aria-label="切换偏移正负号"
        title="切换正负号"
        disabled={disabled}
        onPointerDown={(e) => e.preventDefault()}
        onClick={flipSign}
      >
        ±
      </Button>
      <Input
        id={inputId}
        aria-label="偏移（秒）"
        className="h-9 w-20 text-right tabular-nums md:h-7"
        inputMode="text"
        aria-invalid={invalid}
        value={text}
        disabled={disabled}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => onCommit(text)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') e.currentTarget.blur()
        }}
      />
      <span>秒</span>
      <InfoTip label="偏移说明" content="正数表示弹幕延后，负数表示提前" />
      {dirty && (
        <Button
          type="button"
          size="xs"
          disabled={disabled}
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => onCommit(text)}
        >
          保存
        </Button>
      )}
    </div>
  )
}

/** 上次拉取时间：PC 悬停看精确时间，触屏点击可切换精确时间 */
function FetchedTime({ at }: { at: string }) {
  const [exact, setExact] = useState(false)
  const full = formatDateTime(at)
  return (
    <button
      type="button"
      title={full}
      className="cursor-pointer hover:text-foreground"
      onClick={() => setExact((v) => !v)}
    >
      上次拉取 {exact ? full : formatAgo(at)}
    </button>
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
