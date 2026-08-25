import { useCallback, useEffect, useRef, useState } from 'preact/hooks'

export interface Mailbox { id: number; address: string; created_at: number; last_message_at: number | null; total_count: number; unread_count: number }
export interface MessageListItem { id: number; from_address: string | null; from_name: string | null; subject: string | null; snippet: string; is_read: number; received_at: number; has_ical: number; attachment_count: number }
export interface ParsedAttendee { address: string; name?: string; partstat?: string; role?: string }
export interface IcalEvent { method: string; uid: string; sequence: number; summary: string | null; description: string | null; location: string | null; status: string | null; organizerAddress: string | null; organizerName: string | null; attendees: ParsedAttendee[]; dtstart: number; dtend: number | null; allDay: boolean }
export interface CalendarEvent { id: number; uid: string; sequence: number; summary: string | null; description: string | null; location: string | null; status: string; organizerAddress: string | null; organizerName: string | null; attendees: ParsedAttendee[]; dtstart: number; dtend: number | null; allDay: boolean; lastMessageId: number | null; updatedAt: number }
export interface AddressEntry { address: string; name?: string }
export interface FullMessage { id: number; mailboxId: number; fromAddress: string | null; fromName: string | null; to: AddressEntry[]; cc: AddressEntry[]; subject: string | null; html: string | null; text: string | null; headers: Record<string, string>; size: number; receivedAt: number; icalEvents: IcalEvent[] }
export interface AttachmentMeta { id: number; filename: string | null; contentType: string | null; size: number }
export type InspectionState = 'complete' | 'partial'
export type InspectionCategory = 'analysis' | 'message' | 'mime' | 'authentication' | 'unsubscribe' | 'content' | 'privacy' | 'compatibility'
export type InspectionOutcome = 'pass' | 'fail' | 'observed' | 'not-evaluated'
export type InspectionSeverity = 'error' | 'warning' | 'advisory' | 'none'
export type InspectionBasis = 'standard' | 'recommendation' | 'heuristic' | 'evidence'
export type InspectionApplicability = 'all' | 'html' | 'mailing-list' | 'one-click-claim' | 'bulk-marketing' | 'unknown'
export type InspectionEvidenceSource = 'raw-header' | 'raw-line' | 'mime-part' | 'html' | 'text'
export type InspectionResourceKind = 'link' | 'image' | 'tracking-pixel' | 'cid' | 'data' | 'attachment'

export interface InspectionAnalysis {
  version: number
  state: InspectionState | string
  parsedThroughPath: string | null
  unavailableRuleFamilies: string[]
  truncated: boolean
}

export interface InspectionSummary {
  fail: number
  warning: number
  advisory: number
  observed: number
  pass: number
  notEvaluated: number
}

export interface InspectionReference { label: string; url: string }

export interface InspectionEvidence {
  source: InspectionEvidenceSource | string
  path?: string
  field?: string
  occurrence?: number
  line?: number
  value?: string
}

export interface InspectionFinding {
  id: string
  category: InspectionCategory | string
  outcome: InspectionOutcome | string
  severity: InspectionSeverity | string
  basis: InspectionBasis | string
  applicability: InspectionApplicability | string
  label: string
  detail: string
  evidence: InspectionEvidence[]
  evidenceTruncated: boolean
  reference: InspectionReference | null
}

export interface InspectionResource {
  kind: InspectionResourceKind | string
  path: string | null
  url: string
  text: string
  occurrenceCount: number
}

export interface MimeNodeChecksums {
  md5: string
  sha1: string
  sha256: string
}

export interface MimeNode {
  path: string
  contentType: string
  charset: string | null
  encoding: string | null
  disposition: string | null
  filename: string | null
  contentId: string | null
  rawSize: number | null
  decodedSize: number | null
  // Additive: present only on leaf parts when the backend computes digests.
  checksums?: MimeNodeChecksums
  children: MimeNode[]
}

export interface InspectionHeader {
  name: string
  value: string
  occurrence: number
  line: number
}

export type CompatibilitySupport = 'yes' | 'partial' | 'no'

export interface CompatibilityClient {
  name: string
  family: string
  platform: string
  version: string
  support: CompatibilitySupport | string
  note: string | null
}

export interface CompatibilityScore {
  supported: number
  partial: number
  unsupported: number
}

export interface CompatibilityWarning {
  slug: string
  title: string
  category: string
  description: string
  url: string
  occurrences: number
  score: CompatibilityScore
  clients: CompatibilityClient[]
}

export interface CompatibilityPlatform {
  family: string
  platform: string
  label: string
}

// Can I Email data (caniemail.com, MIT) reported as an additive static dataset.
export interface HTMLCompatibility {
  dataVersion: string
  dataUpdated: string
  nodes: number
  tests: number
  score: CompatibilityScore
  platforms: CompatibilityPlatform[]
  warnings: CompatibilityWarning[]
  warningsTruncated?: boolean
  clientsTruncated?: boolean
  truncated?: boolean
}

