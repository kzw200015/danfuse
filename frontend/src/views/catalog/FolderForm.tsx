import { useState } from 'react'
import { useMutation, type QueryKey } from '@tanstack/react-query'
import { FolderOpenIcon, Loader2Icon } from 'lucide-react'
import { toast } from 'sonner'

import {
  createFolder,
  previewFolderEdit,
  previewNewFolder,
  saveFolder,
  type EntryHashes,
  type EpisodeRuleForm,
  type FolderFiles,
  type FolderPreviewForm,
  type FolderSeasonBinding,
  type FolderSeasonBindingDetail,
  type Preview,
} from '@/api/season-bindings'
import type { Season } from '@/api/series'
import { ErrorNote } from '@/components/ErrorNote'
import { Progress } from '@/components/ui/progress'
import { useElapsed } from '@/hooks/use-elapsed'
import { useSeasonBindingPreview, useWatchSeasonBinding } from '@/hooks/use-season-bindings'
import { cn } from '@/lib/utils'

import { groupFolderFiles, hashEntries, type UploadEntry } from './season-upload'
import {
  AddFormFrame,
  FormFrame,
  PreviewSection,
  RuleFields,
  useRuleDraft,
  useShownPreview,
} from './SeasonBindingFormParts'
import { takeFiles } from './shared'

/** 添加与编辑文件夹的季绑定的不同之处，由 AddFolderForm、EditFolderForm 给出；表单只经由它区分两种模式 */
export interface FolderFormMode {
  /** 表单的标题和无障碍名称 */
  label: string
  /** 保存按钮上的动词 */
  verb: string
  /** 保存成功的提示 */
  savedText: (detail: FolderSeasonBindingDetail) => string
  /** 选文件夹的说明 */
  pickLabel: string
  /** 一定要选文件夹（添加）；编辑时不换文件夹就按保存的条目算 */
  requireFolder: boolean
  /** 集号规则、集号对应的初始值 */
  initialRules: EpisodeRuleForm
  /** 预览的查询键前缀 */
  previewKey: QueryKey
  preview: (form: FolderPreviewForm) => Promise<Preview>
  /** folder 为 null 时不换文件夹；onProgress 收到上传的进度（0～1） */
  save: (
    folder: FolderFiles | null,
    rules: EpisodeRuleForm,
    onProgress: (progress: number) => void,
  ) => Promise<FolderSeasonBindingDetail>
  /** 保存成功或取消后收起表单 */
  onClose: () => void
}

/**
 * 季面板上"上传弹幕文件夹"展开的表单：选文件夹（每个子目录或顶层的每份 XML 是一个条目），按集号规则、集号对应
 * 看保存后的结果，上传后当场建出各集的绑定，要么全部成功，要么什么都不保存
 */
export function AddFolderForm({ season, onClose }: { season: Season; onClose: () => void }) {
  const mode = (patterns: string[]): FolderFormMode => ({
    label: '上传弹幕文件夹',
    verb: '上传',
    savedText: (d) => `已上传「${d.title}」，建出 ${d.bindingCount} 个绑定`,
    pickLabel: '选择文件夹',
    requireFolder: true,
    initialRules: { episodePatterns: patterns, mappingFrom: 1, mappingTo: 1 },
    previewKey: ['season-binding-preview', 'new-folder', season.id],
    preview: (form) => {
      if (form.folder === undefined) throw new Error('还没有选文件夹')
      return previewNewFolder(season.id, form)
    },
    save: (folder, rules, onProgress) => {
      if (!folder) throw new Error('还没有选文件夹')
      return createFolder(season.id, folder, rules, onProgress)
    },
    onClose,
  })
  return (
    <AddFormFrame label="上传弹幕文件夹" onClose={onClose}>
      {(patterns) => <FolderFormBody mode={mode(patterns)} />}
    </AddFormFrame>
  )
}

/**
 * 文件夹的季绑定卡片里的编辑：预填保存的集号规则、集号对应，打开就按保存的条目预览。
 * 可以换文件夹（名称和文件都没变的条目沿用原来的绑定），不换就只改集号规则、集号对应；没改也能保存
 */
export function EditFolderForm({
  binding,
  onClose,
}: {
  binding: FolderSeasonBinding
  onClose: () => void
}) {
  const mode: FolderFormMode = {
    label: `编辑「${binding.title}」`,
    verb: '保存',
    savedText: (d) => `已保存「${d.title}」`,
    pickLabel: '换文件夹（不换就只改集号规则、集号对应）',
    requireFolder: false,
    initialRules: {
      episodePatterns: binding.episodePatterns,
      mappingFrom: binding.mappingFrom,
      mappingTo: binding.mappingTo,
    },
    previewKey: ['season-binding-preview', 'folder', binding.id],
    preview: (form) => previewFolderEdit(binding.id, form),
    save: (folder, rules, onProgress) => saveFolder(binding.id, folder, rules, onProgress),
    onClose,
  }
  return (
    <FormFrame label={mode.label}>
      <FolderFormBody mode={mode} />
    </FormFrame>
  )
}

