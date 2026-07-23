import { expect, mailboxButton, messageRow, sendTestMessage, test } from './fixtures'

test('deletes inboxes from keyboard context menus and preserves focus', async ({ page, request }) => {
  const firstRecipient = 'delete-first@hoomail.test'
  const firstSubject = 'Mailbox deletion first'
  const survivorRecipient = 'delete-survivor@hoomail.test'
  const survivorSubject = 'Mailbox deletion survivor'

  await sendTestMessage(request, {
    to: firstRecipient,
    subject: firstSubject,
    kind: 'plain',
  })
  const firstMailbox = mailboxButton(page, firstRecipient)
  await expect(firstMailbox).toBeVisible()
  await expect(messageRow(page, firstSubject)).toBeVisible()

  await sendTestMessage(request, {
    to: survivorRecipient,
    subject: survivorSubject,
    kind: 'plain',
  })
  const survivorMailbox = mailboxButton(page, survivorRecipient)
  await expect(survivorMailbox).toBeVisible()

  await survivorMailbox.focus()
  await page.keyboard.press('Enter')
  await expect(survivorMailbox).toHaveAttribute('aria-current', 'true')
  await expect(messageRow(page, survivorSubject)).toBeVisible()

  await firstMailbox.focus()
  await page.keyboard.press('Enter')
  await expect(firstMailbox).toHaveAttribute('aria-current', 'true')
  await expect(messageRow(page, firstSubject)).toBeVisible()

  await page.keyboard.press('Shift+F10')
  const deleteInbox = page.getByRole('menuitem', { name: 'Delete inbox' })
  await expect(deleteInbox).toBeFocused()
  await page.keyboard.press('Enter')

  await expect(firstMailbox).toHaveCount(0)
  await expect(survivorMailbox).toBeFocused()
  await expect(survivorMailbox).toHaveAttribute('aria-current', 'true')
  await expect(messageRow(page, survivorSubject)).toBeVisible()
  await expect(messageRow(page, firstSubject)).toHaveCount(0)

  await page.keyboard.press('Shift+F10')
  await expect(deleteInbox).toBeFocused()
  await page.keyboard.press('Enter')

  await expect(survivorMailbox).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Send test' })).toBeFocused()
  await expect(page.getByText('No inboxes yet. Send an email to any address and its inbox appears here automatically.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'No inbox selected' })).toBeVisible()
  await expect(page.getByRole('list', { name: 'Messages' }).getByRole('button')).toHaveCount(0)
})
