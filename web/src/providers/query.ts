import { QueryClient } from '@tanstack/react-query'

// ponytail: дефолты TanStack Query. refetchInterval задаётся на уровне запроса,
// здесь только общие настройки. retry 1 — чтобы не молотить упавший бэкенд.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
      staleTime: 3_000,
    },
  },
})
