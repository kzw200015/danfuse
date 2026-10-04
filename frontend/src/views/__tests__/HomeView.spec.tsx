import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { getHealth } from '@/api/health'
import { ApiError } from '@/api/request'
import HomeView from '@/views/HomeView'

vi.mock('@/api/health', () => ({ getHealth: vi.fn<typeof getHealth>() }))

/** 每个用例使用独立的 QueryClient，避免缓存互相影响 */
function renderView() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <HomeView />
    </QueryClientProvider>,
  )
}

describe('HomeView', () => {
  beforeEach(() => {
    vi.mocked(getHealth).mockReset()
  })

  it('后端正常时显示连接正常', async () => {
    vi.mocked(getHealth).mockResolvedValue({ status: 'ok' })

    renderView()
    expect(screen.getByText('检查中...')).toBeInTheDocument()

    expect(await screen.findByText('后端与数据库连接正常')).toBeInTheDocument()
  })

  it('后端异常时显示错误信息与业务码', async () => {
    vi.mocked(getHealth).mockRejectedValue(new ApiError('数据库不可用', 1, 503))

    renderView()

    expect(await screen.findByText('数据库不可用（code: 1）')).toBeInTheDocument()
  })
})
