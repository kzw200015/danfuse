import { useState } from 'react'
import { CopyIcon, SettingsIcon } from 'lucide-react'
import { toast } from 'sonner'

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
import { Separator } from '@/components/ui/separator'
import { useSettings } from '@/hooks/use-settings'
import { pluginUrl } from '@/lib/plugin-url'
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
      <PopoverContent align="end" className="w-[28rem] gap-3">
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
    <>
      <section className="grid gap-1.5">
        <h3 className="text-sm font-medium">插件地址</h3>
        <PluginUrl token={data.dandanplayToken} />
      </section>
      <Separator />
      <section className="grid gap-1.5">
        <h3 className="text-sm font-medium">目录源</h3>
        <CatalogSourceSettings settings={data} />
      </section>
      <Separator />
      <section className="grid gap-1.5">
        <h3 className="text-sm font-medium">B 站</h3>
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
          <dt className="text-muted-foreground">SESSDATA</dt>
          <dd>
            {data.bilibiliSessdataConfigured
              ? '已配置'
              : '未配置，以未登录的身份拉取，弹幕可能不全'}
          </dd>
        </dl>
      </section>
    </>
  )
}

/** 填进 jellyfin-danmaku 插件的地址，按当前打开管理界面的地址拼出 */
function PluginUrl({ token }: { token: string | null }) {
  const url = pluginUrl(location.origin, token)
  const [copyFailed, setCopyFailed] = useState(false)

  async function copy() {
    setCopyFailed(false)
    try {
      // 剪贴板只在 https 或 localhost 页面可用，通过 http 访问内网地址时 navigator.clipboard 不存在
      await navigator.clipboard.writeText(url)
      toast.success('已复制插件地址')
    } catch {
      setCopyFailed(true)
    }
  }

  return (
    <div className="grid gap-1.5">
      <div className="flex items-center gap-2">
        <code className="min-w-0 flex-1 truncate rounded bg-muted px-2 py-1 font-mono text-xs select-all">
          {url}
        </code>
        <Button size="sm" variant="outline" onClick={copy}>
          <CopyIcon />
          复制
        </Button>
      </div>
      {copyFailed && (
        <ErrorNote onClose={() => setCopyFailed(false)}>
          浏览器不允许这个页面写剪贴板，请点一下地址选中后手动复制
        </ErrorNote>
      )}
      <p className="text-xs text-muted-foreground">
        插件会在后面拼 /api/v2。通过反向代理访问时，把前半段换成反代的地址。
      </p>
    </div>
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
