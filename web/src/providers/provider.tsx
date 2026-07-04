import {
  createContext,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import type { Provider } from '../types'

// Провайдер хранится в URL (?provider=) и в реактивном состоянии.
// Ponytail: без Zustand — useState + Context достаточно, переключатель в шапке.

interface ProviderCtx {
  provider: Provider
  setProvider: (p: Provider) => void
}

const Ctx = createContext<ProviderCtx | null>(null)

export function ProviderProvider({ children }: { children: ReactNode }) {
  const [provider, setProvider] = useState<Provider>(() => {
    const url = new URL(window.location.href)
    const p = url.searchParams.get('provider')
    return p === 'aihubmix' ? 'aihubmix' : 'openrouter'
  })

  const value = useMemo(
    () => ({
      provider,
      setProvider: (p: Provider) => {
        setProvider(p)
        // Синхронизируем URL, чтобы при reload состояние сохранялось.
        const url = new URL(window.location.href)
        url.searchParams.set('provider', p)
        window.history.replaceState({}, '', url)
      },
    }),
    [provider],
  )

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useProvider(): ProviderCtx {
  const c = useContext(Ctx)
  if (!c) throw new Error('useProvider must be used within ProviderProvider')
  return c
}
