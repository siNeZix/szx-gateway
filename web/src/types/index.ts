// Контракты API. Ручная синхронизация с Go-структурами в internal/store/sqlite.go
// и internal/web/api.go. Единый конверт { data, error } разворачивается в apiClient.

export type Provider = 'openrouter' | 'aihubmix'

export interface GeneralStats {
  total_requests: number
  today_requests: number
  active_keys: number
  blocked_keys: number
  invalid_keys: number
  unchecked_keys: number
  total_keys: number
}

export interface ModelStats {
  model: string
  today_requests: number
  total_requests: number
  avg_latency_ms: number
  total_tokens: number
}

export interface KeyUsageStats {
  masked_key: string
  key_hash: string
  status: string
  today_usage: number
  limit: number
  total_requests: number
  error_requests: number
  cooldown_left: string
  cooldown_until: string // ISO-время; пусто если never
  last_used_at: string // ISO-время; пусто если never
}

export interface DailyLimitsInfo {
  total: number
  used: number
  remaining: number
  source: string
  models: unknown[]
}

export interface ModelUsageTrend {
  day: string
  requests: number
  tokens: number
  latency_avg_ms: number
  errors: number
}

export interface UsageBucket {
  bucket: string
  requests: number
  tokens: number
  latency_avg_ms: number
  errors: number
}

export interface RequestLogItem {
  id: number
  timestamp: string
  provider: Provider
  key_hash: string
  model: string
  status_code: number
  status_text: string
  tokens: number
  latency_ms: number
  ttft_ms: number
  is_stream: boolean
}

export interface DBModel {
  id: string
  name: string
  rank: number
  context_length: number
  max_output: number
  type: string
  features: string
  modalities: string
  input_price: number
  output_price: number
  description: string
  updated_at: string
}

export interface ProviderInfo {
  id: string
  name: string
  base_url: string
  total_keys: number
  active_keys: number
}

// Обёрнутый ответ /api/v2/stats (общий снимок).
export interface StatsSnapshot {
  general: GeneralStats
  models: ModelStats[]
  keys: KeyUsageStats[]
  daily_limits: DailyLimitsInfo
  top_models: DBModel[]
  free_models: DBModel[]
  usage_trend: ModelUsageTrend[]
  refreshed_at: string
}

// Ответ /api/v2/models.
export interface ModelsResponse {
  top_models: DBModel[]
  free_models: DBModel[]
}

export interface ProxyItem {
  id: number
  raw: string
  scheme: 'http' | 'https' | 'socks5'
  host: string
  port: string
  username: string
  status: string
  last_checked_at: string
  last_error: string
  created_at: string
}

export interface ProxySettings {
  provider: Provider
  use_for_checker: boolean
  use_for_requests: boolean
  mode: 'always' | 'after_429'
}

export interface ProxyLogItem {
  id: number
  timestamp: string
  proxy_id: number
  provider: Provider
  use_case: string
  success: boolean
  request_bytes: number
  response_bytes: number
  latency_ms: number
  error_msg: string
}

export interface ProxyStats {
  requests: number
  successes: number
  avg_kb: number
  avg_latency_ms: number
}

export interface ProxyUsageBucket {
  bucket: string
  kilobytes: number
  latency_avg_ms: number
  requests: number
  errors: number
}
