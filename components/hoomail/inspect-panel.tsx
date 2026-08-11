import type { ComponentChildren, ComponentType } from 'preact'
import { useMemo, useState } from 'preact/hooks'
import {
  CheckCircle2,
  ExternalLink,
  Eye,
  FileText,
  ImageIcon,
  Info,
  Link2,
  Paperclip,
  RefreshCw,
  Search,
  TriangleAlert,
  XCircle,
  type IconProps,
} from '@/components/ui/icons'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cn } from '@/lib/utils'
import {
  formatBytes,
  useInspection,
  type CompatibilityWarning,
  type HTMLCompatibility,
  type InspectionEvidence,
  type InspectionFinding,
  type InspectionHeader,
  type InspectionReport,
  type InspectionResource,
  type MimeNode,
} from './use-hoomail'

type IconComponent = ComponentType<IconProps>

const CATEGORY_META = [
  ['analysis', 'Analysis'],
  ['message', 'Message format'],
  ['mime', 'MIME'],
  ['authentication', 'Authentication evidence'],
  ['unsubscribe', 'Unsubscribe readiness'],
  ['content', 'Content & accessibility'],
  ['privacy', 'Privacy'],
  ['compatibility', 'Compatibility'],
] as const
const CATEGORY_KEYS: Record<string, true> = Object.fromEntries(CATEGORY_META.map(([category]) => [category, true]))

const EVIDENCE_SOURCES: Record<string, string> = {
  'raw-header': 'Raw header',
  'raw-line': 'Raw line',
  'mime-part': 'MIME part',
  html: 'HTML',
  text: 'Text',
}

const OUTCOME_META: Record<string, { label: string; icon: IconComponent; className: string }> = {
  pass: { label: 'Pass', icon: CheckCircle2, className: 'text-green-500' },
  fail: { label: 'Fail', icon: XCircle, className: 'text-destructive' },
  observed: { label: 'Observed', icon: Info, className: 'text-blue-500' },
  'not-evaluated': { label: 'Not evaluated', icon: TriangleAlert, className: 'text-amber-500' },
}

const RESOURCE_META: Record<string, { label: string; icon: IconComponent }> = {
  link: { label: 'Link', icon: Link2 },
  image: { label: 'Image', icon: ImageIcon },
  'tracking-pixel': { label: 'Tracking pixel', icon: Eye },
  cid: { label: 'CID resource', icon: ImageIcon },
  data: { label: 'Data resource', icon: FileText },
  attachment: { label: 'Attachment', icon: Paperclip },
}

const SUMMARY_ITEMS = [
  ['fail', 'Fail'],
  ['warning', 'Warning'],
  ['advisory', 'Advisory'],
  ['observed', 'Observed'],
  ['pass', 'Pass'],
  ['notEvaluated', 'Not evaluated'],
] as const

const SUPPORT_META: Record<string, { label: string; icon: IconComponent; className: string }> = {
  yes: { label: 'Supported', icon: CheckCircle2, className: 'text-green-500' },
  partial: { label: 'Partial', icon: TriangleAlert, className: 'text-amber-500' },
  no: { label: 'Unsupported', icon: XCircle, className: 'text-destructive' },
}
const UNKNOWN_SUPPORT = { label: 'Unknown', icon: Info, className: 'text-muted-foreground' }

const PLATFORM_META: Record<string, string> = {
  desktop: 'Desktop',
  webmail: 'Webmail',
  mobile: 'Mobile',
}

const COMPATIBILITY_CATEGORY_LABELS: Record<string, string> = {
  css: 'CSS',
  html: 'HTML',
  image: 'Images',
  others: 'Other',
}

function formatPercent(value: number): string {
  if (!Number.isFinite(value)) return '0%'
  return `${value.toFixed(2).replace(/\.?0+$/, '')}%`
}

