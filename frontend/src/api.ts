const BASE = '/api';

export interface Provider {
  name: string;
  url: string;
  models: string[];
  weight: number;
  enabled: boolean;
  key_env?: string;
  timeout_seconds: number;
  health?: HealthStatus;
}

export interface HealthStatus {
  healthy: boolean;
  latency_ms: number;
  last_check: string;
  error?: string;
}

export interface Stats {
  total_requests: number;
  active_providers: number;
  avg_latency_ms: number;
  error_rate: number;
  by_provider: ProviderStats[];
  by_model: ModelStats[];
  recent_errors: ErrorEntry[];
  time_series: TimeSeriesPoint[];
}

export interface ProviderStats {
  name: string;
  total_requests: number;
  error_count: number;
  error_rate: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  rpm: number;
}

export interface ModelStats {
  name: string;
  total_requests: number;
  rpm: number;
}

export interface ErrorEntry {
  provider: string;
  model: string;
  status: number;
  message: string;
  timestamp: string;
}

export interface TimeSeriesPoint {
  timestamp: string;
  providers: Record<string, number>;
  latency: Record<string, number>;
}

export interface ProviderInput {
  name: string;
  url: string;
  models: string[];
  weight: number;
  enabled: boolean;
  api_key?: string;
  key_env?: string;
  timeout_seconds: number;
}

async function request<T>(path: string, opts?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(text || res.statusText);
  }
  return res.json();
}

export const api = {
  getProviders: () => request<Provider[]>('/providers'),
  getProvider: (name: string) => request<Provider>(`/providers/${name}`),
  addProvider: (p: ProviderInput) => request<Provider>('/providers', { method: 'POST', body: JSON.stringify(p) }),
  updateProvider: (name: string, p: Partial<ProviderInput>) => request<Provider>(`/providers/${name}`, { method: 'PUT', body: JSON.stringify(p) }),
  deleteProvider: (name: string) => request<{ deleted: string; orphaned_models: string[] }>(`/providers/${name}`, { method: 'DELETE' }),
  testProvider: (name: string) => request<HealthStatus>(`/providers/${name}/test`, { method: 'POST' }),
  getStats: () => request<Stats>('/stats'),
  getHealth: () => request<Record<string, HealthStatus>>('/health'),
};
