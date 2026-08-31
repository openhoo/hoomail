# Contributing

Open an issue before changing SMTP, POP3, MIME, storage, embedded-client, or Helm
contracts. Small fixes may go directly to a pull request.

## Development

Use versions pinned by `go.mod`, `package.json`, and lockfiles.

```sh
bun install --frozen-lockfile
bun x tsc --noEmit
bun run build
gofmt -w cmd internal web
go vet ./...
go test -race ./...
helm lint --strict charts/hoomail
```

Protocol and parser changes need boundary, malformed-input, cancellation, and
fail-closed tests. Changes below `third_party/go-smtp` must also pass its
standalone module checks.

Commits use Conventional Commits. Pull requests must explain compatibility,
security, persistence, deployment, and rollback impact. Maintainers squash-merge
using the Conventional Commit pull request title. Generated client files and
lockfile changes must accompany their sources.
