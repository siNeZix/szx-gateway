import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
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
import { useProvider } from '../providers/provider'

const RANGES = [
  { label: '7 дней', days: 7 },
  { label: '14 дней', days: 14 },
  { label: '30 дней', days: 30 },
]

export default function Stats() {
  const { provider } = useProvider()
  const [days, setDays] = useState(14)

  const { data: trend = [], isLoading } = useQuery({
    queryKey: ['statsUsage', provider, days],
    queryFn: () => api.statsUsage(provider, days),
    refetchInterval: 30_000,
  })

  const { data: requests = [], isLoading: requestsLoading } = useQuery({
    queryKey: ['requestLog', provider],
    queryFn: () => api.requestLog(provider, 100),
    refetchInterval: 5_000,
  })

  const exportLogs = async () => {
    const logs = await api.requestLog(provider, 500)
    const blob = new Blob([JSON.stringify(logs, null, 2)], {
      type: 'application/json',
    })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `gateway-logs-${provider}-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.json`
    link.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-white">
          Статистика использования ({provider})
        </h2>
        <div className="flex gap-1">
          <button
            onClick={exportLogs}
            disabled={requests.length === 0}
            className="rounded-md bg-slate-800 px-3 py-1 text-xs font-medium text-slate-300 transition hover:text-white disabled:cursor-not-allowed disabled:opacity-50"
          >
            Экспорт логов
          </button>
          {RANGES.map((r) => (
            <button
              key={r.days}
              onClick={() => setDays(r.days)}
              className={`rounded-md px-3 py-1 text-xs font-medium transition ${
                days === r.days
                  ? 'bg-indigo-600 text-white'
                  : 'bg-slate-800 text-slate-400 hover:text-white'
              }`}
            >
              {r.label}
            </button>
          ))}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <Chart
          title="Запросы по дням"
          isLoading={isLoading}
          data={trend}
          dataKey="requests"
          color="#818cf8"
        />
        <Chart
          title="Токены по дням"
          isLoading={isLoading}
          data={trend}
          dataKey="tokens"
          color="#34d399"
        />
        <Chart
          title="Средняя задержка (ms)"
          isLoading={isLoading}
          data={trend}
          dataKey="latency_avg_ms"
          color="#fbbf24"
        />
        <Chart
          title="Ошибки по дням"
          isLoading={isLoading}
          data={trend}
          dataKey="errors"
          color="#fb7185"
        />
      </div>

      <div className="overflow-hidden rounded-xl border border-slate-800">
        <div className="border-b border-slate-800 bg-slate-800/40 px-4 py-3">
          <h3 className="text-sm font-medium text-slate-300">
            Последние запросы
          </h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-slate-900/70 text-xs uppercase text-slate-400">
              <tr>
                <th className="px-4 py-2 text-left">Время</th>
                <th className="px-4 py-2 text-left">Модель</th>
                <th className="px-4 py-2 text-left">Ошибка</th>
                <th className="px-4 py-2 text-right">Код</th>
                <th className="px-4 py-2 text-right">Latency</th>
                <th className="px-4 py-2 text-right">TTFT</th>
                <th className="px-4 py-2 text-right">Токены</th>
                <th className="px-4 py-2 text-right">Stream</th>
              </tr>
            </thead>
            <tbody>
              {requests.map((r) => (
                <tr key={r.id} className="border-t border-slate-800">
                  <td className="whitespace-nowrap px-4 py-2 text-xs text-slate-400">
                    {new Date(r.timestamp).toLocaleString()}
                  </td>
                  <td className="max-w-[360px] truncate px-4 py-2 font-mono text-xs text-slate-200">
                    {r.model}
                  </td>
                  <td className="max-w-[280px] truncate px-4 py-2 text-xs text-rose-300" title={r.error_msg}>
                    {r.error_msg || '—'}
                  </td>
                  <td className={`px-4 py-2 text-right tabular-nums ${r.status_code >= 400 || r.status_code === 0 ? 'text-rose-400' : 'text-emerald-400'}`}>
                    {r.status_code || 'net'}
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-300">
                    {r.latency_ms} ms
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-400">
                    {r.ttft_ms} ms
                  </td>
                  <td className="px-4 py-2 text-right tabular-nums text-slate-400">
                    {r.tokens}
                  </td>
                  <td className="px-4 py-2 text-right text-slate-400">
                    {r.is_stream ? 'да' : 'нет'}
                  </td>
                </tr>
              ))}
              {!requestsLoading && requests.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-6 text-center text-slate-500">
                    Нет данных
                  </td>
                </tr>
              )}
              {requestsLoading && requests.length === 0 && (
                <tr>
                  <td colSpan={8} className="px-4 py-6 text-center text-slate-500">
                    Загрузка…
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

function Chart({
  title,
  isLoading,
  data,
  dataKey,
  color,
}: {
  title: string
  isLoading: boolean
  data: { day: string }[]
  dataKey: string
  color: string
}) {
  return (
    <div className="rounded-xl border border-slate-800 bg-slate-800/30 p-4">
      <h3 className="mb-3 text-sm font-medium text-slate-300">{title}</h3>
      {isLoading ? (
        <div className="h-48 animate-pulse rounded-lg bg-slate-800/60" />
      ) : data.length === 0 ? (
        <div className="flex h-48 items-center justify-center text-sm text-slate-500">
          Нет данных
        </div>
      ) : (
        <ResponsiveContainer width="100%" height={192}>
          <LineChart data={data}>
            <CartesianGrid stroke="#1e293b" strokeDasharray="3 3" />
            <XAxis
              dataKey="day"
              stroke="#64748b"
              fontSize={11}
              tickLine={false}
            />
            <YAxis stroke="#64748b" fontSize={11} tickLine={false} />
            <Tooltip
              contentStyle={{
                backgroundColor: '#1e293b',
                border: '1px solid #334155',
                borderRadius: 8,
                fontSize: 12,
              }}
            />
            <Line
              type="monotone"
              dataKey={dataKey}
              stroke={color}
              strokeWidth={2}
              dot={false}
            />
          </LineChart>
        </ResponsiveContainer>
      )}
    </div>
  )
}
