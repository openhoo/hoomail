---
name: hoomail-development
description: Develop, debug, and test Hoomail itself, including its Go mail protocols, MIME parsing, SQLite store, offline inspector, Preact UI, and Helm chart. Use in a Hoomail source checkout.
---

# Hoomail development

Run commands from the Hoomail source root. Read `CONTRIBUTING.md`; for a
workflow's detail, read `docs/development.md`. Toolchain pins currently are Bun
1.3.14 and Go 1.26.6. Source, lockfiles, and CI override this guide if they change.

## Find the implementation

| Change | Start here | Relevant browser coverage |
| --- | --- | --- |
| Startup and health | `cmd/hoomail/` | Delivery |
| SMTP ingestion | `internal/smtpserver/`, `internal/mimeparse/` | Delivery, viewer |
| POP3 | `internal/pop3server/` | Go protocol tests |
| SQLite, reset, read state | `internal/store/` | Messages, reset, mailboxes |
| iCalendar reconciliation | `internal/calendar/` | Calendar |
| HTTP and schemas | `internal/httpserver/` | Affected workflow |
| SSE lifecycle | `internal/events/`, `components/hoomail/use-hoomail.ts` | Realtime updates |
| Offline email inspection | `internal/inspect/`, `components/hoomail/inspect-panel.tsx` | Viewer |
| Preact UI | `main.tsx`, `components/hoomail/`, `components/ui/` | Viewer, mobile, messages |
| Container and chart | `Dockerfile`, `charts/hoomail/` | Image/Helm checks |

## Build before compiling the server

```bash
bun install --frozen-lockfile
bun x tsc --noEmit
bun run build
go run ./cmd/hoomail
```

`web/embed.go` embeds `web/dist`, which is generated and gitignored. A fresh
checkout needs the client build before full-server Go build, vet, or tests.
Rebuild the client and Go server after frontend changes. Do not commit
`web/dist`. `bun run dev`/`preview` serve Vite only; no API/SSE proxy is configured.

The frontend uses Preact, not React; Vite disables React aliases. Preserve
existing Preact primitives and shared UI patterns.

## Choose verification by change

For backend edits, run the affected packages first, then race tests for changes
crossing protocol/store/event boundaries:

```bash
go vet ./...
go test -race ./...
```

Format changed Go files with `gofmt`. For UI or end-to-end behavior:

```bash
bun x playwright install chromium
bun run test:e2e -- e2e/viewer.spec.ts
```

Replace the spec with the relevant workflow; `bun run test:e2e` runs the full
suite. The harness rebuilds frontend/server and uses `.e2e-runtime/hoomail.db`.
It deletes that directory on startup. Do not run two harnesses in one checkout,
even with different ports; use separate checkouts for independent runs.
Fixtures reset the isolated server and wait for SSE. Use observable UI/network
conditions rather than sleeps. See [verification.md](references/verification.md)
for fork, OpenAPI, chart, and performance checks.

## Contracts to preserve

- One SMTP ingestion path serves API samples too. Shared MIME parsing must
  preserve alternative/related selection, charset handling, raw source bytes,
  attachment projection, and calendar extraction.
- SMTP DATA/BDAT limits and stream resynchronization are patched in the local
  `third_party/go-smtp` module; root tests do not cover its standalone module.
- Inspection is on demand, deterministic, offline, and does not mark mail read.
  Distinguish complete, partial, and truncated results. No DNS/HTTP checks.
- Preserve the sender-faithful HTML allowlist, scoped CID rewriting, iframe
  sandbox/CSP, and download-only treatment of active/unknown attachment formats.
- SSE is a best-effort invalidation stream; reconnecting consumers refetch
  authoritative data. Slow subscribers must not block producers.
- SQLite is a single logical store with WAL sidecars. Reset/delete cascades and
  generation/concurrency safeguards must remain coherent. Keep one chart replica
  and `Recreate` to avoid overlapping writers.

For API changes update the generator, generated OpenAPI, tests, and
`docs/http-api.md` together. Follow applicable protocol/data/UI guides in `docs/`.
Do not use a developer's persistent `data/hoomail.db` for destructive experiments.
Report actual verification and unavailable gates; documentation-only edits need
skill/link checks, not the application suite.
