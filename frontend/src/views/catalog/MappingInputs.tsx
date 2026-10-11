import { Input } from '@/components/ui/input'

import { mappingHint } from './season-binding'

/** 集号对应的一个输入框：输入框里的文字、它是否合法，以及修改它 */
export interface MappingField {
  text: string
  valid: boolean
  onChange: (text: string) => void
}

/**
 * 集号对应的两个输入框："合集第 X 集 = 本地第 Y 集"（prefix 换掉"合集"，文件夹的季绑定为空："第 X 集 = 本地第 Y 集"）。
 * 不合法时标红，由调用方决定能不能提交
 */
export default function MappingInputs({
  from,
  to,
  disabled,
  prefix,
}: {
  from: MappingField
  to: MappingField
  disabled: boolean
  /** X 前面的称呼 */
  prefix: string
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
        aria-invalid={!from.valid}
        value={from.text}
        disabled={disabled}
        onChange={(e) => from.onChange(e.target.value)}
      />
      集 = 本地第
      <Input
        aria-label="本地第几集"
        className={input}
        inputMode="numeric"
        aria-invalid={!to.valid}
        value={to.text}
        disabled={disabled}
        onChange={(e) => to.onChange(e.target.value)}
      />
      集
    </span>
  )
}
