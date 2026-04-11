import { useState, useEffect, useCallback, useRef } from 'react'
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer, Cell } from 'recharts'
import { api } from '../api'
import type { AgentCard, AgentInput, DiscoverResult } from '../api'
import MetricCard from '../components/MetricCard'

// ── Shared helpers ──────────────────────────────────────────────────

const STATUS_COLORS: Record<string, string> = {
  active: 'bg-emerald-400',
  unhealthy: 'bg-red-400',
  inactive: 'bg-gray-300',
}

const STATUS_BORDER: Record<string, string> = {
  active: 'border-l-emerald-400',
  unhealthy: 'border-l-red-400',
  inactive: 'border-l-gray-300',
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    active: 'bg-green-50 text-green-700 border-green-200',
    unhealthy: 'bg-red-50 text-red-700 border-red-200',
    inactive: 'bg-gray-50 text-gray-500 border-gray-200',
  }
  return (
    <span className={`px-2 py-0.5 text-xs font-medium rounded-full border ${colors[status] || colors.inactive}`}>
      {status || 'unknown'}
    </span>
  )
}

function StatusDot({ status }: { status: string }) {
  return <span className={`inline-block w-2 h-2 rounded-full ${STATUS_COLORS[status] || STATUS_COLORS.inactive}`} />
}

function SkillTag({ name }: { name: string }) {
  return <span className="px-2 py-0.5 bg-primary-50 text-primary-700 rounded text-xs font-medium">{name}</span>
}

function CopyButton({ text, label = 'Copy' }: { text: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      onClick={() => { navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 1500) }}
      className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-gray-50 text-gray-600"
    >
      {copied ? 'Copied!' : label}
    </button>
  )
}

const emptyForm: AgentInput = {
  id: '', name: '', description: '', url: '', version: '1.0.0',
  capabilities: { streaming: false, pushNotifications: false, stateTransitionHistory: false },
  skills: [],
}

const TABS = ['Overview', 'Agents', 'Discover', 'Playground'] as const
type Tab = (typeof TABS)[number]

// ── Main component ──────────────────────────────────────────────────