export interface InspectionReport {
  analysis: InspectionAnalysis
  summary: InspectionSummary
  findings: InspectionFinding[]
  resources: InspectionResource[]
  mimeTree: MimeNode | null
  // Additive fields below: absent on older cached responses; renderers must tolerate undefined.
  headers?: InspectionHeader[]
  htmlCompatibility?: HTMLCompatibility | null
}

type CacheEntry = { data?: unknown; error?: unknown; promise?: Promise<void>; revalidateAfter?: boolean; generation: number; fetcher?: (key: string) => Promise<unknown>; listeners: Set<() => void> }
const cache = new Map<string, CacheEntry>()
const MAX_INACTIVE_INSPECTIONS = 8

function trimInspectionCache() {
  const inactive: string[] = []
  for (const [key, value] of cache) {
    if (key.endsWith('/inspect') && value.listeners.size === 0 && !value.promise) inactive.push(key)
  }
  for (let index = 0; index < inactive.length - MAX_INACTIVE_INSPECTIONS; index += 1) {
    cache.delete(inactive[index])
  }
}

function entry(key: string) {
  let value = cache.get(key)
  if (!value) { value = { generation: 0, listeners: new Set() }; cache.set(key, value) }
  return value
}

async function fetchInto<T>(key: string, fetcher: (key: string) => Promise<T>, force = false) {
  const current = entry(key)
  // Revalidation must reuse the fetcher that produced this entry; a JSON
  // revalidation of a text resource would poison the entry with a parse error.
  current.fetcher ??= fetcher
  if (current.promise) {
    if (force) current.revalidateAfter = true
    return current.promise
  }
  const generation = current.generation
  const promise = fetcher(key).then((data) => {
    if (current.generation !== generation) return
    current.data = data
    current.error = undefined
  }).catch((error) => {
    if (current.generation === generation) current.error = error
  }).finally(() => {
    if (current.promise !== promise) return
    current.promise = undefined
    current.listeners.forEach((listener) => listener())
    if (current.revalidateAfter) {
      current.revalidateAfter = false
      void fetchInto(key, fetcher)
    }
    trimInspectionCache()
  })
  current.promise = promise
  current.listeners.forEach((listener) => listener())
  return promise
}

function retryCachedResource<T>(key: string, fetcher: (key: string) => Promise<T>) {
  const current = entry(key)
  current.generation++
  current.data = undefined
  current.error = undefined
  current.promise = undefined
  current.revalidateAfter = false
  current.listeners.forEach((listener) => listener())
  void fetchInto(key, fetcher)
}

export type CacheMatcher = string | ((key: string) => boolean)
export function mutateCache<T>(matcher: CacheMatcher, updater?: (data: T | undefined) => T | undefined, revalidate = true) {
  const keys = typeof matcher === 'string' ? [matcher] : [...cache.keys()].filter(matcher)
  if (typeof matcher === 'string' && keys.length === 0) keys.push(matcher)
  for (const key of keys) {
    const current = entry(key)
    if (updater) {
      current.generation++
      current.data = updater(current.data as T | undefined)
    }
    current.listeners.forEach((listener) => listener())
    if (revalidate) void fetchInto(key, current.fetcher ?? jsonFetcher, true)
  }
}

export function useCachedResource<T>(key: string | null, fetcher: (key: string) => Promise<T> = jsonFetcher, keepPreviousData = false) {
  const [, render] = useState(0)
  const previous = useRef<T | undefined>(undefined)
  const current = key ? entry(key) : null
  const data = current?.data as T | undefined
  const error = current?.error
  if (key === null) previous.current = undefined
  else if (data !== undefined) previous.current = data

  useEffect(() => {
    if (!key) return
    const current = entry(key)
    const listener = () => render((value) => value + 1)
    current.listeners.add(listener)
    // A fetch can settle between the render-time snapshot and this subscription;
    // re-render if the snapshot changed so that late notification is not lost.
    if (current.data !== data || current.error !== error) render((value) => value + 1)
    if (current.data === undefined && !current.promise) void fetchInto(key, fetcher)
    return () => {
      current.listeners.delete(listener)
      if (key.endsWith('/source')) cache.delete(key)
      else if (key.endsWith('/inspect')) trimInspectionCache()
    }
  }, [key, fetcher])

  return {
    data: data ?? (keepPreviousData ? previous.current : undefined),
    error,
    isLoading: Boolean(key && data === undefined && (current?.promise || !current?.error)),
  }
}

const jsonFetcher = async <T,>(url: string): Promise<T> => {
  const response = await fetch(url)
  if (!response.ok) throw new Error(`Request failed: ${response.status}`)
  return response.json()
}

