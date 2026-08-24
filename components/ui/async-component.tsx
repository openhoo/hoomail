import { createElement } from 'preact'
import type { ComponentChild, ComponentType, RenderableProps } from 'preact'
import { useEffect, useState } from 'preact/hooks'

type AsyncLoadState = { error: unknown; retry: number }

function AsyncLoadError({ onRetry }: { onRetry: () => void }) {
  return (
    <div role="alert">
      <span>Unable to load this panel.</span>{' '}
      <button type="button" onClick={onRetry}>
        Retry
      </button>
    </div>
  )
}

export function asyncComponent<Props extends object>(
  load: () => Promise<ComponentType<Props>>,
  fallback: ComponentChild = null,
): ComponentType<Props> {
  let loaded: ComponentType<Props> | null = null
  let pending: Promise<ComponentType<Props>> | null = null

  return function AsyncComponent(props: RenderableProps<Props>) {
    const [{ error, retry }, setLoadState] = useState<AsyncLoadState>({ error: null, retry: 0 })

    useEffect(() => {
      if (loaded) return
      let active = true
      const request = pending ?? (pending = Promise.resolve().then(load))
      void request
        .then((component) => {
          if (pending === request) loaded = component
          if (active) setLoadState({ error: null, retry })
        })
        .catch((reason: unknown) => {
          if (pending === request) pending = null
          if (active) setLoadState({ error: reason, retry })
        })
      return () => {
        active = false
      }
    }, [retry])

    if (loaded) return createElement(loaded, props as Props)
    if (error) {
      return <AsyncLoadError onRetry={() => setLoadState({ error: null, retry: retry + 1 })} />
    }
    return fallback
  }
}
