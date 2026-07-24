import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { CalendarDays, ChevronLeft, ChevronRight, MapPin, User } from '@/components/ui/icons'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { cn } from '@/lib/utils'
import type { CalendarEvent, Mailbox } from './use-hoomail'

const WEEKDAYS = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

function startOfDay(ts: number): number {
  const d = new Date(ts)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

/** Builds the 6x7 grid of days shown for a month (Monday-first). */
function monthGrid(year: number, month: number): Date[] {
  const first = new Date(year, month, 1)
  // getDay(): 0=Sun … 6=Sat → shift to Monday-first offset
  const offset = (first.getDay() + 6) % 7
  const start = new Date(year, month, 1 - offset)
  return Array.from({ length: 42 }, (_, i) => {
    const d = new Date(start)
    d.setDate(start.getDate() + i)
    return d
  })
}

function eventTime(event: CalendarEvent): string {
  if (event.allDay) return 'All day'
  const fmt: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' }
  const start = new Date(event.dtstart).toLocaleTimeString(undefined, fmt)
  if (!event.dtend) return start
  return `${start} – ${new Date(event.dtend).toLocaleTimeString(undefined, fmt)}`
}

export function CalendarView({
  mailbox,
  events,
  onOpenMessage,
}: {
  mailbox: Mailbox | null
  events: CalendarEvent[]
  onOpenMessage: (messageId: number) => void
}) {
  const today = new Date()
  const [cursor, setCursor] = useState({ year: today.getFullYear(), month: today.getMonth() })
  const [selectedDay, setSelectedDay] = useState<number>(startOfDay(today.getTime()))

  const pendingFocusDay = useRef<number | null>(null)
  const days = useMemo(() => monthGrid(cursor.year, cursor.month), [cursor])

  const eventsByDay = useMemo(() => {
    const map = new Map<number, CalendarEvent[]>()
    for (const event of events) {
      const key = startOfDay(event.dtstart)
      const list = map.get(key) ?? []
      list.push(event)
      map.set(key, list)
    }
    for (const list of map.values()) list.sort((a, b) => a.dtstart - b.dtstart)
    return map
  }, [events])

  useEffect(() => {
    if (pendingFocusDay.current == null) return
    const key = pendingFocusDay.current
    const button = document.querySelector<HTMLButtonElement>(`button[data-calendar-day="${key}"]`)
    if (!button) return
    pendingFocusDay.current = null
    button.focus()
  }, [days])

  const selectedDayEvents = eventsByDay.get(selectedDay) ?? []
  const monthLabel = new Date(cursor.year, cursor.month).toLocaleDateString(undefined, {
    month: 'long',
    year: 'numeric',
  })

  const goMonth = (delta: number) => {
    const selected = new Date(selectedDay)
    const targetMonth = new Date(cursor.year, cursor.month + delta, 1)
    const lastDay = new Date(targetMonth.getFullYear(), targetMonth.getMonth() + 1, 0).getDate()
    const target = new Date(
      targetMonth.getFullYear(),
      targetMonth.getMonth(),
      Math.min(selected.getDate(), lastDay)
    )
    setCursor({ year: target.getFullYear(), month: target.getMonth() })
    setSelectedDay(startOfDay(target.getTime()))
  }

  const moveDayFocus = (currentKey: number, delta: number) => {
    const target = new Date(currentKey)
    target.setDate(target.getDate() + delta)
    const nextKey = startOfDay(target.getTime())
    setSelectedDay(nextKey)

    const nextButton = document.querySelector<HTMLButtonElement>(`button[data-calendar-day="${nextKey}"]`)
    if (nextButton) {
      nextButton.focus()
      return
    }

    pendingFocusDay.current = nextKey
    setCursor({ year: target.getFullYear(), month: target.getMonth() })
  }

  if (!mailbox) {
    return (
      <section
        aria-label="Calendar"
        className="flex min-w-0 flex-1 flex-col items-center justify-center gap-3 bg-background"
      >
        <CalendarDays className="size-7 text-muted-foreground/40" aria-hidden="true" />
        <p className="text-sm text-muted-foreground">Select an inbox to see its calendar.</p>
      </section>
    )
  }

  return (
    <section aria-label="Calendar" className="flex min-w-0 flex-1 flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-5">
        <div className="flex items-center gap-2">
          <CalendarDays className="size-4 text-primary" aria-hidden="true" />
          <h2 className="text-sm font-semibold">{monthLabel}</h2>
          <span className="text-xs tabular-nums text-muted-foreground">
            {events.length} {events.length === 1 ? 'event' : 'events'}
          </span>
        </div>
        <div className="flex items-center gap-1">
          <Button
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-xs"
            onClick={() => {
              const now = new Date()
              setCursor({ year: now.getFullYear(), month: now.getMonth() })
              setSelectedDay(startOfDay(now.getTime()))
            }}
          >
            Today
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="size-7"
            onClick={() => goMonth(-1)}
            aria-label="Previous month"
          >
            <ChevronLeft className="size-4" aria-hidden="true" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            className="size-7"
            onClick={() => goMonth(1)}
            aria-label="Next month"
          >
            <ChevronRight className="size-4" aria-hidden="true" />
          </Button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col">
        <div role="grid" aria-label={monthLabel} className="flex min-h-0 flex-[3] flex-col">
          <div role="row" className="grid shrink-0 grid-cols-7 border-b border-border">
            {WEEKDAYS.map((day) => (
              <div
                key={day}
                role="columnheader"
                className="px-2 py-1.5 text-center text-[12px] font-medium uppercase tracking-wider text-muted-foreground"
              >
                {day}
              </div>
            ))}
          </div>

          <div role="rowgroup" className="grid min-h-0 flex-1 grid-cols-7 grid-rows-6">
            {days.map((day) => {
              const key = startOfDay(day.getTime())
              const dayEvents = eventsByDay.get(key) ?? []
              const inMonth = day.getMonth() === cursor.month
              const isToday = key === startOfDay(Date.now())
              const isSelected = key === selectedDay

              return (
                <button
                  key={key}
                  type="button"
                  role="gridcell"
                  data-calendar-day={key}
                  tabIndex={isSelected ? 0 : -1}
                  onKeyDown={(event) => {
                    const delta = event.key === 'ArrowLeft' ? -1
                      : event.key === 'ArrowRight' ? 1
                        : event.key === 'ArrowUp' ? -7
                          : event.key === 'ArrowDown' ? 7
                            : 0
                    if (delta === 0) return
                    event.preventDefault()
                    moveDayFocus(key, delta)
                  }}
                  onClick={() => setSelectedDay(key)}
                  className={cn(
                    'flex min-h-0 flex-col items-stretch gap-0.5 overflow-hidden border-b border-r border-border/60 p-1 text-left transition-colors',
                    !inMonth && 'bg-muted/30',
                    isSelected ? 'bg-accent' : 'hover:bg-accent/50'
                  )}
                  aria-label={`${day.toLocaleDateString()} — ${dayEvents.length} events`}
                  aria-selected={isSelected}
                >
                  <span
                    className={cn(
                      'flex size-5 shrink-0 items-center justify-center rounded-full text-xs',
                      isToday
                        ? 'bg-primary font-bold text-primary-foreground'
                        : inMonth
                          ? 'text-foreground'
                          : 'text-muted-foreground'
                    )}
                  >
                    {day.getDate()}
                  </span>
                  {dayEvents.slice(0, 2).map((event) => (
                    <span
                      key={event.id}
                      className={cn(
                        'truncate rounded-sm px-1 py-px text-[12px] leading-tight',
                        event.status === 'CANCELLED'
                          ? 'bg-destructive/15 text-destructive line-through'
                          : 'bg-primary/20 text-foreground'
                      )}
                    >
                      {event.summary || '(untitled)'}
                    </span>
                  ))}
                  {dayEvents.length > 2 && (
                    <span className="px-1 text-[11px] text-muted-foreground">
                      +{dayEvents.length - 2} more
                    </span>
                  )}
                </button>
              )
            })}
          </div>
        </div>

        <div className="flex min-h-0 flex-[2] flex-col border-t border-border">
          <div className="shrink-0 px-4 py-2">
            <h2 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
              {new Date(selectedDay).toLocaleDateString(undefined, {
                weekday: 'long',
                month: 'long',
                day: 'numeric',
              })}
            </h2>
          </div>
          <ScrollArea className="min-h-0 flex-1" aria-label="Events for selected day">
            {selectedDayEvents.length === 0 && (
              <p className="px-4 pb-4 text-xs text-muted-foreground">No events on this day.</p>
            )}
            <ul className="flex flex-col gap-2 px-4 pb-4">
              {selectedDayEvents.map((event) => {
                const cancelled = event.status === 'CANCELLED'
                return (
                  <li key={event.id}>
                    <button
                      type="button"
                      onClick={() =>
                        event.lastMessageId != null && onOpenMessage(event.lastMessageId)
                      }
                      disabled={event.lastMessageId == null}
                      aria-label={`${event.summary || 'untitled event'}, ${eventTime(event)}${event.location ? `, ${event.location}` : ''}${event.lastMessageId == null ? ', no source message available' : ''}`}
                      className={cn(
                        'w-full rounded-lg border p-3 text-left transition-colors',
                        cancelled
                          ? 'border-destructive/30 bg-destructive/5 hover:bg-destructive/10'
                          : 'border-border bg-card hover:bg-accent/50'
                      )}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <span
                          className={cn(
                            'min-w-0 flex-1 truncate text-sm font-semibold',
                            cancelled && 'line-through decoration-destructive/60'
                          )}
                        >
                          {event.summary || '(untitled event)'}
                        </span>
                        <Badge
                          variant="outline"
                          className={cn(
                            'shrink-0 text-[11px]',
                            cancelled
                              ? 'border-destructive/30 text-destructive'
                              : event.status === 'TENTATIVE'
                                ? 'border-primary/30 text-primary'
                                : 'border-border text-muted-foreground'
                          )}
                        >
                          {event.status.toLowerCase()}
                        </Badge>
                      </div>
                      <p className="mt-0.5 text-xs text-muted-foreground">{eventTime(event)}</p>
                      {event.location && (
                        <p className="mt-1 flex items-center gap-1 text-xs text-muted-foreground">
                          <MapPin className="size-3 shrink-0" aria-hidden="true" />
                          {event.location}
                        </p>
                      )}
                      {event.organizerAddress && (
                        <p className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
                          <User className="size-3 shrink-0" aria-hidden="true" />
                          {event.organizerName || event.organizerAddress}
                        </p>
                      )}
                    </button>
                  </li>
                )
              })}
            </ul>
          </ScrollArea>
        </div>
      </div>
    </section>
  )
}
