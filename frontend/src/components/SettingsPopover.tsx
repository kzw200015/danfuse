import { SettingsIcon } from 'lucide-react'

import type { Settings } from '@/api/settings'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { useSettings } from '@/hooks/use-settings'
import { formatSeconds } from '@/lib/time'

/** 目录源种类的显示名 */
const catalogSourceKinds: Record<string, string> = { jellyfin: 'Jellyfin' }

/** 设置：全部只读，内容少、很少看，放在顶栏右上角的弹出层里 */
export default function SettingsPopover() {
  return (
    <Popover>
      <PopoverTrigger render={<Button variant="ghost" size="icon-sm" title="设置" />}>
        <SettingsIcon />
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96">
        <PopoverHeader>
          <PopoverTitle>设置</PopoverTitle>
          <PopoverDescription>来自配置，只读；修改后重启服务生效</PopoverDescription>
        </PopoverHeader>
        <SettingsContent />
      </PopoverContent>
    </Popover>
  )
}

function SettingsContent() {
  const { data, error } = useSettings()
  if (error) return <ErrorNote>{error.message}</ErrorNote>
  if (!data) return <p className="text-xs text-muted-foreground">加载中…</p>
  return (
    <section className="grid gap-1.5">
      <h3 className="text-sm font-medium">目录源</h3>
      <CatalogSourceSettings settings={data} />
    </section>
  )
}

function CatalogSourceSettings({ settings }: { settings: Settings }) {
  const source = settings.catalogSource
  if (!source) return <p className="text-xs text-muted-foreground">未配置目录源</p>
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
      <dt className="text-muted-foreground">种类</dt>
      <dd>{catalogSourceKinds[source.kind] ?? source.kind}</dd>
      <dt className="text-muted-foreground">地址</dt>
      <dd className="font-mono break-all">{source.url}</dd>
      <dt className="text-muted-foreground">媒体库</dt>
      <dd className="flex flex-wrap gap-1">
        {source.libraries.map((name) => (
          <Badge key={name} variant="secondary">
            {name}
          </Badge>
        ))}
      </dd>
      <dt className="text-muted-foreground">定时同步</dt>
      <dd>{settings.syncInterval > 0 ? `每 ${formatSeconds(settings.syncInterval)}` : '关闭'}</dd>
      <dt className="text-muted-foreground">API key</dt>
      <dd>已配置</dd>
    </dl>
  )
}
