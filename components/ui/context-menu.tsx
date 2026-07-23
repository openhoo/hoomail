import { createContext, type ComponentChildren, type JSX } from 'preact'
import { useContext, useEffect, useRef, useState } from 'preact/hooks'
import { createPortal } from 'preact/compat'

import { cn } from '@/lib/utils'
import { CheckIcon, ChevronRightIcon } from '@/components/ui/icons'

type MenuContextValue = {
  open: boolean
  x: number
  y: number
  show: (x: number, y: number, trigger: HTMLElement) => void
  close: (restoreFocus?: boolean, move?: 'next' | 'previous') => void
}

const MenuContext = createContext<MenuContextValue | null>(null)

function useMenu() {
  const context = useContext(MenuContext)
  if (!context) throw new Error('Context menu components must be used within ContextMenu')
  return context
}

function ContextMenu({ children }: { children?: ComponentChildren }) {
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const triggerRef = useRef<HTMLElement | null>(null)
  const close = (restoreFocus = true, move?: 'next' | 'previous') => {
    const trigger = triggerRef.current
    let moveTarget: HTMLElement | null = null
    if (move && trigger) {
      const candidates = [...document.querySelectorAll<HTMLElement>(
        "a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), iframe:not([tabindex='-1']), [contenteditable='true'], [tabindex]:not([tabindex='-1'])"
      )].filter((element) => element.offsetParent !== null && !element.closest('[role="menu"]'))
      const following = candidates.filter((element) =>
        trigger.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING
      )
      const preceding = candidates.filter((element) =>
        trigger.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_PRECEDING
      )
      moveTarget = move === 'next'
        ? following[0] ?? candidates[0] ?? null
        : preceding.at(-1) ?? candidates.at(-1) ?? null
    }
    setPosition(null)
    if (!restoreFocus || !trigger) return
    requestAnimationFrame(() => (moveTarget ?? trigger).focus())
  }
  return (
    <MenuContext.Provider
      value={{
        open: position !== null,
        x: position?.x ?? 0,
        y: position?.y ?? 0,
        show: (x, y, trigger) => {
          triggerRef.current = trigger
          setPosition({ x, y })
        },
        close,
      }}
    >
      {children}
    </MenuContext.Provider>
  )
}

