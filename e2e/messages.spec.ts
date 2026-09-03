import type { Locator, Page, Request } from '@playwright/test'

import {
  expect,
  mailboxButton,
  mailboxMessagesResponse,
  messageRow,
  sendTestMessage,
  test,
} from './fixtures'


async function seedPlainMessages(
  page: Page,
  request: Parameters<typeof sendTestMessage>[0],
  recipient: string,
  subjects: readonly string[],
): Promise<void> {
  for (const subject of subjects) {
    await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
    await expect(messageRow(page, subject)).toBeVisible()
  }
  await expect(mailboxButton(page, recipient)).toHaveAccessibleName(
    `${recipient}, ${subjects.length} messages, ${subjects.length} unread`,
  )
}

async function rowsInCurrentOrder(page: Page): Promise<Locator[]> {
  const list = page.getByRole('list', { name: 'Messages' })
  const rows = list.getByRole('button')
  await expect(rows).toHaveCount(3)
  return [rows.nth(0), rows.nth(1), rows.nth(2)]
}

async function subjectFor(row: Locator): Promise<string> {
  const name = await row.getAttribute('aria-label')
  if (name === null) throw new Error('Message row has no accessible name')
  return name.split(', ')[1] ?? ''
}

async function expectMessageCount(page: Page, count: number): Promise<void> {
  const text = count === 0 ? 'No messages' : `${count} messages`
  await expect(page.getByRole('status').filter({ hasText: text })).toHaveText(text)
}
async function expectNoSearchResults(page: Page, query: string): Promise<void> {
  await expect(page.getByText(`No messages match "${query}".`, { exact: true })).toBeVisible()
}

async function expectUnread(row: Locator, unread: boolean): Promise<void> {
  await expect(row).toHaveAccessibleName(new RegExp(`, ${unread ? 'unread' : 'read'},`))
}

test('message rows distinguish subjects containing commas', async ({ page, request }) => {
  const recipient = 'message-row-subjects@hoomail.test'
  const exactSubject = 'Target'
  const longerSubject = 'Target, Sender, Other'

  await sendTestMessage(request, { to: recipient, subject: exactSubject, kind: 'plain' })
  await expect(messageRow(page, exactSubject)).toBeVisible()
  await sendTestMessage(request, { to: recipient, subject: longerSubject, kind: 'plain' })

  const exactRow = messageRow(page, exactSubject)
  await expect(exactRow).toBeVisible()
  await expect(exactRow).toHaveAccessibleName(/, Target, unread,/)
  await expect(messageRow(page, longerSubject)).toHaveAccessibleName(/, Target, Sender, Other, unread,/)
})