function clientScore(clients: CompatibilityWarning['clients']): HTMLCompatibility['score'] | null {
  if (clients.length === 0) return null
  let supported = 0
  let partial = 0
  let unsupported = 0
  for (const client of clients) {
    if (client.support === 'yes') supported += 1
    else if (client.support === 'partial') partial += 1
    else if (client.support === 'no') unsupported += 1
  }
  const total = supported + partial + unsupported
  if (total === 0) return null
  return {
    supported: supported / total * 100,
    partial: partial / total * 100,
    unsupported: unsupported / total * 100,
  }
}

export function InspectPanel({ messageId, active }: { messageId: number; active: boolean }) {
  const { inspection, isLoading, error, retry } = useInspection(messageId, active)
  const groupedFindings = useMemo(() => {
    const groups = new Map<string, InspectionFinding[]>()
    if (!inspection) return groups
    for (const finding of inspection.findings) {
      const category = CATEGORY_KEYS[finding.category] ? finding.category : 'unknown'
      const findings = groups.get(category)
      if (findings) findings.push(finding)
      else groups.set(category, [finding])
    }
    return groups
  }, [inspection?.findings])

  if (error) {
    return (
      <div className="flex h-full items-center justify-center px-5 py-4">
        <div className="flex max-w-sm flex-col items-center gap-3 text-center">
          <XCircle className="size-5 text-destructive" aria-hidden="true" />
          <p role="alert" className="text-sm font-medium">Could not analyze this message.</p>
          <Button variant="outline" size="sm" onClick={retry}>
            <RefreshCw aria-hidden="true" />
            Retry analysis
          </Button>
        </div>
      </div>
    )
  }

  if (isLoading || !inspection) {
    return (
      <div role="status" aria-live="polite" className="flex h-full items-center justify-center">
        <p className="text-sm text-muted-foreground">Analyzing message…</p>
      </div>
    )
  }

  return (
    <>
      <span role="status" aria-live="polite" className="sr-only">Message analysis complete</span>
      <ScrollArea className="h-full" aria-label="Message inspection results">
        <div className="flex flex-col gap-6 px-5 py-4">
          <InspectionSummary report={inspection} />

          {CATEGORY_META.map(([category, label]) => {
            const findings = groupedFindings.get(category)
            return findings ? <FindingGroup key={category} label={label} findings={findings} /> : null
          })}

          {groupedFindings.has('unknown') && (
            <FindingGroup label="Unknown" findings={groupedFindings.get('unknown')!} />
          )}

          {inspection.resources.length > 0 && (
            <section aria-label="Links and images">
              <SectionHeading>Links {'&'} images ({inspection.resources.length})</SectionHeading>
              <ul className="divide-y divide-border rounded-lg border border-border bg-card">
                {inspection.resources.map((resource, index) => (
                  <ResourceRow key={`${resource.kind}-${resource.path ?? ''}-${resource.url}-${index}`} resource={resource} />
                ))}
              </ul>
            </section>
          )}

          {inspection.htmlCompatibility && <CompatibilitySection data={inspection.htmlCompatibility} />}

          {inspection.headers && <HeadersSection headers={inspection.headers} />}

          <OfflineExclusionsSection />

          <section aria-label="MIME structure">
            <SectionHeading>MIME structure</SectionHeading>
            {inspection.mimeTree ? (
              <MimeTree tree={inspection.mimeTree} />
            ) : (
              <p className="text-sm text-muted-foreground">MIME structure unavailable</p>
            )}
          </section>
        </div>
      </ScrollArea>
    </>
  )
}

