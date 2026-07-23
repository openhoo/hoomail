import { expect, messageRow, sendTestMessage, test } from './fixtures'

test('message viewer tabs, inspection, and attachments expose the complete plain-message contract', async ({
  page,
  request,
}) => {
  const recipient = 'viewer-contract@hoomail.test'
  const subject = 'Viewer contract message'

  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })

  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: subject, level: 1 })).toBeVisible()

  const htmlTab = page.getByRole('tab', { name: 'HTML' })
  const plainTab = page.getByRole('tab', { name: 'Plain text' })
  const sourceTab = page.getByRole('tab', { name: 'Source' })
  const inspectTab = page.getByRole('tab', { name: 'Inspect' })

  await expect(htmlTab).toHaveAttribute('aria-selected', 'true')

  await htmlTab.focus()
  await page.keyboard.press('Tab')
  await expect(plainTab).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(sourceTab).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(inspectTab).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(sourceTab).toBeFocused()

  await htmlTab.focus()
  await page.keyboard.press('ArrowRight')
  await expect(plainTab).toBeFocused()
  await expect(plainTab).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByLabel('Plain text message')).toContainText(recipient)

  await page.keyboard.press('ArrowRight')
  await expect(sourceTab).toBeFocused()
  await expect(sourceTab).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByLabel('Raw message source')).toContainText('raw headers')

  await page.keyboard.press('End')
  await expect(inspectTab).toBeFocused()
  await expect(inspectTab).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('status').filter({ hasText: 'Message analysis complete' })).toBeVisible()

  const headerChecks = page.getByRole('region', { name: 'Header checks' })
  const linksAndImages = page.getByRole('region', { name: 'Links and images' })
  const mimeStructure = page.getByRole('region', { name: 'MIME structure' })
  await expect(headerChecks).toBeVisible()
  await expect(linksAndImages.getByRole('heading', { name: 'Links & images (1)' })).toBeVisible()
  await expect(linksAndImages).toContainText('https://example.com')
  await expect(mimeStructure).toBeVisible()

  await page.keyboard.press('Home')
  await expect(htmlTab).toBeFocused()
  await expect(htmlTab).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('End')
  await expect(inspectTab).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowLeft')
  await expect(sourceTab).toBeFocused()
  await expect(sourceTab).toHaveAttribute('aria-selected', 'true')

  const preview = page.getByRole('button', { name: 'Preview hoot.txt' })
  await preview.click()
  const previewDialog = page.getByRole('dialog', { name: /hoot\.txt/ })
  await expect(previewDialog).toBeVisible()
  await expect(previewDialog).toContainText('This is a sample attachment from the hoomail test sender.')
  await page.keyboard.press('Escape')
  await expect(previewDialog).toBeHidden()

  const download = page.getByRole('link', { name: 'Download hoot.txt' })
  const href = await download.getAttribute('href')
  expect(href).toMatch(/^\/api\/attachments\/\d+\?download=1$/)
  if (href === null) throw new Error('Download hoot.txt has no href')

  const response = await request.get(href)
  expect(response.status()).toBe(200)
  expect(response.headers()['content-disposition']).toBe('attachment; filename="hoot.txt"')
})

test('switching plain to invite and back restores HTML without stale invite content', async ({ page, request }) => {
  const recipient = 'viewer-switching@hoomail.test'
  const plainSubject = 'Viewer switching plain'
  const inviteTitle = 'Viewer switching invite'
  const inviteSubject = `Invitation: ${inviteTitle}`

  await sendTestMessage(request, { to: recipient, subject: plainSubject, kind: 'plain' })
  const plainRow = messageRow(page, plainSubject)
  await expect(plainRow).toBeVisible()

  await sendTestMessage(request, { to: recipient, subject: inviteTitle, kind: 'invite' })
  const inviteRow = messageRow(page, inviteSubject)
  await expect(inviteRow).toBeVisible()

  await plainRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${plainSubject}` })).toBeVisible()
  await page.getByRole('tab', { name: 'Plain text' }).click()
  await expect(page.getByRole('tab', { name: 'Plain text' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByLabel('Plain text message')).toContainText(recipient)

  await inviteRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${inviteSubject}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: inviteSubject, level: 1 })).toBeVisible()
  await expect(page.getByText(inviteTitle, { exact: true })).toBeVisible()

  await plainRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${plainSubject}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: plainSubject, level: 1 })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'HTML' })).toHaveAttribute('aria-selected', 'true')

  const htmlFrame = page.frameLocator('iframe[title="Email HTML content"]')
  await expect(htmlFrame.getByRole('heading', { name: 'Hoot hoot! It works.', level: 1 })).toBeVisible()
  await expect(htmlFrame.locator('body')).toContainText(recipient)
  await expect(htmlFrame.locator('body')).not.toContainText(inviteTitle)
})
