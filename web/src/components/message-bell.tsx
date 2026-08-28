
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bell, CheckCheck, CircleDot, ExternalLink, X } from 'lucide-react'
import { useWebSocket } from '@/lib/useWebSocket'
import { useMessageStore, MESSAGE_CATEGORY_COLORS, MESSAGE_CATEGORY_LABELS, type InboxMessage } from '@/stores/message'

// 相对时间: 刚刚 / N 分钟前 / N 小时前 / MM-DD HH:mm
function relativeTime(ts: string): string {
  const t = new Date(ts).getTime()
  if (Number.isNaN(t)) return ''
  const diff = Date.now() - t
  if (diff < 60_000) return '刚刚'
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`
  const d = new Date(t)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

export function MessageBell() {
  const navigate = useNavigate()
  const {
    messages,
    unreadCount,
    open,
    loadUnreadCount,
    loadRecent,
    markRead,
    markAllRead,
    setOpen,
  } = useMessageStore()

  const [mounted, setMounted] = useState(false)
  const wrapRef = useRef<HTMLDivElement>(null)

  useEffect(() => setMounted(true), [])

  // 初次加载未读数 + 每 30s 轮询兜底(WS 断线时仍能刷新角标)。
  useEffect(() => {
    void loadUnreadCount()
    const timer = setInterval(() => void loadUnreadCount(), 30_000)
    return () => clearInterval(timer)
  }, [loadUnreadCount])

  // 订阅 WebSocket: 收到 message.new 实时更新角标与列表。
  // 用 ref 持有稳定引用, 避免 onEvent 每次渲染变化导致 WS 反复重连。
  const onWsEvent = useRef((data: any) => {
    if (data?.type === 'message.new' && data.message) {
      useMessageStore.getState().pushIncoming(data.message as InboxMessage)
    }
  }).current

  useWebSocket({ onEvent: onWsEvent })

  // 点击外部关闭面板。
  useEffect(() => {
    if (!open) return
    const onDoc = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [open, setOpen])

  const handleOpen = () => {
    const next = !open
    setOpen(next)
    if (next) void loadRecent()
  }

  const handleClick = (m: InboxMessage) => {
    void markRead(m.id)
    setOpen(false)
    if (m.link) navigate(m.link)
  }

  if (!mounted) return null

  return (
    <div className="relative" ref={wrapRef}>
      <button
        onClick={handleOpen}
        className="relative p-1.5 rounded-md text-muted-foreground hover:text-gray-900 hover:bg-gray-200 dark:hover:text-white dark:hover:bg-white/10 transition-colors"
        title="站内消息"
      >
        <Bell className="w-4 h-4" />
        {unreadCount > 0 && (
          <span className="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 rounded-full bg-red-500 text-white text-[10px] font-bold flex items-center justify-center leading-none">
            {unreadCount > 99 ? '99+' : unreadCount}
          </span>
        )}
      </button>

      {open && (
        <div className="absolute bottom-full right-0 mb-2 w-80 max-h-[70vh] flex flex-col rounded-xl border border-gray-200 bg-white shadow-2xl shadow-black/20 dark:border-white/10 dark:bg-[#111827] overflow-hidden z-50">
          <div className="flex items-center justify-between px-3 py-2.5 border-b border-gray-100 dark:border-white/10">
            <div className="flex items-center gap-2">
              <Bell className="w-4 h-4 text-blue-500" />
              <span className="text-sm font-semibold text-gray-900 dark:text-white">站内消息</span>
              {unreadCount > 0 && (
                <span className="px-1.5 py-0.5 rounded-full bg-red-500/10 text-red-500 text-[10px] font-bold">{unreadCount} 未读</span>
              )}
            </div>
            <button
              onClick={() => void markAllRead()}
              className="flex items-center gap-1 text-[11px] text-muted-foreground hover:text-blue-500 transition-colors"
              title="全部标为已读"
            >
              <CheckCheck className="w-3.5 h-3.5" /> 全部已读
            </button>
          </div>

          <div className="flex-1 overflow-y-auto">
            {messages.length === 0 ? (
              <div className="px-4 py-10 text-center text-sm text-muted-foreground">暂无消息</div>
            ) : (
              <ul className="divide-y divide-gray-100 dark:divide-white/5">
                {messages.map(m => (
                  <li
                    key={m.id}
                    className="group px-3 py-2.5 hover:bg-gray-50 dark:hover:bg-white/5 cursor-pointer transition-colors"
                    onClick={() => handleClick(m)}
                  >
                    <div className="flex items-start gap-2">
                      {!m.read && <CircleDot className="w-3.5 h-3.5 text-blue-500 mt-1 shrink-0" />}
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-1.5">
                          <span className={`text-[10px] font-medium ${MESSAGE_CATEGORY_COLORS[m.category] || 'text-gray-500'}`}>
                            {MESSAGE_CATEGORY_LABELS[m.category] || m.category}
                          </span>
                          <span className="text-[10px] text-muted-foreground/60 truncate">{m.actor && `· ${m.actor}`}</span>
                          <span className="ml-auto text-[10px] text-muted-foreground/60 shrink-0">{relativeTime(m.created_at)}</span>
                        </div>
                        <p className={`text-sm truncate ${m.read ? 'text-muted-foreground' : 'text-gray-900 dark:text-white font-medium'}`}>
                          {m.title}
                        </p>
                        {m.content && (
                          <p className="text-xs text-muted-foreground/80 line-clamp-2 mt-0.5">{m.content}</p>
                        )}
                        {m.link && (
                          <span className="mt-1 inline-flex items-center gap-0.5 text-[11px] text-blue-500 opacity-0 group-hover:opacity-100 transition-opacity">
                            <ExternalLink className="w-3 h-3" /> 查看
                          </span>
                        )}
                      </div>
                      <button
                        onClick={(e) => { e.stopPropagation(); void markRead(m.id) }}
                        className="opacity-0 group-hover:opacity-100 text-muted-foreground/50 hover:text-gray-900 dark:hover:text-white transition-opacity shrink-0"
                        title="标为已读"
                      >
                        <X className="w-3.5 h-3.5" />
                      </button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
