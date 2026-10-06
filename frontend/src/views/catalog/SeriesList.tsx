import { useState } from 'react'
import { RadioTowerIcon, SearchIcon } from 'lucide-react'
import { Link, useLocation, useSearchParams } from 'react-router'

import { Button, buttonVariants } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useSeriesList } from '@/hooks/use-series'
import { cn } from '@/lib/utils'

import {
  categorySearch,
  filterSeries,
  parseCategory,
  seriesCategories,
  seriesMeta,
} from './catalog'
import { Hint, Poster, scrollIntoView, useCatalogPath } from './shared'

/**
 * 左栏：剧列表，按分类（地址栏的 ?type=）、剧名或原名筛选，可以只看追更中的，按年份倒序。
 * 切换分类时选中的剧不变
 */
export default function SeriesList({ selectedId }: { selectedId?: number }) {
  const [keyword, setKeyword] = useState('')
  const [followingOnly, setFollowingOnly] = useState(false)
  const { pathname } = useLocation()
  const [searchParams] = useSearchParams()
  const category = parseCategory(searchParams)
  const path = useCatalogPath()
  const { data, error } = useSeriesList()
  const list = data ? filterSeries(data, { keyword, category, followingOnly }) : []

  return (
    <aside className="flex min-h-0 flex-col border-r">
      <div className="flex gap-1 p-2">
        <div className="relative min-w-0 flex-1">
          <SearchIcon className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-8"
            placeholder="筛选剧名 / 原名"
            aria-label="筛选剧名或原名"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
          />
        </div>
        <Button
          variant={followingOnly ? 'secondary' : 'ghost'}
          size="icon"
          aria-pressed={followingOnly}
          aria-label="只看追更中"
          title="只看追更中"
          onClick={() => setFollowingOnly(!followingOnly)}
        >
          <RadioTowerIcon />
        </Button>
      </div>
      <nav aria-label="分类" className="flex gap-1 px-2 pb-2">
        {seriesCategories.map((c) => {
          const selected = c.value === category
          return (
            <Link
              key={c.value}
              to={{ pathname, search: categorySearch(searchParams, c.value) }}
              aria-current={selected ? 'true' : undefined}
              className={buttonVariants({
                size: 'xs',
                variant: selected ? 'secondary' : 'ghost',
              })}
            >
              {c.label}
            </Link>
          )
        })}
      </nav>
      {data ? (
        <>
          <div className="px-3 pb-1 text-xs text-muted-foreground">{list.length} 部</div>
          {list.length === 0 && (
            <Hint>
              {data.length === 0
                ? '目录是空的，同步之后会出现在这里。'
                : keyword.trim()
                  ? '没有匹配的剧'
                  : followingOnly
                    ? '没有追更中的剧'
                    : `没有${seriesCategories.find((c) => c.value === category)?.label}`}
            </Hint>
          )}
          <ul className="min-h-0 flex-1 overflow-y-auto">
            {list.map((s) => {
              const selected = s.id === selectedId
              return (
                <li key={s.id}>
                  <Link
                    to={path(s.id)}
                    ref={selected ? scrollIntoView : undefined}
                    aria-current={selected ? 'true' : undefined}
                    className={cn(
                      'flex items-center gap-2.5 px-3 py-1.5 hover:bg-muted/60',
                      selected && 'bg-muted',
                    )}
                  >
                    <Poster imageId={s.posterImageId} className="w-8 rounded-sm" />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-1">
                        <span className="truncate text-sm font-medium">{s.title}</span>
                        {s.following && (
                          <RadioTowerIcon
                            className="size-3 shrink-0 text-emerald-700"
                            role="img"
                            aria-label="追更中"
                          >
                            <title>追更中</title>
                          </RadioTowerIcon>
                        )}
                      </div>
                      <div className="text-xs text-muted-foreground">
                        {seriesMeta(s)}
                        {s.type === 'tv' && ` · ${s.seasonCount} 季`}
                      </div>
                    </div>
                    <div className="shrink-0 text-right text-xs text-muted-foreground tabular-nums">
                      <div title="已绑定集数 / 总集数">
                        {s.boundEpisodeCount}/{s.episodeCount}
                      </div>
                      {s.deadBindingCount > 0 && (
                        <div className="text-destructive">{s.deadBindingCount} 失效</div>
                      )}
                    </div>
                  </Link>
                </li>
              )
            })}
          </ul>
        </>
      ) : (
        <Hint error={!!error}>{error ? error.message : '加载中…'}</Hint>
      )}
    </aside>
  )
}
