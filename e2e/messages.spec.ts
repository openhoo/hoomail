import type { Locator, Page } from '@playwright/test'

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

async function expectUnread(row: Locator, unread: boolean): Promise<void> {
  await expect(row).toHaveAccessibleName(new RegExp(`, ${unread ? 'unread' : 'read'},`))
}

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
