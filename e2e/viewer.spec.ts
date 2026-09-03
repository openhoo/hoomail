import { Buffer } from 'node:buffer'
import { createConnection } from 'node:net'
import { expect, messageRow, sendTestMessage, test } from './fixtures'

async function sendRawMessage(raw: string | Buffer, recipient: string): Promise<void> {
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
  const payload = Buffer.isBuffer(raw) ? raw : Buffer.from(raw.replace(/\r?\n/g, '\r\n'), 'utf8')
  socket.write(Buffer.concat([payload, Buffer.from('\r\n.\r\n')]))
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
  await expect(page.getByRole('heading', { name: subject, level: 2 })).toBeVisible()

  const htmlTab = page.getByRole('tab', { name: 'HTML' })
  const plainTab = page.getByRole('tab', { name: 'Plain text' })
  const sourceTab = page.getByRole('tab', { name: 'Source' })
  const inspectTab = page.getByRole('tab', { name: 'Inspect' })

  await expect(htmlTab).toHaveAttribute('aria-selected', 'true')
  expect(await page.evaluate(() =>
    performance.getEntriesByType('resource').some((entry) => entry.name.includes('inspect-panel')),
  )).toBe(false)

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
  expect(await page.evaluate(() =>
    performance.getEntriesByType('resource').some((entry) => entry.name.includes('inspect-panel')),
  )).toBe(true)

  const summary = page.getByRole('region', { name: 'Inspection summary' })
  const linksAndImages = page.getByRole('region', { name: 'Links and images' })
  const mimeStructure = page.getByRole('region', { name: 'MIME structure' })
  await expect(summary).toBeVisible()
  const outcomeTokens: Array<[outcome: string, token: string]> = [
    ['pass', '--color-green-500'],
    ['fail', '--color-destructive'],
    ['observed', '--color-blue-500'],
    ['not-evaluated', '--color-amber-500'],
  ]
  for (const [outcome, token] of outcomeTokens) {
    await expect(page.locator(`li[data-outcome="${outcome}"] svg`).first()).toBeVisible()
    const [iconColor, tokenColor] = await page.evaluate(
      ([outcomeValue, cssToken]) => {
        const icon = document.querySelector(`li[data-outcome="${outcomeValue}"] svg`)
        if (!icon) throw new Error(`missing ${outcomeValue} outcome icon`)
        const probe = document.createElement('span')
        probe.style.color = `var(${cssToken})`
        document.body.appendChild(probe)
        try {
          return [getComputedStyle(icon).color, getComputedStyle(probe).color] as const
        } finally {
          probe.remove()
        }
      },
      [outcome, token],
    )
    expect(iconColor).toBe(tokenColor)
  }
  await expect(summary).toContainText('Static offline analysis. Authentication, delivery, and unsubscribe endpoints are not verified.')
  await expect(linksAndImages.getByRole('heading', { name: 'Links & images (2)' })).toBeVisible()
  await expect(linksAndImages).toContainText('https://example.com')
  await expect(linksAndImages).toContainText('hoot.txt')
  await expect(mimeStructure).toBeVisible()
  const compatibility = page.getByRole('region', { name: 'HTML compatibility' })
  await expect(compatibility).toBeVisible()
  await expect(compatibility).toContainText('Can I Email')
  const headers = page.getByRole('region', { name: 'Message headers' })
  await expect(headers).toBeVisible()
  await headers.getByRole('searchbox', { name: 'Filter headers' }).fill('subject')
  await expect(headers).toContainText(`Subject`)
  await expect(headers).toContainText(subject)
  await headers.getByRole('searchbox', { name: 'Filter headers' }).fill('definitely-missing-header')
  await expect(headers).toContainText('No headers match')
  await expect(headers.getByRole('status')).toHaveText(/0 of \d+ headers match/)
  await expect(page.getByRole('region', { name: 'Unavailable checks' })).toContainText('Link status & redirects')
  await expect(page.getByRole('region', { name: 'Unavailable checks' })).toContainText('SpamAssassin')
  await expect(mimeStructure).toContainText('md5')
  await expect(mimeStructure).toContainText('sha256')

  await inspectTab.focus()
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
test('raw source preserves invalid UTF-8 bytes without replacement characters', async ({ page }) => {
  const recipient = 'viewer-binary-source@hoomail.test'
  const subject = 'Viewer binary source'
  const raw = Buffer.concat([
    Buffer.from(
      `From: sender@example.test\r\nTo: ${recipient}\r\nSubject: ${subject}\r\n` +
      'Content-Type: text/plain; charset=iso-8859-1\r\n' +
      'Content-Transfer-Encoding: 8bit\r\n\r\n' +
      'Body with non-UTF-8 octet: ',
      'ascii',
    ),
    Buffer.from([0xff]),
    Buffer.from('\r\n', 'ascii'),
  ])

  await sendRawMessage(raw, recipient)
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()

  await page.getByRole('tab', { name: 'Source' }).click()
  const source = page.getByLabel('Raw message source')
  await expect(source).toContainText('Body with non-UTF-8 octet: \\xFF')
  await expect(source).not.toContainText('\uFFFD')
})

test('raw source preview is bounded and offers the complete download', async ({ page }) => {
  const recipient = 'viewer-large-source@hoomail.test'
  const subject = 'Viewer large source'
  const rawBytes = 24 * 1024 * 1024
  // The SMTP DATA terminator contributes the required final CRLF to stored
  // source bytes; the dot line itself is stripped by the server.
  const expectedSourceBytes = rawBytes + 2
  const headers = Buffer.from(
    `From: sender@example.test\r\nTo: ${recipient}\r\nSubject: ${subject}\r\n` +
    'Content-Type: application/octet-stream\r\n' +
    'Content-Transfer-Encoding: 8bit\r\n\r\n',
    'ascii',
  )
  const body = Buffer.alloc(rawBytes - headers.length, 0xff)
  for (let offset = 1022; offset + 1 < body.length; offset += 1024) {
    body[offset] = 0x0d
    body[offset + 1] = 0x0a
  }

  await page.evaluate(() => {
    let cancelled = false
    const originalFetch = window.fetch.bind(window)
    Object.defineProperty(window, '__sourcePreviewCancelled', {
      configurable: true,
      get: () => cancelled,
    })
    window.fetch = async (input, init) => {
      const response = await originalFetch(input, init)
      const inputURL = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
      if (!new URL(inputURL, window.location.href).pathname.endsWith('/source') || !response.body) return response
      const sourceReader = response.body.getReader()
      const stream = new ReadableStream<Uint8Array>({
        async pull(controller) {
          const result = await sourceReader.read()
          if (result.done) controller.close()
          else controller.enqueue(result.value)
        },
        async cancel(reason) {
          cancelled = true
          await sourceReader.cancel(reason)
        },
      })
      return new Response(stream, {
        status: response.status,
        statusText: response.statusText,
        headers: response.headers,
      })
    }
  })

  await sendRawMessage(Buffer.concat([headers, body]), recipient)
  const row = messageRow(page, subject)
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${subject}` })).toBeVisible()

  const source = page.getByLabel('Raw message source')
  const preview = source.locator('pre')
  await expect(preview).toBeVisible()
  const text = await preview.textContent()
  expect(text?.length ?? 0).toBeLessThanOrEqual(100_000)
  await expect(source).toContainText('Preview limited to the first 100,000 characters.')
  const download = source.getByRole('link', { name: 'Download complete source' })
  const downloadStarted = page.waitForEvent('download')
  await download.click()
  const completeDownload = await downloadStarted
  expect(completeDownload.suggestedFilename()).toMatch(/^message-\d+\.eml$/)
  const downloadStream = await completeDownload.createReadStream()
  if (!downloadStream) throw new Error('Raw source download has no stream')
  let downloadedBytes = 0
  for await (const chunk of downloadStream) downloadedBytes += Buffer.byteLength(chunk)
  expect(downloadedBytes).toBe(expectedSourceBytes)
  await expect(download).toHaveAttribute('download', /\d+\.eml$/)
  await expect.poll(() => page.evaluate(() => Boolean((window as Window & { __sourcePreviewCancelled?: boolean }).__sourcePreviewCancelled))).toBe(true)
  await expect(page.getByRole('tab', { name: 'Source' })).toBeVisible()
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
  await expect(page.getByRole('heading', { name: inviteSubject, level: 2 })).toBeVisible()
  await expect(page.getByText(inviteTitle, { exact: true })).toBeVisible()

  await plainRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${plainSubject}` })).toBeVisible()
  await expect(page.getByRole('heading', { name: plainSubject, level: 2 })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'HTML' })).toHaveAttribute('aria-selected', 'true')

  const htmlFrame = page.frameLocator('iframe[title="Email HTML content"]')
  await expect(htmlFrame.getByRole('heading', { name: 'Hoot hoot! It works.', level: 1 })).toBeVisible()
  await expect(htmlFrame.locator('body')).toContainText(recipient)
  await expect(htmlFrame.locator('body')).not.toContainText(inviteTitle)
})

