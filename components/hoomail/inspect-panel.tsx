import {
  CheckCircle2,
  ExternalLink,
  Eye,
  ImageIcon,
  Info,
  Link2,
  TriangleAlert,
  XCircle,
} from '@/components/ui/icons'
import { Badge } from '@/components/ui/badge'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cn } from '@/lib/utils'
import {
  formatBytes,
  useInspection,
  type ExtractedLink,
  type HeaderCheck,
  type MimeNode,
} from './use-hoomail'

export function InspectPanel({ messageId, active }: { messageId: number; active: boolean }) {
  const { inspection, isLoading } = useInspection(messageId, active)

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
        <section aria-label="Header checks">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Checks
          </h2>
          <ul className="flex flex-col gap-1.5">
            {inspection.checks.map((check) => (
              <CheckRow key={check.id} check={check} />
            ))}
          </ul>
        </section>

        <section aria-label="Links and images">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            Links {'&'} images ({inspection.links.length})
          </h2>
          {inspection.links.length === 0 ? (
            <p className="text-sm text-muted-foreground">No links or external images found.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {inspection.links.map((link, i) => (
                <LinkRow key={`${link.href}-${i}`} link={link} />
              ))}
            </ul>
          )}
        </section>

        <section aria-label="MIME structure">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
            MIME structure
          </h2>
          {inspection.mimeTree ? (
            <div className="rounded-md border border-border bg-card p-3">
              <MimeTreeNode node={inspection.mimeTree} depth={0} />
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">
              Raw source not available for this message (received before inspection support).
            </p>
          )}
        </section>
      </div>
      </ScrollArea>
    </>
  )
}

const CHECK_META = {
  pass: { icon: CheckCircle2, className: 'text-emerald-500' },
  warn: { icon: TriangleAlert, className: 'text-amber-500' },
  fail: { icon: XCircle, className: 'text-destructive' },
  info: { icon: Info, className: 'text-muted-foreground' },
} as const

function CheckRow({ check }: { check: HeaderCheck }) {
  const meta = CHECK_META[check.status]
  const Icon = meta.icon
  return (
    <li className="flex items-start gap-2.5 rounded-md border border-border bg-card px-3 py-2">
      <Icon className={cn('mt-0.5 size-4 shrink-0', meta.className)} aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="text-[13px] font-medium leading-snug">{check.label}</p>
        <p className="text-xs leading-relaxed text-muted-foreground">{check.detail}</p>
      </div>
      <span className="sr-only">{check.status}</span>
    </li>
  )
}

const LINK_META = {
  link: { icon: Link2, label: 'link' },
  image: { icon: ImageIcon, label: 'image' },
  'tracking-pixel': { icon: Eye, label: 'pixel' },
} as const

function LinkRow({ link }: { link: ExtractedLink }) {
  const meta = LINK_META[link.kind]
  const Icon = meta.icon
  return (
    <li className="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-accent/50">
      <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-mono text-xs">{link.href}</p>
        {link.text && (
          <p className="truncate text-[11px] text-muted-foreground">{link.text}</p>
        )}
      </div>
      {link.kind === 'tracking-pixel' && (
        <Badge
          variant="outline"
          className="shrink-0 border-amber-500/40 text-[10px] text-amber-500"
        >
          tracking
        </Badge>
      )}
      <a
        href={link.href}
        target="_blank"
        rel="noopener noreferrer"
        className="shrink-0 rounded p-1 text-muted-foreground transition-colors hover:text-foreground"
        aria-label={`Open ${link.href} in a new tab`}
      >
        <ExternalLink className="size-3.5" aria-hidden="true" />
      </a>
    </li>
  )
}

function MimeTreeNode({ node, depth }: { node: MimeNode; depth: number }) {
  return (
    <div className={cn(depth > 0 && 'mt-1.5 border-l border-border pl-4')}>
      <div className="flex flex-wrap items-center gap-1.5">
        <code className="rounded bg-secondary px-1.5 py-0.5 font-mono text-xs text-secondary-foreground">
          {node.contentType}
        </code>
        {node.charset && (
          <span className="font-mono text-[10px] text-muted-foreground">{node.charset}</span>
        )}
        {node.encoding && (
          <span className="font-mono text-[10px] text-muted-foreground">{node.encoding}</span>
        )}
        {node.disposition && (
          <Badge variant="secondary" className="text-[10px]">
            {node.disposition}
          </Badge>
        )}
        {node.filename && (
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            {node.filename}
          </span>
        )}
        {node.children.length === 0 && (
          <span className="font-mono text-[10px] text-muted-foreground">
            {formatBytes(node.size)}
          </span>
        )}
      </div>
      {node.children.map((child, i) => (
        <MimeTreeNode key={i} node={child} depth={depth + 1} />
      ))}
    </div>
  )
}
