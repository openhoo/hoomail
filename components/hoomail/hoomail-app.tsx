import type { JSX } from 'preact'
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { CalendarDays, ChevronLeft, Inbox, Mail } from '@/components/ui/icons'
import { Button } from '@/components/ui/button'
import { asyncComponent } from '@/components/ui/async-component'
import { MailboxSidebar } from './mailbox-sidebar'
import { MessageList } from './message-list'
import { MessageViewer } from './message-viewer'
import {
  deleteMailboxRequest,
  refreshAfterRead,
  runMessageAction,
  useCalendarEvents,
  useMailboxes,
  useMessage,
  useMessages,
  useRealtime,
  mutateCache,
  type Mailbox,
  type MessageListItem,
} from './use-hoomail'

const CalendarView = asyncComponent(
  () => import('./calendar-view').then((module) => module.CalendarView),
  <div role="status" className="flex min-w-0 flex-1 items-center justify-center text-sm text-muted-foreground">Loading calendar…</div>,
)
const SendTestDialog = asyncComponent(() => import('./dialogs').then((module) => module.SendTestDialog))
const ResetDialog = asyncComponent(() => import('./dialogs').then((module) => module.ResetDialog))


type MobilePane = 'inboxes' | 'list' | 'reader' | 'calendar'

function focusVisible(selector: string) {
  requestAnimationFrame(() => {
    const target = [...document.querySelectorAll<HTMLElement>(selector)]
      .find((element) => element.offsetParent !== null)
    target?.focus({ preventScroll: true })
  })
}

