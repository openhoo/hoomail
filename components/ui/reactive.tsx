import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'

export function InlinePresence({
  visible,
  children,
  className,
  duration = 180,
}: {
  visible: boolean
  children: ComponentChildren
  className?: string
  duration?: number
}) {
  const [mounted, setMounted] = useState(visible)
  const [exiting, setExiting] = useState(false)

  useEffect(() => {
    if (visible) {
      setMounted(true)
      setExiting(false)
      return
    }
    if (!mounted) return
    setExiting(true)
    const timeout = window.setTimeout(() => {
      setMounted(false)
      setExiting(false)
    }, duration)
    return () => window.clearTimeout(timeout)
  }, [duration, mounted, visible])

  if (!mounted) return null

  return (
    <span
      className={className}
      data-inline-presence={exiting ? 'exiting' : 'present'}
      style={{ '--reactive-duration': `${duration}ms` }}
    >
      {children}
    </span>
  )
}

export function AnimatedValue({
  value,
  children,
  className,
}: {
  value: string | number
  children?: ComponentChildren
  className?: string
}) {
  const firstRender = useRef(true)
  const [revision, setRevision] = useState(0)
  const previous = useRef(value)

  useEffect(() => {
    if (previous.current !== value) {
      previous.current = value
      setRevision((current) => current + 1)
    }
    firstRender.current = false
  }, [value])

  return (
    <span
      key={revision}
      className={className}
      data-value-changed={firstRender.current ? undefined : ''}
    >
      {children ?? value}
    </span>
  )
}
