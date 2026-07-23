# HTTP API and server-sent events

Hoomail exposes a same-origin JSON API, raw attachment responses, and one global server-sent events (SSE) stream from the same Go HTTP server that serves the web application.

This document describes the current implementation. It is a contract reference, not a promise of additional validation, authentication, or event delivery guarantees.

## Conventions

### Routes and methods

The API routes are:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/mailboxes` | List mailboxes. |
| `DELETE` | `/api/mailboxes/{id}` | Delete a mailbox and its stored data. |
| `GET` | `/api/mailboxes/{id}/messages` | List or search messages in a mailbox. |
| `GET` | `/api/mailboxes/{id}/events` | List reconciled calendar events for a mailbox. |
| `GET` | `/api/messages/{id}` | Get parsed message detail and mark it read. |
| `GET` | `/api/messages/{id}/inspect` | Inspect raw MIME, extracted links, and diagnostic checks. |
| `POST` | `/api/messages/actions` | Delete messages or set their read state. |
| `GET` | `/api/attachments/{id}` | Return stored attachment bytes. |
| `GET` | `/api/events` | Open the global SSE stream. |
| `POST` | `/api/reset` | Delete all stored data and reset generated IDs. |
| `POST` | `/api/send-test` | Send a built-in sample through Hoomail's SMTP listener. |

Routing is method-sensitive. An unsupported method, an `/api/*` path not captured by the route matchers described below, or a `HEAD` request for a `GET`-only API route returns Go's plain-text response:

```text
HTTP/1.1 404 Not Found
Content-Type: text/plain; charset=utf-8

404 page not found
```

The API does not return `405 Method Not Allowed` or an `Allow` header.

Nested dynamic routes are matched by prefix and terminal suffix rather than by exact segment count. Consequently, greedy malformed paths such as `/api/mailboxes/1/extra/messages`, `/api/mailboxes/1/extra/events`, or `/api/messages/1/extra/inspect` reach the corresponding handler; the whole intervening value is parsed as the ID and currently returns the endpoint-specific JSON `400`, not the unknown-route plain-text `404`.

Outside `/api/`, production `GET` and `HEAD` requests are served from the embedded web application. If a static path does not exist, the server returns the SPA index. Other methods and deployments without a configured static filesystem return `404`.

### IDs

Path IDs are parsed first as IEEE 754 binary64 (`float64`) values, then accepted when finite and integer-valued and when the rounded float passes the current `math.MinInt64`/`math.MaxInt64` comparisons, before conversion to `int64`. `strconv.ParseFloat` syntax is exposed: besides ordinary decimal integers, forms such as `1.0`, `1e0`, signed values, and hexadecimal floating-point forms such as `0x1p0` can be accepted. Binary64 cannot distinguish every integer above $2^{53}$: for example, `9007199254740993` is handled as `9007199254740992`. Boundary checks are also imprecise because `float64(math.MaxInt64)` is $2^{63}$; currently `9223372036854775807` and `9223372036854775808` can pass and convert to `-9223372036854775808`, while `-9223372036854775809` can round to that same minimum value. Clients should send only the ordinary base-10 integer representation returned by the API, with IDs no greater than $2^{53}$ if exact parsing matters.

Invalid path IDs return `400` JSON with an endpoint-specific message:

```json
{"error":"Invalid message id"}
```

The corresponding messages are `Invalid mailbox id`, `Invalid message id`, and `Invalid attachment id`.

### JSON and content types

Successful JSON responses and documented JSON client errors are compact JSON without a trailing newline:

```text
Content-Type: application/json
```

There is no `charset` parameter and no general JSON cache header. JSON request endpoints do not require or check a request `Content-Type`. The API handler also does not impose its own request-body size limit.

Most expected client errors use:

```json
{"error":"Error text"}
```

Unexpected storage, decoding, or response-encoding failures return Go's plain-text `500 Internal Server Error` response rather than JSON:

```text
Internal Server Error
```

Unknown routes similarly return a plain-text `404`, not the JSON error shape.

### Values and timestamps

- All documented timestamps are Unix time in milliseconds.
- Nullable database values are represented as JSON `null`.
- Message-list booleans `is_read` and `has_ical` are integers (`0` or `1`). Calendar `allDay` is a JSON boolean.
- Field naming is route-specific. Mailbox and message-list responses use snake_case; message detail, calendar, inspection, and SSE payloads primarily use camelCase. Clients must use the exact names below.

## Mailboxes

### `GET /api/mailboxes`

Returns `200 OK`:

```json
{
  "mailboxes": [
    {
      "id": 1,
      "address": "test@example.com",
      "created_at": 1784808000000,
      "last_message_at": 1784808060000,
      "total_count": 3,
      "unread_count": 2
    }
  ]
}
```

Mailbox fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | Mailbox ID. |
| `address` | string | Normalized mailbox address. |
| `created_at` | integer | Creation time, Unix milliseconds. |
| `last_message_at` | integer or `null` | Most recent message time. A mailbox created without a message can be `null`. |
| `total_count` | integer | Current number of stored messages. |
| `unread_count` | integer | Current number of messages whose read flag is `0`. |

Mailboxes are ordered by latest activity: `last_message_at` when present, otherwise `created_at`, descending.

Storage failures return the common plain-text `500` response.

### `DELETE /api/mailboxes/{id}`

Deletes the mailbox. SQLite foreign-key cascades remove its messages, attachments, and calendar events.

Responses:

| Status | Body | Condition |
| --- | --- | --- |
| `200` | `{"ok":true}` | The mailbox existed and was deleted. |
| `400` | `{"error":"Invalid mailbox id"}` | The path ID is invalid. |
| `404` | `{"error":"Mailbox not found"}` | No mailbox has that ID. |
| `500` | plain text | Storage failure. |

A successful deletion emits `mailbox:deleted` with the deleted ID.

## Messages

### `GET /api/mailboxes/{id}/messages`

Optional query parameter:

| Parameter | Meaning |
| --- | --- |
| `q` | Search subject, sender address, sender name, and the stored plain-text body. Leading and trailing whitespace is ignored. |

The search is a SQLite `LIKE` substring search. Literal `%`, `_`, and `\` in `q` are escaped. It does not search headers or the HTML body. With an empty or whitespace-only `q`, all messages in the mailbox are listed.

Returns `200 OK`:

```json
{
  "messages": [
    {
      "id": 14,
      "from_address": "sender@example.com",
      "from_name": "Sender",
      "subject": "Hello",
      "snippet": "A normalized preview of the text body",
      "is_read": 0,
      "received_at": 1784808060000,
      "has_ical": 0,
      "attachment_count": 1
    }
  ]
}
```

Message-list fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | Message ID. |
| `from_address` | string or `null` | Parsed sender address. |
| `from_name` | string or `null` | Parsed sender display name. |
| `subject` | string or `null` | Parsed subject. |
| `snippet` | string | Whitespace-normalized preview, limited to 140 Unicode characters. It uses the text body, or stripped HTML only when text is absent. |
| `is_read` | `0` or `1` | Stored read state. |
| `received_at` | integer | Receipt time, Unix milliseconds. |
| `has_ical` | `0` or `1` | Whether parsed calendar JSON is stored for the message. |
| `attachment_count` | integer | Number of attachments whose `content_id` is null. This can include a parsed calendar part that the detail endpoint later filters out. |

Messages are ordered by `received_at` descending.

A valid but nonexistent mailbox ID returns `200` with `{"messages":[]}`; the route does not separately check mailbox existence. An invalid ID returns `400` with `Invalid mailbox id`. Storage failures return plain-text `500`.

### `GET /api/messages/{id}`

Returns parsed message detail and visible attachment metadata.

**Read side effect:** after finding the message, this `GET` marks it read if it was unread, before decoding the stored recipient, header, or calendar JSON fields used in the response. The unread-to-read mutation emits `messages:changed` for the mailbox. If a later stored-JSON decode fails, the request returns plain-text `500` even though the read mutation and event have already occurred. Retrieving an already-read message does not emit that event. Clients and intermediaries must not treat this route as a side-effect-free read or assume an error response left state unchanged.

Returns `200 OK`:

```json
{
  "message": {
    "id": 14,
    "mailboxId": 1,
    "fromAddress": "sender@example.com",
    "fromName": "Sender",
    "to": [
      {"address": "test@example.com", "name": "Test"}
    ],
    "cc": [],
    "subject": "Hello",
    "html": "<p>Hello</p>",
    "text": "Hello",
    "headers": {
      "message-id": "<example@example.com>"
    },
    "size": 1234,
    "receivedAt": 1784808060000,
    "icalEvents": []
  },
  "attachments": [
    {
      "id": 9,
      "filename": "note.txt",
      "contentType": "text/plain",
      "size": 42
    }
  ]
}
```

Message fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | Message ID. |
| `mailboxId` | integer | Owning mailbox ID. |
| `fromAddress` | string or `null` | Parsed sender address. |
| `fromName` | string or `null` | Parsed sender display name. |
| `to` | address array | Parsed `To` recipients. |
| `cc` | address array | Parsed `Cc` recipients. |
| `subject` | string or `null` | Parsed subject. |
| `html` | string or `null` | Selected HTML representation after scoped CID rewriting and parsed allowlist sanitization, as described below. |
| `text` | string or `null` | Plain-text body. |
| `headers` | object | Parsed header names and string values. |
| `size` | integer | Stored message size in bytes. |
| `receivedAt` | integer | Receipt time, Unix milliseconds. |
| `icalEvents` | calendar-message-event array | Parsed calendar components, or an empty array when none were stored. |

An address entry is:

```json
{"address":"person@example.com","name":"Person"}
```

`address` is always present. `name` is omitted when unavailable.

A calendar-message-event is:

```json
{
  "method": "REQUEST",
  "uid": "meeting@example.com",
  "sequence": 0,
  "summary": "Standup",
  "description": null,
  "location": null,
  "status": "CONFIRMED",
  "organizerAddress": "organizer@example.com",
  "organizerName": null,
  "attendees": [
    {
      "address": "test@example.com",
      "name": "Test",
      "partstat": "NEEDS-ACTION",
      "role": "REQ-PARTICIPANT"
    }
  ],
  "dtstart": 1784894400000,
  "dtend": 1784896200000,
  "allDay": false
}
```

For calendar-message-events, `summary`, `description`, `location`, `status`, `organizerAddress`, `organizerName`, and `dtend` can be `null`. Attendee `name`, `partstat`, and `role` are omitted when unavailable.

Attachment metadata fields are `id` (integer), `filename` (string or `null`), `contentType` (string or `null`), and `size` (integer bytes).

#### HTML sanitization, CID rewriting, and attachment filtering

The `html` field is a safe display projection, not a raw copy:

1. MIME ingestion recursively selects the supported `multipart/alternative` representation and the `multipart/related` root (`start` Content-ID, or the first part), with CID resources scoped to that selected related branch.
2. Matching `cid:` image URLs are percent-decoded and rewritten to `/api/attachments/{attachmentId}`.
3. A Bluemonday policy parses and allowlists the rewritten HTML. Safe email tables, ordinary text formatting, links, images, and conservative inline presentation properties remain; active elements/attributes, unsafe schemes, CSS network functions, remote subresources, fonts, frames, forms, media, and other fetch initiators are removed.

This policy intentionally accepts standards-valid rich HTML; it is a security boundary, not a rule that email must resemble plain correspondence. Client-specific CSS support remains a compatibility concern, and Hoomail does not emulate Gmail or Outlook pixel-for-pixel. The selected, charset-decoded HTML is stored without display sanitization, and the complete original MIME remains stored unchanged.

Safe absolute HTTP(S) and `mailto:` anchors are externalized with `target="_blank"` and `rel="noopener noreferrer"`, but the empty-sandbox preview cannot navigate or open them. Inspection exposes destinations for explicit review/opening. Remote images may appear in inspection diagnostics but are never fetched by the detail projection or preview.

The response's `attachments` array omits:

- every attachment with a non-null content ID, including CID resources referenced from rewritten HTML;
- when parsed calendar JSON exists for the message, non-CID parts recognized as calendar parts. Recognition is based on a content type containing `text/calendar` or `application/ics`, or a filename ending in `.ics`, case-insensitively.

The underlying attachment remains addressable by its ID if a client already knows that ID.

### `GET /api/messages/{id}/inspect`

Inspects the stored raw MIME and parsed message fields. This route does not mark the message read.

Returns `200 OK`:

```json
{
  "mimeTree": {
    "contentType": "multipart/alternative",
    "charset": null,
    "encoding": null,
    "disposition": null,
    "filename": null,
    "size": 456,
    "children": []
  },
  "links": [
    {
      "href": "https://example.com",
      "text": "Example",
      "kind": "link"
    }
  ],
  "checks": [
    {
      "id": "message-id",
      "label": "Message-ID",
      "status": "pass",
      "detail": "Message-ID header present."
    }
  ]
}
```

`mimeTree` is `null` when no raw MIME bytes are stored. Otherwise it is a recursive MIME node:

| Field | Type |
| --- | --- |
| `contentType` | string |
| `charset` | string or `null` |
| `encoding` | string or `null` |
| `disposition` | string or `null` |
| `filename` | string or `null` |
| `size` | integer |
| `children` | MIME node array |

Each extracted link has `href`, `text`, and `kind`. Current kinds are `link`, `image`, and `tracking-pixel`. CID and data-URI images are not included in the image-link results. If HTML is absent, HTTP(S) URLs are extracted from the text body.

Each diagnostic check has string fields `id`, `label`, `status`, and `detail`. Current status values are `pass`, `warn`, and `info`. Checks are heuristic diagnostics, not delivery or security guarantees.

Responses are `400` with `Invalid message id`, `404` with `Message not found`, or plain-text `500` on internal failure.

### `POST /api/messages/actions`

Request shape:

```json
{
  "action": "read",
  "ids": [14, 15]
}
```

Supported action strings are:

| Action | Effect |
| --- | --- |
| `delete` | Delete matching messages. Attachments are removed by SQLite cascade. Calendar rows retain their current `lastMessageId` value because it is not a foreign key. |
| `read` | Set matching messages to read. |
| `unread` | Set matching messages to unread. |

Body handling is intentionally permissive in some places and strict in others:

- The body must contain exactly one valid JSON value. Malformed JSON, an empty body, or a trailing second JSON value returns `400 Invalid JSON body`.
- The decoded top-level value is treated as an object; another JSON type consequently has no usable IDs or action.
- `ids` is useful only when it is an array. JSON numbers are decoded as binary64 (`float64`) values; entries are retained when the rounded value is integer-valued and passes the same imprecise `int64` boundary comparisons as path IDs. JSON permits decimal and exponent forms, but not the hexadecimal forms accepted in paths. Precision is lost above $2^{53}$: for example, `9007199254740993` becomes `9007199254740992`, and the current boundary behavior can convert `9223372036854775807` or `9223372036854775808` to `-9223372036854775808`. Strings, fractional numbers, booleans, objects, arrays, and `null` entries are silently ignored. Clients should send ordinary base-10 JSON integers no greater than $2^{53}$ if exact parsing matters.
- At least one valid numeric ID must remain. This check happens before action validation.
- `action` must be one of the exact lowercase strings above.

Responses:

| Status | Body | Condition |
| --- | --- | --- |
| `200` | `{"ok":true}` | The action was accepted, including when none of the IDs exist or the requested read state was already set. |
| `400` | `{"error":"Invalid JSON body"}` | The body is malformed, empty, or contains more than one JSON value. |
| `400` | `{"error":"No valid message ids provided"}` | No acceptable numeric ID remains. |
| `400` | `{"error":"Unknown action"}` | IDs are valid but `action` is unsupported, missing, or not a string. |
| `500` | plain text | Storage failure. |

The operations are idempotent with respect to final stored state: deleting missing IDs and repeatedly setting the same read state still returns `200`. The response does not report matched or changed counts. For `read` and `unread`, each mailbox containing at least one supplied existing ID receives one `messages:changed` event even if the stored state was already the requested value. `delete` emits one `messages:changed` event per affected mailbox; deleting only missing IDs emits none.

## Calendar view

### `GET /api/mailboxes/{id}/events`

Returns the mailbox's reconciled calendar state, ordered by `dtstart` ascending:

```json
{
  "events": [
    {
      "id": 3,
      "uid": "meeting@example.com",
      "sequence": 1,
      "summary": "Updated standup",
      "description": null,
      "location": "Owl Tree Conference Room",
      "status": "CONFIRMED",
      "organizerAddress": "owl@hoomail.local",
      "organizerName": "The hoomail Owl",
      "attendees": [
        {
          "address": "test@example.com",
          "partstat": "NEEDS-ACTION"
        }
      ],
      "dtstart": 1784898000000,
      "dtend": 1784899800000,
      "allDay": false,
      "lastMessageId": 14,
      "updatedAt": 1784808060000
    }
  ]
}
```

Event fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | integer | Reconciled calendar-row ID. |
| `uid` | string | iCalendar UID, unique within the mailbox. |
| `sequence` | integer | Reconciled iCalendar sequence. |
| `summary` | string or `null` | Event summary. |
| `description` | string or `null` | Event description. |
| `location` | string or `null` | Event location. |
| `status` | string | Stored status, defaulting to `CONFIRMED`; cancellations use `CANCELLED`. |
| `organizerAddress` | string or `null` | Organizer address. |
| `organizerName` | string or `null` | Organizer display name. |
| `attendees` | attendee array | Decoded attendee state. |
| `dtstart` | integer | Start time, Unix milliseconds. |
| `dtend` | integer or `null` | End time, Unix milliseconds. |
| `allDay` | boolean | Whether the source event is all-day. |
| `lastMessageId` | integer or `null` | Last message associated with reconciliation. It may refer to a subsequently deleted message. |
| `updatedAt` | integer | Last reconciliation time, Unix milliseconds. |

Attendees use the same `{address, name?, partstat?, role?}` shape described for message calendar data.

A valid but nonexistent mailbox returns `200` with `{"events":[]}`. Invalid IDs return `400 Invalid mailbox id`; storage or attendee-JSON decoding failures return plain-text `500`.

This endpoint returns reconciled state, not every calendar part from every message. REQUEST/PUBLISH updates with a sequence at least as high as the stored sequence replace the row, CANCEL marks it cancelled, and REPLY updates attendee participation for an existing row.

## Attachments

### `GET /api/attachments/{id}`

Returns the stored bytes directly, not JSON.

Successful inline-capable response example:

```text
Content-Type: text/plain
Content-Length: 42
Content-Disposition: inline; filename="note.txt"
Cache-Control: private, max-age=3600
X-Content-Type-Options: nosniff
```

Behavior:

- The stored content type is parsed, lowercased, and stripped of parameters. Missing, empty, or malformed values become `application/octet-stream`.
- Only `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `text/plain`, and `text/csv` are inline-capable. PDF, HTML/XHTML, SVG/XML, MHTML, JavaScript, unknown, and other active formats use `attachment`.
- `?download=1` forces `attachment`; other `download` values do not.
- Every attachment response, including validation, missing-record, and storage-error responses from this route, includes `X-Content-Type-Options: nosniff`.
- The filename is reduced to its basename and control/path-separator characters are removed; a missing or unusable value becomes `attachment-{id}`. `Content-Disposition` includes a safe ASCII quoted fallback and, for non-ASCII names, a UTF-8 `filename*` parameter.
- The private cache lifetime is one hour. After message deletion or reset, a browser or intermediary may retain a previously fetched private response until its cache lifetime expires; clients should clear relevant local caches when invalidated.

Responses:

| Status | Body | Condition |
| --- | --- | --- |
| `200` | raw bytes | Attachment exists and its content is non-null. |
| `400` | JSON `Invalid attachment id` | Invalid path ID. |
| `404` | JSON `Attachment not found` | Attachment is missing or has null content. |
| `500` | plain text | Storage failure. |

Attachment bytes are not content-sanitized. The narrow inline allowlist, forced download for active/unknown formats, and `nosniff` reduce browser execution risk; Hoomail still must run on an isolated development origin.

## Destructive reset

### `POST /api/reset`

No request body, confirmation token, authentication, or CSRF token is required.

The operation transactionally deletes attachments, calendar events, messages, and mailboxes, then removes the SQLite autoincrement sequence entries for all four tables. It emits `reset` only after the transaction commits.

Returns `200`:

```json
{"ok":true}
```

Storage failure returns plain-text `500`.

**ID reuse warning:** because the generated-ID sequences are reset, future mailboxes, messages, attachments, and calendar rows can reuse IDs that existed before the reset. Clients must discard all cached list, detail, inspection, attachment, and calendar state on `reset`; an old ID must not be assumed to identify the same object afterward.

## Built-in test sender

### `POST /api/send-test`

The optional request body is normally:

```json
{
  "to": "test@example.com",
  "subject": "Optional subject",
  "kind": "plain"
}
```

Normalized request behavior:

| Field | Behavior |
| --- | --- |
| `to` | A string is trimmed and lowercased. Empty, missing, or non-string values become `test@hoomail.local`. Validation is only the current simple pattern `non-space/non-@` + `@` + `non-space/non-@` + `.` + `non-space/non-@`; it is not full email-address validation. |
| `subject` | A string is trimmed. Missing or non-string values become empty. The built-in sender then defaults an empty plain subject to `hoomail delivery test`, and an empty calendar summary to `Owl standup meeting`. |
| `kind` | Exact supported strings are `plain`, `invite`, `update`, and `cancellation`. Missing, non-string, differently cased, or unknown values silently become `plain`. |

Unlike `/api/messages/actions`, body decoding failure is not a client error here. An empty body, malformed JSON, a trailing extra JSON value, or a valid non-object JSON value is treated as an empty object and therefore uses the defaults above. Recognized fields from a partially malformed document are not recovered; all defaults are used.

The production sender opens an SMTP connection to Hoomail's configured local SMTP listener and sends one generated message. `plain` includes text, HTML, and a text attachment. The calendar kinds generate REQUEST, updated REQUEST, or CANCEL calendar samples respectively.

Responses:

| Status | Body | Condition |
| --- | --- | --- |
| `200` | `{"ok":true}` | The sender completed the SMTP transaction. |
| `400` | `{"error":"Invalid recipient address"}` | The normalized recipient fails the simple pattern. |
| `502` | `{"error":"Could not reach the SMTP server. Is it running?"}` | No sender is configured or any sender/SMTP step fails. |

The response does not include the created mailbox or message ID. Normal SMTP ingestion subsequently produces the same database changes and SSE notifications as other delivered mail.

## Server-sent events

### `GET /api/events`

The global SSE endpoint responds `200 OK` with exactly these handler-set headers:

```text
Content-Type: text/event-stream
Cache-Control: no-cache, no-transform
Connection: keep-alive
```

It immediately flushes this stream-local greeting:

```text
data: {"type":"connected"}

```

Broker notifications use one compact JSON object in an SSE `data` field and are flushed after each frame:

```text
data: {"type":"messages:changed","mailboxId":1}

```

Every 25 seconds, the handler flushes an SSE comment heartbeat:

```text
: ping

```

The stream does not send SSE `event:`, `id:`, or `retry:` fields. All broker notifications arrive through the browser `message` event; clients dispatch using the JSON `type`. The `connected` greeting is generated by each HTTP stream and is not a broadcast broker event.

If the response writer does not support HTTP flushing, the route returns the common plain-text `500` response before opening a stream. Once open, the handler ends on request cancellation or a write failure; it provides no terminal event.

### Event payloads and triggers

| JSON `type` | Exact payload shape | Current triggers |
| --- | --- | --- |
| `mailbox:new` | `{"type":"mailbox:new","mailbox":{"id":7,"address":"inbox@example.com"}}` | A committed message delivery creates a mailbox for a recipient, or POP3 opens a previously missing normalized mailbox. |
| `mailbox:deleted` | `{"type":"mailbox:deleted","mailboxId":7}` | `DELETE /api/mailboxes/{id}` successfully deletes a mailbox. |
| `messages:changed` | `{"type":"messages:changed","mailboxId":7}` | An unread `GET /api/messages/{id}` marks the message read, or a message action affects existing messages in the mailbox. |
| `calendar:changed` | `{"type":"calendar:changed","mailboxId":7}` | A committed message delivery contains one or more parsed calendar events and calendar reconciliation runs for the mailbox. The event can be emitted even when sequence/reply rules leave the visible reconciled row unchanged. |
| `message:new` | `{"type":"message:new","mailboxId":7,"message":{"id":11,"subject":"Welcome","fromAddress":"sender@example.com","fromName":null}}` | A message is stored and its transaction commits for that recipient mailbox. `subject`, `fromAddress`, and `fromName` can each be `null`. |
| `reset` | `{"type":"reset"}` | `POST /api/reset` commits successfully. |

For each recipient processed by a committed message delivery, notifications are queued in this order:

1. `mailbox:new`, only if that recipient mailbox was created;
2. `calendar:changed`, only when the message has parsed calendar events;
3. `message:new`.

A new delivery does not additionally emit `messages:changed`.

These events are invalidation hints. Except for the small summaries in `mailbox:new` and `message:new`, they do not contain authoritative resource state. Clients should refetch:

- `/api/mailboxes` after mailbox, message, or reset notifications;
- `/api/mailboxes/{mailboxId}/messages` and any active searches after `message:new` or `messages:changed`;
- `/api/mailboxes/{mailboxId}/events` after `calendar:changed`;
- all relevant resources after `reset` or any stream interruption.

### Delivery limits and reconnection

SSE delivery is in-memory, best-effort, and non-replayable:

- Each connected subscription has a 64-event channel buffer.
- Broadcasts preserve enqueue order for a subscriber while its buffer has capacity.
- Producers never wait for a slow subscriber. When its 64-slot buffer cannot accept the next event immediately, the hub removes that subscriber and closes its subscription channel.
- Overflow handling is only best-effort at the HTTP layer. The stream handler reads the subscription channel without checking whether it was closed, so a closed-channel receive can be serialized as a JSON frame with an empty `type` instead of promptly ending the connection; repeated empty-type frames or a stalled stream are possible current edge behavior. Overflow therefore does not promise a clean disconnect or a distinguishable terminal signal.
- The protocol provides no event ID, replay cursor, resume token, persisted event log, or acknowledgement.
- Events emitted before subscription, during disconnect, after buffer overflow, or during process restart can be missed.

Use the browser `EventSource` reconnection behavior or reconnect explicitly. After every reconnect, refetch authoritative API state rather than assuming that the next event repairs a gap. If output is malformed, incomplete, stalled, or has an unknown or empty `type`, treat the stream as invalid: reconnect and conservatively refetch authoritative state.

## Current security boundaries and limitations

Hoomail is intended for controlled development and test environments. The current HTTP server has:

- no authentication or authorization;
- no built-in TLS termination;
- no CSRF token validation or Origin/Referer enforcement;
- no CORS middleware and no configured `Access-Control-Allow-Origin` policy;
- no application-wide browser security-header middleware: no configured HSTS, `X-Frame-Options`, or `Permissions-Policy`; message frames receive their own restrictive CSP/referrer policy, and successful attachment responses receive `X-Content-Type-Options: nosniff`;
- no authorization or confirmation requirement on destructive message, mailbox, and reset routes.

Some Go standard-library error responses may add their own response-specific headers, but there is no application-wide security-header policy.

CORS is not an authentication boundary, and non-browser network clients are unrestricted. Deploy Hoomail on an isolated origin behind a trusted reverse proxy or network boundary that provides TLS, authentication, access control, and an appropriate browser-security header policy. Do not share its origin with trusted applications. Captured attachment bytes remain untrusted even though active and unknown formats are download-only by default.

## Implementation sources

The contract above is derived from the current repository implementation and tests:

- `internal/httpserver/httpserver.go` — routing, schemas, response handling, attachment behavior, SSE framing, reset, and send-test normalization.
- `internal/events/events.go` — event payloads and the 64-event best-effort subscription hub.
- `internal/store/store.go` and `internal/store/operations.go` — stored field shapes, ordering, side effects, reconciliation, reset, and event triggers.
- `internal/calendar/calendar.go` — calendar payload and part-recognition shapes.
- `internal/inspect/inspect.go` — sanitization, CID rewriting, MIME inspection, links, and checks.
- `internal/sendtest/sendtest.go` — built-in sample-message and SMTP behavior.
- `internal/httpserver/httpserver_test.go` and `internal/events/events_test.go` — exact error, header, framing, payload, filtering, mutation, ordering, and slow-subscriber assertions.
