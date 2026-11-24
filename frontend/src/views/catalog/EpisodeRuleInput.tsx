import { ArrowDownIcon, ArrowUpIcon, PlusIcon, XIcon } from 'lucide-react'

import { InfoTip } from '@/components/InfoTip'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useDefaultEpisodePatterns } from '@/hooks/use-season-bindings'

import { samePatterns } from './season-binding'

/** 集号规则最多的条数，与后端相同 */
const maxPatterns = 10

const ruleHint =
  '从条目的标题里认集号：每一条都在标题里找，取最靠后的那个集号，位置一样时取排在前面的；都匹配不上的条目对不上、不补建。每条是一个正则，有名为 episode 的捕获组时取它，否则取第一个捕获组。默认规则认「S01E03」「第3集」「第3话」「EP3」和结尾的「/ 03」'

/**
 * 集号规则的编辑：一组正则（排在前面的优先），每条一行，可以上移、下移、删除，也可以新增、恢复默认。
 * 是否合法由后端判定，改了之后预览自动刷新
 */
export default function EpisodeRuleInput({
  value,
  onChange,
  disabled = false,
}: {
  value: string[]
  onChange: (patterns: string[]) => void
  disabled?: boolean
}) {
  const defaults = useDefaultEpisodePatterns()
  const move = (i: number, to: number) => {
    const next = [...value]
    ;[next[i], next[to]] = [next[to]!, next[i]!]
    onChange(next)
  }
  return (
    <div role="group" aria-label="集号规则" className="grid gap-1.5 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <span title={ruleHint}>集号规则（取最靠后的集号，位置一样时排在前面的优先）</span>
        <InfoTip label="集号规则说明" content={ruleHint} />
      </div>
      {value.map((pattern, i) => (
        // 没有稳定的 ID，按位置作 key：输入框是受控的，移动之后内容跟着值走；
        // 手机端拆成两行微卡片并隔离删除按钮，避免拇指误删手写正则
        <div
          key={i}
          className="flex flex-col gap-2 rounded-md border bg-background p-2 sm:flex-row sm:items-center sm:gap-1.5 sm:rounded-none sm:border-0 sm:bg-transparent sm:p-0"
        >
          <span className="hidden w-4 shrink-0 text-right tabular-nums sm:inline">{i + 1}</span>
          <Input
            aria-label={`集号规则第 ${i + 1} 条`}
            className="h-9 min-w-0 flex-1 font-mono sm:h-7"
            placeholder="正则，如 第(\d+)集"
            value={pattern}
            disabled={disabled}
            onChange={(e) => onChange(value.map((p, j) => (j === i ? e.target.value : p)))}
          />
          <div className="flex items-center justify-between gap-2 sm:contents">
            <span className="text-xs tabular-nums sm:hidden">规则 #{i + 1}</span>
            <div className="flex items-center gap-3 sm:gap-1">
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
                className="text-destructive sm:text-inherit"
                aria-label={`删除第 ${i + 1} 条`}
                disabled={disabled || value.length === 1}
                onClick={() => onChange(value.filter((_, j) => j !== i))}
              >
                <XIcon />
              </Button>
            </div>
          </div>
        </div>
      ))}
      <div className="flex items-center gap-1 sm:pl-5">
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
      </div>
    </div>
  )
}
