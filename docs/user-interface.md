# User interface guide

Hoomail is organized around inboxes. Select an inbox in the left sidebar, then use either the **Mail** view to inspect and manage messages or the **Calendar** view to review parsed calendar events for that inbox.

This guide describes the current mouse, keyboard, focus, loading, and error behavior. It also calls out limitations that may matter during manual testing and accessibility review.

## Inboxes and global actions

### Inbox creation and selection

- Sending mail to any address creates that inbox automatically.
- When inboxes exist and none is selected, Hoomail automatically selects the first inbox.
- Each inbox button exposes its address, total message count, exact unread count, and whether it is current. The visible unread badge is capped at `99`, while assistive technology receives the exact count.
- Selecting another inbox clears:
  - the open message;
  - any multi-message selection;
  - the current search query; and
  - the Shift+Click range anchor.
- If the current inbox disappears because it was deleted or the store was reset, the open message and message selection are cleared. If another inbox remains, the first available inbox is selected automatically after the inbox list refreshes.

### Deleting one inbox

Open an inbox's context menu and choose **Delete inbox**. This action is immediate: there is no second confirmation dialog. It removes the inbox and its messages.

After deletion, keyboard focus moves to the next inbox in the list, or the previous inbox if there is no next one. If no inbox remains, focus moves to **Send test**.

### Sending a test message

Choose **Send test** to open a dialog that sends a sample through Hoomail's configured real SMTP listener (`HOOMAIL_SMTP_PORT`, default `2525`).

The dialog provides:

- an editable **Recipient**;
- an editable **Subject**, renamed **Event title** for calendar samples; and
- four message types: **Plain email**, **Meeting invite**, **Invite update**, and **Cancellation**.

The plain sample contains plain-text and HTML body alternatives plus a text attachment. The calendar samples contain an iCalendar/iTIP part. Invite, update, and cancellation samples use the same event UID, so send them in that order to observe event reconciliation in the Calendar view.

A failed send is shown as an alert in the dialog. While sending, **Cancel** and **Send** are disabled and an accessible sending status is exposed. A successful send closes the dialog.

The dialog's message-type control has one Tab stop:

- `Arrow Left` or `Arrow Up`: choose the previous type, wrapping at the beginning.
- `Arrow Right` or `Arrow Down`: choose the next type, wrapping at the end.
- `Home`: choose **Plain email**.
- `End`: choose **Cancellation**.
- `Enter` while focus is in **Recipient**: send, except while an input method editor is composing text.

### Resetting all data

Choose **Reset** to open a destructive confirmation dialog. **Wipe everything** permanently removes all inboxes, messages, and attachments. This is different from deleting one inbox: the global reset requires confirmation.

While reset is in progress, **Cancel** and **Wipe everything** are disabled and an accessible resetting status is exposed. Reset also clears the selected inbox, open message, message selection, and search query.

## Mail view

Mail view shows the selected inbox's message list and a message viewer.

### Search scope

Search is available only when an inbox is selected. It searches message subject, sender, and plain-text body **within that inbox**; it does not search the HTML body, inbox names, or all inboxes at once.

- Switching inboxes clears the query.
- Changing or clearing the query clears the multi-message selection and range anchor because the displayed row positions may have changed.
- The UI distinguishes an empty inbox from a search with no matching messages.
- Search and message actions are not displayed in Calendar view.

### Opening and read state

Click a message to open it. Opening an unread message immediately marks it read in the interface, reduces the inbox unread count, and sends the read action to the server. This happens even when the message detail was already cached.

If persisting the read action fails, Hoomail refreshes the affected caches silently; it does not show a read-action error notification.

Read state can also be changed with the message context menu or the multi-selection toolbar.

### Mouse selection

The message list uses desktop-style selection:

| Input | Result |
| --- | --- |
| Click | Open the message and clear any multi-selection. |
| `Shift`+Click | With an existing range anchor, select the contiguous displayed range from that anchor to the clicked row. Without an anchor, it behaves like a normal click and opens the message. |
| `Ctrl`+Click / `Cmd`+Click | Toggle the clicked row in the selection. If selection is initially empty and another message is open, the open message is added before toggling the clicked row. |
| Right-click | Open row actions. |

