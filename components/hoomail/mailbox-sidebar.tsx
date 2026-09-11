import { useAutoAnimate } from '@formkit/auto-animate/preact'
import { getTransitionSizes, type AutoAnimationPlugin } from '@formkit/auto-animate'
import { ChevronLeft, Inbox, RotateCcw, Send, Trash2 } from '@/components/ui/icons'
import { Button } from '@/components/ui/button'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import { ScrollArea } from '@/components/ui/scroll-area'
import { AnimatedValue, InlinePresence } from '@/components/ui/reactive'
import { cn } from '@/lib/utils'
import { formatRelativeTime, type Mailbox } from './use-hoomail'

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

export function MailboxSidebar({
  mailboxes,
  selectedId,
  onSelect,
  onDelete,
  onOpenSendTest,
  onOpenReset,
  onBack,
}: {
  mailboxes: Mailbox[]
  selectedId: number | null
  onSelect: (id: number) => void
  onDelete: (id: number) => void
  onOpenSendTest: () => void
  onOpenReset: () => void
  onBack?: () => void
}) {
  const [mailboxListRef] = useAutoAnimate<HTMLElement>(createMotionPlugin(220))

  const deleteMailboxAndRestoreFocus = (id: number, index: number) => {
    const focusTargetId = mailboxes[index + 1]?.id ?? mailboxes[index - 1]?.id
    void Promise.resolve(onDelete(id)).then(() => requestAnimationFrame(() => {
      const focusTarget = focusTargetId == null
        ? document.querySelector<HTMLButtonElement>('[data-mailbox-fallback]')
        : document.querySelector<HTMLButtonElement>(`[data-mailbox-id="${focusTargetId}"] [data-mailbox-select]`)
      focusTarget?.focus()
    }))
  }
  return (
    <aside
      data-mailbox-sidebar
      aria-labelledby="inboxes-heading"
      className="flex h-full min-w-0 w-72 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground"
    >
      <header className="flex min-w-0 items-center gap-2.5 border-b border-sidebar-border px-4 py-3.5">
        <img
          src="/hoomail-logo.png"
          alt="hoomail owl logo"
          width={30}
          height={30}
          className="shrink-0 rounded-md"
        />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="text-base font-semibold leading-tight tracking-tight">hoomail</span>
          <span className="text-xs text-muted-foreground leading-tight">email testing inbox</span>
        </div>
        {onBack && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            data-mobile-inboxes-back
            className="hoomail-touch-target shrink-0 px-2 text-xs lg:hidden"
            onClick={onBack}
            aria-label="Back to mail"
          >
            <ChevronLeft className="size-4" aria-hidden="true" />
            Back
          </Button>
        )}
      </header>
      <div className="flex items-center justify-between px-4 pt-3 pb-1.5">
        <h2 id="inboxes-heading" className="text-xs font-medium uppercase tracking-wider text-muted-foreground">
          Inboxes
        </h2>
        <AnimatedValue value={mailboxes.length} className="text-xs tabular-nums text-muted-foreground" />
      </div>

      <ScrollArea className="min-h-0 flex-1" aria-label="Inboxes">
        <nav ref={mailboxListRef} aria-label="Inboxes" className="flex flex-col gap-0.5 px-2 pb-2">
          {mailboxes.length === 0 && (
            <div className="flex flex-col items-center gap-2 px-4 py-10 text-center">
              <Inbox className="size-6 text-muted-foreground/50" aria-hidden="true" />
              <p className="text-xs leading-relaxed text-muted-foreground">
                No inboxes yet. Send an email to any address and its inbox appears here automatically.
              </p>
            </div>
          )}
          {mailboxes.map((mailbox, index) => (
            <div key={mailbox.id} data-mailbox-id={mailbox.id} className="min-w-0">
              <ContextMenu>
                <ContextMenuTrigger className="min-w-0">
                  <div className="group flex min-w-0 items-stretch gap-1">
                    <button
                      type="button"
                      data-mailbox-select
                      aria-current={selectedId === mailbox.id ? "true" : undefined}
                      aria-label={`${mailbox.address}, ${mailbox.total_count} messages, ${mailbox.unread_count} unread`}
                      onClick={() => onSelect(mailbox.id)}
                      className={cn(
                        'flex min-h-11 min-w-0 flex-1 items-center gap-2 overflow-hidden rounded-md px-2.5 text-left transition-colors',
                        selectedId === mailbox.id
                          ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                          : 'hover:bg-sidebar-accent/60'
                      )}
                    >
                      <div className="min-w-0 flex-1 py-2">
                        <p className="truncate text-sm font-medium leading-snug">{mailbox.address}</p>
                        <p className="text-xs text-muted-foreground">
                          <AnimatedValue value={mailbox.total_count} />{' '}
                          {mailbox.last_message_at
                            ? ` · ${formatRelativeTime(mailbox.last_message_at)}`
                            : ''}
                        </p>
                      </div>
                      <InlinePresence
                        visible={mailbox.unread_count > 0}
                        className="reactive-badge flex size-5 shrink-0 items-center justify-center rounded-full bg-primary text-[12px] font-bold tabular-nums text-primary-foreground"
                      >
                        <AnimatedValue value={mailbox.unread_count > 99 ? '99' : mailbox.unread_count} />
                        <span className="sr-only">{mailbox.unread_count} unread</span>
                      </InlinePresence>
                    </button>
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      data-mailbox-delete
                      aria-label={`Delete inbox ${mailbox.address}`}
                      className="hoomail-touch-target shrink-0 self-stretch text-destructive hover:text-destructive lg:hidden"
                      onClick={(event) => {
                        event.preventDefault()
                        event.stopPropagation()
                        deleteMailboxAndRestoreFocus(mailbox.id, index)
                      }}
                    >
                      <Trash2 className="size-4" aria-hidden="true" />
                    </Button>
                  </div>
                </ContextMenuTrigger>
                <ContextMenuContent className="w-52">
                  <ContextMenuItem
                    variant="destructive"
                    onClick={() => deleteMailboxAndRestoreFocus(mailbox.id, index)}
                  >
                    <Trash2 className="size-4" aria-hidden="true" />
                    Delete inbox
                  </ContextMenuItem>
                </ContextMenuContent>
              </ContextMenu>
            </div>
          ))}
        </nav>
      </ScrollArea>

      <div className="border-t border-sidebar-border p-3">
        <div className="flex gap-2">
          <Button data-mailbox-fallback size="sm" className="hoomail-touch-target min-w-0 flex-1" onClick={onOpenSendTest}>
            <Send className="size-3.5" aria-hidden="true" />
            Send test
          </Button>
          <Button size="sm" variant="outline" className="hoomail-touch-target min-w-0 flex-1" onClick={onOpenReset}>
            <RotateCcw className="size-3.5" aria-hidden="true" />
            Reset
          </Button>
        </div>
      </div>
    </aside>
  )
}
