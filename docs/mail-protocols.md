# Mail protocols

Hoomail is a development mail catcher, not a production mail server. Its SMTP and POP3 listeners bind to all network interfaces by default and provide **no authentication and no TLS**. Keep ports `2525` and `3110` on a trusted development network or protect them with external network controls. Do not configure clients to require, opportunistically negotiate, or assume transport security.

The listener ports are configurable with `HOOMAIL_SMTP_PORT` and `HOOMAIL_POP3_PORT`; the defaults below are `2525` and `3110`.

## SMTP delivery

Configure an SMTP client with:

| Setting | Value |
| --- | --- |
| Host | Hoomail host, commonly `localhost` |
| Port | `2525` by default |
| Encryption | None |
| Authentication | None |

### Envelope recipients define inboxes

SMTP transport addresses and message headers have different roles:

- Every accepted `RCPT TO` address is an envelope recipient. Hoomail trims surrounding whitespace, lowercases it, and removes case-insensitive duplicates while preserving first-seen order.
- Hoomail creates or reuses one mailbox for every unique normalized envelope recipient and stores a separate message row in each mailbox.
- The mailbox does not have to exist first. Hoomail has no mailbox-domain allowlist or account lookup beyond the SMTP path syntax accepted by the server.
- BCC works because a hidden recipient is still sent with `RCPT TO`; a `Bcc` header is neither needed nor used for delivery.
- `To` and `Cc` headers are display metadata. They do not decide which inboxes receive a normal SMTP transaction.
- The MIME parser can fall back to the parsed `To` and `Cc` addresses only when it is invoked without envelope recipients. A normal SMTP `DATA` transaction cannot take that path because SMTP requires at least one successful `RCPT TO` first.

For example, these commands create two inbox copies, not three:

```text
MAIL FROM:<sender@example.test>
RCPT TO:<Visible@Example.Test>
RCPT TO:<bcc@example.test>
RCPT TO:<BCC@example.test>
```

The resulting inboxes are `visible@example.test` and `bcc@example.test`. The visible `To` header may describe different recipients without changing that delivery result.

### Sender and header precedence

Hoomail parses all `To`, `Cc`, and `From` header address lists for display and inspection:

- The first parsed `From` address and display name become the displayed sender.
- The SMTP `MAIL FROM` address is used as the displayed sender only when no usable `From` header address exists.
- An empty SMTP reverse path and no usable `From` header leave the displayed sender unset.
- Delivery remains controlled by `RCPT TO`, independently of `From`, `To`, and `Cc` headers.
- The complete SMTP `DATA` payload is retained as raw source. Parsed inspection headers use lowercase map keys and one value per field name; when a header is repeated, the last occurrence is retained in that parsed map. The raw source still contains every occurrence.

### Message-size limit

Hoomail advertises the ESMTP extension `SIZE 26214400`, an exact limit of $25 \times 1024 \times 1024$ bytes.

- A `MAIL FROM` command declaring a larger `SIZE` is rejected with SMTP `552` before `DATA`.
- If the client omits or understates `SIZE`, Hoomail still counts the received `DATA` and rejects a payload larger than the limit with `552`.
- An oversized message is not parsed or stored.
- The count is the SMTP message content size: headers, body, and line endings, excluding the terminating SMTP dot and transparency dot-stuffing, as defined by the `SIZE` extension.

MIME parsing or SQLite persistence failures are deliberately hidden from the client. The current server response is the generic permanent transaction failure:

```text
554 5.0.0 Error: transaction failed: message processing failed
```

No parser or database error details are disclosed over SMTP.

### CHUNKING and `BDAT`

Hoomail advertises `CHUNKING` in its `EHLO` response and accepts RFC 3030-style `BDAT <size> [LAST]` transfers after a successful `MAIL FROM` and at least one successful `RCPT TO`.

