'use client'

import { useState, useEffect } from 'react'
import { useRouter } from 'next/navigation'
import { useAuthStore } from '@/stores/auth'
import { Hexagon, Eye, EyeOff } from 'lucide-react'

export default function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPw, setShowPw] = useState(false)
  const { login, loading, error, token, init } = useAuthStore()
  const router = useRouter()

  useEffect(() => {
    init()
  }, [init])

  useEffect(() => {
    if (token) router.replace('/dashboard')
  }, [token, router])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      await login(username, password)
      router.push('/dashboard')
    } catch { /* error handled by store */ }
  }

  return (
    <div className="min-h-screen bg-[#0B0F19] flex items-center justify-center p-4">
      <div className="fixed inset-0 pointer-events-none opacity-[0.03] bg-[url('data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzMDAiIGhlaWdodD0iMzAwIj48ZmlsdGVyIGlkPSJmIj48ZmVUdXJidWxlbmNlIHR5cGU9ImZyYWN0YWxOb2lzZSIgYmFzZUZyZXF1ZW5jeT0iLjc0IiBudW1PY3RhdmVzPSIzIi8+PC9maWx0ZXI+PHJlY3Qgd2lkdGg9IjEwMCUiIGhlaWdodD0iMTAwJSIgZmlsdGVyPSJ1cmwoI2YpIiBvcGFjaXR5PSIwLjA0Ii8+PC9zdmc+')]" />

      <div className="relative w-full max-w-sm">
        <div className="absolute -inset-1 bg-gradient-to-r from-emerald-500/20 via-transparent to-emerald-500/20 rounded-2xl blur-xl" />

        <div className="relative bg-[rgba(15,20,30,0.9)] backdrop-blur-2xl border border-[rgba(255,255,255,0.06)] rounded-2xl p-8">
          <div className="flex flex-col items-center mb-8">
            <div className="relative flex items-center justify-center w-12 h-12 mb-3">
              <Hexagon className="w-12 h-12 text-emerald-400" strokeWidth={1.5} />
              <span className="absolute text-xs font-bold text-[#0B0F19] font-mono">ON</span>
              <div className="absolute inset-0 animate-pulse-ring opacity-40 rounded-full" />
            </div>
            <h1 className="text-lg font-semibold text-white tracking-tight">OpsOrigin</h1>
            <p className="text-xs text-muted-foreground mt-1">Control Plane · AI-native Operations</p>
          </div>

          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-muted-foreground mb-1.5 tracking-wider uppercase">Username</label>
              <input
                type="text"
                value={username}
                onChange={e => setUsername(e.target.value)}
                placeholder="admin"
                className="w-full px-3 py-2.5 bg-[rgba(0,0,0,0.3)] border border-[rgba(255,255,255,0.08)] rounded-lg text-sm text-white placeholder:text-muted-foreground/50 focus:outline-none focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/20 transition-colors font-mono"
              />
            </div>

            <div className="relative">
              <label className="block text-xs font-medium text-muted-foreground mb-1.5 tracking-wider uppercase">Password</label>
              <input
                type={showPw ? 'text' : 'password'}
                value={password}
                onChange={e => setPassword(e.target.value)}
                placeholder="••••••••"
                className="w-full px-3 py-2.5 bg-[rgba(0,0,0,0.3)] border border-[rgba(255,255,255,0.08)] rounded-lg text-sm text-white placeholder:text-muted-foreground/50 focus:outline-none focus:border-emerald-500/50 focus:ring-1 focus:ring-emerald-500/20 transition-colors font-mono pr-10"
              />
              <button type="button" onClick={() => setShowPw(!showPw)} className="absolute right-3 bottom-2.5 text-muted-foreground/50 hover:text-muted-foreground transition-colors">
                {showPw ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>

            {error && <p className="text-xs text-red-400 bg-red-500/10 border border-red-500/20 rounded-lg px-3 py-2">{error}</p>}

            <button
              type="submit"
              disabled={loading}
              className="w-full py-2.5 bg-emerald-500 hover:bg-emerald-400 disabled:opacity-50 text-[#0B0F19] text-sm font-semibold rounded-lg transition-all duration-200 hover:shadow-[0_0_20px_rgba(52,211,153,0.3)]"
            >
              {loading ? 'Signing in...' : 'Sign In'}
            </button>
          </form>

          <p className="text-[10px] text-muted-foreground/40 text-center mt-6 font-mono">
            demo: admin / admin123
          </p>
        </div>
      </div>
    </div>
  )
}
