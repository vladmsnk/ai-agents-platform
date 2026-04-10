import { useState, useEffect, useCallback } from 'react'
import { api } from '../api'
import type { Stats } from '../api'

const GRAFANA_URL = 'http://localhost:3000'
const DASHBOARD_UID = 'llm-gateway'

const panels = [
  { id: 1, title: 'Request Rate by Provider' },
  { id: 2, title: 'Request Rate by Model' },
  { id: 3, title: 'Latency P50 by Provider' },
  { id: 4, title: 'Latency P95 by Provider' },
  { id: 5, title: 'Error Rate by Provider' },
  { id: 6, title: 'CPU Usage' },
  { id: 7, title: 'Active Requests & Goroutines' },
  { id: 8, title: 'Traffic Distribution' },
  { id: 9, title: 'Response Codes' },
]

function grafanaPanelURL(panelId: number) {
  return `${GRAFANA_URL}/d-solo/${DASHBOARD_UID}/llm-gateway?orgId=1&panelId=${panelId}&theme=light&from=now-1h&to=now&refresh=10s`
}

export default function Monitoring() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [enabledProviders, setEnabledProviders] = useState(0)
  const fetchData = useCallback(async () => {
    try {
      const [statsData, providers] = await Promise.all([
        api.getStats(),
        api.getProviders().catch(() => []),
      ])
      setStats(statsData)
      setEnabledProviders((providers || []).filter(p => p.enabled).length)
    } catch (e) {
      console.error('Failed to fetch stats:', e)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const id = window.setInterval(fetchData, 5000)
    return () => clearInterval(id)
  }, [fetchData])

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-gray-900">Monitoring</h1>
        <a
          href={`${GRAFANA_URL}/d/${DASHBOARD_UID}/llm-gateway`}
          target="_blank"
          rel="noopener noreferrer"
          className="px-4 py-2 text-sm text-gray-600 border border-gray-200 rounded-lg hover:bg-gray-50 transition-colors"
        >
          Open in Grafana
        </a>
      </div>

      {/* Metric cards from /api/stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-4">
        <MetricCard label="Total Requests" value={stats?.total_requests ?? 0} />
        <MetricCard label="Active Providers" value={enabledProviders} />
        <MetricCard label="Avg Latency" value={`${Math.round(stats?.avg_latency_ms ?? 0)}ms`} />
        <MetricCard label="Error Rate" value={`${(stats?.error_rate ?? 0).toFixed(1)}%`} accent={!!stats?.error_rate && stats.error_rate > 5} />
      </div>
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
        <MetricCard label="Input Tokens" value={(stats?.total_input_tokens ?? 0).toLocaleString()} />
        <MetricCard label="Output Tokens" value={(stats?.total_output_tokens ?? 0).toLocaleString()} />
        <MetricCard label="Total Cost" value={`$${(stats?.total_cost ?? 0).toFixed(4)}`} />
        <MetricCard label="Avg TTFT" value={
          stats?.by_provider?.length
            ? `${Math.round(stats.by_provider.reduce((s, p) => s + p.avg_ttft_ms, 0) / stats.by_provider.length)}ms`
            : '—'
        } />
      </div>

      {/* Grafana panels */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-5 mb-8">
        {panels.map(panel => (
          <div key={panel.id} className="bg-white rounded-xl border border-gray-200 overflow-hidden">
            <iframe
              src={grafanaPanelURL(panel.id)}
              width="100%"
              height="300"
              frameBorder="0"
              title={panel.title}
            />
          </div>
        ))}
      </div>

      {/* Provider stats table — from /api/stats */}
      <div className="bg-white rounded-xl border border-gray-200 p-5 mb-5">
        <h3 className="text-sm font-semibold text-gray-700 mb-4">Provider Stats</h3>
        <div className="overflow-x-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-gray-500 border-b border-gray-100">
                <th className="text-left pb-2">Provider</th>
                <th className="text-right pb-2">Requests</th>
                <th className="text-right pb-2">RPM</th>
                <th className="text-right pb-2">P50</th>
                <th className="text-right pb-2">P95</th>
                <th className="text-right pb-2">TTFT</th>
                <th className="text-right pb-2">Tokens</th>
                <th className="text-right pb-2">Cost</th>
                <th className="text-right pb-2">Errors</th>
              </tr>
            </thead>
            <tbody>
              {(stats?.by_provider || []).map(p => (
                <tr key={p.name} className="border-b border-gray-50">
                  <td className="py-2 font-medium text-gray-700">{p.name}</td>
                  <td className="py-2 text-right text-gray-600">{p.total_requests}</td>
                  <td className="py-2 text-right text-gray-600">{p.rpm.toFixed(1)}</td>
                  <td className="py-2 text-right text-gray-600">{Math.round(p.latency_p50_ms)}ms</td>
                  <td className="py-2 text-right text-gray-600">{Math.round(p.latency_p95_ms)}ms</td>
                  <td className="py-2 text-right text-gray-600">{p.avg_ttft_ms > 0 ? `${Math.round(p.avg_ttft_ms)}ms` : '—'}</td>
                  <td className="py-2 text-right text-gray-600">{(p.input_tokens + p.output_tokens).toLocaleString()}</td>
                  <td className="py-2 text-right text-gray-600">{p.total_cost > 0 ? `$${p.total_cost.toFixed(4)}` : '—'}</td>
                  <td className="py-2 text-right">
                    <span className={p.error_rate > 0 ? 'text-red-600 font-medium' : 'text-gray-600'}>
                      {p.error_rate.toFixed(1)}%
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Error feed — from /api/stats */}
      <div className="bg-white rounded-xl border border-gray-200 p-5">
        <h3 className="text-sm font-semibold text-gray-700 mb-4">Recent Errors</h3>
        {(stats?.recent_errors?.length ?? 0) > 0 ? (
          <div className="space-y-2 max-h-64 overflow-auto">
            {stats!.recent_errors.map((e, i) => (
              <div key={i} className="flex items-start gap-3 p-3 bg-red-50 rounded-lg text-sm">
                <span className="w-2 h-2 rounded-full bg-red-400 mt-1.5 shrink-0" />
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 mb-0.5">
                    <span className="font-medium text-gray-900">{e.provider}</span>
                    <span className="text-gray-400">·</span>
                    <span className="text-gray-500">{e.model}</span>
                    {e.status > 0 && <span className="px-1.5 py-0.5 bg-red-100 text-red-700 rounded text-xs">{e.status}</span>}
                  </div>
                  <p className="text-red-700 text-xs truncate">{e.message}</p>
                </div>
                <span className="text-xs text-gray-400 shrink-0">
                  {new Date(e.timestamp).toLocaleTimeString()}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <div className="text-center py-8 text-gray-400 text-sm">No errors in the last 5 minutes</div>
        )}
      </div>
    </div>
  )
}

function MetricCard({ label, value, accent }: { label: string; value: string | number; accent?: boolean }) {
  return (
    <div className="bg-white rounded-xl border border-gray-200 p-5">
      <p className="text-xs font-medium text-gray-500 mb-1">{label}</p>
      <p className={`text-2xl font-semibold ${accent ? 'text-red-600' : 'text-gray-900'}`}>{value}</p>
    </div>
  )
}
