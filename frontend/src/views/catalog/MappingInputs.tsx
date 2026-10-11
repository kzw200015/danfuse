import { useState } from 'react'

import type { Mapping } from '@/api/season-bindings'
import { Input } from '@/components/ui/input'

import { mappingHint, parseMappingNumber } from './season-binding'

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
      title={mappingHint(prefix)}
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
