import { useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import { Loader2Icon, PlusIcon, Trash2Icon } from 'lucide-react'
import { toast } from 'sonner'

import {
  createBlockedWord,
  deleteBlockedWord,
  type BlockedWord,
  type BlockedWordKind,
} from '@/api/blocked-words'
import { ErrorNote } from '@/components/ErrorNote'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useBlockedWords, useReloadBlockedWords } from '@/hooks/use-blocked-words'
import { cn } from '@/lib/utils'

const blockedWordKindLabels: Record<BlockedWordKind, string> = {
  keyword: '关键词',
  regex: '正则',
}

const placeholders: Record<BlockedWordKind, string> = {
  keyword: '包含这段文字即屏蔽，不分大小写、全半角，忽略空白',
  regex: '对原始正文匹配（RE2 语法），忽略大小写时写 (?i)',
}

/** 管理屏蔽词：上面新增，下面是列表（新加的在前），每条可以直接删除，删错了重新加一条即可 */
export default function BlockedWordsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* 贴顶显示：列表增删时只有底边动 */}
      <DialogContent className="top-[10vh] translate-y-0 sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>屏蔽词</DialogTitle>
          <DialogDescription>
            正文命中的弹幕不由弹弹 API
            输出，播放器下次取弹幕时生效。保存的弹幕不受影响，删掉屏蔽词后照常输出。
          </DialogDescription>
        </DialogHeader>
        <AddBlockedWordForm />
        <BlockedWordList />
      </DialogContent>
    </Dialog>
  )
}

/** 类型切换、输入框和添加按钮，回车也能添加；失败的提示留在下方，到下次提交或手动关闭为止 */
function AddBlockedWordForm() {
  const reload = useReloadBlockedWords()
  const [kind, setKind] = useState<BlockedWordKind>('keyword')
  const [pattern, setPattern] = useState('')
  const create = useMutation({
    mutationFn: () => createBlockedWord(kind, pattern),
    onSuccess: (word) => {
      toast.success(`已添加屏蔽词「${word.pattern}」`)
      setPattern('')
      return reload()
    },
  })

  return (
    <form
      className="grid gap-1.5"
      onSubmit={(e) => {
        e.preventDefault()
        create.mutate()
      }}
    >
      <div className="flex items-center gap-2">
        <div
          role="group"
          aria-label="类型"
          className="flex shrink-0 gap-0.5 rounded-md border p-0.5"
        >
          {(['keyword', 'regex'] as const).map((k) => (
            <Button
              key={k}
              type="button"
              size="xs"
              variant={kind === k ? 'secondary' : 'ghost'}
              aria-pressed={kind === k}
              onClick={() => setKind(k)}
            >
              {blockedWordKindLabels[k]}
            </Button>
          ))}
        </div>
        <Input
          aria-label="屏蔽词"
          className={cn(kind === 'regex' && 'font-mono')}
          placeholder={placeholders[kind]}
          maxLength={100}
          value={pattern}
          disabled={create.isPending}
          onChange={(e) => setPattern(e.target.value)}
        />
        <Button type="submit" disabled={create.isPending || !pattern.trim()}>
          {create.isPending ? <Loader2Icon className="animate-spin" /> : <PlusIcon />}
          添加
        </Button>
      </div>
      {create.error && <ErrorNote onClose={create.reset}>{create.error.message}</ErrorNote>}
    </form>
  )
}

function BlockedWordList() {
  const { data, error } = useBlockedWords()
  const reload = useReloadBlockedWords()
  const remove = useMutation({
    mutationFn: (word: BlockedWord) => deleteBlockedWord(word.id),
    onSuccess: (_, word) => {
      toast.success(`已删除屏蔽词「${word.pattern}」`)
    },
    // 失败（例如已在别处删掉）时也刷新，列表与服务端一致
    onSettled: () => reload(),
  })

  if (error) return <ErrorNote>{error.message}</ErrorNote>
  if (!data) return <p className="text-xs text-muted-foreground">加载中…</p>
  return (
    <div className="grid gap-1.5">
      {remove.error && <ErrorNote onClose={remove.reset}>{remove.error.message}</ErrorNote>}
      {data.length === 0 ? (
        <p className="text-xs text-muted-foreground">还没有屏蔽词</p>
      ) : (
        <ul className="max-h-[50vh] divide-y overflow-y-auto rounded-md border">
          {data.map((word) => {
            const deleting = remove.isPending && remove.variables.id === word.id
            return (
              <li key={word.id} className="flex items-center gap-2 px-2.5 py-1.5 text-sm">
                <Badge variant={word.kind === 'regex' ? 'outline' : 'secondary'}>
                  {blockedWordKindLabels[word.kind]}
                </Badge>
                <span
                  className={cn('min-w-0 flex-1 break-all', word.kind === 'regex' && 'font-mono')}
                >
                  {word.pattern}
                </span>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  title="删除"
                  aria-label={`删除屏蔽词 ${word.pattern}`}
                  disabled={deleting}
                  onClick={() => remove.mutate(word)}
                >
                  {deleting ? <Loader2Icon className="animate-spin" /> : <Trash2Icon />}
                </Button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
