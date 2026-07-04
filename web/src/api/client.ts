import type {
  KeyUsageStats,
  ModelStats,
  ModelsResponse,
  ModelUsageTrend,
  Provider,
  ProviderInfo,
  StatsSnapshot,
} from '../types'

// Единый API-клиент: разворачивает конверт { data, error } в данные либо бросает.
// Базовый путь относительный — и dev-прокси Vite, и prod (embed) на том же origin.

async function unwrap<T>(res: Response): Promise<T> {
  const body = (await res.json()) as { data?: T; error?: string }
  if (body.error) {
    throw new Error(body.error)
  }
  if (body.data === undefined) {
    throw new Error('empty response')
  }
  return body.data
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(path)
  if (!res.ok) {
    // Всё равно парсим — бэкенд возвращает { error } даже на 4xx/5xx.
    return unwrap<T>(res)
  }
  return unwrap<T>(res)
}

async function postJSON<T>(path: string, payload: unknown): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  })
  return unwrap<T>(res)
}

export const api = {
  providers: () => getJSON<ProviderInfo[]>('/api/v2/providers'),

  stats: (provider: Provider) =>
    getJSON<StatsSnapshot>(`/api/v2/stats?provider=${provider}`),

  statsModels: (provider: Provider) =>
    getJSON<ModelStats[]>(`/api/v2/stats/models?provider=${provider}`),

  statsUsage: (provider: Provider, days = 14) =>
    getJSON<ModelUsageTrend[]>(
      `/api/v2/stats/usage?provider=${provider}&range=${days}`,
    ),

  models: (provider: Provider) =>
    getJSON<ModelsResponse>(`/api/v2/models?provider=${provider}`),

  keys: (provider: Provider, status = '') =>
    getJSON<KeyUsageStats[]>(
      `/api/v2/keys?provider=${provider}${status ? `&status=${status}` : ''}`,
    ),

  addKeys: (provider: Provider, keys: string[]) =>
    postJSON<{ added: number }>('/api/v2/keys', { provider, keys }),

  bulkKeys: (
    provider: Provider,
    hashes: string[],
    action: 'enable' | 'disable' | 'delete',
  ) =>
    postJSON<{ action: string; affected: number }>('/api/v2/keys/bulk', {
      provider,
      hashes,
      action,
    }),
}
