import { useState, type ReactNode } from 'react'
import { Loader2Icon } from 'lucide-react'

import type { Candidate, EpisodeRuleForm, Preview } from '@/api/season-bindings'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useElapsed } from '@/hooks/use-elapsed'
import { useDefaultEpisodePatterns } from '@/hooks/use-season-bindings'

import EpisodeRuleInput from './EpisodeRuleInput'
import MappingInputs from './MappingInputs'
import PreviewTable from './PreviewTable'
import { cleanPatterns, episodeText, parseMappingNumber, summaryTags } from './season-binding'
import { FormFrame } from './shared'

/**
 * 添加季绑定的表单的外框：先取默认的集号规则（集号规则的起点），取到后用它渲染 children。
 * 外框一直是同一个，取到之前显示加载中（或失败的提示）和取消
 */
export function AddFormFrame({
  label,
  onClose,
  children,
}: {
  label: string
  onClose: () => void
  children: (defaultPatterns: string[]) => ReactNode
}) {
  const defaults = useDefaultEpisodePatterns()
  return (
    <FormFrame label={label}>
      {defaults.data ? (
        children(defaults.data)
      ) : (
        <>
          {defaults.error ? (
            <ErrorNote>{defaults.error.message}</ErrorNote>
          ) : (
            <p className="text-xs text-muted-foreground">加载中…</p>
          )}
          <Button variant="ghost" className="w-fit" onClick={onClose}>
            取消
          </Button>
        </>
      )}
    </FormFrame>
  )
}

/**
 * 表单里的集号规则与集号对应（from、to 是两个输入框）。form 是要发出的部分（规则去掉空白和空行），
 * 集号对应不合法时为 null，这时不预览
 */
export function useRuleDraft(initial: EpisodeRuleForm) {
  const [patterns, setPatterns] = useState(initial.episodePatterns)
  const [fromText, setFromText] = useState(String(initial.mappingFrom))
  const [toText, setToText] = useState(String(initial.mappingTo))
  const mappingFrom = parseMappingNumber(fromText)
  const mappingTo = parseMappingNumber(toText)
  const form: EpisodeRuleForm | null =
    mappingFrom === null || mappingTo === null
      ? null
      : { episodePatterns: cleanPatterns(patterns), mappingFrom, mappingTo }
  return {
    patterns,
    setPatterns,
    from: { text: fromText, valid: mappingFrom !== null, onChange: setFromText },
    to: { text: toText, valid: mappingTo !== null, onChange: setToText },
    form,
  }
}

export type RuleDraft = ReturnType<typeof useRuleDraft>

/**
 * 集号规则（showPatterns 时）与集号对应的输入框。改了之后预览自动刷新，没有"重新预览"；
 * prefix 是集号对应里 X 前面的称呼（合集为"合集"，文件夹为空）
 */
export function RuleFields({
  draft,
  showPatterns,
  prefix,
  disabled,
}: {
  draft: RuleDraft
  showPatterns: boolean
  prefix: string
  disabled: boolean
}) {
  return (
    <>
      {showPatterns && (
        <EpisodeRuleInput value={draft.patterns} onChange={draft.setPatterns} disabled={disabled} />
      )}
      <MappingInputs prefix={prefix} from={draft.from} to={draft.to} disabled={disabled} />
    </>
  )
}

/**
 * 最近一次成功的预览：重新预览进行中、失败时仍然显示它（预览失败后查询不再给出上一次的结果），
 * 用户能看着它改集号规则、集号对应
 */
export function useShownPreview(preview: Preview | undefined) {
  const [shown, setShown] = useState(preview)
  if (preview && preview !== shown) setShown(preview)
  return shown
}

/** useSeasonBindingPreview 的结果 */
export interface PreviewState {
  /** 表单已经完整（能预览） */
  ready: boolean
  /** 预览是按当前的表单算的，能保存 */
  current: boolean
  error: Error | null
}

