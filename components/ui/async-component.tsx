import { createElement } from 'preact'
import type { ComponentChild, ComponentType, RenderableProps } from 'preact'
import { useEffect, useState } from 'preact/hooks'

export function asyncComponent<Props extends object>(
  load: () => Promise<ComponentType<Props>>,
  fallback: ComponentChild = null,
): ComponentType<Props> {
  let loaded: ComponentType<Props> | null = null
  let pending: Promise<ComponentType<Props>> | null = null

  return function AsyncComponent(props: RenderableProps<Props>) {
    const [, rerender] = useState(0)

    useEffect(() => {
      if (loaded) return
      let active = true
      pending ??= load()
      void pending.then((component) => {
        loaded = component
        if (active) rerender((version) => version + 1)
      })
      return () => {
        active = false
      }
    }, [])

    return loaded ? createElement(loaded, props as Props) : fallback
  }
}
