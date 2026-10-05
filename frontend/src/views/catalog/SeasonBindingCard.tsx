import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  ChevronDownIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  Loader2Icon,
  RefreshCwIcon,
} from 'lucide-react'
import { toast } from 'sonner'

import { ApiError } from '@/api/request'
import {
  backfillSeasonBinding,
  deleteSeasonBinding,
  updateSeasonBinding,
  type SeasonBinding,
  type SeasonBindingDetail,
  type SeasonBindingPatch,
  type SeasonBindingItem,
} from '@/api/season-bindings'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useElapsed } from '@/hooks/use-elapsed'
import { useSeasonBinding, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { formatAgo, formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'

import MappingInputs from './MappingInputs'
import { itemStateText, parseMappingNumber } from './season-binding'

/**
 * 一个季绑定的卡片：状态、合集标题（链接到原页面）与标签、建出的绑定数、上次检查的时间与错误、补建中的已用秒数；
 * 集号对应（保存后后台补建）、追更开关、立即补建、删除，以及可展开的条目表。
 * 操作成功用 toast（立即补建被拒绝的 409 也用 toast）；其余失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
export default function SeasonBindingCard({
  binding,
  summaryUpdatedAt,
}: {
  /** 剧详情里的这个季绑定 */
  binding: SeasonBinding
  /** 剧详情取到的时间，与轮询到的详情比较哪个新 */
  summaryUpdatedAt: number
}) {
  const [expanded, setExpanded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const showError = (e: Error) => setError(e.message)
  const reload = useReloadSeries()
  const watch = useWatchSeasonBinding()
  // 显示剧详情与轮询到的详情里较新的那一份：补建进行中建出的绑定数等逐条更新
  const { view, detail, error: detailError } = useSeasonBinding(binding, summaryUpdatedAt, expanded)
  const { running } = view
  const elapsed = useElapsed(running)

  const update = useMutation({
    mutationFn: (patch: SeasonBindingPatch) => updateSeasonBinding(binding.id, patch),
    onMutate: clearError,
    onSuccess: async (d, patch) => {
      toast.success(
        patch.follow === undefined
          ? '集号对应已保存，正在后台补建'
          : patch.follow
            ? '已打开追更，正在后台补建'
            : '已关闭追更',
      )
      await watch(d.id, d)
      return reload()
    },
    onError: showError,
  })
  const backfill = useMutation({
    mutationFn: () => backfillSeasonBinding(binding.id),
    onMutate: clearError,
    onSuccess: () => {
      toast.success('已开始补建')
      return watch(binding.id)
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        toast.error(e.message)
        return watch(binding.id) // 正在补建：显示它的进度
      }
      showError(e)
    },
  })
  const remove = useMutation({
    mutationFn: (withBindings: boolean) => deleteSeasonBinding(binding.id, withBindings),
    onMutate: clearError,
    onSuccess: () => {
      toast.success('已删除季绑定')
      return reload()
    },
    onError: (e) => {
      showError(e)
      // 已经不在了（例如在别处删掉了）：重新加载，卡片随之消失
      if (e instanceof ApiError && e.status === 404) return reload()
    },
  })
  // 确认框里"同时删除建出的绑定"的勾选，每次打开时恢复成默认的不勾选
  const [withBindings, setWithBindings] = useState(false)
  const busy = remove.isPending

  const dead = view.status === 'dead'
  return (
    <article
      aria-label={view.title}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        {dead ? (
          <Badge
            variant="destructive"
            title="上次检查时合集已不存在或已下架；已经建出的绑定不受影响，追更开着时照常检查，恢复后自动变回正常"
          >
            失效
          </Badge>
        ) : (
          <Badge variant="outline" className="border-emerald-600/30 text-emerald-700">
            正常
          </Badge>
        )}
        <div className="min-w-0 flex-1">
          <a
            href={view.sourceUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 font-medium break-all hover:underline"
          >
            {view.title || view.sourceLabel}
            <ExternalLinkIcon className="size-3 shrink-0 opacity-50" />
          </a>
          <div className="text-xs text-muted-foreground">
            {view.sourceLabel}
            {view.finished && ' · 已完结'}
          </div>
        </div>
        <label
          className="flex shrink-0 items-center gap-1.5 text-xs"
          title="开着时每天检查一次合集、目录同步进来新的集后一分钟内补建，并在绑定建出后 14 天内每天重新拉取一次"
        >
          <Switch
            size="sm"
            checked={view.follow}
            disabled={update.isPending || busy}
            onCheckedChange={(follow) => update.mutate({ follow })}
          />
          追更
        </label>
      </div>

      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>建出 {view.bindingCount} 个绑定</span>
        {view.lastCheckedAt ? (
          <span title={formatDateTime(view.lastCheckedAt)}>
            上次检查 {formatAgo(view.lastCheckedAt)}
          </span>
        ) : (
          <span>还没有检查</span>
        )}
        {view.lastError && <span className="text-destructive">上次检查：{view.lastError}</span>}
        {running && (
          <span className="inline-flex items-center gap-1 text-foreground">
            <Loader2Icon className="size-3 animate-spin" />
            补建中 {elapsed}s
          </span>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {/* 保存成功、重新加载后对应变了，用新的值重新初始化输入框 */}
        <MappingEditor
          key={`${view.mappingFrom}-${view.mappingTo}`}
          binding={view}
          disabled={update.isPending || busy}
          onSave={(mappingFrom, mappingTo) => update.mutate({ mappingFrom, mappingTo })}
        />
        <div className="ml-auto flex gap-1">
          <Button
            variant="outline"
            size="sm"
            disabled={backfill.isPending || busy}
            title="现在检查一次合集，为新出的集补建绑定；追更关着时也能用"
            onClick={() => backfill.mutate()}
          >
            <RefreshCwIcon />
            立即补建
          </Button>
          <ConfirmButton
            trigger={
              <Button variant="ghost" size="sm" className="text-destructive" disabled={busy}>
                {busy && <Loader2Icon className="animate-spin" />}
                删除
              </Button>
            }
            title="删除这个季绑定？"
            confirmLabel="删除"
            onOpenChange={(open) => open && setWithBindings(false)}
            onConfirm={() => remove.mutate(withBindings)}
          >
            <p>「{view.title || view.sourceLabel}」的条目表和处理过的记录会一起删除。</p>
            {running && <p>正在进行的补建会随即停下。</p>}
            {view.bindingCount > 0 ? (
              <>
                <p>它建出的 {view.bindingCount} 个绑定默认保留，变成普通绑定。</p>
                <label className="flex items-center gap-2 font-medium text-foreground">
                  <Checkbox
                    checked={withBindings}
                    onCheckedChange={(checked) => setWithBindings(checked)}
                  />
                  同时删除它建出的 {view.bindingCount} 个绑定（连同弹幕，无法恢复）
                </label>
              </>
            ) : (
              <p>它还没有建出绑定。</p>
            )}
          </ConfirmButton>
        </div>
      </div>

      <button
        type="button"
        aria-expanded={expanded}
        className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? (
          <ChevronDownIcon className="size-3" />
        ) : (
          <ChevronRightIcon className="size-3" />
        )}
        条目表
      </button>
      {expanded && <ItemTable detail={detail} error={detailError} />}

      {error && <ErrorNote onClose={clearError}>{error}</ErrorNote>}
    </article>
  )
}

/** 集号对应的输入框，改动之后才显示"保存"；不合法时不能保存 */
function MappingEditor({
  binding,
  disabled,
  onSave,
}: {
  binding: SeasonBinding
  disabled: boolean
  onSave: (from: number, to: number) => void
}) {
  const [from, setFrom] = useState(String(binding.mappingFrom))
  const [to, setTo] = useState(String(binding.mappingTo))
  const f = parseMappingNumber(from)
  const t = parseMappingNumber(to)
  const changed = f !== binding.mappingFrom || t !== binding.mappingTo
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (f !== null && t !== null) onSave(f, t)
      }}
    >
      <MappingInputs
        from={from}
        to={to}
        onFromChange={setFrom}
        onToChange={setTo}
        disabled={disabled}
      />
      {changed && (
        <Button
          type="submit"
          size="xs"
          disabled={disabled || f === null || t === null}
          title="只影响还没处理过的条目，已经建出的绑定不动"
        >
          保存
        </Button>
      )}
    </form>
  )
}