/** 保存：verb 是按钮上的动词；pending 时 progress 显示进度（上传的文件夹） */
export interface SaveAction {
  verb: string
  pending: boolean
  error: Error | null
  onSave: () => void
  onDismissError: () => void
  progress?: ReactNode
}

/**
 * 表单下半部分：预览的状态（进行中显示 waitingText）、汇总标签、预览表，以及带确认的保存和取消。
 * 保存一直能点，不比较表单改没改；只有新的预览还没出来、预览失败时不能点。
 * 有删除或替换时先弹确认框，逐条列出要删的绑定，并说明替换的集偏移归零；没有时直接保存
 */
export function PreviewSection({
  candidate,
  state,
  waitingText,
  save,
  onCancel,
}: {
  /** 要显示的预览（选中的候选，或文件夹的那一组）；还没有预览时为 undefined */
  candidate: Candidate | undefined
  state: PreviewState
  waitingText: string
  save: SaveAction
  onCancel: () => void
}) {
  const waiting = state.ready && !state.current && !state.error
  const elapsed = useElapsed(save.pending && !save.progress)
  const disabled = !state.current || !candidate || save.pending
  const button = (onClick?: () => void) => (
    <Button disabled={disabled} onClick={onClick}>
      {save.pending && <Loader2Icon className="animate-spin" />}
      {save.pending && !save.progress ? `${save.verb}中 ${elapsed}s` : save.verb}
    </Button>
  )
  const destructive = candidate && candidate.summary.remove + candidate.summary.replace > 0

  return (
    <div className="grid gap-2">
      {waiting && (
        <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          <Loader2Icon className="size-3 animate-spin" />
          {waitingText}
        </span>
      )}
      {state.error && <ErrorNote>{state.error.message}</ErrorNote>}
      {candidate && (
        <section aria-label="预览" className="grid gap-2">
          <div className="flex flex-wrap gap-1">
            {summaryTags(candidate.summary).map((tag) => (
              <Badge key={tag.text} variant={tag.destructive ? 'destructive' : 'secondary'}>
                {tag.text}
              </Badge>
            ))}
          </div>
          {candidate.items.length === 0 && candidate.removed.length === 0 ? (
            <p className="text-xs text-muted-foreground">没有条目。</p>
          ) : (
            <PreviewTable candidate={candidate} />
          )}
        </section>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {candidate && destructive ? (
          <ConfirmButton
            trigger={button()}
            title={`${save.verb}季绑定？`}
            confirmLabel={save.verb}
            onConfirm={save.onSave}
          >
            <SaveConsequences candidate={candidate} />
          </ConfirmButton>
        ) : (
          button(save.onSave)
        )}
        <Button variant="ghost" disabled={save.pending} onClick={onCancel}>
          取消
        </Button>
        {save.pending && save.progress}
      </div>
      {save.error && <ErrorNote onClose={save.onDismissError}>{save.error.message}</ErrorNote>}
    </div>
  )
}

/** 保存的确认框正文：要删的绑定（没有条目的槽位）与要替换的绑定 */
function SaveConsequences({ candidate }: { candidate: Candidate }) {
  const replaced = candidate.items.flatMap((it) =>
    it.status === 'replace' && it.replaces ? [{ item: it, slot: it.replaces }] : [],
  )
  return (
    <>
      {candidate.removed.length > 0 && (
        <div>
          <p>没有条目对到这些集，它们的绑定会连同弹幕一起删除，无法恢复：</p>
          <ul className="ml-4 list-disc">
            {candidate.removed.map((slot) => (
              <li key={slot.bindingId}>{episodeText(slot.episodeNumber, slot.title)}</li>
            ))}
          </ul>
        </div>
      )}
      {replaced.length > 0 && (
        <div>
          <p>这些绑定会删掉，换成新的弹幕源，偏移归零：</p>
          <ul className="ml-4 list-disc">
            {replaced.map(({ item, slot }) => (
              <li key={slot.bindingId}>
                {episodeText(slot.episodeNumber, slot.title)} → {item.label}
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}
