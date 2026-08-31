# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM oven/bun:1.4.0-alpine@sha256:07235578f79ef8c6f97d94aee7938e76f5cdba5f21ae5dbfdd3d3d38058437eb AS client
WORKDIR /src
COPY package.json bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --frozen-lockfile
COPY index.html main.tsx tsconfig.json vite.config.ts ./
COPY app/globals.css app/globals.css
COPY components components
COPY lib/utils.ts lib/utils.ts
COPY public public
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS server
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party third_party/
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY internal internal
COPY web/embed.go web/embed.go
COPY --from=client /src/web/dist web/dist
ARG VERSION
ARG TARGETOS
ARG TARGETARCH
# Hoomail serves plain HTTP; TLS and HTTP/2 terminate at the reverse proxy.
RUN --mount=type=cache,target=/root/.cache/go-build \
    go generate ./internal/httpserver && \
    VERSION="${VERSION:-$(cat internal/version/version)}" && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -tags=nethttpomithttp2 \
      -ldflags="-s -w -X github.com/openhoo/hoomail/internal/version.Value=${VERSION}" \
      -o /hoomail ./cmd/hoomail
RUN mkdir -p /app/data/tmp && chown 65532:65532 /app/data/tmp

FROM scratch
WORKDIR /app
ENV PORT=3000 \
    HOOMAIL_SMTP_PORT=2525 \
    HOOMAIL_POP3_PORT=3110 \
    HOOMAIL_DB_PATH=/app/data/hoomail.db \
    TMPDIR=/app/data \
    SQLITE_TMPDIR=/app/data
COPY --from=server /hoomail /hoomail
COPY --from=server /src/internal/inspect/LICENSE.mailpit /licenses/LICENSE.mailpit
COPY --from=server /src/internal/inspect/LICENSE.can-i-email /licenses/LICENSE.can-i-email
COPY --from=server --chown=65532:65532 /app/data/tmp /app/data/tmp
USER 65532:65532
VOLUME ["/app/data"]
EXPOSE 3000 2525 3110
HEALTHCHECK --interval=30s --timeout=10s --start-period=3s --retries=3 \
  CMD ["/hoomail", "healthcheck"]
ENTRYPOINT ["/hoomail"]
