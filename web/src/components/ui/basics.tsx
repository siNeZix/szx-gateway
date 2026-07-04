import type { ReactNode } from 'react'

// Карточка сводки для дашборда.
export function StatCard({
  label,
  value,
  hint,
  tone = 'default',
}: {
  label: string
  value: ReactNode
  hint?: string
  tone?: 'default' | 'good' | 'warn' | 'bad'
}) {
  const toneColor = {
    default: 'text-slate-100',
    good: 'text-emerald-400',
    warn: 'text-amber-400',
    bad: 'text-rose-400',
  }[tone]

  return (
    <div className="rounded-xl border border-slate-800 bg-slate-800/40 p-4">
      <div className="text-xs font-medium uppercase tracking-wide text-slate-400">
        {label}
      </div>
      <div className={`mt-1 text-2xl font-semibold ${toneColor}`}>{value}</div>
      {hint && <div className="mt-0.5 text-xs text-slate-500">{hint}</div>}
    </div>
  )
}

// Бейдж статуса ключа с цветом.
const STATUS_STYLES: Record<string, string> = {
  active: 'bg-emerald-500/15 text-emerald-300',
  unchecked: 'bg-slate-500/15 text-slate-300',
  rate_limited: 'bg-amber-500/15 text-amber-300',
  day_exhausted: 'bg-rose-500/15 text-rose-300',
  invalid: 'bg-rose-700/15 text-rose-400',
  disabled: 'bg-slate-700/40 text-slate-500',
}

export function StatusBadge({ status }: { status: string }) {
  const cls = STATUS_STYLES[status] ?? 'bg-slate-500/15 text-slate-300'
  return (
    <span
      className={`inline-block rounded-md px-2 py-0.5 text-xs font-medium ${cls}`}
    >
      {status}
    </span>
  )
}
