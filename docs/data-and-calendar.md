# Data, inspection, and calendar behavior

Hoomail uses SQLite as its durable authority for inboxes, messages, raw MIME, attachments, read state, and reconciled calendar events. This document describes what is stored, what deletion changes, what search and inspection actually examine, and how iCalendar messages update mailbox calendar state.

For the endpoints that expose these records and actions, see [HTTP API](http-api.md). For how messages enter and leave the store, see [mail protocols](mail-protocols.md).

## SQLite lifecycle

At startup, Hoomail:

1. requires a non-empty database path;
2. creates the path's parent directory with mode `0755` for ordinary filesystem paths;
3. opens SQLite and limits the connection pool to one open connection;
4. enables `PRAGMA foreign_keys = ON` and requests `PRAGMA journal_mode = WAL`;
5. creates missing tables and indexes idempotently; and
6. upgrades a legacy `messages` table by adding `ical_json` and `raw` when either column is absent.

The configured path names one logical SQLite database. For a file-backed database that successfully enters WAL mode, SQLite may also create `-wal` and `-shm` sidecar files beside the database. Backups and volume handling must treat such a database and its active sidecars as one unit. In-memory databases and file-backed databases that do not enter WAL mode have no WAL sidecars to manage. See [deployment](deployment.md) for the configured path and persistent-volume guidance.

Parent-directory creation is skipped for `:memory:` and `file:` URI database paths. The application does not run a general migration framework: the current schema is created with `CREATE ... IF NOT EXISTS`, and the two legacy message columns above are the only explicit column migrations.

## Stored data model

### Mailboxes

A mailbox stores:

- its normalized address;
- creation time; and
- `last_message_at`, the time of the most recent delivery to that mailbox.

Mailbox total and unread counts are not cached counters. They are calculated from current message rows whenever mailboxes are listed. Mailboxes are ordered by `last_message_at`, falling back to creation time for a mailbox that has never received a message.

Deleting messages does **not** recompute or clear `last_message_at`. Consequently:

- an empty mailbox remains present until the mailbox itself is deleted or all data is reset; and
- its ordering continues to reflect its latest historical delivery, not the newest message that still exists.

### Per-recipient message copies

SMTP delivery targets the envelope recipients, not merely the visible `To` or `Cc` headers. Recipient addresses are trimmed, lowercased, deduplicated by the SMTP path, and blank addresses are skipped. A mailbox is created automatically when its normalized address is first used.

Hoomail stores one independent message row for every normalized envelope recipient. A multi-recipient delivery therefore creates separate copies—with separate message IDs, read states, attachment rows, and mailbox calendar reconciliation—in each recipient mailbox. This preserves BCC and other envelope-only recipients even when they do not appear in display headers.

Each stored message contains:

- mailbox ownership and delivery time;
- parsed sender address and name;
- parsed `To` and `Cc` address lists;
- subject;
- parsed HTML and plain-text bodies;
- parsed headers as JSON;
- the raw-message byte size;
- read/unread state;
- full raw MIME bytes; and
- parsed iCalendar events as JSON when recognized events were present.

New messages start unread. The raw MIME and parsed representations are both retained: the raw bytes support POP3 retrieval and structural inspection, while parsed fields support the UI, search, and calendar processing.

### Attachments and inline resources

Every MIME part classified as an attachment is stored as a BLOB with its filename, content type, normalized Content-ID when present, byte count, and content bytes. The bytes are copied for every recipient's independent message copy.

There are three related but intentionally different views of attachment data:

| View | Behavior |
| --- | --- |
| SQLite | Stores ordinary attachments, calendar files, and inline Content-ID resources. |
| Message-list attachment count | Counts every row with no Content-ID. Inline CID images do not increase the count, but a recognized calendar part without a Content-ID does. |
| Message detail attachment list | Always omits CID resources. It omits recognized calendar parts only when the message has non-null parsed iCalendar JSON. Unparseable calendar parts, and parts whose events were all incomplete, can therefore remain downloadable; when any event was parsed, every recognized calendar part in that message is hidden. All records remain stored. |

For HTML preview, Hoomail sanitizes the parsed HTML first, then rewrites a matching `cid:` reference to `/api/attachments/{id}`. The reference value must match the stored normalized Content-ID; angle brackets around the HTML reference are not tolerated. An unresolved CID remains unchanged. CID rewriting does not make an absent or mismatched resource downloadable.

