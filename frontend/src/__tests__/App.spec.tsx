import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'

import { routes } from '@/router/routes'

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  render(<RouterProvider router={router} />)
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
