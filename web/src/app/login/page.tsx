
import { useState, useEffect, useRef, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore } from '@/stores/auth'
import { Hexagon, Eye, EyeOff } from 'lucide-react'

// 星星类型
interface Star {
  x: number
  y: number
  size: number
  opacity: number
  speed: number // 视差速度
}

export default function LoginPage() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [showPw, setShowPw] = useState(false)
  const { login, loading, error, token, init } = useAuthStore()
  const navigate = useNavigate()
  const canvasRef = useRef<HTMLCanvasElement>(null)
  const starsRef = useRef<Star[]>([])
  const mouseRef = useRef({ x: 0, y: 0 })
  const animFrameRef = useRef<number>(0)

  useEffect(() => {
    init()
  }, [init])

  useEffect(() => {
    if (token) navigate('/dashboard', { replace: true })
  }, [token, navigate])

  // 初始化星星
  const initStars = useCallback((width: number, height: number) => {
    const stars: Star[] = []
    const count = Math.floor((width * height) / 3000) // 密度
    for (let i = 0; i < count; i++) {
      stars.push({
        x: Math.random() * width,
        y: Math.random() * height,
        size: Math.random() * 2 + 0.5,
        opacity: Math.random() * 0.8 + 0.2,
        speed: Math.random() * 0.5 + 0.1, // 0.1 ~ 0.6 视差速度
      })
    }
    starsRef.current = stars
  }, [])

  // 绘制星空
  const drawStars = useCallback((ctx: CanvasRenderingContext2D, width: number, height: number) => {
    const mx = (mouseRef.current.x - width / 2) / width
    const my = (mouseRef.current.y - height / 2) / height

    ctx.clearRect(0, 0, width, height)

    // 渐变背景
    const bg = ctx.createLinearGradient(0, 0, 0, height)
    bg.addColorStop(0, '#0a0e27')
    bg.addColorStop(0.5, '#0f1a3a')
    bg.addColorStop(1, '#1a1040')
    ctx.fillStyle = bg
    ctx.fillRect(0, 0, width, height)

    // 绘制星星（视差偏移）
    for (const star of starsRef.current) {
      const offsetX = mx * star.speed * 60
      const offsetY = my * star.speed * 60
      const sx = ((star.x + offsetX) % width + width) % width
      const sy = ((star.y + offsetY) % height + height) % height

      ctx.beginPath()
      ctx.arc(sx, sy, star.size, 0, Math.PI * 2)
      ctx.fillStyle = `rgba(255, 255, 255, ${star.opacity})`
      ctx.fill()

      // 大星星加光晕
      if (star.size > 1.5) {
        ctx.beginPath()
        ctx.arc(sx, sy, star.size * 3, 0, Math.PI * 2)
        ctx.fillStyle = `rgba(180, 200, 255, ${star.opacity * 0.1})`
        ctx.fill()
      }
    }

    // 随机闪烁的亮星
    const time = Date.now() / 1000
    for (let i = 0; i < 5; i++) {
      const flickerStar = starsRef.current[Math.floor((Math.sin(time + i * 1.7) * 0.5 + 0.5) * starsRef.current.length)]
      if (flickerStar && flickerStar.size > 1.2) {
        const offsetX = mx * flickerStar.speed * 60
        const offsetY = my * flickerStar.speed * 60
        const sx = ((flickerStar.x + offsetX) % width + width) % width
        const sy = ((flickerStar.y + offsetY) % height + height) % height
        const flickerOpacity = 0.5 + Math.sin(time * 3 + i) * 0.5

        ctx.beginPath()
        ctx.arc(sx, sy, flickerStar.size * 2, 0, Math.PI * 2)
        ctx.fillStyle = `rgba(200, 220, 255, ${flickerOpacity * 0.3})`
        ctx.fill()
      }
    }
  }, [])

  // 动画循环
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return

    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const resize = () => {
      canvas.width = window.innerWidth
      canvas.height = window.innerHeight
      initStars(canvas.width, canvas.height)
    }
    resize()
    window.addEventListener('resize', resize)

    const animate = () => {
      drawStars(ctx, canvas.width, canvas.height)
      animFrameRef.current = requestAnimationFrame(animate)
    }
    animate()

    return () => {
      window.removeEventListener('resize', resize)
      cancelAnimationFrame(animFrameRef.current)
    }
  }, [initStars, drawStars])

  // 鼠标移动
  useEffect(() => {
    const handleMouseMove = (e: MouseEvent) => {
      mouseRef.current = { x: e.clientX, y: e.clientY }
    }
    window.addEventListener('mousemove', handleMouseMove)
    return () => window.removeEventListener('mousemove', handleMouseMove)
  }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    try {
      await login(username, password)
      navigate('/dashboard')
    } catch { /* error handled by store */ }
  }

  return (
    <div className="min-h-screen relative overflow-hidden">
      {/* 星空背景 */}
      <canvas ref={canvasRef} className="fixed inset-0 w-full h-full" />

      {/* 登录卡片 */}
      <div className="min-h-screen flex items-center justify-center p-4 relative z-10">
        <div className="relative w-full max-w-sm">
          {/* 卡片光晕 */}
          <div className="absolute -inset-2 bg-gradient-to-r from-blue-400/20 via-purple-400/10 to-blue-400/20 rounded-3xl blur-2xl opacity-60" />

          {/* 卡片主体 - 白天配色 */}
          <div className="relative bg-white/95 backdrop-blur-xl border border-gray-200/50 rounded-2xl p-8 shadow-2xl">
            {/* Logo */}
            <div className="flex flex-col items-center mb-8">
              <div className="relative flex items-center justify-center w-14 h-14 mb-3">
                <Hexagon className="w-14 h-14 text-blue-500" strokeWidth={1.5} />
                <span className="absolute text-sm font-bold text-blue-600 font-mono">ON</span>
              </div>
              <h1 className="text-xl font-bold text-gray-800 tracking-tight">OpsOrigin</h1>
              <p className="text-xs text-gray-500 mt-1">AI-native Operations</p>
            </div>

            {/* 表单 */}
            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-gray-600 mb-1.5 tracking-wider uppercase">Username</label>
                <input
                  type="text"
                  value={username}
                  onChange={e => setUsername(e.target.value)}
                  placeholder="admin"
                  className="w-full px-3 py-2.5 bg-gray-50 border border-gray-200 rounded-lg text-sm text-gray-800 placeholder:text-gray-400 focus:outline-none focus:border-blue-400 focus:ring-2 focus:ring-blue-100 transition-all font-mono"
                />
              </div>

              <div className="relative">
                <label className="block text-xs font-semibold text-gray-600 mb-1.5 tracking-wider uppercase">Password</label>
                <input
                  type={showPw ? 'text' : 'password'}
                  value={password}
                  onChange={e => setPassword(e.target.value)}
                  placeholder="••••••••"
                  className="w-full px-3 py-2.5 bg-gray-50 border border-gray-200 rounded-lg text-sm text-gray-800 placeholder:text-gray-400 focus:outline-none focus:border-blue-400 focus:ring-2 focus:ring-blue-100 transition-all font-mono pr-10"
                />
                <button type="button" onClick={() => setShowPw(!showPw)} className="absolute right-3 bottom-2.5 text-gray-400 hover:text-gray-600 transition-colors">
                  {showPw ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                </button>
              </div>

              {error && (
                <p className="text-xs text-red-600 bg-red-50 border border-red-200 rounded-lg px-3 py-2">{error}</p>
              )}

              <button
                type="submit"
                disabled={loading}
                className="w-full py-2.5 bg-blue-500 hover:bg-blue-600 disabled:opacity-50 text-white text-sm font-semibold rounded-lg transition-all duration-200 shadow-lg shadow-blue-500/25 hover:shadow-blue-500/40"
              >
                {loading ? 'Signing in...' : 'Sign In'}
              </button>
            </form>

            <p className="text-[10px] text-gray-400 text-center mt-6 font-mono">
              demo: admin / admin123
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
