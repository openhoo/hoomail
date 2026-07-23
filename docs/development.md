# Development and verification

This guide covers the repository-supported contributor workflow. It separates the integrated application, frontend-only Vite servers, isolated browser tests, package tests, benchmarks, and CI gates so that local development does not accidentally use or reset data you intend to keep.

## Toolchains

Use the versions pinned by CI and the container build:

- Bun `1.3.14`
- Go `1.26.5`
- Playwright-managed Chromium for browser tests and the frontend benchmark

`package.json` does not declare `packageManager` or `engines`, so it does **not** enforce the Bun version. Verify the executable in your environment when reproducibility matters:

```bash
bun --version
go version
```

Install JavaScript dependencies from the lockfile:

```bash
bun install --frozen-lockfile
```

Install Playwright's managed Chromium once before running browser tests locally:

```bash
bun x playwright install chromium
```

CI uses `bun x playwright install --with-deps chromium` on its Linux runner to install the browser and operating-system dependencies.

## Build and run the integrated application

The Go server embeds the compiled Vite client. `bun run build` writes that client to `web/dist`, which is generated and gitignored. The `//go:embed dist` declaration in `web/embed.go` makes this directory a compile-time prerequisite whenever a Go command compiles the embedded web package, including the full server and `go test ./...` from a fresh checkout.

Build and run the complete application with:

```bash
bun install --frozen-lockfile
bun run build
go run ./cmd/hoomail
```

Rebuild the client after frontend changes before rebuilding or rerunning the Go server.

The checkout run uses these defaults:

| Service or state | Default |
| --- | --- |
| Web interface and HTTP API | `http://localhost:3000` |
| SMTP | `2525` |
| POP3 | `3110` |
| SQLite database | `data/hoomail.db` relative to the checkout |

That database is persistent local application data. Use a disposable override for experiments that may delete or mutate everything:

```bash
runtime_dir="$(mktemp -d)"
trap 'rm -rf "$runtime_dir"' EXIT
HOOMAIL_DB_PATH="$runtime_dir/hoomail.db" go run ./cmd/hoomail
```

The integrated server can be checked while it is running with:

```bash
go run ./cmd/hoomail healthcheck
```

The built-in health check requests the mailbox API, connects to SMTP, and requires a POP3 `+OK` greeting.

## Frontend scripts and their limits

The exact scripts currently defined in `package.json` are:

| Command | Underlying command | Purpose |
| --- | --- | --- |
| `bun run dev` | `vite` | Start the Vite development server for frontend-only iteration |
| `bun run build` | `vite build` | Generate the production client in `web/dist` |
| `bun run preview` | `vite preview` | Serve the generated static Vite output for frontend-only preview |
| `bun run test:e2e` | `playwright test` | Run all Playwright E2E specifications |
| `bun run test:e2e:ui` | `playwright test --ui` | Open Playwright UI mode using the same isolated server orchestration |
| `bun run bench:frontend` | `playwright test e2e/performance.spec.ts --reporter=line` | Run the isolated Chromium frontend benchmark |

`bun run dev` and `bun run preview` are **not** integrated Hoomail servers. The Vite configuration has no proxy for same-origin `/api/*` requests or the `/api/events` SSE stream. A Vite page therefore cannot forward those calls to a separately running Go server by itself. Use the embedded build/run workflow for a complete application, or provide your own same-origin reverse proxy when specifically working through Vite.

## Type checking

Run the repository's TypeScript check directly:

```bash
bun x tsc --noEmit
```

There is no `bun run typecheck` script. The strict, no-emit TypeScript configuration checks the Preact entry point and components, `lib/utils.ts`, Vite and Playwright configuration, and all `e2e/**/*.ts` files. It does not compile or test Go code, render the application, or emit a frontend bundle.

## Isolated Chromium E2E tests

Run the complete browser suite with:

```bash
bun run test:e2e
```

The Playwright harness is self-contained:

1. It deletes and recreates `.e2e-runtime` at startup.
2. It runs `bun run build` and compiles a fresh Go binary.
3. It starts the real server with SQLite at `.e2e-runtime/hoomail.db`.
4. It uses isolated HTTP, SMTP, and POP3 listener ports.
5. It stops that server when Playwright finishes; it never reuses an already-running server.

A separately running Hoomail instance is neither required nor used. The harness database is disposable and is removed at the next harness startup, so normal `data/hoomail.db` checkout data is not reset. Invoke E2E through the package scripts rather than calling `e2e/run-server.ts` directly; package-script and Playwright runs provide the supported port defaults and lifecycle. Direct helper invocation without port variables currently falls back to POP3 `33110`, rather than the package-script default of SMTP plus one (`33126` with the default SMTP port).

