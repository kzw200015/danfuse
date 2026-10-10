import { useState, type ReactNode } from 'react'
import { useMutation } from '@tanstack/react-query'
import { ChevronDownIcon, ChevronRightIcon, Loader2Icon, RefreshCwIcon } from 'lucide-react'
import { toast } from 'sonner'

import { isApiStatus } from '@/api/request'
import {
  backfillSeasonBinding,
  deleteSeasonBinding,
  updateSeasonBinding,
  type CollectionSeasonBinding,
  type FolderSeasonBinding,
  type SeasonBinding,
  type SeasonBindingDetail,
  type SeasonBindingPatch,
  type SeasonBindingItemState,
} from '@/api/season-bindings'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Switch } from '@/components/ui/switch'
import { useElapsed } from '@/hooks/use-elapsed'
import { useSeasonBinding, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { useSettings } from '@/hooks/use-settings'
import { followRuleText } from '@/lib/follow'
import { formatAgo, formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'

import CollectionItemsTable from './CollectionItemsTable'
import { EpisodeRuleEditor } from './EpisodeRuleInput'
import { MappingEditor } from './MappingInputs'
import { itemStateText, seasonBindingName } from './season-binding'
import { SourceLink, StatusBadge } from './shared'

/** 一个季绑定的卡片，按 kind 分为合集的季绑定和文件夹的季绑定 */
export default function SeasonBindingCard({
  binding,
  summaryUpdatedAt,
}: {
  /** 剧详情里的这个季绑定 */
  binding: SeasonBinding
  /** 剧详情取到的时间，与轮询到的详情比较哪个新 */
  summaryUpdatedAt: number
}) {
  return binding.kind === 'folder' ? (
    <FolderSeasonBindingCard binding={binding} />
  ) : (
    <CollectionSeasonBindingCard binding={binding} summaryUpdatedAt={summaryUpdatedAt} />
  )
}

/**
 * 合集的季绑定的卡片：状态、合集标题（链接到原页面）与标签、建出的绑定数、上次检查的时间与错误、补建中的已用秒数；
 * 集号对应与集号规则（投稿合集、多 P 投稿；保存后后台补建）、追更开关、立即补建、删除，以及可展开的条目表。
 * 操作成功用 toast（立即补建被拒绝的 409 也用 toast）；其余失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
function CollectionSeasonBindingCard({
  binding,
  summaryUpdatedAt,
}: {
  binding: CollectionSeasonBinding
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
      toast.success(updatedText(patch))
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
      if (isApiStatus(e, 409)) {
        toast.error(e.message)
        return watch(binding.id) // 正在补建：显示它的进度
      }
      showError(e)
    },
  })
  const remove = useDeleteSeasonBinding(binding.id, clearError, showError)
  const busy = remove.isPending
  const { data: settings } = useSettings()

  const dead = view.status === 'dead'
  const name = seasonBindingName(view)
  return (
    <article
      aria-label={name}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        <StatusBadge
          dead={dead}
          deadTitle="上次检查时合集已不存在或已下架；已经建出的绑定不受影响，追更开着时照常检查，恢复后自动变回正常"
        />
        <div className="min-w-0 flex-1">
          <SourceLink href={view.sourceUrl}>{name}</SourceLink>
          <div className="text-xs text-muted-foreground">
            {view.sourceLabel}
            {view.finished && ' · 已完结'}
          </div>
        </div>
        <label
          className="flex shrink-0 items-center gap-1.5 text-xs"
          title={settings && `开着时${followRuleText(settings.follow)}`}
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
          onSave={({ from, to }) => update.mutate({ mappingFrom: from, mappingTo: to })}
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
          <DeleteSeasonBindingButton
            pending={busy}
            bindingCount={view.bindingCount}
            checkboxLabel={`同时删除它建出的 ${view.bindingCount} 个绑定（连同弹幕，无法恢复）`}
            onConfirm={(withBindings) => remove.mutate(withBindings)}
          >
            {() => (
              <>
                <p>「{name}」的条目表和处理过的记录会一起删除。</p>
                {running && <p>正在进行的补建会随即停下。</p>}
                {view.bindingCount > 0 ? (
                  <p>它建出的 {view.bindingCount} 个绑定默认保留，变成普通绑定。</p>
                ) : (
                  <p>它还没有建出绑定。</p>
                )}
              </>
            )}
          </DeleteSeasonBindingButton>
        </div>
      </div>

      {view.numberedByRule && (
        // 保存成功后规则变了，用新的值重新初始化输入框
        <EpisodeRuleEditor
          key={view.episodePatterns.join('\n')}
          binding={view}
          disabled={update.isPending || busy}
          onSave={(episodePatterns) => update.mutate({ episodePatterns })}
        />
      )}

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

/**
 * 文件夹的季绑定的卡片：文件夹名、建出的绑定数、上传时间，只能删除。它不补建、不追更、不会失效，没有条目表，也不轮询。
 * 删除成功用 toast；失败的提示显示在卡片下方，保留到下次操作或手动关闭。
 */
function FolderSeasonBindingCard({ binding }: { binding: FolderSeasonBinding }) {
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const remove = useDeleteSeasonBinding(binding.id, clearError, (e) => setError(e.message))
  const n = binding.bindingCount
  return (
    <article aria-label={binding.title} className="grid gap-2 rounded-lg border p-3">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <span className="font-medium break-all">{binding.title}</span>
          <div className="text-xs text-muted-foreground">上传的弹幕文件夹</div>
        </div>
        <DeleteSeasonBindingButton
          pending={remove.isPending}
          bindingCount={n}
          checkboxLabel={`同时删除它建出的 ${n} 个绑定`}
          onConfirm={(withBindings) => remove.mutate(withBindings)}
        >
          {(withBindings) =>
            n === 0 ? (
              <p>它建出的绑定都已删除。</p>
            ) : withBindings ? (
              <p>它建出的 {n} 个绑定和上传的弹幕文件会一起删除，无法恢复。</p>
            ) : (
              <p>它建出的 {n} 个绑定默认保留，变成普通的文件绑定。</p>
            )
          }
        </DeleteSeasonBindingButton>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>建出 {n} 个绑定</span>
        <span>上传于 {formatDateTime(binding.createdAt)}</span>
      </div>
      {error && <ErrorNote onClose={clearError}>{error}</ErrorNote>}
    </article>
  )
}

/** 删除季绑定：成功后用 toast 提示并重新加载剧详情；已经不在了（例如在别处删掉了）时也重新加载，卡片随之消失 */
function useDeleteSeasonBinding(id: number, onMutate: () => void, onError: (e: Error) => void) {
  const reload = useReloadSeries()
  return useMutation({
    mutationFn: (withBindings: boolean) => deleteSeasonBinding(id, withBindings),
    onMutate,
    onSuccess: () => {
      toast.success('已删除季绑定')
      return reload()
    },
    onError: (e) => {
      onError(e)
      if (isApiStatus(e, 404)) return reload()
    },
  })
}

/**
 * 删除季绑定的按钮与确认框。还有建出的绑定时，确认框末尾是"同时删除建出的绑定"的勾选框，每次打开时恢复成默认的不勾选；
 * children 按是否勾选写出后果
 */
function DeleteSeasonBindingButton({
  pending,
  bindingCount,
  checkboxLabel,
  onConfirm,
  children,
}: {
  pending: boolean
  bindingCount: number
  checkboxLabel: string
  onConfirm: (withBindings: boolean) => void
  children: (withBindings: boolean) => ReactNode
}) {
  const [withBindings, setWithBindings] = useState(false)
  return (
    <ConfirmButton
      trigger={
        <Button variant="ghost" size="sm" className="text-destructive" disabled={pending}>
          {pending && <Loader2Icon className="animate-spin" />}
          删除
        </Button>
      }
      title="删除这个季绑定？"
      confirmLabel="删除"
      onOpenChange={(open) => open && setWithBindings(false)}
      onConfirm={() => onConfirm(withBindings)}
    >
      {children(withBindings)}
      {bindingCount > 0 && (
        <label className="flex items-center gap-2 font-medium text-foreground">
          <Checkbox
            checked={withBindings}
            onCheckedChange={(checked) => setWithBindings(checked)}
          />
          {checkboxLabel}
        </label>
      )}
    </ConfirmButton>
  )
}

/** 改季绑定成功的提示：卡片上每次只改一样（追更、集号对应或集号规则） */
function updatedText(patch: SeasonBindingPatch) {
  if (patch.follow !== undefined) return patch.follow ? '已打开追更，正在后台补建' : '已关闭追更'
  if (patch.episodePatterns !== undefined) return '集号规则已保存，正在后台补建'
  return '集号对应已保存，正在后台补建'
}

const stateClass: Partial<Record<SeasonBindingItemState, string>> = {
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
    <CollectionItemsTable
      label="条目表"
      heading="状态"
      rows={detail.items.map((it) => ({
        number: it.number,
        label: it.label,
        text: itemStateText(it),
        className: stateClass[it.state],
        title: it.lastErrorAt ? formatDateTime(it.lastErrorAt) : undefined,
      }))}
    />
  )
}
