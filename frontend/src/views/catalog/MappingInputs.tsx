import { useState } from 'react'

import type { Mapping, SeasonBinding } from '@/api/season-bindings'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { parseMappingNumber } from './season-binding'

/** 集号对应输入框里的文字，以及解析出的对应；任一个不合法时 mapping 为 null */
export function useMappingDraft(initial: Mapping) {
  const [from, setFrom] = useState(String(initial.from))
  const [to, setTo] = useState(String(initial.to))
  const f = parseMappingNumber(from)
  const t = parseMappingNumber(to)
  const mapping: Mapping | null = f === null || t === null ? null : { from: f, to: t }
  return { from, to, setFrom, setTo, mapping }
}

/**
 * 集号对应的两个输入框："合集第 X 集 = 本地第 Y 集"（prefix 换掉"合集"，按季上传时为空："第 X 集 = 本地第 Y 集"）。
 * 不合法时标红，由调用方决定能不能提交
 */
export default function MappingInputs({
  from,
  to,
  onFromChange,
  onToChange,
  disabled = false,
  prefix = '合集',
}: {
  from: string
  to: string
  onFromChange: (text: string) => void
  onToChange: (text: string) => void
  disabled?: boolean
  /** X 前面的称呼 */
  prefix?: string
}) {
  const input = 'h-7 w-12 text-right tabular-nums'
  return (
    <span
      className="flex items-center gap-1 text-xs text-muted-foreground"
      title={`${prefix}第 X 集对应本地第 Y 集，之后一一顺延；序号小于 X 的条目不参与`}
    >
      {prefix}第
      <Input
        aria-label={`${prefix}第几集`}
        className={input}
        inputMode="numeric"
        aria-invalid={parseMappingNumber(from) === null}
        value={from}
        disabled={disabled}
        onChange={(e) => onFromChange(e.target.value)}
      />
      集 = 本地第
      <Input
        aria-label="本地第几集"
        className={input}
        inputMode="numeric"
        aria-invalid={parseMappingNumber(to) === null}
        value={to}
        disabled={disabled}
        onChange={(e) => onToChange(e.target.value)}
      />
      集
    </span>
  )
}

/** 季绑定卡片上集号对应的输入框，改动之后才显示"保存"；不合法时不能保存 */
export function MappingEditor({
  binding,
  disabled,
  onSave,
}: {
  binding: SeasonBinding
  disabled: boolean
  onSave: (mapping: Mapping) => void
}) {
  const { from, to, setFrom, setTo, mapping } = useMappingDraft({
    from: binding.mappingFrom,
    to: binding.mappingTo,
  })
  const changed =
    mapping === null || mapping.from !== binding.mappingFrom || mapping.to !== binding.mappingTo
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault()
        if (mapping) onSave(mapping)
      }}
    >
      <MappingInputs
        from={from}
        to={to}
        onFromChange={setFrom}
        onToChange={setTo}
        disabled={disabled}
      />
      {changed && (
        <Button
          type="submit"
          size="xs"
          disabled={disabled || mapping === null}
          title="只影响还没处理过的条目，已经建出的绑定不动"
        >
          保存
        </Button>
      )}
    </form>
  )
}
