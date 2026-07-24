import type { ComponentChildren } from 'preact'
import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { Download, FileText, Paperclip } from '@/components/ui/icons'
import { asyncComponent } from '@/components/ui/async-component'
import { Badge } from '@/components/ui/badge'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { InviteCard } from './invite-card'
import {
  formatBytes,
  type AttachmentMeta,
  type FullMessage,
  useCachedResource,
} from './use-hoomail'

const InspectPanel = asyncComponent(
  () => import('./inspect-panel').then(({ InspectPanel }) => InspectPanel),
  <div role="status" aria-live="polite" className="flex h-full items-center justify-center">
    <p className="text-sm text-muted-foreground">Analyzing message…</p>
  </div>,
)

const IFRAME_CONTAINMENT_STYLES = `
  <style>
    html, body { max-width: 100%; }
    img { max-width: 100%; }
  </style>
`

function iframeDocumentPrefix(): string {
  const attachmentSource = new URL('/api/attachments/', window.location.href).href
  const policy = [
    "default-src 'none'",
    `img-src ${attachmentSource}`,
    "style-src 'unsafe-inline'",
    "script-src 'none'",
    "object-src 'none'",
    "frame-src 'none'",
    "child-src 'none'",
    "form-action 'none'",
    "connect-src 'none'",
    "media-src 'none'",
    "font-src 'none'",
    "base-uri 'none'",
  ].join('; ')
  return `<meta http-equiv="Content-Security-Policy" content="${policy}"><meta name="referrer" content="no-referrer">${IFRAME_CONTAINMENT_STYLES}`
}

