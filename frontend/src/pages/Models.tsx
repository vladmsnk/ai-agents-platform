import { useState, useEffect, useCallback } from 'react'
import { api } from '../api'
import type { Stats } from '../api'

const COLORS = [
  { dot: 'bg-blue-500', text: 'text-blue-600', bar: 'bg-blue-500' },
  { dot: 'bg-emerald-500', text: 'text-emerald-600', bar: 'bg-emerald-500' },
  { dot: 'bg-amber-500', text: 'text-amber-600', bar: 'bg-amber-500' },
  { dot: 'bg-purple-500', text: 'text-purple-600', bar: 'bg-purple-500' },
  { dot: 'bg-rose-500', text: 'text-rose-600', bar: 'bg-rose-500' },
]

interface ModelInfo {
  name: string
  providers: { name: string; weight: number; enabled: boolean }[]
}

export default function Models() {
  const [models, setModels] = useState<ModelInfo[]>([])
  const [weights, setWeights] = useState<Record<string, Record<string, number>>>({})
  const [initialWeights, setInitialWeights] = useState<Record<string, Record<string, number>>>({})
  const [modelRPM, setModelRPM] = useState<Record<string, number>>({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState<string | null>(null)

  const fetchData = useCallback(async () => {
    try {
      const [providers, stats] = await Promise.all([
        api.getProviders(),
        api.getStats().catch((): Stats => ({
          total_requests: 0, active_providers: 0, avg_latency_ms: 0, error_rate: 0,
          total_input_tokens: 0, total_output_tokens: 0, total_cost: 0,
          by_provider: [], by_model: [], recent_errors: [], time_series: [],
        })),
      ])

      const modelMap: Record<string, ModelInfo['providers']> = {}
      for (const p of providers || []) {
        for (const m of p.models) {
          if (!modelMap[m]) modelMap[m] = []
          modelMap[m].push({ name: p.name, weight: p.weight, enabled: p.enabled })
        }
      }
      const modelList = Object.entries(modelMap)
        .map(([name, providers]) => ({ name, providers }))
        .sort((a, b) => a.name.localeCompare(b.name))
      setModels(modelList)

      const w: Record<string, Record<string, number>> = {}
      for (const m of modelList) {
        w[m.name] = {}
        for (const p of m.providers) {
          w[m.name][p.name] = p.weight
        }
      }
      setWeights(w)
      setInitialWeights(JSON.parse(JSON.stringify(w)))

      const rpm: Record<string, number> = {}
      for (const ms of stats.by_model || []) {
        rpm[ms.name] = ms.rpm
      }
      setModelRPM(rpm)
    } catch (e) {
      console.error('Failed to fetch:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchData() }, [fetchData])

  const totalWeight = (modelName: string) =>
    Object.values(weights[modelName] || {}).reduce((s, v) => s + v, 0)

  const hasChanged = (modelName: string) => {
    const curr = weights[modelName] || {}
    const init = initialWeights[modelName] || {}
    return Object.keys(curr).some(k => curr[k] !== init[k])
  }

  const applyWeights = async (modelName: string) => {
    setSaving(modelName)
    try {
      const modelWeights = weights[modelName] || {}
      await Promise.all(
        Object.entries(modelWeights).map(([provName, w]) =>
          api.updateProvider(provName, { weight: w })
        )
      )
      fetchData()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(null)
    }
  }

  const setWeight = (model: string, provider: string, value: number) => {
    setWeights(prev => ({
      ...prev,
      [model]: { ...prev[model], [provider]: value },
    }))
  }

  if (loading) {
    return <div className="p-8 text-gray-400">Loading...</div>
  }

  if (models.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center">
        <div className="text-6xl mb-4 text-gray-300">◈</div>
        <h2 className="text-xl font-semibold text-gray-700 mb-2">No models available</h2>
        <p className="text-gray-400">Add providers with models to see them here</p>
      </div>
    )
  }

  return (
    <div className="p-8 max-w-2xl">
      <h1 className="text-2xl font-semibold text-gray-900 mb-6">Models</h1>
      <div className="space-y-4">
        {models.map(model => {
          const tw = totalWeight(model.name)
          const isSingle = model.providers.length === 1
          const changed = hasChanged(model.name)
          const rpm = modelRPM[model.name]

          return (
            <div key={model.name} className="bg-white rounded-xl border border-gray-200 p-5">
              {/* Header */}
              <div className="flex items-center justify-between mb-3">
                <div className="flex items-center gap-3">
                  <h3 className="text-lg font-semibold text-gray-900 font-mono">{model.name}</h3>
                  <span className="px-2 py-0.5 bg-gray-100 text-gray-500 rounded-full text-xs font-medium">
                    {model.providers.length} provider{model.providers.length > 1 ? 's' : ''}
                  </span>
                </div>
                {rpm !== undefined && (
                  <span className="text-sm text-gray-400">{Math.round(rpm)} req/min</span>
                )}
              </div>

              {/* Distribution bar */}
              <div className="h-2.5 rounded-full overflow-hidden flex mb-4 bg-gray-100">
                {model.providers.map((p, i) => {
                  const w = weights[model.name]?.[p.name] || p.weight
                  const pct = tw > 0 ? (w / tw) * 100 : 0
                  const c = COLORS[i % COLORS.length]
                  return (
                    <div
                      key={p.name}
                      className={`${c.bar} transition-all duration-300`}
                      style={{ width: `${pct}%` }}
                    />
                  )
                })}
              </div>

              {isSingle ? (
                /* Single provider — no balancing */
                <div className="flex items-center gap-2.5 py-1">
                  <span className={`w-2.5 h-2.5 rounded-full ${COLORS[0].dot}`} />
                  <span className="text-sm font-medium text-gray-700">{model.providers[0].name}</span>
                  <span className="text-sm text-gray-400 ml-auto">Single provider — no balancing</span>
                  <span className={`text-sm font-semibold ${COLORS[0].text}`}>100%</span>
                </div>
              ) : (
                <>
                  {/* Provider rows */}
                  <div className="space-y-2.5">
                    {model.providers.map((p, i) => {
                      const w = weights[model.name]?.[p.name] || p.weight
                      const pct = tw > 0 ? Math.round((w / tw) * 100) : 0
                      const c = COLORS[i % COLORS.length]
                      return (
                        <div key={p.name} className="flex items-center gap-3">
                          <span className={`w-2.5 h-2.5 rounded-full shrink-0 ${c.dot}`} />
                          <span className="text-sm font-medium text-gray-700 w-40 shrink-0">{p.name}</span>
                          <input
                            type="range"
                            min={1}
                            max={10}
                            value={w}
                            onChange={e => setWeight(model.name, p.name, parseInt(e.target.value))}
                            className="flex-1 accent-gray-400 h-1.5"
                          />
                          <span className="text-sm text-gray-500 w-5 text-right tabular-nums">{w}</span>
                          <span className={`text-sm font-semibold w-10 text-right tabular-nums ${c.text}`}>{pct}%</span>
                        </div>
                      )
                    })}
                  </div>

                  {/* Footer: distribution preview + apply */}
                  <div className="flex items-center justify-between mt-4 pt-3 border-t border-gray-100">
                    <p className="text-xs text-gray-400">
                      ~{model.providers.map((p, i) => {
                        const w = weights[model.name]?.[p.name] || p.weight
                        const pct = tw > 0 ? Math.round((w / tw) * 100) : 0
                        return <span key={p.name}>{i > 0 ? ', ~' : ''}{pct} of 100 requests → {p.name}</span>
                      })}
                    </p>
                    <button
                      onClick={() => applyWeights(model.name)}
                      disabled={saving === model.name || !changed}
                      className={`px-4 py-1.5 rounded-lg text-sm font-medium transition-colors shrink-0 ml-4 ${
                        changed
                          ? 'bg-primary-600 text-white hover:bg-primary-700'
                          : 'border border-gray-200 text-gray-400 cursor-default'
                      } disabled:opacity-50`}
                    >
                      {saving === model.name ? 'Applying...' : 'Apply'}
                    </button>
                  </div>
                </>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
