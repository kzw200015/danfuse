import { useMutation } from '@tanstack/react-query'
import { Loader2Icon, Trash2Icon } from 'lucide-react'
import { useNavigate } from 'react-router'
import { toast } from 'sonner'

import { ApiError } from '@/api/request'
import type { Episode, Season, SeriesDetail } from '@/api/series'
import { ConfirmButton } from '@/components/ConfirmButton'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { useReloadSeries } from '@/hooks/use-series'
import { cn } from '@/lib/utils'

import { deletionImpact } from './catalog'

/**
 * 删除剧、季或集的按钮。确认框写明一起删掉多少，以及目录源里还在时会被同步用新 ID 重新建出来。
 * 删除后 URL 退回上一级，剧列表和剧详情重新加载；失败的提示显示在按钮下方，保留到下次操作或手动关闭。
 * 调用方用剧、季或集的 ID 作 key：换了对象时重新挂载，失败的提示、进行中的删除不会带到新的对象上。
 */
export default function DeleteButton({
  label,
  name,
  target,
  remove,
  backTo,
  className,
}: {
  /** 按钮上的文字，例如"删除这部剧" */
  label: string
  /** 确认框标题和成功提示里的名称，例如"「星海旅人」""第 1 季" */
  name: string
  /** 要删除的剧、季或集，确认框里的数量由它算出 */
  target: SeriesDetail | Season | Episode
  remove: () => Promise<unknown>
  /** 删除后退回的上一级 */
  backTo: string
  className?: string
}) {
  const navigate = useNavigate()
  const reload = useReloadSeries()
  // useMutation 上的回调在按钮卸载之后（删除期间切到了别处）也会执行，提示和重新加载放在这里。
  // 不等重新加载完：mutate 上的回调（退回上一级）要等这里返回才执行，跳转应先于重新加载
  const del = useMutation({
    mutationFn: remove,
    onSuccess: () => {
      toast.success(`已删除${name}`)
      void reload()
    },
    onError: (e) => {
      // 已经不在了（例如在别处删掉了）：重新加载，页面显示"找不到"和返回上一级的链接
      if (e instanceof ApiError && e.status === 404) return reload()
    },
  })

  return (
    <div className={cn('grid justify-items-start gap-1.5', className)}>
      <ConfirmButton
        trigger={
          <Button variant="ghost" size="sm" className="text-destructive" disabled={del.isPending}>
            {del.isPending ? <Loader2Icon className="animate-spin" /> : <Trash2Icon />}
            {label}
          </Button>
        }
        title={`删除${name}？`}
        confirmLabel="删除"
        onConfirm={() =>
          del.mutate(undefined, {
            // 只在按钮还在时退回上一级，删除期间切到了别处就留在那里；
            // 替换掉已经删掉的地址，后退时不再回到"找不到"
            onSuccess: () => void navigate(backTo, { replace: true }),
          })
        }
      >
        <p>{deletionImpact(target)}</p>
        <p>
          如果它在目录源里还在，之后的同步（包括正在进行的这次）会用新 ID
          重新建出来，插件、播放器里缓存的旧 ID 会失效，需要重新搜一次。
        </p>
      </ConfirmButton>
      {del.error && <ErrorNote onClose={del.reset}>{del.error.message}</ErrorNote>}
    </div>
  )
}