function ContextMenuTrigger({ className, onContextMenu, onKeyDown, ...props }: JSX.HTMLAttributes<HTMLDivElement>) {
  const { show } = useMenu()
  return (
    <div
      data-slot="context-menu-trigger"
      className={cn('select-none', className)}
      onContextMenu={(event) => {
        onContextMenu?.(event)
        if (event.defaultPrevented) return
        event.preventDefault()
        show(event.clientX, event.clientY, event.currentTarget.querySelector<HTMLElement>('button, a, [tabindex]') ?? event.currentTarget)
      }}
      onKeyDown={(event) => {
        onKeyDown?.(event)
        if (event.defaultPrevented || !(event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10'))) return
        event.preventDefault()
        const trigger = event.target as HTMLElement
        const rect = trigger.getBoundingClientRect()
        show(rect.left + 12, rect.top + 12, trigger)
      }}
      {...props}
    />
  )
}

function ContextMenuPortal({ children }: { children?: ComponentChildren }) {
  if (typeof document === 'undefined') return null
  return createPortal(children, document.body)
}

function ContextMenuContent({ className, children, ...props }: JSX.HTMLAttributes<HTMLDivElement>) {
  const { open, x, y, close } = useMenu()
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const dismiss = () => close(false)
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        close()
        return
      }
      if (event.key === 'Tab') {
        event.preventDefault()
        close(true, event.shiftKey ? 'previous' : 'next')
        return
      }
      if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
      const items = [...(ref.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]:not(:disabled)') ?? [])]
      if (items.length === 0) return
      event.preventDefault()
      const current = items.indexOf(document.activeElement as HTMLButtonElement)
      let next = current
      if (event.key === 'Home') next = 0
      else if (event.key === 'End') next = items.length - 1
      else if (event.key === 'ArrowDown') next = (current + 1 + items.length) % items.length
      else next = (current - 1 + items.length) % items.length
      items[next]?.focus()
    }
    window.addEventListener('pointerdown', dismiss)
    window.addEventListener('blur', dismiss)
    window.addEventListener('keydown', onKeyDown)
    requestAnimationFrame(() => ref.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus())
    return () => {
      window.removeEventListener('pointerdown', dismiss)
      window.removeEventListener('blur', dismiss)
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  if (!open) return null
  return (
    <ContextMenuPortal>
      <div
        ref={ref}
        role="menu"
        data-slot="context-menu-content"
        className={cn(
          'fixed z-50 max-h-[calc(100vh-1rem)] min-w-36 overflow-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none',
          className
        )}
        style={{ left: Math.min(x, window.innerWidth - 224), top: Math.min(y, window.innerHeight - 160) }}
        onPointerDown={(event) => event.stopPropagation()}
        {...props}
      >
        {children}
      </div>
    </ContextMenuPortal>
  )
}

function ContextMenuItem({ className, variant = 'default', disabled, onClick, ...props }: JSX.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'default' | 'destructive' }) {
  const { close } = useMenu()
  return (
    <button
      type="button"
      role="menuitem"
      tabIndex={-1}
      disabled={disabled}
      data-slot="context-menu-item"
      data-variant={variant}
      className={cn(
        'group/context-menu-item relative flex w-full cursor-default items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-sm outline-none hover:bg-accent hover:text-accent-foreground focus:bg-accent focus:text-accent-foreground disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*=size-])]:size-4',
        variant === 'destructive' && 'text-destructive hover:bg-destructive/10 hover:text-destructive focus:bg-destructive/10 focus:text-destructive',
        className
      )}
      onClick={(event) => {
        onClick?.(event)
        if (!event.defaultPrevented) close()
      }}
      {...props}
    />
  )
}

function ContextMenuSeparator({ className, ...props }: JSX.HTMLAttributes<HTMLDivElement>) {
  return <div role="separator" data-slot="context-menu-separator" className={cn('-mx-1 my-1 h-px bg-border', className)} {...props} />
}

function ContextMenuGroup(props: JSX.HTMLAttributes<HTMLDivElement>) { return <div role="group" {...props} /> }
function ContextMenuLabel({ className, ...props }: JSX.HTMLAttributes<HTMLDivElement>) { return <div className={cn('px-1.5 py-1 text-xs font-medium text-muted-foreground', className)} {...props} /> }
function ContextMenuShortcut({ className, ...props }: JSX.HTMLAttributes<HTMLSpanElement>) { return <span className={cn('ml-auto text-xs tracking-widest text-muted-foreground', className)} {...props} /> }
function ContextMenuCheckboxItem({ checked, children, ...props }: JSX.ButtonHTMLAttributes<HTMLButtonElement> & { checked?: boolean }) { return <ContextMenuItem {...props}>{children}{checked && <CheckIcon className="ml-auto" />}</ContextMenuItem> }
function ContextMenuRadioItem(props: JSX.ButtonHTMLAttributes<HTMLButtonElement>) { return <ContextMenuItem {...props} /> }
function ContextMenuRadioGroup(props: JSX.HTMLAttributes<HTMLDivElement>) { return <div role="group" {...props} /> }
function ContextMenuSub({ children }: { children?: ComponentChildren }) { return <>{children}</> }
function ContextMenuSubTrigger({ children, ...props }: JSX.ButtonHTMLAttributes<HTMLButtonElement>) { return <ContextMenuItem {...props}>{children}<ChevronRightIcon className="ml-auto" /></ContextMenuItem> }
function ContextMenuSubContent(props: JSX.HTMLAttributes<HTMLDivElement>) { return <div {...props} /> }

export {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuCheckboxItem,
  ContextMenuRadioItem,
  ContextMenuLabel,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuGroup,
  ContextMenuPortal,
  ContextMenuSub,
  ContextMenuSubContent,
  ContextMenuSubTrigger,
  ContextMenuRadioGroup,
}
