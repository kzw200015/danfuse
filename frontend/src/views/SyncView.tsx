import { useMutation } from '@tanstack/react-query'
import { Loader2Icon } from 'lucide-react'
import { useSearchParams } from 'react-router'
import { toast } from 'sonner'

import { triggerSync, type SyncRun, type SyncRunDetail } from '@/api/sync'
import { ErrorNote } from '@/components/ErrorNote'
import { RunStatusIcon } from '@/components/SyncStatus'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useSettings } from '@/hooks/use-settings'
import { useReloadSyncRuns, useSyncRun, useSyncRuns } from '@/hooks/use-sync-runs'
import {
  progressValue,
  runProgressText,
  runStatusText,
  scheduleText,
  triggerText,
} from '@/lib/sync'
import { parseId } from '@/lib/route'
import { formatAgo, formatDateTime, formatSeconds, secondsBetween } from '@/lib/time'
import { cn } from '@/lib/utils'

export default function SyncView() {
  const [searchParams, setSearchParams] = useSearchParams()
  const runs = useSyncRuns()
  const latest = runs.data?.[0]
  // ?run=:id 选中某一次同步，没有时看最近一次；不是合法的 ID 时不发请求，与后端 404 一样提示不存在
  const runParam = searchParams.get('run')
  const selectedId = runParam === null ? latest?.id : parseId(runParam)

  return (
    <div className="h-full overflow-y-auto">
      <div className="grid gap-5 p-5">
        <SyncHeader latest={latest} />
        {runs.error && <ErrorNote>{runs.error.message}</ErrorNote>}
        {runs.data && (
          <SyncRunTable
            runs={runs.data}
            selectedId={selectedId}
            onSelect={(id) => setSearchParams({ run: String(id) })}
          />
        )}
        {selectedId !== undefined ? (
          <SyncRunPanel id={selectedId} />
        ) : (
          runParam !== null && <ErrorNote>同步记录不存在</ErrorNote>
        )}
      </div>
    </div>
  )
}

/** 标题行：定时间隔、最近一次同步的开始时间、"立即同步" */
function SyncHeader({ latest }: { latest: SyncRun | undefined }) {
  const reloadSyncRuns = useReloadSyncRuns()
  const { data: settings } = useSettings()
  const unconfigured = settings?.catalogSource === null
  const trigger = useMutation({
    mutationFn: triggerSync,
    onSuccess: ({ id }) => {
      toast.success(`已开始同步 #${id}`)
      return reloadSyncRuns()
    },
  })
  const running = latest?.status === 'running'

  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h1 className="text-lg font-semibold">同步</h1>
        {settings && (
          <span className={cn('text-sm text-muted-foreground', unconfigured && 'text-amber-700')}>
            {scheduleText(settings)}
            {latest && `，最近一次开始于 ${formatAgo(latest.startedAt)}`}
          </span>
        )}
        <Button
          size="sm"
          className="ml-auto"
          disabled={unconfigured || trigger.isPending || running}
          onClick={() => trigger.mutate()}
        >
          {running && <Loader2Icon className="animate-spin" />}
          {running ? '同步中…' : '立即同步'}
        </Button>
      </div>
      {trigger.error && <ErrorNote onClose={trigger.reset}>{trigger.error.message}</ErrorNote>}
    </div>
  )
}

