import { Button } from '@/components/ui/button'

export function ResourceStatus({ label, isLoading, error, onRetry }: {
  label: string
  isLoading: boolean
  error?: unknown
  onRetry: () => void
}) {
  if (error) {
    return (
      <div role="alert" className="flex flex-wrap items-center justify-center gap-2 px-3 py-4 text-center text-xs text-destructive">
        <span>Could not load {label}.</span>
        <Button type="button" size="sm" variant="outline" onClick={onRetry} aria-label={`Retry ${label}`}>Retry</Button>
      </div>
    )
  }
  return isLoading ? <p role="status" className="px-3 py-4 text-center text-xs text-muted-foreground">Loading {label}…</p> : null
}
