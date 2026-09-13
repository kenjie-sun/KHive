export interface EsimActivationCode {
  smdp: string
  matchingId: string
  confirmationRequired: boolean
}

export function parseEsimActivationCode(text: string): EsimActivationCode {
  const parts = text.trim().split('$')
  if (parts[0] !== 'LPA:1' || parts.length < 3 || parts.length > 5) {
    throw new Error('这不是有效的 eSIM 激活二维码，请选择运营商提供的二维码照片')
  }
  const smdp = parts[1] || ''
  const matchingId = parts[2] || ''
  if (!/^[a-z\d](?:[a-z\d.-]*[a-z\d])?(?::\d{1,5})?$/i.test(smdp) || /\s/.test(matchingId)) {
    throw new Error('二维码中的 eSIM 激活信息不完整或格式不正确')
  }
  try {
    const url = new URL(`https://${smdp}`)
    if (url.host.toLowerCase() !== smdp.toLowerCase()) throw new Error()
  } catch {
    throw new Error('二维码中的 SM-DP+ 地址格式不正确')
  }
  return { smdp, matchingId, confirmationRequired: parts[4] === '1' }
}

export function validateQrPhoto(file: Pick<File, 'size' | 'type'>) {
  if (!['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'image/bmp'].includes(file.type)) {
    throw new Error('请选择 JPG、PNG 或 WebP 图片；HEIC 照片请先转换为 JPG')
  }
  if (file.size === 0 || file.size > 10 * 1024 * 1024) {
    throw new Error('请选择大小不超过 10 MB 的有效图片')
  }
}

// The image and activation code stay in this browser. Only the existing
// download action submits the parsed activation fields to KHive.
export async function readEsimQrPhoto(file: File, signal: AbortSignal): Promise<EsimActivationCode> {
  validateQrPhoto(file)
  signal.throwIfAborted()
  const url = URL.createObjectURL(file)
  const img = new Image()
  try {
    await new Promise<void>((resolve, reject) => {
      const cleanup = () => {
        img.onload = null
        img.onerror = null
        signal.removeEventListener('abort', aborted)
      }
      const aborted = () => { cleanup(); img.src = ''; reject(new DOMException('Aborted', 'AbortError')) }
      img.onload = () => { cleanup(); resolve() }
      img.onerror = () => { cleanup(); reject(new Error('无法读取这张图片，请换一张清晰的 JPG 或 PNG 照片')) }
      signal.addEventListener('abort', aborted, { once: true })
      img.src = url
    })
    signal.throwIfAborted()
    if (!img.naturalWidth || !img.naturalHeight || img.naturalWidth * img.naturalHeight > 60_000_000) {
      throw new Error('图片尺寸过大，请裁剪至二维码区域后重试')
    }
    const { default: jsQR } = await import('jsqr')
    const canvas = document.createElement('canvas')
    try {
      const ctx = canvas.getContext('2d', { willReadFrequently: true })
      if (!ctx) throw new Error('浏览器无法读取图片，请更新浏览器后重试')
      const edge = Math.max(img.naturalWidth, img.naturalHeight)
      const scales = [...new Set([Math.min(1, 1200 / edge), Math.min(1, 2400 / edge)])]
      for (const scale of scales) {
        await new Promise<void>(resolve => window.setTimeout(resolve, 0))
        signal.throwIfAborted()
        canvas.width = Math.max(1, Math.round(img.naturalWidth * scale))
        canvas.height = Math.max(1, Math.round(img.naturalHeight * scale))
        ctx.fillStyle = '#fff'
        ctx.fillRect(0, 0, canvas.width, canvas.height)
        ctx.drawImage(img, 0, 0, canvas.width, canvas.height)
        const pixels = ctx.getImageData(0, 0, canvas.width, canvas.height)
        const code = jsQR(pixels.data, pixels.width, pixels.height, { inversionAttempts: 'attemptBoth' })
        signal.throwIfAborted()
        if (code) return parseEsimActivationCode(code.data)
      }
      throw new Error('未识别到二维码，请裁剪至单个二维码，保留四周白边后重试')
    } finally {
      canvas.width = 0
      canvas.height = 0
    }
  } finally {
    img.src = ''
    URL.revokeObjectURL(url)
  }
}
