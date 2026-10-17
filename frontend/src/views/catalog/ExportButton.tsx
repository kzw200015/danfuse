import { DownloadIcon } from 'lucide-react'

import { Button, buttonVariants } from '@/components/ui/button'

/**
 * "导出 XML"：<a href download> 指向导出接口，由浏览器直接下载（见 api/export.ts）。
 * 没有可导出的内容时换成禁用的按钮：链接没有禁用状态。
 */
export default function ExportButton({
  href,
  title,
  disabled = false,
}: {
  href: string
  title: string
  disabled?: boolean
}) {
  if (disabled) {
    return (
      <Button variant="outline" size="sm" title={title} disabled>
        <DownloadIcon />
        导出 XML
      </Button>
    )
  }
  return (
    <a
      href={href}
      download
      className={buttonVariants({ variant: 'outline', size: 'sm' })}
      title={title}
    >
      <DownloadIcon />
      导出 XML
    </a>
  )
}
