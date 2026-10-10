import { useState } from 'react'
import { useIsMutating, useMutation, useQueryClient } from '@tanstack/react-query'
import { FolderOpenIcon, FolderUpIcon, Loader2Icon } from 'lucide-react'
import { toast } from 'sonner'

import { createSeasonFileBindings, previewSeasonFileBindings } from '@/api/bindings'
import type { PreviewItem } from '@/api/season-bindings'
import type { Season } from '@/api/series'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { useElapsed } from '@/hooks/use-elapsed'
import { defaultEpisodePatternsOptions } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { cn } from '@/lib/utils'

import CollectionItemsTable from './CollectionItemsTable'
import EpisodeRuleInput, { cleanPatterns, samePatterns } from './EpisodeRuleInput'
import MappingInputs, { useMappingDraft } from './MappingInputs'
import { previewTarget, previewTargetText } from './season-binding'
import {
  defaultMapping,
  groupFolderFiles,
  type FolderGrouping,
  type UploadEntry,
} from './season-upload'
import { takeFiles } from './shared'

/** 一季的上传请求的变更键：上传进行中对话框不能关 */
const uploadKey = (seasonId: number) => ['season-upload', seasonId] as const

/**
 * 季面板上"上传弹幕文件夹"的按钮与对话框。对话框每次打开都从选文件夹开始，关掉就当没发生过；
 * 上传进行中不能关。换一季时由调用方用 key 重新挂载
 */
export default function SeasonUploadButton({ season }: { season: Season }) {
  const [open, setOpen] = useState(false)
  const uploading = useIsMutating({ mutationKey: uploadKey(season.id) }) > 0
  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <FolderUpIcon />
        上传弹幕文件夹
      </Button>
      <Dialog open={open} onOpenChange={(o) => (o || !uploading) && setOpen(o)}>
        {/* 贴顶显示：预览出来时只有底边动 */}
        <DialogContent className="top-[5vh] max-h-[90vh] translate-y-0 overflow-y-auto sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>上传弹幕文件夹</DialogTitle>
            <DialogDescription>
              选一个文件夹：每个子目录（里面的 XML 合在一起）或顶层的每份 XML
              是一个条目，按集号规则认出集号、对到本季的集。
              勾选的条目各建一个用弹幕文件建的绑定，要么全部建出，要么一个都不建。
            </DialogDescription>
          </DialogHeader>
          <SeasonUpload season={season} uploading={uploading} onDone={() => setOpen(false)} />
        </DialogContent>
      </Dialog>
    </>
  )
}

/** 显示中的预览：最近一次成功的预览（选文件夹后的预览，或改了集号规则之后的重新预览） */
interface ShownPreview {
  entries: UploadEntry[]
  /** 这次预览用的集号规则 */
  patterns: string[]
  /** 与 entries 一一对应 */
  items: PreviewItem[]
  /** 每次成功的预览加一，用来重新挂载，上一次的集号对应和勾选不带过来 */
  seq: number
}

/** 选文件夹 → 分组（结构不对时直接提示）→ 用默认的集号规则预览 */
function SeasonUpload({
  season,
  uploading,
  onDone,
}: {
  season: Season
  uploading: boolean
  onDone: () => void
}) {
  const [grouping, setGrouping] = useState<FolderGrouping>()
  const [shown, setShown] = useState<ShownPreview>()
  const show = (entries: UploadEntry[], patterns: string[], items: PreviewItem[]) =>
    setShown((prev) => ({ entries, patterns, items, seq: (prev?.seq ?? 0) + 1 }))
  const queryClient = useQueryClient()
  const preview = useMutation({
    mutationFn: async (entries: UploadEntry[]) => {
      const patterns = await queryClient.ensureQueryData(defaultEpisodePatternsOptions)
      const data = await previewSeasonFileBindings(
        season.id,
        entries.map((e) => e.label),
        patterns,
      )
      return { patterns, items: data.items }
    },
    onSuccess: ({ patterns, items }, entries) => show(entries, patterns, items),
  })

  function pick(files: File[]) {
    if (files.length === 0) return
    const result = groupFolderFiles(files)
    setGrouping(result)
    setShown(undefined)
    preview.reset()
    if (result.ok) preview.mutate(result.entries)
  }

  return (
    <div className="grid min-w-0 gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <label
          className={cn(
            'inline-flex cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm hover:bg-muted/50',
            (preview.isPending || uploading) && 'cursor-default opacity-50',
          )}
        >
          {preview.isPending ? (
            <Loader2Icon className="size-4 animate-spin" />
          ) : (
            <FolderOpenIcon className="size-4" />
          )}
          {preview.isPending ? '预览中…' : shown ? '换一个文件夹' : '选择文件夹'}
          <input
            type="file"
            multiple
            className="sr-only"
            aria-label="选择弹幕文件夹"
            // React 的类型里没有 webkitdirectory：选文件夹，文件带着相对路径（webkitRelativePath）
            {...{ webkitdirectory: '' }}
            disabled={preview.isPending || uploading}
            onChange={(e) => pick(takeFiles(e.target))}
          />
        </label>
        {grouping?.ok && (
          <span className="text-xs text-muted-foreground">
            {grouping.entries.length} 个条目
            {grouping.ignored > 0 && `，忽略了 ${grouping.ignored} 份不是 XML 的文件`}
          </span>
        )}
      </div>
      {grouping && !grouping.ok && <ErrorNote>{grouping.error}</ErrorNote>}
      {preview.error && <ErrorNote onClose={preview.reset}>{preview.error.message}</ErrorNote>}
      {shown && (
        <UploadPreview
          key={shown.seq}
          season={season}
          entries={shown.entries}
          patterns={shown.patterns}
          items={shown.items}
          onRepreviewed={(patterns, items) => show(shown.entries, patterns, items)}
          onDone={onDone}
        />
      )}
    </div>
  )
}

