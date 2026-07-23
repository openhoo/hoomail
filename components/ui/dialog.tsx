import {
  cloneElement,
  createContext,
  type ComponentChildren,
  type JSX,
  type VNode,
} from "preact"
import { createPortal } from "preact/compat"
import {
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from "preact/hooks"

import { cn } from "@/lib/utils"
import { XIcon } from "@/components/ui/icons"

type DialogContextValue = {
  open: boolean
  setOpen: (open: boolean) => void
  titleId: string
  descriptionId: string
  contentId: string
  triggerRef: { current: HTMLElement | null }
  hasDescription: boolean
  setHasDescription: (present: boolean) => void
}

const DialogContext = createContext<DialogContextValue | null>(null)

function useDialogContext() {
  const context = useContext(DialogContext)
  if (!context) throw new Error("Dialog components must be used within Dialog")
  return context
}

type DialogProps = {
  children?: ComponentChildren
  open?: boolean
  defaultOpen?: boolean
  onOpenChange?: (open: boolean) => void
}

function Dialog({ children, open, defaultOpen = false, onOpenChange }: DialogProps) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(defaultOpen)
  const controlled = open !== undefined
  const currentOpen = controlled ? open : uncontrolledOpen
  const titleId = useId()
  const descriptionId = useId()
  const contentId = useId()
  const triggerRef = useRef<HTMLElement | null>(null)
  const [hasDescription, setHasDescription] = useState(false)

  const setOpen = (nextOpen: boolean) => {
    if (!controlled) setUncontrolledOpen(nextOpen)
    onOpenChange?.(nextOpen)
  }

  return (
    <DialogContext.Provider
      value={{
        open: currentOpen,
        setOpen,
        titleId,
        descriptionId,
        contentId,
        triggerRef,
        hasDescription,
        setHasDescription,
      }}
    >
      {children}
    </DialogContext.Provider>
  )
}

type RenderableButtonProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  render?: VNode<JSX.HTMLAttributes<HTMLElement>>
}

function renderButton(
  render: VNode<JSX.HTMLAttributes<HTMLElement>> | undefined,
  props: JSX.ButtonHTMLAttributes<HTMLButtonElement>,
  children: ComponentChildren
) {
  if (!render) return <button {...props}>{children}</button>
  return cloneElement(render, { ...render.props, ...props }, children)
}

function DialogTrigger({ render, children, onClick, ...props }: RenderableButtonProps) {
  const { open, setOpen, contentId, triggerRef } = useDialogContext()
  const triggerProps: JSX.ButtonHTMLAttributes<HTMLButtonElement> = {
    type: "button",
    "aria-haspopup": "dialog",
    "aria-expanded": open,
    "aria-controls": contentId,
    ...props,
    ref: (node) => {
      triggerRef.current = node
      const ref = props.ref
      if (typeof ref === "function") ref(node)
      else if (ref && typeof ref === "object") ref.current = node
    },
    onClick: (event) => {
      onClick?.(event)
      if (!event.defaultPrevented) setOpen(true)
    },
  }
  return renderButton(render, triggerProps, children)
}

type DialogPortalProps = {
  children?: ComponentChildren
  container?: Element | null
}

function DialogPortal({ children, container }: DialogPortalProps) {
  if (typeof document === "undefined") return null
  return createPortal(children, container ?? document.body)
}

function DialogClose({ render, children, onClick, ...props }: RenderableButtonProps) {
  const { setOpen } = useDialogContext()
  const closeProps: JSX.ButtonHTMLAttributes<HTMLButtonElement> = {
    type: "button",
    ...props,
    onClick: (event) => {
      onClick?.(event)
      if (!event.defaultPrevented) setOpen(false)
    },
  }
  return renderButton(render, closeProps, children)
}

type DialogOverlayProps = JSX.HTMLAttributes<HTMLDivElement>

function DialogOverlay({ className, onClick, ...props }: DialogOverlayProps) {
  const { open, setOpen } = useDialogContext()
  if (!open) return null

  return (
    <div
      data-slot="dialog-overlay"
      data-open=""
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/10 duration-100 supports-backdrop-filter:backdrop-blur-xs data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className
      )}
      onClick={(event) => {
        onClick?.(event)
        if (!event.defaultPrevented && event.target === event.currentTarget) setOpen(false)
      }}
      {...props}
    />
  )
}

