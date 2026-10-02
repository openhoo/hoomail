# HTTP assertions and event handling

Use an existing Hoomail base URL, normally `http://127.0.0.1:3000`. Fetch
`GET /openapi.json` from that instance when exact deployed schemas matter.
Responses may mix snake_case and camelCase; do not normalize blindly.

## Find a test message

1. `GET /api/mailboxes` returns `{"mailboxes":[...]}`. Match `address` to the
   normalized test recipient and retain its numeric `id`.
2. `GET /api/mailboxes/{id}/messages?q=<URL-encoded-marker>` returns
   `{"messages":[...]}`. Search covers subject, sender name/address, and plain
   text, not headers or HTML. Lists use `from_address`, `received_at`, `is_read`
   (0/1), and other snake_case fields.
3. `GET /api/messages/{id}` returns `{"message":{...},"attachments":[...]}`.
   Detail uses `fromAddress`, `mailboxId`, `receivedAt`, `icalEvents`, `text`,
   `html`, `to`, and `cc`. This GET marks an unread message read.
4. `GET /api/messages/{id}/source` returns exact stored RFC 822 bytes without
   changing read state; use it for raw-header/MIME assertions.

Poll with a finite deadline and check both recipient and unique marker. Lists
can be empty before ingestion, and a nonexistent mailbox's messages endpoint
also returns an empty list. Do not infer success from SMTP connectivity alone.
Timestamps are Unix milliseconds. Invalid path IDs normally return JSON 400;
unexpected storage failures and unknown routes can be plain-text 500/404.

## Attachments, inspection, calendar

- Attachment metadata includes `id`, `filename`, `contentType`, and `size`.
  Retrieve `/api/attachments/{id}` or append `?download=1`. Active formats/PDF
  are download-only. Assert decoded content and metadata for your own fixtures.
- `/api/messages/{id}/inspect` returns a freshly computed offline report,
  without marking read. A partial/truncated result is not a clean bill of health.
  The browser may cache results; Retry requests a new report.
- `/api/mailboxes/{id}/events` returns reconciled calendar data. Assertions need
  UID, sequence, cancellation, and attendee state, not merely the presence of
  an `.ics` attachment. Use a fixed test timezone for displayed times.

## SSE

`GET /api/events` is a global best-effort, non-replayable invalidation stream.
There is no resume cursor or delivery guarantee. Subscribe before triggering an
action if an event assertion matters, and always refetch authoritative API state
after reconnect. For ordinary mail-delivery tests bounded API polling is simpler.

## Mutations and cleanup

`POST /api/messages/actions` accepts an action `delete`, `read`, or `unread`
and an `ids` array. Use only IDs correlated to the test:

```json
{"action":"delete","ids":[14]}
```

`DELETE /api/mailboxes/{id}` cascades to messages, attachments, and calendar
state. `POST /api/reset` clears every mailbox and resets ID sequences. Neither
endpoint provides an authentication or confirmation barrier. Use reset only
on your dedicated disposable instance; shared-instance tests clean their own IDs.

Built-in samples are submitted to `/api/send-test` as `{"to":"run@example.test",
"kind":"invite"}`. Plain/invite/update/cancellation are supported kinds;
unknown kinds default to plain. Because samples bypass the application's sender,
keep sample health evidence separate from application-mail evidence.
