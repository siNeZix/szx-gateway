import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { useProvider } from '../providers/provider'

function Metric({ label, value, tone = 'text-slate-100' }: { label: string; value: number; tone?: string }) {
  return (
    <div>
      <div className="text-xs text-slate-400">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${tone}`}>{value}</div>
    </div>
  )
}

export default function Service() {
  const { provider } = useProvider()
  const queryClient = useQueryClient()
  const enabled = provider === 'aihubmix'
  const stats = useQuery({ queryKey: ['stats', provider], queryFn: () => api.stats(provider), enabled, refetchInterval: 5_000 })
  const job = useQuery({
    queryKey: ['service-check'],
    queryFn: api.serviceCheck,
    enabled,
    refetchInterval: (query) => query.state.data?.running ? 1_000 : false,
  })
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: ['service-check'] })
    await queryClient.invalidateQueries({ queryKey: ['stats', 'aihubmix'] })
    await queryClient.invalidateQueries({ queryKey: ['keys', 'aihubmix'] })
  }
  const start = useMutation({ mutationFn: api.startServiceCheck, onSuccess: refresh })
  const cancel = useMutation({ mutationFn: api.cancelServiceCheck, onSuccess: refresh })

  if (!enabled) return <p className="text-slate-400">Сервис доступен только для AIHubMix.</p>
  if (stats.isLoading || job.isLoading) return <p className="text-slate-400">Загрузка...</p>
  if (stats.error || job.error) return <p className="text-rose-400">Ошибка: {((stats.error || job.error) as Error).message}</p>

  const general = stats.data?.general
  const current = job.data
  const running = current?.running ?? false
  const progress = current && current.total > 0 ? (current.completed / current.total) * 100 : 0
  const busy = start.isPending || cancel.isPending
  const error = start.error || cancel.error

  return (
    <section className="mx-auto max-w-4xl space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-white">Сервис AIHubMix</h2>
        <p className="mt-1 text-sm text-slate-400">Проверки выполняются по одному ключу и могут быть прерваны.</p>
      </div>

      <div className="grid gap-4 md:grid-cols-2">
        <article className="rounded-xl border border-slate-800 bg-slate-800/40 p-5">
          <h3 className="text-base font-semibold text-white">Ключи</h3>
          <p className="mt-1 text-sm text-slate-400">Проверка доступа к API без расхода квоты.</p>
          <div className="mt-5 flex gap-8"><Metric label="Всего" value={general?.total_keys ?? 0} /><Metric label="Invalid" value={general?.invalid_keys ?? 0} tone="text-rose-400" /></div>
          <button disabled={running || busy} onClick={() => start.mutate('keys')} className="mt-5 rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50">Начать проверку</button>
        </article>

        <article className="rounded-xl border border-slate-800 bg-slate-800/40 p-5">
          <h3 className="text-base font-semibold text-white">Лимиты</h3>
          <p className="mt-1 text-sm text-slate-400">Пробный запрос, как в «Статус». Расходует одну минимальную квоту на ключ.</p>
          <div className="mt-5 flex gap-8"><Metric label="Active" value={general?.active_keys ?? 0} tone="text-emerald-400" /><Metric label="Day exhausted" value={stats.data?.keys.filter((key) => key.status === 'day_exhausted').length ?? 0} tone="text-rose-400" /></div>
          <button disabled={running || busy} onClick={() => start.mutate('limits')} className="mt-5 rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:cursor-not-allowed disabled:opacity-50">Проверить лимиты</button>
        </article>
      </div>

      {current && (running || current.completed > 0) && <div className="rounded-xl border border-slate-800 bg-slate-900 p-5">
        <div className="flex items-center justify-between gap-4"><div><h3 className="font-medium text-white">{current.mode === 'keys' ? 'Проверка ключей' : 'Проверка лимитов'}</h3><p className="mt-1 text-sm text-slate-400">{current.completed} из {current.total}</p></div>{running && <button disabled={busy} onClick={() => cancel.mutate()} className="rounded-md border border-rose-800 px-3 py-1.5 text-sm font-medium text-rose-300 transition hover:bg-rose-950/50 disabled:opacity-50">Прервать</button>}</div>
        <div className="mt-4 h-2 overflow-hidden rounded-full bg-slate-800"><div className="h-full rounded-full bg-indigo-500 transition-[width] duration-200" style={{ width: `${progress}%` }} /></div>
        <div className="mt-4 grid grid-cols-2 gap-3 text-sm sm:grid-cols-4"><span className="text-emerald-400">active: {current.active}</span><span className="text-rose-400">invalid: {current.invalid}</span><span className="text-rose-400">exhausted: {current.day_exhausted}</span><span className="text-amber-300">rate limit: {current.rate_limited}</span></div>
      </div>}
      {error && <p className="rounded-lg border border-rose-900 bg-rose-950/30 p-3 text-sm text-rose-300">{error.message}</p>}
    </section>
  )
}
