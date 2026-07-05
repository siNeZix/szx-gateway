import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'
import { api } from '../api/client'
import { StatusBadge } from '../components/ui/basics'
import type { Provider, ProxySettings } from '../types'

const providers: Provider[] = ['openrouter', 'aihubmix']

function fmt(value: string) {
  if (!value) return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString()
}

export default function Proxies() {
  const queryClient = useQueryClient()
  const [text, setText] = useState('')
  const [scheme, setScheme] = useState<'http' | 'https' | 'socks5'>('http')
  const [selected, setSelected] = useState<Set<number>>(new Set())

  const { data: proxies = [], isLoading } = useQuery({
    queryKey: ['proxies'],
    queryFn: api.proxies,
    refetchInterval: 10_000,
  })
  const { data: settings = [] } = useQuery({
    queryKey: ['proxy-settings'],
    queryFn: api.proxySettings,
  })

  const { data: logs = [] } = useQuery({
    queryKey: ['proxy-logs'],
    queryFn: () => api.proxyLogs(100),
    refetchInterval: 5_000,
  })

  const { data: stats } = useQuery({
    queryKey: ['proxy-stats'],
    queryFn: api.proxyStats,
    refetchInterval: 30_000,
  })

  const { data: usage = [] } = useQuery({
    queryKey: ['proxy-usage-5m'],
    queryFn: api.proxyUsage5m,
    refetchInterval: 30_000,
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['proxies'] })
    queryClient.invalidateQueries({ queryKey: ['proxy-settings'] })
  }

  const add = useMutation({
    mutationFn: () =>
      api.addProxies(
        scheme,
        text.split('\n').map((s) => s.trim()).filter(Boolean),
      ),
    onSuccess: () => {
      setText('')
      invalidate()
    },
  })

  const bulk = useMutation({
    mutationFn: (action: 'enable' | 'disable' | 'delete' | 'recheck') =>
      api.bulkProxies([...selected], action),
    onSuccess: () => {
      setSelected(new Set())
      invalidate()
    },
  })

  const saveSettings = useMutation({
    mutationFn: api.saveProxySettings,
    onSuccess: invalidate,
  })

  const updateSetting = (provider: Provider, patch: Partial<ProxySettings>) => {
    const current = settings.find((s) => s.provider === provider) ?? {
      provider,
      use_for_checker: false,
      use_for_requests: false,
      mode: 'after_429' as const,
    }
    saveSettings.mutate({ ...current, ...patch })
  }

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold text-white">Прокси ({proxies.length})</h2>

      <div className="grid gap-3 md:grid-cols-4">
        <Stat label="Запросов за 24ч" value={stats?.requests ?? 0} />
        <Stat label="Успешных" value={stats?.successes ?? 0} />
        <Stat label="Средний размер" value={`${(stats?.avg_kb ?? 0).toFixed(1)} KB`} />
        <Stat label="Средняя задержка" value={`${stats?.avg_latency_ms ?? 0} ms`} />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Chart title="Килобайты за сутки, шаг 5 минут" data={usage} dataKey="kilobytes" color="#34d399" />
        <Chart title="Задержка за сутки, шаг 5 минут" data={usage} dataKey="latency_avg_ms" color="#fbbf24" />
      </div>

      <details className="rounded-xl border border-slate-800 bg-slate-800/30 p-4">
        <summary className="cursor-pointer text-sm font-medium text-slate-300">Добавить пачкой</summary>
        <div className="mt-3">
          <select
            value={scheme}
            onChange={(e) => setScheme(e.target.value as typeof scheme)}
            className="mb-3 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
          >
            <option value="http">http</option>
            <option value="https">https</option>
            <option value="socks5">socks5</option>
          </select>
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            rows={5}
            placeholder={'ip:port:user:pass\nuser:pass@ip:port\nsocks5://user:pass@ip:port'}
            className="w-full rounded-lg border border-slate-700 bg-slate-900 p-2 font-mono text-xs text-slate-200 outline-none focus:border-indigo-500"
          />
          <button
            onClick={() => add.mutate()}
            disabled={add.isPending || !text.trim()}
            className="mt-3 rounded-lg bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:opacity-50"
          >
            {add.isPending ? 'Проверяю…' : 'Добавить и проверить'}
          </button>
          {add.data && <span className="ml-3 text-xs text-emerald-400">Добавлено: {add.data.added}, проверено: {add.data.checked}</span>}
          {add.error && <span className="ml-3 text-xs text-rose-400">{(add.error as Error).message}</span>}
        </div>
      </details>

      <div className="grid gap-3 md:grid-cols-2">
        {providers.map((provider) => {
          const s = settings.find((x) => x.provider === provider)
          return (
            <div key={provider} className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
              <div className="mb-3 text-sm font-semibold text-white">{provider}</div>
              <label className="mb-2 flex items-center gap-2 text-sm text-slate-300">
                <input type="checkbox" checked={!!s?.use_for_checker} onChange={(e) => updateSetting(provider, { use_for_checker: e.target.checked })} className="accent-indigo-500" />
                Чек ключей
              </label>
              <label className="mb-3 flex items-center gap-2 text-sm text-slate-300">
                <input type="checkbox" checked={!!s?.use_for_requests} onChange={(e) => updateSetting(provider, { use_for_requests: e.target.checked })} className="accent-indigo-500" />
                Боевые запросы
              </label>
              <select
                value={s?.mode ?? 'after_429'}
                onChange={(e) => updateSetting(provider, { mode: e.target.value as ProxySettings['mode'] })}
                className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
              >
                <option value="after_429">только после 429</option>
                <option value="always">всегда</option>
              </select>
            </div>
          )
        })}
      </div>

      {selected.size > 0 && (
        <div className="sticky top-0 z-10 flex flex-wrap items-center gap-3 rounded-lg border border-indigo-700/40 bg-indigo-950/60 px-4 py-2">
          <span className="text-sm font-medium text-indigo-200">Выбрано: {selected.size}</span>
          {(['enable', 'disable', 'recheck'] as const).map((action) => (
            <button key={action} onClick={() => bulk.mutate(action)} disabled={bulk.isPending} className="rounded bg-slate-700 px-2.5 py-1 text-xs font-semibold text-white transition hover:bg-slate-600">
              {action === 'enable' ? 'Включить' : action === 'disable' ? 'Отключить' : 'Перепроверить'}
            </button>
          ))}
          <button
            onClick={() => confirm(`Удалить ${selected.size} прокси?`) && bulk.mutate('delete')}
            disabled={bulk.isPending}
            className="rounded bg-rose-600 px-2.5 py-1 text-xs font-semibold text-white transition hover:bg-rose-500"
          >
            Удалить
          </button>
        </div>
      )}

      <details className="rounded-xl border border-slate-800">
        <summary className="cursor-pointer bg-slate-800/40 px-4 py-3 text-sm font-medium text-slate-300">Список прокси</summary>
        <div className="overflow-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-800/60 text-xs uppercase text-slate-400">
              <tr>
                <th className="px-3 py-2 text-left"><input type="checkbox" checked={selected.size === proxies.length && proxies.length > 0} onChange={(e) => setSelected(e.target.checked ? new Set(proxies.map((p) => p.id)) : new Set())} className="accent-indigo-500" /></th>
                <th className="px-3 py-2 text-left">Прокси</th>
                <th className="px-3 py-2 text-left">Статус</th>
                <th className="px-3 py-2 text-left">Проверен</th>
                <th className="px-3 py-2 text-left">Ошибка</th>
              </tr>
            </thead>
            <tbody>
              {isLoading ? (
                <tr><td colSpan={5} className="px-3 py-8 text-center text-slate-500">Загрузка…</td></tr>
              ) : proxies.length === 0 ? (
                <tr><td colSpan={5} className="px-3 py-8 text-center text-slate-500">Нет прокси</td></tr>
              ) : proxies.map((p) => (
                <tr key={p.id} className={`border-t border-slate-800 ${selected.has(p.id) ? 'bg-indigo-950/30' : ''}`}>
                  <td className="px-3 py-2"><input type="checkbox" checked={selected.has(p.id)} onChange={(e) => setSelected((prev) => { const next = new Set(prev); e.target.checked ? next.add(p.id) : next.delete(p.id); return next })} className="accent-indigo-500" /></td>
                  <td className="px-3 py-2 font-mono text-xs text-slate-200">{p.scheme}://{p.username ? `${p.username}:***@` : ''}{p.host}:{p.port}</td>
                  <td className="px-3 py-2"><StatusBadge status={p.status} /></td>
                  <td className="px-3 py-2 whitespace-nowrap text-xs text-slate-400">{fmt(p.last_checked_at)}</td>
                  <td className="max-w-md truncate px-3 py-2 text-xs text-rose-300" title={p.last_error}>{p.last_error || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>

      <div className="overflow-hidden rounded-xl border border-slate-800">
        <div className="border-b border-slate-800 bg-slate-800/40 px-4 py-3">
          <h3 className="text-sm font-medium text-slate-300">Логи прокси</h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-900/70 text-xs uppercase text-slate-400">
              <tr>
                <th className="px-4 py-2 text-left">Время</th>
                <th className="px-4 py-2 text-left">Провайдер</th>
                <th className="px-4 py-2 text-left">Тип</th>
                <th className="px-4 py-2 text-right">KB</th>
                <th className="px-4 py-2 text-right">Latency</th>
                <th className="px-4 py-2 text-left">Статус</th>
                <th className="px-4 py-2 text-left">Ошибка</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((l) => (
                <tr key={l.id} className="border-t border-slate-800">
                  <td className="whitespace-nowrap px-4 py-2 text-xs text-slate-400">{fmt(l.timestamp)}</td>
                  <td className="px-4 py-2 text-slate-300">{l.provider}</td>
                  <td className="px-4 py-2 text-slate-400">{l.use_case}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-300">{((l.request_bytes + l.response_bytes) / 1024).toFixed(1)}</td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-300">{l.latency_ms} ms</td>
                  <td className={l.success ? 'px-4 py-2 text-emerald-400' : 'px-4 py-2 text-rose-400'}>{l.success ? 'успех' : 'ошибка'}</td>
                  <td className="max-w-[360px] truncate px-4 py-2 text-xs text-rose-300" title={l.error_msg}>{l.error_msg || '—'}</td>
                </tr>
              ))}
              {logs.length === 0 && <tr><td colSpan={7} className="px-4 py-6 text-center text-slate-500">Нет логов</td></tr>}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-900/60 p-4">
      <div className="text-xs text-slate-500">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums text-white">{value}</div>
    </div>
  )
}

function Chart({ title, data, dataKey, color }: { title: string; data: { bucket: string; kilobytes: number; latency_avg_ms: number }[]; dataKey: string; color: string }) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-800/30 p-4">
      <h3 className="mb-3 text-sm font-medium text-slate-300">{title}</h3>
      {data.length === 0 ? (
        <div className="flex h-48 items-center justify-center text-sm text-slate-500">Нет данных</div>
      ) : (
        <ResponsiveContainer width="100%" height={192}>
          <LineChart data={data}>
            <CartesianGrid stroke="#1e293b" strokeDasharray="3 3" />
            <XAxis dataKey="bucket" stroke="#64748b" fontSize={11} tickLine={false} tickFormatter={(v) => new Date(String(v)).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })} />
            <YAxis stroke="#64748b" fontSize={11} tickLine={false} />
            <Tooltip contentStyle={{ backgroundColor: '#020617', border: '1px solid #1e293b', borderRadius: 8, color: '#e2e8f0' }} labelFormatter={(v) => new Date(String(v)).toLocaleString()} />
            <Line type="monotone" dataKey={dataKey} stroke={color} strokeWidth={2} dot={false} />
          </LineChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
