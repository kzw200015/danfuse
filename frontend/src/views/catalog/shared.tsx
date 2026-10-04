import type { ReactNode } from 'react'
import { ArrowLeftIcon, ImageOffIcon } from 'lucide-react'
import { Link } from 'react-router'

import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/** 剧的海报。目前没有海报数据，显示占位图 */
export function Poster({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        'flex aspect-[2/3] shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground',
        className,
      )}
      title="没有海报"
    >
      <ImageOffIcon className="size-1/3" />
    </div>
  )
}

/** 选中行的 ref：挂载时滚进可见区域，刷新页面后也能看到选中的剧或集 */
export function scrollIntoView(el: HTMLElement | null) {
  el?.scrollIntoView({ block: 'nearest' })
}

/** 栏里的提示文字：未选中、加载中、出错 */
export function Hint({ children, error = false }: { children: ReactNode; error?: boolean }) {
  return (
    <p className={cn('p-6 text-sm', error ? 'text-destructive' : 'text-muted-foreground')}>
      {children}
    </p>
  )
}

/** 地址栏里的 ID 已经不存在：提示"找不到"，并给出返回上一级的链接 */
export function NotFound({
  what,
  backTo,
  backLabel,
}: {
  what: string
  backTo: string
  backLabel: string
}) {
  return (
    <div className="grid justify-items-start gap-2 p-6 text-sm">
      <p>找不到{what}，可能已被删除。</p>
      <Link to={backTo} className={buttonVariants({ variant: 'outline', size: 'sm' })}>
        <ArrowLeftIcon />
        返回{backLabel}
      </Link>
    </div>
  )
}
