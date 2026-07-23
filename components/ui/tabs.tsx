import { createContext, type ComponentChildren, type JSX } from "preact"
import { useContext, useId, useState } from "preact/hooks"
import { cva, type VariantProps } from "class-variance-authority"

import { cn } from "@/lib/utils"

type TabsContextValue = {
  value: string | undefined
  setValue: (value: string) => void
  orientation: "horizontal" | "vertical"
  baseId: string
}

const TabsContext = createContext<TabsContextValue | null>(null)

function useTabsContext() {
  const context = useContext(TabsContext)
  if (!context) throw new Error("Tabs components must be used within Tabs")
  return context
}

type TabsProps = JSX.HTMLAttributes<HTMLDivElement> & {
  value?: string
  defaultValue?: string
  onValueChange?: (value: string) => void
  orientation?: "horizontal" | "vertical"
}

function Tabs({
  className,
  orientation = "horizontal",
  value,
  defaultValue,
  onValueChange,
  children,
  ...props
}: TabsProps) {
  const [uncontrolledValue, setUncontrolledValue] = useState(defaultValue)
  const currentValue = value === undefined ? uncontrolledValue : value
  const baseId = useId()
  const setValue = (nextValue: string) => {
    if (value === undefined) setUncontrolledValue(nextValue)
    onValueChange?.(nextValue)
  }

  return (
    <TabsContext.Provider value={{ value: currentValue, setValue, orientation, baseId }}>
      <div
        data-slot="tabs"
        data-orientation={orientation}
        className={cn(
          "group/tabs flex gap-2 data-horizontal:flex-col",
          className
        )}
        {...props}
      >
        {children}
      </div>
    </TabsContext.Provider>
  )
}

const tabsListVariants = cva(
  "group/tabs-list inline-flex w-fit items-center justify-center rounded-lg p-[3px] text-muted-foreground group-data-horizontal/tabs:h-8 group-data-vertical/tabs:h-fit group-data-vertical/tabs:flex-col data-[variant=line]:rounded-none",
  {
    variants: {
      variant: {
        default: "bg-muted",
        line: "gap-1 bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)

type TabsListProps = JSX.HTMLAttributes<HTMLDivElement> & VariantProps<typeof tabsListVariants>

function TabsList({
  className,
  variant = "default",
  onKeyDown,
  ...props
}: TabsListProps) {
  const { orientation } = useTabsContext()
  return (
    <div
      role="tablist"
      aria-orientation={orientation}
      data-slot="tabs-list"
      data-variant={variant}
      className={cn(tabsListVariants({ variant }), className)}
      onKeyDown={(event) => {
        onKeyDown?.(event)
        if (event.defaultPrevented) return
        const previousKey = orientation === "horizontal" ? "ArrowLeft" : "ArrowUp"
        const nextKey = orientation === "horizontal" ? "ArrowRight" : "ArrowDown"
        if (![previousKey, nextKey, "Home", "End"].includes(event.key)) return

        const tabs = Array.from(
          event.currentTarget.querySelectorAll<HTMLButtonElement>("[role='tab']:not([disabled])")
        )
        if (tabs.length === 0) return
        const currentIndex = tabs.indexOf(document.activeElement as HTMLButtonElement)
        let nextIndex = currentIndex
        if (event.key === "Home") nextIndex = 0
        else if (event.key === "End") nextIndex = tabs.length - 1
        else if (event.key === previousKey) nextIndex = (currentIndex - 1 + tabs.length) % tabs.length
        else if (event.key === nextKey) nextIndex = (currentIndex + 1) % tabs.length

        event.preventDefault()
        tabs[nextIndex]?.focus()
        tabs[nextIndex]?.click()
      }}
      {...props}
    />
  )
}

type TabsTriggerProps = JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
  value: string
}

function TabsTrigger({ className, value, onClick, ...props }: TabsTriggerProps) {
  const { value: selectedValue, setValue, baseId } = useTabsContext()
  const active = selectedValue === value
  const encodedValue = encodeURIComponent(value)

  return (
    <button
      type="button"
      role="tab"
      id={`${baseId}-trigger-${encodedValue}`}
      aria-selected={active}
      aria-controls={`${baseId}-content-${encodedValue}`}
      tabIndex={props.disabled ? -1 : 0}
      data-slot="tabs-trigger"
      data-state={active ? "active" : "inactive"}
      data-active={active ? "" : undefined}
      className={cn(
        "relative inline-flex h-[calc(100%-1px)] flex-1 items-center justify-center gap-1.5 rounded-md border border-transparent px-1.5 py-0.5 text-sm font-medium whitespace-nowrap text-foreground/60 transition-all group-data-vertical/tabs:w-full group-data-vertical/tabs:justify-start hover:text-foreground focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:outline-1 focus-visible:outline-ring disabled:pointer-events-none disabled:opacity-50 has-data-[icon=inline-end]:pr-1 has-data-[icon=inline-start]:pl-1 aria-disabled:pointer-events-none aria-disabled:opacity-50 dark:text-muted-foreground dark:hover:text-foreground group-data-[variant=default]/tabs-list:data-active:shadow-sm group-data-[variant=line]/tabs-list:data-active:shadow-none [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        "group-data-[variant=line]/tabs-list:bg-transparent group-data-[variant=line]/tabs-list:data-active:bg-transparent dark:group-data-[variant=line]/tabs-list:data-active:border-transparent dark:group-data-[variant=line]/tabs-list:data-active:bg-transparent",
        "data-active:bg-background data-active:text-foreground dark:data-active:border-input dark:data-active:bg-input/30 dark:data-active:text-foreground",
        "after:absolute after:bg-foreground after:opacity-0 after:transition-opacity group-data-horizontal/tabs:after:inset-x-0 group-data-horizontal/tabs:after:bottom-[-5px] group-data-horizontal/tabs:after:h-0.5 group-data-vertical/tabs:after:inset-y-0 group-data-vertical/tabs:after:-right-1 group-data-vertical/tabs:after:w-0.5 group-data-[variant=line]/tabs-list:data-active:after:opacity-100",
        className
      )}
      onClick={(event) => {
        onClick?.(event)
        if (!event.defaultPrevented && !props.disabled) setValue(value)
      }}
      {...props}
    />
  )
}

type TabsContentProps = JSX.HTMLAttributes<HTMLDivElement> & {
  value: string
}

function TabsContent({ className, value, ...props }: TabsContentProps) {
  const { value: selectedValue, baseId } = useTabsContext()
  const active = selectedValue === value
  const encodedValue = encodeURIComponent(value)

  return (
    <div
      role="tabpanel"
      id={`${baseId}-content-${encodedValue}`}
      aria-labelledby={`${baseId}-trigger-${encodedValue}`}
      tabIndex={-1}
      data-slot="tabs-content"
      data-state={active ? "active" : "inactive"}
      data-active={active ? "" : undefined}
      hidden={!active}
      className={cn("flex-1 text-sm outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent, tabsListVariants }
