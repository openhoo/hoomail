import type { Locator } from '@playwright/test'
import { expect, mailboxEventsResponse, messageRow, sendTestMessage, test } from './fixtures'

const DAY_MS = 24 * 60 * 60 * 1000
const monthFormatter = new Intl.DateTimeFormat('en-US', {
  month: 'long',
  year: 'numeric',
  timeZone: 'UTC',
})

function monthLabel(offset: number): string {
  const now = new Date()
  return monthFormatter.format(new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + offset, 1)))
}

async function calendarDayEpoch(cell: Locator): Promise<number> {
  const value = await cell.getAttribute('data-calendar-day')
  expect(value).not.toBeNull()
  const epoch = Number(value)
  expect(Number.isFinite(epoch)).toBe(true)
  return epoch
}

test('replaces an invitation with its update and cancellation', async ({ page, request }) => {
  const recipient = 'calendar@hoomail.test'
  const title = 'Owl planning'

  await sendTestMessage(request, { to: recipient, subject: title, kind: 'invite' })
  await expect(messageRow(page, `Invitation: ${title}`)).toBeVisible()

  await sendTestMessage(request, { to: recipient, subject: title, kind: 'update' })
  await expect(messageRow(page, `Updated invitation: ${title}`)).toBeVisible()

  await sendTestMessage(request, { to: recipient, subject: title, kind: 'cancellation' })
  await expect(messageRow(page, `Cancelled: ${title}`)).toBeVisible()

  const eventsResponse = mailboxEventsResponse(page)
  await page.getByRole('button', { name: 'Calendar', exact: true }).click()
  await eventsResponse

  const eventDay = page.getByRole('gridcell', { name: /— 1 events$/ })
  await expect(eventDay).toHaveCount(1)
  await eventDay.click()

  const selectedDayEvents = page.getByLabel('Events for selected day')
  const eventCard = selectedDayEvents.getByRole('button', {
    name: /Owl planning, 11:00 AM – 11:30 AM, Owl Tree Conference Room/,
  })
  await expect(eventCard).toHaveCount(1)
  await expect(eventCard).toContainText('cancelled')
  await expect(eventCard).toContainText('Owl planning')
  await expect(eventCard).toContainText('11:00 AM – 11:30 AM')
  await expect(eventCard).toContainText('Owl Tree Conference Room')

  await eventCard.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: Cancelled: ${title}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: `Cancelled: ${title}` })).toBeVisible()
  await expect(page.getByText('Cancellation', { exact: true })).toBeVisible()
  await expect(page.getByText('seq 2', { exact: true })).toBeVisible()
})

test('moves calendar focus across days and months and opens the source message', async ({ page, request }) => {
  const recipient = 'calendar-grid@hoomail.test'
  const title = 'Grid navigation'
  await sendTestMessage(request, { to: recipient, subject: title, kind: 'invite' })
  await expect(messageRow(page, `Invitation: ${title}`)).toBeVisible()

  const eventsResponse = mailboxEventsResponse(page)
  await page.getByRole('button', { name: 'Calendar', exact: true }).click()
  await eventsResponse

  const calendar = page.getByRole('region', { name: 'Calendar' })
  let eventDay = page.getByRole('gridcell', { name: /— 1 events$/ })
  await expect(eventDay).toHaveCount(1)
  await eventDay.click()
  const initialEpoch = await calendarDayEpoch(eventDay)
  await expect(eventDay).toBeFocused()
  await expect(eventDay).toHaveAttribute('aria-selected', 'true')

  await eventDay.press('ArrowRight')
  let focusedDay = page.locator('[data-calendar-day]:focus')
  await expect(focusedDay).toHaveCount(1)
  const rightEpoch = await calendarDayEpoch(focusedDay)
  expect(rightEpoch - initialEpoch).toBe(DAY_MS)
  await expect(focusedDay).toHaveAttribute('aria-selected', 'true')

  await focusedDay.press('ArrowDown')
  focusedDay = page.locator('[data-calendar-day]:focus')
  await expect(focusedDay).toHaveCount(1)
  const downEpoch = await calendarDayEpoch(focusedDay)
  expect(downEpoch - rightEpoch).toBe(7 * DAY_MS)
  await expect(focusedDay).toHaveAttribute('aria-selected', 'true')

  const renderedEpochs = await page.locator('[data-calendar-day]').evaluateAll((cells) =>
    cells.map((cell) => Number(cell.getAttribute('data-calendar-day'))),
  )
  expect(renderedEpochs).toHaveLength(42)
  const earliestEpoch = Math.min(...renderedEpochs)
  const earliestDay = page.locator(`[data-calendar-day="${earliestEpoch}"]`)
  await earliestDay.click()
  await expect(earliestDay).toBeFocused()

  await earliestDay.press('ArrowLeft')
  const previousEpoch = earliestEpoch - DAY_MS
  const previousDay = page.locator(`[data-calendar-day="${previousEpoch}"]`)
  await expect(previousDay).toBeFocused()
  await expect(previousDay).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('[data-calendar-day][aria-selected="true"]')).toHaveCount(1)

  const currentMonth = monthLabel(0)
  const previousMonth = monthLabel(-1)
  const nextMonth = monthLabel(1)
  await page.getByRole('button', { name: 'Today', exact: true }).click()
  await expect(calendar.getByRole('heading', { name: currentMonth })).toBeVisible()

  await page.getByRole('button', { name: 'Next month' }).click()
  await expect(calendar.getByRole('heading', { name: nextMonth })).toBeVisible()

  await page.getByRole('button', { name: 'Previous month' }).click()
  await expect(calendar.getByRole('heading', { name: currentMonth })).toBeVisible()

  await page.getByRole('button', { name: 'Previous month' }).click()
  await expect(calendar.getByRole('heading', { name: previousMonth })).toBeVisible()

  await page.getByRole('button', { name: 'Today', exact: true }).click()
  await expect(calendar.getByRole('heading', { name: currentMonth })).toBeVisible()

  eventDay = page.getByRole('gridcell', { name: /— 1 events$/ })
  await expect(eventDay).toHaveCount(1)
  await eventDay.click()
  const eventCard = page.getByLabel('Events for selected day').getByRole('button', {
    name: /Grid navigation, 10:00 AM – 10:30 AM, Owl Tree Conference Room/,
  })
  await expect(eventCard).toBeVisible()
  await eventCard.click()

  await expect(page.getByRole('button', { name: 'Mail', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: Invitation: ${title}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: `Invitation: ${title}` })).toBeVisible()
  await expect(messageRow(page, `Invitation: ${title}`)).toBeFocused()
})