## Search boundaries

Message search is scoped to one mailbox. After trimming surrounding whitespace, a non-empty query is matched against only:

- subject;
- sender address;
- sender display name; and
- parsed plain-text body.

Search does **not** examine HTML-only body content, recipients, `Cc`, arbitrary headers, raw MIME, attachment names or bytes, Content-IDs, or calendar fields. HTML may be used to build the displayed list snippet when plain text is absent, but that HTML-derived snippet is not part of the search query.

The characters `\`, `%`, and `_` are escaped before the SQLite `LIKE` query. They are treated literally rather than as an escape or wildcard supplied by the user. Results are ordered by delivery time, newest first.

## Read, delete, and reset semantics

### Read state

A single message can be marked read when opened. Bulk read/unread operations accept message IDs that may belong to multiple mailboxes and affect the existing rows found for those IDs. An empty ID list is a no-op. Because read state belongs to each recipient's independent message row, reading one mailbox's copy does not mark another recipient's copy read.

### Deletion consequences

Foreign-key cascades are enabled for the SQLite connection.

| Operation | Consequence |
| --- | --- |
| Delete one or more messages | Deletes those message rows and cascades deletion of their attachment BLOBs. The mailbox remains, and `last_message_at` is not recomputed. |
| Delete a mailbox | Cascades deletion of all messages, attachments, and reconciled calendar events owned by that mailbox. |
| Reset all data | Transactionally deletes attachments, calendar events, messages, and mailboxes, then clears their SQLite AUTOINCREMENT sequences. New IDs can therefore start again at `1`. |

Individual message deletion does not remove reconciled calendar state; see [Calendar source-message lifetime](#source-message-lifetime).

## HTML preview and inspection

Preview sanitization and inspection are related but separate operations. SQLite retains the parsed HTML; the HTTP message-detail response sanitizes a copy for display. The inspection response derives diagnostics from stored raw MIME, parsed headers, and parsed bodies.

### HTML preview sanitization

Before returning HTML for preview, Hoomail removes or neutralizes:

- paired `script` blocks and standalone or self-closing `script` tags;
- paired `iframe` blocks, but not standalone or self-closing `iframe` tags;
- `object`, `embed`, `applet`, and `form` elements;
- `base` and `meta` tags;
- inline `on...` event-handler attributes; and
- `javascript:`, `vbscript:`, and `data:text/html` URLs in `href`, `src`, `action`, or `formaction` attributes.

Dangerous URLs are replaced with `#`. **Security limitation:** this regex-based transformation does not cover standalone or self-closing `iframe` tags and is not a general-purpose browser security boundary or proof that arbitrary HTML is harmless in every context.

### Link and image extraction heuristics

When a non-empty HTML body exists, inspection extracts:

- quoted `href` values from `<a>` elements; and
- quoted `src` values from `<img>` elements.

Fragment-only links are skipped, repeated link/image URLs are deduplicated within their kind, and anchor text is stripped of tags, whitespace-normalized, and truncated. CID and `data:` images are omitted from the extracted external-image list. Bare URLs appearing only as HTML text are not extracted.

Plain-text `http://` and `https://` URLs are extracted only when HTML is absent or empty. Plain text is not used as an additional source when HTML is present.

An image is classified as a **likely tracking pixel** when its tag contains `display:none`, `visibility:hidden`, a numeric width of at most 1, or a numeric height of at most 1. This is a heuristic: it can miss tracking techniques and can classify legitimate tiny or hidden images as tracking pixels. Inspection does not fetch remote URLs.

### Inspection check catalog

The inspection endpoint reports `pass`, `warn`, or `info` results for the following checks:

| Check | What Hoomail evaluates |
| --- | --- |
| Message-ID | Whether a `Message-ID` header is present. |
| Authentication-Results | Textual `spf=...` and `dkim=...` tokens in `Authentication-Results`; both must equal `pass` for a passing result. |
| DKIM-Signature | Whether a `DKIM-Signature` header is present. |
| List-Unsubscribe | Whether a `List-Unsubscribe` header is present. |
| Image alt text | For HTML messages, counts `<img>` tags with no `alt` attribute. An empty `alt` attribute counts as present. |
| External images | For HTML messages, counts extracted non-CID, non-data images not classified as tracking pixels. |
| Tracking pixels | Counts images classified by the size/hidden-style heuristic above. |
| Message size | Warns when the stored raw-message size is greater than 102 KiB as an indicator of Gmail clipping risk. |
| Link security | Warns for extracted link-kind URLs beginning with plain `http://`. |

