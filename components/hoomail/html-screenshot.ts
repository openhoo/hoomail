import { domToPng } from 'modern-screenshot'

const MAX_SCREENSHOT_DIMENSION = 4096
const MAX_SCREENSHOT_AREA = 16_000_000
const MAX_HTML_BYTES = 2 * 1024 * 1024
const MAX_DOM_NODES = 20_000
const MAX_DOM_DEPTH = 128
const MAX_TEXT_CSS_CHARS = 2 * 1024 * 1024
const MAX_RESOURCE_BYTES = 5 * 1024 * 1024
const MAX_EMBEDDED_RESOURCE_BYTES = 16 * 1024 * 1024

export const IFRAME_CONTAINMENT_STYLES = `
  <style data-hoomail-containment>
    html, body { max-width: 100%; }
    img { max-width: 100%; }
  </style>
`

const REMOVED_ELEMENTS = [
  'audio', 'base', 'embed', 'frame', 'iframe', 'link', 'math', 'meta[http-equiv="refresh"]',
  'object', 'script', 'source', 'template', 'track', 'video',
].join(',')
const URL_ATTRIBUTES = [
  'action', 'background', 'cite', 'data', 'formaction', 'href', 'longdesc', 'ping', 'poster',
  'src', 'srcset', 'xlink:href',
]
const CID_PATH = /^\/api\/attachments\/([1-9]\d*)$/
const ALLOWED_IMAGE_TYPES = new Set([
  'image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/svg+xml',
])

function abortError(): DOMException {
  return new DOMException('The screenshot operation was cancelled.', 'AbortError')
}

function throwIfAborted(signal: AbortSignal): void {
  if (signal.aborted) throw abortError()
}

function decodeCssEscapes(value: string): string {
  return value.replace(/\\([0-9a-f]{1,6})\s?|\\([^\r\n])/gi, (_match, hex: string | undefined, escaped: string | undefined) => (
    hex ? String.fromCodePoint(Number.parseInt(hex, 16)) : (escaped ?? '')
  ))
}

const SAFE_FRAGMENT_URL_PATTERN = /url\((['"]?)#[a-z_][a-z0-9_.:-]*\1\)/gi
function hasCssResource(value: string): boolean {
  const normalized = decodeCssEscapes(value)
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/[\u0000-\u0020]+/g, '')
    .toLowerCase()
  return normalized.replace(SAFE_FRAGMENT_URL_PATTERN, '').includes('url(')
    || normalized.includes('@import')
    || normalized.includes('@font-face')
    || normalized.includes('image-set(')
    || normalized.includes('cross-fade(')
    || normalized.includes('element(')
}

function exactCidURL(value: string): URL | null {
  if (!value || value.startsWith('//') || value.includes('#')) return null
  let parsed: URL
  try {
    parsed = new URL(value, window.location.origin)
  } catch {
    return null
  }
  if (parsed.origin !== window.location.origin || parsed.username || parsed.password) return null
  if (!CID_PATH.test(parsed.pathname) || parsed.search !== '?inline=cid') return null
  return parsed
}

function validateDocumentBudget(documentNode: Document): void {
  let nodes = 0
  let textCssChars = 0
  const stack: Array<{ node: Node; depth: number }> = [{ node: documentNode.documentElement, depth: 1 }]
  while (stack.length > 0) {
    const current = stack.pop()!
    nodes += 1
    if (nodes > MAX_DOM_NODES || current.depth > MAX_DOM_DEPTH) {
      throw new Error('The email document is too complex to export safely.')
    }
    if (current.node.nodeType === Node.TEXT_NODE) textCssChars += current.node.textContent?.length ?? 0
    if (current.node instanceof HTMLElement) {
      textCssChars += current.node.getAttribute('style')?.length ?? 0
      if (current.node.tagName === 'STYLE') textCssChars += current.node.textContent?.length ?? 0
    }
    if (textCssChars > MAX_TEXT_CSS_CHARS) {
      throw new Error('The email text and styles are too large to export safely.')
    }
    for (let child = current.node.lastChild; child; child = child.previousSibling) {
      stack.push({ node: child, depth: current.depth + 1 })
    }
  }
}

function signatureMatches(bytes: Uint8Array, type: string): boolean {
  if (type === 'image/png') return bytes.length >= 8 && [137, 80, 78, 71, 13, 10, 26, 10].every((v, i) => bytes[i] === v)
  if (type === 'image/jpeg') return bytes.length >= 4 && bytes[0] === 0xff && bytes[1] === 0xd8 && bytes.at(-2) === 0xff && bytes.at(-1) === 0xd9
  if (type === 'image/gif') return bytes.length >= 6 && (new TextDecoder('ascii').decode(bytes.subarray(0, 6)) === 'GIF87a' || new TextDecoder('ascii').decode(bytes.subarray(0, 6)) === 'GIF89a')
  if (type === 'image/webp') return bytes.length >= 12 && new TextDecoder('ascii').decode(bytes.subarray(0, 4)) === 'RIFF' && new TextDecoder('ascii').decode(bytes.subarray(8, 12)) === 'WEBP'
  return type === 'image/svg+xml' && isSafeSvg(bytes)
}

function isSafeSvg(bytes: Uint8Array): boolean {
  let source: string
  try {
    source = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return false
  }
  if (/<!DOCTYPE|<!ENTITY/i.test(source)) return false
  const svg = new DOMParser().parseFromString(source, 'image/svg+xml')
  if (svg.querySelector('parsererror') || svg.documentElement.localName !== 'svg') return false
  if (svg.querySelector('script,style,foreignObject,iframe,object,embed,audio,video,animate,animateMotion,animateTransform,set')) return false
  for (const element of svg.querySelectorAll('*')) {
    for (const attribute of [...element.attributes]) {
      const name = attribute.name.toLowerCase()
      const value = attribute.value.trim()
      if (name.startsWith('on') || hasCssResource(value)) return false
      if ((name === 'href' || name === 'xlink:href') && value !== '' && !value.startsWith('#')) return false
    }
  }
  return true
}

async function readCappedResponse(response: Response, signal: AbortSignal): Promise<Uint8Array> {
  const declared = Number(response.headers.get('content-length'))
  if (Number.isFinite(declared) && declared > MAX_RESOURCE_BYTES) throw new Error('An embedded image is too large to export safely.')
  if (!response.body) return new Uint8Array()
  const reader = response.body.getReader()
  const chunks: Uint8Array[] = []
  let size = 0
  try {
    while (true) {
      throwIfAborted(signal)
      const { done, value } = await reader.read()
      if (done) break
      size += value.byteLength
      if (size > MAX_RESOURCE_BYTES) throw new Error('An embedded image is too large to export safely.')
      chunks.push(value)
    }
  } finally {
    reader.releaseLock()
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) {
    bytes.set(chunk, offset)
    offset += chunk.byteLength
  }
  return bytes
}

function bytesToDataURL(bytes: Uint8Array, type: string): string {
  let binary = ''
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000))
  }
  return `data:${type};base64,${btoa(binary)}`
}