export function useMailboxes() { const { data, error, isLoading } = useCachedResource<{ mailboxes: Mailbox[] }>('/api/mailboxes'); return { mailboxes: data?.mailboxes ?? [], isLoading, error: error ?? null } }
export function useMessages(mailboxId: number | null, query?: string) { const q = query?.trim(); const key = mailboxId == null ? null : `/api/mailboxes/${mailboxId}/messages${q ? `?q=${encodeURIComponent(q)}` : ''}`; const { data, error, isLoading } = useCachedResource<{ messages: MessageListItem[] }>(key, jsonFetcher); return { messages: mailboxId == null ? [] : data?.messages ?? [], isLoading, error: error ?? null } }
export function useCalendarEvents(mailboxId: number | null, enabled: boolean) { const { data, error, isLoading } = useCachedResource<{ events: CalendarEvent[] }>(enabled && mailboxId != null ? `/api/mailboxes/${mailboxId}/events` : null); return { events: data?.events ?? [], isLoading, error: error ?? null } }
export function useInspection(messageId: number | null, enabled: boolean) {
  const key = enabled && messageId != null ? `/api/messages/${messageId}/inspect` : null
  const { data, error, isLoading } = useCachedResource<InspectionReport>(key)
  const retry = useCallback(() => {
    if (key) retryCachedResource(key, jsonFetcher)
  }, [key])
  return { inspection: data ?? null, isLoading, error: error ?? null, retry }
}
export function useMessage(messageId: number | null) { const { data, error, isLoading } = useCachedResource<{ message: FullMessage; attachments: AttachmentMeta[] }>(messageId != null ? `/api/messages/${messageId}` : null, jsonFetcher, true); return messageId == null ? { detail: null, isLoading: false, error: null } : { detail: data ?? null, isLoading, error: error ?? null } }

export async function runMessageAction(action: 'delete' | 'read' | 'unread', ids: number[]) { return (await fetch('/api/messages/actions', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ action, ids }) })).ok }
export async function deleteMailboxRequest(id: number) { return (await fetch(`/api/mailboxes/${id}`, { method: 'DELETE' })).ok }

// ID-keyed resources invalidated together after a reset or SSE reconnect, since numeric IDs may be reused.
function matchesIdKeyedResource(key: string) {
  return /^\/api\/mailboxes\/\d+\/(?:messages|events)(?:\?|$)/.test(key) || /^\/api\/messages\/\d+(?:\/(?:inspect|source))?$/.test(key) || /^\/api\/attachments\/[^/?]+(?:\?.*)?$/.test(key)
}

export function useRealtime(options: { selectedMailboxId: number | null; onReset: () => void; onNewMailbox?: (mailbox: { id: number; address: string }) => void; onMailboxDeleted?: (mailboxId: number) => void }) {
  const optionsRef = useRef(options)
  optionsRef.current = options
  useEffect(() => {
    const source = new EventSource('/api/events')
    source.onmessage = (event) => {
      let payload: { type: string; [key: string]: unknown }
      try { payload = JSON.parse(event.data) } catch { return }
      const { onReset, onNewMailbox, onMailboxDeleted } = optionsRef.current
      const mailboxId = payload.mailboxId as number
      switch (payload.type) {
        case 'connected':
          // The stream is best-effort and non-replayable: after every (re)connect,
          // discard ID-keyed caches that may have missed events, refetch what is
          // currently displayed, and revalidate the mailbox list in place.
          mutateCache(matchesIdKeyedResource, () => undefined, false)
          mutateCache((key) => matchesIdKeyedResource(key) && (cache.get(key)?.listeners.size ?? 0) > 0)
          mutateCache('/api/mailboxes')
          break
        case 'mailbox:new': mutateCache('/api/mailboxes'); onNewMailbox?.(payload.mailbox as { id: number; address: string }); break
        case 'mailbox:deleted': mutateCache('/api/mailboxes'); onMailboxDeleted?.(mailboxId); break
        case 'message:new':
        case 'messages:changed':
          mutateCache('/api/mailboxes')
          mutateCache(`/api/mailboxes/${mailboxId}/messages`)
          mutateCache((key) => key.startsWith(`/api/mailboxes/${mailboxId}/messages?`))
          break
        case 'calendar:changed': mutateCache(`/api/mailboxes/${mailboxId}/events`); break
        case 'reset':
          // Clear mailbox data before revalidating so the app cannot observe or
          // auto-select a just-deleted mailbox from stale state mid-refresh.
          mutateCache<{ mailboxes: Mailbox[] }>('/api/mailboxes', () => undefined)
          mutateCache(matchesIdKeyedResource, () => undefined, false)
          onReset()
          break
      }
    }
    return () => source.close()
  }, [])
}

export function refreshAfterRead(mailboxId: number) { mutateCache('/api/mailboxes'); mutateCache((key) => key.startsWith(`/api/mailboxes/${mailboxId}/messages`)) }
export function formatRelativeTime(timestamp: number) { const seconds = Math.floor((Date.now() - timestamp) / 1000); if (seconds < 10) return 'just now'; if (seconds < 60) return `${seconds}s ago`; const minutes = Math.floor(seconds / 60); if (minutes < 60) return `${minutes}m ago`; const hours = Math.floor(minutes / 60); if (hours < 24) return `${hours}h ago`; const days = Math.floor(hours / 24); return days < 7 ? `${days}d ago` : new Date(timestamp).toLocaleDateString() }
export function formatBytes(bytes: number) { if (bytes < 1024) return `${bytes} B`; if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`; return `${(bytes / (1024 * 1024)).toFixed(1)} MB` }