function InspectionSummary({ report }: { report: InspectionReport }) {
  return (
    <section aria-label="Inspection summary" className="rounded-lg border border-border bg-card p-3.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-sm font-semibold">Inspection summary</h2>
        <div className="flex flex-wrap gap-1.5">
          {report.analysis.state === 'partial' && <Badge variant="outline">Partial analysis</Badge>}
          {report.analysis.truncated && <Badge variant="outline">Truncated</Badge>}
          {!['complete', 'partial'].includes(report.analysis.state) && <Badge variant="outline">Unknown state</Badge>}
        </div>
      </div>
      <dl className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-3 lg:grid-cols-6">
        {SUMMARY_ITEMS.map(([key, label]) => (
          <div key={key} className="flex items-baseline justify-between gap-2 border-b border-border/60 pb-1 lg:block lg:border-b-0 lg:pb-0">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="font-mono text-sm font-semibold">{report.summary[key]}</dd>
          </div>
        ))}
      </dl>
      {report.analysis.state === 'partial' && (
        <div className="mt-3 border-t border-border/60 pt-3 text-xs leading-relaxed text-muted-foreground">
          {report.analysis.parsedThroughPath && <p>Parsed through MIME path <code className="font-mono">{report.analysis.parsedThroughPath}</code>.</p>}
          {report.analysis.unavailableRuleFamilies.length > 0 && (
            <p>Unavailable checks: {report.analysis.unavailableRuleFamilies.join(', ')}.</p>
          )}
        </div>
      )}
      <p className="mt-3 border-t border-border/60 pt-3 text-xs leading-relaxed text-muted-foreground">
        Static offline analysis. Authentication, delivery, and unsubscribe endpoints are not verified.
      </p>
    </section>
  )
}

function SectionHeading({ children }: { children: ComponentChildren }) {
  return <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{children}</h2>
}

function FindingGroup({ label, findings }: { label: string; findings: InspectionFinding[] }) {
  return (
    <section aria-label={label}>
      <SectionHeading>{label}</SectionHeading>
      <ul className="flex flex-col gap-2">
        {findings.map((finding) => <FindingRow key={finding.id} finding={finding} />)}
      </ul>
    </section>
  )
}

function FindingRow({ finding }: { finding: InspectionFinding }) {
  const outcome = OUTCOME_META[finding.outcome] ?? { label: 'Unknown', icon: Info, className: 'text-muted-foreground' }
  const OutcomeIcon = outcome.icon
  return (
    <li data-outcome={finding.outcome} className="rounded-lg border border-border bg-card px-3 py-2.5">
      <div className="flex items-start gap-2.5">
        <OutcomeIcon className={cn('mt-0.5 size-4 shrink-0', outcome.className)} aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <p className="mr-auto text-sm font-medium leading-snug">{finding.label}</p>
            <MetaBadge value={finding.severity} />
            <MetaBadge value={finding.basis} />
            <MetaBadge value={finding.applicability} />
          </div>
          <span className="sr-only">{outcome.label}</span>
          <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{finding.detail}</p>
          {finding.evidence.length > 0 && <EvidenceList evidence={finding.evidence} truncated={finding.evidenceTruncated} />}
          {finding.reference && <ReferenceLink reference={finding.reference} />}
        </div>
      </div>
    </li>
  )
}

function MetaBadge({ value }: { value: string }) {
  const label = value && ['error', 'warning', 'advisory', 'none', 'standard', 'recommendation', 'heuristic', 'evidence', 'all', 'html', 'mailing-list', 'one-click-claim', 'bulk-marketing', 'unknown'].includes(value)
    ? value.replaceAll('-', ' ')
    : 'Unknown'
  return <Badge variant="secondary" className="text-[12px] capitalize">{label}</Badge>
}

function EvidenceList({ evidence, truncated }: { evidence: InspectionEvidence[]; truncated: boolean }) {
  return (
    <div className="mt-2 rounded-md bg-muted/50 px-2.5 py-2">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <p className="text-[12px] font-semibold uppercase tracking-wide text-muted-foreground">Evidence</p>
        {truncated && <Badge variant="outline" className="text-[12px]">Truncated</Badge>}
      </div>
      <ul className="flex flex-col gap-1.5">
        {evidence.map((item, index) => (
          <li key={index} className="text-xs leading-relaxed text-muted-foreground">
            <span className="font-medium text-foreground">{evidenceLocation(item)}</span>
            {item.value !== undefined && <span className="block break-words font-mono">{item.value}</span>}
          </li>
        ))}
      </ul>
    </div>
  )
}

