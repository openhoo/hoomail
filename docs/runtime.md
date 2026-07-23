# Runtime and CLI reference

Hoomail is one executable. With no arguments, it opens the SQLite store, binds the SMTP, POP3, and HTTP listeners, and serves all three protocols in one process.

```console
$ hoomail
```

The container image uses the same entrypoint, so its normal invocation is `/hoomail` with no arguments.

This reference describes the behavior implemented by [`cmd/hoomail/main.go`](../cmd/hoomail/main.go), [`internal/version/version.go`](../internal/version/version.go), [`web/embed.go`](../web/embed.go), and the static handler in [`internal/httpserver/httpserver.go`](../internal/httpserver/httpserver.go).

## Command dispatch

Hoomail does not use a flag parser. A subcommand is recognized only when it is the **one and only** argument after the executable name:

| Invocation shape | Behavior |
| --- | --- |
| `hoomail version` | Print the resolved version followed by a newline, then exit successfully. |
| `hoomail healthcheck` | Run the built-in HTTP, SMTP, and POP3 probes. |
| `hoomail` | Start the application. |
| Any other shape, including `hoomail --help`, `hoomail unknown`, `hoomail version extra`, or `hoomail healthcheck extra` | Start the application; the arguments are otherwise ignored. |

The subcommand names are exact and case-sensitive.

### `version`

```console
$ hoomail version
0.4.0
```

`internal/version.Value` starts as `"dev"`. During package initialization, an unchanged `"dev"` value is replaced with the trimmed contents of the compile-time embedded [`internal/version/version`](../internal/version/version) file, currently `0.4.0`. Release builds can replace `internal/version.Value` through Go linker flags; when replaced with a value other than `"dev"`, that linker-provided value is printed instead.

The command prints only the resolved value and a newline. It does not open the database or bind any listener.

### `healthcheck`

```console
$ hoomail healthcheck
```

Success is silent and exits with status `0`. Any failed probe is returned to `log.Fatal`, which writes the error to standard error and exits with status `1`. Probes run sequentially in this order:

1. **HTTP:** create an HTTP client with a two-second timeout and request `GET http://<host>:<PORT>/api/mailboxes`. The response body is fully discarded and closed. Success requires an HTTP `200 OK`; connection, request, body-read, body-close, and non-200 results are failures.
2. **SMTP:** open a TCP connection to `<host>:<HOOMAIL_SMTP_PORT>` with a two-second dial timeout, then close it. The probe does not read an SMTP greeting or issue SMTP commands.
3. **POP3:** open a TCP connection to `<host>:<HOOMAIL_POP3_PORT>` with a two-second dial timeout. After connecting, set a new two-second read deadline, read one newline-terminated greeting, and require it to begin with `+OK`.

Each network step has its own timeout; there is no single deadline covering the complete healthcheck. A failure stops the sequence immediately. The healthcheck only observes process reachability and the behavior above: it does not send mail, authenticate through POP3, inspect SQLite directly, or validate the frontend.

## Environment and defaults

An environment variable is used only when its value is non-empty. An unset variable and a variable explicitly set to the empty string both use the fallback shown below. Non-empty values are accepted verbatim.

| Variable | Executable fallback | Purpose |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listener port; also the HTTP healthcheck target port. |
| `HOOMAIL_SMTP_PORT` | `2525` | SMTP listener port, local send-test target port, and SMTP healthcheck target port. |
| `HOOMAIL_POP3_PORT` | `3110` | POP3 listener port and POP3 healthcheck target port. |
| `HOOMAIL_DB_PATH` | `data/hoomail.db` | SQLite path used during normal application startup. The relative fallback is resolved from the process working directory. |
| `HOOMAIL_HEALTHCHECK_HOST` | `127.0.0.1` | Host used only by the `healthcheck` subcommand. It does not control listener binding. |

The production image sets `HOOMAIL_DB_PATH=/app/data/hoomail.db`, overriding the executable's checkout/default fallback. Therefore:

