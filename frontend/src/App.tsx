import { NavLink, Outlet } from 'react-router'

import SettingsPopover from '@/components/SettingsPopover'
import { SyncNavStatus } from '@/components/SyncStatus'
import { buttonVariants } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { useLatestSyncRun } from '@/hooks/use-sync-runs'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/catalog', label: '目录' },
  { to: '/sync', label: '同步' },
]

export default function App() {
  // 同步状态挂在"同步"导航项上，在这里轮询，不切换页面也能看到
  const latestRun = useLatestSyncRun()

  return (
    <div className="flex h-svh flex-col bg-background text-foreground">
      <header className="flex h-12 shrink-0 items-center gap-4 border-b px-4">
        <span className="font-semibold">Danfuse</span>
        <nav className="flex items-center gap-1">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(buttonVariants({ variant: 'ghost', size: 'sm' }), isActive && 'bg-muted')
              }
            >
              {item.label}
              {item.to === '/sync' && <SyncNavStatus run={latestRun} />}
            </NavLink>
          ))}
        </nav>
        <div className="ml-auto">
          <SettingsPopover />
        </div>
      </header>

      {/* 页面占满顶栏以下的高度，需要滚动的页面自己处理 */}
      <main className="min-h-0 flex-1">
        <Outlet />
      </main>

      <Toaster richColors />
    </div>
  )
}
