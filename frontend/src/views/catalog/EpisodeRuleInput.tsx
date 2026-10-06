import { useState, type ReactNode } from 'react'
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, XIcon } from 'lucide-react'

import type { SeasonBinding } from '@/api/season-bindings'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
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
 * 集号规则的编辑：按优先级排列的一组正则，每条一行，可以上移、下移、删除，也可以新增、恢复默认。
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
      <span title="从条目的标题里认集号：按顺序逐条匹配，第一条匹配上的给出集号，都匹配不上的条目对不上、不补建。每条是一个正则，有名为 episode 的捕获组时取它，否则取第一个捕获组。默认规则认「S01E03」「第3集」「第3话」「EP3」">
        集号规则（按顺序匹配，第一条匹配上的为准）
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
  binding: SeasonBinding
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
