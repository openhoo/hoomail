import { expect, mailboxButton, messageRow, sendTestMessage, test } from './fixtures'

test('reports an inbox load failure and retries instead of claiming the store is empty', async ({ page }) => {
  let unavailable = true
  await page.route('**/api/mailboxes', async (route) => {
    if (unavailable) await route.fulfill({ status: 503, body: 'Unavailable' })
    else await route.continue()
  })
  await page.reload()
  const inboxes = page.getByRole('navigation', { name: 'Inboxes' })
  await expect(inboxes.getByRole('alert')).toContainText('Could not load inboxes.')
  await expect(inboxes.getByText('No inboxes yet.', { exact: false })).toBeHidden()
  unavailable = false
  await inboxes.getByRole('button', { name: 'Retry inboxes' }).click()
  await expect(inboxes.getByRole('alert')).toBeHidden()
  await expect(inboxes.getByText('No inboxes yet.', { exact: false })).toBeVisible()
})

test('reports message and calendar failures and recovers through visible retries', async ({ page, request }) => {
  const recipient = 'failure-recovery@hoomail.test'
  const subject = 'Recovered invitation'
  let messagesUnavailable = true
  let calendarUnavailable = true
  await page.route('**/api/mailboxes/*/messages*', async (route) => {
    if (messagesUnavailable) await route.fulfill({ status: 503, body: 'Unavailable' })
    else await route.continue()
  })
  await page.route('**/api/mailboxes/*/events', async (route) => {
    if (calendarUnavailable) await route.fulfill({ status: 503, body: 'Unavailable' })
    else await route.continue()
  })
  await sendTestMessage(request, { to: recipient, subject, kind: 'invite' })
  const list = page.locator('[data-message-list-shell]')
  await expect(list.getByRole('alert')).toContainText('Could not load messages.')
  await expect(list.getByText('No mail yet', { exact: false })).toBeHidden()
  messagesUnavailable = false
  await list.getByRole('button', { name: 'Retry messages' }).click()
  await expect(messageRow(page, `Invitation: ${subject}`)).toBeVisible()
  await expect(list.getByRole('alert')).toBeHidden()

  await page.getByRole('button', { name: 'Calendar', exact: true }).click()
  const calendar = page.getByRole('region', { name: 'Calendar', exact: true })
  await expect(calendar.getByRole('alert')).toContainText('Could not load calendar.')
  await expect(calendar.getByText('No events on this day.')).toBeHidden()
  calendarUnavailable = false
  await calendar.getByRole('button', { name: 'Retry calendar' }).click()
  await expect(calendar.getByRole('alert')).toBeHidden()
  await expect(calendar.getByText(subject)).toBeVisible()
})

for (const searchActive of [false, true]) test(`does not retain a deleted reader or bulk selection after an external deletion${searchActive ? ' while searching' : ''}`, async ({ page, request }) => {
  const recipient = 'external-delete@hoomail.test'
  const subject = 'Deleted from another client'
  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  const messageId = Number(await row.getAttribute('data-message-id'))
  await row.click()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  if (searchActive) {
    await page.getByRole('searchbox', { name: 'Search messages' }).fill('another client')
    await expect(row).toBeVisible()
    await row.focus()
  }
  await row.press('ControlOrMeta+a')
  await expect(page.getByText('1 selected')).toBeVisible()

  const response = await request.post('/api/messages/actions', { data: { action: 'delete', ids: [messageId] } })
  expect(response.status()).toBe(200)
  await expect(row).toHaveCount(0)
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeHidden()
  await expect(page.getByText('1 selected')).toBeHidden()
  await expect(page.getByText('Select a message to read it here.')).toBeVisible()
})

