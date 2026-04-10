import { useState, useEffect, useCallback } from 'react'
import { api } from '../api'
import type { Provider, ProviderInput, HealthStatus } from '../api'

const emptyForm: ProviderInput = {
  name: '', url: '', models: [], weight: 1, enabled: true, api_key: '', key_env: '', timeout_seconds: 60,
  price_per_input_token: 0, price_per_output_token: 0, rate_limit_rpm: 0, priority: 10,
}

export default function Providers() {
  const [providers, setProviders] = useState<Provider[]>([])
  const [loading, setLoading] = useState(true)
  const [slideOpen, setSlideOpen] = useState(false)
  const [editingName, setEditingName] = useState<string | null>(null)
  const [form, setForm] = useState<ProviderInput>(emptyForm)
  const [modelInput, setModelInput] = useState('')
  const [testResult, setTestResult] = useState<HealthStatus | null>(null)
  const [testLoading, setTestLoading] = useState(false)
  const [menuOpen, setMenuOpen] = useState<string | null>(null)
  const [confirmModal, setConfirmModal] = useState<{ type: 'disable' | 'delete'; name: string; orphanedModels?: string[] } | null>(null)
  const [saving, setSaving] = useState(false)

  const fetchProviders = useCallback(async () => {
    try {
      const data = await api.getProviders()
      setProviders(data || [])
    } catch (e) {
      console.error('Failed to fetch providers:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchProviders() }, [fetchProviders])

  const openAdd = () => {
    setForm(emptyForm)
    setEditingName(null)
    setTestResult(null)
    setSlideOpen(true)
  }

  const openEdit = (p: Provider) => {
    setForm({
      name: p.name, url: p.url, models: p.models, weight: p.weight,
      enabled: p.enabled, api_key: '', key_env: p.key_env || '', timeout_seconds: p.timeout_seconds,
      price_per_input_token: p.price_per_input_token, price_per_output_token: p.price_per_output_token,
      rate_limit_rpm: p.rate_limit_rpm, priority: p.priority,
    })
    setEditingName(p.name)
    setTestResult(null)
    setMenuOpen(null)
    setSlideOpen(true)
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      if (editingName) {
        await api.updateProvider(editingName, form)
      } else {
        await api.addProvider(form)
      }
      setSlideOpen(false)
      fetchProviders()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async () => {
    setTestLoading(true)
    setTestResult(null)
    try {
      const name = editingName || form.name || '_test'
      const result = await api.testProvider(name)
      setTestResult(result)
    } catch {
      setTestResult({ healthy: false, latency_ms: 0, last_check: '', error: 'Connection failed' })
    } finally {
      setTestLoading(false)
    }
  }

  const handleToggle = async (p: Provider) => {
    if (p.enabled) {
      setConfirmModal({ type: 'disable', name: p.name })
    } else {
      await api.updateProvider(p.name, { enabled: true })
      fetchProviders()
    }
    setMenuOpen(null)
  }

  const confirmDisable = async () => {
    if (!confirmModal) return
    await api.updateProvider(confirmModal.name, { enabled: false })
    setConfirmModal(null)
    fetchProviders()
  }

  const handleDelete = (name: string) => {
    const provider = providers.find(p => p.name === name)
    if (!provider) return
    // Check for orphaned models
    const orphaned = provider.models.filter(m =>
      !providers.some(p => p.name !== name && p.models.includes(m))
    )
    setConfirmModal({ type: 'delete', name, orphanedModels: orphaned })
    setMenuOpen(null)
  }

  const confirmDelete = async () => {
    if (!confirmModal) return
    await api.deleteProvider(confirmModal.name)
    setConfirmModal(null)
    fetchProviders()
  }

  const addModel = () => {
    const m = modelInput.trim()
    if (m && !form.models.includes(m)) {
      setForm({ ...form, models: [...form.models, m] })
    }
    setModelInput('')
  }

  const removeModel = (m: string) => {
    setForm({ ...form, models: form.models.filter(x => x !== m) })
  }

  const statusDot = (p: Provider) => {
    if (!p.enabled) return 'bg-gray-300'
    if (!p.health) return 'bg-gray-300'
    return p.health.healthy ? 'bg-emerald-400' : 'bg-red-400'
  }

  if (loading) {
    return <div className="p-8 text-gray-400">Loading...</div>
  }

  if (providers.length === 0 && !slideOpen) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center">
        <div className="text-6xl mb-4 text-gray-300">⬡</div>
        <h2 className="text-xl font-semibold text-gray-700 mb-2">No providers configured</h2>
        <p className="text-gray-400 mb-6">Add your first LLM provider to get started</p>
        <button onClick={openAdd} className="px-5 py-2.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors font-medium">
          Add Provider
        </button>
      </div>
    )
  }

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-gray-900">Providers</h1>
        <button onClick={openAdd} className="px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium">
          + Add Provider
        </button>
      </div>

      {/* Table */}
      <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200">
              <th className="text-left px-4 py-3 font-medium text-gray-500">Status</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Name</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">URL</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Models</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Weight</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Latency</th>
              <th className="text-right px-4 py-3 font-medium text-gray-500"></th>
            </tr>
          </thead>
          <tbody>
            {providers.map((p) => (
              <tr key={p.name} className="border-b border-gray-100 hover:bg-gray-50 transition-colors">
                <td className="px-4 py-3">
                  <span className={`inline-block w-2.5 h-2.5 rounded-full ${statusDot(p)}`} />
                </td>
                <td className="px-4 py-3 font-medium text-gray-900">{p.name}</td>
                <td className="px-4 py-3 text-gray-500 font-mono text-xs">{p.url}</td>
                <td className="px-4 py-3">
                  <div className="flex flex-wrap gap-1">
                    {p.models.map((m) => (
                      <span key={m} className="px-2 py-0.5 bg-primary-50 text-primary-700 rounded text-xs font-medium">{m}</span>
                    ))}
                  </div>
                </td>
                <td className="px-4 py-3 text-gray-700">{p.weight}</td>
                <td className="px-4 py-3 text-gray-500 text-xs">
                  {p.health ? `${p.health.latency_ms}ms` : '—'}
                </td>
                <td className="px-4 py-3 text-right relative">
                  <button
                    onClick={() => setMenuOpen(menuOpen === p.name ? null : p.name)}
                    className="p-1.5 rounded-lg hover:bg-gray-100 text-gray-400 hover:text-gray-600"
                  >
                    ⋯
                  </button>
                  {menuOpen === p.name && (
                    <div className="absolute right-4 top-10 bg-white border border-gray-200 rounded-lg shadow-lg py-1 z-10 min-w-[140px]">
                      <button onClick={() => openEdit(p)} className="w-full text-left px-4 py-2 text-sm hover:bg-gray-50 text-gray-700">Edit</button>
                      <button onClick={() => handleToggle(p)} className="w-full text-left px-4 py-2 text-sm hover:bg-gray-50 text-gray-700">
                        {p.enabled ? 'Disable' : 'Enable'}
                      </button>
                      <button onClick={() => handleDelete(p.name)} className="w-full text-left px-4 py-2 text-sm hover:bg-gray-50 text-red-600">Delete</button>
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Slide-over panel */}
      {slideOpen && (
        <div className="fixed inset-0 z-30">
          <div className="absolute inset-0 bg-black/20" onClick={() => setSlideOpen(false)} />
          <div className="absolute right-0 top-0 h-full w-full max-w-[420px] bg-white shadow-2xl flex flex-col">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">
                {editingName ? 'Edit Provider' : 'Add Provider'}
              </h2>
              <button onClick={() => setSlideOpen(false)} className="text-gray-400 hover:text-gray-600 text-xl">×</button>
            </div>
            <div className="flex-1 overflow-auto p-6 space-y-5">
              {/* Name */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
                <input
                  value={form.name}
                  onChange={e => setForm({ ...form, name: e.target.value })}
                  disabled={!!editingName}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none disabled:bg-gray-50 disabled:text-gray-500"
                  placeholder="openai-primary"
                />
              </div>
              {/* URL */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">URL</label>
                <input
                  value={form.url}
                  onChange={e => setForm({ ...form, url: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  placeholder="https://api.openai.com"
                />
              </div>
              {/* API Key */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">API Key</label>
                <input
                  type="password"
                  value={form.api_key || ''}
                  onChange={e => setForm({ ...form, api_key: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  placeholder={editingName ? '••••••••' : 'sk-...'}
                />
              </div>
              {/* Models */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Models</label>
                <div className="flex flex-wrap gap-1.5 mb-2">
                  {form.models.map(m => (
                    <span key={m} className="inline-flex items-center gap-1 px-2.5 py-1 bg-primary-50 text-primary-700 rounded-md text-xs font-medium">
                      {m}
                      <button onClick={() => removeModel(m)} className="text-primary-400 hover:text-primary-600">×</button>
                    </span>
                  ))}
                </div>
                <div className="flex gap-2">
                  <input
                    value={modelInput}
                    onChange={e => setModelInput(e.target.value)}
                    onKeyDown={e => e.key === 'Enter' && (e.preventDefault(), addModel())}
                    className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                    placeholder="gpt-4o"
                  />
                  <button onClick={addModel} className="px-3 py-2 bg-gray-100 text-gray-600 rounded-lg text-sm hover:bg-gray-200">Add</button>
                </div>
              </div>
              {/* Weight slider */}
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Weight: {form.weight}</label>
                <input
                  type="range"
                  min={1}
                  max={10}
                  value={form.weight}
                  onChange={e => setForm({ ...form, weight: parseInt(e.target.value) })}
                  className="w-full accent-primary-600"
                />
                <div className="flex justify-between text-xs text-gray-400 mt-0.5">
                  <span>1</span><span>10</span>
                </div>
              </div>
              {/* Pricing */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">Price/Input Token ($)</label>
                  <input type="number" step="0.000001" min={0}
                    value={form.price_per_input_token || 0}
                    onChange={e => setForm({ ...form, price_per_input_token: parseFloat(e.target.value) || 0 })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">Price/Output Token ($)</label>
                  <input type="number" step="0.000001" min={0}
                    value={form.price_per_output_token || 0}
                    onChange={e => setForm({ ...form, price_per_output_token: parseFloat(e.target.value) || 0 })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  />
                </div>
              </div>
              {/* Rate Limit & Priority */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">Rate Limit (RPM)</label>
                  <input type="number" min={0}
                    value={form.rate_limit_rpm || 0}
                    onChange={e => setForm({ ...form, rate_limit_rpm: parseInt(e.target.value) || 0 })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                    placeholder="0 = unlimited"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 mb-1">Priority</label>
                  <input type="number" min={1}
                    value={form.priority || 10}
                    onChange={e => setForm({ ...form, priority: parseInt(e.target.value) || 10 })}
                    className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                    placeholder="Lower = higher priority"
                  />
                </div>
              </div>
              {/* Enabled toggle */}
              <div className="flex items-center justify-between">
                <label className="text-sm font-medium text-gray-700">Enabled</label>
                <button
                  onClick={() => setForm({ ...form, enabled: !form.enabled })}
                  className={`relative w-11 h-6 rounded-full transition-colors ${form.enabled ? 'bg-primary-600' : 'bg-gray-300'}`}
                >
                  <span className={`absolute top-0.5 left-0.5 w-5 h-5 bg-white rounded-full shadow transition-transform ${form.enabled ? 'translate-x-5' : ''}`} />
                </button>
              </div>
              {/* Test Connection */}
              <div>
                <button
                  onClick={handleTest}
                  disabled={testLoading || !form.url}
                  className="w-full px-4 py-2 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50 transition-colors"
                >
                  {testLoading ? 'Testing...' : 'Test Connection'}
                </button>
                {testResult && (
                  <div className={`mt-2 p-3 rounded-lg text-sm ${testResult.healthy ? 'bg-emerald-50 text-emerald-700' : 'bg-red-50 text-red-700'}`}>
                    {testResult.healthy
                      ? `Connected (${testResult.latency_ms}ms)`
                      : `Failed: ${testResult.error}`}
                  </div>
                )}
              </div>
            </div>
            <div className="px-6 py-4 border-t border-gray-200 flex gap-3">
              <button onClick={() => setSlideOpen(false)} className="flex-1 px-4 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50">
                Cancel
              </button>
              <button onClick={handleSave} disabled={saving} className="flex-1 px-4 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50">
                {saving ? 'Saving...' : editingName ? 'Update' : 'Add Provider'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Confirm modal */}
      {confirmModal && (
        <div className="fixed inset-0 z-40 flex items-center justify-center">
          <div className="absolute inset-0 bg-black/30" onClick={() => setConfirmModal(null)} />
          <div className="relative bg-white rounded-xl shadow-2xl p-6 max-w-md w-full mx-4">
            <h3 className="text-lg font-semibold text-gray-900 mb-2">
              {confirmModal.type === 'disable' ? 'Disable Provider' : 'Delete Provider'}
            </h3>
            {confirmModal.type === 'disable' ? (
              <p className="text-sm text-gray-600 mb-4">
                Are you sure you want to disable <strong>{confirmModal.name}</strong>? Active streams will complete normally.
              </p>
            ) : (
              <div className="mb-4">
                <p className="text-sm text-gray-600 mb-2">
                  Are you sure you want to delete <strong>{confirmModal.name}</strong>? This action cannot be undone.
                </p>
                {confirmModal.orphanedModels && confirmModal.orphanedModels.length > 0 && (
                  <div className="p-3 bg-amber-50 border border-amber-200 rounded-lg text-sm text-amber-700">
                    <strong>Warning:</strong> These models will become unavailable:
                    <div className="flex flex-wrap gap-1 mt-1">
                      {confirmModal.orphanedModels.map(m => (
                        <span key={m} className="px-2 py-0.5 bg-amber-100 rounded text-xs font-medium">{m}</span>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )}
            <div className="flex gap-3">
              <button onClick={() => setConfirmModal(null)} className="flex-1 px-4 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50">
                Cancel
              </button>
              <button
                onClick={confirmModal.type === 'disable' ? confirmDisable : confirmDelete}
                className={`flex-1 px-4 py-2.5 rounded-lg text-sm font-medium text-white ${
                  confirmModal.type === 'delete' ? 'bg-red-600 hover:bg-red-700' : 'bg-amber-500 hover:bg-amber-600'
                }`}
              >
                {confirmModal.type === 'disable' ? 'Disable' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
