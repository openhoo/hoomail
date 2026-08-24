import { useEffect, useRef, useState } from 'preact/hooks'
import { Loader2, RotateCcw, Send } from '@/components/ui/icons'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export function SendTestDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [to, setTo] = useState('test@hoomail.local')
  const [subject, setSubject] = useState('hoomail delivery test')
  const [kind, setKind] = useState<'plain' | 'invite' | 'update' | 'cancellation'>('plain')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const sendingRef = useRef(false)
  const sendControllerRef = useRef<AbortController | null>(null)
  const sendGeneration = useRef(0)

  useEffect(() => {
    return () => {
      sendGeneration.current += 1
      sendControllerRef.current?.abort()
    }
  }, [])
  useEffect(() => {
    if (!open) {
      sendGeneration.current += 1
      sendControllerRef.current?.abort()
      sendingRef.current = false
      setSending(false)
    }
  }, [open])

  const send = async () => {
    if (sendingRef.current) return
    sendingRef.current = true
    const generation = ++sendGeneration.current
    const controller = new AbortController()
    sendControllerRef.current = controller
    setSending(true)
    setError(null)
    try {
      const res = await fetch('/api/send-test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ to, subject, kind }),
        signal: controller.signal,
      })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        throw new Error(data.error || 'Send failed')
      }
      if (controller.signal.aborted || sendGeneration.current !== generation) return
      onOpenChange(false)
    } catch (err) {
      if (controller.signal.aborted || sendGeneration.current !== generation) return
      setError(err instanceof Error ? err.message : 'Send failed')
    } finally {
      if (sendGeneration.current !== generation) return
      setSending(false)
      sendingRef.current = false
      if (sendControllerRef.current === controller) sendControllerRef.current = null
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Send a test email</DialogTitle>
          <DialogDescription>
            Sends a sample HTML email (with an attachment) through the real SMTP server on port
            2525, exercising the full pipeline.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="send-test-to">Recipient</Label>
            <Input
              id="send-test-to"
              value={to}
              onInput={(event) => setTo(event.currentTarget.value)}
              placeholder="anyone@example.test"
              className="text-sm"
              onKeyDown={(e) => {
                if (
                  e.key === 'Enter' &&
                  !(e as KeyboardEvent).isComposing &&
                  e.keyCode !== 229
                ) {
                  send()
                }
              }}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="send-test-subject">
              {kind === 'plain' ? 'Subject' : 'Event title'}
            </Label>
            <Input
              id="send-test-subject"
              value={subject}
              onInput={(event) => setSubject(event.currentTarget.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <span id="message-type-label" className="text-sm font-medium">Message type</span>
            <div
              role="radiogroup"
              aria-labelledby="message-type-label"
              className="grid grid-cols-2 gap-1.5"
              onKeyDown={(event) => {
                const radios = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="radio"]')]
                const current = radios.indexOf(document.activeElement as HTMLButtonElement)
                if (current < 0) return
                let next = current
                if (event.key === 'Home') next = 0
                else if (event.key === 'End') next = radios.length - 1
                else if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') next = (current - 1 + radios.length) % radios.length
                else if (event.key === 'ArrowRight' || event.key === 'ArrowDown') next = (current + 1) % radios.length
                else return
                event.preventDefault()
                const value = ['plain', 'invite', 'update', 'cancellation'][next] as typeof kind
                radios[next]?.focus()
                setKind(value)
                setSubject((previous) => {
                  const defaults = ['hoomail delivery test', 'Owl standup meeting']
                  if (!defaults.includes(previous)) return previous
                  return value === 'plain' ? 'hoomail delivery test' : 'Owl standup meeting'
                })
              }}
            >
              {(
                [
                  ['plain', 'Plain email'],
                  ['invite', 'Meeting invite'],
                  ['update', 'Invite update'],
                  ['cancellation', 'Cancellation'],
                ] as const
              ).map(([value, label]) => (
                <Button
                  key={value}
                  type="button"
                  size="sm"
                  variant={kind === value ? 'default' : 'outline'}
                  role="radio"
                  aria-checked={kind === value}
                  tabIndex={kind === value ? 0 : -1}
                  onClick={() => {
                    setKind(value)
                    setSubject((prev) => {
                      const defaults = ['hoomail delivery test', 'Owl standup meeting']
                      if (!defaults.includes(prev)) return prev
                      return value === 'plain' ? 'hoomail delivery test' : 'Owl standup meeting'
                    })
                  }}
                  className="justify-start text-xs"
                >
                  {label}
                </Button>
              ))}
            </div>
            {kind !== 'plain' && (
              <p className="text-xs leading-relaxed text-muted-foreground">
                Sends a standards-shaped iCalendar (iTIP) part like Outlook does. Invite, update,
                and cancellation target the same event UID, so send them in sequence to see the
                calendar react.
              </p>
            )}
          </div>
          {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={sending}>
            Cancel
          </Button>
          <Button onClick={send} disabled={sending} aria-busy={sending || undefined}>
            {sending ? (
              <>
                <span className="sr-only" role="status">Sending test email</span>
                <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              </>
            ) : (
              <Send className="size-4" aria-hidden="true" />
            )}
            Send
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ResetDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [resetting, setResetting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const resetControllerRef = useRef<AbortController | null>(null)
  const resetGeneration = useRef(0)
  useEffect(() => {
    return () => {
      resetGeneration.current += 1
      resetControllerRef.current?.abort()
    }
  }, [])
  useEffect(() => {
    if (!open) {
      resetGeneration.current += 1
      resetControllerRef.current?.abort()
      setResetting(false)
    }
  }, [open])

  const reset = async () => {
    const generation = ++resetGeneration.current
    const controller = new AbortController()
    resetControllerRef.current = controller
    setResetting(true)
    setError(null)
    try {
      const response = await fetch('/api/reset', { method: 'POST', signal: controller.signal })
      if (!response.ok) {
        const data = await response.json().catch(() => ({}))
        throw new Error(data.error || 'Reset failed')
      }
      if (controller.signal.aborted || resetGeneration.current !== generation) return
      onOpenChange(false)
    } catch (err) {
      if (controller.signal.aborted || resetGeneration.current !== generation) return
      setError(err instanceof Error ? err.message : 'Reset failed')
    } finally {
      if (resetGeneration.current !== generation) return
      setResetting(false)
      if (resetControllerRef.current === controller) resetControllerRef.current = null
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>Reset hoomail?</DialogTitle>
          <DialogDescription>
            This permanently wipes all inboxes, messages, and attachments. Perfect for starting a
            clean test run.
          </DialogDescription>
        </DialogHeader>
        {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={resetting}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={reset} disabled={resetting} aria-busy={resetting || undefined}>
            {resetting ? (
              <>
                <span className="sr-only" role="status">Resetting hoomail</span>
                <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              </>
            ) : (
              <RotateCcw className="size-4" aria-hidden="true" />
            )}
            Wipe everything
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