test('reports failed message and inbox deletions while preserving the records', async ({ page, request }) => {
  const recipient = 'failed-deletion@hoomail.test'
  const subject = 'Deletion failure survivor'
  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await page.route('**/api/messages/actions', (route) => route.fulfill({ status: 503, body: 'Unavailable' }))
  await row.focus()
  await row.press('Delete')
  await expect(page.getByRole('alert')).toContainText('Could not delete messages.')
  await expect(row).toBeVisible()

  await page.route('**/api/mailboxes/*', (route) => route.request().method() === 'DELETE'
    ? route.abort('connectionfailed')
    : route.continue())
  await mailboxButton(page, recipient).focus()
  await mailboxButton(page, recipient).press('Shift+F10')
  await page.getByRole('menuitem', { name: 'Delete inbox', exact: true }).click()
  await expect(page.getByRole('alert').filter({ hasText: 'Could not delete inbox.' })).toBeVisible()
  await expect(mailboxButton(page, recipient)).toBeVisible()
})

test('retries failed raw-source and text-attachment previews without reopening the message', async ({ page, request }) => {
  const subject = 'Preview recovery'
  await sendTestMessage(request, { to: 'preview-recovery@hoomail.test', subject, kind: 'plain' })
  await messageRow(page, subject).click()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  let sourceUnavailable = true
  let attachmentUnavailable = true
  await page.route('**/api/messages/*/source', (route) => sourceUnavailable
    ? route.fulfill({ status: 503, body: 'Unavailable' })
    : route.continue())
  await page.route('**/api/attachments/*', (route) => attachmentUnavailable
    ? route.fulfill({ status: 503, body: 'Unavailable' })
    : route.continue())
  await page.getByRole('tab', { name: 'Source', exact: true }).click()
  await expect(page.getByRole('alert')).toContainText('Could not load raw message.')
  sourceUnavailable = false
  await page.getByRole('button', { name: 'Retry raw message' }).click()
  await expect(page.getByLabel('Raw message source')).toContainText(`Subject: ${subject}`)

  await page.getByRole('button', { name: 'Preview hoot.txt' }).click()
  const preview = page.getByRole('dialog', { name: /hoot\.txt/ })
  await expect(preview.getByRole('alert')).toContainText('Could not load preview.')
  attachmentUnavailable = false
  await preview.getByRole('button', { name: 'Retry attachment preview' }).click()
  await expect(preview).toContainText('This is a sample attachment from the hoomail test sender.')
  await expect(preview.getByRole('alert')).toBeHidden()
})

test('native SSE reconnect refetches messages and mailboxes when reset reuses their IDs', async ({ page, request }) => {
  const oldRecipient = 'before-reconnect@hoomail.test'
  const oldSubject = 'Before missed reset'
  const newRecipient = 'after-reconnect@hoomail.test'
  const newSubject = 'After missed reset'
  await sendTestMessage(request, { to: oldRecipient, subject: oldSubject, kind: 'plain' })
  await expect(messageRow(page, oldSubject)).toBeVisible()

  let permitReconnect!: () => void
  const reconnectPermission = new Promise<void>((resolve) => { permitReconnect = resolve })
  let sawReconnect!: () => void
  const reconnectStarted = new Promise<void>((resolve) => { sawReconnect = resolve })
  let eventRequests = 0
  await page.route('**/api/events', async (route) => {
    eventRequests += 1
    if (eventRequests === 1) {
      // End a valid stream to trigger EventSource's native reconnection. While
      // disconnected, reset and delivery events are deliberately missed.
      await route.fulfill({ contentType: 'text/event-stream', body: 'data: {"type":"connected"}\n\n' })
      return
    }
    sawReconnect()
    await reconnectPermission
    await route.continue()
  })
  try {
    await page.reload()
    await messageRow(page, oldSubject).click()
    await expect(page.getByRole('heading', { name: oldSubject, exact: true })).toBeVisible()
    await page.getByRole('tab', { name: 'Source', exact: true }).click()
    await expect(page.getByLabel('Raw message source')).toContainText(`Subject: ${oldSubject}`)
    expect((await request.post('/api/reset')).status()).toBe(200)
    await sendTestMessage(request, { to: newRecipient, subject: newSubject, kind: 'plain' })
    await expect(page.getByRole('heading', { name: oldSubject, exact: true })).toBeVisible()
    await reconnectStarted
    permitReconnect()
    await expect(mailboxButton(page, newRecipient)).toBeVisible()
    await expect(mailboxButton(page, oldRecipient)).toHaveCount(0)
    await expect(messageRow(page, newSubject)).toBeVisible()
    await expect(page.getByRole('heading', { name: newSubject, exact: true })).toBeVisible()
    await expect(page.getByLabel('Raw message source')).toContainText(`Subject: ${newSubject}`)
    await expect(page.getByLabel('Raw message source')).not.toContainText(`Subject: ${oldSubject}`)
  } finally {
    permitReconnect()
  }
})

