import { useState } from 'react'
import { ArrowDownIcon, ArrowUpIcon, PlusIcon, XIcon } from 'lucide-react'

import type { EpisodePattern, EpisodeRuleConfig } from '@/api/season-bindings'
import { ErrorNote } from '@/components/ErrorNote'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useDefaultEpisodePatterns, useEpisodeTemplate } from '@/hooks/use-season-bindings'

const maxPatterns = 10
const selectClass = 'h-8 rounded-md border bg-background px-2 text-xs text-foreground'

export default function EpisodeRuleInput({
  value,
  onChange,
  disabled = false,
}: {
  value: EpisodeRuleConfig
  onChange: (rule: EpisodeRuleConfig) => void
  disabled?: boolean
}) {
  const defaults = useDefaultEpisodePatterns()
  const legacy = value.version === 1
  const changeRules = (rules: EpisodePattern[]) => onChange({ ...value, rules })
  const move = (i: number, to: number) => {
    const next = [...value.rules]
    ;[next[i], next[to]] = [next[to]!, next[i]!]
    changeRules(next)
  }
  return (
    <div role="group" aria-label="集号规则" className="grid gap-2 text-xs text-muted-foreground">
      {legacy ? (
        <div className="flex flex-wrap items-center gap-2">
          <span>旧版规则（完整标题，保留原识别行为）</span>
          <Button
            type="button"
            variant="outline"
            size="xs"
            disabled={disabled || !defaults.data}
            onClick={() => defaults.data && onChange(structuredClone(defaults.data.episodeRule))}
          >
            使用新版模板
          </Button>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-2">
              匹配范围
              <select
                aria-label="匹配范围"
                className={selectClass}
                value={value.scope}
                disabled={disabled}
                onChange={(e) =>
                  onChange({ ...value, scope: e.target.value as EpisodeRuleConfig['scope'] })
                }
              >
                <option value="entry">下级条目标题（分 P / 文件名）</option>
                <option value="full">完整展示标题</option>
              </select>
            </label>
            <label className="flex items-center gap-1.5">
              <input
                type="checkbox"
                checked={value.excludes.some((e) => e.kind === 'nonRegular')}
                disabled={
                  disabled ||
                  !defaults.data ||
                  (!value.excludes.some((e) => e.kind === 'nonRegular') &&
                    value.excludes.length >= maxPatterns)
                }
                onChange={(e) =>
                  onChange({
                    ...value,
                    excludes: [
                      ...value.excludes.filter((p) => p.kind !== 'nonRegular'),
                      ...(e.target.checked
                        ? defaults.data!.episodeRule.excludes.filter((p) => p.kind === 'nonRegular')
                        : []),
                    ],
                  })
                }
              />
              仅正片
            </label>
          </div>
          <span>先排除非正片，再匹配集号。来源季需要明确配置；集号加减在判定重复之前生效。</span>
          <TemplatePicker disabled={disabled} value={value} onChange={onChange} />
        </>
      )}
      {defaults.error && <ErrorNote>{defaults.error.message}</ErrorNote>}
      <span>集号规则（取最靠后的集号，位置一样时排在前面的优先）</span>
      {value.rules.map((rule, i) => (
        <div key={i} className="flex flex-wrap items-center gap-1">
          <span className="w-5 shrink-0 text-right tabular-nums">{i + 1}</span>
          <span className="min-w-0 flex-1 truncate">规则 {i + 1}</span>
          {!legacy && (
            <label className="flex items-center gap-1">
              集号加减
              <Input
                type="number"
                step={1}
                min={-2147483647}
                max={2147483647}
                className="h-7 w-24"
                aria-label={`第 ${i + 1} 条集号加减`}
                value={Number.isNaN(rule.adjustment) ? '' : rule.adjustment}
                disabled={disabled}
                onChange={(e) =>
                  changeRules(
                    value.rules.map((p, j) =>
                      j === i
                        ? { ...p, adjustment: e.target.value === '' ? NaN : Number(e.target.value) }
                        : p,
                    ),
                  )
                }
              />
            </label>
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`上移第 ${i + 1} 条`}
            disabled={disabled || i === 0}
            onClick={() => move(i, i - 1)}
          >
            <ArrowUpIcon />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`下移第 ${i + 1} 条`}
            disabled={disabled || i === value.rules.length - 1}
            onClick={() => move(i, i + 1)}
          >
            <ArrowDownIcon />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={`删除第 ${i + 1} 条`}
            disabled={disabled || value.rules.length === 1}
            onClick={() => changeRules(value.rules.filter((_, j) => j !== i))}
          >
            <XIcon />
          </Button>
        </div>
      ))}
      <details open={legacy || undefined}>
        <summary className="cursor-pointer py-1">高级正则与排除规则</summary>
        <div className="grid gap-2 py-2">
          <p>
            使用 RE2 正则；优先取 episode 捕获组，否则取第一个捕获组。每组最多 10 条，每条最多 200
            字符。
          </p>
          {value.rules.map((rule, i) => (
            <Input
              key={i}
              aria-label={`集号规则第 ${i + 1} 条`}
              className="h-7 font-mono"
              value={rule.pattern}
              disabled={disabled}
              placeholder="正则，如 第(\d+)集"
              onChange={(e) =>
                changeRules(
                  value.rules.map((p, j) => (j === i ? { ...p, pattern: e.target.value } : p)),
                )
              }
            />
          ))}
          <div className="flex gap-1">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={disabled || value.rules.length >= maxPatterns}
              onClick={() => changeRules([...value.rules, { pattern: '', adjustment: 0 }])}
            >
              <PlusIcon />
              新增
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={disabled || !defaults.data}
              onClick={() =>
                defaults.data &&
                onChange(
                  legacy
                    ? {
                        ...value,
                        rules: defaults.data.episodePatterns.map((pattern) => ({
                          pattern,
                          adjustment: 0,
                        })),
                      }
                    : structuredClone(defaults.data.episodeRule),
                )
              }
            >
              恢复默认
            </Button>
          </div>
          {!legacy && (
            <>
              <span>自定义排除（始终检查下级标题，不需要捕获组）</span>
              {value.excludes.map(
                (exclude, i) =>
                  exclude.kind === 'custom' && (
                    <div key={i} className="flex gap-1">
                      <Input
                        aria-label={`排除规则第 ${i + 1} 条`}
                        className="h-7 font-mono"
                        value={exclude.pattern}
                        disabled={disabled}
                        onChange={(e) =>
                          onChange({
                            ...value,
                            excludes: value.excludes.map((p, j) =>
                              j === i ? { ...p, pattern: e.target.value } : p,
                            ),
                          })
                        }
                      />
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        aria-label={`删除排除规则第 ${i + 1} 条`}
                        disabled={disabled}
                        onClick={() =>
                          onChange({ ...value, excludes: value.excludes.filter((_, j) => j !== i) })
                        }
                      >
                        <XIcon />
                      </Button>
                    </div>
                  ),
              )}
              <Button
                type="button"
                variant="ghost"
                size="xs"
                className="w-fit"
                disabled={disabled || value.excludes.length >= maxPatterns}
                onClick={() =>
                  onChange({
                    ...value,
                    excludes: [...value.excludes, { pattern: '', kind: 'custom' }],
                  })
                }
              >
                <PlusIcon />
                新增排除规则
              </Button>
            </>
          )}
        </div>
      </details>
    </div>
  )
}