function getRootBackgroundColor(root: Element): string | null {
  const computed = root.ownerDocument.defaultView?.getComputedStyle(root).backgroundColor ?? ''
  if (!computed || computed === 'transparent') return null
  const match = /^(?:rgba|hsla)\(([^)]+)\)$/.exec(computed)
  if (match) {
    const components = match[1].split(/[,\s/]+/).filter(Boolean)
    const alpha = components.length >= 4 ? Number.parseFloat(components[components.length - 1]) : 1
    if (alpha === 0) return null
  }
  return computed
}

async function isolatedScreenshotDocument(html: string, signal: AbortSignal): Promise<Document> {
  if (html.length > MAX_HTML_BYTES * 2) {
    throw new Error('The email HTML is too large to export safely.')
  }
  if (new TextEncoder().encode(html).byteLength > MAX_HTML_BYTES) {
    throw new Error('The email HTML is too large to export safely.')
  }
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  validateDocumentBudget(parsed)
  for (const element of parsed.querySelectorAll(REMOVED_ELEMENTS)) element.remove()
  for (const style of parsed.querySelectorAll('style')) {
    if (hasCssResource(style.textContent ?? '')) style.remove()
  }

  const cidImages: Array<{ image: HTMLImageElement; url: URL }> = []
  const embeddedImages: Array<{ image: HTMLImageElement; type: string; bytes: Uint8Array }> = []
  for (const element of parsed.querySelectorAll<HTMLElement>('*')) {
    for (const attribute of [...element.attributes]) {
      if (attribute.name.toLowerCase().startsWith('on')) element.removeAttribute(attribute.name)
    }
    const inlineStyle = element.getAttribute('style')
    if (inlineStyle && hasCssResource(inlineStyle)) element.removeAttribute('style')
    const src = element instanceof HTMLImageElement ? element.getAttribute('src') : null
    const cid = src ? exactCidURL(src) : null
    if (element instanceof HTMLAnchorElement && element.hasAttribute('href')) element.setAttribute('href', '#')
    for (const attribute of URL_ATTRIBUTES) {
      if (attribute === 'href' && element instanceof HTMLAnchorElement) continue
      element.removeAttribute(attribute)
    }
    if (!(element instanceof HTMLImageElement)) continue
    if (cid) {
      cidImages.push({ image: element, url: cid })
      continue
    }
    const dataMatch = src?.match(/^data:(image\/(?:png|jpe?g|gif|webp|svg\+xml));base64,([A-Za-z0-9+/=]+)$/i)
    if (!dataMatch) {
      element.remove()
      continue
    }
    try {
      const binary = atob(dataMatch[2])
      const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0))
      const type = dataMatch[1].toLowerCase()
      if (bytes.length > MAX_RESOURCE_BYTES || !signatureMatches(bytes, type)) {
        element.remove()
        continue
      }
      embeddedImages.push({ image: element, type, bytes })
    } catch {
      element.remove()
    }
  }

  let aggregateBytes = 0
  for (const { image, type, bytes } of embeddedImages) {
    aggregateBytes += bytes.byteLength
    if (aggregateBytes > MAX_EMBEDDED_RESOURCE_BYTES) throw new Error('The email contains too many embedded image bytes to export safely.')
    image.src = bytesToDataURL(bytes, type)
  }

  const cidResources = new Map<string, { bytes: Uint8Array; dataUrl: string }>()
  for (const { image, url } of cidImages) {
    throwIfAborted(signal)
    let resource = cidResources.get(url.href)
    if (!resource) {
      const response = await fetch(url, { credentials: 'same-origin', redirect: 'error', signal })
      if (!response.ok) throw new Error('An embedded image could not be loaded for export.')
      const type = response.headers.get('content-type')?.split(';', 1)[0].trim().toLowerCase() ?? ''
      if (!ALLOWED_IMAGE_TYPES.has(type)) throw new Error('An embedded image has an unsupported media type.')
      const bytes = await readCappedResponse(response, signal)
      if (!signatureMatches(bytes, type)) throw new Error('An embedded image does not match its declared media type.')
      resource = { bytes, dataUrl: bytesToDataURL(bytes, type) }
      cidResources.set(url.href, resource)
    }
    aggregateBytes += resource.bytes.byteLength
    if (aggregateBytes > MAX_EMBEDDED_RESOURCE_BYTES) throw new Error('The email contains too many embedded image bytes to export safely.')
    image.src = resource.dataUrl
  }

  parsed.head.insertAdjacentHTML('afterbegin', '<meta http-equiv="Content-Security-Policy" content="default-src &apos;none&apos;; img-src data:; style-src &apos;unsafe-inline&apos;; script-src &apos;none&apos;; object-src &apos;none&apos;; frame-src &apos;none&apos;; connect-src &apos;none&apos;; font-src &apos;none&apos;; media-src &apos;none&apos;">')
  parsed.head.insertAdjacentHTML('afterbegin', IFRAME_CONTAINMENT_STYLES)
  return parsed
}



