import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'

import { folderSeasonBinding, newQueryClient, seasonBinding, seedSeries } from '@/__tests__/utils'
import type { SeasonBindingDetail } from '@/api/season-bindings'
import { seasonBindingKeys, useWatchSeasonBinding } from '@/hooks/use-season-bindings'

describe('useWatchSeasonBinding', () => {
  // 合集保存表单时当场删掉没有条目的槽位，文件夹当场建出绑定：两种写入之后剧详情都要重新加载
  it.each([
    ['合集', seasonBinding(1, { running: true })],
    ['文件夹', folderSeasonBinding(2)],
  ])('%s的写入成功后把详情放进缓存，让剧列表和剧详情失效', async (_, sb) => {
    const queryClient = newQueryClient()
    const seriesInvalidated = seedSeries(queryClient)
    const { result } = renderHook(useWatchSeasonBinding, {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
      ),
    })
    const detail: SeasonBindingDetail = { ...sb, items: [], emptySlots: [] }

    await act(() => result.current(detail))

    expect(queryClient.getQueryData(seasonBindingKeys.detail(sb.id))).toBe(detail)
    expect(seriesInvalidated()).toEqual([true, true])
  })
})
