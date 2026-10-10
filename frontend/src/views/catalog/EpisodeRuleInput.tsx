import { useState, type ReactNode } from 'react'
import { ArrowDownIcon, ArrowUpIcon, Loader2Icon, PlusIcon, XIcon } from 'lucide-react'

import type { CollectionSeasonBinding } from '@/api/season-bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useElapsed } from '@/hooks/use-elapsed'
import { useDefaultEpisodePatterns } from '@/hooks/use-season-bindings'

/** 集号规则最多的条数，与后端相同 */
const maxPatterns = 10

/** 去掉每条前后的空白、丢掉空的：提交和比较都用它 */
export function cleanPatterns(patterns: string[]) {
  return patterns.map((p) => p.trim()).filter(Boolean)
}

export function samePatterns(a: string[], b: string[]) {
  return a.length === b.length && a.every((p, i) => p === b[i])
}

/**
 * 集号规则的编辑：一组正则（排在前面的优先），每条一行，可以上移、下移、删除，也可以新增、恢复默认。
 * 是否合法由后端判定。children 放在"新增""恢复默认"的同一行（例如"保存"）
 */
export default function EpisodeRuleInput({
  value,
  onChange,
  disabled = false,
  children,
}: {
  value: string[]
  onChange: (patterns: string[]) => void
  disabled?: boolean
  children?: ReactNode
}) {
  const defaults = useDefaultEpisodePatterns()
  const move = (i: number, to: number) => {
    const next = [...value]
    ;[next[i], next[to]] = [next[to]!, next[i]!]
    onChange(next)
  }
  return (
    <div role="group" aria-label="集号规则" className="grid gap-1 text-xs text-muted-foreground">
      <span title="从条目的标题里认集号：每一条都在标题里找，取最靠后的那个集号，位置一样时取排在前面的；都匹配不上的条目对不上、不补建。每条是一个正则，有名为 episode 的捕获组时取它，否则取第一个捕获组。默认规则认「S01E03」「第3集」「第3话」「EP3」和结尾的「/ 03」">
        集号规则（取最靠后的集号，位置一样时排在前面的优先）
      </span>
      {value.map((pattern, i) => (
        // 没有稳定的 ID，按位置作 key：输入框是受控的，移动之后内容跟着值走
        <div key={i} className="flex items-center gap-1">
          <span className="w-4 shrink-0 text-right tabular-nums">{i + 1}</span>
          <Input
            aria-label={`集号规则第 ${i + 1} 条`}
            className="h-7 min-w-0 flex-1 font-mono"
            placeholder="正则，如 第(\d+)集"
            value={pattern}
            disabled={disabled}
            onChange={(e) => onChange(value.map((p, j) => (j === i ? e.target.value : p)))}
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`上移第 ${i + 1} 条`}
            disabled={disabled || i === 0}
            onClick={() => move(i, i - 1)}
          >
            <ArrowUpIcon />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`下移第 ${i + 1} 条`}
            disabled={disabled || i === value.length - 1}
            onClick={() => move(i, i + 1)}
          >
            <ArrowDownIcon />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`删除第 ${i + 1} 条`}
            disabled={disabled || value.length === 1}
            onClick={() => onChange(value.filter((_, j) => j !== i))}
          >
            <XIcon />
          </Button>
        </div>
      ))}
      <div className="flex items-center gap-1 pl-5">
        <Button
          type="button"
          variant="ghost"
          size="xs"
          disabled={disabled || value.length >= maxPatterns}
          onClick={() => onChange([...value, ''])}
        >
          <PlusIcon />
          新增
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="xs"
          disabled={disabled || !defaults.data || samePatterns(value, defaults.data)}
          onClick={() => defaults.data && onChange(defaults.data)}
        >
          恢复默认
        </Button>
        {children}
      </div>
    </div>
  )
}

/** 季绑定卡片上集号规则的编辑，改动之后才显示"保存" */
export function EpisodeRuleEditor({
  binding,
  disabled,
  onSave,
}: {
  binding: CollectionSeasonBinding
  disabled: boolean
  onSave: (patterns: string[]) => void
}) {
  const [draft, setDraft] = useState(binding.episodePatterns)
  const patterns = cleanPatterns(draft)
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        onSave(patterns)
      }}
    >
      <EpisodeRuleInput value={draft} onChange={setDraft} disabled={disabled}>
        {!samePatterns(patterns, binding.episodePatterns) && (
          <Button
            type="submit"
            size="xs"
            disabled={disabled}
            title="条目表随即按新规则重新认集号；只影响还没处理过的条目，已经建出的绑定不动"
          >
            保存
          </Button>
        )}
      </EpisodeRuleInput>
    </form>
  )
}

/** 按新的集号规则重新预览的请求（useMutation 的结果） */
interface Repreview {
  isPending: boolean
  error: Error | null
  mutate: (patterns: string[]) => void
  reset: () => void
}

/**
 * 预览里集号规则的编辑：changed（与这次预览用的规则不同）时显示"重新预览"，提交清理过的规则；
 * 重新预览失败时在下面显示提示。季绑定的预览和按季上传的预览共用
 */
export function EpisodeRuleRepreview({
  value,
  onChange,
  changed,
  disabled,
  repreview,
}: {
  value: string[]
  onChange: (patterns: string[]) => void
  changed: boolean
  disabled: boolean
  repreview: Repreview
}) {
  const elapsed = useElapsed(repreview.isPending)
  return (
    <div className="grid gap-1.5">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          repreview.mutate(cleanPatterns(value))
        }}
      >
        <EpisodeRuleInput value={value} onChange={onChange} disabled={disabled}>
          {changed && (
            <Button type="submit" size="xs" variant="outline" disabled={disabled}>
              {repreview.isPending && <Loader2Icon className="animate-spin" />}
              {repreview.isPending ? `重新预览中 ${elapsed}s` : '重新预览'}
            </Button>
          )}
        </EpisodeRuleInput>
      </form>
      {repreview.error && (
        <ErrorNote onClose={repreview.reset}>{repreview.error.message}</ErrorNote>
      )}
    </div>
  )
}
