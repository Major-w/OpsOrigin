'use client'

import { useState, useEffect } from 'react'
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

  useEffect(() => {
    const getTarget = () => {
      if (target) {
        if (typeof target === 'string') return document.querySelector(target) as HTMLElement
        return target
      }
      return document.querySelector('[data-scroll-container]') || document.documentElement
    }

    const scrollTarget = getTarget()
    if (!scrollTarget) return

    const handleScroll = () => {
      const scrollTop = scrollTarget === document.documentElement
        ? window.scrollY || document.documentElement.scrollTop
        : scrollTarget.scrollTop
      setVisible(scrollTop > threshold)
    }

    scrollTarget.addEventListener('scroll', handleScroll, { passive: true })
    handleScroll()

    return () => scrollTarget.removeEventListener('scroll', handleScroll)
  }, [target, threshold])

  const scrollToTop = () => {
    const getTarget = () => {
      if (target) {
        if (typeof target === 'string') return document.querySelector(target) as HTMLElement
        return target
      }
      return document.querySelector('[data-scroll-container]') || document.documentElement
    }

    const scrollTarget = getTarget()
    if (!scrollTarget) return

    if (scrollTarget === document.documentElement) {
      window.scrollTo({ top: 0, behavior: 'smooth' })
    } else {
      scrollTarget.scrollTo({ top: 0, behavior: 'smooth' })
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
