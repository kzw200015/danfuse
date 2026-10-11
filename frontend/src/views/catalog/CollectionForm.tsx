import { useState } from 'react'
import { useMutation, type QueryKey } from '@tanstack/react-query'
import { SearchIcon } from 'lucide-react'
import { toast } from 'sonner'

import {
  createCollection,
  previewCollectionEdit,
  previewNewCollection,
  saveCollection,
  type Candidate,
  type CollectionForm,
  type CollectionPreviewForm,
  type CollectionSeasonBinding,
  type CollectionSeasonBindingDetail,
  type EpisodeRuleForm,
  type Preview,
} from '@/api/season-bindings'
import type { Season } from '@/api/series'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useSeasonBindingPreview, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { useReloadSeries } from '@/hooks/use-series'
import { useSettings } from '@/hooks/use-settings'
import { followCostText } from '@/lib/follow'
import { cn } from '@/lib/utils'

import { candidateText } from './season-binding'
import {
  AddFormFrame,
  FormFrame,
  PreviewSection,
  RuleFields,
  useRuleDraft,
  useShownPreview,
} from './SeasonBindingFormParts'
import { SourceLink } from './shared'

/** 添加与编辑合集的季绑定的不同之处，由 AddCollectionForm、EditCollectionForm 给出；表单只经由它区分两种模式 */
export interface CollectionFormMode {
  /** 表单的标题和无障碍名称 */
  label: string
  /** 保存按钮上的动词 */
  verb: string
  /** 保存成功的提示 */
  savedText: string
  /** 链接的初始值；非空时打开就按它预览 */
  initialLink: string
  /** 集号规则、集号对应的初始值 */
  initialRules: EpisodeRuleForm
  /** 预览的查询键前缀 */
  previewKey: QueryKey
  preview: (form: CollectionPreviewForm) => Promise<Preview>
  save: (form: CollectionForm) => Promise<CollectionSeasonBindingDetail>
  /** 选中的合集已完结时的提示；不提示时没有 */
  finishedNote?: string
  /** 保存成功或取消后收起表单 */
  onClose: () => void
}

/** 合集预览里正在等的提示：后端要识别链接、请求平台列出合集 */
const waitingText = '正在列出合集，最长约 25 秒…'

/**
 * 季面板上"绑定合集"展开的表单：贴链接预览（每次都请求平台），有多个候选时先选一个，按集号规则（按规则编号的合集）、
 * 集号对应看保存后的结果，创建后季绑定在后台补建。集号规则、集号对应预填默认规则和 1 = 1，不按合集内容猜
 */
export function AddCollectionForm({ season, onClose }: { season: Season; onClose: () => void }) {
  const { data: settings } = useSettings()
  const mode = (patterns: string[]): CollectionFormMode => ({
    label: '绑定合集',
    verb: '创建',
    savedText: '已创建季绑定，正在后台补建',
    initialLink: '',
    initialRules: { episodePatterns: patterns, mappingFrom: 1, mappingTo: 1 },
    previewKey: ['season-binding-preview', 'new-collection', season.id],
    preview: (form) => previewNewCollection(season.id, form),
    save: (form) => createCollection(season.id, form),
    finishedNote: `已完结，可以关掉追更${settings ? `：新建的季绑定开着追更，${followCostText(settings.follow)}` : ''}。`,
    onClose,
  })
  return (
    <AddFormFrame label="绑定合集" onClose={onClose}>
      {(patterns) => <CollectionFormBody mode={mode(patterns)} />}
    </AddFormFrame>
  )
}

/**
 * 合集的季绑定卡片里的编辑：预填保存的链接、集号规则、集号对应，打开就预览并选中保存的候选。
 * 没改表单也能保存（保存时删掉没有条目的槽位）
 */
export function EditCollectionForm({
  binding,
  onClose,
}: {
  binding: CollectionSeasonBinding
  onClose: () => void
}) {
  const mode: CollectionFormMode = {
    label: '编辑季绑定',
    verb: '保存',
    savedText: '已保存，正在后台补建',
    initialLink: binding.link,
    initialRules: {
      episodePatterns: binding.episodePatterns,
      mappingFrom: binding.mappingFrom,
      mappingTo: binding.mappingTo,
    },
    previewKey: ['season-binding-preview', 'collection', binding.id],
    preview: (form) => previewCollectionEdit(binding.id, form),
    save: (form) => saveCollection(binding.id, form),
    finishedNote: binding.follow ? '已完结，可以关掉追更。' : undefined,
    onClose,
  }
  return (
    <FormFrame label={mode.label}>
      <CollectionFormBody mode={mode} />
    </FormFrame>
  )
}

