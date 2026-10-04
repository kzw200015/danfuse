import type { ReactNode } from 'react'
import { TriangleAlertIcon, XIcon } from 'lucide-react'

import { cn } from '@/lib/utils'

/** 显示在出错位置的提示，保留到下次操作或手动关闭（传了 onClose 才能关闭） */
export function ErrorNote({
  children,
  onClose,
  className,
}: {
  children: ReactNode
  onClose?: () => void
  className?: string
}) {
  return (
    <div
      role="alert"
      className={cn(
        'flex items-start gap-2 rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive',
        className,
      )}
    >
      <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
      <span className="flex-1 break-words">{children}</span>
      {onClose && (
        <button
          type="button"
          className="opacity-60 hover:opacity-100"
          title="关闭"
          aria-label="关闭"
          onClick={onClose}
        >
          <XIcon className="size-3.5" />
        </button>
      )}
    </div>
  )
}