These are diagnostics, not cryptographic or deliverability verification. Hoomail does not validate DKIM signatures, perform SPF evaluation, determine whether unsubscribe behavior works, assess remote-host reputation, or prove that a provider will accept, render, or clip a message.

### MIME tree and its limits

When raw MIME bytes exist, inspection builds a structural tree containing:

- content type;
- charset parameter;
- transfer-encoding label;
- content disposition;
- filename (preferring disposition `filename`, then content-type `name`);
- body size measured in UTF-16 code units; and
- recursively discovered multipart children.

The tree is a lightweight diagnostic view. It unfolds header continuations and splits multipart bodies on the declared boundary, but it does not decode transfer-encoded bodies, decode RFC 2047/RFC 2231 parameter values, or provide a standards-complete MIME parse. Reported node size describes the body text as present in the raw part, not necessarily the decoded attachment or rendered body size. If legacy data has no raw MIME, no MIME tree is returned.

## Calendar recognition and parsing

### Recognized MIME parts

A MIME part is considered calendar content, case-insensitively, when any of these rules match:

- the content type contains `text/calendar`;
- the content type contains `application/ics`; or
- the filename ends in `.ics`.

This means an `.ics` file can be recognized even when sent as `application/octet-stream`. The part remains stored as an attachment BLOB even when its events are parsed. Duplicate parsed events within one message are suppressed by the combination of UID, sequence, and method.

### Parsed event fields

Hoomail documents reconciliation behavior for `PUBLISH`, `REQUEST`, `CANCEL`, and `REPLY`. If `METHOD` is missing, parsing defaults it to `PUBLISH`. Methods and status values are uppercased. A missing or invalid `SEQUENCE` becomes `0`.

A `VEVENT` must contain a non-empty `UID` and a valid `DTSTART`; an event missing either is skipped. Parsed optional data includes:

- `SUMMARY`, `DESCRIPTION`, `LOCATION`, and `STATUS`;
- organizer email address and `CN` name;
- attendee email address, `CN`, `PARTSTAT`, and `ROLE`;
- `DTEND` or `DURATION`; and
- all-day status.

Email addresses parsed from `ORGANIZER` and `ATTENDEE` are lowercased and may use either `mailto:` or a bare address. Repeated `ATTENDEE` properties are retained. For other repeated event properties, the first parsed occurrence is used.

Malformed calendar container structure, malformed content lines, an invalid date/time, or an invalid duration causes that calendar part to produce no events. A structurally valid part may still return its other events when individual `VEVENT` blocks merely omit UID or DTSTART; those incomplete blocks are skipped.

### Time zones and end times

Calendar timestamps are stored as Unix milliseconds.

| Input | Interpretation |
| --- | --- |
| Timestamp ending in `Z` | UTC. |
| Timed value with a loadable `TZID` | That named system time zone. |
| Timed value with an unknown `TZID` | Silently falls back to the server's local time zone. |
| Floating timed value with no `TZID` | Server local time zone. |
| `VALUE=DATE` or date-only value | All-day date in the server local time zone. |

Embedded `VTIMEZONE` definitions are not interpreted; named zones must be loadable by the Go runtime. Deployment time-zone configuration can therefore change the milliseconds assigned to floating, all-day, or unknown-zone values.

End-time precedence is:

1. use `DTEND` when present;
2. otherwise add `DURATION` when present;
3. otherwise default an all-day event to one day after its start; or
4. leave a timed event's end as `null` when neither `DTEND` nor `DURATION` exists.

The parser supports signed duration components for weeks, days, hours, minutes, and seconds. It does not infer a default duration for timed events.

## Mailbox-scoped calendar reconciliation

Calendar events are durable mailbox state, not a live projection of currently retained messages. SQLite enforces one reconciled record for each `(mailbox, UID)` pair. The same UID in two mailboxes is independent, and a multi-recipient delivery applies the event separately to every recipient mailbox. Calendar lists are ordered by start time.