/** 选好的文件夹：分好的条目与各份文件的哈希（预览时代替文件本身） */
interface Picked {
  folder: string
  entries: UploadEntry[]
  ignored: number
  hashes: EntryHashes[]
}

/** 表单从上到下：选文件夹、集号规则、集号对应、预览、保存。集号规则、集号对应改了之后约 0.6 秒自动重新预览 */
function FolderFormBody({ mode }: { mode: FolderFormMode }) {
  const [picked, setPicked] = useState<Picked | null>(null)
  const [pickError, setPickError] = useState<string | null>(null)
  const hash = useMutation({
    mutationFn: async ({ folder, entries, ignored }: Omit<Picked, 'hashes'>) => ({
      folder,
      entries,
      ignored,
      hashes: await hashEntries(entries),
    }),
    onSuccess: setPicked,
  })
  const rules = useRuleDraft(mode.initialRules)
  let input: FolderPreviewForm | null = null
  if (rules.form && picked) {
    input = { ...rules.form, folder: picked.folder, entries: picked.hashes }
  } else if (rules.form && !mode.requireFolder) {
    input = rules.form
  }
  const result = useSeasonBindingPreview(mode.previewKey, mode.preview, input)
  const shown = useShownPreview(result.preview)

  // 上传的进度（整数百分比，同一个百分比不重复渲染），上传完是"正在保存"
  const [progress, setProgress] = useState(0)
  const watch = useWatchSeasonBinding()
  const save = useMutation({
    mutationFn: (form: EpisodeRuleForm) =>
      mode.save(
        picked && {
          files: picked.entries.flatMap((e) => e.files),
          paths: picked.entries.flatMap((e) => e.paths),
        },
        form,
        (p) => setProgress(Math.floor(p * 100)),
      ),
    onMutate: () => setProgress(0),
    onSuccess: async (detail) => {
      toast.success(mode.savedText(detail))
      mode.onClose()
      // 当场建出了各集的绑定：剧详情随之重新加载
      return watch(detail)
    },
  })
  const uploading = save.isPending && picked !== null

  function pick(files: File[]) {
    if (files.length === 0) return
    const grouped = groupFolderFiles(files)
    setPicked(null)
    hash.reset()
    if (!grouped.ok) {
      setPickError(grouped.error)
      return
    }
    setPickError(null)
    hash.mutate(grouped)
  }

  const busy = hash.isPending || save.isPending
  return (
    <>
      <div className="grid gap-1">
        <div className="flex flex-wrap items-center gap-2">
          <label
            className={cn(
              'inline-flex cursor-pointer items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-sm hover:bg-muted/50',
              busy && 'cursor-default opacity-50',
            )}
          >
            {hash.isPending ? (
              <Loader2Icon className="size-4 animate-spin" />
            ) : (
              <FolderOpenIcon className="size-4" />
            )}
            {mode.pickLabel}
            <input
              type="file"
              multiple
              className="sr-only"
              aria-label="选择弹幕文件夹"
              // React 的类型里没有 webkitdirectory：选文件夹，文件带着相对路径（webkitRelativePath）
              {...{ webkitdirectory: '' }}
              disabled={busy}
              onChange={(e) => pick(takeFiles(e.target))}
            />
          </label>
          {hash.isPending && (
            <span className="text-xs text-muted-foreground">正在计算文件哈希…</span>
          )}
          {picked && (
            <span className="text-xs">
              已选：{picked.folder}（{picked.entries.length} 个条目
              {picked.ignored > 0 && `，忽略了 ${picked.ignored} 份不是 XML 的文件`}）
            </span>
          )}
        </div>
        {pickError && <ErrorNote>{pickError}</ErrorNote>}
        {hash.error && <ErrorNote>{hash.error.message}</ErrorNote>}
      </div>

      {(picked || !mode.requireFolder) && (
        <RuleFields draft={rules} showPatterns prefix="" disabled={save.isPending} />
      )}

      <PreviewSection
        candidate={shown?.candidates[0]}
        state={{ ready: input !== null, current: result.current, error: result.error }}
        waitingText="正在预览…"
        save={{
          verb: mode.verb,
          pending: save.isPending,
          error: save.error,
          onSave: () => rules.form && save.mutate(rules.form),
          onDismissError: save.reset,
          progress: uploading && <UploadProgress progress={progress} />,
        }}
        onCancel={mode.onClose}
      />
    </>
  )
}

/** 上传进行中：进度条与百分比，传完之后是"正在保存"和已用秒数 */
function UploadProgress({ progress }: { progress: number }) {
  const saving = progress >= 100
  const elapsed = useElapsed(saving)
  return saving ? (
    <span className="text-xs text-muted-foreground">正在保存 {elapsed}s…</span>
  ) : (
    <span className="flex min-w-40 flex-1 items-center gap-2 text-xs text-muted-foreground">
      <Progress className="flex-1" value={progress} aria-label="上传进度" />
      上传中 {progress}%
    </span>
  )
}
