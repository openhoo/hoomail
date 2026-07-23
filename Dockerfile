# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM oven/bun:1.3.14-alpine@sha256:5acc90a93e91ff07bf72aa90a7c9f0fa189765aec90b47bdbf2152d2196383c0 AS client
WORKDIR /src
COPY package.json bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache bun install --frozen-lockfile
COPY index.html main.tsx tsconfig.json vite.config.ts ./
COPY app/globals.css app/globals.css
COPY components components
COPY lib/utils.ts lib/utils.ts
COPY public public
RUN bun run build

FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS server
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd cmd
COPY internal internal
COPY web/embed.go web/embed.go
COPY --from=client /src/web/dist web/dist
ARG VERSION
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/root/.cache/go-build \
    VERSION="${VERSION:-$(cat internal/version/version)}" && \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath \
      -ldflags="-s -w -X github.com/openhoo/hoomail/internal/version.Value=${VERSION}" \
      -o /hoomail ./cmd/hoomail

FROM scratch
WORKDIR /app
ENV PORT=3000 \
    HOOMAIL_SMTP_PORT=2525 \
    HOOMAIL_POP3_PORT=3110 \
    HOOMAIL_DB_PATH=/app/data/hoomail.db
COPY --from=server /hoomail /hoomail
USER 65532:65532
VOLUME ["/app/data"]
EXPOSE 3000 2525 3110
HEALTHCHECK --interval=30s --timeout=3s --start-period=3s --retries=3 \
  CMD ["/hoomail", "healthcheck"]
ENTRYPOINT ["/hoomail"]
