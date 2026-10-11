import { useEffect, useRef, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Loader2Icon, SearchIcon } from 'lucide-react'
import { toast } from 'sonner'

import {
  createSeasonBinding,
  previewSeasonBinding,
  previewSeasonBindingChange,
  updateSeasonBinding,
  type ChangeCandidate,
  type CollectionCandidate,
  type CollectionSeasonBinding,
  type CollectionSeasonBindingDetail,
  type Mapping,
} from '@/api/season-bindings'
import type { Season } from '@/api/series'
import { ConfirmButton } from '@/components/ConfirmButton'
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
import { EpisodeRuleRepreview } from './EpisodeRuleInput'
import MappingInputs, { useMappingDraft } from './MappingInputs'
import {
  candidateText,
  cleanPatterns,
  previewTarget,
  previewTargetClass,
  previewTargetText,
  samePatterns,
  seasonBindingPatch,
  type SeasonBindingEdit,
} from './season-binding'
import { SourceLink } from './shared'

/** 添加与编辑的不同之处：由 AddSeasonBindingForm、EditSeasonBindingForm 给出，表单的各层只经由它区分两种模式 */
interface FormMode {
  /** 表单的名称 */
  label: string
  /** 保存按钮上的动词 */
  verb: string
  /** 保存成功的提示 */
  savedText: string
  /** 链接输入框的初始值 */
  initialLink: string
  /** 打开就按 initialLink 预览一次 */
  previewOnOpen: boolean
  /** 集号对应的初始值 */
  initialMapping: Mapping
  /** 贴链接预览时用的集号规则，也是改规则的起点 */
  patterns: () => Promise<string[]>
  /** 按链接和集号规则预览 */
  fetchCandidates: (link: string, patterns: string[]) => Promise<ChangeCandidate[]>
  /** 没有改动，不能保存 */
  unchanged: (edit: SeasonBindingEdit) => boolean
  save: (edit: SeasonBindingEdit) => Promise<CollectionSeasonBindingDetail>
  /** 选中的合集已完结时的提示；不提示时没有 */
  finishedNote?: string
  /** 保存或取消后收起表单；没有时清空表单、留在原处 */
  onClose?: () => void
}

/** 显示中的预览：最近一次成功的预览（贴链接的预览，或改了集号规则之后的重新预览），重新预览进行中、失败时仍然显示 */
interface ShownPreview {
  link: string
  /** 这次预览用的集号规则 */
  patterns: string[]
  candidates: ChangeCandidate[]
  /** 每次成功的预览加一，用来重新挂载，上一次改过的对应不带过来 */
  seq: number
}

/**
 * 季面板里添加季绑定：贴链接 → 预览（后端当场识别链接、列出合集）→ 有多个候选时先选一个 → 看对应表、改集号对应
 * （投稿合集和多 P 投稿还可以改集号规则、重新预览）→ 创建。
 * 预览不保存任何东西，取消就当没发生过。点"创建"后预览收起，新的季绑定卡片出现并显示补建进度。
 * 换一季时由调用方用 key 重新挂载，输入和预览不会带到别的季。
 */
export function AddSeasonBindingForm({ season }: { season: Season }) {
  const queryClient = useQueryClient()
  const { data: settings } = useSettings()
  const mode: FormMode = {
    label: '添加季绑定',
    verb: '创建',
    savedText: '已创建季绑定，正在后台补建',
    initialLink: '',
    previewOnOpen: false,
    // 集号对应预填"合集第 1 集 = 本地第 1 集"，不按合集内容猜：接着上一季编号的番剧在对应表里一眼能看出来，手动改
    initialMapping: { from: 1, to: 1 },
    // 默认规则：第一次预览时取，之后用缓存
    patterns: () => queryClient.ensureQueryData(defaultEpisodePatternsOptions),
    fetchCandidates: async (link, patterns) =>
      (await previewSeasonBinding(season.id, link, patterns)).candidates.map(asChangeCandidate),
    unchanged: () => false,
    save: ({ link, candidate, mapping, patterns }) =>
      createSeasonBinding(season.id, {
        link,
        kind: candidate.kind,
        mappingFrom: mapping.from,
        mappingTo: mapping.to,
        episodePatterns: patterns,
      }),
    finishedNote: `已完结，可以关掉追更${settings ? `：新建的季绑定开着追更，${followCostText(settings.follow)}` : ''}。`,
  }
  return <SeasonBindingForm season={season} mode={mode} />
}