function evidenceLocation(evidence: InspectionEvidence): string {
  const parts = [EVIDENCE_SOURCES[evidence.source] ?? 'Unknown']
  if (evidence.path) parts.push(`path ${evidence.path}`)
  if (evidence.field) parts.push(evidence.field)
  if (evidence.occurrence !== undefined) parts.push(`occurrence ${evidence.occurrence}`)
  if (evidence.line !== undefined) parts.push(`line ${evidence.line}`)
  return parts.join(' · ')
}

function ReferenceLink({ reference }: { reference: { label: string; url: string } }) {
  if (!safeExternalURL(reference.url, false)) {
    return <p className="mt-2 text-xs text-muted-foreground">Source: {reference.label}</p>
  }
  return (
    <a
      href={reference.url}
      target="_blank"
      rel="noopener noreferrer"
      className="mt-2 inline-flex items-center gap-1 rounded-sm text-xs font-medium text-foreground underline underline-offset-2 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
      aria-label={`Open source ${reference.label} in a new tab`}
    >
      {reference.label}
      <ExternalLink className="size-3" aria-hidden="true" />
    </a>
  )
}

function ResourceRow({ resource }: { resource: InspectionResource }) {
  const meta = RESOURCE_META[resource.kind] ?? { label: 'Unknown', icon: Info }
  const Icon = meta.icon
  const canOpen = resource.url !== '' && safeExternalURL(resource.url, true)
  return (
    <li className="flex items-start gap-2.5 px-3 py-2.5">
      <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs font-medium">{meta.label}</span>
          {resource.path && <code className="font-mono text-[12px] text-muted-foreground">{resource.path}</code>}
          {resource.occurrenceCount > 1 && <Badge variant="secondary" className="text-[12px]">{resource.occurrenceCount} occurrences</Badge>}
        </div>
        {resource.url && <p className="break-all font-mono text-xs">{resource.url}</p>}
        {resource.text && <p className="break-words text-xs text-muted-foreground">{resource.text}</p>}
      </div>
      {canOpen && (
        <a
          href={resource.url}
          target="_blank"
          rel="noopener noreferrer"
          className="shrink-0 rounded-sm p-1 text-muted-foreground transition-colors hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          aria-label={`Open ${meta.label.toLowerCase()} destination ${resource.url} in a new tab`}
        >
          <ExternalLink className="size-3.5" aria-hidden="true" />
        </a>
      )}
    </li>
  )
}

function safeExternalURL(value: string, allowMailto: boolean): boolean {
  try {
    const protocol = new URL(value).protocol
    return protocol === 'http:' || protocol === 'https:' || (allowMailto && protocol === 'mailto:')
  } catch {
    return false
  }
}

function MimeTree({ tree }: { tree: MimeNode }) {
  const nodes = useMemo(() => {
    const flattened: MimeNode[] = []
    const pending = [tree]
    while (pending.length > 0) {
      const node = pending.pop()
      if (!node) continue
      flattened.push(node)
      for (let index = node.children.length - 1; index >= 0; index -= 1) pending.push(node.children[index])
    }
    return flattened
  }, [tree])

  return (
    <ol className="divide-y divide-border rounded-lg border border-border bg-card">
      {nodes.map((node, index) => (
        <li key={`${node.path}-${index}`} className="px-3 py-2.5">
          <div className="flex flex-wrap items-center gap-1.5">
            <code className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[12px] text-secondary-foreground">{node.path}</code>
            <code className="font-mono text-xs">{node.contentType}</code>
            {node.charset && <span className="font-mono text-[12px] text-muted-foreground">{node.charset}</span>}
            {node.encoding && <span className="font-mono text-[12px] text-muted-foreground">{node.encoding}</span>}
            {node.disposition && <Badge variant="secondary" className="text-[12px]">{node.disposition}</Badge>}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-muted-foreground">
            <span>Raw {node.rawSize === null ? 'unknown' : formatBytes(node.rawSize)}</span>
            <span>Decoded {node.decodedSize === null ? 'unknown' : formatBytes(node.decodedSize)}</span>
            {node.filename && <span className="break-all font-mono">{node.filename}</span>}
            {node.contentId && <span className="break-all font-mono">{node.contentId}</span>}
          </div>
          {node.children.length === 0 && node.checksums && (
            <dl className="mt-1.5 flex flex-col gap-0.5 text-[12px]">
              {(['md5', 'sha1', 'sha256'] as const).map((algorithm) => (
                <div key={algorithm} className="flex flex-wrap items-baseline gap-x-2">
                  <dt className="shrink-0 font-mono uppercase text-muted-foreground">{algorithm}</dt>
                  <dd>
                    <code className="break-all rounded bg-muted px-1 py-0.5 font-mono select-all">{node.checksums![algorithm]}</code>
                  </dd>
                </div>
              ))}
            </dl>
          )}
        </li>
      ))}
    </ol>
  )
}

