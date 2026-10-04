import { SettingsIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'

/** 设置：全部只读，内容少、很少看，放在顶栏右上角的弹出层里 */
export default function SettingsPopover() {
  return (
    <Popover>
      <PopoverTrigger render={<Button variant="ghost" size="icon-sm" title="设置" />}>
        <SettingsIcon />
      </PopoverTrigger>
      <PopoverContent align="end">
        <PopoverHeader>
          <PopoverTitle>设置</PopoverTitle>
          <PopoverDescription>暂无内容</PopoverDescription>
        </PopoverHeader>
      </PopoverContent>
    </Popover>
  )
}
