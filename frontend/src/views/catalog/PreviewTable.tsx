import type { Candidate, ItemStatus } from '@/api/season-bindings'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import { previewStatusText } from './season-binding'

/** 条目状态的颜色：新建绿、替换蓝、对不上黄，其余灰。条目表与预览表共用 */
export const itemStatusClass: Record<ItemStatus, string> = {
  keep: 'text-muted-foreground',
  create: 'text-emerald-700',
  replace: 'text-sky-700',
  alreadyBound: 'text-muted-foreground',
  unmatched: 'text-amber-700',
  beforeStart: 'text-muted-foreground',
  noEpisode: 'text-muted-foreground',
}

/**
 * 预览表：序号 | 条目 | 保存后（对到第几集和状态，替换写出原来的绑定）。
 * 保存后要删的槽位（没有条目对到的）接在表尾，标红、划掉
 */
export default function PreviewTable({ candidate }: { candidate: Candidate }) {
  return (
    <Table aria-label="预览表" className="text-xs">
      <TableHeader>
        <TableRow>
          <TableHead className="w-12 text-right">序号</TableHead>
          <TableHead>条目</TableHead>
          <TableHead>保存后</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {candidate.items.map((item, i) => (
          // 条目名称在一组里不重复，但后端给出的顺序就是位置，按位置作 key
          <TableRow key={i}>
            <TableCell className="text-right tabular-nums">{item.number ?? '—'}</TableCell>
            <TableCell className="whitespace-normal">{item.label}</TableCell>
            <TableCell className={cn('whitespace-normal', itemStatusClass[item.status])}>
              {previewStatusText(item)}
            </TableCell>
          </TableRow>
        ))}
        {candidate.removed.map((slot) => (
          <TableRow key={`removed-${slot.bindingId}`} className="bg-destructive/5 text-destructive">
            <TableCell className="text-right">—</TableCell>
            <TableCell className="whitespace-normal line-through">{slot.title}</TableCell>
            <TableCell className="whitespace-normal">
              第 {slot.episodeNumber} 集 · 删除（没有条目对到这一集）
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