test('HTML preview applies its canvas while preserving sender content styling and privacy', async ({ page }) => {
  const remoteRequests: string[] = []
  // Whole-lifecycle recorders: track every HTTP(S) page request plus everything
  // originating inside the sandboxed preview document (about:srcdoc).
  // data:/blob: loads never reach the network layer, so validated CID
  // attachment requests are the only traffic allowed out of the email frame.
  const allHttpRequests: string[] = []
  const previewFrameRequests: string[] = []
  page.on('request', (request) => {
    const url = request.url()
    if (!url.startsWith('http://') && !url.startsWith('https://')) return
    allHttpRequests.push(url)
    if (request.frame().url() === 'about:srcdoc') previewFrameRequests.push(url)
    if (url.includes('remote.invalid')) remoteRequests.push(url)
  })

  const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='
  const embeddedSvg = Buffer.from(
    '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="9" viewBox="0 0 14 9"><rect width="14" height="9" fill="#004b76"/></svg>',
  ).toString('base64')
  const hostileDataSvg = Buffer.from(
    '<svg xmlns="http://www.w3.org/2000/svg"><image href="https://remote.invalid/data-svg.png"/></svg>',
  ).toString('base64')
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
    `<!doctype html><html><head><link rel="stylesheet" href="https://remote.invalid/email.css"><style>@import "https://remote.invalid/import.css";@font-face{font-family:Remote;src:u/**/rl(https://remote.invalid/font.woff2)}.remote{background-image:u/**/rl(https://remote.invalid/background.png)}</style></head><body style="color:rgb(12,34,56)"><table style="border-collapse:collapse"><tr><td style="padding:7px">Sender table</td><td class="remote" style="padding:7px"><img alt="CID logo" src="cid:logo@example.test"><img alt="CID vector" src="cid:vector@example.test"><img alt="Embedded data vector" src="data:image/svg+xml;base64,${embeddedSvg}"><img alt="Hostile data vector" src="data:image/svg+xml;base64,${hostileDataSvg}"><img alt="Mixed CID logo" src="cid:mixed-logo@example.test"><img alt="Remote tracking pixel" src="https://remote.invalid/pixel.png" srcset="https://remote.invalid/pixel-2x.png 2x"><img alt="Arbitrary same-origin path" src="/api/reset"></td></tr></table></body></html>`,
    '--related',
    'Content-Type: image/png; name="logo.png"',
    'Content-Disposition: inline; filename="logo.png"',
    'Content-ID: <logo@example.test>',
    'Content-Transfer-Encoding: base64',
    '',
    png,
    '--related',
    'Content-Type: image/svg+xml; name="vector.svg"',
    'Content-Disposition: inline; filename="vector.svg"',
    'Content-ID: <vector@example.test>',
    '',
    '<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" width="12" height="8" viewBox="0 0 12 8" onload="fetch(\'https://remote.invalid/svg-onload\')"><style>@import url(https://remote.invalid/svg-style.css)</style><script>fetch(\'https://remote.invalid/svg-script\')</script><foreignObject><iframe src="https://remote.invalid/svg-frame"></iframe></foreignObject><rect width="12" height="8" fill="#336699"/></svg>',
    '--related--',
    '--outer',
    'Content-Type: image/png; name="mixed-logo.png"',
    'Content-Disposition: inline; filename="mixed-logo.png"',
    'Content-ID: <mixed-logo@example.test>',
    'Content-Transfer-Encoding: base64',
    '',
    png,
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
  const cidLogo = frame.getByRole('img', { name: 'CID logo', exact: true })
  const cidVector = frame.getByRole('img', { name: 'CID vector' })
  const embeddedDataVector = frame.getByRole('img', { name: 'Embedded data vector' })
  const mixedCidLogo = frame.getByRole('img', { name: 'Mixed CID logo' })
  await expect(senderTable).toBeVisible()
  await expect(senderCell).toBeVisible()
  await expect(cidLogo).toBeVisible()
  await expect.poll(() => cidLogo.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(1)
  await expect(cidVector).toBeVisible()
  await expect.poll(() => cidVector.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(12)
  await expect(embeddedDataVector).toBeVisible()
  await expect.poll(() => embeddedDataVector.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(14)
  await expect(mixedCidLogo).toBeVisible()
  await expect.poll(() => mixedCidLogo.evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(1)
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
  const appOrigin = new URL(page.url()).origin
  const isCidAttachmentURL = (url: URL): boolean => (
    url.origin === appOrigin
    && /^\/api\/attachments\/[1-9]\d*$/.test(url.pathname)
    && url.search === '?inline=cid'
  )
  const previewURLs = previewFrameRequests.map((value) => new URL(value))
  expect(previewURLs.filter((url) => !isCidAttachmentURL(url))).toEqual([])
  expect(previewURLs.filter((url) => isCidAttachmentURL(url))).toHaveLength(3)

  const previewMetrics = await frame.locator('html').evaluate((root) => ({
    width: root.clientWidth,
    height: root.clientHeight,
  }))
  const screenshotRequests: string[] = []
  const captureScreenshotRequest = (request: { url(): string }) => {
    const url = new URL(request.url())
    if (url.protocol === 'http:' || url.protocol === 'https:') screenshotRequests.push(url.href)
  }
  page.on('request', captureScreenshotRequest)
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download HTML screenshot' }).click()
  const screenshot = await downloadPromise
  page.off('request', captureScreenshotRequest)
  expect(screenshot.suggestedFilename()).toMatch(/^message-\d+\.png$/)
  const screenshotStream = await screenshot.createReadStream()
  if (!screenshotStream) throw new Error('Screenshot download has no stream')
  const chunks: Buffer[] = []
  for await (const chunk of screenshotStream) chunks.push(Buffer.from(chunk))
  const screenshotBytes = Buffer.concat(chunks)
  expect(screenshotBytes.subarray(0, 8)).toEqual(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]))
  const screenshotInfo = await page.evaluate((base64) => new Promise<{
    width: number
    height: number
    hasCidColor: boolean
    hasOpaquePixel: boolean
    topLeft: [number, number, number, number]
  }>((resolve, reject) => {
    const image = new Image()
    image.onload = () => {
      const canvas = document.createElement('canvas')
      canvas.width = image.naturalWidth
      canvas.height = image.naturalHeight
      const context = canvas.getContext('2d')
      if (!context) return reject(new Error('Canvas unavailable'))
      context.drawImage(image, 0, 0)
      const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data
      let hasCidColor = false
      let hasOpaquePixel = false
      for (let index = 0; index < pixels.length; index += 4) {
        if (pixels[index + 3] !== 0) hasOpaquePixel = true
        if (pixels[index] === 51 && pixels[index + 1] === 102 && pixels[index + 2] === 153 && pixels[index + 3] === 255) {
          hasCidColor = true
          break
        }
      }
      resolve({
        width: image.naturalWidth,
        height: image.naturalHeight,
        hasCidColor,
        hasOpaquePixel,
        topLeft: [pixels[0], pixels[1], pixels[2], pixels[3]],
      })
    }
    image.onerror = () => reject(new Error('PNG decode failed'))
    image.src = `data:image/png;base64,${base64}`
  }), screenshotBytes.toString('base64'))
  expect(screenshotInfo.width).toBe(previewMetrics.width)
  expect(screenshotInfo.height).toBe(previewMetrics.height)
  expect(screenshotInfo.width * screenshotInfo.height).toBeLessThanOrEqual(16_000_000)
  expect(screenshotInfo.hasOpaquePixel).toBe(true)
  expect(screenshotInfo.topLeft).toEqual([255, 255, 255, 255])
  expect(screenshotInfo.hasCidColor).toBe(true)
  const requestURLs = screenshotRequests.map((value) => new URL(value))
  const cidRequests = requestURLs.filter((url) => (
    url.origin === appOrigin
    && /^\/api\/attachments\/[1-9]\d*$/.test(url.pathname)
    && url.search === '?inline=cid'
  ))
  expect(cidRequests).toHaveLength(3)
  const unexpectedRequests = requestURLs.filter((url) => !(
    url.origin === appOrigin
    && /^\/api\/attachments\/[1-9]\d*$/.test(url.pathname)
    && url.search === '?inline=cid'
  ))
  expect(unexpectedRequests.map((url) => url.href)).toEqual([])
  expect(remoteRequests).toEqual([])
  const finalPreviewURLs = previewFrameRequests.map((value) => new URL(value))
  expect(finalPreviewURLs.filter((url) => !isCidAttachmentURL(url))).toEqual([])
  expect(allHttpRequests.filter((value) => new URL(value).pathname === '/api/reset')).toEqual([])

  for (const name of ['report.pdf', 'active.svg']) {
    await expect(page.getByRole('link', { name: `Download ${name}` })).toBeVisible()
    await expect(page.getByRole('button', { name: `Preview ${name}` })).toHaveCount(0)
  }
})

test('switching messages aborts screenshot export and re-enables download', async ({ page }) => {
  const recipient = 'screenshot-switch@hoomail.test'
  const firstSubject = 'Screenshot export source'
  const secondSubject = 'Screenshot export replacement'
  const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='
  await sendRawMessage([
    'From: Sender <sender@example.test>',
    `To: ${recipient}`,
    `Subject: ${firstSubject}`,
    'MIME-Version: 1.0',
    'Content-Type: multipart/related; boundary="related"',
    '',
    '--related',
    'Content-Type: text/html; charset=utf-8',
    '',
    '<html><body><img src="cid:logo@example.test" alt="Export logo"></body></html>',
    '--related',
    'Content-Type: image/png',
    'Content-ID: <logo@example.test>',
    'Content-Transfer-Encoding: base64',
    '',
    png,
    '--related--',
  ].join('\r\n'), recipient)
  await sendRawMessage([
    'From: Sender <sender@example.test>',
    `To: ${recipient}`,
    `Subject: ${secondSubject}`,
    'MIME-Version: 1.0',
    'Content-Type: text/html; charset=utf-8',
    '',
    '<html><body>Replacement content</body></html>',
  ].join('\r\n'), recipient)

  const messageList = page.getByRole('list', { name: 'Messages' })
  const firstRow = messageList.getByRole('button').filter({ hasText: firstSubject }).last()
  const secondRow = messageList.getByRole('button').filter({ hasText: secondSubject }).last()
  await expect(firstRow).toBeVisible()
  await expect(secondRow).toBeVisible()
  await firstRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${firstSubject}` })).toBeVisible()

  let releaseAttachment = () => {}
  const attachmentGate = new Promise<void>((resolve) => {
    releaseAttachment = resolve
  })
  let markAttachmentBlocked = () => {}
  const attachmentBlocked = new Promise<void>((resolve) => {
    markAttachmentBlocked = resolve
  })
  const attachmentPattern = '**/api/attachments/*?inline=cid'
  await page.route(attachmentPattern, async (route) => {
    markAttachmentBlocked()
    await attachmentGate
    await route.continue().catch(() => undefined)
  })

  const downloadButton = page.locator('button[aria-label="Download HTML screenshot"]')
  await downloadButton.click()
  await attachmentBlocked
  await expect(downloadButton).toBeDisabled()
  await secondRow.click()
  await expect(page.getByRole('status').filter({ hasText: `Message loaded: ${secondSubject}` })).toBeVisible()
  await expect(downloadButton).toBeEnabled()
  releaseAttachment()
  await page.unroute(attachmentPattern)
})

test('HTML preview supports mobile presets, custom dimensions, rotation, and panel fit', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 1000 })
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
  await expect(frame.locator('body')).toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
  const fitDocumentWidth = await frame.locator('html').evaluate((element) => element.clientWidth)
  const fitContentWidth = await fluidContent.evaluate((element) => element.getBoundingClientRect().width)
  expect(fitDocumentWidth).toBeGreaterThan(375)
  expect(fitContentWidth).toBeGreaterThan(375)
  await expect(sizeSelect).toHaveValue('fit')
  await expect(page.getByLabel('Email preview canvas')).toHaveCount(0)
  await expect(page.getByRole('group', { name: 'Email viewport' })).toBeVisible()
  await iframe.evaluate((element) => element.setAttribute('data-viewport-identity', 'stable'))
  const fitSizing = await iframe.evaluate((element) => {
    const frame = element.parentElement
    const canvas = frame?.parentElement
    if (!frame || !canvas) throw new Error('Fit preview structure is unavailable')
    const canvasStyle = getComputedStyle(canvas)
    return {
      frameHeight: frame.getBoundingClientRect().height,
      availableHeight:
        canvas.clientHeight -
        Number.parseFloat(canvasStyle.paddingTop) -
        Number.parseFloat(canvasStyle.paddingBottom),
    }
  })
  expect(fitSizing.frameHeight).toBeCloseTo(fitSizing.availableHeight, 0)
  expect(fitSizing.frameHeight).toBeGreaterThan(480)
  await page.setViewportSize({ width: 1280, height: 720 })

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

test('attachment preview refetches reused attachment IDs after reset', async ({ page }) => {
  const recipient = 'viewer-reset-attachment@hoomail.test'
  const filename = 'reused.txt'
  const firstSubject = 'Attachment before reset'
  const secondSubject = 'Attachment after reset'
  const firstText = 'Attachment content before reset.'
  const secondText = 'Different attachment content after reset.'
  const messageList = page.getByRole('list', { name: 'Messages' })
  const attachmentResponse = () => page.waitForResponse((response) => {
    const url = new URL(response.url())
    return response.status() === 200
      && response.request().method() === 'GET'
      && /^\/api\/attachments\/\d+$/.test(url.pathname)
      && !url.search
  })
  const sendAttachment = (subject: string, text: string) => sendRawMessage([
    'From: sender@example.test',
    `To: ${recipient}`,
    `Subject: ${subject}`,
    'MIME-Version: 1.0',
    'Content-Type: multipart/mixed; boundary="reset-attachment"',
    '',
    '--reset-attachment',
    'Content-Type: text/plain; charset=utf-8',
    '',
    'Message body.',
    '--reset-attachment',
    `Content-Type: text/plain; name="${filename}"`,
    `Content-Disposition: attachment; filename="${filename}"`,
    '',
    text,
    '--reset-attachment--',
  ].join('\n'), recipient)

  await sendAttachment(firstSubject, firstText)
  const firstRow = messageList.getByRole('button').filter({ hasText: firstSubject }).last()
  await expect(firstRow).toBeVisible()
  await firstRow.click()

  const firstFetch = attachmentResponse()
  await page.getByRole('button', { name: `Preview ${filename}` }).click()
  const previewDialog = page.getByRole('dialog', { name: filename })
  await expect(previewDialog).toContainText(firstText)
  const firstResponse = await firstFetch
  await page.keyboard.press('Escape')

  await page.getByRole('button', { name: 'Reset', exact: true }).click()
  const resetDialog = page.getByRole('dialog', { name: 'Reset hoomail?' })
  await resetDialog.getByRole('button', { name: 'Wipe everything' }).click()
  await expect(page.getByRole('heading', { name: 'No inbox selected' })).toBeVisible()

  await sendAttachment(secondSubject, secondText)
  const secondRow = messageList.getByRole('button').filter({ hasText: secondSubject }).last()
  await expect(secondRow).toBeVisible()
  await secondRow.click()

  const secondFetch = attachmentResponse()
  await page.getByRole('button', { name: `Preview ${filename}` }).click()
  await expect(previewDialog).toContainText(secondText)
  await expect(previewDialog).not.toContainText(firstText)
  const secondResponse = await secondFetch

  expect(new URL(secondResponse.url()).pathname).toBe(new URL(firstResponse.url()).pathname)
})