/**
 * 季绑定卡片里编辑合集的季绑定：与添加同一套流程，预填记下的链接、集号对应和集号规则，打开就预览一次并选中现在的合集。
 * 对应表里处理过的条目不再变动；换成别的合集时，它建出的、弹幕源不在新合集里的绑定会连同弹幕删除，保存前确认。
 * 只发出改了的字段，没有改动时不能保存。保存或取消后由 onClose 收起
 */
export function EditSeasonBindingForm({
  season,
  binding,
  onClose,
}: {
  season: Season
  binding: CollectionSeasonBinding
  onClose: () => void
}) {
  const patch = (edit: SeasonBindingEdit) => seasonBindingPatch(binding, edit)
  const mode: FormMode = {
    label: '编辑季绑定',
    verb: '保存',
    savedText: '已保存，正在后台补建',
    initialLink: binding.link,
    previewOnOpen: true,
    initialMapping: { from: binding.mappingFrom, to: binding.mappingTo },
    patterns: async () => binding.episodePatterns,
    fetchCandidates: async (link, patterns) =>
      (await previewSeasonBindingChange(binding.id, link, patterns)).candidates,
    unchanged: (edit) => Object.keys(patch(edit)).length === 0,
    save: (edit) => updateSeasonBinding(binding.id, patch(edit)),
    finishedNote: binding.follow ? '已完结，可以关掉追更。' : undefined,
    onClose,
  }
  return <SeasonBindingForm season={season} mode={mode} />
}

/** 创建时的预览补上编辑时才有的字段：都不是现在的合集、不删绑定、没有处理过的条目 */
function asChangeCandidate(c: CollectionCandidate): ChangeCandidate {
  return {
    ...c,
    current: false,
    removedBindings: 0,
    items: c.items.map((it) => ({ ...it, handledEpisode: null })),
  }
}

function SeasonBindingForm({ season, mode }: { season: Season; mode: FormMode }) {
  const [link, setLink] = useState(mode.initialLink)
  // 选中的候选：重新预览时保留
  const [kind, setKind] = useState<string>()
  const [shown, setShown] = useState<ShownPreview>()
  const show = (url: string, patterns: string[], candidates: ChangeCandidate[]) =>
    setShown((prev) => ({ link: url, patterns, candidates, seq: (prev?.seq ?? 0) + 1 }))
  const preview = useMutation({
    mutationFn: async (url: string) => {
      const patterns = await mode.patterns()
      return { patterns, candidates: await mode.fetchCandidates(url, patterns) }
    },
    onSuccess: ({ patterns, candidates }, url) => {
      // 编辑时默认选中现在的合集
      setKind(candidates.find((c) => c.current)?.kind)
      show(url, patterns, candidates)
    },
  })
  const elapsed = useElapsed(preview.isPending)
  const close = () => {
    setShown(undefined)
    preview.reset()
  }

  // 打开就预览一次（编辑）：只在挂载时，mode 每次渲染都是新对象，取出它的字段作依赖
  const { mutate } = preview
  const { previewOnOpen, initialLink } = mode
  const opened = useRef(false)
  useEffect(() => {
    if (previewOnOpen && !opened.current) {
      opened.current = true
      mutate(initialLink)
    }
  }, [previewOnOpen, initialLink, mutate])

  return (
    <div className="grid gap-2">
      <form
        aria-label={mode.label}
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
          {/* 预览出来之前（或预览失败时）也能收起编辑；预览出来之后用预览里的"取消" */}
          {mode.onClose && !shown && (
            <Button type="button" variant="ghost" onClick={mode.onClose}>
              取消
            </Button>
          )}
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
          mode={mode}
          link={shown.link}
          patterns={shown.patterns}
          candidates={shown.candidates}
          kind={kind}
          onKindChange={setKind}
          onRepreview={async (patterns) =>
            show(shown.link, patterns, await mode.fetchCandidates(shown.link, patterns))
          }
          onDone={() => {
            if (mode.onClose) return mode.onClose()
            setLink('')
            close()
          }}
          onCancel={mode.onClose ?? close}
        />
      )}
    </div>
  )
}

