import { useRef, useState, type ComponentProps, type MouseEvent, type UIEvent } from 'react'
import { Slider } from '@base-ui/react/slider'
import { Loader2Icon, MessageSquareTextIcon } from 'lucide-react'

import type { Binding, DanmakuItem, DanmakuMode } from '@/api/bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { InfoTip } from '@/components/InfoTip'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useBindingDanmaku } from '@/hooks/use-bindings'
import { formatDuration } from '@/lib/time'
import { cn } from '@/lib/utils'

import { parseTimestamp } from './catalog'

/** 离列表底部不到这么多像素时加载下一页 */
const LOAD_MORE_THRESHOLD = 200

/** 不是滚动的类型显示的标签 */
const modeLabels: Partial<Record<DanmakuMode, string>> = { 4: '底部', 5: '顶部', 6: '逆向' }

const WHITE = 0xffffff

/**
 * 绑定卡片上的弹幕条数（"弹幕 N 条"，有弹幕时做成按钮），点开是这个绑定保存的弹幕的表格：按弹幕源时间排序，只读，
 * 用来确认弹幕源没有绑错。打开时才取，滚到底部加载下一页，可以拖动或输入时间跳转；没有弹幕时不可点。
 */
export default function BindingDanmakuDialog({ binding }: { binding: Binding }) {
  const [open, setOpen] = useState(false)
  const count = `弹幕 ${binding.danmakuCount.toLocaleString()} 条`
  if (binding.danmakuCount === 0) return <span>{count}</span>
  return (
    <>
      <Button
        variant="outline"
        size="xs"
        className="-my-1"
        title="查看弹幕"
        onClick={() => setOpen(true)}
      >
        <MessageSquareTextIcon />
        {count}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        {/* 贴顶显示：弹幕区域高度随内容伸缩；按 dvh 限高，避免移动端软键盘遮挡 */}
        <DialogContent className="top-3 flex max-h-[calc(100dvh-1.5rem)] translate-y-0 flex-col overflow-hidden sm:top-[10vh] sm:max-h-[80dvh] sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>弹幕</DialogTitle>
            <DialogDescription className="break-all">
              {binding.title} · 共 {binding.danmakuCount.toLocaleString()} 条
            </DialogDescription>
          </DialogHeader>
          <DanmakuList binding={binding} enabled={open} />
        </DialogContent>
      </Dialog>
    </>
  )
}

/** 跳转控件和弹幕表格。拖动条的长度是绑定上最晚一条弹幕的时间 */
function DanmakuList({ binding, enabled }: { binding: Binding; enabled: boolean }) {
  const [fromMs, setFromMs] = useState<number | null>(null)
  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <JumpControls maxTimeMs={binding.maxTimeMs} fromMs={fromMs} onJump={setFromMs} />
      <DanmakuTable binding={binding} fromMs={fromMs} enabled={enabled} />
    </div>
  )
}

/**
 * 拖动条与时间输入框：拖动时输入框跟着显示时间，松手时跳转；输入时间点跳转或回车，清空后回到开头。
 * 拖动、输入中的状态留在这里，不让表格跟着每次拖动重新渲染。
 */
function JumpControls({
  maxTimeMs,
  fromMs,
  onJump,
}: {
  maxTimeMs: number
  fromMs: number | null
  onJump: (fromMs: number | null) => void
}) {
  const [text, setText] = useState('')
  // 拖动中的位置，没在拖动时为 null
  const [dragMs, setDragMs] = useState<number | null>(null)

  const jump = (ms: number | null) => {
    setDragMs(null)
    setText(ms === null ? '' : formatTime(ms))
    onJump(ms)
  }
  const typed = parseTimestamp(text)
  const empty = text.trim() === ''
  const submit = () => {
    if (empty) jump(null)
    else if (typed !== null) jump(typed)
  }
  return (
    <div className="flex flex-wrap items-center gap-2 sm:flex-nowrap sm:gap-3">
      {maxTimeMs > 0 && (
        <TimeSlider
          max={Math.ceil(maxTimeMs / 1000) * 1000}
          value={dragMs ?? fromMs ?? 0}
          dragging={dragMs !== null}
          onDrag={(value) => {
            setDragMs(value)
            setText(formatTime(value))
          }}
          // 拖回 0 即回到开头（也包括时间为负的弹幕）
          onCommit={(value) => jump(value > 0 ? value : null)}
        />
      )}
      <div className="flex shrink-0 items-center gap-1.5">
        <Input
          aria-label="跳到弹幕源时间"
          title="输入秒、m:ss 或 h:mm:ss，回车或点跳转；清空后回到开头"
          className="h-9 w-20 shrink-0 tabular-nums sm:h-7"
          placeholder="m:ss"
          aria-invalid={!empty && typed === null}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit()
          }}
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!empty && typed === null}
          onClick={submit}
        >
          跳转
        </Button>
        <InfoTip
          label="跳转格式说明"
          content="输入秒、m:ss 或 h:mm:ss，点击跳转或按回车；留空点击跳转可回到开头"
        />
      </div>
    </div>
  )
}

