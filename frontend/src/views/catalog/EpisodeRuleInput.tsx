import { useState } from 'react'

import type { SeasonBinding } from '@/api/season-bindings'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

/** 集号规则的输入框：留空用内置规则，否则是正则，第一个捕获组为集号。是否合法由后端判定 */
export default function EpisodeRuleInput({
  value,
  onChange,
  disabled = false,
}: {
  value: string
  onChange: (text: string) => void
  disabled?: boolean
}) {
  return (
    <span
      className="flex items-center gap-1 text-xs text-muted-foreground"
      title="从条目的标题里认集号，认不出的条目对不上、不补建。留空用内置规则：认「第3集」「第3话」「EP3」，整个合集都没有这种写法时认标题末尾的数字；也可以填一个正则，第一个捕获组为集号"
    >
      集号规则
      <Input
        aria-label="集号规则"
        className="h-7 w-48 font-mono"
        placeholder="内置规则，或如 第(\d+)集"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      />
    </span>
  )
}

/** 季绑定卡片上集号规则的输入框，改动之后才显示"保存" */
export function EpisodeRuleEditor({
  binding,
  disabled,
  onSave,
}: {
  binding: SeasonBinding
  disabled: boolean
  onSave: (pattern: string) => void
}) {
  const [draft, setDraft] = useState(binding.episodePattern)
  const pattern = draft.trim()
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        onSave(pattern)
      }}
    >
      <EpisodeRuleInput value={draft} onChange={setDraft} disabled={disabled} />
      {pattern !== binding.episodePattern && (
        <Button
          type="submit"
          size="xs"
          disabled={disabled}
          title="条目表随即按新规则重新认集号；只影响还没处理过的条目，已经建出的绑定不动"
        >
          保存
        </Button>
      )}
    </form>
  )
}
