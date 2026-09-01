import { QueryClient, QueryClientProvider, useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { type ReactNode } from 'react'
import { apiFetch } from '@/lib/utils'
import { getMessage } from '@/lib/message'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30 * 1000,
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

export function QueryProvider({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

export function useApiQuery<T>(key: string[], path: string) {
  return useQuery({
    queryKey: key,
    queryFn: () => apiFetch(path) as Promise<T>,
  })
}

export function useApiMutation<TData, TVariables>(
  key: string[],
  path: string | ((variables: TVariables) => string),
  method: 'POST' | 'PUT' | 'DELETE' | 'PATCH' = 'POST'
) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: async (variables: TVariables) => {
      const url = typeof path === 'function' ? path(variables) : path
      return apiFetch(url, { method, body: JSON.stringify(variables) }) as Promise<TData>
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: key })
    },
  })
}
