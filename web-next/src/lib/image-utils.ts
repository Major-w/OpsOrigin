// 图片工具: 本地图片 → 压缩后的 data URL(Base64), 用于知识库/工单 Markdown 内嵌。
// 选 Base64 内嵌是为了零后端改动(现有 /api/v1/files/upload 是 agent 专用、下载需签名 token, 不适合 <img> 直接用)。

function supportsWebp(): boolean {
  const c = document.createElement('canvas')
  return c.toDataURL('image/webp').startsWith('data:image/webp')
}

/**
 * 读本地图片并压缩为 data URL。
 * - 等比缩到 maxWidth(默认 1280px)以内, 控制文章体积(DB 存正文, 过大 base64 会膨胀)。
 * - 有透明通道的原图(png)保 png, 其余尽量 webp, 再退 jpeg。
 */
export function fileToCompressedDataUrl(
  file: File,
  opts?: { maxWidth?: number; quality?: number },
): Promise<string> {
  const { maxWidth = 1280, quality = 0.82 } = opts || {}
  return new Promise((resolve, reject) => {
    if (!file.type.startsWith('image/')) {
      reject(new Error('请选择图片文件'))
      return
    }
    const reader = new FileReader()
    reader.onerror = () => reject(new Error('读取文件失败'))
    reader.onload = () => {
      const img = new Image()
      img.onerror = () => reject(new Error('图片解析失败, 可能不是有效图片'))
      img.onload = () => {
        const scale = Math.min(1, maxWidth / img.width)
        const w = Math.max(1, Math.round(img.width * scale))
        const h = Math.max(1, Math.round(img.height * scale))
        const canvas = document.createElement('canvas')
        canvas.width = w
        canvas.height = h
        const ctx = canvas.getContext('2d')
        if (!ctx) {
          reject(new Error('Canvas 不可用'))
          return
        }
        ctx.drawImage(img, 0, 0, w, h)
        let mime = 'image/jpeg'
        if (file.type === 'image/png' || !supportsWebp()) {
          mime = file.type === 'image/png' ? 'image/png' : 'image/jpeg'
        } else {
          mime = 'image/webp'
        }
        resolve(canvas.toDataURL(mime, quality))
      }
      img.src = String(reader.result)
    }
    reader.readAsDataURL(file)
  })
}