test('retrying an inbox refresh preserves the opened reader and bulk selection until the response arrives', async ({ page, request }) => {
  const recipient = 'retry-context@hoomail.test'
  const subject = 'Keep context during retry'
  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
  const row = messageRow(page, subject)
  await row.click()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  await row.press('ControlOrMeta+a')
  await expect(page.getByText('1 selected')).toBeVisible()
  let unavailable = true
  let releaseRetry!: () => void
  const retryPermission = new Promise<void>((resolve) => { releaseRetry = resolve })
  let retryStarted!: () => void
  const retryRequest = new Promise<void>((resolve) => { retryStarted = resolve })
  await page.route('**/api/mailboxes', async (route) => {
    if (unavailable) await route.fulfill({ status: 503, body: 'Unavailable' })
    else {
      retryStarted()
      await retryPermission
      await route.continue()
    }
  })
  try {
    await sendTestMessage(request, { to: recipient, subject: 'Triggers background refresh', kind: 'plain' })
    const inboxes = page.getByRole('navigation', { name: 'Inboxes' })
    await expect(inboxes.getByRole('alert')).toContainText('Could not load inboxes.')
    unavailable = false
    await inboxes.getByRole('button', { name: 'Retry inboxes' }).click()
    await retryRequest
    await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
    await expect(page.getByText('1 selected')).toBeVisible()
    await expect(mailboxButton(page, recipient)).toBeVisible()
    releaseRetry()
    await expect(inboxes.getByRole('status')).toBeHidden()
    await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
    await expect(page.getByText('1 selected')).toBeVisible()
  } finally {
    releaseRetry()
  }
})

test('search exposes and retries an unfiltered inbox failure needed to reconcile a deleted reader', async ({ page, request }) => {
  const recipient = 'unfiltered-recovery@hoomail.test'
  const deletedSubject = 'Opened message outside search'
  const survivingSubject = 'Search survivor'
  await sendTestMessage(request, { to: recipient, subject: deletedSubject, kind: 'plain' })
  const openedRow = messageRow(page, deletedSubject)
  await openedRow.click()
  await expect(page.getByRole('heading', { name: deletedSubject, exact: true })).toBeVisible()
  const deletedId = Number(await openedRow.getAttribute('data-message-id'))
  await sendTestMessage(request, { to: recipient, subject: survivingSubject, kind: 'plain' })
  await expect(messageRow(page, survivingSubject)).toBeVisible()
  await page.getByRole('searchbox', { name: 'Search messages' }).fill('survivor')
  await expect(messageRow(page, deletedSubject)).toHaveCount(0)
  let unavailable = true
  await page.route('**/api/mailboxes/*/messages*', async (route) => {
    if (unavailable && !new URL(route.request().url()).searchParams.has('q')) {
      await route.fulfill({ status: 503, body: 'Unavailable' })
    } else await route.continue()
  })
  const deletion = await request.post('/api/messages/actions', { data: { action: 'delete', ids: [deletedId] } })
  expect(deletion.status()).toBe(200)
  const list = page.locator('[data-message-list-shell]')
  await expect(list.getByRole('alert')).toContainText('Could not load messages.')
  await expect(messageRow(page, survivingSubject)).toBeVisible()
  unavailable = false
  await list.getByRole('button', { name: 'Retry messages' }).click()
  await expect(list.getByRole('alert')).toBeHidden()
  await expect(page.getByRole('heading', { name: deletedSubject, exact: true })).toBeHidden()
  await expect(page.getByText('Select a message to read it here.')).toBeVisible()
  await expect(messageRow(page, survivingSubject)).toBeVisible()
})
