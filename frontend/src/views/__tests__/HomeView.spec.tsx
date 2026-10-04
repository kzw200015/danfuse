import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'

import { getHealth } from '@/api/health'
import { ApiError } from '@/api/request'
import HomeView from '@/views/HomeView'

vi.mock('@/api/health', () => ({ getHealth: vi.fn<typeof getHealth>() }))

describe('HomeView', () => {
  beforeEach(() => {
    vi.mocked(getHealth).mockReset()
  })

  it('后端正常时显示连接正常', async () => {
    vi.mocked(getHealth).mockResolvedValue({ status: 'ok' })

    render(<HomeView />)
    expect(screen.getByText('检查中...')).toBeInTheDocument()

    expect(await screen.findByText('后端与数据库连接正常')).toBeInTheDocument()
  })

  it('后端异常时显示错误信息与业务码', async () => {
    vi.mocked(getHealth).mockRejectedValue(new ApiError('数据库不可用', 1, 503))

    render(<HomeView />)

    expect(await screen.findByText('数据库不可用（code: 1）')).toBeInTheDocument()
  })
})
