import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import { getHealth } from '@/api/health'
import { ApiError } from '@/api/request'
import HomeView from '@/views/HomeView.vue'

vi.mock('@/api/health', () => ({ getHealth: vi.fn<typeof getHealth>() }))

describe('HomeView', () => {
  beforeEach(() => {
    vi.mocked(getHealth).mockReset()
  })

  it('后端正常时显示连接正常', async () => {
    vi.mocked(getHealth).mockResolvedValue({ status: 'ok' })

    const wrapper = mount(HomeView)
    expect(wrapper.text()).toContain('检查中...')

    await flushPromises()
    expect(wrapper.text()).toContain('后端与数据库连接正常')
  })

  it('后端异常时显示错误信息与业务码', async () => {
    vi.mocked(getHealth).mockRejectedValue(new ApiError('数据库不可用', 1, 503))

    const wrapper = mount(HomeView)
    await flushPromises()

    expect(wrapper.text()).toContain('数据库不可用（code: 1）')
  })
})
