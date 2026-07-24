import { createConnection } from 'node:net'
import { expect, messageRow, sendTestMessage, test } from './fixtures'

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
  await command('EHLO viewer.test')
  await command('MAIL FROM:<sender@example.test>')
  await command(`RCPT TO:<${recipient}>`)
  await command('DATA')
  socket.write(`${raw.replace(/\r?\n/g, '\r\n')}\r\n.\r\n`)
  const reply = await waitForReply()
  if (!/^250 /.test(reply)) throw new Error(`SMTP rejected message: ${reply}`)
  socket.end('QUIT\r\n')
}


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
  const rawSource = page.getByLabel('Raw message source')
  await expect(rawSource).toContainText('raw message')
  await expect(rawSource).toContainText(`Subject: ${subject}`)
  await expect(rawSource).toContainText('Content-Type: multipart/mixed; boundary="hoomail-mixed-boundary"')
  await expect(rawSource).toContainText('Hoot hoot! It works.')
  await expect(rawSource).toContainText('Content-Disposition: attachment; filename="hoot.txt"')

  await page.keyboard.press('End')
  await expect(inspectTab).toBeFocused()
  await expect(inspectTab).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('status').filter({ hasText: 'Message analysis complete' })).toBeVisible()

  const summary = page.getByRole('region', { name: 'Inspection summary' })
  const linksAndImages = page.getByRole('region', { name: 'Links and images' })
  const mimeStructure = page.getByRole('region', { name: 'MIME structure' })
  await expect(summary).toBeVisible()
  for (const [outcome, colorClass] of [
    ['pass', 'text-green-500'],
    ['fail', 'text-destructive'],
    ['observed', 'text-blue-500'],
    ['not-evaluated', 'text-amber-500'],
  ]) {
    const icon = page.locator(`li[data-outcome="${outcome}"] svg`).first()
    await expect(icon).toBeVisible()
    await expect(icon).toHaveClass(new RegExp(colorClass))
  }
  await expect(summary).toContainText('Static offline analysis. Authentication, delivery, and unsubscribe endpoints are not verified.')
  await expect(linksAndImages.getByRole('heading', { name: 'Links & images (2)' })).toBeVisible()
  await expect(linksAndImages).toContainText('https://example.com')
  await expect(linksAndImages).toContainText('hoot.txt')
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

