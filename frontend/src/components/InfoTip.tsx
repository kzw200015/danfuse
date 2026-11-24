import { InfoIcon } from 'lucide-react'

import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

/**
 * 轻量点击式说明气泡：PC 端紧凑不占地，触屏端点击可查看原本藏在 title 里的规则说明。
 */
export function InfoTip({
  label,
  content,
  className,
}: {
  /** 按钮的无障碍名称 */
  label: string
  /** 弹层里的说明文字 */
  content: string
  className?: string
}) {
  return (
    <Popover>
      <PopoverTrigger
        render={
          <button
            type="button"
            aria-label={label}
            title={content}
            className={cn(
              'relative inline-flex size-4 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none coarse:after:absolute coarse:after:-inset-2.5 coarse:after:content-[""]',
              className,
            )}
          />
        }
      >
        <InfoIcon className="size-3.5" />
      </PopoverTrigger>
      <PopoverContent className="w-64 p-2.5 text-xs leading-relaxed">{content}</PopoverContent>
    </Popover>
  )
}