function CompatibilitySection({ data }: { data: HTMLCompatibility }) {
  const [platform, setPlatform] = useState('all')
  const platforms = useMemo(() => {
    const seen = new Map<string, string>()
    for (const entry of data.platforms) {
      const label = PLATFORM_META[entry.platform] ?? entry.label ?? entry.platform
      if (!seen.has(entry.platform)) seen.set(entry.platform, label)
    }
    return [...seen.entries()]
  }, [data.platforms])

  const effectivePlatform = data.clientsTruncated ? 'all' : platform
  const filtered = useMemo(
    () => effectivePlatform === 'all'
      ? data.warnings
      : data.warnings.filter((warning) => warning.clients.some((client) => client.platform === effectivePlatform)),
    [data.warnings, effectivePlatform],
  )
  const platformLabel = effectivePlatform === 'all' ? null : (PLATFORM_META[effectivePlatform] ?? effectivePlatform)
  return (
    <section aria-label="HTML compatibility">
      <SectionHeading>HTML compatibility ({data.warnings.length} {data.warnings.length === 1 ? 'warning' : 'warnings'})</SectionHeading>
      <div className="rounded-lg border border-border bg-card p-3.5">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <dl className="flex flex-wrap gap-x-4 gap-y-1">
            <div className="flex items-baseline gap-1.5">
              <dt className="text-xs text-muted-foreground">Overall supported</dt>
              <dd className="font-mono text-sm font-semibold text-green-600 dark:text-green-500">{formatPercent(data.score.supported)}</dd>
            </div>
            <div className="flex items-baseline gap-1.5">
              <dt className="text-xs text-muted-foreground">Overall partial</dt>
              <dd className="font-mono text-sm font-semibold text-amber-600 dark:text-amber-500">{formatPercent(data.score.partial)}</dd>
            </div>
            <div className="flex items-baseline gap-1.5">
              <dt className="text-xs text-muted-foreground">Overall unsupported</dt>
              <dd className="font-mono text-sm font-semibold text-destructive">{formatPercent(data.score.unsupported)}</dd>
            </div>
          </dl>
          {!data.clientsTruncated && platforms.length > 1 && (
            <div className="flex flex-wrap gap-1" role="group" aria-label="Filter warnings by client platform">
              <button
                type="button"
                aria-pressed={platform === 'all'}
                onClick={() => setPlatform('all')}
                className={cn(
                  'rounded-md border px-2 py-1 text-xs transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none',
                  platform === 'all' ? 'border-transparent bg-primary text-primary-foreground' : 'border-border text-muted-foreground hover:text-foreground',
                )}
              >
                All platforms
              </button>
              {platforms.map(([key, label]) => (
                <button
                  key={key}
                  type="button"
                  aria-pressed={platform === key}
                  onClick={() => setPlatform(key)}
                  className={cn(
                    'rounded-md border px-2 py-1 text-xs transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none',
                    platform === key ? 'border-transparent bg-primary text-primary-foreground' : 'border-border text-muted-foreground hover:text-foreground',
                  )}
                >
                  {label}
                </button>
              ))}
            </div>
          )}
        </div>
        <p className="mt-2 text-[12px] text-muted-foreground">
          {data.nodes.toLocaleString()} HTML nodes · {data.tests.toLocaleString()} tests · Can I Email data {data.dataVersion} updated {data.dataUpdated}
        </p>
        {data.clientsTruncated && (
          <p className="text-[12px] text-muted-foreground">
            Client details and platform filters are unavailable because this bounded report was trimmed.
          </p>
        )}
        <p className="text-[12px] text-muted-foreground">
          Static markup support from the <a href="https://www.caniemail.com" target="_blank" rel="noopener noreferrer" className="rounded-sm underline underline-offset-2 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none">Can I Email</a> dataset; no messages are sent to real clients.
        </p>
      </div>
      {filtered.length === 0 ? (
        <p className="mt-2 text-sm text-muted-foreground">No warnings{platformLabel ? ` for ${platformLabel}` : ''}.</p>
      ) : (
        <ul className="mt-2 flex flex-col gap-2">
          {filtered.map((warning) => <CompatibilityWarningRow key={warning.slug} warning={warning} platform={effectivePlatform} />)}
        </ul>
      )}
    </section>
  )
}

