import { NavLink, Outlet } from 'react-router'

import SettingsPopover from '@/components/SettingsPopover'
import { buttonVariants } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/catalog', label: '目录' },
  { to: '/sync', label: '同步' },
]

export default function App() {
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
