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

For HTML preview, Hoomail resolves matching `cid:` references against resources from the selected `multipart/related` branch, then passes the rewritten document through the parsed HTML allowlist. Content-ID values are normalized, and percent-encoded `cid:` references are decoded before matching. Unresolved or out-of-branch references are removed rather than fetched remotely.

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

Preview sanitization and inspection are related but separate projections. SQLite retains selected decoded bodies and complete raw MIME without a schema migration. The message-detail endpoint rewrites scoped CID references and sanitizes a copy for display. SMTP ingestion and inspection share one semantic MIME parser and presentation selector: SMTP owns envelope handling and storage projection, while inspection additionally builds bounded raw header, line, body, boundary, and wire-order indexes.

### Standards and compatibility

Rich HTML email—including table layouts, branding, color, typography, and elaborate inline styling—is valid `text/html`. Whether a particular declaration renders in Outlook, Gmail, Apple Mail, or another client is a compatibility question, not MIME validity. Hoomail preserves safe sender formatting to inspect the captured message, but does not claim pixel-perfect parity with those clients.


### Inspection ownership and offline report

Raw bytes are authoritative when present. Inspection preserves duplicate header fields, casing, whitespace, physical line endings, MIME boundaries, and stable 1-based part paths from bounded raw indexes. Semantic nodes own normalized media metadata, transfer-decoded content, charset-converted text, and the selected text/HTML presentation. Raw and decoded sizes therefore have different meanings: raw size covers a complete indexed entity body excluding multipart boundaries; decoded size exists only after transfer decoding and, for text, charset conversion succeeds. Multipart decoded size is unknown.

Legacy rows require no database migration. When raw is absent, stored selected HTML/text bodies are fallback evidence; stored header JSON is decoded only to preserve corrupt-row error behavior and is not treated as raw header evidence. When raw exists, raw bytes are authoritative and stored bodies are used only when semantic presentation selection is unavailable. Fallback-derived findings identify that provenance and never imply reconstructed MIME selection or raw/authentication conformance.

The inspection endpoint returns report schema version `1` with:

- analysis state, parsed-prefix path, unavailable rule families, and truncation state;
- mutually exclusive summary counts;
- ordered applicability-aware findings with bounded evidence and primary references;
- first-seen resources covering links, images, suspected tracking pixels, CID/data sources, and MIME attachments; and
- a wire-order MIME tree with nullable raw/decoded sizes.

Findings cover message format, MIME, authentication and ARC evidence, unsubscribe syntax, content/accessibility, privacy, and compatibility. Authentication-Results assertions, DKIM/ARC fields, and DKIM unsubscribe-coverage claims are parsed as untrusted static evidence. A syntactically valid DKIM field does not mean the message is signed or verified: signature/body-hash validity, DNS keys, SPF, DMARC, ARC custody, alignment, reputation, SMTP transport, delivery, and unsubscribe endpoint behavior are unavailable from stored bytes and are never inferred.

HTML facts are collected once from the selected decoded HTML, before display sanitization. Link resources accept parseable HTTP, HTTPS, and mailto anchor destinations. Image resources distinguish HTTP(S), `cid:`, and `data:` sources; an external hidden or at-most-1-pixel candidate is reported as a tracking-pixel resource rather than duplicated as an image. This remains heuristic and can produce false positives or negatives. Plain-text HTTP(S) scanning runs only when authoritative HTML is unavailable. Resources retain exact bounded values, aggregate identical `(kind, path, URL)` occurrences, and are never fetched by inspection.

The Gmail clipping-risk heuristic uses only selected decoded HTML bytes above 102 KiB. It does not use total raw-message size, attachments, or encoded multipart overhead, and it is provider-behavior guidance rather than a conformance result.

Inspection is bounded and deterministic. Parser/analyzer caps can stop raw bytes, MIME depth/parts, headers, physical lines, legacy bodies, HTML tokens/nodes, resources, findings, evidence, or report bytes. Completed-prefix evidence remains available, `analysis.state` becomes `partial`, and `analysis.truncated` records causes. Raw absence or fatal semantic parsing can also produce useful partial reports. Unsupported encodings and charsets instead produce findings and unknown decoded sizes where applicable. Only internal parser/index correlation or other invariant failures become endpoint `500` responses.

### Preview security boundary

Security remains separate from analysis. Hoomail uses a Bluemonday policy built on Go's HTML parser/tokenizer rather than regex replacement as the display boundary. The policy preserves common email structures and ordinary formatting while allowing only a conservative set of inline presentation properties. Scripts, frames, forms, active embeds, event handlers, metadata/base changes, unsafe URL schemes, CSS URL/network functions, remote images, fonts, media, and other fetch initiators are removed. This follows the allowlist principle described by [OWASP's HTML sanitization guidance](https://cheatsheetseries.owasp.org/cheatsheets/Cross_Site_Scripting_Prevention_Cheat_Sheet.html#html-sanitization).

Matching CID image sources are rewritten before final sanitization. Safe absolute `http:`, `https:`, and `mailto:` links can remain as inert, externalized metadata with `target="_blank"` and `rel="noopener noreferrer"`; the empty-sandbox preview itself cannot navigate or open them. Use Inspect to review destinations before opening them separately. Neither inspection nor preview fetches remote image URLs.

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
- Preview rendering uses a parsed allowlist, blocks remote resources, and keeps safe sender formatting without promising delivery-client parity.
- Inspection is bounded static analysis. Partial reports preserve completed evidence; no network, cryptographic, authentication, endpoint, transport, delivery, reputation, or provider-rendering verification occurs.
- Calendar support is a practical subset of iCalendar, not complete RFC 5545 scheduling. Unknown methods have no documented scheduling contract.
- Unknown `TZID` values silently become server-local time, and embedded `VTIMEZONE` data is not applied.
- Stale updates remain visible in their source messages even though they do not change reconciled calendar state.
- Individual message deletion does not roll back calendar reconciliation.

## Implementation sources

The contracts above are grounded in the current implementation:

- `internal/store/store.go` — database opening, pragmas, schema, legacy columns, mailbox ordering/counts, search, and attachment counts.
- `internal/store/operations.go` — recipient copies, stored fields/BLOBs, read/delete/reset behavior, and calendar reconciliation.
- `internal/mimeparse` and `internal/smtpserver/parser.go` — shared MIME/raw parsing and presentation selection; SMTP storage projection and acceptance behavior.
- `internal/inspect/inspect.go` — preview sanitization/CID rewriting and the versioned bounded offline report.
- `internal/calendar/calendar.go` — recognized calendar parts, parser fields, date/time/duration behavior, and method defaults.
- `internal/httpserver/httpserver.go` — sanitized message-detail projection, hidden CID/calendar attachment behavior, and inspection response assembly.
