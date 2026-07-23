import type { JSX } from "preact"
import { useEffect, useRef, useState } from "preact/hooks"

import { cn } from "@/lib/utils"

function ScrollArea({
  className,
  children,
  tabIndex,
  ...props
}: JSX.HTMLAttributes<HTMLDivElement>) {
  const rootRef = useRef<HTMLDivElement>(null)
  const viewportRef = useRef<HTMLDivElement>(null)
  const [state, setState] = useState({ scrollable: false, needsTabStop: false })

  useEffect(() => {
    const root = rootRef.current
    const viewport = viewportRef.current
    if (!root || !viewport) return

    let updateFrame = 0
    const update = () => {
      updateFrame = 0
      const scrollable = root.scrollHeight > root.clientHeight + 1
      const hasFocusableDescendant = root.querySelector(
        "a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), iframe:not([tabindex='-1']), [contenteditable='true'], [tabindex]:not([tabindex='-1'])"
      ) !== null
      const needsTabStop = scrollable && !hasFocusableDescendant
      setState((current) =>
        current.scrollable === scrollable && current.needsTabStop === needsTabStop
          ? current
          : { scrollable, needsTabStop }
      )
    }
    const scheduleUpdate = () => {
      if (updateFrame === 0) updateFrame = requestAnimationFrame(update)
    }
    const resizeObserver = new ResizeObserver(scheduleUpdate)
    const mutationObserver = new MutationObserver(scheduleUpdate)
    resizeObserver.observe(root)
    resizeObserver.observe(viewport)
    mutationObserver.observe(viewport, { childList: true, subtree: true, attributes: true, attributeFilter: ["disabled", "hidden", "tabindex"] })
    update()
    return () => {
      if (updateFrame !== 0) cancelAnimationFrame(updateFrame)
      resizeObserver.disconnect()
      mutationObserver.disconnect()
    }
  }, [])

  return (
    <div
      ref={rootRef}
      data-slot="scroll-area"
      data-scrollable={state.scrollable ? "true" : "false"}
      tabIndex={tabIndex ?? (state.needsTabStop ? 0 : undefined)}
      className={cn("scroll-area relative size-full overflow-x-hidden overflow-y-auto rounded-[inherit] outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1", className)}
      {...props}
    >
      <div
        ref={viewportRef}
        data-slot="scroll-area-viewport"
        className="min-h-full min-w-full rounded-[inherit]"
      >
        {children}
      </div>
    </div>
  )
}

function ScrollBar({
  className,
  orientation = "vertical",
  ...props
}: JSX.HTMLAttributes<HTMLDivElement> & {
  orientation?: "horizontal" | "vertical"
}) {
  return (
    <div
      data-slot="scroll-area-scrollbar"
      data-orientation={orientation}
      className={cn(
        "hidden touch-none p-px transition-colors select-none data-[orientation=horizontal]:h-2.5 data-[orientation=horizontal]:flex-col data-[orientation=horizontal]:border-t data-[orientation=horizontal]:border-t-transparent data-[orientation=vertical]:h-full data-[orientation=vertical]:w-2.5 data-[orientation=vertical]:border-l data-[orientation=vertical]:border-l-transparent",
        className
      )}
      {...props}
    >
      <div
        data-slot="scroll-area-thumb"
        className="relative flex-1 rounded-full bg-border"
      />
    </div>
  )
}

export { ScrollArea, ScrollBar }