test('searches messages and supports keyboard navigation, selection, bulk actions, and range deletion', async ({
  page,
  request,
}) => {
  const recipient = 'message-workflow@hoomail.test'
  const subjects = ['Amber subject needle', 'Birch sender result', 'Cedar body result'] as const
  await seedPlainMessages(page, request, recipient, subjects)

  const search = page.getByRole('searchbox', { name: 'Search messages' })

  const subjectResponse = mailboxMessagesResponse(page, 'Amber subject needle')
  await search.fill('Amber subject needle')
  await subjectResponse
  await expect(messageRow(page, 'Amber subject needle')).toBeVisible()
  await expect(messageRow(page, 'Birch sender result')).toBeHidden()
  await expect(messageRow(page, 'Cedar body result')).toBeHidden()
  await expectMessageCount(page, 1)

  const senderResponse = mailboxMessagesResponse(page, 'owl@hoomail.local')
  await search.fill('owl@hoomail.local')
  await senderResponse
  for (const subject of subjects) await expect(messageRow(page, subject)).toBeVisible()
  await expectMessageCount(page, 3)

  const bodyResponse = mailboxMessagesResponse(page, recipient)
  await search.fill(recipient)
  await bodyResponse
  for (const subject of subjects) await expect(messageRow(page, subject)).toBeVisible()
  await expectMessageCount(page, 3)

  const clearedResponse = mailboxMessagesResponse(page)
  await page.getByRole('button', { name: 'Clear search' }).click()
  await clearedResponse
  await expect(search).toHaveValue('')
  for (const subject of subjects) await expect(messageRow(page, subject)).toBeVisible()
  await expectMessageCount(page, 3)

  let [firstRow, secondRow, thirdRow] = await rowsInCurrentOrder(page)
  await firstRow.focus()
  await expect(firstRow).toBeFocused()

  await firstRow.press('ArrowDown')
  await expect(secondRow).toBeFocused()
  await expect(secondRow).toHaveAttribute('aria-current', 'true')

  await secondRow.press('ArrowUp')
  await expect(firstRow).toBeFocused()
  await expect(firstRow).toHaveAttribute('aria-current', 'true')

  await firstRow.press('ControlOrMeta+A')
  await expect(page.getByText('3 selected', { exact: true })).toBeVisible()
  for (const row of [firstRow, secondRow, thirdRow]) await expect(row).toHaveAttribute('aria-pressed', 'true')

  await firstRow.press('Escape')
  await expect(page.getByText('3 selected', { exact: true })).toBeHidden()
  for (const row of [firstRow, secondRow, thirdRow]) await expect(row).toHaveAttribute('aria-pressed', 'false')

  await firstRow.press('ControlOrMeta+A')
  await page.getByRole('button', { name: 'Read', exact: true }).click()
  await expect(page.getByText('3 selected', { exact: true })).toBeHidden()
  for (const row of [firstRow, secondRow, thirdRow]) await expectUnread(row, false)
  await expect(mailboxButton(page, recipient)).toHaveAccessibleName(`${recipient}, 3 messages, 0 unread`)

  await firstRow.focus()
  await firstRow.press('ControlOrMeta+A')
  await page.getByRole('button', { name: 'Unread', exact: true }).click()
  await expect(page.getByText('3 selected', { exact: true })).toBeHidden()
  for (const row of [firstRow, secondRow, thirdRow]) await expectUnread(row, true)
  await expect(mailboxButton(page, recipient)).toHaveAccessibleName(`${recipient}, 3 messages, 3 unread`)

  ;[firstRow, secondRow, thirdRow] = await rowsInCurrentOrder(page)
  await firstRow.click()
  await expect(firstRow).toHaveAttribute('aria-current', 'true')
  await secondRow.click({ modifiers: ['ControlOrMeta'] })
  await thirdRow.click({ modifiers: ['Shift'] })
  await expect(firstRow).toHaveAttribute('aria-pressed', 'false')
  await expect(secondRow).toHaveAttribute('aria-pressed', 'true')
  await expect(thirdRow).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible()

  await page.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(secondRow).toBeHidden()
  await expect(thirdRow).toBeHidden()
  await expect(firstRow).toBeVisible()
  await expect(firstRow).toBeFocused()
  await expect(page.getByRole('list', { name: 'Messages' }).getByRole('button')).toHaveCount(1)
})

