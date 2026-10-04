import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { SearchIcon } from 'lucide-react'
import { Link } from 'react-router'

import { listSeries, seriesKeys } from '@/api/series'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

import { catalogPath, filterSeries, seriesMeta } from './catalog'
import { Hint, Poster, scrollIntoView } from './shared'

/** 左栏：剧列表，按剧名或原名筛选、按标题排序 */
export default function SeriesList({ selectedId }: { selectedId?: number }) {
  const [keyword, setKeyword] = useState('')
  const { data, error } = useQuery({ queryKey: seriesKeys.list, queryFn: listSeries })
  const list = data ? filterSeries(data, keyword) : []

  return (
    <aside className="flex min-h-0 flex-col border-r">
      <div className="relative p-2">
        <SearchIcon className="absolute top-1/2 left-4 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          className="pl-8"
          placeholder="筛选剧名 / 原名"
          aria-label="筛选剧名或原名"
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
        />
      </div>
      {data ? (
        <>
          <div className="px-3 pb-1 text-xs text-muted-foreground">{list.length} 部</div>
          {list.length === 0 && (
            <Hint>{data.length === 0 ? '目录是空的，同步之后会出现在这里。' : '没有匹配的剧'}</Hint>
          )}
          <ul className="min-h-0 flex-1 overflow-y-auto">
            {list.map((s) => {
              const selected = s.id === selectedId
              return (
                <li key={s.id}>
                  <Link
                    to={catalogPath(s.id)}
                    ref={selected ? scrollIntoView : undefined}
                    aria-current={selected ? 'true' : undefined}
                    className={cn(
                      'flex items-center gap-2.5 px-3 py-1.5 hover:bg-muted/60',
                      selected && 'bg-muted',
                    )}
                  >
                    <Poster className="w-8 rounded-sm" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm font-medium">{s.title}</div>
                      <div className="text-xs text-muted-foreground">
                        {seriesMeta(s)}
                        {s.type === 'tv' && ` · ${s.seasonCount} 季`}
                      </div>
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
