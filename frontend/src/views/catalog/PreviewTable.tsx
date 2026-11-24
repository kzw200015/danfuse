import type { Candidate, Item, ItemStatus } from '@/api/season-bindings'
import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

import { episodeText, previewStatusText } from './season-binding'

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

/** 一条条目的状态展示：调用方只描述一次，由 ItemTable 分别适配桌面表格与移动端双行卡片 */
export interface ItemStatusDisplay {
  text: string
  className?: string
  title?: string
}

/**
 * 条目的表格，预览表与条目表共用：序号 | 条目 | statusHead；
 * 桌面端自然展开保持原样，手机端切为限高滚动的紧凑双行卡片。
 */
export function ItemTable({
  label,
  items,
  statusHead,
  getStatus,
  removed = [],
}: {
  /** 表格的无障碍名称 */
  label: string
  items: Item[]
  statusHead: string
  getStatus: (item: Item) => ItemStatusDisplay
  /** 保存后要删的槽位，接在表尾标红划掉 */
  removed?: Candidate['removed']
}) {
  return (
    <>
      <Table aria-label={label} className="text-xs" containerClassName="hidden sm:block">
        <TableHeader>
          <TableRow>
            <TableHead className="w-12 text-right">序号</TableHead>
            <TableHead>条目</TableHead>
            <TableHead>{statusHead}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item, i) => {
            const status = getStatus(item)
            // 条目名称在一组里不重复，但后端给出的顺序就是位置，按位置作 key
            return (
              <TableRow key={i}>
                <TableCell className="text-right tabular-nums">{item.number ?? '—'}</TableCell>
                <TableCell className="whitespace-normal">{item.label}</TableCell>
                <TableCell
                  className={cn('whitespace-normal', status.className)}
                  title={status.title}
                >
                  {status.text}
                </TableCell>
              </TableRow>
            )
          })}
          {removed.map((slot) => (
            <TableRow
              key={`removed-${slot.bindingId}`}
              className="bg-destructive/5 text-destructive"
            >
              <TableCell className="text-right">—</TableCell>
              <TableCell className="whitespace-normal line-through">{slot.title}</TableCell>
              <TableCell className="whitespace-normal">
                {episodeText(slot.episodeNumber, '删除（没有条目对到这一集）')}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ul
        className="max-h-[45dvh] divide-y overflow-y-auto rounded-md border text-xs sm:hidden"
        aria-label={`${label}（移动端列表）`}
      >
        {items.map((item, i) => {
          const status = getStatus(item)
          return (
            <li key={i} className="grid gap-1 px-2.5 py-2">
              <div className="flex items-start gap-2">
                <Badge variant="secondary" className="shrink-0 tabular-nums">
                  {item.number ?? '—'}
                </Badge>
                <span className="min-w-0 flex-1 font-medium break-all">{item.label}</span>
              </div>
              <div className={cn('pl-0.5 break-words', status.className)} title={status.title}>
                {status.text}
              </div>
            </li>
          )
        })}
        {removed.map((slot) => (
          <li
            key={`removed-${slot.bindingId}`}
            className="grid gap-1 bg-destructive/5 px-2.5 py-2 text-destructive"
          >
            <div className="flex items-start gap-2">
              <Badge variant="destructive" className="shrink-0">
                删
              </Badge>
              <span className="min-w-0 flex-1 break-all line-through">{slot.title}</span>
            </div>
            <div>{episodeText(slot.episodeNumber, '删除（没有条目对到这一集）')}</div>
          </li>
        ))}
      </ul>
    </>
  )
}

/**
 * 预览表：序号 | 条目 | 保存后（对到第几集和状态，替换写出原来的绑定）。
 * 保存后要删的槽位（没有条目对到的）接在表尾，标红、划掉
 */
export default function PreviewTable({ candidate }: { candidate: Candidate }) {
  return (
    <ItemTable
      label="预览表"
      items={candidate.items}
      statusHead="保存后"
      getStatus={(item) => ({
        text: previewStatusText(item),
        className: itemStatusClass[item.status],
      })}
      removed={candidate.removed}
    />
  )
}
