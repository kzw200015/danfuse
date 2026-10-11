import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
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
import { defaultEpisodePatternsOptions, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { useSettings } from '@/hooks/use-settings'
import { followCostText } from '@/lib/follow'
import { cn } from '@/lib/utils'

import CollectionItemsTable from './CollectionItemsTable'
import { cleanPatterns, EpisodeRuleRepreview, samePatterns } from './EpisodeRuleInput'
import MappingInputs, { useMappingDraft } from './MappingInputs'
import {
  candidateText,
  previewTarget,
  previewTargetClass,
  previewTargetText,
} from './season-binding'
import { SourceLink } from './shared'

/** 显示中的预览：最近一次成功的预览（贴链接的预览，或改了集号规则之后的重新预览），重新预览进行中、失败时仍然显示 */
interface ShownPreview {
  link: string
  /** 这次预览用的集号规则 */
  patterns: string[]
  candidates: CollectionCandidate[]
  /** 每次成功的预览加一，用来重新挂载，上一次改过的对应不带过来 */
  seq: number
}

/**
 * 季面板里添加季绑定：贴链接 → 预览（后端当场识别链接、列出合集）→ 有多个候选时先选一个 → 看对应表、改集号对应
 * （投稿合集和多 P 投稿还可以改集号规则、重新预览）→ 创建。
 * 预览不保存任何东西，取消就当没发生过。点"创建"后预览收起，新的季绑定卡片出现并显示补建进度。
 * 换一季时由调用方用 key 重新挂载，输入和预览不会带到别的季。
 */
export default function AddSeasonBindingForm({ season }: { season: Season }) {
  const [link, setLink] = useState('')
  // 选中的候选：重新预览时保留
  const [kind, setKind] = useState<string>()
  const [shown, setShown] = useState<ShownPreview>()
  const show = (url: string, patterns: string[], candidates: CollectionCandidate[]) =>
    setShown((prev) => ({ link: url, patterns, candidates, seq: (prev?.seq ?? 0) + 1 }))
  // 贴链接的预览用默认规则，它也是编辑的起点；默认规则在第一次预览时取，之后用缓存
  const queryClient = useQueryClient()
  const preview = useMutation({
    mutationFn: async (url: string) => {
      const patterns = await queryClient.ensureQueryData(defaultEpisodePatternsOptions)
      return { patterns, data: await previewSeasonBinding(season.id, url, patterns) }
    },
    onSuccess: ({ patterns, data }, url) => show(url, patterns, data.candidates),
  })
  const elapsed = useElapsed(preview.isPending)
  const close = () => {
    setShown(undefined)
    preview.reset()
  }

  return (
    <div className="grid gap-2">
      <form
        aria-label="添加季绑定"
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault()
          setShown(undefined)
          setKind(undefined)
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
      {shown && (
        <CandidateChooser
          key={shown.seq}
          season={season}
          link={shown.link}
          patterns={shown.patterns}
          candidates={shown.candidates}
          kind={kind}
          onKindChange={setKind}
          onRepreviewed={(patterns, candidates) => show(shown.link, patterns, candidates)}
          onDone={() => {
            setLink('')
            close()
          }}
          onCancel={close}
        />
      )}
    </div>
  )
}

/** 预览结果：有多个候选时先选一个（例如"这个稿件的 N 个分 P"和"它所在的合集"），选中的显示对应表 */
function CandidateChooser({
  season,
  link,
  patterns,
  candidates,
  kind,
  onKindChange,
  onRepreviewed,
  onDone,
  onCancel,
}: {
  season: Season
  link: string
  /** 这次预览用的集号规则 */
  patterns: string[]
  candidates: CollectionCandidate[]
  kind: string | undefined
  onKindChange: (kind: string) => void
  /** 按新的集号规则重新预览成功 */
  onRepreviewed: (patterns: string[], candidates: CollectionCandidate[]) => void
  onDone: () => void
  onCancel: () => void
}) {
  const selectedKind = candidates.length === 1 ? candidates[0]!.kind : kind
  const selected = candidates.find((c) => c.kind === selectedKind)

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
              aria-checked={c.kind === selectedKind}
              className={cn(
                'rounded-md border px-3 py-2 text-left text-sm hover:bg-muted',
                c.kind === selectedKind && 'border-primary bg-muted',
              )}
              onClick={() => onKindChange(c.kind)}
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
          patterns={patterns}
          candidate={selected}
          onRepreviewed={onRepreviewed}
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

/**
 * 一个候选合集的预览：合集信息、完结提示、集号规则（按规则编号的合集，改了之后重新预览才能创建）、可改的集号对应，
 * 以及按对应现算的对应表
 */
function CandidatePreview({
  season,
  link,
  patterns,
  candidate,
  onRepreviewed,
  onDone,
  onCancel,
}: {
  season: Season
  link: string
  patterns: string[]
  candidate: CollectionCandidate
  onRepreviewed: (patterns: string[], candidates: CollectionCandidate[]) => void
  onDone: () => void
  onCancel: () => void
}) {
  const reload = useReloadSeries()
  const watch = useWatchSeasonBinding()
  const { data: settings } = useSettings()
  // 集号对应预填"合集第 1 集 = 本地第 1 集"，不按合集内容猜：接着上一季编号的番剧在对应表里一眼能看出来，手动改
  const { from, to, setFrom, setTo, mapping } = useMappingDraft({ from: 1, to: 1 })
  const [rule, setRule] = useState(patterns)
  const ruleChanged = candidate.numberedByRule && !samePatterns(cleanPatterns(rule), patterns)
  // 用同一个链接按新规则重新预览，成功后由调用方换上新的预览（重新挂载）
  const repreview = useMutation({
    mutationFn: (p: string[]) => previewSeasonBinding(season.id, link, p),
    onSuccess: (data, p) => onRepreviewed(p, data.candidates),
  })
  const create = useMutation({
    mutationFn: (m: Mapping) =>
      createSeasonBinding(season.id, {
        link,
        kind: candidate.kind,
        mappingFrom: m.from,
        mappingTo: m.to,
        episodePatterns: patterns,
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
  const busy = create.isPending || repreview.isPending

  return (
    <div className="grid gap-3">
      <div>
        <SourceLink href={candidate.sourceUrl}>{candidate.title}</SourceLink>
        <div className="text-xs text-muted-foreground">
          {candidate.sourceLabel} · 共 {candidate.items.length} 条
        </div>
        {candidate.finished && (
          <p className="mt-1 text-xs text-amber-700">
            已完结，可以关掉追更
            {settings && `：新建的季绑定开着追更，${followCostText(settings.follow)}`}。
          </p>
        )}
      </div>

      {candidate.numberedByRule && (
        <EpisodeRuleRepreview
          value={rule}
          onChange={setRule}
          changed={ruleChanged}
          disabled={busy}
          repreview={repreview}
        />
      )}

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
              text: target ? previewTargetText(target) : '—',
              className: previewTargetClass(target),
            }
          })}
        />
      )}

      <div className="flex items-center gap-2">
        <Button
          disabled={busy || mapping === null || ruleChanged}
          onClick={() => mapping && create.mutate(mapping)}
        >
          {create.isPending && <Loader2Icon className="animate-spin" />}
          {create.isPending ? `创建中 ${elapsed}s` : '创建'}
        </Button>
        <Button variant="ghost" disabled={create.isPending} onClick={onCancel}>
          取消
        </Button>
        {ruleChanged && (
          <span className="text-xs text-muted-foreground">集号规则改了，先重新预览</span>
        )}
      </div>
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </div>
  )
}