/**
 * 表单从上到下：链接与候选、集号规则（按规则编号的合集才有）、集号对应、预览、保存。
 * 链接点"预览"才提交；集号规则、集号对应改了之后约 0.6 秒自动重新预览
 */
function CollectionFormBody({ mode }: { mode: CollectionFormMode }) {
  const [link, setLink] = useState(mode.initialLink)
  // 提交预览的链接
  const [submitted, setSubmitted] = useState(mode.initialLink.trim())
  // 选中的候选；没选时用保存的候选（编辑），只有一个候选时就是它
  const [kind, setKind] = useState<string | null>(null)
  const rules = useRuleDraft(mode.initialRules)
  const input: CollectionPreviewForm | null =
    submitted && rules.form ? { link: submitted, ...rules.form } : null
  const result = useSeasonBindingPreview(mode.previewKey, mode.preview, input)
  const shown = useShownPreview(result.preview)
  const candidates = shown?.candidates ?? []
  const selected = selectCandidate(candidates, kind)

  const watch = useWatchSeasonBinding()
  const reload = useReloadSeries()
  const save = useMutation({
    mutationFn: mode.save,
    onSuccess: async (detail) => {
      toast.success(mode.savedText)
      await watch(detail)
      mode.onClose()
      // 季面板显示新的（或改过的）季绑定卡片；保存时删掉了没有条目的槽位，各集的绑定也要刷新
      return reload()
    },
  })

  return (
    <>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault()
          setKind(null)
          setSubmitted(link.trim())
        }}
      >
        <Input
          aria-label="合集的链接"
          placeholder="粘贴 B 站番剧（ss、md、ep 链接）、空间里的合集页、多 P 投稿的链接或 b23.tv 短链"
          value={link}
          disabled={save.isPending}
          onChange={(e) => setLink(e.target.value)}
        />
        <Button type="submit" variant="outline" disabled={save.isPending || !link.trim()}>
          <SearchIcon />
          预览
        </Button>
      </form>

      {candidates.length > 1 && (
        <div role="radiogroup" aria-label="选择要绑定的合集" className="grid gap-1.5">
          <p className="text-xs text-muted-foreground">这个链接对应多个合集，选一个：</p>
          {candidates.map((c) => (
            <button
              key={c.collectionKind}
              type="button"
              role="radio"
              aria-checked={c === selected}
              disabled={save.isPending}
              className={cn(
                'rounded-md border px-3 py-2 text-left text-sm hover:bg-muted',
                c === selected && 'border-primary bg-muted',
              )}
              onClick={() => setKind(c.collectionKind)}
            >
              {candidateText(c)}
              <span className="block text-xs text-muted-foreground">
                {c.sourceLabel}
                {c.current && ' · 现在保存的'}
              </span>
            </button>
          ))}
        </div>
      )}

      {selected && (
        <div>
          {selected.sourceUrl ? (
            <SourceLink href={selected.sourceUrl}>{selected.title}</SourceLink>
          ) : (
            <span className="font-medium">{selected.title}</span>
          )}
          <div className="text-xs text-muted-foreground">
            {selected.sourceLabel} · 共 {selected.items.length} 条
          </div>
          {selected.finished && mode.finishedNote && (
            <p className="mt-1 text-xs text-amber-700">{mode.finishedNote}</p>
          )}
        </div>
      )}

      {shown && (
        <RuleFields
          draft={rules}
          // 番剧的集号由平台给出，只有按规则编号的合集要集号规则
          showPatterns={selected?.numberedByRule ?? false}
          prefix="合集"
          disabled={save.isPending}
        />
      )}

      <PreviewSection
        candidate={selected}
        state={{ ready: input !== null, current: result.current, error: result.error }}
        waitingText={waitingText}
        save={{
          verb: mode.verb,
          pending: save.isPending,
          error: save.error,
          onSave: () =>
            input &&
            selected &&
            save.mutate({ ...input, collectionKind: selected.collectionKind ?? undefined }),
          onDismissError: save.reset,
        }}
        onCancel={mode.onClose}
      />
    </>
  )
}

/** 选中的候选：只有一个时就是它；否则是用户选的，没选时是保存的候选（编辑），都没有时为 undefined */
function selectCandidate(candidates: Candidate[], kind: string | null) {
  if (candidates.length === 1) return candidates[0]
  return candidates.find((c) => c.collectionKind === kind) ?? candidates.find((c) => c.current)
}