test('inspection failure exposes retry and recovers the same message', async ({ page, request }) => {
  const recipient = 'viewer-inspection-retry@hoomail.test'
  const subject = 'Viewer inspection retry'
  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()

  let failInspection = true
  await page.route('**/api/messages/*/inspect', async (route) => {
    if (failInspection) {
      await route.fulfill({ status: 500, contentType: 'text/plain', body: 'Internal Server Error\n' })
      return
    }
    await route.continue()
  })

  await page.getByRole('tab', { name: 'Inspect' }).click()
  await expect(page.getByRole('alert')).toHaveText('Could not analyze this message.')
  failInspection = false
  await page.getByRole('button', { name: 'Retry analysis' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'Analyzing message…' })).toBeVisible()
  await expect(page.getByRole('status').filter({ hasText: 'Message analysis complete' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Inspection summary' })).toBeVisible()
})

test('partial inspection displays unavailable rule families and parsed scope', async ({ page, request }) => {
  const recipient = 'viewer-inspection-partial@hoomail.test'
  const subject = 'Viewer partial inspection'
  await sendTestMessage(request, { to: recipient, subject, kind: 'plain' })
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()

  await page.route('**/api/messages/*/inspect', async (route) => {
    const response = await route.fetch()
    const report = await response.json()
    report.analysis.state = 'partial'
    report.analysis.parsedThroughPath = '1.2'
    report.analysis.unavailableRuleFamilies = ['mime', 'authentication']
    await route.fulfill({ response, json: report })
  })

  await page.getByRole('tab', { name: 'Inspect' }).click()
  const summary = page.getByRole('region', { name: 'Inspection summary' })
  await expect(summary).toContainText('Parsed through MIME path 1.2.')
  await expect(summary).toContainText('Unavailable checks: mime, authentication.')
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

test('HTML preview preserves sender styling while blocking remote content and active previews', async ({ page }) => {
  const remoteRequests: string[] = []
  page.on('request', (request) => {
    if (request.url().includes('remote.invalid')) remoteRequests.push(request.url())
  })

  const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='
  const raw = [
    'From: Sender <sender@example.test>',
    'To: privacy-viewer@hoomail.test',
    'Subject: Sender faithful privacy preview',
    'MIME-Version: 1.0',
    'Content-Type: multipart/mixed; boundary="outer"',
    '',
    '--outer',
    'Content-Type: multipart/related; boundary="related"; start="<root@example.test>"',
    '',
    '--related',
    'Content-Type: text/html; charset=utf-8',
    'Content-ID: <root@example.test>',
    '',
    '<!doctype html><html><head><link rel="stylesheet" href="https://remote.invalid/email.css"></head><body style="color:rgb(12,34,56)"><table style="border-collapse:collapse"><tr><td style="padding:7px">Sender table</td><td style="padding:7px"><img alt="CID logo" src="cid:logo@example.test"><img alt="Remote tracking pixel" src="https://remote.invalid/pixel.png"></td></tr></table></body></html>',
    '--related',
    'Content-Type: image/png; name="logo.png"',
    'Content-Disposition: inline; filename="logo.png"',
    'Content-ID: <logo@example.test>',
    'Content-Transfer-Encoding: base64',
    '',
    png,
    '--related--',
    '--outer',
    'Content-Type: application/pdf; name="report.pdf"',
    'Content-Disposition: attachment; filename="report.pdf"',
    'Content-Transfer-Encoding: base64',
    '',
    'JVBERi0xLjQKJSVFT0Y=',
    '--outer',
    'Content-Type: image/svg+xml; name="active.svg"',
    'Content-Disposition: attachment; filename="active.svg"',
    '',
    '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>',
    '--outer--',
  ].join('\r\n')

  await sendRawMessage(raw, 'privacy-viewer@hoomail.test')
  const row = page
    .getByRole('list', { name: 'Messages' })
    .getByRole('button', { name: /^Sender, Sender faithful privacy preview,/ })
    .last()
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: 'Message loaded: Sender faithful privacy preview' })).toBeVisible()

  const iframe = page.locator('iframe[title="Email HTML content"]')
  await expect(iframe).toHaveAttribute('sandbox', '')
  await expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer')
  const frame = page.frameLocator('iframe[title="Email HTML content"]')
  const senderTable = frame.getByRole('table').filter({ hasText: 'Sender table' })
  const senderCell = frame.getByRole('cell', { name: 'Sender table' })
  const cidLogo = frame.getByRole('img', { name: 'CID logo' })
  await expect(senderTable).toBeVisible()
  await expect(senderCell).toBeVisible()
  await expect(cidLogo).toBeVisible()
  await expect.poll(() => cidLogo.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(1)
  const senderStyles = await frame.locator('body').evaluate((body) => {
    const style = getComputedStyle(body)
    const table = body.querySelector('table')
    const cell = body.querySelector('td')
    return {
      color: style.color,
      margin: style.margin,
      padding: style.padding,
      background: style.backgroundColor,
      fontFamily: style.fontFamily,
      tableBorderCollapse: table ? getComputedStyle(table).borderCollapse : '',
      cellPadding: cell ? getComputedStyle(cell).padding : '',
    }
  })
  expect(senderStyles).toEqual({
    color: 'rgb(12, 34, 56)',
    margin: '8px',
    padding: '0px',
    background: 'rgba(0, 0, 0, 0)',
    fontFamily: '"Times New Roman"',
    tableBorderCollapse: 'collapse',
    cellPadding: '7px',
  })
  await page.waitForTimeout(100)
  expect(remoteRequests).toEqual([])

  for (const name of ['report.pdf', 'active.svg']) {
    await expect(page.getByRole('link', { name: `Download ${name}` })).toBeVisible()
    await expect(page.getByRole('button', { name: `Preview ${name}` })).toHaveCount(0)
  }
})

test('HTML preview supports mobile presets, custom dimensions, rotation, and panel fit', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 720 })
  const subject = 'Responsive email viewport'
  const raw = [
    'From: Responsive Sender <responsive@example.test>',
    'To: responsive-viewer@hoomail.test',
    `Subject: ${subject}`,
    'MIME-Version: 1.0',
    'Content-Type: text/html; charset=utf-8',
    '',
    '<!doctype html><html><body><div style="box-sizing:border-box;width:100%;padding:12px">Fluid email content</div></body></html>',
  ].join('\r\n')

  await sendRawMessage(raw, 'responsive-viewer@hoomail.test')
  const row = page
    .getByRole('list', { name: 'Messages' })
    .getByRole('button', { name: new RegExp(`Responsive Sender, ${subject},`) })
    .last()
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()

  const sizeSelect = page.getByLabel('Preview size')
  const iframe = page.locator('iframe[title="Email HTML content"]')
  const frame = page.frameLocator('iframe[title="Email HTML content"]')
  const fluidContent = frame.getByText('Fluid email content')
  await expect(fluidContent).toBeVisible()
  const fitDocumentWidth = await frame.locator('html').evaluate((element) => element.clientWidth)
  const fitContentWidth = await fluidContent.evaluate((element) => element.getBoundingClientRect().width)
  expect(fitDocumentWidth).toBeGreaterThan(375)
  expect(fitContentWidth).toBeGreaterThan(375)
  await expect(sizeSelect).toHaveValue('fit')
  await expect(page.getByLabel('Email preview canvas')).toHaveCount(0)
  await expect(page.getByRole('group', { name: 'Email viewport' })).toBeVisible()
  await iframe.evaluate((element) => element.setAttribute('data-viewport-identity', 'stable'))

  for (const preset of [
    { id: 'mobile-s', width: 375, height: 667 },
    { id: 'mobile-m', width: 390, height: 844 },
    { id: 'mobile-l', width: 430, height: 932 },
    { id: 'tablet', width: 768, height: 1024 },
  ]) {
    await sizeSelect.selectOption(preset.id)
    await expect(page.getByLabel('Preview width')).toHaveValue(String(preset.width))
    await expect(page.getByLabel('Preview height')).toHaveValue(String(preset.height))
    await expect(iframe).toHaveCSS('width', `${preset.width}px`)
    await expect(iframe).toHaveCSS('height', `${preset.height}px`)
  }

  await sizeSelect.selectOption('mobile-s')
  await expect(page.getByLabel('Preview width')).toHaveValue('375')
  await expect(page.getByLabel('Preview height')).toHaveValue('667')
  await expect(iframe).toHaveCSS('width', '375px')
  await expect(iframe).toHaveCSS('height', '667px')
  await expect(iframe).toHaveAttribute('data-viewport-identity', 'stable')

  const mobileDocumentWidth = await page
    .frameLocator('iframe[title="Email HTML content"]')
    .locator('html')
    .evaluate((element) => element.clientWidth)
  expect(mobileDocumentWidth).toBe(375)
  const mobileContentWidth = await fluidContent.evaluate((element) => element.getBoundingClientRect().width)
  expect(mobileContentWidth).toBe(mobileDocumentWidth - 16)
  expect(mobileContentWidth).toBeLessThan(fitContentWidth)

  const canvas = page.getByRole('region', { name: 'Email preview canvas' })
  await expect(canvas).toHaveAttribute('tabindex', '0')
  await canvas.focus()
  await expect(canvas).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect.poll(() => canvas.evaluate((element) => element.scrollTop)).toBeGreaterThan(0)

  const widthInput = page.getByLabel('Preview width')
  await widthInput.fill('')
  await widthInput.press('Tab')
  await expect(widthInput).toHaveValue('375')
  await expect(sizeSelect).toHaveValue('mobile-s')
  await expect(iframe).toHaveCSS('width', '375px')

  await widthInput.fill('320')
  await widthInput.press('Tab')
  await expect(sizeSelect).toHaveValue('custom')
  await expect(iframe).toHaveCSS('width', '320px')
  const customDocumentWidth = await frame.locator('html').evaluate((element) => element.clientWidth)
  const customContentWidth = await fluidContent.evaluate((element) => element.getBoundingClientRect().width)
  expect(customDocumentWidth).toBe(320)
  expect(customContentWidth).toBe(customDocumentWidth - 16)

  await page.getByRole('button', { name: 'Rotate preview' }).click()
  await expect(page.getByLabel('Preview width')).toHaveValue('667')
  await expect(page.getByLabel('Preview height')).toHaveValue('320')
  await expect(iframe).toHaveCSS('width', '667px')
  await expect(iframe).toHaveCSS('height', '320px')
  const rotatedDocument = await frame.locator('html').evaluate((element) => ({
    width: element.clientWidth,
    height: element.clientHeight,
  }))
  expect(rotatedDocument).toEqual({ width: 667, height: 320 })

  await widthInput.fill('100')
  await widthInput.press('Tab')
  await expect(widthInput).toHaveValue('240')
  await expect(iframe).toHaveCSS('width', '240px')
  await widthInput.fill('100')
  await widthInput.press('Tab')
  await expect(widthInput).toHaveValue('240')
  await expect.poll(() => frame.locator('html').evaluate((element) => element.clientWidth)).toBe(240)

  const heightInput = page.getByLabel('Preview height')
  await heightInput.fill('9999')
  await heightInput.press('Tab')
  await expect(heightInput).toHaveValue('1920')
  await expect(iframe).toHaveCSS('height', '1920px')
  await expect.poll(() => frame.locator('html').evaluate((element) => element.clientHeight)).toBe(1920)

  await heightInput.fill('100')
  await heightInput.press('Tab')
  await expect(heightInput).toHaveValue('240')
  await expect(iframe).toHaveCSS('height', '240px')
  await expect(canvas).not.toHaveAttribute('tabindex', '0')

  await page.setViewportSize({ width: 900, height: 720 })
  await sizeSelect.selectOption('mobile-s')
  const toolbarBox = await page.getByRole('group', { name: 'Email viewport' }).boundingBox()
  const dimensionBox = await page.getByRole('group', { name: 'Custom viewport dimensions' }).boundingBox()
  if (!toolbarBox || !dimensionBox) throw new Error('Viewport controls are not visible')
  expect(dimensionBox.x).toBeGreaterThanOrEqual(toolbarBox.x)
  expect(dimensionBox.x + dimensionBox.width).toBeLessThanOrEqual(toolbarBox.x + toolbarBox.width)
  await page.setViewportSize({ width: 1280, height: 720 })

  await sizeSelect.selectOption('fit')
  await expect(page.getByLabel('Email preview canvas')).toHaveCount(0)
  await expect(page.getByLabel('Preview width')).toHaveCount(0)
  await expect.poll(() => frame.locator('html').evaluate((element) => element.clientWidth)).toBe(fitDocumentWidth)
  await expect(iframe).toHaveAttribute('data-viewport-identity', 'stable')
})

