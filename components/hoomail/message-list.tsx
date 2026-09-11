import type { JSX } from 'preact'
import { useAutoAnimate } from '@formkit/auto-animate/preact'
import { getTransitionSizes, type AutoAnimationPlugin } from '@formkit/auto-animate'
import { useRef } from 'preact/hooks'
import { CalendarDays, Mail, MailOpen, Paperclip, Search, Trash2, X } from '@/components/ui/icons'
import { Button } from '@/components/ui/button'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import { AnimatedValue, InlinePresence } from '@/components/ui/reactive'
import { cn } from '@/lib/utils'
import { formatRelativeTime, type Mailbox, type MessageListItem } from './use-hoomail'

/**
 * Open the row's existing context menu from a real, visible touch button.
 * Dispatching the same bubbling event keeps pointer, keyboard, and context
 * menu actions on one path rather than maintaining a second action menu.
 */
function openRowContextMenu(event: JSX.TargetedMouseEvent<HTMLButtonElement>) {
  event.preventDefault()
  event.stopPropagation()
  const trigger = event.currentTarget.closest<HTMLElement>('[data-slot="context-menu-trigger"]')
  if (!trigger) return
  const rect = event.currentTarget.getBoundingClientRect()
  trigger.dispatchEvent(new MouseEvent('contextmenu', {
    bubbles: true,
    cancelable: true,
    clientX: Math.round(rect.left + rect.width / 2),
    clientY: Math.round(rect.top + rect.height / 2),
  }))
}

/**
 * AutoAnimate plugin emitting exact-duration ease-out effects for every action.
 * The library's options path stretches additions to duration * 1.5 with ease-in,
 * and its built-in reduced-motion guard only covers options, not plugins.
 */
function createMotionPlugin(duration: number): AutoAnimationPlugin {
  return (element, action, previousCoords, currentCoords) => {
    if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) {
      return new KeyframeEffect(element, null, { duration: 0 })
    }
    if (action === 'remain') {
      // For "remain" the library passes (oldCoords, newCoords); FLIP back from
      // the previous position, mirroring the library's own remain branch.
      const oldCoords = previousCoords
      const newCoords = currentCoords
      if (!oldCoords || !newCoords) return new KeyframeEffect(element, null, { duration: 0 })
      let deltaLeft = oldCoords.left - newCoords.left
      let deltaTop = oldCoords.top - newCoords.top
      const deltaRight = oldCoords.left + oldCoords.width - (newCoords.left + newCoords.width)
      const deltaBottom = oldCoords.top + oldCoords.height - (newCoords.top + newCoords.height)
      if (deltaBottom === 0) deltaTop = 0
      if (deltaRight === 0) deltaLeft = 0
      const [widthFrom, widthTo, heightFrom, heightTo] = getTransitionSizes(element, oldCoords, newCoords)
      const from: Keyframe = { transform: `translate(${deltaLeft}px, ${deltaTop}px)` }
      const to: Keyframe = { transform: 'translate(0px, 0px)' }
      if (widthFrom !== widthTo) {
        from.width = `${widthFrom}px`
        to.width = `${widthTo}px`
      }
      if (heightFrom !== heightTo) {
        from.height = `${heightFrom}px`
        to.height = `${heightTo}px`
      }
      return new KeyframeEffect(element, [from, to], { duration, easing: 'ease-out' })
    }
    if (action === 'remove') {
      return new KeyframeEffect(
        element,
        [
          { transform: 'scale(1)', opacity: 1 },
          { transform: 'scale(0.98)', opacity: 0 },
        ],
        { duration, easing: 'ease-out' },
      )
    }
    return new KeyframeEffect(
      element,
      [
        { transform: 'scale(0.98)', opacity: 0 },
        { transform: 'scale(1)', opacity: 1 },
      ],
      { duration, easing: 'ease-out' },
    )
  }
}

