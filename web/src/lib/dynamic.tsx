import React from 'react'

type DynamicOptions = {
  ssr?: boolean
  loading?: () => React.ReactNode
}

export default function dynamic<T extends React.ComponentType<any>>(
  factory: () => Promise<{ default: T }>,
  _options?: DynamicOptions,
) {
  const LazyComponent = React.lazy(factory)
  const Wrapper = (props: React.ComponentProps<T>) => (
    <React.Suspense fallback={_options?.loading ? _options.loading() : null}>
      <LazyComponent {...props} />
    </React.Suspense>
  )
  return Wrapper as unknown as T
}