type DialogContentProps = JSX.HTMLAttributes<HTMLDivElement> & {
  showCloseButton?: boolean
}

const focusableSelector = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "iframe",
  "object",
  "embed",
  "[contenteditable='true']",
  "[tabindex]:not([tabindex='-1'])",
].join(",")

function DialogContent({
  className,
  children,
  showCloseButton = true,
  onKeyDown,
  ...props
}: DialogContentProps) {
  const { open, setOpen, titleId, descriptionId, contentId, triggerRef, hasDescription } = useDialogContext()
  const contentRef = useRef<HTMLDivElement | null>(null)

  useLayoutEffect(() => {
    if (!open) return
    const previousActiveElement = document.activeElement as HTMLElement | null
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = "hidden"

    const content = contentRef.current
    const firstFocusable = content?.querySelector<HTMLElement>(focusableSelector)
    ;(firstFocusable ?? content)?.focus()

    return () => {
      document.body.style.overflow = previousOverflow
      const focusTarget = triggerRef.current ?? previousActiveElement
      focusTarget?.focus()
    }
  }, [open, triggerRef])

  if (!open) return null

  return (
    <DialogPortal>
      <DialogOverlay />
      <div
        ref={contentRef}
        id={contentId}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={hasDescription ? descriptionId : undefined}
        tabIndex={-1}
        data-slot="dialog-content"
        data-open=""
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 rounded-xl bg-popover p-4 text-sm text-popover-foreground ring-1 ring-foreground/10 duration-100 outline-none sm:max-w-sm data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        onKeyDown={(event) => {
          onKeyDown?.(event)
          if (event.defaultPrevented) return
          if (event.key === "Escape") {
            event.preventDefault()
            setOpen(false)
            return
          }
          if (event.key !== "Tab") return

          const focusable = Array.from(
            contentRef.current?.querySelectorAll<HTMLElement>(focusableSelector) ?? []
          ).filter((element) => element.offsetParent !== null)
          if (focusable.length === 0) {
            event.preventDefault()
            contentRef.current?.focus()
            return
          }

          const first = focusable[0]
          const last = focusable[focusable.length - 1]
          if (event.shiftKey && document.activeElement === first) {
            event.preventDefault()
            last.focus()
          } else if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault()
            first.focus()
          }
        }}
        {...props}
      >
        {children}
        {showCloseButton && (
          <DialogClose
            aria-label="Close"
            className="absolute top-2 right-2 inline-flex size-7 items-center justify-center rounded-[min(var(--radius-md),12px)] border border-transparent transition-all outline-none hover:bg-muted hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <XIcon />
            <span className="sr-only">Close</span>
          </DialogClose>
        )}
      </div>
    </DialogPortal>
  )
}

function DialogHeader({ className, ...props }: JSX.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("flex flex-col gap-2", className)}
      {...props}
    />
  )
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: JSX.HTMLAttributes<HTMLDivElement> & {
  showCloseButton?: boolean
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex flex-col-reverse gap-2 rounded-b-xl border-t bg-muted/50 p-4 sm:flex-row sm:justify-end",
        className
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogClose className="inline-flex h-8 items-center justify-center rounded-lg border border-border bg-background px-2.5 text-sm font-medium hover:bg-muted hover:text-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50">
          Close
        </DialogClose>
      )}
    </div>
  )
}

function DialogTitle({ className, id, ...props }: JSX.HTMLAttributes<HTMLHeadingElement>) {
  const { titleId } = useDialogContext()
  return (
    <h2
      id={id ?? titleId}
      data-slot="dialog-title"
      className={cn("text-base leading-none font-medium", className)}
      {...props}
    />
  )
}

function DialogDescription({ className, id, ...props }: JSX.HTMLAttributes<HTMLParagraphElement>) {
  const { descriptionId, setHasDescription } = useDialogContext()
  useEffect(() => {
    setHasDescription(true)
    return () => setHasDescription(false)
  }, [setHasDescription])
  return (
    <p
      id={id ?? descriptionId}
      data-slot="dialog-description"
      className={cn(
        "text-sm text-muted-foreground *:[a]:underline *:[a]:underline-offset-3 *:[a]:hover:text-foreground",
        className
      )}
      {...props}
    />
  )
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
}