/** 预览结果：有多个候选时先选一个（例如"这个稿件的 N 个分 P"和"它所在的合集"），选中的显示对应表 */
function CandidateChooser({
  season,
  mode,
  link,
  patterns,
  candidates,
  kind,
  onKindChange,
  onRepreview,
  onDone,
  onCancel,
}: {
  season: Season
  mode: FormMode
  link: string
  /** 这次预览用的集号规则 */
  patterns: string[]
  candidates: ChangeCandidate[]
  kind: string | undefined
  onKindChange: (kind: string) => void
  /** 用同一个链接按新的集号规则重新预览，成功后显示新的预览 */
  onRepreview: (patterns: string[]) => Promise<void>
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
              <span className="block text-xs text-muted-foreground">
                {c.sourceLabel}
                {c.current && ' · 现在绑着的'}
              </span>
            </button>
          ))}
        </div>
      )}
      {selected ? (
        <CandidatePreview
          key={selected.kind}
          season={season}
          mode={mode}
          link={link}
          patterns={patterns}
          candidate={selected}
          onRepreview={onRepreview}
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
 * 一个候选合集的预览：合集信息、完结提示、集号规则（按规则编号的合集，改了之后重新预览才能保存）、可改的集号对应，
 * 以及按对应现算的对应表，按 mode 保存
 */
function CandidatePreview({
  season,
  mode,
  link,
  patterns,
  candidate,
  onRepreview,
  onDone,
  onCancel,
}: {
  season: Season
  mode: FormMode
  link: string
  patterns: string[]
  candidate: ChangeCandidate
  onRepreview: (patterns: string[]) => Promise<void>
  onDone: () => void
  onCancel: () => void
}) {
  const reload = useReloadSeries()
  const watch = useWatchSeasonBinding()
  const { from, to, setFrom, setTo, mapping } = useMappingDraft(mode.initialMapping)
  const [rule, setRule] = useState(patterns)
  const ruleChanged = candidate.numberedByRule && !samePatterns(cleanPatterns(rule), patterns)
  const repreview = useMutation({ mutationFn: onRepreview })
  const unchanged = mapping !== null && mode.unchanged({ link, candidate, mapping, patterns })
  const save = useMutation({
    mutationFn: (m: Mapping) => mode.save({ link, candidate, mapping: m, patterns }),
    onSuccess: async (detail) => {
      toast.success(mode.savedText)
      await watch(detail.id, detail)
      onDone()
      // 季面板显示新的（或改过的）季绑定卡片
      return reload()
    },
  })
  const elapsed = useElapsed(save.isPending)
  const busy = save.isPending || repreview.isPending
  const disabled = busy || mapping === null || ruleChanged || unchanged
  const submit = (onClick?: () => void) => (
    <Button disabled={disabled} onClick={onClick}>
      {save.isPending && <Loader2Icon className="animate-spin" />}
      {save.isPending ? `${mode.verb}中 ${elapsed}s` : mode.verb}
    </Button>
  )

  return (
    <div className="grid gap-3">
      <div>
        <SourceLink href={candidate.sourceUrl}>{candidate.title}</SourceLink>
        <div className="text-xs text-muted-foreground">
          {candidate.sourceLabel} · 共 {candidate.items.length} 条
        </div>
        {candidate.finished && mode.finishedNote && (
          <p className="mt-1 text-xs text-amber-700">{mode.finishedNote}</p>
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
            if (it.handledEpisode !== null) {
              return {
                number: it.number,
                label: it.label,
                text: `已处理：第 ${it.handledEpisode} 集，不再变动`,
                className: 'text-muted-foreground',
              }
            }
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
        {candidate.removedBindings > 0 ? (
          <ConfirmButton
            trigger={submit()}
            title="换成这个合集？"
            confirmLabel="换合集"
            onConfirm={() => mapping && save.mutate(mapping)}
          >
            <p>
              它建出的 {candidate.removedBindings}{' '}
              个绑定的弹幕源不在这个合集里，会连同弹幕一起删除，无法恢复。
            </p>
            <p>两个合集里都有的条目，已经建出的绑定保留。</p>
          </ConfirmButton>
        ) : (
          submit(() => mapping && save.mutate(mapping))
        )}
        <Button variant="ghost" disabled={save.isPending} onClick={onCancel}>
          取消
        </Button>
        {ruleChanged && (
          <span className="text-xs text-muted-foreground">集号规则改了，先重新预览</span>
        )}
        {unchanged && !ruleChanged && (
          <span className="text-xs text-muted-foreground">没有改动</span>
        )}
      </div>
      {save.error && <ErrorNote onClose={save.reset}>{save.error.message}</ErrorNote>}
    </div>
  )
}
