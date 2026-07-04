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

  // ponytail: aihubmix пишет в model_usage, openrouter — нет. Графики пустые для OR,
  // это норма. Текстовая подсказка объясняет.
  const noData = provider === 'openrouter'

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-white">
          Статистика использования ({provider})
        </h2>
        <div className="flex gap-1">
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

      {noData && (
        <div className="rounded-lg border border-amber-700/40 bg-amber-950/30 px-4 py-2 text-sm text-amber-300">
          Тренд использования доступен только для AIHubMix. Для OpenRouter
          метрики не пишутся в model_usage.
        </div>
      )}

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