function TemplatePicker({
  value,
  onChange,
  disabled,
}: {
  value: EpisodeRuleConfig
  onChange: (rule: EpisodeRuleConfig) => void
  disabled: boolean
}) {
  const defaults = useDefaultEpisodePatterns()
  const [templateId, setTemplateId] = useState('episodeLabel')
  const [season, setSeason] = useState('')
  const [adjustment, setAdjustment] = useState('0')
  const [open, setOpen] = useState(false)
  const template = defaults.data?.templates.find((t) => t.id === templateId)
  const seasonValid =
    !template?.season || season === '' || (/^\d{1,4}$/.test(season) && Number(season) > 0)
  const valid =
    seasonValid && /^-?\d+$/.test(adjustment) && Math.abs(Number(adjustment)) <= 2147483647
  const input = {
    templateId,
    ...(template?.season && season !== '' ? { season: Number(season) } : {}),
    adjustment: Number(adjustment),
  }
  const rendered = useEpisodeTemplate(input, open && valid && !!template)
  const apply = (replace: boolean) => {
    if (rendered.data)
      onChange({
        ...value,
        rules: replace ? [rendered.data.rule] : [rendered.data.rule, ...value.rules],
      })
  }
  return (
    <details className="rounded-md border p-2" onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="cursor-pointer text-foreground">使用模板</summary>
      <div className="mt-2 grid gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1">
            格式
            <select
              aria-label="集号模板"
              className={selectClass}
              value={templateId}
              disabled={disabled}
              onChange={(e) => setTemplateId(e.target.value)}
            >
              {defaults.data?.templates.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </select>
          </label>
          {template?.season && (
            <label className="flex items-center gap-1">
              来源季（留空不限）
              <Input
                aria-label="模板来源季"
                className="h-8 w-20"
                type="number"
                min={1}
                max={9999}
                value={season}
                disabled={disabled}
                onChange={(e) => setSeason(e.target.value)}
              />
            </label>
          )}
          <label className="flex items-center gap-1">
            集号加减
            <Input
              aria-label="模板集号加减"
              className="h-8 w-24"
              type="number"
              step={1}
              value={adjustment}
              disabled={disabled}
              onChange={(e) => setAdjustment(e.target.value)}
            />
          </label>
        </div>
        {rendered.data && valid && (
          <span>
            示例：{rendered.data.example}；集号加减 {input.adjustment >= 0 ? '+' : ''}
            {input.adjustment}
          </span>
        )}
        {!valid && <ErrorNote>来源季或集号加减值不合法</ErrorNote>}
        {rendered.error && <ErrorNote>{rendered.error.message}</ErrorNote>}
        <div className="flex flex-wrap gap-2">
          <Button
            type="button"
            size="xs"
            variant="outline"
            disabled={disabled || !valid || !rendered.data}
            onClick={() => apply(true)}
          >
            仅使用此规则
          </Button>
          <Button
            type="button"
            size="xs"
            variant="outline"
            disabled={disabled || !valid || !rendered.data || value.rules.length >= maxPatterns}
            onClick={() => apply(false)}
          >
            添加覆盖规则
          </Button>
        </div>
      </div>
    </details>
  )
}
