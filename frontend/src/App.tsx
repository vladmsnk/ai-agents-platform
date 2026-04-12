import { useState } from 'react'
import { Routes, Route, NavLink, Navigate } from 'react-router-dom'
import { useAuth } from './AuthContext'
import Providers from './pages/Providers'
import Models from './pages/Models'
import Monitoring from './pages/Monitoring'
import Agents from './pages/Agents'

const navItems = [
  { to: '/providers', label: 'Providers', icon: '⬡' },
  { to: '/models', label: 'Models', icon: '◈' },
  { to: '/agents', label: 'Agents', icon: '◇' },
  { to: '/monitoring', label: 'Monitoring', icon: '◉' },
]

function LoginPage({ onLogin }: { onLogin: () => void }) {
  return (
    <div className="flex items-center justify-center min-h-screen bg-gray-50">
      <div className="bg-white rounded-xl shadow-lg p-8 max-w-sm w-full text-center">
        <div className="text-5xl mb-4">⬡</div>
        <h1 className="text-2xl font-bold text-gray-900 mb-2">LLM Gateway</h1>
        <p className="text-gray-500 text-sm mb-6">Sign in to access the admin panel</p>
        <button
          onClick={onLogin}
          className="w-full bg-primary-600 hover:bg-primary-700 text-white font-medium py-2.5 px-4 rounded-lg transition-colors"
        >
          Sign in with Keycloak
        </button>
      </div>
    </div>
  )
}

export default function App() {
  const { user, loading, login, logout } = useAuth()
  const [sidebarOpen, setSidebarOpen] = useState(false)

  const closeSidebar = () => setSidebarOpen(false)

  if (loading) {
    return (
      <div className="flex items-center justify-center min-h-screen bg-gray-50">
        <div className="text-gray-400 text-sm">Loading...</div>
      </div>
    )
  }

  if (!user) {
    return <LoginPage onLogin={login} />
  }

  return (
    <div className="flex h-screen bg-gray-50">
      {/* Mobile header */}
      <div className="fixed top-0 left-0 right-0 z-20 bg-sidebar flex items-center px-4 py-3 md:hidden">
        <button onClick={() => setSidebarOpen(!sidebarOpen)} className="text-white p-1 mr-3">
          <svg width="24" height="24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
            <path d="M3 6h18M3 12h18M3 18h18" />
          </svg>
        </button>
        <h1 className="text-white text-lg font-semibold tracking-tight">LLM Gateway</h1>
      </div>

      {/* Sidebar overlay for mobile */}
      {sidebarOpen && (
        <div className="fixed inset-0 bg-black/30 z-30 md:hidden" onClick={closeSidebar} />
      )}

      {/* Sidebar */}
      <aside className={`fixed md:static inset-y-0 left-0 z-40 w-60 bg-sidebar flex flex-col transform transition-transform duration-200 md:translate-x-0 ${sidebarOpen ? 'translate-x-0' : '-translate-x-full'}`}>
        <div className="px-5 py-6">
          <h1 className="text-white text-lg font-semibold tracking-tight">LLM Gateway</h1>
          <p className="text-slate-400 text-xs mt-0.5">Admin Panel</p>
        </div>
        <nav className="flex-1 px-3 space-y-1">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              onClick={closeSidebar}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm transition-colors ${
                  isActive
                    ? 'bg-sidebar-active text-white font-medium'
                    : 'text-slate-300 hover:bg-sidebar-hover hover:text-white'
                }`
              }
            >
              <span className="text-base">{item.icon}</span>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="px-4 py-4 border-t border-slate-700">
          <div className="flex items-center justify-between">
            <div className="min-w-0">
              <p className="text-slate-200 text-sm truncate">{user.preferred_username}</p>
              <p className="text-slate-500 text-xs">v1.0.0</p>
            </div>
            <button
              onClick={logout}
              title="Sign out"
              className="text-slate-400 hover:text-white transition-colors text-xs px-2 py-1 rounded hover:bg-sidebar-hover shrink-0"
            >
              Sign out
            </button>
          </div>
        </div>
      </aside>

      {/* Content */}
      <main className="flex-1 overflow-auto pt-12 md:pt-0">
        <Routes>
          <Route path="/providers" element={<Providers />} />
          <Route path="/models" element={<Models />} />
          <Route path="/agents" element={<Agents />} />
          <Route path="/monitoring" element={<Monitoring />} />
          <Route path="*" element={<Navigate to="/providers" replace />} />
        </Routes>
      </main>
    </div>
  )
}
