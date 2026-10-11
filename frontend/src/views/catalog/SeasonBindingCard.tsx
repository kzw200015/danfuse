import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  ChevronDownIcon,
  ChevronRightIcon,
  Loader2Icon,
  PencilIcon,
  RefreshCwIcon,
} from 'lucide-react'
import { toast } from 'sonner'

import { isApiStatus } from '@/api/request'
import {
  backfillSeasonBinding,
  deleteSeasonBinding,
  setFollow,
  type SeasonBinding,
  type SeasonBindingDetail,
} from '@/api/season-bindings'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { TableCell } from '@/components/ui/table'
import { Switch } from '@/components/ui/switch'
import { useElapsed } from '@/hooks/use-elapsed'
import { isRunning, useSeasonBinding, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { useSettings } from '@/hooks/use-settings'
import { followRuleText } from '@/lib/follow'
import { formatAgo, formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'

import { EditCollectionForm } from './CollectionForm'
import { EditFolderForm } from './FolderForm'
import { ItemTable, itemStatusClass } from './PreviewTable'
import { episodeText, itemStatusText, mappingHint, seasonBindingName } from './season-binding'
import { SourceLink, StatusBadge } from './shared'

/**
 * 一个季绑定的卡片，两种季绑定共用：名称与来源、集号对应、建出的绑定数；合集另有状态、追更开关、上次检查与补建进度。
 * 操作有编辑（在卡片里展开表单）、立即补建、删除，以及可展开的条目表。
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
  const [editing, setEditing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const clearError = () => setError(null)
  const showError = (e: Error) => setError(e.message)
  // 显示剧详情与轮询到的详情里较新的那一份：补建进行中建出的绑定数等逐条更新
  const {
    view,
    detail,
    error: detailError,
    refetch,
  } = useSeasonBinding(binding, summaryUpdatedAt, expanded)
  const running = isRunning(view)
  const elapsed = useElapsed(running)
  const watch = useWatchSeasonBinding()
  const reload = useReloadSeries()

  const follow = useMutation({
    mutationFn: (on: boolean) => setFollow(binding.id, on),
    onMutate: clearError,
    onSuccess: (d, on) => {
      toast.success(on ? '已打开追更，正在后台补建' : '已关闭追更')
      // 剧列表上的追更标记随之更新
      return watch(d)
    },
    onError: showError,
  })
  const backfill = useMutation({
    mutationFn: () => backfillSeasonBinding(binding.id),
    onMutate: clearError,
    onSuccess: (d) => {
      toast.success(d.kind === 'collection' ? '已开始补建' : '已补建')
      return watch(d)
    },
    onError: async (e) => {
      if (isApiStatus(e, 409)) {
        toast.error(e.message)
        // 正在补建：取一次详情，显示它的进度
        await refetch()
        return
      }
      showError(e)
    },
  })
  const remove = useMutation({
    mutationFn: () => deleteSeasonBinding(binding.id),
    onMutate: clearError,
    onSuccess: () => {
      toast.success('已删除季绑定')
      return reload()
    },
    onError: (e) => {
      showError(e)
      // 已经不在了（例如在别处删掉了）：重新加载，卡片随之消失
      if (isApiStatus(e, 404)) return reload()
    },
  })
  const busy = remove.isPending
  const { data: settings } = useSettings()

  const collection = view.kind === 'collection' ? view : null
  const dead = collection?.status === 'dead'
  const name = seasonBindingName(view)
  const n = view.bindingCount
  return (
    <article
      aria-label={name}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        {collection && (
          <StatusBadge
            dead={dead}
            deadTitle="上次检查时合集已不存在或已下架；已经建出的绑定不受影响，追更开着时照常检查，恢复后自动变回正常"
          />
        )}
        <div className="min-w-0 flex-1">
          {collection ? (
            <SourceLink href={collection.sourceUrl}>{name}</SourceLink>
          ) : (
            <span className="font-medium break-all">{name}</span>
          )}
          <div className="text-xs text-muted-foreground">
            {view.kind === 'collection'
              ? view.sourceLabel
              : `上传的弹幕文件夹 · ${view.entryCount} 个条目`}
          </div>
        </div>
        {collection && (
          <label
            className="flex shrink-0 items-center gap-1.5 text-xs"
            title={settings && `开着时${followRuleText(settings.follow)}`}
          >
            <Switch
              size="sm"
              checked={collection.follow}
              disabled={follow.isPending || busy}
              onCheckedChange={(on) => follow.mutate(on)}
            />
            追更
          </label>
        )}
      </div>

      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span title={mappingHint(collection ? '合集' : '')}>
          {collection ? '合集' : ''}第 {view.mappingFrom} 集 = 本地第 {view.mappingTo} 集
        </span>
        <span>建出 {n} 个绑定</span>
        {collection &&
          (collection.lastCheckedAt ? (
            <span title={formatDateTime(collection.lastCheckedAt)}>
              上次检查 {formatAgo(collection.lastCheckedAt)}
            </span>
          ) : (
            <span>还没有检查</span>
          ))}
        {!collection && <span>上传于 {formatDateTime(view.createdAt)}</span>}
        {collection?.lastError && (
          <span className="text-destructive">上次检查：{collection.lastError}</span>
        )}
        {running && (
          <span className="inline-flex items-center gap-1 text-foreground">
            <Loader2Icon className="size-3 animate-spin" />
            补建中 {elapsed}s
          </span>
        )}
      </div>

      <div className="flex justify-end gap-1">
        <Button
          variant="outline"
          size="sm"
          disabled={editing || busy}
          title={
            collection
              ? '换合集、改集号规则和集号对应，先预览再保存'
              : '换文件夹、改集号规则和集号对应，先预览再保存'
          }
          onClick={() => {
            clearError()
            setEditing(true)
          }}
        >
          <PencilIcon />
          编辑
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={backfill.isPending || busy}
          title={
            collection
              ? '现在检查一次合集，为缺的集补建绑定；追更关着时也能用'
              : '按保存的条目为缺的集建出绑定'
          }
          onClick={() => backfill.mutate()}
        >
          {backfill.isPending ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
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
          onConfirm={() => remove.mutate()}
        >
          <p>
            它建出的 {n} 个绑定会连同弹幕一起删除
            {collection ? '' : '，上传的文件也一起删除'}，无法恢复。
          </p>
          {running && <p>正在进行的补建会随即停下。</p>}
        </ConfirmButton>
      </div>

      {editing &&
        (view.kind === 'collection' ? (
          <EditCollectionForm binding={view} onClose={() => setEditing(false)} />
        ) : (
          <EditFolderForm binding={view} onClose={() => setEditing(false)} />
        ))}

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
      {expanded && <EntryTable detail={detail} error={detailError} />}

      {error && <ErrorNote onClose={clearError}>{error}</ErrorNote>}
    </article>
  )
}

/**
 * 条目表：各条目按补建的语义逐条显示状态，上次补建失败的标红并写出原因；
 * 表下用黄色提示列出没有条目、绑定还留着的槽位（补建不删，保存一次表单会删掉）
 */
function EntryTable({ detail, error }: { detail?: SeasonBindingDetail; error: Error | null }) {
  if (!detail) {
    return error ? (
      <ErrorNote>{error.message}</ErrorNote>
    ) : (
      <p className="text-xs text-muted-foreground">加载中…</p>
    )
  }
  return (
    <div className="grid gap-2">
      {detail.items.length === 0 ? (
        <p className="text-xs text-muted-foreground">没有条目。</p>
      ) : (
        <ItemTable
          label="条目表"
          items={detail.items}
          statusHead="状态"
          renderStatus={(item) => (
            <TableCell
              className={cn(
                'whitespace-normal',
                item.lastError ? 'text-destructive' : itemStatusClass[item.status],
              )}
              title={item.lastErrorAt ? `失败于 ${formatDateTime(item.lastErrorAt)}` : undefined}
            >
              {itemStatusText(item, detail.kind)}
              {item.lastError && ` · 上次失败：${item.lastError}`}
            </TableCell>
          )}
        />
      )}
      {detail.emptySlots.length > 0 && (
        <div
          role="note"
          aria-label="没有条目的槽位"
          className="rounded-md bg-amber-50 p-2 text-xs text-amber-900"
        >
          <p className="font-medium">没有条目、绑定还留着（{detail.emptySlots.length}）</p>
          <ul>
            {detail.emptySlots.map((slot) => (
              <li key={slot.bindingId}>
                {episodeText(slot.episodeNumber, slot.title)}
                {slot.dead && '（失效）'}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-amber-700">
            {detail.kind === 'collection' ? '合集' : '文件夹'}
            里已经没有对到这些集的条目。补建不会删它们；编辑并保存一次季绑定会删掉。
          </p>
        </div>
      )}
    </div>
  )
}
