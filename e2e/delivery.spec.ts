import { expect, mailboxButton, messageRow, test } from './fixtures'

test.describe('test message delivery', () => {
  test('validates the dialog and delivers a message through SMTP and SSE without reloading', async ({ page }) => {
    const sendTest = page.getByRole('button', { name: 'Send test' })

    await sendTest.click()

    const dialog = page.getByRole('dialog', { name: 'Send a test email' })
    const recipient = dialog.getByLabel('Recipient')
    const close = dialog.getByRole('button', { name: 'Close' })

    await expect(dialog).toBeVisible()
    await expect(recipient).toBeFocused()

    await page.keyboard.press('Shift+Tab')
    await expect(close).toBeFocused()
    await page.keyboard.press('Tab')
    await expect(recipient).toBeFocused()

    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()
    await expect(sendTest).toBeFocused()

    await sendTest.click()
    await expect(recipient).toBeFocused()

    await recipient.fill('not-an-address')
    await dialog.getByRole('button', { name: 'Send', exact: true }).click()

    await expect(dialog.getByRole('alert')).toHaveText('Invalid recipient address')
    await expect(dialog).toBeVisible()

    await dialog.getByLabel('Subject').fill('Realtime delivery')
    await recipient.fill('realtime@hoomail.test')
    await expect(dialog.getByRole('alert')).toHaveText('Invalid recipient address')
    await recipient.focus()
    await recipient.press('Enter')

    await expect(dialog).toBeHidden()

    const inbox = mailboxButton(page, 'realtime@hoomail.test')
    const row = messageRow(page, 'Realtime delivery')
    await expect(inbox).toHaveAccessibleName('realtime@hoomail.test, 1 messages, 1 unread')
    await expect(row).toBeVisible()

    await row.click()
    await expect(
      page.getByRole('status').filter({ hasText: 'Message loaded: Realtime delivery' }),
    ).toHaveText('Message loaded: Realtime delivery')
    await expect(page.getByRole('heading', { name: 'Realtime delivery' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'HTML' })).toHaveAttribute('aria-selected', 'true')
    await expect(
      page.frameLocator('iframe[title="Email HTML content"]').getByRole('heading', {
        name: 'Hoot hoot! It works.',
      }),
    ).toBeVisible()
    await expect(page.getByRole('button', { name: 'Preview hoot.txt' })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Download hoot.txt' })).toBeVisible()
    await expect(inbox).toHaveAccessibleName('realtime@hoomail.test, 1 messages, 0 unread')
  })

  test('supports roving message-type selection and calendar defaults', async ({ page }) => {
    await page.getByRole('button', { name: 'Send test' }).click()

    const dialog = page.getByRole('dialog', { name: 'Send a test email' })
    const group = dialog.getByRole('radiogroup', { name: 'Message type' })
    const plain = group.getByRole('radio', { name: 'Plain email' })
    const invite = group.getByRole('radio', { name: 'Meeting invite' })
    const cancellation = group.getByRole('radio', { name: 'Cancellation' })

    await expect(plain).toBeChecked()
    await expect(dialog.getByLabel('Subject')).toHaveValue('hoomail delivery test')

    await plain.focus()
    await plain.press('ArrowRight')
    await expect(invite).toBeFocused()
    await expect(invite).toBeChecked()
    await expect(dialog.getByLabel('Event title')).toHaveValue('Owl standup meeting')
    await invite.press('ArrowLeft')
    await expect(plain).toBeFocused()
    await expect(plain).toBeChecked()
    await expect(dialog.getByLabel('Subject')).toHaveValue('hoomail delivery test')

    await plain.press('ArrowRight')
    await expect(invite).toBeFocused()
    await expect(invite).toBeChecked()
    await expect(dialog.getByLabel('Event title')).toHaveValue('Owl standup meeting')

    await invite.press('End')
    await expect(cancellation).toBeFocused()
    await expect(cancellation).toBeChecked()
    await expect(dialog.getByLabel('Event title')).toHaveValue('Owl standup meeting')

    await cancellation.press('Home')
    await expect(plain).toBeFocused()
    await expect(plain).toBeChecked()
    await expect(dialog.getByLabel('Subject')).toHaveValue('hoomail delivery test')

    await plain.press('ArrowRight')
    await expect(invite).toBeFocused()
    await expect(invite).toBeChecked()
    await expect(dialog.getByLabel('Event title')).toHaveValue('Owl standup meeting')
  })
})
