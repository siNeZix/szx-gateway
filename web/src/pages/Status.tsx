import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { api } from '../api/client'
import { useProvider } from '../providers/provider'
import type { ModelCheckHour, ModelCheckItem } from '../types'

function normalizedError(error: string) {
  const text = error.replace(/\d+/g, '#').replace(/\s+/g, ' ').trim()
  return text || error
}

function AvailabilityBar({ hour }: { hour: ModelCheckHour }) {
  const [open, setOpen] = useState(false)
  const date = new Date(hour.hour)
  const grouped = new Map<string, { text: string; count: number }>()
  const errors = hour.errors ?? []
  const noDataReasons = hour.no_data_reasons ?? []
  for (const error of errors) {
    const key = normalizedError(error)
    const entry = grouped.get(key)
    if (entry) entry.count++
    else grouped.set(key, { text: error, count: 1 })
  }
  const color = !hour.has_data ? 'bg-slate-700' : hour.percent === 100 ? 'bg-emerald-500' : hour.percent === 0 ? 'bg-red-500' : 'bg-amber-400'

  return (
    <div className="relative min-w-px flex-1" onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)} onFocus={() => setOpen(true)} onBlur={() => setOpen(false)}>
      <button aria-label={`Доступность ${date.toLocaleString('ru-RU')}`} className={`h-full w-full rounded-sm ${color} transition duration-150 hover:-translate-y-1 hover:scale-x-125 hover:brightness-125 focus:-translate-y-1 focus:outline-none`} />
      {open && <div className="pointer-events-none absolute bottom-[calc(100%+10px)] left-1/2 z-20 w-72 -translate-x-1/2 rounded-lg border border-slate-700 bg-slate-950 p-3 text-left shadow-xl">
        <div className="flex items-center justify-between gap-3"><span className="text-xs text-slate-400">{date.toLocaleString('ru-RU', { dateStyle: 'medium', timeStyle: 'short' })}</span><span className={`font-semibold ${!hour.has_data ? 'text-slate-300' : hour.percent === 100 ? 'text-emerald-400' : hour.percent === 0 ? 'text-red-400' : 'text-amber-300'}`}>{hour.has_data ? `${hour.percent}%` : 'Нет данных'}</span></div>
        {hour.has_data && <p className="mt-2 text-xs text-slate-400">Проверок: {hour.checks ?? 0}. Успешно: {(hour.checks ?? 0) - errors.length}. Ошибок: {errors.length}.</p>}
        {noDataReasons.length > 0 && <div className="mt-2 border-t border-slate-800 pt-2"><p className="mb-1 text-xs font-medium text-slate-400">Нет данных</p>{[...new Set(noDataReasons)].map((reason) => <p key={reason} className="mb-1 break-words text-xs leading-4 text-slate-300">{reason}</p>)}</div>}
        {grouped.size > 0 && <div className="mt-2 border-t border-slate-800 pt-2"><p className="mb-1 text-xs font-medium text-red-300">Ошибки</p>{[...grouped.values()].map((error) => <p key={error.text} className="mb-1 break-words text-xs leading-4 text-slate-300">{error.text}{error.count > 1 ? ` (${error.count}x)` : ''}</p>)}</div>}
      </div>}
    </div>
  )
}

export default function Status() {
  const { provider } = useProvider()
  const queryClient = useQueryClient()
  const [dragged, setDragged] = useState<string | null>(null)
  const [testing, setTesting] = useState<string | null>(null)
  const { data: rows = [], isLoading, error } = useQuery({ queryKey: ['model-checks', provider], queryFn: () => api.modelChecks(provider), refetchInterval: 15_000 })

  function refresh() { return queryClient.invalidateQueries({ queryKey: ['model-checks', provider] }) }
  async function toggle(row: ModelCheckItem) { await api.saveModelCheck(provider, row.model, !row.enabled, row.position); await refresh() }
  async function test(row: ModelCheckItem) { setTesting(row.model); try { await api.testModelCheck(provider, row.model); await refresh() } finally { setTesting(null) } }
  async function drop(target: string) {
    if (!dragged || dragged === target) return
    const next = [...rows]
    const from = next.findIndex((row) => row.model === dragged)
    const to = next.findIndex((row) => row.model === target)
    const [item] = next.splice(from, 1)
    next.splice(to, 0, item)
    await api.saveModelCheckOrder(provider, next.map((row) => row.model))
    setDragged(null)
    await refresh()
  }

  return <section className="mx-auto max-w-[1800px]"><div className="mb-5 flex items-end justify-between"><div><h2 className="text-xl font-semibold text-white">Статус моделей</h2><p className="mt-1 text-sm text-slate-400">96 часов. Проверки: 10 минут, после ошибки: 5 минут.</p></div><span className="text-xs text-slate-500">Перетаскивайте строки для изменения порядка</span></div>{isLoading ? <p className="text-slate-400">Загрузка...</p> : error ? <p className="rounded-lg border border-red-900 bg-red-950/40 p-4 text-sm text-red-300">Не удалось загрузить статус: {error.message}</p> : <div className="overflow-x-auto rounded-xl border border-slate-800 bg-slate-900"><table className="w-full min-w-[1120px] text-sm"><thead className="border-b border-slate-800 text-left text-xs uppercase tracking-wide text-slate-500"><tr><th className="w-12 px-4 py-3"></th><th className="w-72 px-3 py-3">Модель</th><th className="px-3 py-3">Доступность, 4 дня</th><th className="w-72 px-3 py-3">Статус</th><th className="w-14 px-3 py-3">Тест</th></tr></thead><tbody>{rows.map((row) => <tr key={row.model} draggable onDragStart={() => setDragged(row.model)} onDragOver={(event) => event.preventDefault()} onDrop={() => void drop(row.model)} className="border-b border-slate-800/80 last:border-0 hover:bg-slate-800/40"><td className="cursor-grab px-4 text-slate-600 active:cursor-grabbing">::</td><td className="px-3 py-3 font-mono text-xs text-slate-200"><label className="flex items-center gap-3"><input type="checkbox" checked={row.enabled} onChange={() => void toggle(row)} className="size-4 accent-indigo-500"/><span className="truncate" title={row.model}>{row.name || row.model}</span></label></td><td className="px-3 py-3"><div className="flex h-7 min-w-[500px] items-stretch gap-px">{(row.hours ?? []).map((hour) => <AvailabilityBar key={hour.hour} hour={hour}/>)}</div></td><td className={`max-w-72 truncate px-3 py-3 text-xs ${row.status === 'ок' ? 'text-emerald-400' : row.status === 'нет данных' ? 'text-slate-400' : 'text-red-400'}`} title={row.status_detail || row.status}>{row.status}</td><td className="px-3 py-3"><button onClick={() => void test(row)} disabled={testing === row.model} title="Запустить ручную проверку" className="rounded-md p-2 text-slate-400 transition hover:bg-indigo-500/20 hover:text-indigo-300 disabled:animate-pulse disabled:opacity-50">{testing === row.model ? '...' : '↻'}</button></td></tr>)}</tbody></table></div>}</section>
}
