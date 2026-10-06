import { useState, type ReactNode } from 'react'
import { ArrowLeftIcon, ExternalLinkIcon, ImageOffIcon } from 'lucide-react'
import { Link } from 'react-router'

import { imageUrl } from '@/api/images'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

/**
 * 剧的海报。没有海报时显示占位图；图片加载失败时也显示占位图（例如同步换了海报、剧列表还没刷新时旧图已被删除）。
 * 旁边总有剧名，图片只作装饰，alt 为空。
 */
export function Poster({ imageId, className }: { imageId: number | null; className?: string }) {
  const [failedId, setFailedId] = useState<number | null>(null)
  const base = 'aspect-[2/3] shrink-0 rounded-md bg-muted'
  if (imageId === null || imageId === failedId) {
    return (
      <div
        className={cn(base, 'flex items-center justify-center text-muted-foreground', className)}
        title="没有海报"
      >
        <ImageOffIcon className="size-1/3" />
      </div>
    )
  }
  return (
    <img
      src={imageUrl(imageId)}
      alt=""
      loading="lazy"
      onError={() => setFailedId(imageId)}
      className={cn(base, 'object-cover', className)}
    />
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

/** 绑定、季绑定卡片上的状态：失效标红，悬停时由 deadTitle 说明失效的含义 */
export function StatusBadge({ dead, deadTitle }: { dead: boolean; deadTitle: string }) {
  return dead ? (
    <Badge variant="destructive" title={deadTitle}>
      失效
    </Badge>
  ) : (
    <Badge variant="outline" className="border-emerald-600/30 text-emerald-700">
      正常
    </Badge>
  )
}

/** 取出文件选择框里选中的文件，并清空选择框：同样的文件可以再选一次（否则不触发 change） */
export function takeFiles(input: HTMLInputElement) {
  const files = Array.from(input.files ?? [])
  input.value = ''
  return files
}

/** 弹幕源、合集的标题，在新标签页打开它在平台上的页面 */
export function SourceLink({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1 font-medium break-all hover:underline"
    >
      {children}
      <ExternalLinkIcon className="size-3 shrink-0 opacity-50" />
    </a>
  )
}
