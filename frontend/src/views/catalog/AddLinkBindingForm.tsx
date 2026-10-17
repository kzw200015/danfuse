import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { LinkIcon, Loader2Icon } from 'lucide-react'
import { toast } from 'sonner'

import { createBinding } from '@/api/bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useElapsed } from '@/hooks/use-elapsed'
import { useReloadSeries } from '@/hooks/use-series'

import { FormFrame } from './shared'

/**
 * 集面板上"绑定链接"展开的表单：贴链接创建绑定。后端当场拉取全部弹幕，进行中显示已用秒数，不能取消；
 * 成功后收起，失败的提示留在输入框下方，到下次提交或手动关闭为止
 */
export default function AddLinkBindingForm({
  episodeId,
  onClose,
}: {
  episodeId: number
  onClose: () => void
}) {
  const reload = useReloadSeries()
  const [link, setLink] = useState('')
  const create = useMutation({
    mutationFn: (url: string) => createBinding(episodeId, url),
    onSuccess: (binding) => {
      toast.success(`已绑定，拉取到 ${binding.danmakuCount.toLocaleString()} 条弹幕`)
      onClose()
      // 集面板显示新的绑定
      return reload()
    },
  })
  const elapsed = useElapsed(create.isPending)

  return (
    <FormFrame label="绑定链接">
      <form
        className="grid gap-1.5"
        onSubmit={(e) => {
          e.preventDefault()
          create.mutate(link.trim())
        }}
      >
        <div className="flex gap-2">
          <Input
            aria-label="弹幕源链接"
            placeholder="粘贴 B 站投稿、番剧单集链接或 b23.tv 短链，或 BV / av / ep 号"
            value={link}
            disabled={create.isPending}
            onChange={(e) => setLink(e.target.value)}
          />
          <Button type="submit" disabled={create.isPending || !link.trim()}>
            {create.isPending ? <Loader2Icon className="animate-spin" /> : <LinkIcon />}
            {create.isPending ? `拉取中 ${elapsed}s` : '绑定'}
          </Button>
          <Button type="button" variant="ghost" disabled={create.isPending} onClick={onClose}>
            取消
          </Button>
        </div>
        {create.isPending && (
          <p className="text-xs text-muted-foreground">正在解析链接并拉取全部弹幕，最长约 25 秒…</p>
        )}
        {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
      </form>
    </FormFrame>
  )
}
