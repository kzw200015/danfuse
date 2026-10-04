import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { LinkIcon, Loader2Icon } from 'lucide-react'
import { toast } from 'sonner'

import { createBinding } from '@/api/bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useElapsed } from '@/hooks/use-elapsed'
import { seriesKeys } from '@/hooks/use-series'

/**
 * 集面板里贴链接创建绑定。后端当场拉取全部弹幕，进行中显示已用秒数；
 * 失败的提示留在输入框下方，到下次提交或手动关闭为止。换一集时由调用方用 key 重新挂载，提示不会带到别的集。
 */
export default function AddBindingForm({ episodeId }: { episodeId: number }) {
  const queryClient = useQueryClient()
  const [link, setLink] = useState('')
  const create = useMutation({
    mutationFn: (url: string) => createBinding(episodeId, url),
    onSuccess: (binding) => {
      toast.success(`已绑定，拉取到 ${binding.danmakuCount.toLocaleString()} 条弹幕`)
      setLink('')
      // 剧详情的键以剧列表的键为前缀，一起刷新：集面板显示新的绑定，各处的绑定统计随之更新
      return queryClient.invalidateQueries({ queryKey: seriesKeys.list })
    },
  })
  const elapsed = useElapsed(create.isPending)

  return (
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
      </div>
      {create.isPending && (
        <p className="text-xs text-muted-foreground">正在解析链接并拉取全部弹幕，最长约 25 秒…</p>
      )}
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </form>
  )
}
