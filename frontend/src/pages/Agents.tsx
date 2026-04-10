import { useState, useEffect, useCallback } from 'react'
import { api } from '../api'
import type { AgentCard, AgentInput } from '../api'

const emptyForm: AgentInput = {
  id: '', name: '', description: '', url: '', version: '1.0.0',
  capabilities: { streaming: false, pushNotifications: false, stateTransitionHistory: false },
  skills: [],
}

export default function Agents() {
  const [agents, setAgents] = useState<AgentCard[]>([])
  const [loading, setLoading] = useState(true)
  const [slideOpen, setSlideOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<AgentInput>(emptyForm)
  const [saving, setSaving] = useState(false)

  const fetchAgents = useCallback(async () => {
    try {
      const data = await api.getAgents()
      setAgents(data || [])
    } catch (e) {
      console.error('Failed to fetch agents:', e)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { fetchAgents() }, [fetchAgents])

  const openAdd = () => {
    setForm(emptyForm)
    setEditingId(null)
    setSlideOpen(true)
  }

  const openEdit = (a: AgentCard) => {
    setForm({
      id: a.id, name: a.name, description: a.description, url: a.url,
      version: a.version, capabilities: a.capabilities, skills: a.skills,
      provider_name: a.provider_name,
    })
    setEditingId(a.id)
    setSlideOpen(true)
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      if (editingId) {
        await api.updateAgent(editingId, form)
      } else {
        await api.addAgent(form)
      }
      setSlideOpen(false)
      fetchAgents()
    } catch (e: any) {
      alert(e.message)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (id: string) => {
    if (!confirm(`Delete agent "${id}"?`)) return
    await api.deleteAgent(id)
    fetchAgents()
  }

  if (loading) {
    return <div className="p-8 text-gray-400">Loading...</div>
  }

  if (agents.length === 0 && !slideOpen) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-center">
        <div className="text-6xl mb-4 text-gray-300">&#x25C7;</div>
        <h2 className="text-xl font-semibold text-gray-700 mb-2">No agents registered</h2>
        <p className="text-gray-400 mb-6">Register your first A2A agent to get started</p>
        <button onClick={openAdd} className="px-5 py-2.5 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors font-medium">
          Register Agent
        </button>
      </div>
    )
  }

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-gray-900">A2A Agents</h1>
        <button onClick={openAdd} className="px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium">
          + Register Agent
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        {agents.map(a => (
          <div key={a.id} className="bg-white rounded-xl border border-gray-200 p-5">
            <div className="flex items-start justify-between mb-3">
              <div>
                <h3 className="text-lg font-semibold text-gray-900">{a.name}</h3>
                <p className="text-xs text-gray-400 font-mono">{a.id}</p>
              </div>
              <div className="flex gap-2">
                <button onClick={() => openEdit(a)} className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-gray-50 text-gray-600">Edit</button>
                <button onClick={() => handleDelete(a.id)} className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-red-50 text-red-600">Delete</button>
              </div>
            </div>
            <p className="text-sm text-gray-600 mb-3">{a.description || 'No description'}</p>
            <div className="space-y-1.5 text-xs text-gray-500">
              <div><span className="font-medium text-gray-700">URL:</span> <span className="font-mono">{a.url}</span></div>
              <div><span className="font-medium text-gray-700">Version:</span> {a.version}</div>
              <div className="flex gap-2">
                {a.capabilities?.streaming && <span className="px-2 py-0.5 bg-blue-50 text-blue-700 rounded">Streaming</span>}
                {a.capabilities?.pushNotifications && <span className="px-2 py-0.5 bg-purple-50 text-purple-700 rounded">Push</span>}
              </div>
              {a.skills && a.skills.length > 0 && (
                <div className="flex flex-wrap gap-1 mt-2">
                  {a.skills.map(s => (
                    <span key={s.id} className="px-2 py-0.5 bg-primary-50 text-primary-700 rounded text-xs font-medium">{s.name}</span>
                  ))}
                </div>
              )}
            </div>
          </div>
        ))}
      </div>

      {slideOpen && (
        <div className="fixed inset-0 z-30">
          <div className="absolute inset-0 bg-black/20" onClick={() => setSlideOpen(false)} />
          <div className="absolute right-0 top-0 h-full w-full max-w-[420px] bg-white shadow-2xl flex flex-col">
            <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
              <h2 className="text-lg font-semibold text-gray-900">
                {editingId ? 'Edit Agent' : 'Register Agent'}
              </h2>
              <button onClick={() => setSlideOpen(false)} className="text-gray-400 hover:text-gray-600 text-xl">&times;</button>
            </div>
            <div className="flex-1 overflow-auto p-6 space-y-5">
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">ID</label>
                <input value={form.id} onChange={e => setForm({ ...form, id: e.target.value })} disabled={!!editingId}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none disabled:bg-gray-50"
                  placeholder="my-agent" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
                <input value={form.name} onChange={e => setForm({ ...form, name: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  placeholder="My Agent" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Description</label>
                <textarea value={form.description} onChange={e => setForm({ ...form, description: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  rows={2} placeholder="What does this agent do?" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">URL</label>
                <input value={form.url} onChange={e => setForm({ ...form, url: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  placeholder="http://my-agent:8000" />
              </div>
              <div>
                <label className="block text-sm font-medium text-gray-700 mb-1">Version</label>
                <input value={form.version || ''} onChange={e => setForm({ ...form, version: e.target.value })}
                  className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
                  placeholder="1.0.0" />
              </div>
              <div className="space-y-2">
                <label className="block text-sm font-medium text-gray-700">Capabilities</label>
                {(['streaming', 'pushNotifications', 'stateTransitionHistory'] as const).map(cap => (
                  <label key={cap} className="flex items-center gap-2 text-sm text-gray-600">
                    <input type="checkbox" checked={form.capabilities?.[cap] || false}
                      onChange={e => setForm({ ...form, capabilities: { ...form.capabilities!, [cap]: e.target.checked } })}
                      className="accent-primary-600" />
                    {cap}
                  </label>
                ))}
              </div>
            </div>
            <div className="px-6 py-4 border-t border-gray-200 flex gap-3">
              <button onClick={() => setSlideOpen(false)} className="flex-1 px-4 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50">Cancel</button>
              <button onClick={handleSave} disabled={saving} className="flex-1 px-4 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50">
                {saving ? 'Saving...' : editingId ? 'Update' : 'Register'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
