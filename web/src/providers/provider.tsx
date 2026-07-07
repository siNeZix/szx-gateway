import {
  createContext,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import type { Provider } from '../types'

// Провайдер хранится в localStorage и в реактивном состоянии.
// Ponytail: без Zustand — useState + Context достаточно, переключатель в шапке.

const STORAGE_KEY = 'szx.provider'

interface ProviderCtx {
  provider: Provider
  setProvider: (p: Provider) => void
}

const Ctx = createContext<ProviderCtx | null>(null)

export function ProviderProvider({ children }: { children: ReactNode }) {
  const [provider, setProvider] = useState<Provider>(() => {
    const p = localStorage.getItem(STORAGE_KEY)
    return p === 'aihubmix' ? 'aihubmix' : 'openrouter'
  })

  const value = useMemo(
    () => ({
      provider,
      setProvider: (p: Provider) => {
        setProvider(p)
        localStorage.setItem(STORAGE_KEY, p)
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