/** 弹幕源时间轴：每格 1 秒；鼠标悬停或触屏拖动时在上方显示对应时间 */
function TimeSlider({
  max,
  value,
  dragging,
  onDrag,
  onCommit,
}: {
  max: number
  value: number
  dragging: boolean
  onDrag: (ms: number) => void
  onCommit: (ms: number) => void
}) {
  // 悬停的位置：相对拖动条左端的像素与对应的时间；鼠标不在拖动条上时为 null
  const [hover, setHover] = useState<{ x: number; ms: number } | null>(null)
  const thumbRef = useRef<HTMLDivElement>(null)
  const onMouseMove = (e: MouseEvent<HTMLElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    // 滑块贴边对齐：0 和最大值分别在两端向内半个滑块处
    const thumb = thumbRef.current?.offsetWidth ?? 0
    const x = Math.min(Math.max(e.clientX - rect.left, 0), rect.width)
    const ratio = Math.min(Math.max((x - thumb / 2) / (rect.width - thumb), 0), 1)
    const ms = Math.round((ratio * max) / 1000) * 1000
    setHover({ x, ms })
  }
  const dragPercent = max > 0 ? Math.min(Math.max((value / max) * 100, 0), 100) : 0
  return (
    <div
      className="relative min-w-[12rem] flex-1"
      onMouseMove={onMouseMove}
      onMouseLeave={() => setHover(null)}
      onPointerCancel={() => setHover(null)}
    >
      {/* 直接用 Base UI 的原件拼，样式取自 shadcn 的 slider：只要一个滑块，读屏读出 m:ss，点击区域加大 */}
      <Slider.Root
        min={0}
        max={max}
        step={1000}
        value={value}
        thumbAlignment="edge"
        onValueChange={onDrag}
        onValueCommitted={(v) => {
          setHover(null)
          onCommit(v)
        }}
      >
        {/* 移动端加厚点击轨道，触屏拖动更稳 */}
        <Slider.Control className="relative flex w-full cursor-pointer touch-none items-center py-3 select-none md:py-2">
          <Slider.Track className="relative h-1 w-full grow overflow-hidden rounded-full bg-muted select-none">
            <Slider.Indicator className="h-full bg-primary select-none" />
          </Slider.Track>
          <Slider.Thumb
            ref={thumbRef}
            aria-label="弹幕源时间轴"
            getAriaValueText={(_, v) => formatTime(v)}
            className="relative block size-4 shrink-0 rounded-full border border-ring bg-white ring-ring/50 transition-[color,box-shadow] select-none after:absolute after:-inset-3 hover:ring-3 focus-visible:ring-3 focus-visible:outline-hidden active:ring-3 md:size-3 md:after:-inset-2"
          />
        </Slider.Control>
      </Slider.Root>
      {dragging ? (
        <span
          role="tooltip"
          className="pointer-events-none absolute bottom-full -translate-x-1/2 rounded bg-foreground px-1.5 py-0.5 text-xs text-background tabular-nums"
          style={{ left: `${dragPercent}%` }}
        >
          {formatTime(value)}
        </span>
      ) : (
        hover && (
          <span
            role="tooltip"
            className="pointer-events-none absolute bottom-full -translate-x-1/2 rounded bg-foreground px-1.5 py-0.5 text-xs text-background tabular-nums"
            style={{ left: hover.x }}
          >
            {formatTime(hover.ms)}
          </span>
        )
      )}
    </div>
  )
}

