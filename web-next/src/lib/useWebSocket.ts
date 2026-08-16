'use client'

import { useEffect, useRef } from 'react'

type EventHandler = (data: any) => void

interface UseWebSocketOptions {
  onEvent?: EventHandler
  onConnect?: () => void
  onDisconnect?: () => void
}

export function useWebSocket({ onEvent, onConnect, onDisconnect }: UseWebSocketOptions = {}) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef(0)

  // 把回调存进 ref, 使下方 effect 能以空依赖运行一次, 避免父组件传入的
  // 内联箭头函数每次渲染都改变 connect 身份、导致 useEffect 反复重跑、
  // 在 socket 仍处于 CONNECTING 时调用 close() 触发
  // "WebSocket is closed before the connection is established" 重连风暴。
  const cbRef = useRef({ onEvent, onConnect, onDisconnect })
  cbRef.current = { onEvent, onConnect, onDisconnect }

  useEffect(() => {
    let active = true
    let retryTimer: ReturnType<typeof setTimeout> | null = null

    const connect = () => {
      const token = localStorage.getItem('opsorigin_token')
      if (!token || !active) return

      const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
      // WebSocket 不能走 Next.js 的 /api 代理(rewrite 不支持 upgrade/hijack,
      // 会报 http.ResponseWriter does not implement http.Hijacker)。必须直连后端。
      // 默认把前端端口(:3000)换成后端外暴端口(:8080); 容器/反代等场景可用
      // NEXT_PUBLIC_WS_URL 覆盖(如 wss://example.com:8080)。
      const wsBase = process.env.NEXT_PUBLIC_WS_URL || `${protocol}//${location.hostname}:8080`
      // 后端 WebSocket 握手需带 JWT(浏览器无法自定义头), 服务端校验后派生
      // 用户身份, 才能做「只推给某个人」的定向推送(站内消息角标)。
      const url = `${wsBase}/api/v1/ws?token=${encodeURIComponent(token)}`

      const ws = new WebSocket(url)
      wsRef.current = ws

      ws.onopen = () => {
        if (!active) return
        reconnectRef.current = 0
      }
      ws.onmessage = (event) => {
        if (!active) return
        try {
          const data = JSON.parse(event.data)
          if (data.type === 'ping' || data.type === 'heartbeat') return
          cbRef.current.onEvent?.(data)
        } catch { /* ignore malformed */ }
      }
      ws.onclose = () => {
        if (!active) return
        cbRef.current.onDisconnect?.()
        const delay = Math.min(1000 * Math.pow(2, reconnectRef.current), 15000)
        reconnectRef.current++
        retryTimer = setTimeout(connect, delay)
      }
      ws.onerror = () => ws.close()
    }

    connect()

    return () => {
      active = false
      if (retryTimer) clearTimeout(retryTimer)
      const ws = wsRef.current
      wsRef.current = null
      if (!ws) return
      if (ws.readyState === WebSocket.CONNECTING) {
        // 直接 close() 一个 CONNECTING 的 socket 会让浏览器打印
        // "WebSocket is closed before the connection is established"。
        // 改为等它连上再关(StrictMode 双挂载产生的临时连接走这里)。
        ws.onopen = () => ws.close()
      } else {
        ws.close()
      }
    }
  }, [])

  return { ws: wsRef }
}