export function HoomailApp() {
  const [selectedMailboxId, setSelectedMailboxId] = useState<number | null>(null)
  const [selectedMessageId, setSelectedMessageId] = useState<number | null>(null)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [searchQuery, setSearchQuery] = useState('')
  const [view, setView] = useState<'mail' | 'calendar'>('mail')
  const [mobilePane, setMobilePane] = useState<MobilePane>('list')
  const [sendTestOpen, setSendTestOpen] = useState(false)
  const [resetOpen, setResetOpen] = useState(false)
  const anchorIdRef = useRef<number | null>(null)
  const pendingMessageFocusRef = useRef<number | null>(null)
  const pendingListFocusRef = useRef(false)
  const mobileReturnPaneRef = useRef<MobilePane>('list')
  const selectedMessageIdRef = useRef<number | null>(selectedMessageId)
  selectedMessageIdRef.current = selectedMessageId

  const { mailboxes } = useMailboxes()
  const { messages } = useMessages(selectedMailboxId, searchQuery)
  const { detail, isLoading: messageLoading, error: messageError } = useMessage(selectedMessageId)
  const { events } = useCalendarEvents(selectedMailboxId, view === 'calendar')
  const openMessageStateRef = useRef({ messages, selectedMailboxId })
  openMessageStateRef.current = { messages, selectedMailboxId }
  const actionStateRef = useRef({ messages, selectedMailboxId, selectedMessageId, searchQuery, mobilePane })
  actionStateRef.current = { messages, selectedMailboxId, selectedMessageId, searchQuery, mobilePane }

  // Auto-select the first mailbox when none is selected
  useEffect(() => {
    if (selectedMailboxId == null && mailboxes.length > 0) {
      mutateCache(`/api/mailboxes/${mailboxes[0].id}/messages`)
      setSelectedMailboxId(mailboxes[0].id)
    }
  }, [mailboxes, selectedMailboxId])

  // If the selected mailbox disappeared (e.g. after reset), clear selection
  useEffect(() => {
    if (selectedMailboxId != null) {
      if (!mailboxes.some((m) => m.id === selectedMailboxId)) {
        setSelectedMailboxId(null)
        setSelectedMessageId(null)
        setSelectedIds(new Set())
      }
    }
  }, [mailboxes, selectedMailboxId])

  const handleReset = useCallback(() => {
    setSelectedMailboxId(null)
    setSelectedMessageId(null)
    setSelectedIds(new Set())
    setSearchQuery('')
    setMobilePane('inboxes')
    pendingMessageFocusRef.current = null
    pendingListFocusRef.current = false
  }, [])

  useRealtime({
    selectedMailboxId,
    onReset: handleReset,
    onMailboxDeleted: (mailboxId) => {
      if (selectedMailboxId === mailboxId) {
        setSelectedMailboxId(null)
        setSelectedMessageId(null)
        setSelectedIds(new Set())
        setMobilePane('inboxes')
        mobileReturnPaneRef.current = 'list'
      }
    },
  })

  // The sidebar stays interactive while a deletion is pending, so long-lived
  // handlers must compare against the latest selection, not the captured one.
  const selectedMailboxIdRef = useRef(selectedMailboxId)
  selectedMailboxIdRef.current = selectedMailboxId

  const handleDeleteMailbox = async (id: number) => {
    const ok = await deleteMailboxRequest(id)
    if (!ok) return
    if (selectedMailboxIdRef.current === id) {
      setSelectedMailboxId(null)
      setSelectedMessageId(null)
      setSelectedIds(new Set())
      setMobilePane('inboxes')
      mobileReturnPaneRef.current = 'list'
      pendingMessageFocusRef.current = null
      pendingListFocusRef.current = false
    }
    mutateCache('/api/mailboxes')
  }

  const selectMailbox = (id: number) => {
    mutateCache(`/api/mailboxes/${id}/messages`)
    setSelectedMailboxId(id)
    setSelectedMessageId(null)
    setSelectedIds(new Set())
    setSearchQuery('')
    anchorIdRef.current = null
    if (view === 'calendar') {
      setMobilePane('calendar')
      mobileReturnPaneRef.current = 'calendar'
    } else {
      setMobilePane('list')
      pendingMessageFocusRef.current = null
      pendingListFocusRef.current = window.matchMedia('(max-width: 1023px)').matches
      mobileReturnPaneRef.current = 'list'
    }
  }

  const openInboxes = () => {
    mobileReturnPaneRef.current = mobilePane === 'calendar' ? 'calendar' : 'list'
    setMobilePane('inboxes')
    focusVisible('[data-mobile-inboxes-back]')
  }

  const closeInboxes = () => {
    const destination = mobileReturnPaneRef.current
    setMobilePane(destination)
    if (destination === 'list') {
      pendingMessageFocusRef.current = selectedMessageIdRef.current
      pendingListFocusRef.current = true
    } else if (destination === 'reader') {
      focusVisible('[data-mobile-reader-back]')
    } else if (destination === 'calendar') {
      focusVisible('[data-mobile-calendar-back], [data-calendar-pane-focus]')
    }
  }

  const returnToInbox = () => {
    pendingMessageFocusRef.current = selectedMessageIdRef.current ?? messages[0]?.id ?? null
    pendingListFocusRef.current = true
    setView('mail')
    setMobilePane('list')
  }

  const handleSearchChange = (query: string) => {
    setSearchQuery(query)
    if (query.trim() === '' && selectedMailboxId != null) {
      mutateCache(`/api/mailboxes/${selectedMailboxId}/messages`)
    }
    // Filtering changes row indices, so a kept selection would be misleading
    setSelectedIds(new Set())
    anchorIdRef.current = null
  }

  const openMessage = useCallback((id: number) => {
    const { messages, selectedMailboxId } = openMessageStateRef.current
    setView('mail')
    setMobilePane('reader')
    pendingListFocusRef.current = false
    setSelectedMessageId(id)
    setSelectedIds(new Set())
    anchorIdRef.current = id
    if (window.matchMedia('(max-width: 1023px)').matches) {
      focusVisible('[data-mobile-reader-back], [data-reader-pane-focus]')
    }

    const current = messages.find((message) => message.id === id)
    if (!current || current.is_read !== 0 || selectedMailboxId == null) return

    // Detail responses can be cached, so opening a message cannot rely on the
    // GET endpoint's mark-read side effect. Patch the existing row immediately
    // and persist the transition explicitly.
    mutateCache<{ messages: MessageListItem[] }>(
      (key) => key.startsWith(`/api/mailboxes/${selectedMailboxId}/messages`),
      (data) => data
        ? {
            messages: data.messages.map((message) =>
              message.id === id ? { ...message, is_read: 1 } : message
            ),
          }
        : data,
      false
    )
    mutateCache<{ mailboxes: Mailbox[] }>(
      '/api/mailboxes',
      (data) => data
        ? {
            mailboxes: data.mailboxes.map((mailbox) =>
              mailbox.id === selectedMailboxId
                ? { ...mailbox, unread_count: Math.max(0, mailbox.unread_count - 1) }
                : mailbox
            ),
          }
        : data,
      false
    )

    void runMessageAction('read', [id])
      .catch(() => false)
      .then((ok) => {
        if (!ok) refreshAfterRead(selectedMailboxId)
      })
  }, [])

  useEffect(() => {
    const id = pendingMessageFocusRef.current
    if (view !== 'mail') return
    if (!pendingListFocusRef.current && id == null) return

    const rows = [...document.querySelectorAll<HTMLButtonElement>(
      'button.reactive-message[data-message-id]'
    )].filter((row) => row.offsetParent !== null)
    const row = (id == null ? null : rows.find((candidate) => candidate.dataset.messageId === String(id)))
      ?? rows[0]
    if (row) {
      pendingMessageFocusRef.current = null
      pendingListFocusRef.current = false
      row.focus({ preventScroll: true })
      row.scrollIntoView({ block: 'nearest' })
      return
    }

    const listShell = [...document.querySelectorAll<HTMLElement>('[data-message-list-shell]')]
      .find((element) => element.offsetParent !== null)
    if (!listShell) return
    pendingMessageFocusRef.current = null
    pendingListFocusRef.current = false
    listShell.focus({ preventScroll: true })
  }, [messages, view, mobilePane])

  const toggleMessageSelection = (id: number) => {
    setSelectedIds((previous) => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
    anchorIdRef.current = id
  }

  /** Click / Shift+Click / Ctrl+Click semantics like a desktop mail client */
  const handleRowClick = (id: number, event: JSX.TargetedMouseEvent<HTMLButtonElement>) => {
    if (event.shiftKey && anchorIdRef.current != null) {
      const ids = messages.map((m) => m.id)
      const anchorIndex = ids.indexOf(anchorIdRef.current)
      const targetIndex = ids.indexOf(id)
      if (anchorIndex !== -1 && targetIndex !== -1) {
        const [from, to] = [Math.min(anchorIndex, targetIndex), Math.max(anchorIndex, targetIndex)]
        setSelectedIds(new Set(ids.slice(from, to + 1)))
        return
      }
    }
    if (event.ctrlKey || event.metaKey) {
      setSelectedIds((prev) => {
        const next = new Set(prev)
        // Seed the toggle set with the currently open message
        if (next.size === 0 && selectedMessageId != null && selectedMessageId !== id) {
          next.add(selectedMessageId)
        }
        if (next.has(id)) next.delete(id)
        else next.add(id)
        return next
      })
      anchorIdRef.current = id
      return
    }
    openMessage(id)
  }

  const handleAction = useCallback(async (action: 'delete' | 'read' | 'unread', ids: number[]) => {
    const { messages, selectedMailboxId, selectedMessageId, searchQuery, mobilePane } = actionStateRef.current
    const actionContext = { mailboxId: selectedMailboxId, searchQuery: searchQuery.trim(), ids: new Set(ids) }
    // Optimistic delete: drop the rows from the cache immediately so the
    // exit animation starts right away instead of after the round-trip
    if (action === 'delete' && selectedMailboxId != null) {
      const idSet = new Set(ids)
      const survivingMessages = messages.filter((message) => !idSet.has(message.id))
      const focusedMessageId = Number(
        (document.activeElement as HTMLElement | null)?.closest<HTMLButtonElement>('[data-message-id]')?.dataset.messageId
      )
      const deletedIndex = messages.findIndex((message) =>
        message.id === (Number.isInteger(focusedMessageId) ? focusedMessageId : selectedMessageId)
      )
      const focusTarget = survivingMessages[Math.min(Math.max(0, deletedIndex), survivingMessages.length - 1)]
      pendingMessageFocusRef.current = focusTarget?.id ?? null
      if (mobilePane === 'list') pendingListFocusRef.current = true
      mutateCache<{ messages: MessageListItem[] }>(
        (key) => key.startsWith(`/api/mailboxes/${selectedMailboxId}/messages`),
        (data) => data ? { messages: data.messages.filter((message) => !idSet.has(message.id)) } : data,
        false
      )
      if (selectedMessageId != null && idSet.has(selectedMessageId)) {
        setSelectedMessageId(focusTarget?.id ?? null)
      }
      anchorIdRef.current = focusTarget?.id ?? null
      setSelectedIds(new Set())
    }

    const ok = await runMessageAction(action, ids).catch(() => false)
    if (!ok) {
      // Revert the optimistic update by revalidating from the server
      if (selectedMailboxId != null) refreshAfterRead(selectedMailboxId)
      return
    }
    const currentContext = actionStateRef.current
    if (
      currentContext.selectedMailboxId === actionContext.mailboxId &&
      currentContext.searchQuery.trim() === actionContext.searchQuery
    ) {
      setSelectedIds((current) => {
        const next = new Set(current)
        for (const id of actionContext.ids) next.delete(id)
        return next
      })
    }
    // SSE 'messages:changed' refreshes lists; this covers latency gaps
    if (actionContext.mailboxId != null) refreshAfterRead(actionContext.mailboxId)
  }, [])

  const openMessageFromCalendar = (messageId: number) => {
    const isMobile = window.matchMedia('(max-width: 1023px)').matches
    pendingMessageFocusRef.current = isMobile ? null : messageId
    pendingListFocusRef.current = false
    setView('mail')
    setMobilePane('reader')
    openMessage(messageId)
    if (isMobile) focusVisible('[data-mobile-reader-back], [data-reader-pane-focus]')
  }

  const closeMessage = () => {
    const id = selectedMessageIdRef.current
    pendingMessageFocusRef.current = id
    pendingListFocusRef.current = true
    setSelectedMessageId(null)
    setSelectedIds(new Set())
    setMobilePane('list')
    anchorIdRef.current = id
  }
  // Keyboard navigation: arrows move through the list, Delete removes,
  // Ctrl/Cmd+A selects all, Escape clears the multi-selection.
  // Refs avoid stale closures inside the long-lived keydown listener.
  const keyboardStateRef = useRef({ messages, selectedMessageId, selectedIds, view })
  keyboardStateRef.current = { messages, selectedMessageId, selectedIds, view }
  const handleActionRef = useRef(handleAction)
  handleActionRef.current = handleAction

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      const { messages, selectedMessageId, selectedIds, view } = keyboardStateRef.current
      if (view !== 'mail') return

      const target = event.target as HTMLElement | null
      if (
        target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.isContentEditable)
      ) {
        // Allow Escape to blur the search field; ignore everything else
        if (event.key === 'Escape') target.blur()
        return
      }

      const messageList = target?.closest('[data-message-list]')
      if (!messageList) return

      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        if (messages.length === 0) return

        const currentRow = (document.activeElement as HTMLElement | null)?.closest<HTMLButtonElement>(
          'button.reactive-message[data-message-id]'
        )
        const currentId = Number(currentRow?.dataset.messageId)
        const currentIndex = Number.isInteger(currentId)
          ? messages.findIndex((message) => message.id === currentId)
          : selectedMessageId == null
            ? -1
            : messages.findIndex((message) => message.id === selectedMessageId)
        const nextIndex = currentIndex < 0
          ? event.key === 'ArrowDown' ? 0 : messages.length - 1
          : Math.min(
              messages.length - 1,
              Math.max(0, currentIndex + (event.key === 'ArrowDown' ? 1 : -1))
            )
        const nextId = messages[nextIndex]?.id
        const nextRow = messageList.querySelector<HTMLButtonElement>(
          `button.reactive-message[data-message-id="${nextId}"]`
        )
        if (!nextRow || nextId == null) return

        // Move browser focus before updating application state. This keeps the
        // native focus ring, Enter activation, and selection on one row even
        // during rapid key-repeat events.
        nextRow.focus()
        nextRow.scrollIntoView({ block: 'nearest' })
        if (nextId !== selectedMessageId) {
          if (window.matchMedia('(max-width: 1023px)').matches) {
            setSelectedMessageId(nextId)
            anchorIdRef.current = nextId
          } else {
            openMessage(nextId)
          }
        }
        return
      }

      if (event.key === 'Delete' || event.key === 'Backspace') {
        let targets: number[] = []
        if (selectedIds.size > 0) {
          targets = [...selectedIds]
        } else {
          // Under an active search the roving tab stop can sit on a visible
          // row other than the hidden opened message; delete what is focused.
          const focusedId = Number(
            (document.activeElement as HTMLElement | null)?.closest<HTMLButtonElement>(
              'button.reactive-message[data-message-id]'
            )?.dataset.messageId
          )
          if (Number.isInteger(focusedId)) targets = [focusedId]
          else if (selectedMessageId != null) targets = [selectedMessageId]
        }
        if (targets.length > 0) {
          event.preventDefault()
          handleActionRef.current('delete', targets)
        }
        return
      }

      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a') {
        event.preventDefault()
        setSelectedIds(new Set(messages.map((m) => m.id)))
        return
      }

      if (event.key === 'Escape' && selectedIds.size > 0) {
        setSelectedIds(new Set())
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const selectedMailbox = mailboxes.find((m) => m.id === selectedMailboxId) ?? null

  // The detail cache deliberately retains the previously opened message while
  // the next request is in flight. Keep rendering it so the viewer shell and
  // iframe remain mounted; only replace their content when the new detail
  // arrives.
  const displayedDetail = detail

  return (
    <main data-hoomail-shell className="hoomail-shell flex h-dvh min-h-0 min-w-0 max-w-full overflow-hidden bg-background text-foreground">
      <h1 className="sr-only">Hoomail email testing inbox</h1>
      <div
        id="inboxes-pane"
        data-mobile-pane="inboxes"
        data-mobile-active={mobilePane === 'inboxes' ? 'true' : 'false'}
        className="hoomail-sidebar-pane h-full"
      >
        <MailboxSidebar
          mailboxes={mailboxes}
          selectedId={selectedMailboxId}
          onSelect={selectMailbox}
          onDelete={handleDeleteMailbox}
          onOpenSendTest={() => setSendTestOpen(true)}
          onOpenReset={() => setResetOpen(true)}
          onBack={closeInboxes}
        />
      </div>
      <div
        data-mobile-content-active={mobilePane === 'inboxes' ? 'false' : 'true'}
        className="hoomail-content flex min-w-0 flex-1 flex-col"
      >
        <nav aria-label="Primary views" className="hoomail-view-nav flex min-h-10 shrink-0 items-center gap-1 border-b border-border px-3">
          <Button
            size="sm"
            variant={mobilePane === 'inboxes' ? 'secondary' : 'ghost'}
            data-mobile-inboxes
            aria-controls="inboxes-pane"
            aria-expanded={mobilePane === 'inboxes'}
            aria-label="Inboxes"
            className="hoomail-touch-target px-2.5 text-xs lg:hidden"
            onClick={openInboxes}
          >
            <Inbox className="size-3.5" aria-hidden="true" />
            Inboxes
          </Button>
          <Button
            size="sm"
            variant={view === 'mail' ? 'secondary' : 'ghost'}
            className="hoomail-touch-target px-2.5 text-xs"
            onClick={() => {
              pendingMessageFocusRef.current = selectedMessageIdRef.current ?? messages[0]?.id ?? null
              pendingListFocusRef.current = true
              setView('mail')
              setMobilePane('list')
            }}
            aria-pressed={view === 'mail'}
          >
            <Mail className="size-3.5" aria-hidden="true" />
            Mail
          </Button>
          <Button
            size="sm"
            variant={view === 'calendar' ? 'secondary' : 'ghost'}
            className="hoomail-touch-target px-2.5 text-xs"
            onClick={() => {
              pendingMessageFocusRef.current = null
              pendingListFocusRef.current = false
              if (selectedMailboxId != null) mutateCache(`/api/mailboxes/${selectedMailboxId}/events`)
              setView('calendar')
              setMobilePane('calendar')
              focusVisible('[data-mobile-calendar-back], [data-calendar-pane-focus]')
            }}
            aria-pressed={view === 'calendar'}
          >
            <CalendarDays className="size-3.5" aria-hidden="true" />
            Calendar
          </Button>
        </nav>
        <div className="hoomail-workspace flex min-h-0 min-w-0 flex-1">
          <div
            data-view-pane="mail"
            hidden={view !== 'mail'}
            aria-hidden={view === 'mail' ? undefined : 'true'}
            className="hoomail-mail-panes flex min-h-0 min-w-0 flex-1"
          >
            <div
              data-mobile-pane="list"
              data-mobile-active={mobilePane === 'list' ? 'true' : 'false'}
              className="hoomail-list-pane flex min-h-0 min-w-0 shrink-0"
            >
              <MessageList
                mailbox={selectedMailbox}
                messages={messages}
                selectedId={selectedMessageId}
                selectedIds={selectedIds}
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                onRowClick={handleRowClick}
                onToggleSelection={toggleMessageSelection}
                onAction={handleAction}
              />
            </div>
            <div
              data-mobile-pane="reader"
              data-mobile-active={mobilePane === 'reader' ? 'true' : 'false'}
              className="hoomail-reader-pane flex min-h-0 min-w-0 flex-1 flex-col"
            >
              <div className="hoomail-reader-backbar shrink-0 border-b border-border px-3 py-1 lg:hidden">
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  data-mobile-reader-back
                  className="hoomail-touch-target px-2 text-xs"
                  onClick={closeMessage}
                  aria-label="Back to messages"
                >
                  <ChevronLeft className="size-4" aria-hidden="true" />
                  Back to messages
                </Button>
              </div>
              <div data-reader-pane-focus tabIndex={-1} className="hoomail-reader-content flex min-h-0 min-w-0 flex-1 outline-none">
                <MessageViewer
                  message={displayedDetail?.message ?? null}
                  attachments={displayedDetail?.attachments ?? []}
                  selectedMessageId={selectedMessageId}
                  isLoading={messageLoading}
                  detailError={messageError}
                />
              </div>
            </div>
          </div>
          <div
            data-view-pane="calendar"
            data-mobile-pane="calendar"
            data-mobile-active={mobilePane === 'calendar' ? 'true' : 'false'}
            hidden={view !== 'calendar'}
            aria-hidden={view === 'calendar' ? undefined : 'true'}
            className="hoomail-calendar-pane flex min-h-0 min-w-0 flex-1 flex-col"
          >
            <div className="hoomail-calendar-backbar shrink-0 border-b border-border px-3 py-1 lg:hidden">
              <Button
                type="button"
                size="sm"
                variant="ghost"
                data-mobile-calendar-back
                className="hoomail-touch-target px-2 text-xs"
                onClick={returnToInbox}
                aria-label="Back to inbox"
              >
                <ChevronLeft className="size-4" aria-hidden="true" />
                Back to inbox
              </Button>
            </div>
            <div data-calendar-pane-focus tabIndex={-1} className="hoomail-calendar-focus flex min-h-0 min-w-0 flex-1 outline-none">
              <CalendarView
                mailbox={selectedMailbox}
                events={events}
                onOpenMessage={openMessageFromCalendar}
              />
            </div>
          </div>
        </div>
      </div>
      {sendTestOpen && <SendTestDialog open onOpenChange={setSendTestOpen} />}
      {resetOpen && <ResetDialog open onOpenChange={setResetOpen} />}
    </main>
  )
}