const stateClass: Partial<Record<SeasonBindingItem['state'], string>> = {
  bound: 'text-emerald-700',
  failed: 'text-destructive',
  unmatched: 'text-amber-700',
}

/** 条目表：上次检查时合集里的条目，按在合集里的顺序，逐条显示状态 */
function ItemTable({ detail, error }: { detail?: SeasonBindingDetail; error: Error | null }) {
  if (!detail) {
    return error ? (
      <ErrorNote>{error.message}</ErrorNote>
    ) : (
      <p className="text-xs text-muted-foreground">加载中…</p>
    )
  }
  if (detail.items.length === 0) {
    return <p className="text-xs text-muted-foreground">上次检查时合集里没有条目。</p>
  }
  return (
    <Table aria-label="条目表" className="text-xs">
      <TableHeader>
        <TableRow>
          <TableHead className="w-12 text-right">序号</TableHead>
          <TableHead>条目</TableHead>
          <TableHead>状态</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {detail.items.map((it, i) => (
          <TableRow key={i}>
            <TableCell className="text-right tabular-nums">{it.number ?? '—'}</TableCell>
            <TableCell className="whitespace-normal">
              {it.label}
              {it.note && <div className="text-muted-foreground">{it.note}</div>}
            </TableCell>
            <TableCell
              className={cn('whitespace-normal', stateClass[it.state])}
              title={it.lastErrorAt ? formatDateTime(it.lastErrorAt) : undefined}
            >
              {itemStateText(it)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