export function MessageViewer({
  message,
  attachments,
  selectedMessageId,
  isLoading,
}: {
  message: FullMessage | null
  attachments: AttachmentMeta[]
  selectedMessageId: number | null
  isLoading: boolean
}) {
  const htmlDoc = useMemo(() => {
    if (!message?.html) return null
    const prefix = iframeDocumentPrefix()
    const hasHead = /<head[\s>]/i.test(message.html)
    if (hasHead) {
      return message.html.replace(/<head([^>]*)>/i, `<head$1>${prefix}`)
    }
    return `<!DOCTYPE html><html><head>${prefix}</head><body>${message.html}</body></html>`
  }, [message?.html])

  // Choose the new message's default tab during the same render. Updating the
  // controlled state only after render briefly exposes the previous `text`
  // tab on the first HTML email, which produces a visible one-frame flash.
  const defaultTab = htmlDoc ? 'html' : message?.text ? 'text' : 'source'
  const [tab, setTab] = useState(defaultTab)
  const [tabMessageId, setTabMessageId] = useState(message?.id ?? null)
  const messageChanged = message != null && message.id !== tabMessageId
  const activeTab = messageChanged ? defaultTab : tab
  const [readyMessageId, setReadyMessageId] = useState<number | null>(null)
  const selectedDetailPending = selectedMessageId != null && message?.id !== selectedMessageId
  const detailReady = !selectedDetailPending && (!htmlDoc || readyMessageId === message?.id)
  const markHtmlReady = useCallback(() => {
    if (message) setReadyMessageId(message.id)
  }, [message?.id])

  useEffect(() => {
    if (!messageChanged || !message) return
    setTabMessageId(message.id)
    setTab(defaultTab)
    setReadyMessageId(null)
  }, [defaultTab, message, messageChanged])

  if (!message) {
    return (
      <section aria-label="Message viewer" aria-live="polite" className="flex min-w-0 flex-1 flex-col items-center justify-center gap-4 bg-background">
        {isLoading || selectedMessageId != null ? (
          <p role="status" className="text-sm text-muted-foreground">Loading message…</p>
        ) : (
          <>
            {/* Static local asset. */}
            <img
              src="/hoomail-logo.png"
              alt=""
              width={56}
              height={56}
              className="opacity-30"
              aria-hidden="true"
            />
            <p className="max-w-xs text-center text-sm leading-relaxed text-muted-foreground text-pretty">
              Select a message to read it here.
            </p>
          </>
        )}
      </section>
    )
  }

  const rawSource = Object.values(message.headers).join('\n')

  return (
    <section
      aria-label="Message viewer"
      aria-busy={!detailReady || undefined}
      className="relative flex min-w-0 flex-1 flex-col bg-background"
    >
      <span role="status" aria-live="polite" className="sr-only">
        {detailReady ? `Message loaded: ${message.subject || 'no subject'}` : 'Loading message'}
      </span>
      <header className="shrink-0 border-b border-border px-5 py-4">
        <h1 className="text-lg font-semibold leading-snug text-balance">
          {message.subject || '(no subject)'}
        </h1>
        <dl className="mt-2 flex flex-col gap-0.5 text-[13px]">
          <HeaderRow label="From">
            <span className="font-medium">{message.fromName || message.fromAddress}</span>
            {message.fromName && message.fromAddress && (
              <span className="ml-1.5 font-mono text-xs text-muted-foreground">
                {'<'}{message.fromAddress}{'>'}
              </span>
            )}
          </HeaderRow>
          <HeaderRow label="To">
            <span className="font-mono text-xs">
              {message.to.map((t) => t.address).join(', ') || '—'}
            </span>
          </HeaderRow>
          {message.cc.length > 0 && (
            <HeaderRow label="Cc">
              <span className="font-mono text-xs">{message.cc.map((c) => c.address).join(', ')}</span>
            </HeaderRow>
          )}
          <HeaderRow label="Date">
            <span className="text-muted-foreground">
              {new Date(message.receivedAt).toLocaleString()}
            </span>
            <span className="ml-2 font-mono text-[11px] text-muted-foreground">
              {formatBytes(message.size)}
            </span>
          </HeaderRow>
        </dl>

        {message.icalEvents.length > 0 && (
          <div className="mt-3">
            <InviteCard events={message.icalEvents} />
          </div>
        )}

        {attachments.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {attachments.map((att) => (
              <AttachmentChip key={att.id} attachment={att} />
            ))}
          </div>
        )}
      </header>

      <Tabs
        value={activeTab}
        onValueChange={setTab}
        className="flex min-h-0 flex-1 flex-col gap-0"
      >
        <div className="shrink-0 border-b border-border px-5 py-2">
          <TabsList className="h-8">
            <TabsTrigger value="html" disabled={!htmlDoc} className="text-xs">
              HTML
            </TabsTrigger>
            <TabsTrigger value="text" disabled={!message.text} className="text-xs">
              Plain text
            </TabsTrigger>
            <TabsTrigger value="source" className="text-xs">
              Source
            </TabsTrigger>
            <TabsTrigger value="inspect" className="text-xs">
              Inspect
            </TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="html" className="min-h-0 flex-1 bg-white data-[state=inactive]:hidden">
          {htmlDoc && <HtmlFrame doc={htmlDoc} onReady={markHtmlReady} />}
        </TabsContent>

        <TabsContent value="text" className="min-h-0 flex-1 data-[state=inactive]:hidden">
          <ScrollArea className="h-full" aria-label="Plain text message">
            <pre className="whitespace-pre-wrap px-5 py-4 font-mono text-[13px] leading-relaxed">
              {message.text || 'No plain text part.'}
            </pre>
          </ScrollArea>
        </TabsContent>

        <TabsContent value="source" className="min-h-0 flex-1 data-[state=inactive]:hidden">
          <ScrollArea className="h-full" aria-label="Raw message source">
            <div className="px-5 py-4">
              <div className="mb-2 flex items-center gap-2">
                <FileText className="size-3.5 text-muted-foreground" aria-hidden="true" />
                <Badge variant="secondary" className="font-mono text-[10px]">
                  raw headers
                </Badge>
              </div>
              <pre className="whitespace-pre-wrap break-all font-mono text-xs leading-relaxed text-muted-foreground">
                {rawSource || 'No headers captured.'}
              </pre>
            </div>
          </ScrollArea>
        </TabsContent>

        <TabsContent value="inspect" className="min-h-0 flex-1 data-[state=inactive]:hidden">
          <InspectPanel messageId={message.id} active={activeTab === 'inspect'} />
        </TabsContent>
      </Tabs>
    </section>
  )
}

/**
 * Double-buffered email frame. The incoming iframe remains transparent until
 * its document has loaded (or the bounded grace period expires), preventing
 * partially parsed HTML from becoming visible during its first paint.
 */
