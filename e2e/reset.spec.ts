import { expect, mailboxButton, messageRow, sendTestMessage, test } from './fixtures'

test('resets all inboxes and restores the empty application state', async ({ page, request }) => {
  const firstRecipient = 'reset-first@hoomail.test'
  const secondRecipient = 'reset-second@hoomail.test'
  const firstSubject = 'Reset seed first inbox'
  const secondSubject = 'Reset seed second inbox'

  await sendTestMessage(request, {
    to: firstRecipient,
    subject: firstSubject,
    kind: 'plain',
  })
  await expect(mailboxButton(page, firstRecipient)).toBeVisible()
  await expect(messageRow(page, firstSubject)).toBeVisible()

  await sendTestMessage(request, {
    to: secondRecipient,
    subject: secondSubject,
    kind: 'plain',
  })
  const firstInbox = mailboxButton(page, firstRecipient)
  const secondInbox = mailboxButton(page, secondRecipient)
  await expect(secondInbox).toBeVisible()
  await secondInbox.click()
  await expect(messageRow(page, secondSubject)).toBeVisible()

  const resetButton = page.getByRole('button', { name: 'Reset', exact: true })
  const resetDialog = page.getByRole('dialog', { name: 'Reset hoomail?' })
  const cancelButton = resetDialog.getByRole('button', { name: 'Cancel' })
  const closeButton = resetDialog.getByRole('button', { name: 'Close' })

  await resetButton.click()
  await expect(resetDialog).toBeVisible()
  await expect(cancelButton).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(closeButton).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(cancelButton).toBeFocused()
  await cancelButton.click()
  await expect(resetDialog).toBeHidden()
  await expect(resetButton).toBeFocused()

  await resetButton.click()
  await expect(resetDialog).toBeVisible()
  await expect(cancelButton).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(resetDialog).toBeHidden()
  await expect(resetButton).toBeFocused()

  await resetButton.click()
  await expect(resetDialog).toBeVisible()
  const mailboxesRevalidated = page.waitForResponse((response) => {
    const url = new URL(response.url())
    return response.status() === 200 && response.request().method() === 'GET' && url.pathname === '/api/mailboxes'
  })
  await resetDialog.getByRole('button', { name: 'Wipe everything' }).click()

  const mailboxResponse = await mailboxesRevalidated
  await expect(resetDialog).toBeHidden()
  expect(await mailboxResponse.json()).toEqual({ mailboxes: [] })

  await expect(firstInbox).toHaveCount(0)
  await expect(secondInbox).toHaveCount(0)
  await expect(page.getByRole('button', { name: /^The hoomail Owl,/ })).toHaveCount(0)
  await expect(
    page.getByText('No inboxes yet. Send an email to any address and its inbox appears here automatically.'),
  ).toBeVisible()
  await expect(page.getByRole('heading', { name: 'No inbox selected' })).toBeVisible()

  const apiResponse = await request.get('/api/mailboxes')
  expect(apiResponse.status()).toBe(200)
  expect(await apiResponse.json()).toEqual({ mailboxes: [] })
})
