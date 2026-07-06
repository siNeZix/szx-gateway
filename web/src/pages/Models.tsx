import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { useProvider } from '../providers/provider'

export default function Models() {
  const { provider } = useProvider()
  const { data, isLoading } = useQuery({
    queryKey: ['models', provider],
    queryFn: () => api.models(provider),
  })
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<'name' | 'date' | 'context'>('name')

  const free = data?.free_models ?? []
  const top = data?.top_models ?? []

  const filteredFree = useMemo(() => {
    const q = search.toLowerCase()
    return free
      .filter(
        (m) =>
          !q.trim() ||
          m.id.toLowerCase().includes(q) ||
          m.name.toLowerCase().includes(q),
      )
      .toSorted((a, b) => {
        if (sort === 'context') {
          return b.context_length - a.context_length || a.id.localeCompare(b.id)
        }
        if (sort === 'date') {
          return (
            Date.parse(b.updated_at) - Date.parse(a.updated_at) ||
            a.id.localeCompare(b.id)
          )
        }
        return (a.name || a.id).localeCompare(b.name || b.id) || a.id.localeCompare(b.id)
      })
  }, [free, search, sort])

  const copyMarkdown = () => {
    const lines = [
      '| id | context | max_output | modalities | features | input_price | output_price |',
      '| --- | ---: | ---: | --- | --- | ---: | ---: |',
    ]
    filteredFree.forEach((m) => {
      lines.push(
        `| ${m.id} | ${m.context_length} | ${m.max_output} | ${m.modalities} | ${m.features} | ${m.input_price} | ${m.output_price} |`,
      )
    })
    navigator.clipboard.writeText(lines.join('\n'))
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-lg font-semibold text-white">
          Модели ({provider})
        </h2>
        <div className="flex gap-2">
          <select
            value={sort}
            onChange={(e) => setSort(e.target.value as typeof sort)}
            className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200 outline-none focus:border-indigo-500"
          >
            <option value="name">по имени</option>
            <option value="date">по дате</option>
            <option value="context">по контексту</option>
          </select>
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="поиск…"
            className="w-48 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200 outline-none focus:border-indigo-500"
          />
        </div>
      </div>

      {top.length > 0 && (
        <div>
          <h3 className="mb-2 text-sm font-medium text-slate-300">
            Топ моделей (Shir-Man)
          </h3>
          <div className="flex flex-wrap gap-2">
            {top.map((m) => (
              <div
                key={m.id}
                className="rounded-lg border border-slate-800 bg-slate-800/40 px-3 py-2"
              >
                <div className="text-xs font-semibold text-amber-400">
                  #{m.rank}
                </div>
                <div className="font-mono text-xs text-slate-200">{m.id}</div>
                <div className="text-xs text-slate-500">
                  ctx: {m.context_length.toLocaleString()}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      <div>
        <div className="mb-2 flex items-center justify-between">
          <h3 className="text-sm font-medium text-slate-300">
            Free-модели ({filteredFree.length})
          </h3>
          <button
            onClick={copyMarkdown}
            className="rounded-md border border-slate-700 px-3 py-1 text-xs text-slate-300 transition hover:border-indigo-500 hover:text-white"
          >
            Копировать таблицу
          </button>
        </div>

        {isLoading ? (
          <div className="text-slate-500">Загрузка…</div>
        ) : (
          <div className="overflow-auto rounded-xl border border-slate-800">
            <table className="w-full text-sm">
              <thead className="bg-slate-800/60 text-xs uppercase text-slate-400">
                <tr>
                  <th className="px-3 py-2 text-left">ID</th>
                  <th className="px-3 py-2 text-right">Context</th>
                  <th className="px-3 py-2 text-right">Max Output</th>
                  <th className="px-3 py-2 text-left">Modalities</th>
                  <th className="px-3 py-2 text-right">In</th>
                  <th className="px-3 py-2 text-right">Out</th>
                </tr>
              </thead>
              <tbody>
                {filteredFree.map((m) => (
                  <tr key={m.id} className="border-t border-slate-800">
                    <td className="px-3 py-2 font-mono text-xs text-slate-200">
                      {m.id}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-400">
                      {m.context_length.toLocaleString()}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-400">
                      {m.max_output.toLocaleString()}
                    </td>
                    <td className="px-3 py-2 text-xs text-slate-400">
                      {m.modalities}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-400">
                      {m.input_price}
                    </td>
                    <td className="px-3 py-2 text-right tabular-nums text-slate-400">
                      {m.output_price}
                    </td>
                  </tr>
                ))}
                {filteredFree.length === 0 && (
                  <tr>
                    <td
                      colSpan={6}
                      className="px-3 py-8 text-center text-slate-500"
                    >
                      Нет моделей
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
