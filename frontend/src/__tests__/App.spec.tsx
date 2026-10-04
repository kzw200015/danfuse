import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'

import { routes } from '@/router/routes'

vi.mock('@/api/series', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/series')>()
  return { ...actual, listSeries: vi.fn<typeof actual.listSeries>().mockResolvedValue([]) }
})

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

describe('App', () => {
  it('根路径重定向到目录页', async () => {
    const router = renderAt('/')

    expect(await screen.findByRole('link', { name: '目录' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(router.state.location.pathname).toBe('/catalog')
  })

  it('顶栏导航切换页面', async () => {
    const router = renderAt('/catalog')

    fireEvent.click(await screen.findByRole('link', { name: '同步' }))

    // 页面是懒加载的，加载完成后导航才生效
    await waitFor(() => expect(router.state.location.pathname).toBe('/sync'))
    expect(screen.getByRole('link', { name: '同步' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: '目录' })).not.toHaveAttribute('aria-current')
  })

  it('齿轮打开设置弹出层', async () => {
    renderAt('/catalog')

    fireEvent.click(await screen.findByRole('button', { name: '设置' }))

    expect(await screen.findByRole('dialog')).toHaveTextContent('设置')
  })
})
