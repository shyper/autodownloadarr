# AutoDownloadarr

**Watch once, then let it go.** AutoDownloadarr fetches movies and series from
Usenet for one-time viewing, adds them to your media server while they are
still unpacking, and removes them again after you have watched them.

It complements Sonarr and Radarr: they build the collection you keep,
AutoDownloadarr handles everything you only want to see once.

> **Status: early development.** Movies work end to end: search, request,
> download with extraction while downloading, PAR2 repair, 7z/ZIP, automatic
> fallback to the next release, Plex integration and automatic deletion after
> watching. Series keep a rolling window of the next episodes: watched ones
> are removed and the next ones fetched. See the [roadmap](docs/PLAN.md#14-roadmap).

## Planned features

- Built-in Usenet downloader that unpacks **while downloading**, with PAR2
  repair, password handling and automatic fallback to the next release
- Newznab indexers, quality profiles (maximum resolution, required and
  forbidden words, e.g. German releases only)
- Plex first, Jellyfin and Emby later: new items appear immediately, watched
  ones are removed after a configurable delay (default 48 h)
- Series as a rolling window: only the next *n* episodes are kept; starting the
  next episode removes the watched one and fetches another
- Folder mapping between the container and the media server
- Multiple ways to request: web UI, API, Plex watchlist, …

The full design is in [docs/PLAN.md](docs/PLAN.md) (German).

## Quick start (Docker)

```yaml
services:
  autodownloadarr:
    image: ghcr.io/shyper/autodownloadarr:latest
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Berlin
    volumes:
      - ./config:/config
    ports:
      - "8686:8686"
    restart: unless-stopped
```

1. Start the container: `docker compose up -d`
2. Read the one-time setup token from the log: `docker logs autodownloadarr`
3. Open <http://localhost:8686> and follow the setup wizard: Usenet provider,
   indexer, movies folder and TMDB key, Plex.

Credentials (Usenet password, API keys, Plex token) are stored encrypted with
a key in `/config/secret.key`. Keep that file with your backups.

## Configuration

Settings are made in the web UI. Environment variables only cover what is
needed before the database is available:

| Variable | Default | Description |
|---|---|---|
| `AUTODOWNLOADARR_LISTEN` | `:8686` | HTTP listen address |
| `AUTODOWNLOADARR_CONFIG_DIR` | `/config` | Database and other state |
| `AUTODOWNLOADARR_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `AUTODOWNLOADARR_LOG_FORMAT` | `text` | `text` or `json` |
| `AUTODOWNLOADARR_TRUST_PROXY` | `false` | Honour `X-Forwarded-For`/`-Proto`. Enable only behind a reverse proxy. |
| `AUTODOWNLOADARR_SESSION_TTL` | `720h` | How long a login stays valid without activity |
| `AUTODOWNLOADARR_TMDB_URL` | TMDB API | Alternative TMDB endpoint (proxies, tests) |
| `PUID` / `PGID` | `1000` | User and group the server runs as (Docker image) |

### Forgot your password?

```bash
docker exec -it -u 1000:1000 autodownloadarr /app/autodownloadarr reset-password admin
```

The new password is read from the terminal; all sessions of that user are
signed out.

### Trying the downloader

The built-in Usenet client can be run on its own. It downloads an NZB and
extracts RAR archives while they are still downloading:

```bash
export AUTODOWNLOADARR_NNTP_SERVER=news.example.com:563
export AUTODOWNLOADARR_NNTP_USER=myuser
read -rs AUTODOWNLOADARR_NNTP_PASSWORD && export AUTODOWNLOADARR_NNTP_PASSWORD
autodownloadarr download-nzb -connections 30 -out ./downloads release.nzb
```

Run `autodownloadarr download-nzb -h` for all options (archive password,
keeping archives, PAR2 recovery volumes, …).

## Development

Requirements: Go 1.26+, Node.js 24+, and for the repair and extraction
tests `par2` (par2cmdline) and 7-Zip (`7zz`, `7z` or `7za`).

```bash
make web        # build the web UI (embedded into the binary)
make run        # start the server on :8686 with ./config
make dev-web    # Vite dev server on :5173 with hot reload, proxies /api
make test       # Go and web tests
make lint       # go vet, golangci-lint, ESLint, TypeScript
make docker     # build the image
```

Project layout:

```
cmd/autodownloadarr/   entry point and CLI commands
internal/api/          HTTP API (/api/v1) and static file serving
internal/fetch/        download, extraction and repair shared by movies and series
internal/movies/       movie lifecycle: search, download, Plex, deletion
internal/series/       series: rolling window of episodes
internal/store/        persistence of settings, providers and movies
internal/release/      release name parser, quality profiles
internal/indexer/      Newznab client
internal/metadata/     TMDB client
internal/mediaserver/  Plex client, path mapping
internal/secrets/      encryption of stored credentials
internal/auth/         accounts, sessions, rate limiting, setup token
internal/config/       environment configuration
internal/db/           SQLite and migrations
internal/events/       in-process event bus (live updates)
internal/scheduler/    persistent, retryable background tasks
internal/usenet/       NZB parser, NNTP client and pool, yEnc, downloader,
                       extraction (unpack); nntp/nntptest is a fake server
internal/testutil/     test helpers (e.g. a writer for RAR5 test archives)
internal/webui/        embedded web UI build
web/                   React + TypeScript frontend
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for conventions.

## License

[GPL-3.0](LICENSE)