A normal open establishes the range anchor. Modifier selection changes selection without opening the clicked message.

### Bulk and context-menu targeting

The bulk toolbar appears only when more than one message is selected. It offers **Read**, **Unread**, and **Delete**.

A row context menu targets messages as follows:

- If the right-clicked row is part of the current multi-selection, its action applies to the entire selection.
- If the right-clicked row is not selected, its action applies only to that row.

The delete label reflects the selected-message count when it will delete multiple messages.

### Delete and focus behavior

Message deletion is optimistic: rows disappear before the server round trip completes.

When deletion removes the open or focused message, Hoomail chooses a nearby surviving row, opens it when necessary, moves focus to it, and scrolls it into view. It prefers the row now occupying the deleted row's position; when deletion occurred at the end, it uses the preceding surviving row. If no message survives, the viewer has no open message.

After any message action, multi-selection is cleared. If deletion fails, the list is refreshed from the server without a visible error notification.

## Message viewer

### Header and invitation information

The viewer displays the subject, sender, recipients, optional Cc recipients, received date/time, and message size. Dates and times use the browser's locale and time zone.

Parsed calendar parts appear above the body as read-only invitation cards. A card can identify an invitation, updated invitation, cancellation, or attendee response and can show:

- sequence number;
- event title and date/time or all-day state;
- location;
- organizer;
- attendees and their accepted, declined, tentative, or no-reply state; and
- description.

These cards display calendar and participation information only. Hoomail does **not** provide accept, decline, tentative, or other outbound RSVP controls.

### Display tabs and fallback order

The viewer has four tabs:

1. **HTML**
2. **Plain text**
3. **Source**
4. **Inspect**

HTML and Plain text are disabled when that body part is absent. On each newly opened message, the initial tab is selected in this order:

1. HTML, when available;
2. Plain text, when HTML is absent; or
3. Source, when neither body representation is available.

The **Source** tab shows captured header values labeled as raw headers. It is not a complete raw MIME-message viewer. If no captured headers are present, it shows **No headers captured.**

All enabled tab buttons participate in normal sequential Tab navigation. The tab list also supports:

- `Arrow Left` / `Arrow Right`: activate the previous or next enabled tab, wrapping at either end.
- `Home` / `End`: activate the first or last enabled tab.

### Sender-faithful HTML, sanitization, and sandboxing

HTML email is a standards-valid message body, even when it uses elaborate tables, branding, colors, typography, spacing, and inline CSS. Hoomail preserves those sender choices when they are within its security allowlist. It is an inspection tool, not a pixel-perfect Outlook, Gmail, or Apple Mail emulator; rendering differences caused by each client's supported HTML/CSS subset remain compatibility differences.

Before display, the server rewrites CID image references only from captured resources scoped to the selected multipart/related content represented by parser storage, then applies a Bluemonday parsed allowlist. Safe email tables, ordinary formatting, links, images, and a conservative set of inline presentation properties survive. Scripts, frames, forms, active embeds, event handlers, unsafe URL schemes, CSS URL/network functions, remote images, fonts, media, and other fetch initiators are removed. This parser-based policy replaces the former regex transformation as the security boundary.

The UI then adds only security and containment metadata: `html`/`body` maximum-width containment, responsive image maximum width, a restrictive Content Security Policy, and `no-referrer`. The message body keeps the browser-default outer margin; Hoomail does not override it. It does **not** impose Hoomail typography, foreground colors, background colors, link colors, or padding on the message. Consequently:

- the Hoomail application chrome follows the browser/operating-system light or dark preference;
- the message canvas remains white in either app theme unless the sender's allowed styling supplies a different background;
- explicitly styled message content keeps its safe sender-provided typography, colors, spacing, and background; and
- unstyled message content uses browser document defaults rather than inheriting or being recolored by the app theme.

Sanitization, CSP, and iframe sandboxing are separate defenses. The message is rendered in an iframe with an empty `sandbox` attribute, no scripts or same-origin permission, and no referrer. Only the visible email iframe is in the Tab order. The viewer keeps the previous frame visible until the replacement document is ready, preserving the stable viewer shell during message switches.

