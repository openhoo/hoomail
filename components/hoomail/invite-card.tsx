import { CalendarDays, CalendarX2, MapPin, RefreshCw, User, Users } from '@/components/ui/icons'
import { Badge } from '@/components/ui/badge'
import { cn } from '@/lib/utils'
import type { IcalEvent } from './use-hoomail'

function formatEventRange(event: IcalEvent): string {
  const start = new Date(event.dtstart)
  const dateFmt: Intl.DateTimeFormatOptions = {
    weekday: 'short',
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  }
  if (event.allDay) {
    return `${start.toLocaleDateString(undefined, dateFmt)} (all day)`
  }
  const timeFmt: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' }
  const startStr = `${start.toLocaleDateString(undefined, dateFmt)} ${start.toLocaleTimeString(undefined, timeFmt)}`
  if (!event.dtend) return startStr
  const end = new Date(event.dtend)
  const sameDay = start.toDateString() === end.toDateString()
  return sameDay
    ? `${startStr} – ${end.toLocaleTimeString(undefined, timeFmt)}`
    : `${startStr} – ${end.toLocaleDateString(undefined, dateFmt)} ${end.toLocaleTimeString(undefined, timeFmt)}`
}

function methodInfo(event: IcalEvent): {
  label: string
  icon: typeof CalendarDays
  className: string
} {
  if (event.method === 'CANCEL' || event.status === 'CANCELLED') {
    return {
      label: 'Cancellation',
      icon: CalendarX2,
      className: 'bg-destructive/15 text-destructive border-destructive/30',
    }
  }
  if (event.method === 'REPLY') {
    return {
      label: 'Response',
      icon: User,
      className: 'bg-secondary text-secondary-foreground border-border',
    }
  }
  if (event.sequence > 0) {
    return {
      label: 'Updated invitation',
      icon: RefreshCw,
      className: 'bg-primary/15 text-primary border-primary/30',
    }
  }
  return {
    label: 'Invitation',
    icon: CalendarDays,
    className: 'bg-primary/15 text-primary border-primary/30',
  }
}

function partstatBadge(partstat?: string): { label: string; className: string } {
  switch (partstat) {
    case 'ACCEPTED':
      return { label: 'accepted', className: 'text-green-500' }
    case 'DECLINED':
      return { label: 'declined', className: 'text-destructive' }
    case 'TENTATIVE':
      return { label: 'tentative', className: 'text-primary' }
    default:
      return { label: 'no reply', className: 'text-muted-foreground' }
  }
}

/**
 * Renders a calendar invitation the way Outlook / Gmail present a
 * text/calendar MIME part: an appointment card above the message body.
 */
export function InviteCard({ events }: { events: IcalEvent[] }) {
  return (
    <div className="flex flex-col gap-2">
      {events.map((event, i) => {
        const info = methodInfo(event)
        const Icon = info.icon
        const cancelled = event.method === 'CANCEL' || event.status === 'CANCELLED'

        return (
          <div
            key={`${event.uid}-${i}`}
            className={cn(
              'rounded-lg border p-3.5',
              cancelled ? 'border-destructive/30 bg-destructive/5' : 'border-primary/25 bg-primary/5'
            )}
          >
            <div className="flex items-center gap-2">
              <Icon
                className={cn('size-4 shrink-0', cancelled ? 'text-destructive' : 'text-primary')}
                aria-hidden="true"
              />
              <Badge variant="outline" className={cn('text-[12px] font-semibold', info.className)}>
                {info.label}
              </Badge>
              {event.sequence > 0 && (
                <span className="text-[12px] tabular-nums text-muted-foreground">
                  seq {event.sequence}
                </span>
              )}
            </div>

            <p
              className={cn(
                'mt-2 text-base font-semibold leading-snug text-balance',
                cancelled && 'line-through decoration-destructive/60'
              )}
            >
              {event.summary || '(untitled event)'}
            </p>
            <p className="mt-0.5 text-sm text-muted-foreground">{formatEventRange(event)}</p>

            {event.location && (
              <p className="mt-1.5 flex items-center gap-1.5 text-sm">
                <MapPin className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                {event.location}
              </p>
            )}

            {event.organizerAddress && (
              <p className="mt-1 flex items-center gap-1.5 text-sm">
                <User className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
                <span className="text-muted-foreground">Organizer:</span>
                <span className="text-xs">
                  {event.organizerName || event.organizerAddress}
                </span>
              </p>
            )}

            {event.attendees.length > 0 && (
              <div className="mt-1 flex items-start gap-1.5 text-sm">
                <Users
                  className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                  aria-hidden="true"
                />
                <div className="flex flex-wrap gap-x-3 gap-y-0.5">
                  {event.attendees.map((att) => {
                    const ps = partstatBadge(att.partstat)
                    return (
                      <span key={att.address} className="text-xs">
                        {att.name || att.address}
                        <span className={cn('ml-1 text-[12px]', ps.className)}>({ps.label})</span>
                      </span>
                    )
                  })}
                </div>
              </div>
            )}

            {event.description && (
              <p className="mt-2 border-t border-border/60 pt-2 text-xs leading-relaxed text-muted-foreground">
                {event.description}
              </p>
            )}
          </div>
        )
      })}
    </div>
  )
}