/** 弹幕源时间（毫秒）显示为 m:ss 或 h:mm:ss，不足一秒的舍去；弹幕文件里的时间可能为负 */
function formatTime(ms: number) {
  return formatDuration(Math.trunc(ms / 1000))
}

function DanmakuTable({
  binding,
  fromMs,
  enabled,
}: {
  binding: Binding
  fromMs: number | null
  enabled: boolean
}) {
  const { data, error, hasNextPage, isFetchingNextPage, isPlaceholderData, fetchNextPage } =
    useBindingDanmaku(binding.id, binding.contentVersion, fromMs, enabled)
  if (!data) {
    return (
      <TableBox>
        {error ? (
          <ErrorNote>{error.message}</ErrorNote>
        ) : (
          <p className="text-xs text-muted-foreground">加载中…</p>
        )}
      </TableBox>
    )
  }

  const loadMore = () => {
    if (hasNextPage && !isFetchingNextPage && !isPlaceholderData) void fetchNextPage()
  }
  const onScroll = (e: UIEvent<HTMLElement>) => {
    const el = e.currentTarget
    if (el.scrollHeight - el.scrollTop - el.clientHeight < LOAD_MORE_THRESHOLD) loadMore()
  }
  const items = data.pages.flatMap((p) => p.items)
  if (items.length === 0) {
    return (
      <TableBox>
        <p className="text-xs text-muted-foreground">这个时间之后没有弹幕</p>
      </TableBox>
    )
  }
  return (
    // 换了查询（跳转、内容版本变了）时滚动区域重新挂载，滚动回到顶部；新数据到达之前显示上一份（变淡）
    <TableBox
      key={`${binding.contentVersion}:${fromMs}`}
      aria-busy={isPlaceholderData}
      className={cn('overflow-y-auto transition-opacity', isPlaceholderData && 'opacity-50')}
      onScroll={onScroll}
    >
      <Table aria-label="弹幕" className="text-xs">
        <TableHeader className="sticky top-0 bg-popover">
          <TableRow>
            <TableHead className="w-16 px-1.5 sm:w-20 sm:px-2">弹幕源时间</TableHead>
            <TableHead className="w-10 px-1.5 sm:w-12 sm:px-2">类型</TableHead>
            <TableHead className="w-9 px-1.5 sm:w-12 sm:px-2">颜色</TableHead>
            <TableHead className="px-1.5 sm:px-2">内容</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((d, i) => (
            <DanmakuRow key={i} item={d} />
          ))}
        </TableBody>
      </Table>
      {hasNextPage && (
        <div className="py-1 text-center">
          {/* 列表不够高、滚不动时也能手动加载 */}
          <Button variant="ghost" size="sm" disabled={isFetchingNextPage} onClick={loadMore}>
            {isFetchingNextPage && <Loader2Icon className="animate-spin" />}
            加载更多
          </Button>
        </div>
      )}
      {/* 翻页失败时保留已加载的，提示在列表末尾 */}
      {error && <ErrorNote>{error.message}</ErrorNote>}
    </TableBox>
  )
}

/** 表格所在的区域：占满弹窗剩余高度并在区域内滚动；加载中、出错、没有弹幕的提示也放在这里 */
function TableBox({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('min-h-0 flex-1', className)} {...props} />
}

/** 一行弹幕：弹幕源时间、类型、颜色、内容；滚动的类型和白色留空，只标出不同的 */
function DanmakuRow({ item }: { item: DanmakuItem }) {
  const color = `#${item.color.toString(16).padStart(6, '0')}`
  return (
    <TableRow>
      <TableCell className="px-1.5 text-muted-foreground tabular-nums sm:px-2">
        {formatTime(item.timeMs)}
      </TableCell>
      <TableCell className="px-1.5 sm:px-2">{modeLabels[item.mode]}</TableCell>
      <TableCell className="px-1.5 sm:px-2">
        {item.color !== WHITE && (
          <span
            aria-label={`颜色 ${color}`}
            title={color}
            className="inline-block size-2.5 rounded-full ring-1 ring-foreground/20"
            style={{ backgroundColor: color }}
          />
        )}
      </TableCell>
      <TableCell className="px-1.5 whitespace-normal break-all sm:px-2">{item.text}</TableCell>
    </TableRow>
  )
}
