import { expect, mailboxButton, messageRow, sendTestMessage, test } from './fixtures'

test('phone navigation preserves the reader across viewport changes and exposes message actions', async ({ page, request }) => {
  await sendTestMessage(request, { to: 'phone@example.com', subject: 'Phone reader', kind: 'plain' })
  await mailboxButton(page, 'phone@example.com').click()
  await page.setViewportSize({ width: 390, height: 844 })
  await messageRow(page, 'Phone reader').click()
  const frame = page.getByTitle('Email HTML content', { exact: true })
  await expect(frame).toBeVisible()
  const originalFrame = await frame.elementHandle()
  await expect(page.getByRole('searchbox', { name: 'Search messages' })).toBeHidden()

  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page.getByRole('searchbox', { name: 'Search messages' })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await frame.evaluate((element, original) => element === original, originalFrame)).toBe(true)
  await page.getByRole('button', { name: 'Back to messages', exact: true }).click()
  await expect(messageRow(page, 'Phone reader')).toBeFocused()
  await page.getByRole('button', { name: 'Message actions for The hoomail Owl, Phone reader' }).click()
  await page.getByRole('menuitem', { name: 'Mark as unread', exact: true }).click()
  await expect(messageRow(page, 'Phone reader')).toHaveAttribute('aria-label', /unread/)

  await page.getByRole('button', { name: 'Inboxes', exact: true }).click()
  await expect(mailboxButton(page, 'phone@example.com')).toBeVisible()
  await mailboxButton(page, 'phone@example.com').click()
  await expect(messageRow(page, 'Phone reader')).toBeVisible()
})

test('short landscape keeps calendar days separate and dialog actions reachable', async ({ page, request }) => {
  await sendTestMessage(request, { to: 'landscape@example.com', subject: 'Landscape meeting', kind: 'invite' })
  await mailboxButton(page, 'landscape@example.com').click()
  await page.setViewportSize({ width: 844, height: 390 })
  await page.getByRole('button', { name: 'Calendar', exact: true }).click()
  const cells = page.getByRole('gridcell')
  await expect(cells).toHaveCount(42)
  const bounds = await cells.evaluateAll(elements => elements.map(element => {
    const { top, bottom } = element.getBoundingClientRect()
    return { top, bottom }
  }))
  for (let index = 7; index < bounds.length; index++) {
    expect(bounds[index].top).toBeGreaterThanOrEqual(bounds[index - 7].bottom - 1)
  }
  await cells.last().click()
  await expect(cells.last()).toHaveAttribute('aria-selected', 'true')

  await page.getByRole('button', { name: 'Inboxes', exact: true }).click()
  await page.getByRole('button', { name: 'Send test', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Send a test email' })
  await expect(dialog).toBeVisible()
  const dialogBounds = await dialog.boundingBox()
  expect(dialogBounds!.y).toBeGreaterThanOrEqual(0)
  expect(dialogBounds!.y + dialogBounds!.height).toBeLessThanOrEqual(390)
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: 'Send test', exact: true })).toBeFocused()
})

test('long message headers keep the reader controls reachable across phone, tablet and desktop widths', async ({ page, request }, testInfo) => {
  const subject = `Long header smoke ${'with extensive mail metadata '.repeat(45)}`.trim()
  await sendTestMessage(request, { to: 'long-header@hoomail.test', subject, kind: 'plain' })
  await messageRow(page, subject).click()
  await expect(page.getByRole('heading', { name: subject, exact: true })).toBeVisible()
  for (const viewport of [
    { width: 320, height: 568 },
    { width: 390, height: 844 },
    { width: 768, height: 1024 },
    { width: 1440, height: 900 },
  ]) {
    await page.setViewportSize(viewport)
    const tabs = page.getByRole('tablist')
    const bounds = await tabs.boundingBox()
    expect(bounds).not.toBeNull()
    expect(bounds!.y).toBeGreaterThanOrEqual(0)
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(viewport.height)
    const dimensions = await page.locator('[data-hoomail-shell]').evaluate((shell) => ({
      viewport: window.innerWidth,
      bodyWidth: document.body.scrollWidth,
      shellWidth: shell.getBoundingClientRect().width,
    }))
    expect(dimensions.bodyWidth).toBeLessThanOrEqual(dimensions.viewport)
    expect(dimensions.shellWidth).toBeLessThanOrEqual(dimensions.viewport)
    await page.getByRole('tab', { name: 'Source', exact: true }).click()
    await expect(page.getByLabel('Raw message source')).toContainText('Long header smoke')
    const screenshotPath = testInfo.outputPath(`reader-${viewport.width}px.png`)
    await page.screenshot({ path: screenshotPath })
    await testInfo.attach(`reader-${viewport.width}px`, { path: screenshotPath, contentType: 'image/png' })
    await page.getByRole('tab', { name: 'HTML', exact: true }).click()
  }
})
