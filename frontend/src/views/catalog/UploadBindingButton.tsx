import { useState, type DragEvent } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Loader2Icon, UploadIcon } from 'lucide-react'
import { toast } from 'sonner'

import { createFileBinding } from '@/api/bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { useElapsed } from '@/hooks/use-elapsed'
import { useReloadSeries } from '@/hooks/use-series'
import { cn } from '@/lib/utils'

import { DANMAKU_FILE_ACCEPT } from './catalog'
import { takeFiles } from './shared'

/**
 * 集面板里上传弹幕文件创建绑定：点击选择或拖进来，可以多选，选好就上传；一次上传的几份文件合起来是一个弹幕源。
 * 不经过预览，解析出的条数在成功提示里给出。失败的提示留在下方，到下次上传或手动关闭为止；
 * 换一集时由调用方用 key 重新挂载，提示不会带到别的集。
 */
export default function UploadBindingButton({ episodeId }: { episodeId: number }) {
  const reload = useReloadSeries()
  const [dragging, setDragging] = useState(false)
  const create = useMutation({
    mutationFn: (files: File[]) => createFileBinding(episodeId, files),
    onSuccess: (binding) => {
      toast.success(`已绑定，解析出 ${binding.danmakuCount.toLocaleString()} 条弹幕`)
      // 集面板显示新的绑定
      return reload()
    },
  })
  const elapsed = useElapsed(create.isPending)

  function upload(files: File[]) {
    if (files.length > 0 && !create.isPending) create.mutate(files)
  }
  function onDrop(e: DragEvent) {
    e.preventDefault()
    setDragging(false)
    upload(Array.from(e.dataTransfer.files))
  }

  return (
    <div className="grid gap-1.5">
      <label
        className={cn(
          'flex cursor-pointer items-center justify-center gap-2 rounded-md border border-dashed px-3 py-2.5 text-sm text-muted-foreground hover:bg-muted/50',
          dragging && 'border-primary bg-muted/50',
          create.isPending && 'cursor-default opacity-70',
        )}
        onDragOver={(e) => {
          e.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={onDrop}
      >
        {create.isPending ? (
          <Loader2Icon className="size-4 animate-spin" />
        ) : (
          <UploadIcon className="size-4" />
        )}
        {create.isPending
          ? `上传中 ${elapsed}s`
          : '上传弹幕文件：点击选择或拖到这里（B 站 XML，可多选，同一个视频的几份合成一个绑定）'}
        <input
          type="file"
          multiple
          accept={DANMAKU_FILE_ACCEPT}
          className="sr-only"
          aria-label="上传弹幕文件"
          disabled={create.isPending}
          onChange={(e) => upload(takeFiles(e.target))}
        />
      </label>
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </div>
  )
}
