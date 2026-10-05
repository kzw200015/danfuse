import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { cn } from '@/lib/utils'

/** 合集条目表的一行；第三栏的内容由调用方算好 */
export interface CollectionItemRow {
  /** 合集序号；对不上时为 null，显示"—" */
  number: number | null
  label: string
  note: string | null
  /** 第三栏的文字 */
  text: string
  /** 第三栏的样式 */
  className?: string
  /** 第三栏的悬停提示 */
  title?: string
}

/** 合集的条目，按在合集里的顺序：序号 | 条目（标题与备注）| 第三栏（季绑定的状态，或预览时对到本地的哪一集） */
export default function CollectionItemsTable({
  label,
  heading,
  rows,
}: {
  /** 表格的无障碍名称，例如"条目表" */
  label: string
  /** 第三栏的表头 */
  heading: string
  rows: CollectionItemRow[]
}) {
  return (
    <Table aria-label={label} className="text-xs">
      <TableHeader>
        <TableRow>
          <TableHead className="w-12 text-right">序号</TableHead>
          <TableHead>条目</TableHead>
          <TableHead>{heading}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row, i) => (
          <TableRow key={i}>
            <TableCell className="text-right tabular-nums">{row.number ?? '—'}</TableCell>
            <TableCell className="whitespace-normal">
              {row.label}
              {row.note && <div className="text-muted-foreground">{row.note}</div>}
            </TableCell>
            <TableCell className={cn('whitespace-normal', row.className)} title={row.title}>
              {row.text}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