test('responsive viewport persists across HTML message switches', async ({ page }) => {
  const recipient = 'responsive-switching@hoomail.test'
  const messages = [
    { subject: 'Responsive first message', content: 'First responsive content' },
    { subject: 'Responsive second message', content: 'Second responsive content' },
  ]

  for (const message of messages) {
    const raw = [
      'From: Responsive Sender <responsive@example.test>',
      `To: ${recipient}`,
      `Subject: ${message.subject}`,
      'MIME-Version: 1.0',
      'Content-Type: text/html; charset=utf-8',
      '',
      `<!doctype html><html><body><main style="width:100%">${message.content}</main></body></html>`,
    ].join('\r\n')
    await sendRawMessage(raw, recipient)
  }

  const messageList = page.getByRole('list', { name: 'Messages' })
  const firstRow = messageList
    .getByRole('button', { name: /^Responsive Sender, Responsive first message,/ })
    .last()
  const secondRow = messageList
    .getByRole('button', { name: /^Responsive Sender, Responsive second message,/ })
    .last()
  await expect(firstRow).toBeVisible()
  await expect(secondRow).toBeVisible()

  await firstRow.click()
  await expect(page.getByRole('status').filter({ hasText: 'Message loaded: Responsive first message' })).toBeVisible()
  const sizeSelect = page.getByLabel('Preview size')
  const iframe = page.locator('iframe[title="Email HTML content"]')
  const frame = page.frameLocator('iframe[title="Email HTML content"]')
  await sizeSelect.selectOption('mobile-m')
  await expect(frame.getByText('First responsive content')).toBeVisible()
  const canvas = page.getByRole('region', { name: 'Email preview canvas' })
  await canvas.evaluate((element) => element.setAttribute('data-viewport-container', 'stable'))

  await secondRow.click()
  await expect(page.getByRole('status').filter({ hasText: 'Message loaded: Responsive second message' })).toBeVisible()
  await expect(frame.getByText('Second responsive content')).toBeVisible()
  await expect(frame.getByText('First responsive content')).toHaveCount(0)
  await expect(sizeSelect).toHaveValue('mobile-m')
  await expect(page.getByLabel('Preview width')).toHaveValue('390')
  await expect(page.getByLabel('Preview height')).toHaveValue('844')
  await expect(iframe).toHaveCSS('width', '390px')
  await expect(iframe).toHaveCSS('height', '844px')
  await expect.poll(() => frame.locator('html').evaluate((element) => element.clientWidth)).toBe(390)
  await expect(canvas).toHaveAttribute('data-viewport-container', 'stable')

  await firstRow.click()
  await expect(page.getByRole('status').filter({ hasText: 'Message loaded: Responsive first message' })).toBeVisible()
  await expect(frame.getByText('First responsive content')).toBeVisible()
  await expect(frame.getByText('Second responsive content')).toHaveCount(0)
  await expect(sizeSelect).toHaveValue('mobile-m')
  await expect(iframe).toHaveCSS('width', '390px')
})
