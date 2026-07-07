import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { StatCard } from '../components/ui/basics'
import { useProvider } from '../providers/provider'

export default function Dashboard() {
  const { provider } = useProvider()
  const { data, isLoading, error } = useQuery({
    queryKey: ['stats', provider],
    queryFn: () => api.stats(provider),
    refetchInterval: 5_000,
  })

  if (isLoading) return <div className="text-slate-400">Загрузка…</div>
  if (error)
    return (
      <div className="text-rose-400">Ошибка: {(error as Error).message}</div>
    )
  if (!data) return <div className="text-slate-400">Нет данных</div>

  const g = data.general
  const daily = data.daily_limits
  const dailyPct = daily.total > 0 ? Math.max(0, Math.min(100, (daily.remaining / daily.total) * 100)) : 0

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-lg font-semibold text-white">Сводка</h2>
        <p className="text-xs text-slate-500">
          Обновлено: {data.refreshed_at} · провайдер: {provider}
        </p>
      </div>

      <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-7">
        <StatCard label="Всего запросов" value={g.total_requests} />
        <StatCard label="Сегодня" value={g.today_requests} />
        <StatCard
          label="Активных ключей"
          value={g.active_keys}
          tone="good"
          hint={`из ${g.total_keys}`}
        />
        <StatCard
          label="Unchecked"
          value={g.unchecked_keys}
          tone="warn"
        />
        <StatCard
          label="Заблокировано"
          value={g.blocked_keys}
          tone="bad"
        />
        <StatCard
          label="Невалидных"
          value={g.invalid_keys}
          tone="bad"
        />
        <StatCard
          label="Неактивно"
          value={g.total_keys - g.active_keys - g.blocked_keys - g.invalid_keys - g.unchecked_keys}
          hint="disabled/прочее"
        />
      </div>

      {daily.total > 0 && (
        <div className="rounded-2xl border border-slate-800 bg-slate-800/40 p-4">
          <div className="flex items-end justify-between gap-4">
            <div>
              <div className="text-xs font-medium uppercase tracking-wide text-slate-400">
                Дневной лимит
              </div>
              <div className="mt-1 text-2xl font-semibold tabular-nums text-emerald-400">
                {daily.remaining} осталось
              </div>
            </div>
            <div className="text-right text-xs text-slate-500">
              <div className="tabular-nums">доступно: {daily.total}</div>
              <div className="tabular-nums">использовано: {daily.used}</div>
            </div>
          </div>
          <div className="mt-4 h-2 overflow-hidden rounded-full bg-slate-950/70">
            <div
              className="h-full rounded-full bg-emerald-400"
              style={{ width: `${dailyPct}%` }}
            />
          </div>
        </div>
      )}

      <div>
        <h3 className="mb-2 text-sm font-medium text-slate-300">
          Топ моделей по запросам
        </h3>
        <div className="overflow-hidden rounded-xl border border-slate-800">
          <table className="w-full text-sm">
            <thead className="bg-slate-800/60 text-xs uppercase text-slate-400">
                <tr>
                  <th className="px-4 py-2 text-left">Модель</th>
                  <th className="px-4 py-2 text-right">Сегодня</th>
                  <th className="px-4 py-2 text-right">Запросов</th>
                  <th className="px-4 py-2 text-right">Токенов</th>
                  <th className="px-4 py-2 text-right">Avg latency</th>
              </tr>
            </thead>
            <tbody>
              {data.models.slice(0, 10).map((m) => (
                <tr key={m.model} className="border-t border-slate-800">
                  <td className="px-4 py-2 font-mono text-xs text-slate-200">
                    {m.model}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-emerald-400">
                    {m.today_requests}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums">
                    {m.total_requests}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-400">
                    {m.total_tokens}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-400">
                    {m.avg_latency_ms} ms
                  </td>
                </tr>
              ))}
              {data.models.length === 0 && (
                <tr>
                  <td colSpan={5} className="px-4 py-6 text-center text-slate-500">
                    Нет данных
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