- Each command is followed immediately by exactly `<size>` raw octets. A non-final accepted chunk receives `250 2.0.0 Continue`; the server retains the open transaction and accepts the next `BDAT` chunk.
- The server concatenates accepted chunks in order and does not parse or persist the message until a chunk marked `LAST` completes. Successful final processing receives `250 2.0.0 OK: queued`; MIME parsing or persistence failure receives the same generic `554 5.0.0` processing failure documented above.
- The 25 MiB limit applies to the cumulative declared sizes of all chunks in the current transaction. If the next chunk would make the total exceed 26,214,400 bytes, Hoomail consumes and discards that chunk, replies `552 5.3.4 Max message size exceeded`, aborts the accumulated message, and resets the transaction.
- While a chunked transfer is open, `MAIL FROM`, `RCPT TO`, and `DATA` are rejected with `502 5.5.1`; the client must continue with `BDAT` or abort with `RSET` or by closing the connection.
- A valid `RSET` during chunking aborts the pending message and replies `250 2.0.0 Session reset`. Successful or failed `BDAT ... LAST`, an oversized chunk, `RSET`, and connection closure all discard chunk-transfer state; transaction reset also clears the sender and recipients, so a new message must start with `MAIL FROM` and `RCPT TO`.

## MIME interpretation

Hoomail retains the complete raw message while deriving one practical display model from the MIME tree. HTML email is standards-valid, including elaborate tables and inline styling; MIME validity does not imply that Outlook, Gmail, and other clients implement every HTML/CSS feature identically. Hoomail preserves safe sender formatting, but it is not a pixel-perfect emulator of any delivery provider or desktop client.

### Display-body selection