export async function createHtmlScreenshot(
  sanitizedHtml: string,
  width: number,
  height: number,
  signal: AbortSignal,
): Promise<Blob> {
  const rasterWidth = Math.round(width)
  const rasterHeight = Math.round(height)
  if (!Number.isFinite(rasterWidth) || !Number.isFinite(rasterHeight)
    || rasterWidth < 1 || rasterHeight < 1
    || rasterWidth > MAX_SCREENSHOT_DIMENSION || rasterHeight > MAX_SCREENSHOT_DIMENSION
    || rasterWidth * rasterHeight > MAX_SCREENSHOT_AREA) {
    throw new Error('The email preview is too large to export safely.')
  }
  throwIfAborted(signal)
  const rasterDocument = await isolatedScreenshotDocument(sanitizedHtml, signal)
  throwIfAborted(signal)
  const iframe = document.createElement('iframe')
  iframe.referrerPolicy = 'no-referrer'
  iframe.setAttribute('aria-hidden', 'true')
  iframe.tabIndex = -1
  iframe.sandbox.value = 'allow-same-origin'
  iframe.style.cssText = `position:fixed;left:-100000px;top:0;border:0;width:${rasterWidth}px;height:${rasterHeight}px`
  const doctype = rasterDocument.doctype ? `<!DOCTYPE ${rasterDocument.doctype.name}>` : '<!DOCTYPE html>'
  iframe.srcdoc = `${doctype}${rasterDocument.documentElement.outerHTML}`
  document.body.append(iframe)
  try {
    await new Promise<void>((resolve, reject) => {
      const onAbort = () => reject(abortError())
      signal.addEventListener('abort', onAbort, { once: true })
      iframe.addEventListener('load', () => {
        signal.removeEventListener('abort', onAbort)
        resolve()
      }, { once: true })
    })
    throwIfAborted(signal)
    const root = iframe.contentDocument?.documentElement
    if (!root) throw new Error('The browser could not prepare the email document for export.')
    await Promise.all([...iframe.contentDocument!.images].map((image) => image.decode().catch(() => undefined)))
    throwIfAborted(signal)
    const rootBackgroundColor = getRootBackgroundColor(root)
    const dataURL = await domToPng(root, {
      width: rasterWidth,
      height: rasterHeight,
      scale: 1,
      backgroundColor: '#fff',
      onCloneNode: (clone) => {
        if (rootBackgroundColor && clone.nodeType === Node.ELEMENT_NODE && clone.nodeName.toLowerCase() === 'html') {
          (clone as HTMLElement).style.setProperty('background-color', rootBackgroundColor, 'important')
        }
      },
    })
    throwIfAborted(signal)
    const blob = await (await fetch(dataURL, { signal })).blob()
    if (blob.type !== 'image/png' || blob.size === 0) throw new Error('The browser could not create a PNG screenshot.')
    return blob
  } finally {
    iframe.remove()
  }
}

export function downloadScreenshotBlob(png: Blob, messageId: number): void {
  const pngUrl = URL.createObjectURL(png)
  const anchor = document.createElement('a')
  anchor.href = pngUrl
  anchor.download = `message-${messageId}.png`
  anchor.hidden = true
  document.body.append(anchor)
  try {
    anchor.click()
  } finally {
    anchor.remove()
    URL.revokeObjectURL(pngUrl)
  }
}
