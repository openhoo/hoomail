import type { JSX } from 'preact'
import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { CalendarDays, Mail } from '@/components/ui/icons'
import { Button } from '@/components/ui/button'
import { CalendarView } from './calendar-view'
import { MailboxSidebar } from './mailbox-sidebar'
import { MessageList } from './message-list'
import { MessageViewer } from './message-viewer'
import { ResetDialog, SendTestDialog } from './dialogs'
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

export function HoomailApp() {

  const [selectedMailboxId, setSelectedMailboxId] = useState<number | null>(null)
  const [selectedMessageId, setSelectedMessageId] = useState<number | null>(null)
  const [selectedIds, setSelectedIds] = useState<Set<number>>(new Set())
  const [searchQuery, setSearchQuery] = useState('')
  const [view, setView] = useState<'mail' | 'calendar'>('mail')
  const [sendTestOpen, setSendTestOpen] = useState(false)
  const [resetOpen, setResetOpen] = useState(false)
  const anchorIdRef = useRef<number | null>(null)
  const pendingMessageFocusRef = useRef<number | null>(null)

  const { mailboxes } = useMailboxes()
  const { messages } = useMessages(selectedMailboxId, searchQuery)
  const { detail, isLoading: messageLoading } = useMessage(selectedMessageId)
  const { events } = useCalendarEvents(selectedMailboxId, view === 'calendar')

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
  }, [])

  useRealtime({
    selectedMailboxId,
    onReset: handleReset,
    onMailboxDeleted: (mailboxId) => {
      if (selectedMailboxId === mailboxId) {
        setSelectedMailboxId(null)
        setSelectedMessageId(null)
        setSelectedIds(new Set())
      }
    },
  })

  const handleDeleteMailbox = async (id: number) => {
    const ok = await deleteMailboxRequest(id)
    if (!ok) return
    if (selectedMailboxId === id) {
      setSelectedMailboxId(null)
      setSelectedMessageId(null)
      setSelectedIds(new Set())
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
    setSelectedMessageId(id)
    setSelectedIds(new Set())
    anchorIdRef.current = id

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

    void runMessageAction('read', [id]).then((ok) => {
      if (!ok) refreshAfterRead(selectedMailboxId)
    })
  }, [messages, selectedMailboxId])

  useEffect(() => {
    const id = pendingMessageFocusRef.current
    if (view !== 'mail' || id == null) return
    const row = document.querySelector<HTMLButtonElement>(`button.reactive-message[data-message-id="${id}"]`)
    if (!row) return
    pendingMessageFocusRef.current = null
    row.focus()
    row.scrollIntoView({ block: 'nearest' })
  }, [messages, view])

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

  const handleAction = async (action: 'delete' | 'read' | 'unread', ids: number[]) => {
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

    const ok = await runMessageAction(action, ids)
    if (!ok) {
      // Revert the optimistic update by revalidating from the server
      if (selectedMailboxId != null) refreshAfterRead(selectedMailboxId)
      return
    }
    setSelectedIds(new Set())
    // SSE 'messages:changed' refreshes lists; this covers latency gaps
    if (selectedMailboxId != null) refreshAfterRead(selectedMailboxId)
  }

  const openMessageFromCalendar = (messageId: number) => {
    pendingMessageFocusRef.current = messageId
    setView('mail')
    openMessage(messageId)
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
      // Don't steal keys while a dialog or menu is open
      if (document.querySelector('[role="dialog"], [role="menu"]')) return

      const messageList = document.querySelector('[data-message-list]')
      if (!target || !messageList?.contains(target)) return

      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault()
        if (messages.length === 0) return

        const rows = [...document.querySelectorAll<HTMLButtonElement>('button.reactive-message[data-message-id]')]
        const focusedIndex = rows.indexOf(document.activeElement as HTMLButtonElement)
        const selectedIndex = selectedMessageId == null
          ? -1
          : rows.findIndex((row) => Number(row.dataset.messageId) === selectedMessageId)
        const currentIndex = focusedIndex >= 0 ? focusedIndex : selectedIndex
        const nextIndex = currentIndex < 0
          ? event.key === 'ArrowDown' ? 0 : rows.length - 1
          : Math.min(
              rows.length - 1,
              Math.max(0, currentIndex + (event.key === 'ArrowDown' ? 1 : -1))
            )
        const nextRow = rows[nextIndex]
        const nextId = Number(nextRow?.dataset.messageId)
        if (!nextRow || !Number.isInteger(nextId)) return

        // Move browser focus before updating application state. This keeps the
        // native focus ring, Enter activation, and selection on one row even
        // during rapid key-repeat events.
        nextRow.focus()
        nextRow.scrollIntoView({ block: 'nearest' })
        if (nextId !== selectedMessageId) openMessage(nextId)
        return
      }

      if (event.key === 'Delete' || event.key === 'Backspace') {
        const targets = selectedIds.size > 0 ? [...selectedIds] : selectedMessageId != null ? [selectedMessageId] : []
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
  }, [openMessage])

  const selectedMailbox = mailboxes.find((m) => m.id === selectedMailboxId) ?? null

  // The detail cache deliberately retains the previously opened message while
  // the next request is in flight. Keep rendering it so the viewer shell and
  // iframe remain mounted; only replace their content when the new detail
  // arrives.
  const displayedDetail = detail

  return (
    <main className="flex h-dvh overflow-hidden bg-background text-foreground">
      <MailboxSidebar
        mailboxes={mailboxes}
        selectedId={selectedMailboxId}
        onSelect={selectMailbox}
        onDelete={handleDeleteMailbox}
        onOpenSendTest={() => setSendTestOpen(true)}
        onOpenReset={() => setResetOpen(true)}
      />
      <div className="flex min-w-0 flex-1 flex-col">
        <nav aria-label="Primary views" className="flex h-10 shrink-0 items-center gap-1 border-b border-border px-3">
          <Button
            size="sm"
            variant={view === 'mail' ? 'secondary' : 'ghost'}
            className="h-7 px-2.5 text-xs"
            onClick={() => setView('mail')}
            aria-pressed={view === 'mail'}
          >
            <Mail className="size-3.5" aria-hidden="true" />
            Mail
          </Button>
          <Button
            size="sm"
            variant={view === 'calendar' ? 'secondary' : 'ghost'}
            className="h-7 px-2.5 text-xs"
            onClick={() => {
              if (selectedMailboxId != null) mutateCache(`/api/mailboxes/${selectedMailboxId}/events`)
              setView('calendar')
            }}
            aria-pressed={view === 'calendar'}
          >
            <CalendarDays className="size-3.5" aria-hidden="true" />
            Calendar
          </Button>
        </nav>
        <div className="flex min-h-0 flex-1">
          {view === 'mail' ? (
            <>
              <MessageList
                mailbox={selectedMailbox}
                messages={messages}
                selectedId={selectedMessageId}
                selectedIds={selectedIds}
                searchQuery={searchQuery}
                onSearchChange={handleSearchChange}
                onRowClick={handleRowClick}
                onAction={handleAction}
              />
              <MessageViewer
                message={displayedDetail?.message ?? null}
                attachments={displayedDetail?.attachments ?? []}
                selectedMessageId={selectedMessageId}
                isLoading={messageLoading}
              />
            </>
          ) : (
            <CalendarView
              mailbox={selectedMailbox}
              events={events}
              onOpenMessage={openMessageFromCalendar}
            />
          )}
        </div>
      </div>
      <SendTestDialog open={sendTestOpen} onOpenChange={setSendTestOpen} />
      <ResetDialog open={resetOpen} onOpenChange={setResetOpen} />
    </main>
  )
}
