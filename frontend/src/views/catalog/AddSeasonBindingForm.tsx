import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Loader2Icon, SearchIcon } from 'lucide-react'
import { toast } from 'sonner'

import {
  createSeasonBinding,
  previewSeasonBinding,
  type CollectionCandidate,
  type Mapping,
} from '@/api/season-bindings'
import type { Season } from '@/api/series'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useElapsed } from '@/hooks/use-elapsed'
import { useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { cn } from '@/lib/utils'

import CollectionItemsTable from './CollectionItemsTable'
import MappingInputs, { useMappingDraft } from './MappingInputs'
import { candidateText, previewTarget, previewTargetText } from './season-binding'
import { SourceLink } from './shared'

/**
 * 季面板里添加季绑定：贴链接 → 预览（后端当场识别链接、列出合集）→ 有多个候选时先选一个 → 看对应表、改集号对应 → 创建。
 * 预览不保存任何东西，取消就当没发生过。点"创建"后预览收起，新的季绑定卡片出现并显示补建进度。
 * 换一季时由调用方用 key 重新挂载，输入和预览不会带到别的季。
 */
export default function AddSeasonBindingForm({ season }: { season: Season }) {
  const [link, setLink] = useState('')
  const preview = useMutation({
    mutationFn: (url: string) => previewSeasonBinding(season.id, url),
  })
  const elapsed = useElapsed(preview.isPending)

  return (
    <div className="grid gap-2">
      <form
        aria-label="添加季绑定"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault()
          preview.mutate(link.trim())
        }}
      >
        <div className="flex gap-2">
          <Input
            aria-label="合集的链接"
            placeholder="粘贴 B 站番剧（ss、md、ep 链接）、空间里的合集页、多 P 投稿的链接或 b23.tv 短链"
            value={link}
            disabled={preview.isPending}
            onChange={(e) => setLink(e.target.value)}
          />
          <Button type="submit" variant="outline" disabled={preview.isPending || !link.trim()}>
            {preview.isPending ? <Loader2Icon className="animate-spin" /> : <SearchIcon />}
            {preview.isPending ? `预览中 ${elapsed}s` : '预览'}
          </Button>
        </div>
        {preview.isPending && (
          <p className="text-xs text-muted-foreground">正在识别链接并列出合集，最长约 25 秒…</p>
        )}
        {preview.error && <ErrorNote onClose={preview.reset}>{preview.error.message}</ErrorNote>}
      </form>
      {preview.data && preview.variables !== undefined && (
        <CandidateChooser
          // 每次预览重新挂载，上一次的选择和改过的对应不带过来
          key={preview.submittedAt}
          season={season}
          link={preview.variables}
          candidates={preview.data.candidates}
          onDone={() => {
            setLink('')
            preview.reset()
          }}
          onCancel={preview.reset}
        />
      )}
    </div>
  )
}

/** 预览结果：有多个候选时先选一个（例如"这个稿件的 N 个分 P"和"它所在的合集"），选中的显示对应表 */
function CandidateChooser({
  season,
  link,
  candidates,
  onDone,
  onCancel,
}: {
  season: Season
  link: string
  candidates: CollectionCandidate[]
  onDone: () => void
  onCancel: () => void
}) {
  const [kind, setKind] = useState(candidates.length === 1 ? candidates[0]!.kind : undefined)
  const selected = candidates.find((c) => c.kind === kind)

  return (
    <section aria-label="预览" className="grid gap-3 rounded-lg border bg-muted/30 p-3">
      {candidates.length > 1 && (
        <div role="radiogroup" aria-label="选择要绑定的合集" className="grid gap-1.5">
          <p className="text-xs text-muted-foreground">这个链接对应多个合集，选一个：</p>
          {candidates.map((c) => (
            <button
              key={c.kind}
              type="button"
              role="radio"
              aria-checked={c.kind === kind}
              className={cn(
                'rounded-md border px-3 py-2 text-left text-sm hover:bg-muted',
                c.kind === kind && 'border-primary bg-muted',
              )}
              onClick={() => setKind(c.kind)}
            >
              {candidateText(c)}
              <span className="block text-xs text-muted-foreground">{c.sourceLabel}</span>
            </button>
          ))}
        </div>
      )}
      {selected ? (
        <CandidatePreview
          key={selected.kind}
          season={season}
          link={link}
          candidate={selected}
          onDone={onDone}
          onCancel={onCancel}
        />
      ) : (
        <div>
          <Button variant="ghost" size="sm" onClick={onCancel}>
            取消
          </Button>
        </div>
      )}
    </section>
  )
}

/** 一个候选合集的预览：合集信息、完结提示、可改的集号对应，以及按对应现算的对应表 */
function CandidatePreview({
  season,
  link,
  candidate,
  onDone,
  onCancel,
}: {
  season: Season
  link: string
  candidate: CollectionCandidate
  onDone: () => void
  onCancel: () => void
}) {
  const reload = useReloadSeries()
  const watch = useWatchSeasonBinding()
  const { from, to, setFrom, setTo, mapping } = useMappingDraft({
    from: candidate.mappingFrom,
    to: candidate.mappingTo,
  })
  const create = useMutation({
    mutationFn: (m: Mapping) =>
      createSeasonBinding(season.id, {
        link,
        kind: candidate.kind,
        mappingFrom: m.from,
        mappingTo: m.to,
      }),
    onSuccess: async (detail) => {
      toast.success('已创建季绑定，正在后台补建')
      await watch(detail.id, detail)
      onDone()
      // 季面板显示新的季绑定卡片
      return reload()
    },
  })
  const elapsed = useElapsed(create.isPending)

  return (
    <div className="grid gap-3">
      <div>
        <SourceLink href={candidate.sourceUrl}>{candidate.title}</SourceLink>
        <div className="text-xs text-muted-foreground">
          {candidate.sourceLabel} · 共 {candidate.items.length} 条
        </div>
        {candidate.finished && (
          <p className="mt-1 text-xs text-amber-700">
            已完结，可以关掉追更：新建的季绑定开着追更，建出的绑定在 14 天内每天自动重新拉取。
          </p>
        )}
      </div>

      <MappingInputs from={from} to={to} onFromChange={setFrom} onToChange={setTo} />

      {candidate.items.length === 0 ? (
        <p className="text-xs text-muted-foreground">合集里还没有条目。</p>
      ) : (
        <CollectionItemsTable
          label="对应表"
          heading="对到本地"
          rows={candidate.items.map((it) => {
            const target = mapping && previewTarget(it, mapping, season.episodes)
            return {
              number: it.number,
              label: it.label,
              note: it.note,
              text: target ? previewTargetText(target) : '—',
              className: cn(
                target?.kind === 'episode' ? 'text-foreground' : 'text-muted-foreground',
                target?.kind === 'unmatched' && 'text-amber-700',
              ),
            }
          })}
        />
      )}

      <div className="flex gap-2">
        <Button
          disabled={create.isPending || mapping === null}
          onClick={() => mapping && create.mutate(mapping)}
        >
          {create.isPending && <Loader2Icon className="animate-spin" />}
          {create.isPending ? `创建中 ${elapsed}s` : '创建'}
        </Button>
        <Button variant="ghost" disabled={create.isPending} onClick={onCancel}>
          取消
        </Button>
      </div>
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </div>
  )
}
