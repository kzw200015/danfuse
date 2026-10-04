import { useState, type ReactElement, type ReactNode } from 'react'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'

/**
 * 点 trigger 打开确认框，确认后关闭确认框并调用 onConfirm。
 * 操作进行中的状态和失败的提示由调用方显示在出错的位置（例如绑定卡片上），不留在确认框里。
 */
export function ConfirmButton({
  trigger,
  title,
  children,
  confirmLabel,
  onConfirm,
}: {
  trigger: ReactElement
  title: string
  /** 正文：写明这个操作的后果 */
  children: ReactNode
  confirmLabel: string
  onConfirm: () => void
}) {
  const [open, setOpen] = useState(false)
  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <AlertDialogTrigger render={trigger} />
      <AlertDialogContent className="data-[size=default]:sm:max-w-md">
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription render={<div className="grid gap-2 text-left" />}>
            {children}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>取消</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            onClick={() => {
              setOpen(false)
              onConfirm()
            }}
          >
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