Specifications using the shared page fixture reset the isolated application through `POST /api/reset` before each test, navigate to the app, wait for a live `200` SSE response, and verify the empty UI before the test body runs. The performance specification performs its own explicit reset. Tests should wait for observable UI or network results rather than fixed sleeps.

### Execution policy, ports, and artifacts

Playwright runs one Chromium project, one worker, and no fully parallel tests. The browser context uses locale `en-US` and time zone `UTC`. Local runs do not retry; CI retries failures twice and rejects committed `test.only` calls.

Default harness ports are:

| Listener | Default | Override |
| --- | ---: | --- |
| HTTP | `33100` | `HOOMAIL_E2E_HTTP_PORT` |
| SMTP | `33125` | `HOOMAIL_E2E_SMTP_PORT` |
| POP3 | `33126` | `HOOMAIL_E2E_POP3_PORT` |

For package-script and Playwright runs, POP3 defaults to the selected SMTP port plus one unless explicitly overridden. Direct `e2e/run-server.ts` invocation without variables instead currently falls back to `33110`, so use the package scripts. For example:

```bash
HOOMAIL_E2E_HTTP_PORT=33200 \
HOOMAIL_E2E_SMTP_PORT=33225 \
bun run test:e2e -- e2e/delivery.spec.ts
```

That example uses POP3 `33226`. Set `HOOMAIL_E2E_POP3_PORT` as well if that port is unavailable.

The normal HTML report is written to `playwright-report/`; per-test output is written under `test-results/`. Configuration retains screenshots and videos on failure and records a trace on the first retry. Because local runs have no retries, retry traces are normally a CI diagnostic. CI uploads both directories for seven days when the E2E job fails.

### Focused browser suites

Pass a specification path after `--` to run one workflow:

```bash
bun run test:e2e -- e2e/delivery.spec.ts
bun run test:e2e -- e2e/messages.spec.ts
bun run test:e2e -- e2e/viewer.spec.ts
bun run test:e2e -- e2e/calendar.spec.ts
bun run test:e2e -- e2e/mailboxes.spec.ts
bun run test:e2e -- e2e/reset.spec.ts
```

| Specification | Observable contracts it exercises |
| --- | --- |
| `e2e/delivery.spec.ts` | Send-test dialog validation and focus behavior; real SMTP delivery; SSE-driven inbox/message appearance without reload; HTML, attachment, and automatic read-state behavior; keyboard selection of sample-message kinds |
| `e2e/messages.spec.ts` | Search by subject, sender, and body within an inbox; row navigation; selection and ranges; bulk read/unread/delete; keyboard context menus; focus retention |
| `e2e/viewer.spec.ts` | HTML/plain/source/inspect tabs; sequential Tab and arrow/Home/End behavior; inspection regions; attachment preview/download; stale-content prevention while switching messages |
| `e2e/calendar.spec.ts` | Invitation/update/cancellation reconciliation in the UI; calendar grid semantics; cross-day and cross-month keyboard movement; opening an event's source message |
| `e2e/mailboxes.spec.ts` | Keyboard inbox deletion through context menus and deterministic focus transfer, including the final empty state |
| `e2e/reset.spec.ts` | Reset dialog containment, cancellation, Escape behavior, complete isolated-state wipe, cache revalidation, and the resulting API/UI empty state |

Use `bun run test:e2e:ui` when interactive Playwright inspection is useful. It retains the same automatic build, isolated database, ports, and real-server setup.

## Frontend benchmark

Run the deterministic large-inbox Chromium benchmark with:

```bash
bun run bench:frontend
```

It resets the same isolated E2E database, seeds `200` messages by default in concurrent batches of 20, opens the mailbox, and samples up to 100 `ArrowDown` navigation operations. Configure a larger workload with `HOOMAIL_BENCH_MESSAGES`:

```bash
HOOMAIL_BENCH_MESSAGES=1000 bun run bench:frontend
```

The benchmark prints JSON and attaches `frontend-performance.json` to the Playwright result. It records:

- rendered message-row count;
- total DOM node count;
- mean, p95, and maximum synchronous key-handler time;
- mean, p95, and maximum time to the next animation frame;
- document-level `querySelector` and `querySelectorAll` calls per key operation.

This is a measurement harness, not a performance gate: it defines no pass/fail thresholds. Compare like-for-like workloads and environments when evaluating changes.

## Focused Go tests

Build `web/dist` first before running the full package graph from a fresh checkout:

```bash
bun run build
go test -race ./...
```

