import { NavLink, Outlet } from 'react-router-dom'
import { ProviderProvider, useProvider } from '../../providers/provider'
import type { Provider } from '../../types'

const navItems = [
  { to: '/', label: 'Дашборд' },
  { to: '/keys', label: 'Ключи' },
  { to: '/stats', label: 'Статистика' },
  { to: '/models', label: 'Модели' },
	{ to: '/status', label: 'Статус' },
  { to: '/proxies', label: 'Прокси' },
]

function ProviderSwitcher() {
  const { provider, setProvider } = useProvider()
  const options: Provider[] = ['openrouter', 'aihubmix', 'google']

  return (
    <div className="flex items-center gap-2">
      <span className="text-xs text-slate-400">Провайдер:</span>
      <div className="flex rounded-lg bg-slate-800 p-0.5">
        {options.map((p) => (
          <button
            key={p}
            onClick={() => setProvider(p)}
            className={`rounded-md px-3 py-1 text-xs font-medium transition ${
              provider === p
                ? 'bg-indigo-600 text-white'
                : 'text-slate-300 hover:text-white'
            }`}
          >
            {p}
          </button>
        ))}
      </div>
    </div>
  )
}

function ProviderNav() {
  return (
    <nav className="flex gap-1">
      {navItems.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.to === '/'}
          className={({ isActive }) =>
            `rounded-md px-3 py-1.5 text-sm font-medium transition ${
              isActive
                ? 'bg-slate-800 text-white'
                : 'text-slate-400 hover:text-white'
            }`
          }
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  )
}

export default function Layout() {
  return (
    <ProviderProvider>
      <div className="flex h-full flex-col">
        <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900 px-6 py-3">
          <div className="flex items-center gap-6">
            <h1 className="text-base font-semibold text-white">SZX Gateway</h1>
            <ProviderNav />
          </div>
          <ProviderSwitcher />
        </header>
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </ProviderProvider>
  )
}
