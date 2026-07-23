import { expect, test } from '@playwright/test'
import { mailboxButton, resetState, sendTestMessage } from './fixtures'

test('measures large-list rendering and keyboard navigation', async ({ page, request }, testInfo) => {
  const recipient = 'frontend-benchmark@hoomail.test'
  const messageCount = Number.parseInt(process.env.HOOMAIL_BENCH_MESSAGES ?? '200', 10)

  await resetState(request)
  for (let index = 0; index < messageCount; index += 20) {
    await Promise.all(
      Array.from({ length: Math.min(20, messageCount - index) }, (_, offset) =>
        sendTestMessage(request, {
          to: recipient,
          subject: `Frontend benchmark ${String(index + offset).padStart(4, '0')}`,
          kind: 'plain',
        })
      )
    )
  }

  await page.goto('/')
  await mailboxButton(page, recipient).click()
  const rows = page.locator('button.reactive-message[data-message-id]')
  await expect(rows).toHaveCount(messageCount)

  const metrics = await page.evaluate(async () => {
    const firstRow = document.querySelector<HTMLButtonElement>('button.reactive-message[data-message-id]')
    if (!firstRow) throw new Error('message list has no rows')
    firstRow.focus()
    let documentQueryCount = 0
    let documentQueryAllCount = 0
    const originalDocumentQuery = Document.prototype.querySelector
    const originalDocumentQueryAll = Document.prototype.querySelectorAll
    Document.prototype.querySelector = function (this: Document, selectors: string) {
      documentQueryCount++
      return originalDocumentQuery.call(this, selectors)
    } as typeof Document.prototype.querySelector
    Document.prototype.querySelectorAll = function (this: Document, selectors: string) {
      documentQueryAllCount++
      return originalDocumentQueryAll.call(this, selectors)
    } as typeof Document.prototype.querySelectorAll

    const synchronousKeyTimes: number[] = []
    const frameTimes: number[] = []
    const iterations = Math.min(100, document.querySelectorAll('[data-message-id]').length - 1)
    for (let index = 0; index < iterations; index++) {
      const startedAt = performance.now()
      document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
      synchronousKeyTimes.push(performance.now() - startedAt)
      await new Promise<void>((resolve) => requestAnimationFrame(() => {
        frameTimes.push(performance.now() - startedAt)
        resolve()
      }))
    }

    const summarize = (samples: number[]) => {
      const sorted = [...samples].sort((left, right) => left - right)
      return {
        mean: sorted.reduce((total, sample) => total + sample, 0) / sorted.length,
        p95: sorted[Math.floor(sorted.length * 0.95)],
        max: sorted.at(-1),
      }
    }

    return {
      rows: document.querySelectorAll('[data-message-id]').length,
      domNodes: document.getElementsByTagName('*').length,
      synchronousKeyMs: summarize(synchronousKeyTimes),
      frameMs: summarize(frameTimes),
      documentQueriesPerKey: documentQueryCount / iterations,
      documentQueryAllPerKey: documentQueryAllCount / iterations,
    }
  })

  await testInfo.attach('frontend-performance.json', {
    body: JSON.stringify(metrics, null, 2),
    contentType: 'application/json',
  })
  console.log(JSON.stringify(metrics))
})