Remote content is blocked by default and the preview never requests sender-controlled images, stylesheets, fonts, frames, media, or CSS resources. CID images can load only from Hoomail's own captured-attachment endpoint. This differs from mail clients that proxy or optionally load remote images; see [Gmail's image policy](https://support.google.com/mail/answer/145919) and [Outlook external-image protection](https://support.microsoft.com/en-us/outlook/external-image-protection-in-outlook-com-43c0c17e-8fd1-41c6-93fe-ffe54638e82b).

Safe absolute HTTP(S) and `mailto:` link destinations are preserved for inspection, but links do not navigate or open windows from the empty-sandbox preview. Review the destination in **Inspect** before opening it separately; Inspect's explicit open control uses a new tab with opener isolation.

### Attachments

Only regular attachments listed by the viewer have download controls; inline CID resources and recognized calendar parts are omitted. Preview is intentionally limited to the same conservative media allowlist used by the attachment endpoint:

| Content type | Preview |
| --- | --- |
| `image/png`, `image/jpeg`, `image/gif`, `image/webp` | Image preview dialog. |
| `text/plain`, `text/csv` | Text preview dialog, limited to the first `100,000` characters. |
| PDF, HTML/XHTML, SVG/XML, MHTML, JavaScript, any other active/unknown type | No inline preview; download only. |

MIME parameters and case are normalized before this decision. Every successful attachment response has `X-Content-Type-Options: nosniff`; `?download=1` forces download for an otherwise inline-capable item. The UI shows **Loading…** while fetching a text preview and **Could not load preview.** if it fails. There is no embedded PDF preview.

### Inspection

The **Inspect** tab fetches analysis when activated and announces **Analyzing message…** followed by completion to assistive technology.

Inspection includes:

- header checks with `pass`, `warn`, `fail`, or `info` status and explanatory details;
- extracted links, external images, and suspected tracking pixels;
- controls that open extracted URLs in a new tab with opener isolation; and
- a structural MIME tree containing content type, declared charset, transfer-encoding label, disposition, filename, and raw-part body size.

Opening an extracted link or image URL contacts that external destination. Inspection itself and ordinary HTML preview do not fetch it; review the destination before activating it.

The MIME tree depends on stored raw source. Older messages captured before inspection support can show checks and extracted URLs but display **Raw source not available for this message (received before inspection support).** instead of a MIME tree.

## Calendar view

Calendar data is loaded for the selected inbox when Calendar view is opened. If no inbox is selected, the view asks the user to select one.

### Month layout

The calendar is a fixed, Monday-first, six-row by seven-column month grid. It provides **Today**, **Previous month**, and **Next month** controls.

Current limitations:

- Monday is fixed as the first day of the week; there is no user setting for Sunday-first or another regional start day.
- Events are grouped only under the local-calendar day containing their start time. A multi-day event is not painted across every day of its duration.
- This is a month view, not a week, agenda, or multi-day-span view.
- Event dates and times use the browser's locale and time zone.

A day cell shows up to two event titles, followed by **+N more** when additional events exist. Selecting a day shows its complete event list below the grid, including status, local time or all-day state, location, and organizer. Cancelled events are struck through; tentative events have a distinct status treatment.

### Opening a source message

Activate an event in the selected-day list to return to Mail view and open the event's latest source message. If that message row survives the active search filter, focus moves to it and it is scrolled into view. Otherwise the message still opens in the viewer, but the user may need to clear the search to reveal and focus its row.

An event without an available source-message ID is disabled and cannot be opened from the calendar.

### Calendar keyboard model

The calendar grid has 42 grid cells and one roving Tab stop: the selected day.

- `Arrow Left` / `Arrow Right`: move one day.
- `Arrow Up` / `Arrow Down`: move one week.
- Movement crosses month boundaries, changes the visible month, and retains focus on the destination day.
- `Enter` or `Space`: select the focused day through its native button behavior.

The event entries below the grid are native buttons; use `Enter` or `Space` to open an enabled source message.

## Keyboard and focus reference

### Inbox and primary-view controls

Inbox entries and the **Mail** and **Calendar** view selectors are native buttons in the normal Tab order. Use `Enter` or `Space` to activate them. The selected inbox exposes `aria-current`; the active primary-view button exposes its pressed state.

### Message list

Message-list shortcuts are deliberately scoped. They run only when focus is inside the message list in Mail view; they do not consume the same keys from unrelated controls. When focus is in the search input, all list shortcuts are ignored except `Escape`, which blurs the input.

Only the open message row—or the first displayed row when no displayed message is open—is in the sequential Tab order. After Tab enters the list:

| Key | Result |
| --- | --- |
| `Arrow Down` | Focus and open the next displayed message; stop at the last row. |
| `Arrow Up` | Focus and open the previous displayed message; stop at the first row. |
| `Enter` / `Space` | Activate the focused message button. |
| `Delete` / `Backspace` | Delete the multi-selection, or the open message when there is no multi-selection. |
| `Ctrl`+`A` / `Cmd`+`A` | Select every message currently displayed, including only current search results when a search is active. |
| `Escape` | Clear the current multi-selection. |

Arrow navigation moves native browser focus before changing the open message, keeping the focus ring, selected row, viewer, and subsequent Enter activation aligned.

### Context menus

Inbox and message context menus can be opened with:

- right-click;
- the keyboard **Context Menu** key; or
- `Shift`+`F10`.

When a context menu opens, its first item receives focus.

| Key | Result |
| --- | --- |
| `Arrow Down` / `Arrow Up` | Move between enabled menu items and wrap. |
| `Home` / `End` | Move to the first or last enabled item. |
| `Enter` / `Space` | Activate the focused native menu-item button. |
| `Escape` | Close the menu and restore focus to its inbox or message trigger. |
| `Tab` | Close the menu and continue to the next focusable page control. |
| `Shift`+`Tab` | Close the menu and continue to the previous focusable page control. |

A pointer press outside the menu or loss of window focus dismisses it without restoring trigger focus.

### Dialogs

Send-test, reset, and attachment-preview dialogs:

- move focus into the dialog when opened;
- trap `Tab` and `Shift`+`Tab` within visible focusable controls;
- close on `Escape`;
- can be closed with their close button or by activating the backdrop; and
- restore focus to the control that opened them, or to the previously focused element when no explicit dialog trigger is registered.

### Viewer tabs

Every enabled viewer tab is a normal Tab stop. Use `Arrow Left`/`Arrow Right` or `Home`/`End` for direct tab-list navigation. Arrow and boundary keys both move focus and activate the destination tab.

### Scroll regions

Scrollable UI regions become keyboard-focusable only when they actually overflow and do not already contain a focusable descendant. This avoids adding inert scroll containers to the Tab order while still allowing keyboard scrolling when it is needed.

## Loading and realtime updates

Hoomail uses server-sent events (SSE) for event-specific refreshes:

- a new or deleted inbox refreshes the inbox list;
- a new message or message change refreshes the inbox list plus that inbox's unfiltered list and any cached filtered message lists;
- a calendar change refreshes that inbox's calendar events; and
- a full reset refreshes the inbox list, clears cached message lists and calendar events, and invalidates cached message details and inspections.

New mail can therefore appear in the current list without a manual page reload. Message and inspection caches are invalidated by realtime handling only for a full reset, not for ordinary message or calendar events.

Observable loading states include:

- **Loading calendar…** while the separately loaded Calendar interface is being fetched;
- **Loading message…** before any detail is available;
- an accessible busy/loading announcement while switching message details;
- **Analyzing message…** in Inspect; and
- attachment text-preview loading and failure text.

The message viewer deliberately retains the previously displayed detail while the next message request is pending, keeping the viewer shell and existing iframe mounted until replacement content is ready.

## Current error and status limitations

The UI has targeted feedback for Send-test failures and text-attachment preview failures, but it does not currently provide a general network-error banner or notification system.

Consequences include:

- failed inbox, message-list, or calendar loads can appear empty;
- a failed initial message-detail request can remain on **Loading message…**;
- mailbox deletion and message read/unread/delete failures have no visible error notification and are handled through silent refresh where implemented;
- inspection request failure has no dedicated error state; and
- realtime connection state, disconnection, reconnection, and last-event time are not displayed.

The absence of a realtime-status indicator means the interface does not tell users or accessibility reviewers whether SSE is currently connected. A manual reload may be necessary when validating behavior after an unseen network interruption.