function CompatibilityWarningRow({ warning, platform }: { warning: CompatibilityWarning; platform: string }) {
  const clients = platform === 'all' ? warning.clients : warning.clients.filter((client) => client.platform === platform)
  const score = platform === 'all' ? warning.score : clientScore(clients)
  const categoryLabel = COMPATIBILITY_CATEGORY_LABELS[warning.category] ?? (warning.category || 'Unknown')
  return (
    <li className="rounded-lg border border-border bg-card px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-1.5">
        <p className="mr-auto text-sm font-medium leading-snug">{warning.title}</p>
        <Badge variant="secondary" className="text-[12px]">{categoryLabel}</Badge>
        {warning.occurrences > 1 && <Badge variant="secondary" className="text-[12px]">{warning.occurrences} occurrences</Badge>}
      </div>
      <p className="mt-1 text-xs leading-relaxed text-muted-foreground">{warning.description}</p>
      {score ? (
        <p className="mt-1.5 text-[12px]">
          <span className="font-medium text-green-600 dark:text-green-500">{formatPercent(score.supported)} supported</span>
          <span className="text-muted-foreground"> · </span>
          <span className="font-medium text-amber-600 dark:text-amber-500">{formatPercent(score.partial)} partial</span>
          <span className="text-muted-foreground"> · </span>
          <span className="font-medium text-destructive">{formatPercent(score.unsupported)} unsupported</span>
        </p>
      ) : (
        <p className="mt-1.5 text-[12px] text-muted-foreground">Compatibility score unavailable for this platform.</p>
      )}
      {clients.length > 0 && (
        <ul className="mt-2 flex flex-col divide-y divide-border/60 border-t border-border/60">
          {clients.map((client) => {
            const meta = SUPPORT_META[client.support] ?? UNKNOWN_SUPPORT
            const SupportIcon = meta.icon
            return (
              <li key={client.name} className="flex items-start gap-2 py-1.5">
                <SupportIcon className={cn('mt-0.5 size-3.5 shrink-0', meta.className)} aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  <p className="text-xs font-medium">{client.name}</p>
                  {client.note && <p className="text-[12px] leading-relaxed text-muted-foreground">{client.note}</p>}
                </div>
                <Badge variant="secondary" className={cn('shrink-0 text-[12px]', meta.className)}>{meta.label}</Badge>
              </li>
            )
          })}
        </ul>
      )}
      {safeExternalURL(warning.url, false) && (
        <a
          href={warning.url}
          target="_blank"
          rel="noopener noreferrer"
          className="mt-2 inline-flex items-center gap-1 rounded-sm text-xs font-medium text-foreground underline underline-offset-2 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
          aria-label={`Open Can I Email reference for ${warning.title} in a new tab`}
        >
          Can I Email
          <ExternalLink className="size-3" aria-hidden="true" />
        </a>
      )}
    </li>
  )
}