- `multipart/alternative` is evaluated recursively and the last supported alternative wins, following the sender-preference order in [RFC 2046 section 5.1.4](https://www.rfc-editor.org/rfc/rfc2046#section-5.1.4). When that selected alternative provides HTML but no plain text, Hoomail may retain the nearest earlier supported plain-text-only alternative as its text fallback. That fallback contributes only text: related resources and ordinary attachments remain scoped to the selected branch.
- `multipart/related` selects the part named by its `start` parameter, matching the part's normalized `Content-ID`; without `start`, the first part is the root. Hoomail recursively selects the display body from that root and associates inline resources only from the selected related branch. If `start` names no child, that related container supplies no display body.
- `multipart/mixed` and other multipart containers use the first supported body aggregate. Remaining leaves are stored as attachments rather than replacing the selected body.
- A named `text/plain` or `text/html` leaf, or a leaf explicitly marked `Content-Disposition: attachment`, is an attachment rather than a display body.
- HTML is retained for safe sender-faithful rendering and later sanitized by the HTTP display projection. The original MIME bytes remain unchanged for POP3 and inspection.

### Attachment classification and filenames

Attachment classification depends on both leaf metadata and its position in the selected MIME structure:

- a leaf with `Content-Disposition: attachment` or a filename/MIME `name` is an attachment;
- non-display media and related resources are attachments;
- after a mixed/unknown multipart selects its first supported body, leaves in the remaining branches are attachments, including otherwise displayable text; and
- branches not selected from `multipart/alternative` contribute no ordinary attachments or independently displayed bodies. The only exceptions are the nearest earlier plain-text fallback described above and recognized calendar leaves, which are retained for calendar/invite extraction; the complete raw MIME still contains every branch.

If no supported body exists, all leaves are retained as attachments. The resolved filename preference is:

1. a filename decoded by the MIME attachment-header parser;
2. the `Content-Disposition` `filename` parameter;
3. the `Content-Type` `name` parameter.

### Inline CID resources

For a resource with `Content-ID`, Hoomail trims whitespace and surrounding angle brackets before storing the identifier. Within the selected `multipart/related` branch, an HTML `cid:` URL is percent-decoded and matched to that identifier; a match is rewritten to Hoomail's attachment endpoint before the parsed HTML allowlist performs its final sanitization. Unresolved or out-of-branch CID references do not become network fallbacks.

CID-bearing parts are not listed as ordinary attachments. The association follows [RFC 2387](https://www.rfc-editor.org/rfc/rfc2387) root selection and [RFC 2392](https://www.rfc-editor.org/rfc/rfc2392) Content-ID URL semantics; malformed, duplicate, or cross-branch identifiers have no guaranteed resolution.

### Calendar boundaries

An attachment-classified part is considered calendar input when any of these is true:

- its media type contains `text/calendar`;
- its media type contains `application/ics`; or
- its filename ends in `.ics`, case-insensitively.

Parseable events are reconciled into the calendar model, including events from recognized calendar leaves retained from otherwise unselected alternatives. Within one message, duplicate parsed events with the same `UID`, `SEQUENCE`, and `METHOD` are collapsed. Calendar parts remain stored as attachments. When the message contains at least one parsed calendar event, every calendar-classified part is omitted from the ordinary attachment list and represented through the message/calendar views. If the message contains no parsed calendar events, its calendar-looking parts remain ordinary attachments.

### Malformed MIME and charset handling

- An absent part `Content-Type` retains the MIME default of `text/plain`; a malformed explicit content type is not silently promoted into a display body and is retained as an opaque attachment.
- Content-transfer encoding is decoded before text character decoding. Common registered character sets are converted to UTF-8 through go-message's charset support, so legacy mail such as ISO-8859-1 and Windows-1252 can be displayed alongside UTF-8 mail.
- Unknown-character-set errors are tolerated when go-message can still expose the entity; Hoomail does not promise successful UTF-8 conversion for an unregistered or mislabeled charset. An unknown or malformed explicit transfer encoding is retained as opaque attachment content rather than guessed text.
- Other malformed top-level headers, address fields, multipart structure, part reads, or MIME errors can reject the entire SMTP transaction with the generic processing failure shown above.
- Fallback or decoded display representations never rewrite the retained raw source.

## Built-in sample sender

`POST /api/send-test` sends a real SMTP transaction through Hoomail's own configured SMTP listener. It therefore exercises the same SMTP acceptance, MIME parsing, SQLite storage, mailbox creation, and event path as an external client.

```bash
curl --fail-with-body \
  --request POST \
  --header 'Content-Type: application/json' \
  --data '{"to":"developer@example.test","kind":"plain"}' \
  http://localhost:3000/api/send-test
```

### Request normalization

| Field | Behavior |
| --- | --- |
| `to` | String; trimmed and lowercased. Defaults to `test@hoomail.local`. It must match Hoomail's simple `local@domain.suffix` validation or the API returns HTTP `400` with `Invalid recipient address`. |
| `kind` | `plain`, `invite`, `update`, or `cancellation`. Missing, non-string, or unknown values silently select `plain`. |
| `subject` | String; surrounding whitespace is trimmed. Its meaning depends on the kind below. |

Malformed JSON, a non-object JSON value, or an empty body is currently treated like an empty request and therefore uses the defaults. Failure to connect to or complete the SMTP transaction returns HTTP `502` with `Could not reach the SMTP server. Is it running?`.

The sender treats both fields as untrusted. Recipient headers are serialized with `net/mail`; subject headers use RFC 2047 encoded words; plain-text and calendar bodies use MIME quoted-printable encoding; HTML interpolations are entity-escaped; and iCalendar values escape backslashes, line breaks, semicolons, and commas. Embedded CR/LF therefore cannot create additional message headers or MIME parts.

The SMTP transaction uses:

- `MAIL FROM:<owl@hoomail.local>`;
- one `RCPT TO` equal to the normalized `to` value; and
- `From: "The hoomail Owl" <owl@hoomail.local>` in the message headers.

The application normally connects to `127.0.0.1:<HOOMAIL_SMTP_PORT>`; the sender component's standalone fallback is `127.0.0.1:2525`. No shortcut writes directly to SQLite.

### Exact sample kinds

| Kind | Default/custom subject behavior | MIME payload | Calendar contract |
| --- | --- | --- | --- |
| `plain` | Default email subject: `hoomail delivery test`. A custom `subject` replaces it directly. | `multipart/mixed` containing a `multipart/alternative` plain-text and HTML delivery message, plus a base64 `text/plain` attachment named `hoot.txt`. Despite the kind name, this is not a plain-text-only email. | None. |
| `invite` | Default summary: `Owl standup meeting`; a custom `subject` becomes the summary. Email subject is `Invitation: <summary>`. | `multipart/alternative` with plain text and quoted-printable `text/calendar`. | `METHOD:REQUEST`, `SEQUENCE:0`, `STATUS:CONFIRMED`; starts tomorrow at 10:00 UTC and lasts 30 minutes. |
| `update` | Same summary rule. Email subject is `Updated invitation: <summary>`. | Same calendar message shape. | `METHOD:REQUEST`, `SEQUENCE:1`, `STATUS:CONFIRMED`; starts tomorrow at 11:00 UTC and lasts 30 minutes. |
| `cancellation` | Same summary rule. Email subject is `Cancelled: <summary>`. | Plain cancellation text plus quoted-printable `text/calendar`. | `METHOD:CANCEL`, `SEQUENCE:2`, `STATUS:CANCELLED`; carries a 10:00–10:30 UTC event window tomorrow. |

All three calendar kinds use a stable UID derived from the normalized recipient:

```text
hoomail-demo-<recipient-with-each-nonalphanumeric-replaced-by-hyphen>@hoomail.local
```

For example, `developer@example.test` becomes `hoomail-demo-developer-example-test@hoomail.local`. Sending `invite`, then `update`, then `cancellation` to the same recipient exercises calendar sequencing and reconciliation for the same logical event. Each request uses its current send time for `DTSTAMP` and calculates “tomorrow” from that request time in UTC.

## POP3 retrieval

POP3 listens on TCP port `3110` by default and binds to all network interfaces. It is a deliberately unauthenticated, plaintext development interface:

- there is **no TLS**, `STLS`, or implicit TLS;
- there is **no real password authentication**, SASL, or APOP; and
- `PASS` accepts any non-empty argument without checking it.

Restrict the listener to a trusted development network. A mail client configured to require encrypted POP3 or genuine credential validation is incompatible with this service.

### Session model

The greeting is:

```text
+OK Hoomail POP3 ready
```

A typical state sequence is:

```text
CAPA                 # optional; valid before or after login
USER developer@example.test
PASS any-non-empty-value
STAT                 # transaction commands
LIST
RETR 1
QUIT                 # commits messages marked with DELE
```

`USER` is valid only before login and requires exactly one non-whitespace token. Repeating `USER` before `PASS` replaces the selected address. Mailbox lookup and creation do not happen until `PASS`.

On `PASS`, Hoomail:

1. trims and lowercases the selected inbox address;
2. creates and persists an empty mailbox if it does not exist;
3. loads messages ordered oldest first by receipt time, then database ID;
4. clones their raw bytes into a fixed session snapshot; and
5. assigns one-based POP3 message numbers from that snapshot.

The login response says `mailbox locked and ready`, but Hoomail does **not** take an exclusive per-mailbox POP3 lock. Concurrent sessions have independent snapshots. Mail delivered after `PASS` is not visible until the client reconnects, and existing message numbers do not change within the session.

`CAPA` and `QUIT` work before or after login. `USER` and `PASS` are authorization-state commands. Every other implemented command requires a successful `PASS`.

### Commands and response shapes

Commands are case-insensitive. Brackets below indicate an optional argument; they are not sent literally.

| Command | Valid state | Success behavior and response shape |
| --- | --- | --- |
| `CAPA` | Before or after login | `+OK Capability list follows`, followed by exactly `USER`, `UIDL`, and `TOP`, then `.`. CAPA is not a complete list of every working base command. |
| `USER <inbox-address>` | Before login | Selects/replaces the mailbox token; `+OK user accepted`. The token cannot contain spaces or tabs. |
| `PASS <value>` | Before login, after `USER` | Accepts any non-empty argument without verification, opens/creates the normalized mailbox, and snapshots it; `+OK mailbox locked and ready, <count> messages`. |
| `STAT` | After login | `+OK <count> <octets>` for all snapshot messages not marked deleted. |
| `LIST` | After login | `+OK <count> messages (<octets> octets)`, then one `<number> <octets>` line per undeleted message, then `.`. |
| `LIST <number>` | After login | `+OK <number> <octets>` for one undeleted snapshot message. |
| `UIDL` | After login | `+OK unique-id listing follows`, then one `<number> <uid>` line per undeleted message, then `.`. |
| `UIDL <number>` | After login | `+OK <number> <uid>` for one undeleted message. The UID is Hoomail's persisted numeric SQLite message ID, not the email `Message-ID` header. |
| `RETR <number>` | After login | `+OK <stored-raw-octets> octets`, followed by the full raw message as a POP3 multiline response. |
| `TOP <number> <lines>` | After login | `+OK top of message follows`, followed by all headers and up to the requested non-negative number of body lines. |
| `DELE <number>` | After login | Marks an undeleted snapshot message for this session; `+OK message deleted`. |
| `RSET` | After login | Clears all session deletion marks; `+OK <count> messages (<octets> octets)`. |
| `NOOP` | After login | No state change; `+OK`. |
| `QUIT` | Before or after login | Before login, closes with `+OK goodbye`. After login, commits marked deletions and closes with `+OK goodbye`; commit failure is described below. |

`TOP` recognizes either CRLF-CRLF or LF-LF as the header/body boundary. If the stored raw message has no recognized separator, `TOP` returns the complete raw message. A body-line count of `0` returns the headers and separator only.

Messages marked by `DELE` immediately disappear from the session's `STAT`, `LIST`, `UIDL`, `RETR`, `TOP`, and further `DELE` operations. Their original message numbers remain reserved rather than being renumbered.

### Multiline wire format and octet counts

Multiline `CAPA`, `LIST`, `UIDL`, `RETR`, and `TOP` output:

- uses CRLF line endings on the wire;
- doubles a leading `.` on each content line (POP3 dot-stuffing);
- ensures content ends with CRLF; and
- terminates with a line containing only `.`.

The octet values in `STAT`, `LIST`, and the initial `RETR` response are the byte lengths of the raw messages stored in SQLite. If stored input used LF-only or CR-only line endings, or contains lines requiring dot-stuffing, the number of bytes actually transmitted by `RETR` or `TOP` can differ after wire normalization and stuffing.

### Deletion commit semantics

`DELE` changes only the in-memory session marks. It does not immediately delete from SQLite.

- `RSET` clears every mark.
- A TCP disconnect, client crash, server shutdown, or any other close without a successful transaction-state `QUIT` abandons the marks and retains the messages.
- A successful `QUIT` deletes the marked persisted message IDs and then replies `+OK goodbye`.
- If persistence fails, Hoomail replies `-ERR unable to delete messages` and closes the connection instead of falsely acknowledging the deletion.

### Stable POP3 error classes

Clients should branch on the following current `-ERR` classes rather than expect additional diagnostic detail:

| Response | Meaning |
| --- | --- |
| `-ERR malformed command` | Empty/malformed syntax, missing or extra arguments, invalid command framing, a NUL byte, or a command line beyond the 4096-byte reader limit. Framing/NUL/over-limit errors close the connection after the response; ordinary syntax errors do not. |
| `-ERR command not valid in this state` | A transaction command was sent before `PASS`, or `USER`/`PASS` was sent after login. |
| `-ERR unknown command` | An unknown keyword was sent after login. Before login, an unknown keyword receives the state error above. |
| `-ERR no such message` | The message number is missing, malformed, out of range, or already marked deleted for `LIST n`, `UIDL n`, `RETR`, or `DELE`. |
| `-ERR no such message or invalid line count` | `TOP` has an invalid/unavailable message number or a negative/non-numeric line count. A wrong number of `TOP` arguments instead produces `malformed command`. |
| `-ERR unable to open mailbox` | SQLite could not open/create/snapshot the mailbox during `PASS`; the session remains connected in authorization state. |
| `-ERR unable to delete messages` | `QUIT` could not commit marked deletions; the connection closes. |
| `-ERR server unavailable` | The POP3 service has no usable store; this is sent instead of the normal greeting and the connection closes. |

EOF closes silently. Hoomail does not advertise or emit RFC 2449 extended response codes such as `[IN-USE]`.

## Standards, compatibility, and security

These are separate boundaries:

- **Standards:** MIME defines valid body types, alternative preference, related roots, transfer encodings, character sets, and Content-ID references. Rich `text/html` is not invalid merely because it resembles a web page.
- **Compatibility:** mail clients support different HTML and CSS subsets. A standards-valid message can render differently in Gmail, Outlook, Apple Mail, and Hoomail.
- **Security:** Hoomail applies a parsed allowlist when presenting HTML, blocks active markup and remote resources by default, and limits inline attachment types. These restrictions protect the testing UI; they are not claims that the sender's MIME was invalid.

## Standards and implementation references

Hoomail intentionally implements a development-oriented behavior profile. Protocol and media terminology comes from these primary specifications:

- [RFC 5321 — Simple Mail Transfer Protocol](https://www.rfc-editor.org/rfc/rfc5321)
- [RFC 1870 — SMTP Service Extension for Message Size Declaration](https://www.rfc-editor.org/rfc/rfc1870)
- [RFC 3030 — SMTP Service Extensions for Transmission of Large and Binary MIME Messages](https://www.rfc-editor.org/rfc/rfc3030)
- [RFC 2045 — MIME Part One: Message Body Format](https://www.rfc-editor.org/rfc/rfc2045)
- [RFC 2046 — MIME Part Two: Media Types](https://www.rfc-editor.org/rfc/rfc2046)
- [RFC 2047 — MIME Header Extensions for Non-ASCII Text](https://www.rfc-editor.org/rfc/rfc2047)
- [RFC 2183 — Content-Disposition](https://www.rfc-editor.org/rfc/rfc2183)
- [RFC 2392 — `cid:` and `mid:` URLs](https://www.rfc-editor.org/rfc/rfc2392)
- [RFC 2387 — `multipart/related`](https://www.rfc-editor.org/rfc/rfc2387)
- [RFC 2557 — MIME encapsulation of aggregate HTML documents](https://www.rfc-editor.org/rfc/rfc2557)
- [RFC 2854 — `text/html`](https://www.rfc-editor.org/rfc/rfc2854)
- [RFC 1939 — Post Office Protocol Version 3](https://www.rfc-editor.org/rfc/rfc1939)
- [RFC 2449 — POP3 Extension Mechanism and CAPA](https://www.rfc-editor.org/rfc/rfc2449)

The exact Hoomail behavior documented here is defined by the current implementation:

- [`internal/smtpserver/smtpserver.go`](../internal/smtpserver/smtpserver.go)
- [`internal/smtpserver/parser.go`](../internal/smtpserver/parser.go)
- [`internal/sendtest/sendtest.go`](../internal/sendtest/sendtest.go)
- [`internal/pop3server/pop3server.go`](../internal/pop3server/pop3server.go)
- [`internal/store/operations.go`](../internal/store/operations.go)
- [`internal/calendar/calendar.go`](../internal/calendar/calendar.go)
- [`internal/httpserver/httpserver.go`](../internal/httpserver/httpserver.go)
