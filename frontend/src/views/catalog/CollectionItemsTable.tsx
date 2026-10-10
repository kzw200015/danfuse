import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Checkbox } from '@/components/ui/checkbox'
import { cn } from '@/lib/utils'

/** 合集条目表的一行；第三栏的内容由调用方算好 */
export interface CollectionItemRow {
  /** 合集序号；对不上时为 null，显示"—" */
  number: number | null
  label: string
  /** 第三栏的文字 */
  text: string
  /** 第三栏的样式 */
  className?: string
  /** 第三栏的悬停提示 */
  title?: string
}

/** 一行的勾选框 */
export interface RowSelection {
  checked: boolean
  disabled: boolean
  onCheckedChange: (checked: boolean) => void
}

/**
 * 合集的条目，按在合集里的顺序：序号 | 条目 | 第三栏（季绑定的状态，或预览时对到本地的哪一集）。
 * 传了 selection 时最前面加一列勾选框（按季上传挑出要上传的条目），勾选框的名称为"上传 条目"
 */
export default function CollectionItemsTable<R extends CollectionItemRow>({
  label,
  heading,
  rows,
  selection,
}: {
  /** 表格的无障碍名称，例如"条目表" */
  label: string
  /** 第三栏的表头 */
  heading: string
  rows: R[]
  /** 每一行的勾选框 */
  selection?: (row: R) => RowSelection
}) {
  return (
    <Table aria-label={label} className="text-xs">
      <TableHeader>
        <TableRow>
          {selection && (
            <TableHead className="w-8">
              <span className="sr-only">上传</span>
            </TableHead>
          )}
          <TableHead className="w-12 text-right">序号</TableHead>
          <TableHead>条目</TableHead>
          <TableHead>{heading}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((row, i) => (
          <TableRow key={i}>
            {selection && <SelectionCell label={row.label} {...selection(row)} />}
            <TableCell className="text-right tabular-nums">{row.number ?? '—'}</TableCell>
            <TableCell className="whitespace-normal">{row.label}</TableCell>
            <TableCell className={cn('whitespace-normal', row.className)} title={row.title}>
              {row.text}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function SelectionCell({
  label,
  checked,
  disabled,
  onCheckedChange,
}: RowSelection & { label: string }) {
  return (
    <TableCell>
      <Checkbox
        aria-label={`上传 ${label}`}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(c) => onCheckedChange(c)}
      />
    </TableCell>
  )
}
