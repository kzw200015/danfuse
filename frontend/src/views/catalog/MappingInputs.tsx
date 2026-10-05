import { Input } from '@/components/ui/input'

import { parseMappingNumber } from './season-binding'

/** 集号对应的两个输入框："合集第 X 集 = 本地第 Y 集"。不合法时标红，由调用方决定能不能提交 */
export default function MappingInputs({
  from,
  to,
  onFromChange,
  onToChange,
  disabled = false,
}: {
  from: string
  to: string
  onFromChange: (text: string) => void
  onToChange: (text: string) => void
  disabled?: boolean
}) {
  const input = 'h-7 w-16 text-right tabular-nums'
  return (
    <span
      className="flex items-center gap-1 text-xs text-muted-foreground"
      title="合集第 X 集对应本地第 Y 集，之后一一顺延；序号小于 X 的条目不参与"
    >
      合集第
      <Input
        aria-label="合集第几集"
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