test('evicts inactive cache entries by recency while preserving active queries', async ({ page, request }) => {
  const recipient = 'inactive-cache@hoomail.test'
  const queries = Array.from({ length: 33 }, (_, index) => `inactive-cache-query-${index}`)
  const preservedQuery = queries[1]
  const untouchedQuery = queries[2]
  const preservedSubject = `Preserved ${preservedQuery}`
  const revalidationQuery = 'inactive-cache-query-revalidation'
  const activeSubject = `Active ${revalidationQuery}`
  await sendTestMessage(request, { to: recipient, subject: preservedSubject, kind: 'plain' })
  await expect(messageRow(page, preservedSubject)).toBeVisible()
  await sendTestMessage(request, { to: recipient, subject: activeSubject, kind: 'plain' })
  await expect(messageRow(page, activeSubject)).toBeVisible()

  const search = page.getByRole('searchbox', { name: 'Search messages' })
  for (const query of queries) {
    const response = mailboxMessagesResponse(page, query)
    await search.fill(query)
    await response
    if (query === preservedQuery) await expect(messageRow(page, preservedSubject)).toBeVisible()
    else await expectNoSearchResults(page, query)
  }

  await search.fill(preservedQuery)
  await expect(messageRow(page, preservedSubject)).toBeVisible()

  const revalidationResponse = mailboxMessagesResponse(page, revalidationQuery)
  await search.fill(revalidationQuery)
  await revalidationResponse
  await expect(messageRow(page, activeSubject)).toBeVisible()

  // The untouched query was evicted, so only the recently consumed query can
  // be revalidated by this event.
  const preservedRevalidation = mailboxMessagesResponse(page, preservedQuery)
  await sendTestMessage(request, { to: recipient, subject: 'Cache revalidation event', kind: 'plain' })
  await preservedRevalidation

  // Opening and closing the dialog forces an unrelated app render while this
  // query remains selected, so an active entry cannot be trimmed.
  await page.getByRole('button', { name: 'Send test', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Send a test email' })).toBeVisible()
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(messageRow(page, activeSubject)).toBeVisible()

  const evictionQuery = 'inactive-cache-query-eviction'
  const evictionResponse = mailboxMessagesResponse(page, evictionQuery)
  await search.fill(evictionQuery)
  await evictionResponse
  await expectNoSearchResults(page, evictionQuery)

  let preservedRequests = 0
  let untouchedRequests = 0
  const onRequest = (request: Request) => {
    const url = new URL(request.url())
    if (request.method() !== 'GET' || !/^\/api\/mailboxes\/\d+\/messages$/.test(url.pathname)) return
    if (url.searchParams.get('q') === preservedQuery) preservedRequests += 1
    if (url.searchParams.get('q') === untouchedQuery) untouchedRequests += 1
  }
  page.on('request', onRequest)
  try {
    await search.fill(preservedQuery)
    await expect(messageRow(page, preservedSubject)).toBeVisible()
    expect(preservedRequests).toBe(0)

    const untouchedResponse = mailboxMessagesResponse(page, untouchedQuery)
    await search.fill(untouchedQuery)
    await untouchedResponse
    await expectNoSearchResults(page, untouchedQuery)
    expect(untouchedRequests).toBeGreaterThan(0)
  } finally {
    page.off('request', onRequest)
  }
})

test('does not expose old search rows while a new query is pending', async ({ page, request }) => {
  const recipient = 'stale-search@hoomail.test'
  const oldSubject = 'Old query result'
  const newSubject = 'New query result'
  await sendTestMessage(request, { to: recipient, subject: oldSubject, kind: 'plain' })
  await expect(messageRow(page, oldSubject)).toBeVisible()
  await sendTestMessage(request, { to: recipient, subject: newSubject, kind: 'plain' })
  await expect(messageRow(page, newSubject)).toBeVisible()

  let release!: () => void
  let requestStarted!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  const started = new Promise<void>((resolve) => { requestStarted = resolve })
  await page.route('**/api/mailboxes/*/messages*', async (route) => {
    const url = new URL(route.request().url())
    if (url.searchParams.get('q') === newSubject) {
      requestStarted()
      await pending
      await route.continue()
      return
    }
    await route.continue()
  })

  const search = page.getByRole('searchbox', { name: 'Search messages' })
  await search.fill(newSubject)
  await started
  await expect(messageRow(page, oldSubject)).toBeHidden()
  await expect(page.getByRole('list', { name: 'Messages' }).getByRole('button')).toHaveCount(0)
  release()
  await expect(messageRow(page, newSubject)).toBeVisible()
})

test('preserves a new selection when an old bulk action completes after mailbox change', async ({ page, request }) => {
  const firstRecipient = 'bulk-old@hoomail.test'
  const secondRecipient = 'bulk-new@hoomail.test'
  const firstSubject = 'Old bulk selection'
  const oldSecondSubject = 'Another old bulk selection'
  const secondSubject = 'New bulk selection'
  const thirdSubject = 'Another new bulk selection'
  await sendTestMessage(request, { to: firstRecipient, subject: firstSubject, kind: 'plain' })
  await expect(messageRow(page, firstSubject)).toBeVisible()
  await sendTestMessage(request, { to: firstRecipient, subject: oldSecondSubject, kind: 'plain' })
  await expect(messageRow(page, oldSecondSubject)).toBeVisible()
  await sendTestMessage(request, { to: secondRecipient, subject: secondSubject, kind: 'plain' })
  await sendTestMessage(request, { to: secondRecipient, subject: thirdSubject, kind: 'plain' })
  const secondMailbox = mailboxButton(page, secondRecipient)
  await expect(secondMailbox).toBeVisible()

  let release!: () => void
  let actionStarted!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  const started = new Promise<void>((resolve) => { actionStarted = resolve })
  await page.route('**/api/messages/actions', async (route) => {
    actionStarted()
    await pending
    await route.fulfill({ status: 200, contentType: 'application/json', body: '{}' })
  })

  const oldRow = messageRow(page, firstSubject)
  await oldRow.focus()
  await oldRow.press('ControlOrMeta+A')
  await page.getByRole('button', { name: 'Read', exact: true }).click()
  await started

  await secondMailbox.click()
  const newRow = messageRow(page, secondSubject)
  const anotherNewRow = messageRow(page, thirdSubject)
  await expect(newRow).toBeVisible()
  await newRow.click({ modifiers: ['ControlOrMeta'] })
  await anotherNewRow.click({ modifiers: ['ControlOrMeta'] })
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible()
  release()
  await expect(page.getByText('2 selected', { exact: true })).toBeVisible()
  await expect(newRow).toHaveAttribute('aria-pressed', 'true')
  await expect(anotherNewRow).toHaveAttribute('aria-pressed', 'true')
})

test('supports keyboard context-menu navigation, dismissal, and single-row deletion', async ({ page, request }) => {
  const recipient = 'message-menu@hoomail.test'
  const subjects = ['Dawn menu row', 'Elm menu row', 'Frost menu row'] as const
  await seedPlainMessages(page, request, recipient, subjects)

  const [triggerRow, nextRow, finalRow] = await rowsInCurrentOrder(page)
  const triggerSubject = await subjectFor(triggerRow)
  const nextSubject = await subjectFor(nextRow)
  const finalSubject = await subjectFor(finalRow)
  await triggerRow.focus()
  await triggerRow.press('Shift+F10')

  const menu = page.getByRole('menu')
  const markRead = menu.getByRole('menuitem', { name: 'Mark as read' })
  const markUnread = menu.getByRole('menuitem', { name: 'Mark as unread' })
  const deleteItem = menu.getByRole('menuitem', { name: 'Delete', exact: true })
  await expect(markRead).toBeFocused()

  await markRead.press('ArrowDown')
  await expect(markUnread).toBeFocused()
  await markUnread.press('ArrowUp')
  await expect(markRead).toBeFocused()
  await markRead.press('End')
  await expect(deleteItem).toBeFocused()
  await deleteItem.press('Home')
  await expect(markRead).toBeFocused()

  await markRead.press('Escape')
  await expect(menu).toBeHidden()
  await expect(triggerRow).toBeFocused()

  await triggerRow.press('Shift+F10')
  await expect(markRead).toBeFocused()
  await markRead.press('Tab')
  await expect(menu).toBeHidden()
  await expect(nextRow).toBeFocused()

  await triggerRow.focus()
  await triggerRow.press('Shift+F10')
  await expect(markRead).toBeFocused()
  await markRead.press('End')
  await expect(deleteItem).toBeFocused()
  await deleteItem.press('Enter')

  await expect(messageRow(page, triggerSubject)).toHaveCount(0)
  await expect(messageRow(page, nextSubject)).toBeVisible()
  await expect(messageRow(page, finalSubject)).toBeVisible()
  await expect(page.getByRole('list', { name: 'Messages' }).getByRole('button')).toHaveCount(2)
  await expect(mailboxButton(page, recipient)).toHaveAccessibleName(`${recipient}, 2 messages, 2 unread`)
})
