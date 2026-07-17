'use client'

import { create } from 'zustand'

type Theme = 'light' | 'dark'

interface ThemeState {
  theme: Theme
  toggle: () => void
  set: (t: Theme) => void
}

const isClient = typeof window !== 'undefined'
const initialTheme: Theme = isClient
  ? (localStorage.getItem('theme') as Theme) || 'dark'
  : 'dark'

export const useThemeStore = create<ThemeState>((set) => ({
  theme: initialTheme,
  toggle: () => set(s => {
    const next = s.theme === 'dark' ? 'light' : 'dark'
    if (isClient) {
      localStorage.setItem('theme', next)
      document.documentElement.classList.toggle('dark', next === 'dark')
    }
    return { theme: next }
  }),
  set: (t) => {
    if (isClient) {
      localStorage.setItem('theme', t)
      document.documentElement.classList.toggle('dark', t === 'dark')
    }
    set({ theme: t })
  },
}))

// Init class on load (client-only)
if (isClient) {
  const saved = localStorage.getItem('theme') as Theme
  if (saved) document.documentElement.classList.toggle('dark', saved === 'dark')
}
