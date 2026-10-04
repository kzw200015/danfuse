import { NavLink, Outlet } from 'react-router'

import { buttonVariants } from '@/components/ui/button'
import { Toaster } from '@/components/ui/sonner'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/', label: '首页' },
  { to: '/users', label: '用户' },
]

export default function App() {
  return (
    <div className="min-h-svh bg-background text-foreground">
      <header className="border-b">
        <div className="mx-auto flex h-14 max-w-5xl items-center gap-6 px-4">
          <span className="font-semibold">Danfuse</span>
          <nav className="flex items-center gap-1">
            {navItems.map((item) => (
              <NavLink
                key={item.to}
                to={item.to}
                end
                className={({ isActive }) =>
                  cn(buttonVariants({ variant: 'ghost', size: 'sm' }), isActive && 'bg-muted')
                }
              >
                {item.label}
              </NavLink>
            ))}
          </nav>
        </div>
      </header>

      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>

      <Toaster richColors />
    </div>
  )
}