- a direct checkout or locally built binary without `HOOMAIL_DB_PATH` uses `./data/hoomail.db` relative to its current working directory;
- the supplied container image uses `/app/data/hoomail.db` and declares `/app/data` as its data volume.

The image also explicitly sets the three listener-port variables to the same values as the executable fallbacks.

## Listener binding and startup

Normal startup proceeds in a fixed order:

1. Resolve the environment values.
2. Open and initialize the SQLite store.
3. Construct the SMTP, POP3, and HTTP services.
4. Bind the SMTP listener.
5. Bind the POP3 listener.
6. Bind the HTTP listener.
7. Start serving all three listeners concurrently.

All listeners use the address form `:<port>`. They therefore bind the wildcard address on all interfaces supported by the host's Go networking configuration; `HOOMAIL_HEALTHCHECK_HOST` does not restrict them to loopback.

The HTTP server has these explicit timeout settings:

| Setting | Value |
| --- | --- |
| `ReadHeaderTimeout` | 10 seconds |
| `IdleTimeout` | 60 seconds |
| `ReadTimeout` | Not explicitly set |
| `WriteTimeout` | Not explicitly set |

Startup is all-or-nothing:

- A database open or initialization error aborts before any listener is bound.
- If SMTP binding fails, startup aborts.
- If POP3 binding fails, the already-open SMTP listener is closed before startup aborts.
- If HTTP binding fails, the already-open SMTP and POP3 listeners are closed before startup aborts.

An error returned from startup reaches `log.Fatal`, so the process writes the error and exits with status `1`. Once serving has started, the process waits for either a shutdown signal or the first service to exit. An unexpected HTTP, SMTP, or POP3 serve error initiates coordinated shutdown and is then fatal. A normal/expected server-close result initiates coordinated shutdown without turning that close result into a fatal error.

The built-in send-test API targets `127.0.0.1:<HOOMAIL_SMTP_PORT>`, so generated test messages pass through the SMTP service in the same process rather than writing directly to the store.

## Coordinated shutdown

The process listens for `SIGINT` and `SIGTERM`. Receipt of either signal starts graceful shutdown. The first service exit also causes the process to shut down the remaining services.

Shutdown creates one shared context with a ten-second deadline, then calls the services **sequentially** in this order:

1. HTTP
2. SMTP
3. POP3

Because all three calls share the same context, time spent shutting down an earlier service reduces the time available to later services. All three shutdown calls are attempted even if an earlier one returns an error. Afterward, errors are reported with HTTP taking precedence, followed by an unexpected SMTP error, then an unexpected POP3 error. Recognized server-closed results are not treated as shutdown failures.

The SQLite store remains open while the services shut down and is closed when the runtime function returns. An error from closing the store is not surfaced.

## Embedded frontend and SPA routing

The frontend is a compile-time dependency of the Go binary:

- [`web/embed.go`](../web/embed.go) uses `//go:embed dist`.
- The exported filesystem is rooted at the embedded `dist` directory, so runtime paths such as `index.html` are relative to `web/dist` rather than prefixed with `dist/`.
- The frontend must exist in `web/dist` when the Go executable is compiled.
- Changing files in `web/dist` after compilation does not change an already-built executable; the binary must be rebuilt.

Requests not handled by an API route pass to the static single-page application handler. Its exact behavior is:

| Request | Result |
| --- | --- |
| `GET /` or `HEAD /` | Serve embedded `index.html`. |
| `GET` or `HEAD` for an existing embedded file | Serve that file. |
| `GET` or `HEAD` for a missing non-API path | Serve `index.html` as the SPA fallback. This includes paths that look like missing static assets. |
| Any unmatched path beginning with `/api/` | Return `404`; API misses never fall back to the SPA. |
| A non-`GET`/`HEAD` request not matched by an API route | Return `404`. |
| A static request when neither the requested file nor `index.html` can be read | Return `404`. |

The requested path is cleaned before the embedded filesystem lookup. The response content type is inferred from the extension of the file actually served; when a missing path falls back, that file is `index.html`. Go's `http.ServeContent` provides the final GET/HEAD response semantics.