export default function Agents() {
  const [agents, setAgents] = useState<AgentCard[]>([])
  const [loading, setLoading] = useState(true)
  const [tab, setTab] = useState<Tab>('Overview')
  const [taskCount, setTaskCount] = useState(0)

  // Edit/Add state
  const [slideOpen, setSlideOpen] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<AgentInput>(emptyForm)
  const [saving, setSaving] = useState(false)

  // Detail slide
  const [detailAgent, setDetailAgent] = useState<AgentCard | null>(null)

  // Playground pre-selection
  const [playgroundAgent, setPlaygroundAgent] = useState<string | null>(null)

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

  const fetchTaskCount = useCallback(async () => {
    try {
      const resp = await api.getTasksList()
      setTaskCount(resp?.result?.length || 0)
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    fetchAgents()
    fetchTaskCount()
    const id = window.setInterval(() => { fetchAgents(); fetchTaskCount() }, 5000)
    return () => clearInterval(id)
  }, [fetchAgents, fetchTaskCount])

  // ── CRUD handlers ──────────────────────────────────────────────────

  const openAdd = () => { setForm(emptyForm); setEditingId(null); setSlideOpen(true) }

  const openEdit = (a: AgentCard) => {
    setForm({
      id: a.id, name: a.name, description: a.description, url: a.url,
      version: a.version, capabilities: a.capabilities, skills: a.skills,
      provider_name: a.provider_name,
    })
    setEditingId(a.id)
    setDetailAgent(null)
    setSlideOpen(true)
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      if (editingId) await api.updateAgent(editingId, form)
      else await api.addAgent(form)
      setSlideOpen(false)
      fetchAgents()
    } catch (e: unknown) {
      alert(e instanceof Error ? e.message : 'Save failed')
    } finally { setSaving(false) }
  }

  const handleDelete = async (id: string) => {
    if (!confirm(`Delete agent "${id}"?`)) return
    await api.deleteAgent(id)
    setDetailAgent(null)
    fetchAgents()
  }

  const openPlayground = (agentId: string) => {
    setPlaygroundAgent(agentId)
    setTab('Playground')
  }

  // ── Loading / empty states ─────────────────────────────────────────

  if (loading) return <div className="p-8 text-gray-400">Loading...</div>

  const active = agents.filter(a => a.status === 'active').length
  const unhealthy = agents.filter(a => a.status === 'unhealthy').length

  return (
    <div className="p-8">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-semibold text-gray-900">A2A Agents</h1>
        <button onClick={openAdd} className="px-4 py-2 bg-primary-600 text-white rounded-lg hover:bg-primary-700 transition-colors text-sm font-medium">
          + Register Agent
        </button>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-6 border-b border-gray-200">
        {TABS.map(t => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${
              tab === t
                ? 'border-primary-600 text-primary-600'
                : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300'
            }`}
          >
            {t}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {tab === 'Overview' && <OverviewTab agents={agents} active={active} unhealthy={unhealthy} taskCount={taskCount} onClickAgent={a => { setDetailAgent(a); setTab('Agents') }} />}
      {tab === 'Agents' && <AgentsTab agents={agents} onEdit={openEdit} onDelete={handleDelete} onDetail={setDetailAgent} onPlayground={openPlayground} />}
      {tab === 'Discover' && <DiscoverTab onPlayground={openPlayground} />}
      {tab === 'Playground' && <PlaygroundTab agents={agents} preselectedAgent={playgroundAgent} onClearPreselect={() => setPlaygroundAgent(null)} />}

      {/* Detail slide-out */}
      {detailAgent && (
        <SlidePanel title="Agent Details" onClose={() => setDetailAgent(null)}>
          <AgentDetail agent={detailAgent} onEdit={() => openEdit(detailAgent)} onDelete={() => handleDelete(detailAgent.id)} onPlayground={() => openPlayground(detailAgent.id)} />
        </SlidePanel>
      )}

      {/* Add/Edit slide-out */}
      {slideOpen && (
        <SlidePanel title={editingId ? 'Edit Agent' : 'Register Agent'} onClose={() => setSlideOpen(false)}>
          <AgentForm form={form} setForm={setForm} editingId={editingId} saving={saving} onSave={handleSave} onCancel={() => setSlideOpen(false)} />
        </SlidePanel>
      )}
    </div>
  )
}

// ── Slide Panel wrapper ──────────────────────────────────────────────

function SlidePanel({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-30">
      <div className="absolute inset-0 bg-black/20" onClick={onClose} />
      <div className="absolute right-0 top-0 h-full w-full max-w-[480px] bg-white shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-lg font-semibold text-gray-900">{title}</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600 text-xl">&times;</button>
        </div>
        <div className="flex-1 overflow-auto p-6">{children}</div>
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// TAB 1: OVERVIEW
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function OverviewTab({ agents, active, unhealthy, taskCount, onClickAgent }: {
  agents: AgentCard[]; active: number; unhealthy: number; taskCount: number
  onClickAgent: (a: AgentCard) => void
}) {
  return (
    <div>
      {/* KPI cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
        <MetricCard label="Total Agents" value={agents.length} />
        <MetricCard label="Active" value={active} />
        <MetricCard label="Unhealthy" value={unhealthy} accent={unhealthy > 0} />
        <MetricCard label="Tasks Processed" value={taskCount} />
      </div>

      {/* Agent status table */}
      <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="bg-gray-50 border-b border-gray-200">
              <th className="text-left px-4 py-3 font-medium text-gray-500">Agent</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Status</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Skills</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">URL</th>
              <th className="text-left px-4 py-3 font-medium text-gray-500">Version</th>
            </tr>
          </thead>
          <tbody>
            {agents.map(a => (
              <tr key={a.id} onClick={() => onClickAgent(a)} className="border-b border-gray-100 hover:bg-gray-50 cursor-pointer transition-colors">
                <td className="px-4 py-3 font-medium text-gray-900 flex items-center gap-2">
                  <StatusDot status={a.status} />
                  {a.name}
                </td>
                <td className="px-4 py-3"><StatusBadge status={a.status} /></td>
                <td className="px-4 py-3 text-gray-500">{a.skills?.length || 0} skills</td>
                <td className="px-4 py-3 text-gray-400 font-mono text-xs truncate max-w-[200px]">{a.url}</td>
                <td className="px-4 py-3 text-gray-400">{a.version}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {agents.length === 0 && (
          <div className="text-center py-8 text-gray-400">No agents registered</div>
        )}
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// TAB 2: AGENTS
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function AgentsTab({ agents, onEdit, onDelete, onDetail, onPlayground }: {
  agents: AgentCard[]
  onEdit: (a: AgentCard) => void
  onDelete: (id: string) => void
  onDetail: (a: AgentCard) => void
  onPlayground: (id: string) => void
}) {
  if (agents.length === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-16 text-center">
        <div className="text-5xl mb-4 text-gray-300">&#x25C7;</div>
        <h2 className="text-lg font-semibold text-gray-700 mb-2">No agents registered</h2>
        <p className="text-gray-400">Register your first A2A agent to get started</p>
      </div>
    )
  }

  return (
    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
      {agents.map(a => (
        <div
          key={a.id}
          className={`bg-white rounded-xl border border-gray-200 border-l-4 ${STATUS_BORDER[a.status] || STATUS_BORDER.inactive} p-5 hover:shadow-md transition-shadow cursor-pointer`}
          onClick={() => onDetail(a)}
        >
          <div className="flex items-start justify-between mb-3">
            <div>
              <div className="flex items-center gap-2 mb-1">
                <h3 className="text-lg font-semibold text-gray-900">{a.name}</h3>
                <StatusBadge status={a.status} />
              </div>
              <p className="text-xs text-gray-400 font-mono">{a.id}</p>
            </div>
            <div className="flex gap-1" onClick={e => e.stopPropagation()}>
              <button onClick={() => onPlayground(a.id)} className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-primary-50 text-primary-600" title="Send Task">
                &#9654;
              </button>
              <button onClick={() => onEdit(a)} className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-gray-50 text-gray-600">Edit</button>
              <button onClick={() => onDelete(a.id)} className="text-xs px-2 py-1 border border-gray-200 rounded hover:bg-red-50 text-red-600">Del</button>
            </div>
          </div>
          <p className="text-sm text-gray-600 mb-3 line-clamp-2">{a.description || 'No description'}</p>
          <div className="space-y-1.5 text-xs text-gray-500">
            <div><span className="font-medium text-gray-700">URL:</span> <span className="font-mono">{a.url}</span></div>
            {a.skills && a.skills.length > 0 && (
              <div className="flex flex-wrap gap-1 mt-2">
                {a.skills.map(s => <SkillTag key={s.id} name={s.name} />)}
              </div>
            )}
          </div>
        </div>
      ))}
    </div>
  )
}

// ── Agent Detail panel ───────────────────────────────────────────────

function AgentDetail({ agent, onEdit, onDelete, onPlayground }: {
  agent: AgentCard; onEdit: () => void; onDelete: () => void; onPlayground: () => void
}) {
  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center gap-3">
        <StatusDot status={agent.status} />
        <div>
          <h3 className="text-lg font-semibold text-gray-900">{agent.name}</h3>
          <p className="text-xs text-gray-400 font-mono">{agent.id}</p>
        </div>
        <StatusBadge status={agent.status} />
      </div>

      {/* Description */}
      <div>
        <label className="text-xs font-medium text-gray-500 block mb-1">Description</label>
        <p className="text-sm text-gray-700">{agent.description || 'No description'}</p>
      </div>

      {/* Info grid */}
      <div className="grid grid-cols-2 gap-4 text-sm">
        <div>
          <label className="text-xs font-medium text-gray-500 block mb-1">URL</label>
          <p className="text-gray-700 font-mono text-xs break-all">{agent.url}</p>
        </div>
        <div>
          <label className="text-xs font-medium text-gray-500 block mb-1">Version</label>
          <p className="text-gray-700">{agent.version}</p>
        </div>
      </div>

      {/* Skills */}
      {agent.skills && agent.skills.length > 0 && (
        <div>
          <label className="text-xs font-medium text-gray-500 block mb-2">Skills ({agent.skills.length})</label>
          <div className="space-y-2">
            {agent.skills.map(s => (
              <div key={s.id} className="flex items-start gap-2 text-sm">
                <SkillTag name={s.name} />
                {s.description && s.description !== s.name && (
                  <span className="text-gray-400 text-xs">{s.description}</span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Capabilities */}
      <div>
        <label className="text-xs font-medium text-gray-500 block mb-2">Capabilities</label>
        <div className="flex flex-wrap gap-2">
          {agent.capabilities?.streaming && <span className="px-2 py-0.5 bg-blue-50 text-blue-700 rounded text-xs">Streaming</span>}
          {agent.capabilities?.pushNotifications && <span className="px-2 py-0.5 bg-purple-50 text-purple-700 rounded text-xs">Push Notifications</span>}
          {agent.capabilities?.stateTransitionHistory && <span className="px-2 py-0.5 bg-amber-50 text-amber-700 rounded text-xs">State History</span>}
          {!agent.capabilities?.streaming && !agent.capabilities?.pushNotifications && !agent.capabilities?.stateTransitionHistory && (
            <span className="text-xs text-gray-400">None declared</span>
          )}
        </div>
      </div>

      {/* Agent Card JSON */}
      <div>
        <label className="text-xs font-medium text-gray-500 block mb-2">Agent Card JSON</label>
        <div className="bg-gray-50 rounded-lg p-3 text-xs font-mono text-gray-600 max-h-40 overflow-auto">
          {JSON.stringify(agent, null, 2)}
        </div>
        <div className="mt-2">
          <CopyButton text={JSON.stringify(agent, null, 2)} label="Copy JSON" />
        </div>
      </div>

      {/* Actions */}
      <div className="flex gap-2 pt-4 border-t border-gray-200">
        <button onClick={onPlayground} className="flex-1 px-4 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700">
          Send Task
        </button>
        <button onClick={onEdit} className="px-4 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50">
          Edit
        </button>
        <button onClick={onDelete} className="px-4 py-2.5 border border-red-200 rounded-lg text-sm text-red-600 hover:bg-red-50">
          Delete
        </button>
      </div>
    </div>
  )
}

// ── Agent Form (Add/Edit) with Skills Editor ─────────────────────────

function AgentForm({ form, setForm, editingId, saving, onSave, onCancel }: {
  form: AgentInput; setForm: (f: AgentInput) => void; editingId: string | null
  saving: boolean; onSave: () => void; onCancel: () => void
}) {
  const [skillName, setSkillName] = useState('')
  const [skillDesc, setSkillDesc] = useState('')

  const addSkill = () => {
    const name = skillName.trim()
    if (!name) return
    const id = name.toLowerCase().replace(/\s+/g, '-')
    const newSkill = { id, name, description: skillDesc.trim() || name }
    setForm({ ...form, skills: [...(form.skills || []), newSkill] })
    setSkillName('')
    setSkillDesc('')
  }

  const removeSkill = (id: string) => {
    setForm({ ...form, skills: (form.skills || []).filter(s => s.id !== id) })
  }

  return (
    <div className="space-y-5">
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
          rows={3} placeholder="Describe what this agent does — this text is used for semantic discovery" />
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

      {/* Skills editor */}
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-2">Skills</label>
        {(form.skills || []).length > 0 && (
          <div className="space-y-2 mb-3">
            {(form.skills || []).map(s => (
              <div key={s.id} className="flex items-center gap-2 bg-gray-50 rounded-lg px-3 py-2">
                <SkillTag name={s.name} />
                <span className="text-xs text-gray-400 flex-1 truncate">{s.description}</span>
                <button onClick={() => removeSkill(s.id)} className="text-red-400 hover:text-red-600 text-xs">&times;</button>
              </div>
            ))}
          </div>
        )}
        <div className="flex gap-2">
          <input value={skillName} onChange={e => setSkillName(e.target.value)} placeholder="Skill name"
            className="flex-1 px-3 py-1.5 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500"
            onKeyDown={e => e.key === 'Enter' && (e.preventDefault(), addSkill())} />
          <input value={skillDesc} onChange={e => setSkillDesc(e.target.value)} placeholder="Description (optional)"
            className="flex-1 px-3 py-1.5 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500"
            onKeyDown={e => e.key === 'Enter' && (e.preventDefault(), addSkill())} />
          <button onClick={addSkill} className="px-3 py-1.5 bg-gray-100 rounded-lg text-sm text-gray-600 hover:bg-gray-200">Add</button>
        </div>
      </div>

      {/* Capabilities */}
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

      {/* Actions */}
      <div className="flex gap-3 pt-4 border-t border-gray-200">
        <button onClick={onCancel} className="flex-1 px-4 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 hover:bg-gray-50">Cancel</button>
        <button onClick={onSave} disabled={saving} className="flex-1 px-4 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50">
          {saving ? 'Saving...' : editingId ? 'Update' : 'Register'}
        </button>
      </div>
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// TAB 3: DISCOVER
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

function DiscoverTab({ onPlayground }: { onPlayground: (id: string) => void }) {
  const [query, setQuery] = useState('')
  const [topN, setTopN] = useState(5)
  const [minScore, setMinScore] = useState(0)
  const [includeUnhealthy, setIncludeUnhealthy] = useState(false)
  const [results, setResults] = useState<DiscoverResult[] | null>(null)
  const [searching, setSearching] = useState(false)

  const handleSearch = async () => {
    if (!query.trim()) return
    setSearching(true)
    try {
      const data = await api.discoverAgents(query.trim(), topN, minScore, includeUnhealthy)
      setResults(data || [])
    } catch (e) {
      console.error('Discover failed:', e)
      setResults([])
    } finally { setSearching(false) }
  }

  const chartData = (results || []).map(r => ({
    name: r.agent.name,
    score: Math.round(r.score * 1000) / 10,
  }))

  const CHART_COLORS = ['#2563eb', '#7c3aed', '#059669', '#d97706', '#dc2626', '#6366f1', '#0891b2', '#be185d']

  return (
    <div>
      {/* Search bar */}
      <div className="bg-white rounded-xl border border-gray-200 p-5 mb-6">
        <div className="flex gap-3 mb-4">
          <input
            value={query}
            onChange={e => setQuery(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && handleSearch()}
            className="flex-1 px-4 py-2.5 border border-gray-300 rounded-lg text-sm focus:ring-2 focus:ring-primary-500 focus:border-primary-500 outline-none"
            placeholder="Describe what you need, e.g. 'translate code review to Japanese'"
          />
          <button onClick={handleSearch} disabled={searching || !query.trim()}
            className="px-5 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50 transition-colors">
            {searching ? 'Searching...' : 'Discover'}
          </button>
        </div>

        {/* Parameters */}
        <div className="flex flex-wrap items-center gap-6 text-sm text-gray-600">
          <label className="flex items-center gap-2">
            <span className="text-xs font-medium text-gray-500">Top N:</span>
            <input type="range" min={1} max={10} value={topN} onChange={e => setTopN(+e.target.value)} className="w-20 accent-primary-600" />
            <span className="text-xs font-mono w-4">{topN}</span>
          </label>
          <label className="flex items-center gap-2">
            <span className="text-xs font-medium text-gray-500">Min Score:</span>
            <input type="range" min={0} max={100} value={Math.round(minScore * 100)} onChange={e => setMinScore(+e.target.value / 100)} className="w-20 accent-primary-600" />
            <span className="text-xs font-mono w-8">{(minScore * 100).toFixed(0)}%</span>
          </label>
          <label className="flex items-center gap-2 text-xs">
            <input type="checkbox" checked={includeUnhealthy} onChange={e => setIncludeUnhealthy(e.target.checked)} className="accent-primary-600" />
            Include unhealthy
          </label>
        </div>
      </div>

      {/* Results */}
      {results !== null && (
        <div>
          {results.length === 0 ? (
            <div className="text-center py-12 text-gray-400">No matching agents found</div>
          ) : (
            <>
              {/* Score chart */}
              <div className="bg-white rounded-xl border border-gray-200 p-5 mb-6">
                <h3 className="text-sm font-medium text-gray-700 mb-3">Relevance Scores</h3>
                <ResponsiveContainer width="100%" height={results.length * 44 + 20}>
                  <BarChart data={chartData} layout="vertical" margin={{ left: 10, right: 30 }}>
                    <XAxis type="number" domain={[0, 100]} tick={{ fontSize: 11 }} unit="%" />
                    <YAxis type="category" dataKey="name" width={120} tick={{ fontSize: 12 }} />
                    <Tooltip formatter={(v) => `${v}%`} />
                    <Bar dataKey="score" radius={[0, 4, 4, 0]} barSize={24}>
                      {chartData.map((entry, i) => <Cell key={entry.name} fill={CHART_COLORS[i % CHART_COLORS.length]} />)}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>

              {/* Result cards */}
              <div className="space-y-3">
                {results.map((r, i) => (
                  <div key={r.agent.id} className="bg-white rounded-xl border border-gray-200 p-4 flex items-start gap-4">
                    {/* Rank */}
                    <div className="text-2xl font-bold text-gray-200 w-8 text-center shrink-0">#{i + 1}</div>

                    {/* Info */}
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 mb-1">
                        <span className="font-semibold text-gray-900">{r.agent.name}</span>
                        <StatusBadge status={r.agent.status} />
                      </div>
                      <p className="text-sm text-gray-500 mb-2">{r.agent.description}</p>
                      <div className="flex flex-wrap gap-1 mb-2">
                        {r.agent.skills?.map(s => <SkillTag key={s.id} name={s.name} />)}
                      </div>
                      {/* Score bar */}
                      <div className="flex items-center gap-2">
                        <div className="flex-1 h-2 bg-gray-100 rounded-full overflow-hidden">
                          <div
                            className="h-full rounded-full transition-all"
                            style={{ width: `${r.score * 100}%`, backgroundColor: CHART_COLORS[i % CHART_COLORS.length] }}
                          />
                        </div>
                        <span className="text-sm font-mono font-medium" style={{ color: CHART_COLORS[i % CHART_COLORS.length] }}>
                          {(r.score * 100).toFixed(1)}%
                        </span>
                      </div>
                      {/* Proxy URL */}
                      {r.proxy_url && (
                        <div className="mt-2 flex items-center gap-2">
                          <code className="text-xs text-gray-400 bg-gray-50 px-2 py-0.5 rounded font-mono">
                            {r.proxy_url}
                          </code>
                          <CopyButton text={r.proxy_url} label="Copy URL" />
                        </div>
                      )}
                    </div>

                    {/* Action */}
                    <button onClick={() => onPlayground(r.agent.id)}
                      className="px-3 py-1.5 bg-primary-50 text-primary-700 rounded-lg text-xs font-medium hover:bg-primary-100 shrink-0">
                      Send Task &#9654;
                    </button>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}

// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
// TAB 4: PLAYGROUND
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

type PlaygroundResponse = {
  raw: unknown
  state: string
  text: string
  latencyMs: number
  routedTo?: string
  taskId?: string
  error?: string
  streamEvents?: Array<{ type: string; data: unknown }>
}

function PlaygroundTab({ agents, preselectedAgent, onClearPreselect }: {
  agents: AgentCard[]; preselectedAgent: string | null; onClearPreselect: () => void
}) {
  const [routing, setRouting] = useState<'auto' | 'explicit'>(preselectedAgent ? 'explicit' : 'auto')
  const [agentId, setAgentId] = useState(preselectedAgent || '')
  const [method, setMethod] = useState('message/send')
  const [message, setMessage] = useState('')
  const [taskId, setTaskId] = useState('')
  const [stateFilter, setStateFilter] = useState('')
  const [sending, setSending] = useState(false)
  const [response, setResponse] = useState<PlaygroundResponse | null>(null)
  const [showRaw, setShowRaw] = useState(false)
  const [history, setHistory] = useState<PlaygroundResponse[]>([])
  const responseRef = useRef<HTMLDivElement>(null)

  // Apply preselection
  useEffect(() => {
    if (preselectedAgent) {
      setRouting('explicit')
      setAgentId(preselectedAgent)
      onClearPreselect()
    }
  }, [preselectedAgent, onClearPreselect])

  const isTaskMethod = method === 'tasks/get' || method === 'tasks/cancel'
  const isListMethod = method === 'tasks/list'
  const isStreamMethod = method === 'message/stream'
  const needsMessage = method === 'message/send' || method === 'message/stream'

  const handleSend = async () => {
    setSending(true)
    setResponse(null)
    const start = Date.now()
    const targetAgent = routing === 'explicit' && agentId ? agentId : null

    try {
      if (isStreamMethod) {
        // Streaming
        const events: Array<{ type: string; data: unknown }> = []
        const tId = taskId || `playground-${Date.now()}`
        await api.fetchA2AStream(
          targetAgent,
          { id: tId, message: { role: 'user', parts: [{ type: 'text', text: message }] } },
          (evt) => {
            events.push(evt)
            // Update response incrementally
            const lastData = evt.data as Record<string, unknown>
            const text = extractArtifactText(lastData)
            setResponse({
              raw: lastData,
              state: String((lastData as Record<string, unknown>)?.status && ((lastData as Record<string, unknown>).status as Record<string, unknown>)?.state || 'streaming'),
              text: text || `[${events.length} events received]`,
              latencyMs: Date.now() - start,
              taskId: tId,
              streamEvents: [...events],
            })
          },
        )
        const elapsed = Date.now() - start
        const finalResp: PlaygroundResponse = {
          raw: events,
          state: events.length > 0 ? 'completed' : 'unknown',
          text: events.map(e => {
            const d = e.data as Record<string, unknown>
            return extractArtifactText(d) || `[${e.type}]`
          }).join('\n'),
          latencyMs: elapsed,
          taskId: tId,
          streamEvents: events,
        }
        setResponse(finalResp)
        setHistory(h => [finalResp, ...h].slice(0, 10))
      } else if (isTaskMethod) {
        const resp = await api.sendA2A(null, method, { id: taskId })
        const elapsed = Date.now() - start
        const result = resp.result || {}
        const pr: PlaygroundResponse = {
          raw: resp,
          state: result.status?.state || 'unknown',
          text: extractArtifactText(result) || JSON.stringify(result, null, 2),
          latencyMs: elapsed,
          taskId: result.id,
          error: resp.error?.message,
        }
        setResponse(pr)
        setHistory(h => [pr, ...h].slice(0, 10))
      } else if (isListMethod) {
        const resp = await api.getTasksList(stateFilter || undefined)
        const elapsed = Date.now() - start
        const tasks = resp.result || []
        const pr: PlaygroundResponse = {
          raw: resp,
          state: 'completed',
          text: `${tasks.length} tasks found`,
          latencyMs: elapsed,
        }
        setResponse(pr)
        setHistory(h => [pr, ...h].slice(0, 10))
      } else {
        // message/send
        const tId = taskId || `playground-${Date.now()}`
        const resp = await api.sendA2A(
          targetAgent,
          method,
          { id: tId, message: { role: 'user', parts: [{ type: 'text', text: message }] } },
        )
        const elapsed = Date.now() - start
        const result = resp.result || {}
        const pr: PlaygroundResponse = {
          raw: resp,
          state: result.status?.state || 'unknown',
          text: extractArtifactText(result) || '',
          latencyMs: elapsed,
          taskId: result.id,
          error: resp.error?.message,
        }
        setResponse(pr)
        setHistory(h => [pr, ...h].slice(0, 10))
      }
    } catch (e) {
      const elapsed = Date.now() - start
      const pr: PlaygroundResponse = {
        raw: { error: String(e) },
        state: 'failed',
        text: '',
        latencyMs: elapsed,
        error: String(e),
      }
      setResponse(pr)
      setHistory(h => [pr, ...h].slice(0, 10))
    } finally {
      setSending(false)
      responseRef.current?.scrollIntoView({ behavior: 'smooth' })
    }
  }

  return (
    <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
      {/* Left: Request builder */}
      <div className="bg-white rounded-xl border border-gray-200 p-5">
        <h3 className="text-sm font-semibold text-gray-900 mb-4">Request</h3>

        {/* Routing */}
        <div className="mb-4">
          <label className="text-xs font-medium text-gray-500 block mb-2">Routing</label>
          <div className="flex gap-2">
            <button onClick={() => setRouting('auto')}
              className={`flex-1 px-3 py-2 rounded-lg text-sm border transition-colors ${routing === 'auto' ? 'bg-primary-50 border-primary-300 text-primary-700 font-medium' : 'border-gray-200 text-gray-500 hover:bg-gray-50'}`}>
              Auto-route
            </button>
            <button onClick={() => setRouting('explicit')}
              className={`flex-1 px-3 py-2 rounded-lg text-sm border transition-colors ${routing === 'explicit' ? 'bg-primary-50 border-primary-300 text-primary-700 font-medium' : 'border-gray-200 text-gray-500 hover:bg-gray-50'}`}>
              Explicit Agent
            </button>
          </div>
        </div>

        {/* Agent selector */}
        {routing === 'explicit' && (
          <div className="mb-4">
            <label className="text-xs font-medium text-gray-500 block mb-1">Agent</label>
            <select value={agentId} onChange={e => setAgentId(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500">
              <option value="">Select agent...</option>
              {agents.map(a => (
                <option key={a.id} value={a.id}>{a.name} ({a.status})</option>
              ))}
            </select>
          </div>
        )}

        {/* Method */}
        <div className="mb-4">
          <label className="text-xs font-medium text-gray-500 block mb-1">Method</label>
          <select value={method} onChange={e => setMethod(e.target.value)}
            className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500">
            <option value="message/send">message/send</option>
            <option value="message/stream">message/stream (SSE)</option>
            <option value="tasks/get">tasks/get</option>
            <option value="tasks/cancel">tasks/cancel</option>
            <option value="tasks/list">tasks/list</option>
          </select>
        </div>

        {/* Message (for send methods) */}
        {needsMessage && (
          <div className="mb-4">
            <label className="text-xs font-medium text-gray-500 block mb-1">Message</label>
            <textarea value={message} onChange={e => setMessage(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500"
              rows={4} placeholder="Type your message here..." />
          </div>
        )}

        {/* Task ID (for get/cancel) */}
        {(isTaskMethod || needsMessage) && (
          <div className="mb-4">
            <label className="text-xs font-medium text-gray-500 block mb-1">
              Task ID {needsMessage && <span className="text-gray-400">(auto-generated if empty)</span>}
            </label>
            <input value={taskId} onChange={e => setTaskId(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500"
              placeholder={isTaskMethod ? 'Enter task ID' : 'Optional — leave empty for auto'} />
          </div>
        )}

        {/* State filter (for list) */}
        {isListMethod && (
          <div className="mb-4">
            <label className="text-xs font-medium text-gray-500 block mb-1">State Filter (optional)</label>
            <select value={stateFilter} onChange={e => setStateFilter(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-lg text-sm outline-none focus:ring-1 focus:ring-primary-500">
              <option value="">All states</option>
              <option value="completed">completed</option>
              <option value="working">working</option>
              <option value="failed">failed</option>
              <option value="canceled">canceled</option>
            </select>
          </div>
        )}

        {/* Send button */}
        <button onClick={handleSend} disabled={sending || (needsMessage && !message.trim()) || (isTaskMethod && !taskId.trim())}
          className="w-full px-4 py-2.5 bg-primary-600 text-white rounded-lg text-sm font-medium hover:bg-primary-700 disabled:opacity-50 transition-colors">
          {sending ? 'Sending...' : '&#9654; Send Request'}
        </button>
      </div>

      {/* Right: Response + history */}
      <div ref={responseRef}>
        {/* Response viewer */}
        <div className="bg-white rounded-xl border border-gray-200 p-5 mb-4">
          <div className="flex items-center justify-between mb-3">
            <h3 className="text-sm font-semibold text-gray-900">Response</h3>
            {response && (
              <div className="flex gap-2">
                <button onClick={() => setShowRaw(false)}
                  className={`text-xs px-2 py-1 rounded ${!showRaw ? 'bg-primary-50 text-primary-700' : 'text-gray-500 hover:bg-gray-50'}`}>
                  Pretty
                </button>
                <button onClick={() => setShowRaw(true)}
                  className={`text-xs px-2 py-1 rounded ${showRaw ? 'bg-primary-50 text-primary-700' : 'text-gray-500 hover:bg-gray-50'}`}>
                  Raw JSON
                </button>
              </div>
            )}
          </div>

          {!response && !sending && (
            <div className="text-center py-8 text-gray-400 text-sm">Send a request to see the response here</div>
          )}

          {sending && (
            <div className="text-center py-8 text-primary-500 text-sm">
              {isStreamMethod ? 'Streaming...' : 'Waiting for response...'}
            </div>
          )}

          {response && (
            <div>
              {/* Status bar */}
              <div className="flex flex-wrap gap-4 mb-4 text-xs">
                <span className="flex items-center gap-1.5">
                  <StatusDot status={response.state === 'completed' ? 'active' : response.state === 'failed' ? 'unhealthy' : 'inactive'} />
                  <span className="font-medium">{response.state}</span>
                </span>
                <span className="text-gray-400">Latency: <span className="font-mono">{response.latencyMs}ms</span></span>
                {response.taskId && <span className="text-gray-400">Task: <span className="font-mono">{response.taskId}</span></span>}
              </div>

              {/* Error */}
              {response.error && (
                <div className="bg-red-50 border border-red-200 rounded-lg p-3 mb-4 text-sm text-red-700">{response.error}</div>
              )}

              {/* Content */}
              {showRaw ? (
                <div className="bg-gray-50 rounded-lg p-3 text-xs font-mono text-gray-600 max-h-80 overflow-auto whitespace-pre-wrap">
                  {JSON.stringify(response.raw, null, 2)}
                </div>
              ) : (
                <div className="bg-gray-50 rounded-lg p-4 max-h-80 overflow-auto">
                  <div className="text-sm text-gray-700 whitespace-pre-wrap">{response.text || '(empty response)'}</div>
                </div>
              )}

              {/* Stream events */}
              {response.streamEvents && response.streamEvents.length > 0 && !showRaw && (
                <div className="mt-3">
                  <p className="text-xs font-medium text-gray-500 mb-1">SSE Events ({response.streamEvents.length})</p>
                  <div className="space-y-1">
                    {response.streamEvents.map((evt, i) => (
                      <div key={i} className="text-xs bg-gray-50 rounded px-2 py-1 font-mono text-gray-500">
                        <span className="text-primary-600">{evt.type}</span> {JSON.stringify(evt.data).slice(0, 100)}...
                      </div>
                    ))}
                  </div>
                </div>
              )}

              {/* Copy buttons */}
              <div className="flex gap-2 mt-3">
                {response.text && <CopyButton text={response.text} label="Copy Text" />}
                <CopyButton text={JSON.stringify(response.raw, null, 2)} label="Copy JSON" />
              </div>
            </div>
          )}
        </div>

        {/* Request history */}
        {history.length > 0 && (
          <div className="bg-white rounded-xl border border-gray-200 p-4">
            <h4 className="text-xs font-medium text-gray-500 mb-2">Recent Requests ({history.length})</h4>
            <div className="space-y-1.5">
              {history.map((h, i) => (
                <button
                  key={i}
                  onClick={() => { setResponse(h); setShowRaw(false) }}
                  className="w-full text-left px-3 py-2 rounded-lg text-xs hover:bg-gray-50 transition-colors flex items-center gap-2"
                >
                  <StatusDot status={h.state === 'completed' ? 'active' : h.state === 'failed' ? 'unhealthy' : 'inactive'} />
                  <span className="font-medium text-gray-700 truncate flex-1">{h.taskId || 'task'}</span>
                  <span className="text-gray-400">{h.state}</span>
                  <span className="text-gray-400 font-mono">{h.latencyMs}ms</span>
                </button>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// ── Utility ──────────────────────────────────────────────────────────

function extractArtifactText(result: unknown): string {
  if (!result || typeof result !== 'object') return ''
  const r = result as Record<string, unknown>
  const artifacts = r.artifacts as Array<{ parts?: Array<{ type?: string; text?: string }> }> | undefined
  if (!artifacts) return ''
  for (const a of artifacts) {
    for (const p of a.parts || []) {
      if (p.type === 'text' && p.text) return p.text
    }
  }
  return ''
}