/**
 * 预览：集号规则（改了之后重新预览才能上传）、可改的集号对应（预填 defaultMapping），以及按对应现算的表格。
 * 对到本地已有的集的行可以勾，默认勾上；那一集已有用弹幕文件建的绑定时默认不勾。确认后只上传勾选的行
 */
function UploadPreview({
  season,
  entries,
  patterns,
  items,
  onRepreviewed,
  onDone,
}: {
  season: Season
  entries: UploadEntry[]
  patterns: string[]
  items: PreviewItem[]
  onRepreviewed: (patterns: string[], items: PreviewItem[]) => void
  onDone: () => void
}) {
  const reload = useReloadSeries()
  const { from, to, setFrom, setTo, mapping } = useMappingDraft(
    defaultMapping(items, season.episodes),
  )
  const [rule, setRule] = useState(patterns)
  const ruleChanged = !samePatterns(cleanPatterns(rule), patterns)
  const repreview = useMutation({
    mutationFn: (p: string[]) =>
      previewSeasonFileBindings(
        season.id,
        entries.map((e) => e.label),
        p,
      ),
    onSuccess: (data, p) => onRepreviewed(p, data.items),
  })
  // 手动改过的勾选，按"条目 → 集"记：改了集号对应、对到别的集时回到默认
  const [toggled, setToggled] = useState<ReadonlyMap<string, boolean>>(new Map())
  const rows = entries.map((entry, i) => {
    const target = mapping && previewTarget(items[i]!, mapping, season.episodes)
    const episode = target?.kind === 'episode' ? target.episode : null
    const hasFile = episode?.bindings.some((b) => b.kind === 'file') ?? false
    const key = episode && `${entry.label}\n${episode.id}`
    const checked = key !== null && (toggled.get(key) ?? !hasFile)
    return { entry, item: items[i]!, target, episode, hasFile, key, checked }
  })
  const selected = rows.filter((r) => r.checked)

  // 上传的进度（0～1），上传完是"正在保存"
  const [progress, setProgress] = useState(0)
  const create = useMutation({
    mutationKey: uploadKey(season.id),
    mutationFn: () =>
      createSeasonFileBindings(
        season.id,
        {
          files: selected.flatMap((r) => r.entry.files),
          paths: selected.flatMap((r) => r.entry.paths),
          targets: selected.map((r) => ({ label: r.entry.label, episodeId: r.episode!.id })),
        },
        setProgress,
      ),
    onMutate: () => setProgress(0),
    onSuccess: (result) => {
      toast.success(`已建出 ${result.bindings} 个绑定，共 ${result.added.toLocaleString()} 条弹幕`)
      onDone()
      // 季下各集显示新的绑定
      return reload()
    },
  })
  const saving = create.isPending && progress >= 1
  const savingElapsed = useElapsed(saving)
  const repreviewElapsed = useElapsed(repreview.isPending)
  const busy = create.isPending || repreview.isPending

  return (
    <section aria-label="预览" className="grid min-w-0 gap-3 rounded-lg border bg-muted/30 p-3">
      <div className="grid gap-1.5">
        <form
          onSubmit={(e) => {
            e.preventDefault()
            repreview.mutate(cleanPatterns(rule))
          }}
        >
          <EpisodeRuleInput value={rule} onChange={setRule} disabled={busy}>
            {ruleChanged && (
              <Button type="submit" size="xs" variant="outline" disabled={busy}>
                {repreview.isPending && <Loader2Icon className="animate-spin" />}
                {repreview.isPending ? `重新预览中 ${repreviewElapsed}s` : '重新预览'}
              </Button>
            )}
          </EpisodeRuleInput>
        </form>
        {repreview.error && (
          <ErrorNote onClose={repreview.reset}>{repreview.error.message}</ErrorNote>
        )}
      </div>

      <MappingInputs
        prefix=""
        from={from}
        to={to}
        onFromChange={setFrom}
        onToChange={setTo}
        disabled={create.isPending}
      />

      <CollectionItemsTable
        label="上传预览"
        heading="对到本地"
        selectable
        rows={rows.map((r) => ({
          number: r.item.number,
          label: r.entry.label,
          text: r.target ? previewTargetText(r.target) + (r.hasFile ? ' · 已有文件绑定' : '') : '—',
          className: cn(
            r.target?.kind === 'episode' ? 'text-foreground' : 'text-muted-foreground',
            r.target?.kind === 'unmatched' && 'text-amber-700',
          ),
          selection: {
            checked: r.checked,
            disabled: r.key === null || create.isPending,
            onCheckedChange: (checked) =>
              r.key !== null && setToggled((prev) => new Map(prev).set(r.key!, checked)),
          },
        }))}
      />

      <div className="flex flex-wrap items-center gap-2">
        <Button
          disabled={busy || mapping === null || ruleChanged || selected.length === 0}
          onClick={() => create.mutate()}
        >
          {create.isPending && <Loader2Icon className="animate-spin" />}
          上传 {selected.length} 个条目
        </Button>
        {ruleChanged && (
          <span className="text-xs text-muted-foreground">集号规则改了，先重新预览</span>
        )}
        {create.isPending &&
          (saving ? (
            <span className="text-xs text-muted-foreground">正在保存 {savingElapsed}s…</span>
          ) : (
            <span className="flex min-w-40 flex-1 items-center gap-2 text-xs text-muted-foreground">
              <Progress
                className="flex-1"
                value={Math.round(progress * 100)}
                aria-label="上传进度"
              />
              上传中 {Math.round(progress * 100)}%
            </span>
          ))}
      </div>
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </section>
  )
}
