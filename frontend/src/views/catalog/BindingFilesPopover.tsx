import { useState } from 'react'

import type { Binding } from '@/api/bindings'
import { ErrorNote } from '@/components/ErrorNote'
import {
  Popover,
  PopoverContent,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useBindingFiles } from '@/hooks/use-bindings'
import { formatDateTime } from '@/lib/time'

import { formatFileSize } from './catalog'

/** 用弹幕文件建的绑定的标签（"弹幕文件 · 5 份"），点开是文件列表：文件名、大小、加入时间。打开时才取 */
export default function BindingFilesPopover({ binding }: { binding: Binding }) {
  const [open, setOpen] = useState(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={<button type="button" className="hover:underline" title="查看弹幕文件" />}
      >
        {binding.sourceLabel}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-96 gap-2">
        <PopoverHeader>
          <PopoverTitle>弹幕文件</PopoverTitle>
        </PopoverHeader>
        <FileList id={binding.id} enabled={open} />
      </PopoverContent>
    </Popover>
  )
}

function FileList({ id, enabled }: { id: number; enabled: boolean }) {
  const { data, error } = useBindingFiles(id, enabled)
  if (error) return <ErrorNote>{error.message}</ErrorNote>
  if (!data) return <p className="text-xs text-muted-foreground">加载中…</p>
  return (
    <ul aria-label="弹幕文件" className="grid max-h-80 gap-1.5 overflow-y-auto text-xs">
      {data.map((f, i) => (
        <li key={i} className="grid gap-0.5">
          <span className="break-all">{f.name}</span>
          <span className="text-muted-foreground tabular-nums">
            {formatFileSize(f.size)} · 加入于 {formatDateTime(f.uploadedAt)}
          </span>
        </li>
      ))}
    </ul>
  )
}
