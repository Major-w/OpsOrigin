import { type ReactNode, useCallback } from 'react'

const prefetched = new Set<string>()

export function usePrefetch() {
  const prefetch = useCallback((routePath: string) => {
    if (prefetched.has(routePath)) return
    prefetched.add(routePath)
    const routes = import.meta.glob('/src/app/**/page.tsx')
    const normalizedPath = routePath.replace(/^\//, '').replace(/\/$/, '') || 'app/(dashboard)/dashboard/page'
    for (const path in routes) {
      if (path.includes(normalizedPath)) {
        routes[path]().catch(() => {})
      }
    }
  }, [])

  return prefetch
}

export function PrefetchLink({ to, children, className, ...props }: { to: string; children: ReactNode; className?: string } & React.AnchorHTMLAttributes<HTMLAnchorElement>) {
  const prefetch = usePrefetch()
  return (
    <a
      href={to}
      className={className}
      onMouseEnter={() => prefetch(to)}
      onFocus={() => prefetch(to)}
      {...props}
    >
      {children}
    </a>
  )
}
