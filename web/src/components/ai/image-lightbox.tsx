
// ============================================================================
// AI 图片页内预览(Lightbox) —— 附件缩略图与 Markdown 内嵌图片共用
// 用法: 任意处调用 openLightbox(src, alt) 弹出全屏遮罩; <LightboxHost/> 挂载一次。
// 通过 window CustomEvent 解耦, 避免层层传 ref/context。
// ============================================================================

import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { X } from 'lucide-react'

const LIGHTBOX_EVENT = 'opsorigin:ai-lightbox'

export function openLightbox(src: string, alt?: string) {
  if (!src) return
  window.dispatchEvent(new CustomEvent(LIGHTBOX_EVENT, { detail: { src, alt } }))
}

/** 绑定容器内 Markdown 图片点击 → 页内预览(跳过链接内图片, 不破坏原有 <a> 行为)。
 *  返回解绑函数, 供 effect cleanup。 */
export function bindImageLightbox(root: HTMLElement) {
  const onClick = (e: MouseEvent) => {
    const img = (e.target as HTMLElement | null)?.closest?.('img')
    if (!img) return
    if (img.closest('a, button')) return
    e.preventDefault()
    openLightbox(img.src, img.alt)
  }
  root.addEventListener('click', onClick)
  return () => root.removeEventListener('click', onClick)
}

export function LightboxHost() {
  const [state, setState] = useState<{ src: string; alt?: string } | null>(null)

  useEffect(() => {
    const open = (e: Event) => setState((e as CustomEvent).detail)
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setState(null) }
    window.addEventListener(LIGHTBOX_EVENT, open)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener(LIGHTBOX_EVENT, open)
      window.removeEventListener('keydown', onKey)
    }
  }, [])

  // 预览时锁定背景滚动
  useEffect(() => {
    if (!state) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [state])

  if (!state) return null
  return createPortal(
    <div
      className="fixed inset-0 z-[999] bg-black/80 flex items-center justify-center p-6 cursor-zoom-out"
      onClick={() => setState(null)}
      title="点击空白处关闭"
    >
      <img
        src={state.src}
        alt={state.alt || ''}
        className="max-w-full max-h-full rounded-lg shadow-2xl cursor-zoom-in select-none"
        onClick={e => e.stopPropagation()}
      />
      <div className="absolute top-4 right-4 flex items-center gap-1.5" onClick={e => e.stopPropagation()}>
        <a
          href={state.src}
          target="_blank"
          rel="noreferrer"
          className="px-2.5 py-1.5 rounded-md text-xs bg-white/10 text-white/90 hover:bg-white/20 transition-colors"
          title="在新窗口打开原图"
        >新窗口打开</a>
        <button
          type="button"
          onClick={() => setState(null)}
          className="p-1.5 rounded-md bg-white/10 text-white/90 hover:bg-white/20 transition-colors"
          title="关闭 (Esc)"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      {state.alt && (
        <div className="absolute bottom-4 left-1/2 -translate-x-1/2 max-w-[70%] truncate px-3 py-1 rounded-full bg-black/50 text-xs text-white/80 select-none">
          {state.alt}
        </div>
      )}
    </div>,
    document.body,
  )
}
