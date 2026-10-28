# syntax=docker/dockerfile:1

# --- Web UI ------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Writes to /src/internal/webui/dist
RUN npm run build

# --- Server ------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/shyper/autodownloadarr/internal/buildinfo.Version=${VERSION}" \
      -o /out/autodownloadarr ./cmd/autodownloadarr

# --- Runtime -----------------------------------------------------------------
FROM alpine:3.22
# par2cmdline repairs incomplete downloads, 7zip extracts 7z/ZIP archives.
RUN apk add --no-cache ca-certificates tzdata su-exec par2cmdline 7zip
COPY --chmod=0755 docker/entrypoint.sh /entrypoint.sh
COPY --from=server /out/autodownloadarr /app/autodownloadarr

ENV AUTODOWNLOADARR_CONFIG_DIR=/config \
    PUID=1000 \
    PGID=1000
VOLUME ["/config"]
EXPOSE 8686
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD ["/app/autodownloadarr", "healthcheck"]
ENTRYPOINT ["/entrypoint.sh"]
CMD ["serve"]
