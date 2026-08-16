'use client'

import { useState, useEffect, useRef } from 'react'
import { Button } from '@/components/ui/button'
import { ArrowUp } from 'lucide-react'
import { cn } from '@/lib/utils'

interface ScrollToTopProps {
  target?: string | HTMLElement
  threshold?: number
  className?: string
}

export function ScrollToTop({ target, threshold = 300, className }: ScrollToTopProps) {
  const [visible, setVisible] = useState(false)
  // 记录真正滚动的元素(main 或内部 overflow 容器), 点击时滚它
  const scrollerRef = useRef<HTMLElement | Window | null>(null)

  useEffect(() => {
    const resolveTarget = (): HTMLElement | null => {
      if (!target) return document.querySelector('[data-scroll-container]') as HTMLElement | null
      return typeof target === 'string'
        ? (document.querySelector(target) as HTMLElement | null)
        : target
    }
    const explicit = resolveTarget()

    const update = (el: HTMLElement | null, top: number) => {
      setVisible(top > threshold)
      if (top > 0) scrollerRef.current = el ?? window
    }

    const handleScroll = (e?: Event) => {
      // 显式 target: 只认它
      if (explicit) {
        update(explicit, explicit.scrollTop)
        return
      }
      // 捕获阶段: e.target 是真正触发滚动的元素(<main> 或内部 overflow 容器如设置页 TabsContent)
      const el = e?.target ?? null
      const top =
        el instanceof HTMLElement && typeof el.scrollTop === 'number'
          ? el.scrollTop
          : window.scrollY || document.documentElement.scrollTop || 0
      update(el instanceof HTMLElement ? el : null, top)
    }

    // capture=true: scroll 不冒泡, 但捕获阶段能在 window 层感知任意嵌套滚动容器
    window.addEventListener('scroll', handleScroll, true)
    handleScroll()
    return () => window.removeEventListener('scroll', handleScroll, true)
  }, [target, threshold])

  const scrollToTop = () => {
    const s = scrollerRef.current
    if (s === window) {
      window.scrollTo({ top: 0, behavior: 'smooth' })
    } else if (s) {
      ;(s as HTMLElement).scrollTo({ top: 0, behavior: 'smooth' })
    } else {
      const main = document.querySelector('[data-scroll-container]') as HTMLElement | null
      if (main) main.scrollTo({ top: 0, behavior: 'smooth' })
      else window.scrollTo({ top: 0, behavior: 'smooth' })
    }
  }

  if (!visible) return null

  return (
    <div className={cn("fixed bottom-6 right-6 z-50", className)}>
      <Button
        size="icon"
        className="h-10 w-10 rounded-full shadow-lg bg-emerald-500 hover:bg-emerald-600 text-white"
        onClick={scrollToTop}
      >
        <ArrowUp className="h-4 w-4" />
      </Button>
    </div>
  )
}

export default ScrollToTop
