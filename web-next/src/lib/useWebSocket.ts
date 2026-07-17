'use client'

import { useEffect, useRef, useCallback } from 'react'

type EventHandler = (data: any) => void

interface UseWebSocketOptions {
  onEvent?: EventHandler
  onConnect?: () => void
  onDisconnect?: () => void
}

export function useWebSocket({ onEvent, onConnect, onDisconnect }: UseWebSocketOptions = {}) {
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectRef = useRef<number>(0)

  const connect = useCallback(() => {
    if (typeof window === 'undefined') return
    const token = localStorage.getItem('opsnexus_token')
    if (!token) return

    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const url = `${protocol}//${location.host}/api/v1/ws`

    const ws = new WebSocket(url)
    wsRef.current = ws

    ws.onopen = () => {
      reconnectRef.current = 0
      onConnect?.()
    }

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (data.type === 'ping' || data.type === 'heartbeat') return
        onEvent?.(data)
      } catch { /* ignore malformed */ }
    }

    ws.onclose = () => {
      onDisconnect?.()
      const delay = Math.min(1000 * Math.pow(2, reconnectRef.current), 15000)
      reconnectRef.current++
      setTimeout(connect, delay)
    }

    ws.onerror = () => ws.close()
  }, [onEvent, onConnect, onDisconnect])

  useEffect(() => {
    connect()
    return () => {
      wsRef.current?.close()
    }
  }, [connect])

  return { ws: wsRef }
}
