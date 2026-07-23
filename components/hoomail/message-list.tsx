import type { JSX } from 'preact'
import { useAutoAnimate } from '@formkit/auto-animate/preact'
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

export function MessageList({
  mailbox,
  messages,
  selectedId,
  selectedIds,
  searchQuery,
  onSearchChange,
  onRowClick,
  onAction,
}: {
  mailbox: Mailbox | null
  messages: MessageListItem[]
  selectedId: number | null
  selectedIds: Set<number>
  searchQuery: string
  onSearchChange: (q: string) => void
  onRowClick: (id: number, event: JSX.TargetedMouseEvent<HTMLButtonElement>) => void
  onAction: (action: 'delete' | 'read' | 'unread', ids: number[]) => void
}) {
  const searchRef = useRef<HTMLInputElement>(null)
  const multiSelected = selectedIds.size > 1
  const hasSelectedMessage = selectedId != null && messages.some((message) => message.id === selectedId)
  const [messageListRef] = useAutoAnimate<HTMLUListElement>({ duration: 180, easing: 'ease-out' })
  const [toolbarRef] = useAutoAnimate<HTMLDivElement>({ duration: 160, easing: 'ease-out' })

  /** Ids an action should apply to when triggered from a row's context menu */
  const actionTargets = (rowId: number): number[] =>
    selectedIds.has(rowId) ? [...selectedIds] : [rowId]

  return (
    <section
      aria-labelledby="message-list-heading"
      className="flex h-full w-96 shrink-0 flex-col border-r border-border bg-background"
    >
      <header className="flex h-12 shrink-0 items-center justify-between gap-2 border-b border-border px-4">
        <h2 id="message-list-heading" className="min-w-0 flex-1 truncate font-mono text-sm">
          {mailbox ? mailbox.address : 'No inbox selected'}
        </h2>
        {mailbox && (
          <AnimatedValue
            value={messages.length}
            className="shrink-0 font-mono text-[11px] text-muted-foreground"
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
              className="h-8 pl-8 pr-8 text-[13px]"
              aria-label="Search messages"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => {
                  onSearchChange('')
                  searchRef.current?.focus()
                }}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground"
                aria-label="Clear search"
              >
                <X className="size-3.5" aria-hidden="true" />
              </button>
            )}
          </div>
        </div>
      )}

      <div ref={toolbarRef}>
        {multiSelected && (
          <div className="border-b border-border bg-accent/50">
            <div className="flex items-center justify-between gap-2 px-3 py-1.5">
              <span className="text-xs font-medium">{selectedIds.size} selected</span>
              <div className="flex items-center gap-1">
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs"
                  onClick={() => onAction('read', [...selectedIds])}
                >
                  <MailOpen className="size-3.5" aria-hidden="true" />
                  Read
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs"
                  onClick={() => onAction('unread', [...selectedIds])}
                >
                  <Mail className="size-3.5" aria-hidden="true" />
                  Unread
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 px-2 text-xs text-destructive hover:text-destructive"
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
              <li key={message.id}>
                  <ContextMenu>
                    <ContextMenuTrigger>
                      <button
                        type="button"
                        data-message-id={message.id}
                        tabIndex={isTabStop ? 0 : -1}
                        onClick={(event) => onRowClick(message.id, event)}
                        className={cn(
                          'reactive-message flex w-full flex-col gap-0.5 border-b border-border/60 px-4 py-3 text-left transition-[background-color,color] duration-200',
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
                        <div className="flex items-center">
                          <InlinePresence
                            visible={!message.is_read}
                            className="reactive-unread-dot mr-2 size-2 shrink-0 rounded-full bg-primary"
                          >
                            <span className="sr-only">Unread</span>
                          </InlinePresence>
                          <span
                            className={cn(
                              'min-w-0 flex-1 truncate text-[13px]',
                              message.is_read ? 'text-muted-foreground' : 'font-semibold'
                            )}
                          >
                            {message.from_name || message.from_address || 'Unknown sender'}
                          </span>
                          <span className="shrink-0 text-[11px] text-muted-foreground">
                            {relativeTime}
                          </span>
                        </div>
                        <div className="flex items-center gap-1.5">
                          <span
                            className={cn(
                              'min-w-0 flex-1 truncate text-[13px]',
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
                        <p className="truncate text-xs text-muted-foreground/80">
                          {message.snippet}
                        </p>
                      </button>
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

      <footer className="shrink-0 border-t border-border px-4 py-1.5">
        <p className="text-[10px] leading-relaxed text-muted-foreground">
          Click to open · Shift+Click to select a range · Ctrl/Cmd+Click to toggle · Right-click for actions
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