For quicker investigation, use package-focused tests:

```bash
go test ./internal/pop3server
go test ./internal/smtpserver
go test ./internal/calendar ./internal/store
go test ./internal/httpserver ./internal/inspect
go test ./internal/sendtest
go test ./internal/events
```

| Packages | Primary contracts proved |
| --- | --- |
| `internal/pop3server` | Real socket protocol flow, authentication state, listing/retrieval, dot-stuffing, and delete-on-successful-`QUIT` semantics |
| `internal/smtpserver` | SMTP envelope and BCC recipients, normalization/deduplication, raw MIME preservation, MIME parsing, and advertised/enforced 25 MiB size behavior |
| `internal/calendar`, `internal/store` | iCalendar parsing and sequence/reply/cancellation reconciliation; SQLite migrations, search, transactions/events, cascades, reset, and message persistence semantics within the test process |
| `internal/httpserver`, `internal/inspect` | HTTP response/error shapes, SSE handshake, attachment delivery, SPA fallback, sanitized/CID-rewritten message details, MIME/link/header inspection |
| `internal/sendtest` | Built-in sample-message generation contracts |
| `internal/events` | Event-hub subscription and broadcast behavior |

Package tests are the primary coverage for protocol and parsing edge cases that would be unnecessarily indirect or slow to express through the browser.

## Go benchmarks

Run the full benchmark inventory without running unit tests:

```bash
go test ./internal/calendar ./internal/smtpserver ./internal/inspect ./internal/store ./internal/events \
  -run '^$' -bench . -benchmem -benchtime=100ms
```

`-run '^$'` intentionally selects no unit tests. The benchmark set covers:

- realistic iCalendar `REQUEST` parsing and a generated 100-event `PUBLISH` calendar;
- small plain and realistic multipart SMTP/MIME parsing, including envelope recipients;
- HTML sanitization, CID rewriting, MIME-tree construction, and link/header inspection at multiple input sizes;
- listing and searching a 1,000-message SQLite inbox and storing a realistic attachment-bearing message;
- synchronous event broadcast fan-out to 1, 16, and 64 subscribers.

For a more stable targeted comparison, run repeated one-second samples:

```bash
go test ./internal/store -run '^$' \
  -bench 'BenchmarkListMessages1000' -benchmem -benchtime=1s -count=5
```

Compare allocations first (`allocs/op`), then allocated bytes (`B/op`), then timing across repeated samples. Benchmarks are not CI thresholds and should be compared on equivalent hardware, toolchains, and workloads.

## What CI checks

The CI workflow runs these independent or dependent gates:

| Gate | What it establishes |
| --- | --- |
| Conventional Commits | Hooversion validates commit messages for release-compatible history; automated release commits are exempted from this job |
| Preact client | Lockfile installation, strict TypeScript no-emit checking, and a production Vite build |
| Playwright E2E | The isolated real-server Chromium workflows described above |
| Go race tests | Embedded-client build followed by `go test -race ./...` |
| Go quality | `gofmt` cleanliness for `cmd`, `internal`, and `web`; `go vet ./...`; module checksum verification |
| Go security | Reachable vulnerability scanning with `govulncheck v1.6.0` and static security-pattern scanning with `gosec v2.28.0` (reviewed exclusions: `G104`, `G202`, `G301`, `G705`) |
| Helm chart | Strict linting, representative template renders, and chart/application version synchronization |
| Docker build and smoke | Build the production image, verify its embedded version, start it as configured by the image, and pass the built-in HTTP/SMTP/POP3 health check |

CI does not run either benchmark as a gate and does not enforce a line or branch coverage percentage.

## Coverage boundaries

No single command proves the entire product:

- Playwright E2E proves the principal UI, accessibility, focus, realtime SSE, built-in SMTP delivery, viewer, calendar, and destructive-reset workflows against a real isolated server.
- Go package tests provide deeper SMTP and POP3 protocol behavior, MIME and iCalendar parsing, HTTP schemas, inspection, event ordering, and SQLite semantics.
- Frontend and Go benchmarks report performance characteristics but contain no regression thresholds.
- The Docker smoke proves image startup and the built-in HTTP/SMTP/POP3 health check, not full protocol behavior or durable restart persistence.
- The listed unit and E2E suites do not demonstrate preservation of SQLite data across a process or container replacement. They also do not prove multi-platform publication, registry availability, release attestations/SBOMs, or a live Kubernetes deployment.
- CI has no coverage-percentage gate; successful checks establish the named contracts, not exhaustive path coverage.

See the repository [README](../README.md) for user-facing configuration, protocol entry points, deployment options, and the architecture overview.
