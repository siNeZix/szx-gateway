import {
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnDef,
  type SortingState,
} from '@tanstack/react-table'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { api } from '../api/client'
import { StatusBadge } from '../components/ui/basics'
import { useProvider } from '../providers/provider'
import type { KeyUsageStats } from '../types'

const STATUS_FILTERS = ['all', 'active', 'unchecked', 'rate_limited', 'day_exhausted', 'disabled', 'invalid']
const PAGE_SIZE = 50

function formatLastUsed(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString()
}

export default function Keys() {
  const { provider } = useProvider()
  const queryClient = useQueryClient()

  const [statusFilter, setStatusFilter] = useState('all')
  const [search, setSearch] = useState('')
  const [sorting, setSorting] = useState<SortingState>([
    { id: 'today_usage', desc: true },
  ])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [addText, setAddText] = useState('')

  const { data: keys = [], isLoading } = useQuery({
    queryKey: ['keys', provider, statusFilter],
    queryFn: () => api.keys(provider, statusFilter === 'all' ? '' : statusFilter),
    refetchInterval: 5_000,
  })

  // Локальный full-text-поиск по masked_key.
  const filtered = useMemo(() => {
    if (!search.trim()) return keys
    const q = search.toLowerCase()
    return keys.filter((k) => k.masked_key.toLowerCase().includes(q))
  }, [keys, search])

  const columns = useMemo<ColumnDef<KeyUsageStats>[]>(
    () => [
      {
        id: 'select',
        header: () => (
          <input
            type="checkbox"
            checked={selected.size === filtered.length && filtered.length > 0}
            onChange={(e) => {
              if (e.target.checked) {
                setSelected(new Set(filtered.map((k) => k.key_hash)))
              } else {
                setSelected(new Set())
              }
            }}
            className="accent-indigo-500"
          />
        ),
        cell: ({ row }) => (
          <input
            type="checkbox"
            checked={selected.has(row.original.key_hash)}
            onChange={(e) => {
              setSelected((prev) => {
                const next = new Set(prev)
                if (e.target.checked) next.add(row.original.key_hash)
                else next.delete(row.original.key_hash)
                return next
              })
            }}
            className="accent-indigo-500"
          />
        ),
        enableSorting: false,
      },
      {
        accessorKey: 'masked_key',
        header: 'Ключ',
        cell: (info) => (
          <span className="font-mono text-xs text-slate-200">
            {info.getValue() as string}
          </span>
        ),
      },
      {
        accessorKey: 'status',
        header: 'Статус',
        cell: (info) => <StatusBadge status={info.getValue() as string} />,
      },
      {
        accessorKey: 'today_usage',
        header: 'Сегодня',
        cell: (info) => (
          <span className="tabular-nums">{info.getValue() as number}</span>
        ),
      },
      {
        id: 'limit',
        header: 'Лимит',
        accessorFn: (k) => k.limit,
        cell: (info) => {
          const k = info.row.original
          const nearLimit = k.limit > 0 && k.limit - k.today_usage <= 2
          return (
            <span className={`tabular-nums ${nearLimit ? 'text-amber-400' : 'text-slate-400'}`}>
              {k.today_usage}/{k.limit || '∞'}
            </span>
          )
        },
      },
      {
        accessorKey: 'total_requests',
        header: 'Всего',
        cell: (info) => (
          <span className="tabular-nums text-slate-400">
            {info.getValue() as number}
          </span>
        ),
      },
      {
        id: 'error_rate',
        header: 'Ошибок',
        accessorFn: (k) => (k.total_requests > 0 ? k.error_requests / k.total_requests : -1),
        cell: (info) => {
          const k = info.row.original
          const rate = k.total_requests > 0 ? (k.error_requests / k.total_requests) * 100 : 0
          return (
            <span className={`tabular-nums ${rate > 10 ? 'text-rose-400' : 'text-slate-500'}`}>
              {k.total_requests > 0 ? `${rate.toFixed(1)}%` : '—'}
            </span>
          )
        },
      },
      {
        accessorKey: 'cooldown_left',
        header: 'Cooldown',
        cell: (info) => {
          const v = info.getValue() as string
          return v ? (
            <span className="text-amber-400">{v}</span>
          ) : (
            <span className="text-slate-600">—</span>
          )
        },
      },
      {
        accessorKey: 'last_used_at',
        header: 'Last used',
        cell: (info) => (
          <span className="whitespace-nowrap text-xs text-slate-400">
            {formatLastUsed(info.getValue() as string)}
          </span>
        ),
      },
    ],
    [selected, filtered],
  )

  const table = useReactTable({
    data: filtered,
    columns,
    state: { sorting, globalFilter: search },
    onSortingChange: setSorting,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: PAGE_SIZE } },
  })

  // Мутации
  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['keys', provider] })
    queryClient.invalidateQueries({ queryKey: ['stats', provider] })
  }

  const bulkMutation = useMutation({
    mutationFn: (vars: { action: 'enable' | 'disable' | 'delete' }) =>
      api.bulkKeys(provider, [...selected], vars.action),
    onSuccess: () => {
      setSelected(new Set())
      invalidate()
    },
  })

  const addMutation = useMutation({
    mutationFn: (raw: string[]) => api.addKeys(provider, raw),
    onSuccess: () => {
      setAddText('')
      invalidate()
    },
  })

  const handleAdd = () => {
    const list = addText
      .split('\n')
      .map((s) => s.trim())
      .filter((s) => s && !s.startsWith('#') && !s.startsWith('//'))
    if (list.length === 0) return
    addMutation.mutate(list)
  }

  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold text-white">
        Ключи ({filtered.length})
      </h2>

      {/* Добавление пачкой */}
      <div className="rounded-xl border border-slate-800 bg-slate-800/30 p-3">
        <details>
          <summary className="cursor-pointer text-sm font-medium text-slate-300">
            Добавить ключи пачкой
          </summary>
          <div className="mt-3 space-y-2">
            <textarea
              value={addText}
              onChange={(e) => setAddText(e.target.value)}
              placeholder={'sk-or-v1-...\nsk-or-v1-...\n# комментарии игнорируются'}
              rows={5}
              className="w-full rounded-lg border border-slate-700 bg-slate-900 p-2 font-mono text-xs text-slate-200 outline-none focus:border-indigo-500"
            />
            <button
              onClick={handleAdd}
              disabled={addMutation.isPending}
              className="rounded-lg bg-indigo-600 px-4 py-1.5 text-sm font-medium text-white transition hover:bg-indigo-500 disabled:opacity-50"
            >
              {addMutation.isPending ? 'Добавляю…' : 'Добавить'}
            </button>
            {addMutation.data && (
              <span className="ml-3 text-xs text-emerald-400">
                Добавлено: {addMutation.data.added}
              </span>
            )}
            {addMutation.error && (
              <span className="ml-3 text-xs text-rose-400">
                {(addMutation.error as Error).message}
              </span>
            )}
          </div>
        </details>
      </div>

      {/* Bulk-бар */}
      {selected.size > 0 && (
        <div className="sticky top-0 z-10 flex items-center gap-3 rounded-lg border border-indigo-700/40 bg-indigo-950/60 px-4 py-2">
          <span className="text-sm font-medium text-indigo-200">
            Выбрано: {selected.size}
          </span>
          <button
            onClick={() => bulkMutation.mutate({ action: 'enable' })}
            disabled={bulkMutation.isPending}
            className="rounded bg-emerald-600 px-2.5 py-1 text-xs font-semibold text-white transition hover:bg-emerald-500"
          >
            Включить
          </button>
          <button
            onClick={() => bulkMutation.mutate({ action: 'disable' })}
            disabled={bulkMutation.isPending}
            className="rounded bg-slate-700 px-2.5 py-1 text-xs font-semibold text-white transition hover:bg-slate-600"
          >
            Отключить
          </button>
          <button
            onClick={() => {
              if (confirm(`Удалить ${selected.size} ключей безвозвратно?`)) {
                bulkMutation.mutate({ action: 'delete' })
              }
            }}
            disabled={bulkMutation.isPending}
            className="rounded bg-rose-600 px-2.5 py-1 text-xs font-semibold text-white transition hover:bg-rose-500"
          >
            Удалить
          </button>
        </div>
      )}

      {/* Фильтры */}
      <div className="flex flex-wrap items-center gap-2">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            onClick={() => setStatusFilter(s)}
            className={`rounded-md px-2.5 py-1 text-xs font-medium transition ${
              statusFilter === s
                ? 'bg-indigo-600 text-white'
                : 'bg-slate-800 text-slate-400 hover:text-white'
            }`}
          >
            {s}
          </button>
        ))}
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="поиск…"
          className="ml-auto w-48 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200 outline-none focus:border-indigo-500"
        />
      </div>

      {/* Таблица */}
      <div className="overflow-auto rounded-xl border border-slate-800">
        <table className="w-full text-sm">
          <thead className="bg-slate-800/60 text-xs uppercase text-slate-400">
            {table.getHeaderGroups().map((hg) => (
              <tr key={hg.id}>
                {hg.headers.map((header) => (
                  <th
                    key={header.id}
                    onClick={header.column.getToggleSortingHandler()}
                    className={`px-3 py-2 text-left ${
                      header.column.getCanSort() ? 'cursor-pointer select-none' : ''
                    }`}
                  >
                    {flexRender(
                      header.column.columnDef.header,
                      header.getContext(),
                    )}
                    {header.column.getIsSorted() === 'asc' && ' ↑'}
                    {header.column.getIsSorted() === 'desc' && ' ↓'}
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td
                  colSpan={columns.length}
                  className="px-3 py-8 text-center text-slate-500"
                >
                  Загрузка…
                </td>
              </tr>
            ) : table.getRowModel().rows.length === 0 ? (
              <tr>
                <td
                  colSpan={columns.length}
                  className="px-3 py-8 text-center text-slate-500"
                >
                  Нет ключей
                </td>
              </tr>
            ) : (
              table.getRowModel().rows.map((row) => (
                <tr
                  key={row.id}
                  className={`border-t border-slate-800 ${
                    selected.has(row.original.key_hash) ? 'bg-indigo-950/30' : ''
                  }`}
                >
                  {row.getVisibleCells().map((cell) => (
                    <td key={cell.id} className="px-3 py-2">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* Пагинация */}
      {table.getPageCount() > 1 && (
        <div className="flex items-center gap-2 text-sm text-slate-400">
          <button
            onClick={() => table.previousPage()}
            disabled={!table.getCanPreviousPage()}
            className="rounded-md border border-slate-700 px-3 py-1 disabled:opacity-30"
          >
            ←
          </button>
          <span>
            Стр. {table.getState().pagination.pageIndex + 1} /{' '}
            {table.getPageCount()}
          </span>
          <button
            onClick={() => table.nextPage()}
            disabled={!table.getCanNextPage()}
            className="rounded-md border border-slate-700 px-3 py-1 disabled:opacity-30"
          >
            →
          </button>
        </div>
      )}
    </div>
  )
}
