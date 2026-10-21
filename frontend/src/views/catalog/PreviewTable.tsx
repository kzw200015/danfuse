import { useId, type ReactNode } from 'react'

import type { Candidate, Item, ItemStatus } from '@/api/season-bindings'
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

/**
 * 条目的表格，预览表与条目表共用：序号 | 条目 | statusHead，第三列的格子由 renderStatus 渲染；
 * children 是接在表尾的行
 */
export function ItemTable({
  label,
  items,
  statusHead,
  renderStatus,
  children,
}: {
  /** 表格的无障碍名称 */
  label: string
  items: Item[]
  statusHead: string
  renderStatus: (item: Item) => ReactNode
  children?: ReactNode
}) {
  const tableId = useId()
  return (
    <Table aria-label={label} className="text-xs">
      <TableHeader>
        <TableRow>
          <TableHead className="w-12 text-right">序号</TableHead>
          <TableHead>条目</TableHead>
          <TableHead>{statusHead}</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((item, i) => (
          // 条目名称在一组里不重复，但后端给出的顺序就是位置，按位置作 key
          <TableRow key={i} id={`${tableId}-row-${i + 1}`} tabIndex={-1}>
            <TableCell className="text-right tabular-nums">{item.number ?? '—'}</TableCell>
            <TableCell className="whitespace-normal">
              {item.label}
              {item.trace && <ItemDiagnosis item={item} tableId={tableId} row={i + 1} />}
            </TableCell>
            {renderStatus(item)}
          </TableRow>
        ))}
        {children}
      </TableBody>
    </Table>
  )
}

function ItemDiagnosis({ item, tableId, row }: { item: Item; tableId: string; row: number }) {
  const trace = item.trace!
  const locate = (target: number) => {
    const element = document.getElementById(`${tableId}-row-${target}`)
    element?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    element?.focus({ preventScroll: true })
  }
  return (
    <details className="mt-1 text-muted-foreground">
      <summary className="w-fit cursor-pointer">识别诊断</summary>
      <div className="my-2 grid gap-1 border-l-2 pl-2">
        {trace.scopeLabel && <div>匹配范围：{trace.scopeLabel}</div>}
        {trace.input && <div className="break-all">匹配标题：{trace.input}</div>}
        {trace.ruleIndex !== undefined && <div>命中规则：第 {trace.ruleIndex} 条</div>}
        {trace.captured && <div>捕获内容：{trace.captured}</div>}
        {trace.season && <div>来源季标记：{trace.season}</div>}
        <div>归一化：{trace.normalized ? '是（全角等字符已转为 NFKC）' : '否'}</div>
        {trace.rawNumber !== undefined && <div>原始集号：{trace.rawNumber}</div>}
        {trace.ruleIndex !== undefined && (
          <div>
            集号加减：{trace.adjustment >= 0 ? '+' : ''}
            {trace.adjustment}
          </div>
        )}
        {trace.adjustedNumber !== undefined && <div>调整后集号：{trace.adjustedNumber}</div>}
        {trace.localNumber !== undefined && <div>本地集号：{trace.localNumber}</div>}
        {trace.excludeIndex !== undefined && (
          <div className="break-all">
            排除规则：第 {trace.excludeIndex} 条 · <code>{trace.excludePattern}</code>
          </div>
        )}
        {item.reason && <div>原因：{item.reason}</div>}
        {trace.conflicts
          ?.filter((c) => c.row !== row)
          .map((conflict) => (
            <button
              key={conflict.row}
              type="button"
              className="w-fit text-left underline underline-offset-2"
              onClick={() => locate(conflict.row)}
            >
              定位冲突行 {conflict.row}：{conflict.label}
            </button>
          ))}
      </div>
    </details>
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
      renderStatus={(item) => (
        <TableCell className={cn('whitespace-normal', itemStatusClass[item.status])}>
          {previewStatusText(item)}
        </TableCell>
      )}
    >
      {candidate.removed.map((slot) => (
        <TableRow key={`removed-${slot.bindingId}`} className="bg-destructive/5 text-destructive">
          <TableCell className="text-right">—</TableCell>
          <TableCell className="whitespace-normal line-through">{slot.title}</TableCell>
          <TableCell className="whitespace-normal">
            {episodeText(slot.episodeNumber, '删除（没有条目对到这一集）')}
          </TableCell>
        </TableRow>
      ))}
    </ItemTable>
  )
}