Message storage, attachment storage, and calendar reconciliation occur in one database transaction. The source message and its parsed iCalendar JSON are stored even when a stale update does not change the reconciled calendar record.

### `PUBLISH` and `REQUEST`

`PUBLISH` and `REQUEST` use the same UID/sequence update rule:

- a previously unseen UID creates a record;
- an existing record is replaced when the incoming sequence is equal to or greater than the stored sequence; and
- a lower incoming sequence is stale and leaves the reconciled record unchanged.

An equal sequence is therefore allowed to overwrite event details. A missing status defaults to `CONFIRMED`; an explicit status is retained, including `CANCELLED`.

### `CANCEL`

A cancellation is applied even when its sequence is lower than the stored sequence.

- For an existing UID, Hoomail sets status to `CANCELLED`, retains the greater of the old and incoming sequences, updates the source-message reference/time, and preserves the existing summary, times, organizer, and attendees.
- For an unknown UID, Hoomail creates a `CANCELLED` record from the fields available in the cancellation.

Cancellation therefore preserves a durable cancelled calendar entry rather than deleting the event.

### `REPLY`

A reply never creates a missing event. For an existing UID:

- attendee addresses are matched case-insensitively;
- a matching attendee's `PARTSTAT` is updated;
- an unmatched attendee is appended; and
- reply sequence and all other event fields are ignored.

A reply with no attendees has no reconciliation effect. For a matched attendee, the existing name and role remain unchanged; only participation status is replaced.

### Source-message lifetime

A calendar record stores `last_message_id` as provenance, but that column is intentionally not a foreign key.

- Deleting an individual source message removes its attachment rows but leaves the reconciled calendar event intact. Its `last_message_id` may consequently refer to a message that no longer exists.
- A later accepted update, cancellation, or attendee reply can replace the source-message reference.
- Deleting the mailbox cascades deletion of its calendar events.
- Reset deletes all calendar events and resets their ID sequence along with the other stored entities.

Do not treat `last_message_id` as guaranteed dereferenceable history. It identifies the latest message that affected reconciliation when that message still exists.

## Explicit limitations

- SQLite access is intentionally serialized through one open connection; Hoomail is not a multi-writer database service.
- WAL sidecars are normal only for file-backed databases that successfully enter WAL mode; when present, they must not be discarded independently while the database is active.
- Search is mailbox-local and field-limited; it is not full-text search over raw mail or attachments.
- Each envelope recipient consumes independent storage for the message and all attachment bytes.
- Inline CID resources do not contribute to the message-list attachment count. Recognized calendar parts can contribute to that count. Message detail always omits CID resources, but hides recognized calendar parts only when parsed iCalendar JSON is non-null; unparseable or wholly incomplete calendar content can remain downloadable, while any parsed event causes all recognized calendar parts in that message to be hidden. All remain stored.
- Preview sanitization is regex-based and narrow. As a security limitation, standalone and self-closing `iframe` tags are not removed; inspection is heuristic and performs no remote or cryptographic verification.
- The MIME tree is structural, not a full decoder.
- Calendar support is a practical subset of iCalendar, not complete RFC 5545 scheduling. Unknown methods have no documented scheduling contract.
- Unknown `TZID` values silently become server-local time, and embedded `VTIMEZONE` data is not applied.
- Stale updates remain visible in their source messages even though they do not change reconciled calendar state.
- Individual message deletion does not roll back calendar reconciliation.

## Implementation sources

The contracts above are grounded in the current implementation:

- `internal/store/store.go` — database opening, pragmas, schema, legacy columns, mailbox ordering/counts, search, and attachment counts.
- `internal/store/operations.go` — recipient copies, stored fields/BLOBs, read/delete/reset behavior, and calendar reconciliation.
- `internal/smtpserver/parser.go` and `internal/smtpserver/smtpserver.go` — raw/parsed MIME ingestion, attachment classification, calendar-part parsing, and recipient normalization.
- `internal/inspect/inspect.go` — preview sanitization, CID rewriting, link/image heuristics, check catalog, and MIME-tree construction.
- `internal/calendar/calendar.go` — recognized calendar parts, parser fields, date/time/duration behavior, and method defaults.
- `internal/httpserver/httpserver.go` — sanitized message-detail projection, hidden CID/calendar attachment behavior, and inspection response assembly.