function SyncRunTable({
  runs,
  selectedId,
  onSelect,
}: {
  runs: SyncRun[]
  selectedId: number | undefined
  onSelect: (id: number) => void
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="text-xs hover:bg-transparent">
          <TableHead>编号</TableHead>
          <TableHead>触发方式</TableHead>
          <TableHead>状态</TableHead>
          <TableHead>开始时间</TableHead>
          <TableHead>进度</TableHead>
          <TableHead>新增 剧 / 季 / 集</TableHead>
          <TableHead>警告</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.length === 0 && (
          <TableRow className="hover:bg-transparent">
            <TableCell colSpan={7} className="py-6 text-center text-muted-foreground">
              还没有同步过
            </TableCell>
          </TableRow>
        )}
        {runs.map((run) => (
          <TableRow
            key={run.id}
            data-state={run.id === selectedId ? 'selected' : undefined}
            className="cursor-pointer"
            onClick={() => onSelect(run.id)}
          >
            <TableCell className="tabular-nums">#{run.id}</TableCell>
            <TableCell>{triggerText[run.trigger]}</TableCell>
            <TableCell>
              <span className="inline-flex items-center gap-1">
                <RunStatusIcon status={run.status} className="size-3.5" />
                {runStatusText[run.status]}
              </span>
            </TableCell>
            <TableCell className="tabular-nums">{formatDateTime(run.startedAt)}</TableCell>
            <TableCell className="tabular-nums">{runProgressText(run)}</TableCell>
            <TableCell className="tabular-nums">
              {run.createdSeries} / {run.createdSeasons} / {run.createdEpisodes}
            </TableCell>
            <TableCell className={cn('tabular-nums', run.warningCount > 0 && 'text-amber-700')}>
              {run.warningCount}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

/** 选中的那次同步：左边是状态与进度，右边是警告 */
function SyncRunPanel({ id }: { id: number }) {
  const { data: run, error } = useSyncRun(id)
  if (error) return <ErrorNote>{error.message}</ErrorNote>
  if (!run) return null
  return (
    <div className="grid items-start gap-4 md:grid-cols-2">
      <Card size="sm">
        <CardHeader>
          <CardTitle>同步 #{run.id}</CardTitle>
        </CardHeader>
        <CardContent>
          <SyncRunSummary run={run} />
        </CardContent>
      </Card>
      <Card size="sm">
        <CardHeader>
          <CardTitle>警告</CardTitle>
          <CardDescription>多是目录源里的命名问题，修好后重新同步</CardDescription>
        </CardHeader>
        <CardContent>
          <WarningList run={run} />
        </CardContent>
      </Card>
    </div>
  )
}

function SyncRunSummary({ run }: { run: SyncRunDetail }) {
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <RunStatusIcon status={run.status} />
        <span className="font-medium">{runStatusText[run.status]}</span>
        <span className="text-xs text-muted-foreground">
          {triggerText[run.trigger]}触发 · {formatDateTime(run.startedAt)} 开始
          {run.finishedAt &&
            `，用时 ${formatSeconds(secondsBetween(run.startedAt, run.finishedAt))}`}
        </span>
      </div>
      <Progress value={progressValue(run)} aria-label="同步进度" />
      <div className="flex flex-wrap justify-between gap-x-4 text-xs text-muted-foreground">
        <span>剧（含电影）{runProgressText(run)}</span>
        <span>
          新增 剧 {run.createdSeries} · 季 {run.createdSeasons} · 集 {run.createdEpisodes}
        </span>
      </div>
      {run.status === 'interrupted' && (
        <p className="text-xs text-amber-700">
          同步被服务关闭或崩溃打断。已提交的部分保留，重新同步即可补齐。
        </p>
      )}
      {run.error && <ErrorNote>失败原因：{run.error}</ErrorNote>}
    </div>
  )
}

/** 警告：后端最多保存 200 条，另记总数 */
function WarningList({ run }: { run: SyncRunDetail }) {
  if (run.warningCount === 0) return <p className="text-xs text-muted-foreground">没有警告</p>
  return (
    <div className="grid gap-1.5">
      <p className="text-xs font-medium text-amber-700">
        共 {run.warningCount} 条
        {run.warningCount > run.warnings.length && `，只保存了前 ${run.warnings.length} 条`}
      </p>
      <ul
        aria-label="警告"
        className="max-h-80 overflow-y-auto rounded-md border bg-muted/30 p-2 text-xs leading-relaxed"
      >
        {run.warnings.map((warning, i) => (
          <li key={i}>{warning}</li>
        ))}
      </ul>
    </div>
  )
}