export function MessageList({
  mailbox,
  messages,
  selectedId,
  selectedIds,
  searchQuery,
  onSearchChange,
  onRowClick,
  onToggleSelection,
  onAction,
}: {
  mailbox: Mailbox | null
  messages: MessageListItem[]
  selectedId: number | null
  selectedIds: Set<number>
  searchQuery: string
  onSearchChange: (q: string) => void
  onRowClick: (id: number, event: JSX.TargetedMouseEvent<HTMLButtonElement>) => void
  onToggleSelection: (id: number) => void
  onAction: (action: 'delete' | 'read' | 'unread', ids: number[]) => void
}) {
  const searchRef = useRef<HTMLInputElement>(null)
  const hasSelection = selectedIds.size > 0
  const multiSelected = selectedIds.size > 1
  const hasSelectedMessage = selectedId != null && messages.some((message) => message.id === selectedId)
  const [messageListRef] = useAutoAnimate<HTMLUListElement>(createMotionPlugin(220))
  const [toolbarRef] = useAutoAnimate<HTMLDivElement>(createMotionPlugin(180))

  /** Ids an action should apply to when triggered from a row's context menu */
  const actionTargets = (rowId: number): number[] =>
    selectedIds.has(rowId) ? [...selectedIds] : [rowId]
 

  return (
    <section
      data-message-list-shell
      tabIndex={-1}
      aria-labelledby="message-list-heading"
      className="flex h-full min-w-0 w-96 shrink-0 flex-col border-r border-border bg-background outline-none"
    >
      <header className="flex h-12 shrink-0 items-center justify-between gap-2 border-b border-border px-4">
        <h2 id="message-list-heading" className="min-w-0 flex-1 truncate text-sm font-medium">
          {mailbox ? mailbox.address : 'No inbox selected'}
        </h2>
        {mailbox && (
          <AnimatedValue
            value={messages.length}
            className="shrink-0 text-xs tabular-nums text-muted-foreground"
          />
        )}
      </header>

      {mailbox && (
        <div className="shrink-0 border-b border-border px-3 py-2">
          <div className="relative">
            <Search
              className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground"
              aria-hidden="true"
            />
            <Input
              ref={searchRef}
              type="search"
              value={searchQuery}
              onInput={(event) => onSearchChange(event.currentTarget.value)}
              placeholder="Search subject, sender, body…"
              className="h-8 min-w-0 pl-8 pr-12 text-sm hoomail-touch-target"
              aria-label="Search messages"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => {
                  onSearchChange('')
                  searchRef.current?.focus()
                }}
                className="hoomail-touch-target absolute right-0 top-1/2 flex -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground transition-colors hover:text-foreground"
                aria-label="Clear search"
              >
                <X className="size-3.5" aria-hidden="true" />
              </button>
            )}
          </div>
        </div>
      )}

      <div ref={toolbarRef}>
        {hasSelection && (
          <div className="border-b border-border bg-accent/50">
            <div className="hoomail-bulk-toolbar flex flex-wrap items-center justify-between gap-2 px-3 py-1.5">
              <span className="text-xs font-medium">{selectedIds.size} selected</span>
              <div className="flex flex-wrap items-center justify-end gap-1">
                <Button
                  size="sm"
                  variant="ghost"
                  className="hoomail-touch-target h-7 px-2 text-xs"
                  onClick={() => onAction('read', [...selectedIds])}
                >
                  <MailOpen className="size-3.5" aria-hidden="true" />
                  Read
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="hoomail-touch-target h-7 px-2 text-xs"
                  onClick={() => onAction('unread', [...selectedIds])}
                >
                  <Mail className="size-3.5" aria-hidden="true" />
                  Unread
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="hoomail-touch-target h-7 px-2 text-xs text-destructive hover:text-destructive"
                  onClick={() => onAction('delete', [...selectedIds])}
                >
                  <Trash2 className="size-3.5" aria-hidden="true" />
                  Delete
                </Button>
              </div>
            </div>
          </div>
        )}
      </div>

      <ScrollArea className="min-h-0 flex-1" aria-label="Messages">
        {!mailbox && (
          <EmptyHint text="Select an inbox on the left, or send an email to any address to create one." />
        )}
        {mailbox && messages.length === 0 && (
          <EmptyHint
            text={
              searchQuery
                ? `No messages match "${searchQuery}".`
                : 'No mail yet — the owls are resting. New messages appear here instantly.'
            }
          />
        )}
        <span role="status" aria-live="polite" aria-atomic="true" className="sr-only">
          {messages.length === 0 ? 'No messages' : `${messages.length} messages`}
        </span>
        <ul ref={messageListRef} data-message-list aria-label="Messages" className="flex flex-col select-none">
          {messages.map((message, index) => {
            const isChecked = selectedIds.has(message.id)
            const relativeTime = formatRelativeTime(message.received_at)
            const isTabStop = selectedId === message.id || (!hasSelectedMessage && index === 0)
            return (
              <li key={message.id} className="min-w-0">
                <ContextMenu>
                  <ContextMenuTrigger className="flex min-w-0 items-stretch">
                    <label
                      className="hoomail-message-select hoomail-touch-target flex shrink-0 cursor-pointer items-center justify-center px-1 lg:hidden"
                      onClick={(event) => event.stopPropagation()}
                    >
                      <input
                        type="checkbox"
                        checked={isChecked}
                        onChange={() => onToggleSelection(message.id)}
                        aria-label={`Select message from ${message.from_name || message.from_address || 'Unknown sender'}, ${message.subject || 'no subject'}`}
                        className="size-4 cursor-pointer rounded border-input accent-primary"
                      />
                    </label>
                    <button
                      type="button"
                      data-message-id={message.id}
                      tabIndex={isTabStop ? 0 : -1}
                      onClick={(event) => onRowClick(message.id, event)}
                      className={cn(
                        'reactive-message flex min-h-11 min-w-0 flex-1 flex-col gap-0.5 border-b border-border/60 px-3 py-3 text-left transition-[background-color,color] duration-200',
                        message.is_read ? 'is-read' : 'is-unread',
                        isChecked
                          ? 'bg-primary/15'
                          : selectedId === message.id
                            ? 'bg-accent'
                            : 'hover:bg-accent/50'
                      )}
                      aria-pressed={isChecked}
                      aria-current={selectedId === message.id ? "true" : undefined}
                      aria-label={`${message.from_name || message.from_address || 'Unknown sender'}, ${message.subject || 'no subject'}, ${message.is_read ? 'read' : 'unread'}, ${relativeTime}`}
                    >
                      <div className="flex min-w-0 items-center">
                        <InlinePresence
                          visible={!message.is_read}
                          className="reactive-unread-dot mr-2 size-2 shrink-0 rounded-full bg-primary"
                        >
                          <span className="sr-only">Unread</span>
                        </InlinePresence>
                        <span
                          className={cn(
                            'min-w-0 flex-1 truncate text-sm',
                            message.is_read ? 'text-muted-foreground' : 'font-semibold'
                          )}
                        >
                          {message.from_name || message.from_address || 'Unknown sender'}
                        </span>
                        <span className="shrink-0 text-xs text-muted-foreground">
                          {relativeTime}
                        </span>
                      </div>
                      <div className="flex min-w-0 items-center gap-1.5">
                        <span
                          data-message-subject
                          className={cn(
                            'min-w-0 flex-1 truncate text-sm',
                            message.is_read ? 'text-muted-foreground' : 'text-foreground'
                          )}
                        >
                          {message.subject || '(no subject)'}
                        </span>
                        {message.has_ical === 1 && (
                          <CalendarDays
                            className="size-3 shrink-0 text-primary"
                            aria-label="Calendar invitation"
                          />
                        )}
                        {message.attachment_count > 0 && (
                          <Paperclip
                            className="size-3 shrink-0 text-muted-foreground"
                            aria-label={`${message.attachment_count} attachments`}
                          />
                        )}
                      </div>
                      <p className="truncate text-xs text-muted-foreground">
                        {message.snippet}
                      </p>
                    </button>
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      data-message-actions
                      aria-haspopup="menu"
                      aria-label={`Message actions for ${message.from_name || message.from_address || 'Unknown sender'}, ${message.subject || 'no subject'}`}
                      className="hoomail-touch-target min-w-11 shrink-0 self-stretch rounded-none border-b border-border/60 px-2 text-xs lg:hidden"
                      onClick={openRowContextMenu}
                    >
                      Actions
                    </Button>
                  </ContextMenuTrigger>
                  <ContextMenuContent className="w-52">
                    <ContextMenuItem
                      onClick={() => onAction('read', actionTargets(message.id))}
                    >
                      <MailOpen className="size-4" aria-hidden="true" />
                      Mark as read
                    </ContextMenuItem>
                    <ContextMenuItem
                      onClick={() => onAction('unread', actionTargets(message.id))}
                    >
                      <Mail className="size-4" aria-hidden="true" />
                      Mark as unread
                    </ContextMenuItem>
                    <ContextMenuSeparator />
                    <ContextMenuItem
                      variant="destructive"
                      onClick={() => onAction('delete', actionTargets(message.id))}
                    >
                      <Trash2 className="size-4" aria-hidden="true" />
                      {selectedIds.has(message.id) && multiSelected
                        ? `Delete ${selectedIds.size} messages`
                        : 'Delete'}
                    </ContextMenuItem>
                  </ContextMenuContent>
                </ContextMenu>
              </li>
            )
          })}
        </ul>
      </ScrollArea>

      <footer className="shrink-0 border-t border-border px-3 py-1.5">
        <p className="text-[12px] leading-relaxed text-muted-foreground">
          Click to open · Select messages for bulk actions · Use Actions or right-click for message actions
        </p>
      </footer>
    </section>
  )
}

function EmptyHint({ text }: { text: string }) {
  return (
    <div className="flex flex-col items-center gap-3 px-8 py-16 text-center">
      <Mail className="size-7 text-muted-foreground/40" aria-hidden="true" />
      <p className="text-xs leading-relaxed text-muted-foreground text-pretty">{text}</p>
    </div>
  )
}
