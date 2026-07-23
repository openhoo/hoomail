import { expect, test as base, type APIRequestContext, type Page } from '@playwright/test'

export type TestMessageKind = 'plain' | 'invite' | 'update' | 'cancellation'

export async function resetState(request: APIRequestContext): Promise<void> {
  const response = await request.post('/api/reset')
  expect(response.status()).toBe(200)
}

export async function sendTestMessage(
  request: APIRequestContext,
  message: { to: string; subject: string; kind: TestMessageKind },
): Promise<void> {
  const response = await request.post('/api/send-test', { data: message })
  expect(response.status()).toBe(200)
}

export function messageRow(page: Page, subject: string) {
  // AutoAnimate briefly leaves an identical exiting row in the DOM. The last
  // match is the live list item while preserving the accessible-name contract.
  return page
    .getByRole('list', { name: 'Messages' })
    .getByRole('button', { name: new RegExp(`The hoomail Owl, ${escapeRegex(subject)},`) })
    .last()
}

export function mailboxButton(page: Page, address: string) {
  return page.getByRole('button', { name: new RegExp(`^${escapeRegex(address)}, \\d+ messages, \\d+ unread$`) })
}

export function mailboxMessagesResponse(page: Page, query?: string) {
  return page.waitForResponse((response) => {
    if (response.status() !== 200) return false
    const url = new URL(response.url())
    if (!/^\/api\/mailboxes\/\d+\/messages$/.test(url.pathname)) return false
    return query === undefined ? !url.searchParams.has('q') : url.searchParams.get('q') === query
  })
}

export function mailboxEventsResponse(page: Page) {
  return page.waitForResponse((response) => {
    const url = new URL(response.url())
    return response.status() === 200 && /^\/api\/mailboxes\/\d+\/events$/.test(url.pathname)
  })
}

function escapeRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export const test = base.extend({
  page: async ({ page, request }, use) => {
    await resetState(request)
    const eventsConnected = page.waitForResponse(
      (response) => response.status() === 200 && new URL(response.url()).pathname === '/api/events',
    )
    await page.goto('/')
    await eventsConnected
    await expect(page.getByRole('navigation', { name: 'Inboxes' })).toBeVisible()
    await expect(
      page
        .getByRole('navigation', { name: 'Inboxes' })
        .getByText('No inboxes yet. Send an email to any address and its inbox appears here automatically.'),
    ).toBeVisible()
    await expect(page.getByRole('heading', { name: 'No inbox selected' })).toBeVisible()
    await use(page)
  },
})

export { expect }
