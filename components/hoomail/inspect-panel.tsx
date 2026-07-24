import type { ComponentChildren, ComponentType } from 'preact'
import { useMemo } from 'preact/hooks'
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
  TriangleAlert,
  XCircle,
  type IconProps,
} from '@/components/ui/icons'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cn } from '@/lib/utils'
import {
  formatBytes,
  useInspection,
  type InspectionEvidence,
  type InspectionFinding,
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
  pass: { label: 'Pass', icon: CheckCircle2, className: 'text-foreground' },
  fail: { label: 'Fail', icon: XCircle, className: 'text-destructive' },
  observed: { label: 'Observed', icon: Info, className: 'text-muted-foreground' },
  'not-evaluated': { label: 'Not evaluated', icon: TriangleAlert, className: 'text-muted-foreground' },
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
            <dt className="text-[11px] text-muted-foreground">{label}</dt>
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
    <li className="rounded-lg border border-border bg-card px-3 py-2.5">
      <div className="flex items-start gap-2.5">
        <OutcomeIcon className={cn('mt-0.5 size-4 shrink-0', outcome.className)} aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <p className="mr-auto text-[13px] font-medium leading-snug">{finding.label}</p>
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
  return <Badge variant="secondary" className="text-[10px] capitalize">{label}</Badge>
}

function EvidenceList({ evidence, truncated }: { evidence: InspectionEvidence[]; truncated: boolean }) {
  return (
    <div className="mt-2 rounded-md bg-muted/50 px-2.5 py-2">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <p className="text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">Evidence</p>
        {truncated && <Badge variant="outline" className="text-[10px]">Truncated</Badge>}
      </div>
      <ul className="flex flex-col gap-1.5">
        {evidence.map((item, index) => (
          <li key={index} className="text-[11px] leading-relaxed text-muted-foreground">
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
    return <p className="mt-2 text-[11px] text-muted-foreground">Source: {reference.label}</p>
  }
  return (
    <a
      href={reference.url}
      target="_blank"
      rel="noopener noreferrer"
      className="mt-2 inline-flex items-center gap-1 rounded-sm text-[11px] font-medium text-foreground underline underline-offset-2 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-none"
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
          <span className="text-[11px] font-medium">{meta.label}</span>
          {resource.path && <code className="font-mono text-[10px] text-muted-foreground">{resource.path}</code>}
          {resource.occurrenceCount > 1 && <Badge variant="secondary" className="text-[10px]">{resource.occurrenceCount} occurrences</Badge>}
        </div>
        {resource.url && <p className="break-all font-mono text-xs">{resource.url}</p>}
        {resource.text && <p className="break-words text-[11px] text-muted-foreground">{resource.text}</p>}
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
            <code className="rounded bg-secondary px-1.5 py-0.5 font-mono text-[10px] text-secondary-foreground">{node.path}</code>
            <code className="font-mono text-xs">{node.contentType}</code>
            {node.charset && <span className="font-mono text-[10px] text-muted-foreground">{node.charset}</span>}
            {node.encoding && <span className="font-mono text-[10px] text-muted-foreground">{node.encoding}</span>}
            {node.disposition && <Badge variant="secondary" className="text-[10px]">{node.disposition}</Badge>}
          </div>
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-muted-foreground">
            <span>Raw {node.rawSize === null ? 'unknown' : formatBytes(node.rawSize)}</span>
            <span>Decoded {node.decodedSize === null ? 'unknown' : formatBytes(node.decodedSize)}</span>
            {node.filename && <span className="break-all font-mono">{node.filename}</span>}
            {node.contentId && <span className="break-all font-mono">{node.contentId}</span>}
          </div>
        </li>
      ))}
    </ol>
  )
}
