import { useEffect } from 'react'
import { App } from 'antd'
import { setMessage } from '@/lib/message'

export function useMessage() {
  const { message } = App.useApp()
  useEffect(() => { setMessage(message) }, [message])
  return message
}