function HtmlFrame({ doc, onReady }: { doc: string; onReady: () => void }) {
  const [prevDoc, setPrevDoc] = useState<string | null>(null)
  const [visibleDoc, setVisibleDoc] = useState<string | null>(null)
  const lastDocRef = useRef(doc)

  if (doc !== lastDocRef.current) {
    setPrevDoc(visibleDoc === lastDocRef.current ? lastDocRef.current : null)
    setVisibleDoc(null)
    lastDocRef.current = doc
  }

  // iframe load waits for subresources. Bound the hidden period so unreachable
  // tracking pixels cannot leave an otherwise rendered email invisible.
  useEffect(() => {
    const timer = setTimeout(() => {
      setVisibleDoc(doc)
      setPrevDoc(null)
      onReady()
    }, 150)
    return () => clearTimeout(timer)
  }, [doc, onReady])

  const reveal = () => {
    setVisibleDoc(doc)
    setPrevDoc(null)
    onReady()
  }

  return (
    <div className="relative size-full bg-white">
      <iframe
        key={doc}
        title="Email HTML content"
        srcDoc={doc}
        sandbox=""
        referrerPolicy="no-referrer"
        tabIndex={visibleDoc === doc ? 0 : -1}
        aria-hidden={visibleDoc === doc ? undefined : "true"}
        onLoad={reveal}
        className={`absolute inset-0 size-full border-0 ${visibleDoc === doc ? 'opacity-100' : 'opacity-0'}`}
      />
      {prevDoc !== null && (
        <iframe
          key={prevDoc}
          title="Previous email content"
          srcDoc={prevDoc}
          sandbox=""
          referrerPolicy="no-referrer"
          tabIndex={-1}
          className="absolute inset-0 size-full border-0"
        />
      )}
    </div>
  )
}

function isPreviewable(contentType: string | null): 'image' | 'text' | null {
  if (!contentType) return null
  const normalized = contentType.split(';', 1)[0].trim().toLowerCase()
  if (['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(normalized)) return 'image'
  if (normalized === 'text/plain' || normalized === 'text/csv') return 'text'
  return null
}

function AttachmentChip({ attachment }: { attachment: AttachmentMeta }) {
  const [open, setOpen] = useState(false)
  const previewKind = isPreviewable(attachment.contentType)
  const name = attachment.filename || `attachment-${attachment.id}`
  const url = `/api/attachments/${attachment.id}`

  const chipIcon =
    previewKind === 'image' ? (
      <img
        src={url || "/placeholder.svg"}
        alt=""
        aria-hidden="true"
        className="size-5 shrink-0 rounded-sm border border-border bg-checkerboard object-cover"
        loading="lazy"
      />
    ) : (
      <Paperclip className="size-3" aria-hidden="true" />
    )

  const chipBody = (
    <>
      {chipIcon}
      <span className="max-w-48 truncate">{name}</span>
      <span className="text-muted-foreground">{formatBytes(attachment.size)}</span>
    </>
  )

  const chip = (
    <span className="inline-flex items-center overflow-hidden rounded-md border border-border bg-secondary text-xs text-secondary-foreground">
      {previewKind ? (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className="inline-flex items-center gap-1.5 px-2 py-1 transition-colors hover:bg-accent"
          aria-label={`Preview ${name}`}
        >
          {chipBody}
        </button>
      ) : (
        <span className="inline-flex items-center gap-1.5 px-2.5 py-1">{chipBody}</span>
      )}
      <a
        href={`${url}?download=1`}
        download={attachment.filename || undefined}
        className="self-stretch border-l border-border px-1.5 py-1 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        aria-label={`Download ${name}`}
      >
        <Download className="size-3 h-full" aria-hidden="true" />
      </a>
    </span>
  )

  if (!previewKind) return chip

  return (
    <>
      {chip}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="truncate pr-6 font-mono text-sm">
              {name}
              <span className="ml-2 font-sans text-xs text-muted-foreground">
                {attachment.contentType} · {formatBytes(attachment.size)}
              </span>
            </DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-auto rounded-md border border-border bg-card">
            {previewKind === 'image' && (
              <div className="flex min-h-48 items-center justify-center bg-checkerboard p-4">
                {/* Served from our own API; dimensions are unknown. */}
                <img src={url || "/placeholder.svg"} alt={name} className="max-h-[62vh] max-w-full object-contain" />
              </div>
            )}
            {previewKind === 'text' && <TextPreview url={url} active={open} />}
          </div>
        </DialogContent>
      </Dialog>
    </>
  )
}

const textFetcher = (url: string) =>
  fetch(url).then((r) => {
    if (!r.ok) throw new Error('failed')
    return r.text()
  })

function TextPreview({ url, active }: { url: string; active: boolean }) {
  const { data, error } = useCachedResource<string>(active ? url : null, textFetcher)
  return (
    <pre className="whitespace-pre-wrap p-4 font-mono text-xs leading-relaxed">
      {error ? 'Could not load preview.' : (data?.slice(0, 100_000) ?? 'Loading…')}
    </pre>
  )
}

function HeaderRow({ label, children }: { label: string; children: ComponentChildren }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="w-10 shrink-0 text-xs text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 truncate">{children}</dd>
    </div>
  )
}