function HeadersSection({ headers }: { headers: InspectionHeader[] }) {
  const [query, setQuery] = useState('')
  const terms = useMemo(() => query.toLowerCase().split(/\s+/).filter(Boolean), [query])
  const filtered = useMemo(
    () => terms.length === 0 ? headers : headers.filter((header) => terms.every((term) => header.name.toLowerCase().includes(term) || header.value.toLowerCase().includes(term))),
    [headers, terms],
  )
  const showHighlight = terms.length > 0

  return (
    <section aria-label="Message headers">
      <SectionHeading>Headers ({headers.length})</SectionHeading>
      {headers.length === 0 ? (
        <p className="text-sm text-muted-foreground">No headers recorded.</p>
      ) : (
        <>
          <div className="relative mb-2">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden="true" />
            <Input
              type="search"
              value={query}
              onInput={(event) => setQuery(event.currentTarget.value)}
              placeholder="Filter headers…"
              aria-label="Filter headers"
              className="pl-8"
            />
          </div>
          <span role="status" aria-live="polite" aria-atomic="true" className="sr-only">
            {filtered.length} of {headers.length} headers match
          </span>
          {filtered.length === 0 ? (
            <p className="text-sm text-muted-foreground">No headers match “{query}”.</p>
          ) : (
            <ul className="divide-y divide-border rounded-lg border border-border bg-card">
              {filtered.map((header) => (
                <li key={`${header.name}-${header.occurrence}-${header.line}`} className="px-3 py-2">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <p className="break-all font-mono text-xs font-semibold"><HighlightedText text={header.name} terms={terms} /></p>
                    <span className="text-[12px] text-muted-foreground">line {header.line}{header.occurrence > 1 ? ` · occurrence ${header.occurrence}` : ''}</span>
                  </div>
                  <p className="break-all font-mono text-xs text-muted-foreground">
                    {showHighlight ? <HighlightedText text={header.value} terms={terms} /> : header.value}
                  </p>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  )
}

function HighlightedText({ text, terms }: { text: string; terms: string[] }) {
  const ranges: Array<[number, number]> = []
  const lower = text.toLowerCase()
  for (const term of terms) {
    let index = lower.indexOf(term)
    while (index !== -1) {
      ranges.push([index, index + term.length])
      index = lower.indexOf(term, index + term.length)
    }
  }
  if (ranges.length === 0) return <>{text}</>
  ranges.sort((a, b) => a[0] - b[0])
  const merged: Array<[number, number]> = []
  for (const range of ranges) {
    const last = merged[merged.length - 1]
    if (last && range[0] <= last[1]) last[1] = Math.max(last[1], range[1])
    else merged.push([...range])
  }
  const parts: ComponentChildren[] = []
  let cursor = 0
  for (const [start, end] of merged) {
    if (start > cursor) parts.push(text.slice(cursor, start))
    parts.push(<mark key={start} className="rounded-sm bg-amber-200/60 font-inherit text-foreground dark:bg-amber-500/30">{text.slice(start, end)}</mark>)
    cursor = end
  }
  if (cursor < text.length) parts.push(text.slice(cursor))
  return <>{parts}</>
}

function OfflineExclusionsSection() {
  return (
    <section aria-label="Unavailable checks">
      <SectionHeading>Unavailable in offline inspection</SectionHeading>
      <ul className="flex flex-col gap-2">
        <li className="flex items-start gap-2.5 rounded-lg border border-border bg-card px-3 py-2.5">
          <Link2 className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium leading-snug">Link status {'&'} redirects</p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
              HTTP status and redirect checks require contacting link destinations. Hoomail never performs network requests during inspection, so destinations are not fetched.
            </p>
          </div>
        </li>
        <li className="flex items-start gap-2.5 rounded-lg border border-border bg-card px-3 py-2.5">
          <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium leading-snug">SpamAssassin scoring</p>
            <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
              External SpamAssassin analysis requires a running SpamAssassin service, which is not part of Hoomail's offline inspection.
            </p>
          </div>
        </li>
      </ul>
    </section>
  )
}
