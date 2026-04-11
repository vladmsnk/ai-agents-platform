import { useState, useEffect, useCallback } from 'react'
import { api } from '../api'
import type { Stats, Provider } from '../api'
import MetricCard from '../components/MetricCard'

const COLORS = [
  { dot: 'bg-blue-500', text: 'text-blue-600', bar: 'bg-blue-500' },
  { dot: 'bg-emerald-500', text: 'text-emerald-600', bar: 'bg-emerald-500' },
  { dot: 'bg-amber-500', text: 'text-amber-600', bar: 'bg-amber-500' },
  { dot: 'bg-purple-500', text: 'text-purple-600', bar: 'bg-purple-500' },
  { dot: 'bg-rose-500', text: 'text-rose-600', bar: 'bg-rose-500' },
]

export default function Models() {
  const [stats, setStats] = useState<Stats | null>(null)
  const [providers, setProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)

  const fetchData = useCallback(async () => {
    try {
      const [statsData, provData] = await Promise.all([
        api.getStats().catch((): Stats => ({
          total_requests: 0, active_providers: 0, avg_latency_ms: 0, error_rate: 0,
          total_input_tokens: 0, total_output_tokens: 0, total_cost: 0,
          by_provider: [], by_model: [], recent_errors: [], time_series: [],
          a2a_total_tasks: 0, a2a_error_rate: 0, a2a_avg_latency_ms: 0, by_agent: [],
        })),
        api.getProviders().catch(() => [] as Provider[]),
      ])
      setStats(statsData)
      setProviders(provData)
    } catch (e) {
      console.error('Failed to fetch:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  const providerHealth = useCallback((provName: string) => {
    const p = providers.find(pr => pr.name === provName)
    if (!p || !p.health) return 'bg-gray-300'
    return p.health.healthy ? 'bg-emerald-400' : 'bg-red-400'
  }, [providers])

  if (loading) {
    return <div className="p-8 text-gray-400">Loading...</div>
  }

  const models = stats?.by_model || []

  if (models.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center">
        <div className="text-6xl mb-4 text-gray-300">◈</div>
        <h2 className="text-xl font-semibold text-gray-700 mb-2">No models available</h2>
        <p className="text-gray-400">Add providers with models to see them here</p>
      </div>
    )
  }

  const totalRequests = models.reduce((s, m) => s + m.total_requests, 0)
  const totalCost = models.reduce((s, m) => s + m.total_cost, 0)
  const weightedErrorRate = totalRequests > 0
    ? models.reduce((s, m) => s + m.error_rate * m.total_requests, 0) / totalRequests
    : 0

  return (
    <div className="p-8 max-w-4xl">
      <h1 className="text-2xl font-semibold text-gray-900 mb-6">Models</h1>


      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <MetricCard label="Total Models" value={models.length} />
        <MetricCard label="Total Requests" value={totalRequests.toLocaleString()} />
        <MetricCard label="Avg Error Rate" value={`${weightedErrorRate.toFixed(1)}%`} accent={weightedErrorRate > 5} />
        <MetricCard label="Total Cost" value={`$${totalCost.toFixed(4)}`} />
      </div>


      <div className="space-y-4">
        {models.map(model => {
          const modelProviders = model.providers || []
          const isSingle = modelProviders.length <= 1

          return (
            <div key={model.name} className="bg-white rounded-xl border border-gray-200 p-5">

              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-3">
                  <h3 className="text-lg font-semibold text-gray-900 font-mono">{model.name}</h3>
                  <span className="px-2 py-0.5 bg-gray-100 text-gray-500 rounded-full text-xs font-medium">
                    {modelProviders.length} provider{modelProviders.length !== 1 ? 's' : ''}
                  </span>
                </div>
                {model.rpm > 0 && (
                  <span className="text-sm text-gray-400">{Math.round(model.rpm)} req/min</span>
                )}
              </div>


              <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
                <StatChip label="Requests" value={model.total_requests.toLocaleString()} />
                <StatChip
                  label="Avg Latency"
                  value={`${Math.round(model.avg_latency_ms)}ms`}
                  sub={`p95: ${Math.round(model.latency_p95_ms)}ms`}
                />
                <StatChip
                  label="Error Rate"
                  value={`${model.error_rate.toFixed(1)}%`}
                  accent={model.error_rate > 5}
                />
                <StatChip label="Cost" value={`$${model.total_cost.toFixed(4)}`} />
              </div>


              <div className="h-2.5 rounded-full overflow-hidden flex mb-4 bg-gray-100">
                {modelProviders.map((p, i) => {
                  const c = COLORS[i % COLORS.length]
                  return (
                    <div
                      key={p.name}
                      className={`${c.bar} transition-all duration-300`}
                      style={{ width: `${p.traffic_share}%` }}
                    />
                  )
                })}
              </div>


              {isSingle ? (
                <div className="flex items-center gap-2.5 py-1">
                  <span className={`w-2.5 h-2.5 rounded-full ${providerHealth(modelProviders[0]?.name || '')}`} />
                  <span className="text-sm font-medium text-gray-700">{modelProviders[0]?.name}</span>
                  <span className="text-sm text-gray-400 ml-auto">Single provider</span>
                  <span className="text-xs text-gray-400">
                    {Math.round(modelProviders[0]?.avg_latency_ms || 0)}ms
                  </span>
                  <span className={`text-sm font-semibold ${COLORS[0].text}`}>100%</span>
                </div>
              ) : (
                <div className="space-y-2">
                  {modelProviders.map((p, i) => {
                    const c = COLORS[i % COLORS.length]
                    return (
                      <div key={p.name} className="flex items-center gap-3">
                        <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${providerHealth(p.name)}`} />
                        <span className="text-sm font-medium text-gray-700 w-40 shrink-0">{p.name}</span>
                        <span className="text-xs text-gray-400">{p.total_requests} req</span>
                        <span className="text-xs text-gray-400">{Math.round(p.avg_latency_ms)}ms</span>
                        {p.error_rate > 0 && (
                          <span className={`text-xs ${p.error_rate > 5 ? 'text-red-500' : 'text-gray-400'}`}>
                            {p.error_rate.toFixed(1)}% err
                          </span>
                        )}
                        <span className={`text-sm font-semibold ml-auto tabular-nums ${c.text}`}>
                          {Math.round(p.traffic_share)}%
                        </span>
                      </div>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

function StatChip({ label, value, sub, accent }: { label: string; value: string; sub?: string; accent?: boolean }) {
  return (
    <div className="bg-gray-50 rounded-lg px-3 py-2">
      <p className="text-xs text-gray-400 mb-0.5">{label}</p>
      <p className={`text-sm font-semibold ${accent ? 'text-red-600' : 'text-gray-900'}`}>{value}</p>
      {sub && <p className="text-xs text-gray-400">{sub}</p>}
    </div>
  )
}
