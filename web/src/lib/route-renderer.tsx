import React from 'react'
import { Route, Navigate } from 'react-router-dom'
import type { RouteConfig } from '@/routes'
import { useAccess, type Access } from '@/access'
import ExceptionPage from '@/components/exception'

function RouteGuard({ route, access }: { route: RouteConfig; access: Access }) {
  if (route.access && !access[route.access]) {
    return <Route path={route.path} element={<ExceptionPage type="403" />} />
  }
  if (route.component) {
    return <Route path={route.path} element={<route.component />} />
  }
  if (route.redirect) {
    return <Route path={route.path} element={<Navigate to={route.redirect} replace />} />
  }
  return null
}

export function renderRoutes(routes: RouteConfig[]) {
  const access = useAccess()

  function render(items: RouteConfig[]): React.ReactNode[] {
    return items.map((route) => {
      if (route.routes) {
        return (
          <Route key={route.path} path={route.path}>
            {route.redirect && <Route index element={<Navigate to={route.redirect} replace />} />}
            {render(route.routes)}
          </Route>
        )
      }
      return (
        <RouteGuard key={route.path} route={route} access={access} />
      )
    })
  }

  return render(routes)
}
