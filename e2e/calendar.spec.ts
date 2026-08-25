import { createConnection } from 'node:net'
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

// Raw SMTP delivery mirroring e2e/viewer.spec.ts: the built-in /api/send-test
// kinds only produce timed events, so the all-day VALUE=DATE fixture is mailed
// directly through the harness SMTP server.
async function sendRawMessage(raw: string, recipient: string): Promise<void> {
  const socket = createConnection({ host: '127.0.0.1', port: Number(process.env.HOOMAIL_E2E_SMTP_PORT ?? '33125') })
  socket.setEncoding('utf8')
  let buffer = ''
  const waitForReply = () => new Promise<string>((resolve, reject) => {
    const onData = (chunk: string) => {
      buffer += chunk
      const lines = buffer.split('\r\n')
      buffer = lines.pop() ?? ''
      let final: string | undefined
      for (let index = lines.length - 1; index >= 0; index -= 1) {
        const line: string = lines[index]
        if (!/^\d{3} /.test(line)) continue
        final = line
        break
      }
      if (!final) return
      socket.off('data', onData)
      resolve(final)
    }
    socket.on('data', onData)
    socket.once('error', reject)
  })
  const command = async (value: string) => {
    socket.write(`${value}\r\n`)
    const reply = await waitForReply()
    if (!/^[23]/.test(reply)) throw new Error(`SMTP rejected ${value}: ${reply}`)
  }
  await waitForReply()
  await command('EHLO calendar.test')
  await command('MAIL FROM:<owl@example.test>')
  await command(`RCPT TO:<${recipient}>`)
  await command('DATA')
  socket.write(`${raw.replace(/\r?\n/g, '\r\n')}\r\n.\r\n`)
  const reply = await waitForReply()
  if (!/^250 /.test(reply)) throw new Error(`SMTP rejected message: ${reply}`)
  socket.end('QUIT\r\n')
}

/**
 * Epoch milliseconds of local midnight for a wall-clock calendar day in an IANA
 * timezone, matching how the browser keys grid cells (`startOfDay`) while that
 * timezone is active. Two offset refinements converge except across DST shifts.
 */
function zonedDayStartEpoch(year: number, monthIndex: number, day: number, timeZone: string): number {
  const wallUtc = (utcMs: number): number => {
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone,
      hourCycle: 'h23',
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }).formatToParts(new Date(utcMs))
    const get = (type: string): number => Number(parts.find((part) => part.type === type)?.value)
    return Date.UTC(get('year'), get('month') - 1, get('day'), get('hour'), get('minute'), get('second'))
  }
  const naive = Date.UTC(year, monthIndex, day)
  const guess = naive - (wallUtc(naive) - naive)
  return guess - (wallUtc(guess) - naive)
}

test.describe('all-day invitations stay on their UTC day west of UTC', () => {
  // playwright.config pins timezoneId:'UTC' globally; this context runs pinned
  // seven hours west so naive local rendering would drift the event to Aug 24.
  test.use({ timezoneId: 'America/Los_Angeles' })

  test('renders a VALUE=DATE event on August 25 instead of drifting to August 24', async ({ page }) => {
    const recipient = 'calendar-allday@hoomail.test'
    const title = 'All-day UTC drift probe'
    const raw = [
      'From: "The hoomail Owl" <owl@example.test>',
      `To: ${recipient}`,
      `Subject: ${title}`,
      'MIME-Version: 1.0',
      'Content-Type: multipart/mixed; boundary="allday-probe"',
      '',
      '--allday-probe',
      'Content-Type: text/plain; charset=utf-8',
      '',
      'You have been invited to an all-day event.',
      '--allday-probe',
      'Content-Type: text/calendar; method=REQUEST; charset=utf-8; name=invite.ics',
      'Content-Disposition: attachment; filename=invite.ics',
      '',
      'BEGIN:VCALENDAR',
      'PRODID:-//hoomail//e2e all-day probe//EN',
      'VERSION:2.0',
      'METHOD:REQUEST',
      'BEGIN:VEVENT',
      'UID:allday-utc-drift-probe@example.test',
      'SEQUENCE:0',
      'DTSTAMP:20260820T120000Z',
      'DTSTART;VALUE=DATE:20260825',
      'DTEND;VALUE=DATE:20260826',
      'SUMMARY:All-day UTC drift probe',
      'END:VEVENT',
      'END:VCALENDAR',
      '--allday-probe--',
    ].join('\n')

    await sendRawMessage(raw, recipient)
    await expect(messageRow(page, title)).toBeVisible()

    const eventsResponse = mailboxEventsResponse(page)
    await page.getByRole('button', { name: 'Calendar', exact: true }).click()
    await eventsResponse

    const aug25Epoch = zonedDayStartEpoch(2026, 7, 25, 'America/Los_Angeles')
    const aug24Epoch = zonedDayStartEpoch(2026, 7, 24, 'America/Los_Angeles')

    const aug25Cell = page.locator(`[data-calendar-day="${aug25Epoch}"]`)
    await expect(aug25Cell).toHaveCount(1)
    await expect(aug25Cell).toHaveAttribute('aria-label', /^8\/25\/2026 — 1 events$/)
    await expect(aug25Cell).toContainText(title)

    const aug24Cell = page.locator(`[data-calendar-day="${aug24Epoch}"]`)
    await expect(aug24Cell).toHaveAttribute('aria-label', /^8\/24\/2026 — 0 events$/)
    await expect(aug24Cell).not.toContainText(title)

    await aug25Cell.click()
    const eventCard = page
      .getByLabel('Events for selected day')
      .getByRole('button', { name: `${title}, All day` })
    await expect(eventCard).toHaveCount(1)
    await eventCard.click()

    await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${title}` })).toBeVisible()
    // invite-card.tsx formats all-day ranges with timeZone:'UTC'; without it the
    // Los Angeles browser would render "Mon, Aug 24, 2026 (all day)" here.
    await expect(page.getByText('Tue, Aug 25, 2026 (all day)')).toBeVisible()
    await expect(page.getByText('Mon, Aug 24, 2026 (all day)')).toHaveCount(0)
  })
})
