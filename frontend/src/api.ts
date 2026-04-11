const BASE = '/api';

export interface Provider {
  name: string;
  url: string;
  models: string[];
  weight: number;
  enabled: boolean;
  key_env?: string;
  timeout_seconds: number;
  price_per_input_token: number;
  price_per_output_token: number;
  rate_limit_rpm: number;
  priority: number;
  health?: HealthStatus;
}

export interface HealthStatus {
  healthy: boolean;
  latency_ms: number;
  last_check: string;
  error?: string;
}

export interface AgentStatsEntry {
  name: string;
  total_tasks: number;
  completed_tasks: number;
  failed_tasks: number;
  avg_latency_ms: number;
  latency_p95_ms: number;
  avg_score: number;
  status: string;
}

export interface Stats {
  total_requests: number;
  active_providers: number;
  avg_latency_ms: number;
  error_rate: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cost: number;
  by_provider: ProviderStats[];
  by_model: ModelStats[];
  recent_errors: ErrorEntry[];
  time_series: TimeSeriesPoint[];
  a2a_total_tasks: number;
  a2a_error_rate: number;
  a2a_avg_latency_ms: number;
  by_agent: AgentStatsEntry[];
}

export interface ProviderStats {
  name: string;
  total_requests: number;
  error_count: number;
  error_rate: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  rpm: number;
  input_tokens: number;
  output_tokens: number;
  total_cost: number;
  avg_ttft_ms: number;
}

export interface ModelProviderStats {
  name: string;
  total_requests: number;
  error_rate: number;
  avg_latency_ms: number;
  traffic_share: number;
}

export interface ModelStats {
  name: string;
  total_requests: number;
  rpm: number;
  error_count: number;
  error_rate: number;
  avg_latency_ms: number;
  latency_p95_ms: number;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cost: number;
  avg_ttft_ms: number;
  providers: ModelProviderStats[];
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
  price_per_input_token?: number;
  price_per_output_token?: number;
  rate_limit_rpm?: number;
  priority?: number;
}

export interface AgentCard {
  id: string;
  name: string;
  description: string;
  url: string;
  version: string;
  capabilities: {
    streaming: boolean;
    pushNotifications: boolean;
    stateTransitionHistory: boolean;
  };
  skills: { id: string; name: string; description: string; tags?: string[] }[];
  provider_name?: string;
  status: string;
}

export interface DiscoverResult {
  agent: AgentCard;
  score: number;
  proxy_url?: string;
}

export interface AgentInput {
  id: string;
  name: string;
  description: string;
  url: string;
  version?: string;
  capabilities?: AgentCard['capabilities'];
  skills?: AgentCard['skills'];
  provider_name?: string;
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
  // Providers
  getProviders: () => request<Provider[]>('/providers'),
  getProvider: (name: string) => request<Provider>(`/providers/${name}`),
  addProvider: (p: ProviderInput) => request<Provider>('/providers', { method: 'POST', body: JSON.stringify(p) }),
  updateProvider: (name: string, p: Partial<ProviderInput>) => request<Provider>(`/providers/${name}`, { method: 'PUT', body: JSON.stringify(p) }),
  deleteProvider: (name: string) => request<{ deleted: string; orphaned_models: string[] }>(`/providers/${name}`, { method: 'DELETE' }),
  testProvider: (name: string) => request<HealthStatus>(`/providers/${name}/test`, { method: 'POST' }),
  getStats: () => request<Stats>('/stats'),
  getHealth: () => request<Record<string, HealthStatus>>('/health'),

  // Agents
  getAgents: () => request<AgentCard[]>('/agents'),
  getAgent: (id: string) => request<AgentCard>(`/agents/${id}`),
  addAgent: (a: AgentInput) => request<AgentCard>('/agents', { method: 'POST', body: JSON.stringify(a) }),
  updateAgent: (id: string, a: Partial<AgentInput>) => request<AgentCard>(`/agents/${id}`, { method: 'PUT', body: JSON.stringify(a) }),
  deleteAgent: (id: string) => request<{ deleted: string }>(`/agents/${id}`, { method: 'DELETE' }),
  discoverAgents: (query: string, topN = 5, minScore = 0.1, includeUnhealthy = false) =>
    request<DiscoverResult[]>('/agents/discover', {
      method: 'POST',
      body: JSON.stringify({ query, top_n: topN, min_score: minScore, include_unhealthy: includeUnhealthy }),
    }),
  getAgentHealth: (id: string) => request<{ id: string; status: string }>(`/agents/${id}/health`),

  // A2A protocol calls
  sendA2A: async (agentId: string | null, method: string, params: Record<string, unknown>) => {
    const url = agentId ? `/a2a/${agentId}` : '/a2a';
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', method, id: Date.now(), params }),
    });
    return res.json();
  },

  fetchA2AStream: async (
    agentId: string | null,
    params: Record<string, unknown>,
    onEvent: (event: { type: string; data: unknown }) => void,
  ) => {
    const url = agentId ? `/a2a/${agentId}` : '/a2a';
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', method: 'message/stream', id: Date.now(), params }),
    });
    const reader = res.body?.getReader();
    if (!reader) return;
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() || '';
      let currentEvent = '';
      for (const line of lines) {
        if (line.startsWith('event: ')) {
          currentEvent = line.slice(7);
        } else if (line.startsWith('data: ')) {
          try {
            const data = JSON.parse(line.slice(6));
            onEvent({ type: currentEvent || 'message', data });
          } catch { /* skip malformed */ }
        }
      }
    }
  },

  getTasksList: (state?: string) =>
    api.sendA2A(null, 'tasks/list', state ? { state } : {}),
};
