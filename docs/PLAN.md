# AutoDownloadarr – project plan

> Temporary movies and series "on demand" from Usenet: request, download, unpack while downloading,
> show in the media server, delete automatically after watching.
> Complements Sonarr/Radarr (permanent collection), does not replace them.

Status: 2026-10-29 · Phase 0–5 implemented

---

## 1. Goals and non-goals

**Goals**

- Request movies and series (seasons) for *one-time* viewing.
- Own search client (Newznab indexers) and own Usenet downloader (NNTP) that **unpacks while downloading**.
- The media server (Plex, Jellyfin, Emby) is notified immediately, content appears in the library right away.
- Watch state is monitored, content is deleted automatically according to rules.
- Series run as a "rolling window": only X episodes are available, watched ones are deleted, the next one is fetched.
- Web interface with login, deployment as a single Docker container.
- Everything interchangeable (indexers, downloaders, media servers, metadata, notifications, input channels) sits behind interfaces.

**Non-goals (for now)**

- No permanent library management, upgrades or quality "cutoff" upgrades like Sonarr/Radarr.
- No torrent support in the MVP (possible later through the interfaces).
- No transcoding.

---

## 2. Technology choices

| Area | Choice | Rationale |
|---|---|---|
| Backend | **Go** | Excellent concurrency (many parallel NNTP connections via goroutines), high network throughput, a static binary → tiny Docker image, fast builds, cross-compilation for amd64/arm64 (NAS, Raspberry Pi). |
| RAR extraction | `github.com/nwaples/rardecode/v2` (pure Go) | Reads RAR 1.5–5, multi-volume archives, **as a stream** via `io.Reader` – exactly what we need for unpacking while downloading. Supports encrypted archives (password option). 7z/ZIP incl. AES via pure-Go libraries. |
| PAR2 | `par2cmdline-turbo` as an external binary in the image | Mature and fast; implementing our own PAR2 repair is not worth it. Verification (MD5/CRC) is done by us. |
| yEnc | own implementation in Go | Simple, easy to test; SIMD optimisation later if needed. |
| Database | **SQLite** (`modernc.org/sqlite`, no CGO) + migrations (`goose`) + `sqlc` | No extra container, backups = one file. The repository layer allows Postgres later. |
| HTTP/API | `chi` router, REST + Server-Sent Events for live progress | Lean, close to the standard library. |
| Frontend | **TypeScript + React + Vite**, Tailwind + shadcn/ui, TanStack Query | Large ecosystem; the build is embedded into the binary via `go:embed` → one artifact. |
| Container | Multi-stage build, Alpine runtime image (because of par2) | `PUID`/`PGID`, volumes `/config`, `/data`. |

**Why not …**

- *Python* (like SABnzbd): would need C extensions for yEnc/network throughput; packaging is harder.
- *Node/TypeScript in the backend*: feasible, but CPU-heavy decoding/unpacking fits the event loop poorly.
- *Rust*: maximum performance, but much slower development for a project with a lot of integration code.
- *C#/.NET* (like Sonarr/Radarr): a good alternative, but larger images and more runtime overhead than Go.

---

## 3. Architecture

Modular monolith: one process, clearly separated packages, communication via an internal event bus.
All external systems are *providers* behind Go interfaces, registered through a registry
(`registry.Register("newznab", newznabFactory)`). A new indexer/media server = a new package that implements an interface.

```
                         ┌──────────────────────── Web UI (React) ────────────────────────┐
                         │  Search · Library · Downloads · Settings · Users               │
                         └──────────────────────────────┬─────────────────────────────────┘
                                                        │ REST + SSE
┌───────────────┐   ┌───────────────────────────────────▼──────────────────────────────────┐
│ Input channels│──▶│                         Core / Domain                                │
│ UI, API,      │   │  Requests · Movies · Series · Episodes · Lifecycle engine · Scheduler│
│ watchlists,   │   └──┬───────────┬────────────┬──────────────┬─────────────┬─────────────┘
│ bots, *arr API│      │           │            │              │             │
└───────────────┘      ▼           ▼            ▼              ▼             ▼
               ┌──────────┐ ┌───────────┐ ┌──────────────┐ ┌──────────┐ ┌──────────────┐
               │ Metadata │ │ Indexer   │ │ Decision     │ │ Download │ │ Media server │
               │ TMDB,TVDB│ │ Newznab,  │ │ engine       │ │ client   │ │ Plex,        │
               │          │ │ Prowlarr  │ │ parser,      │ │ internal │ │ Jellyfin,    │
               │          │ │           │ │ profile,     │ │ SAB/NZBG │ │ Emby         │
               └──────────┘ └───────────┘ │ scoring      │ └────┬─────┘ └──────┬───────┘
                                          └──────────────┘      │              │ watch events
                                             ┌──────────────────▼───┐          ▼
                                             │ Internal downloader  │   Watch tracker
                                             │ NZB→NNTP→yEnc→       │   (webhook/WS/polling)
                                             │ stream unpack→PAR2   │
                                             └──────────────────────┘
```

### 3.1 Package structure (proposal)

```
cmd/autodownloadarr/        main, wiring
internal/
  config/                   env + config file, defaults
  db/                       migrations, sqlc queries, repositories
  auth/                     users, sessions, API keys, (later OIDC)
  domain/                   entities + state machines
  events/                   internal event bus
  scheduler/                persistent jobs/timers (deletion deadlines, searches, polling)
  lifecycle/                rules: movie deletion, series window, storage
  metadata/        (iface)  tmdb/, tvdb/
  indexer/         (iface)  newznab/, prowlarr/
  release/                  release name parser, quality profiles, scoring
  downloadclient/  (iface)  internal/, sabnzbd/, nzbget/
  usenet/                   nzb/, nntp/ (pool), yenc/, assembler/, unpack/, par2/, deobfuscate/
  password/        (iface)  nzbmeta/, jobname/, release/, list/, subject/, nfo/, archivecomment/, manual/
  mediaserver/     (iface)  plex/, jellyfin/, emby/
  notify/          (iface)  telegram/, discord/, ntfy/, webhook/
  inputs/          (iface)  plexwatchlist/, trakt/, arrapi/ (Overseerr-compatible), telegrambot/
  library/                  file naming, paths, safe deletion
  api/                      HTTP handlers, SSE
web/                        React frontend (embedded)
```

### 3.2 Core interfaces (sketch)

```go
type MetadataProvider interface {
    SearchMovies(ctx context.Context, q string) ([]MovieMeta, error)
    SearchSeries(ctx context.Context, q string) ([]SeriesMeta, error)
    Movie(ctx context.Context, id ExternalIDs) (MovieMeta, error)
    Series(ctx context.Context, id ExternalIDs) (SeriesMeta, []SeasonMeta, error) // incl. episodes and air dates
}

type Indexer interface {
    Name() string
    Capabilities(ctx context.Context) (Caps, error)               // supported search types/IDs/categories
    Search(ctx context.Context, q SearchQuery) ([]Release, error) // movie(imdb/tmdb) | episode(tvdb, S, E) | season | free text
    FetchNZB(ctx context.Context, r Release) (io.ReadCloser, error)
}

type DownloadClient interface {
    Add(ctx context.Context, nzb io.Reader, job JobSpec) (JobID, error)
    Status(ctx context.Context, id JobID) (JobStatus, error)
    Remove(ctx context.Context, id JobID, deleteData bool) error
    Events() <-chan DownloadEvent                              // progress, done, errors
}

type MediaServer interface { // all paths here are media server paths; the PathMapper converts them
    Name() string
    Test(ctx context.Context) error
    Libraries(ctx context.Context) ([]Library, error)
    NotifyAdded(ctx context.Context, path string) error        // targeted partial scan
    NotifyRemoved(ctx context.Context, path string) error
    ResolveItem(ctx context.Context, path string) (ItemRef, error)
    WatchState(ctx context.Context, item ItemRef, users []string) (WatchState, error)
    Subscribe(ctx context.Context) (<-chan PlaybackEvent, error) // start/progress/stop/watched – webhook, WS or polling
}

type PasswordSource interface { // see section 7
    Name() string
    Candidates(ctx context.Context, pc PasswordContext) ([]PasswordCandidate, error)
}

type Notifier interface { Send(ctx context.Context, n Notification) error }

type InputSource interface { // watchlist etc., polled periodically
    Name() string
    Poll(ctx context.Context) ([]MediaRequest, error)
}
```

---

## 4. Data model (core)

| Table | Important fields |
|---|---|
| `users` | id, name, password_hash (argon2id), role (admin/user), media_server_user_ids |
| `quality_profiles` | name, max_resolution, min_resolution, allowed_sources, codecs, required_words, forbidden_words, preferred_words (+score), min/max MB per minute, prefer_proper, password_policy |
| `movies` | tmdb_id, imdb_id, title, year, profile_id, requested_by, state, keep_flag, delete_after, attempts, next_search_at, last_error |
| `series` | tvdb_id, tmdb_id, title, profile_id, requested_by, window_size, season_selection, keep_flag |
| `episodes` | series_id, season, episode, air_date, state, file_id, watched_at, attempts, next_search_at, last_error |
| `releases` | candidates per search: indexer, guid, title, size, parsed_attrs, score, rejected_reason, fingerprint (name+size), password_flag (Newznab), searched_at |
| `blocklist` | fingerprint, guid, media_item, error class, reason (missing articles, unknown password, spam, fake, wrong language …), timestamp |
| `download_jobs` | release_id, client, state, progress, speed, error_kind, error, paths, last_progress_at, encrypted, password (encrypted), password_source |
| `passwords` | password (encrypted), scope (global/indexer/group), scope_ref, hits, last_hit_at, created_by |
| `attempts` | media_item, release_id, started/finished, result, error class, details – history of all attempts |
| `files` | **every file we created**: path, size, media_item, created_at – basis for safe deletion |
| `scheduled_tasks` | type, ref, run_at, payload (persistent timers, survive restarts) |
| `events_log` | history/audit for the UI ("deleted because …") |
| `providers` | configuration per indexer/server/media server/notifier (type + JSON settings, secrets encrypted) |
| `path_mappings` | provider_id, local_path, remote_path, remote_os (posix/windows) – path mapping per media server/client |

---

## 5. Flows

### 5.1 State machine media item (movie or episode)

```
Requested ─▶ Searching ─▶ Downloading ─▶ Unpacking/Verifying ─▶ Available ─▶ Watching ─▶ Watched ─▶ PendingDelete ─▶ Deleted
    ▲            │             │                  │
    │            ▼             └──── error ───────┴──▶ Failed ─▶ (blocklist release, next candidate) ─▶ Searching
    │       NotFound ─▶ (retry on schedule, e.g. not aired yet)
    └────────────── "Download again" from Deleted
```

Additional intermediate state `PasswordRequired` (only in the "wait" password mode, section 7.3): Downloading ⇄ PasswordRequired.

### 5.2 Movie

1. Request (UI/watchlist/API) → metadata from TMDB.
2. Pre-check: is the movie already in the media server (e.g. via Radarr)? → hint instead of download.
3. Search all active indexers (ID-based, free-text fallback) → parser → profile filters → scoring → candidate list.
4. Best candidate → optional availability check (NNTP `STAT` on a sample of articles) → download.
5. Stream unpack directly into the target folder under the final name; after the first 32 MB `NotifyAdded` (watchable during the download), again after completion.
6. Watch tracker reports "watched" → deletion time = now + `movie_delete_after` (default 48 h) → notification "will be deleted on …".
7. Timer expires → delete files (only from the `files` table), `NotifyRemoved`, state `Deleted`.

Additionally: **expiry deadline** – unwatched movies are deleted after N days (e.g. 14, configurable, can be disabled).
The **"Keep" switch** pauses all deletion rules.

### 5.3 Series – rolling window

Settings per series (default from global settings):

- **Season selection**: individual season(s), all, or "from S02E05".
- **Window size X** (default 3): number of available, not yet watched episodes.
- **Deletion trigger** (protects against accidentally clicking the next episode):
  - *Progress* (default): episode N is deleted once at least **10 %** of episode N+1 has been watched.
  - *Waiting time*: episode N is deleted **Y minutes** (e.g. 15) after N+1 started, if N+1 is still playing or was continued.
  - *Watched state*: episode N is deleted once the media server marks it as watched (+ optional grace period).
  - Progress and waiting time can be combined ("whichever comes first"); percentage and minutes are configurable.
- **Season boundary**: at the end of a season, continue automatically with the next selected season.
- **Running series**: episodes that have not aired yet are searched after their air date (+ buffer).

Logic (runs on every playback event and periodically):

```
position  = last episode started/watched (per tracked user)
keep      = episodes from position, at most X ahead, plus the currently playing one
delete    = all available episodes before position whose deletion trigger fired
fetch     = next missing episodes in order until X unwatched episodes are available/downloading
```

Example X = 3: start → E1, E2, E3 loaded. User starts E2 → E4 is fetched immediately; once 10 % of E2 has been watched, E1 is deleted.
(Fetching already starts on playback, deletion only on the trigger – so the buffer never drops below X.)

Special cases: user skips ahead (do all skipped episodes count as consumed? → setting), rewatch (no repeated deletion with keep), double episodes (S01E01E02 as one file), season packs (when single episodes cannot be found: download a season pack, extract only the window episodes – possible with stream unpacking because we extract specific files from the archive).

### 5.4 Watch tracking

| Media server | Primary | Fallback |
|---|---|---|
| Plex | WebSocket notifications from the server (`/:/websockets/notifications`, playing events, no Plex Pass needed) | Polling `/status/sessions` + `viewCount` of the items; webhooks (Plex Pass) optional |
| Jellyfin | WebSocket/sessions API | Polling `UserData.Played`; webhook plugin optional |
| Emby | like Jellyfin (shared base implementation) | – |

Configurable: **which media server users count** (one, several = "all must have watched" or "one is enough").
The default is the single-person case (owner of the Plex server, detected automatically during setup); multi-user is part of the data model from the start because the project will be open source.
Watched threshold: media server state or our own value (e.g. ≥ 90 % progress).

---

## 6. Internal downloader (core piece)

```
NZB ─▶ parser ─▶ file list (volumes, PAR2, other) ─▶ prioritisation
                                         │
           ┌────────────────────────────▼─────────────────────────────┐
           │ NNTP pool: multiple servers, priority, N connections/srv │
           │ TLS, AUTH, pipelining, retry, fallback to backup server  │
           └────────────────────────────┬─────────────────────────────┘
                                        ▼
                          yEnc decode + CRC32 per segment
                                        ▼
                  assembler: write segments at offsets in volume files,
                  track "available up to byte n" per volume
                                        ▼
          stream unpacker reads volumes sequentially (blocks until data arrives)
                                        ▼
                   target file in the media folder (final name, grows)
```

**Unpacking while downloading**

- **Implemented in phase 1.** Volumes are downloaded in archive order (PAR2 index first, then `part1, part2, …` or `.rar, .r00, …`, then the rest).
- The running download exposes its files as a filesystem (`fs.FS`). Reads block until the bytes are contiguously on disk; `rardecode` reads through it and writes the target file while further volumes are still downloading. Once the last volume is there, the movie is practically finished immediately.
- The video is written under its final name right away. As soon as the first 32 MB are on disk (container header with runtime and codecs), the media server gets a partial scan – the title is watchable while the rest is still downloading. A second scan follows after completion. Other files (extras; samples are not extracted at all) are named `name.partial` while writing. If the attempt fails, the file is deleted and rescanned.
- If an article is permanently missing, stream unpacking aborts with "data missing" instead of hanging (fallback to repair, see below).
- Special case *store mode* (RAR m0, common for scene releases): payload is stored uncompressed in the archive → we could write segments directly to the correct offset of the target file and not store the RAR volumes at all. Saves disk space and I/O. (Optimisation for later.)
- After unpacking: verification via CRC from the RAR header or PAR2 hashes.

**Watching before the download is finished (variant A implemented)**

To be able to watch *while* downloading, there are two approaches. A is implemented (growing file, see above); B remains an option for later if seeking into not-yet-downloaded parts becomes important:

| | A: Growing file | B: Virtual filesystem |
|---|---|---|
| Idea | The extracted file appears in the media server immediately and grows | A drive (WebDAV/FUSE) shows the *complete* file immediately; reads from Plex fetch the required articles on demand (with read-ahead) |
| Seeking | Only into already downloaded parts | Anywhere; missing parts are fetched immediately |
| Prerequisite | none | RAR in store mode (common) or a file without an archive; Plex must mount the drive (FUSE in the container or WebDAV mount on the Plex host) |
| Risk | Plex reads runtime/index (MKV cues at the end of the file) incorrectly on the first scan; playback stops when it catches up with the download | Considerably more effort; users have to set up the mount |
| Effort | low | high (its own phase) |

Recommendation: try A first as an option (little effort, shows the limits of Plex), B as its own later phase if A is not enough.

**Failure cases** (overall concept see section 9)

- Missing articles (430) on all servers → stream unpacking pauses, PAR2 recovery blocks are downloaded (only as many as needed), `par2` repairs, then unpack classically.
- Encrypted archive → password search (section 7); the source only counts as unusable once no password fits.
- Not repairable, fake (e.g. `.exe`, wrong size/runtime) → release goes on the blocklist, next candidate automatically.
- **Availability check up front** via `STAT` reduces false starts.
- **Deobfuscation**: resolve random file names via PAR2 metadata (original names + MD5 of the first 16 KiB) and file signatures.
- Non-RAR: 7z/zip (pure-Go libraries), direct `.mkv`/`.mp4` without an archive.
- Samples, `.nfo`, `.exe` etc. are discarded.

**More downloader features**: bandwidth limit (with schedule), global/parallel job limits, priority ("watch now" before background fetching), disk space watchdog (pause below minimum free space), article cache in RAM.

**Interchangeability**: because the downloader sits behind `DownloadClient`, SABnzbd/NZBGet can be connected as alternative clients (without stream unpacking). Useful for comparison and as a fallback.

---

## 7. Password-protected releases

Encrypted archives are especially common with German releases. **Password-protected does not mean broken**: a source only counts
as unusable once no suitable password was found from any source.

### 7.1 Password sources

Every source is its own module behind an interface (`PasswordSource`). All sources return candidates that are merged,
deduplicated and tested in this order. Every candidate remembers its origin (for the UI and statistics).

| # | Source | Examples | Phase |
|---|---|---|---|
| 1 | **NZB metadata** | `<head><meta type="password">secret</meta></head>` (also multiple `password` entries) | 2 |
| 2 | **NZB file name / job name** | `Movie.2024.German.720p.WEB.x264{{secret}}.nzb`, SABnzbd notation `Jobname / secret`, `password=secret` – applies to upload, API and indexer downloads (`Content-Disposition`) | 2 |
| 3 | **Release title and indexer data** | `{{secret}}` in the title, Newznab attributes/description/comment, default password per indexer | 2 |
| 4 | **Password list** | global, per indexer, per release group; sorted by success rate ("learned" passwords) | 2 simple, 3 with groups + statistics |
| 5 | **Manual entry** | web UI, API, later reply to the bot | 2 |
| 6 | **Subject lines / file names in the NZB** | `[PW: secret]`, `Password - secret`, `password.txt` as a file name | 3 |
| 7 | **Companion files in the post** | `.nfo`, `.txt` are downloaded first (because they are small) and searched for password patterns | 3 |
| 8 | **Archive comment** | RAR comment, readable as long as the headers are not encrypted | 3 |

The search patterns are configurable regular expressions (defaults include `pw`, `pass`, `passwort`, `password`, `kennwort`, `{{…}}`),
so users can add new conventions without changing code. Found values are cleaned up (spaces, quotes, brackets).

```go
type PasswordSource interface {
    Name() string
    // ctx contains the NZB, release, indexer and already downloaded companion files
    Candidates(ctx context.Context, pc PasswordContext) ([]PasswordCandidate, error)
}
```

### 7.2 Detecting and testing

- **Detect early**: the first volume is downloaded first; the archive header is read from the first segments.
  - Only data encrypted → the file list is visible, the fake check (`.exe`, wrong size) is possible immediately.
  - Headers encrypted (`rar -hp`) → the file list is only available with the password.
- **Test before downloading much**:
  - RAR5 contains a password check value → candidates can be tested in milliseconds without decrypting data.
  - RAR3/4, 7z-AES, ZIP-AES: decrypt the first data block with the candidate and verify against the CRC or container signature (MKV/MP4 header).
- **Hit** → stream unpacking continues normally with the password; the password and its origin are stored with the job, the list's success rate is incremented.
- **Nested archives**: inner archives go through the same password search (last successful password first).
- **Limits**: at most N candidates from the list per release (e.g. 50), no brute force.
- **Detect spam**: if the NFO/file name points to a web site for the password ("password at www…"), the release counts as spam → blocklist with its own reason, not as "password unknown".

### 7.3 When no password fits

```
Encrypted detected ─▶ test candidates from all sources
   ├─ hit ───────────▶ continue downloading + stream unpacking
   └─ no hit
        ├─ mode "automatic" (default): blocklist release (reason "password unknown") ─▶ next candidate.
        │      The release stays visible under "Password required" in case the password is supplied later.
        └─ mode "wait": download paused, state "Password required", notification.
               After time T (e.g. 30 min) without input ─▶ same as "automatic".

Manual entry ─▶ checked immediately; already downloaded data is reused,
                optionally "add password to the list" (global / indexer / group).
```

### 7.4 Consideration during release selection

- The Newznab attribute `password` is evaluated (0 = no, 1 = password-protected, 2 = contains an inner archive; maintained with varying quality depending on the indexer).
- Profile option "password-protected releases": *allow* · *only when password known* · *avoid* (score penalty) · *reject*.
- If the password is already known from the NZB or title, there is no penalty.

### 7.5 Security

- The password list and job passwords are stored encrypted in the DB like other secrets and masked in logs.
- Passwords never leave the system (no queries to external services).
- Import/export of the password list as a text file (one line per password, optionally with scope).

---

## 8. Release selection

**Parser** (own package, table-tested with many real release names):
title, year, season/episode(s), resolution, source (WEB-DL, WEBRip, BluRay, HDTV, …), codec, HDR, audio, language(s) (`German`, `DL`, `ML`, …), group, PROPER/REPACK, edition.

**Quality profile**

- **Maximum resolution** (e.g. 720p) – hard limit; within it the highest wins.
- Minimum resolution (optional).
- **Required words** (all/one must match, regex possible) – e.g. `German`.
- **Forbidden words** – e.g. `CAM`, `TS`, `HDCAM`, `Subbed`, `LD`.
- Preferred words with scores (e.g. `DL` +10, certain groups +5).
- Size limits per minute of runtime (filters fakes and huge remuxes).
- Preferred codec (x265 saves space for temporary content).
- Minimum release age / minimum grabs (optional, against fakes).
- Handling of password-protected releases: allow / only with known password / avoid / reject (section 7.4).

**Scoring**: resolution (≤ max) > source > preferred words > size close to target > age/grabs. Rejected candidates are stored with a reason and shown in the UI ("why was nothing found?"). Manual selection of a release from the list is possible.

---

## 9. Error handling and automatic source switching

Principle: every error is **classified**, and the class determines the reaction. The user only has to intervene
when no source can really be found any more or the configuration is wrong. Every attempt is visible in the UI with its reason.

### 9.1 Error classes

| Class | Examples | Reaction |
|---|---|---|
| **Source broken** (affects only this release) | articles missing (DMCA/takedown, retention), PAR2 not enough, password unknown (after unsuccessful password search, section 7), spam, fake/malware (`.exe`, `.lnk`), unpacking/CRC fails, runtime or language does not match | blocklist release → **next candidate** |
| **Temporary** (infrastructure) | indexer timeout/5xx, API limit reached, NNTP connection dropped, "too many connections", media server offline | retry with exponential backoff + jitter, job is *paused*, not counted as failed; circuit breaker per provider |
| **Configuration** | wrong Usenet credentials (NNTP 481/502), invalid API key, path not writable | stop immediately, no retry loop; warning in the UI + notification |
| **Resources** | disk full, minimum free space undercut | pause downloads, apply storage rules, resume automatically |
| **Not yet available** | no matching releases, movie not yet released as digital/BluRay, episode not aired | state "Waiting", new search on schedule; release dates from TMDB determine the earliest search time |

In code: custom error types with a class (`errors.Is(err, ErrSourceBroken)`, `ErrTransient`, `ErrConfig`, …) set by every layer
(indexer, NNTP, unpacker, media server). The lifecycle engine decides exclusively based on the class.

### 9.2 Source switching flow

```
Search ─▶ candidate list (sorted by score, stored in the DB)
   │
   ▼
Candidate k ─▶ pre-check: fetch NZB · file types/size plausible · STAT sample of the articles
   │   └─ failed ─▶ blocklist ─▶ candidate k+1
   ▼
Download + stream unpacking
   │   ├─ missing articles > PAR2 reserve (calculated continuously) ─▶ abort immediately ─▶ blocklist ─▶ k+1
   │   ├─ missing articles ≤ PAR2 reserve ─▶ repair ─▶ continue
   │   ├─ encrypted ─▶ password search (section 7) ─▶ hit: continue · no hit: blocklist ─▶ k+1
   │   └─ fake / spam / unpack error ─▶ blocklist ─▶ k+1
   ▼
Check before release (ffprobe): runtime ≈ TMDB, video and audio track present, audio language matches
   │   └─ failed ─▶ delete file ─▶ blocklist ─▶ k+1
   ▼
Available

Candidates exhausted ─▶ new search: all indexers, fresh results, free-text fallback with alternative titles
                        (German title, original title, aliases from TMDB)
Still nothing        ─▶ state "Not found", new search with backoff (1 h, 6 h, 24 h, then daily),
                        one-time notification
```

**Details**

- **Candidate list is stored**: switching to the next source does not need a new indexer query (spares API limits) and happens within seconds. The list is valid for N hours, after which a new search is done.
- **Detect duplicates**: the same release often appears on several indexers. A fingerprint of normalised name + size ensures a broken release counts as broken everywhere.
- **Fail fast**: missing articles are continuously compared against the available PAR2 blocks. If it is clear that the repair will not be enough, the download is aborted immediately instead of after the complete download.
- **Attempt limit**: after e.g. 5 failed sources the item is paused and a notification is sent (prevents endless loops with systematic problems, e.g. a wrong required word).
- **Optional fallback profile**: if nothing works in the profile, a looser profile may be used (if enabled), e.g. also 480p. *Required words are never relaxed.*
- **Cleanup**: partial data, `.partial` files and working folders of a failed attempt are deleted immediately.
- **Series**: same logic per episode. If a single episode is not available anywhere, a season pack is tried as a source. Other episodes in the window keep downloading independently.

### 9.3 Errors noticed only while watching

- Button **"Source broken"** (with reason: wrong language, poor quality, video/audio defects, wrong movie) in the UI → delete file, blocklist release, next source is loaded automatically.
- Optionally later: Plex playback repeatedly aborts early → hint in the UI ("check source?"), no automatic deletion.

### 9.4 System robustness

- **Persistent state**: every step is recorded in the DB. After a restart, downloads continue (segment progress stored) or are restarted cleanly.
- **Idempotent steps**: triggering a scan, deleting a file, sending a notification – running them multiple times does no harm, so they can be repeated safely.
- **Circuit breaker** per indexer and Usenet server: disabled for a time T after N errors, others take over; state in the health display.
- **NNTP fallback per article**: if an article is missing on the primary server, it is requested from backup servers (ideally a different backbone) before it counts as missing.
- **Watchdog**: jobs without progress for X minutes are aborted and restarted.
- **Media server offline**: scans and notifications go into a queue and are retried; file deletions on disk are not blocked by this.
- **Bundle notifications**: rather "3 sources for *Movie* failed, next one running" than three separate messages.

### 9.5 Transparency and manual intervention

- Attempt history per movie/episode: release, indexer, error class, reason, duration, timestamp.
- Actions: "Next source", "Search again", "Select release manually", "Enter password", remove blocklist entry, resume item.
- Structured logging (`log/slog`, JSON) with a correlation ID per item and attempt, so errors in issues can be traced.

---

## 10. Input channels ("different ways")

| Channel | Phase | Note |
|---|---|---|
| Web UI search (TMDB) | MVP | poster, description, season selection |
| REST API with API key | MVP | basis for everything else |
| Manual NZB upload | MVP | for special cases |
| Paste link (IMDb/TMDB/TVDB URL) | 2 | also as bookmarklet / share target (PWA) |
| Plex watchlist (own + friends) | 3 | add to the watchlist on any device → gets downloaded; own tag/filter possible |
| Trakt watchlist/lists | 4 | |
| Overseerr/Jellyseerr | later | Not planned for now. Idea: partially emulate the Radarr/Sonarr API so Seerr can use AutoDownloadarr as "Radarr" |
| Telegram/Discord bot | 4 | `/movie Dune` → selection → done message |

---

## 11. Further features

- **Handover to Radarr/Sonarr**: "Keep after all" → title is added to Radarr/Sonarr, temporary copy stays until import.
- **Duplicate protection**: checks the media server and optionally Radarr/Sonarr before downloading.
- **Storage management**: maximum size of the temporary library; when exceeded, delete watched first, then oldest unwatched (with notification).
- **Notifications**: ready, failed, will be deleted soon, deleted (Telegram, Discord, ntfy, generic webhook).
- **Multi-user** (implemented): admin/user roles, requests per user, optional quotas (count/GB). Mapping to media server users follows.
- **"Watch now" priority**: download with the highest priority, message as soon as it is playable.
- **History & statistics**: what was downloaded/deleted and why, data volume used.
- **Dashboard with live progress** (SSE): speed, ETA, unpack status.
- **Subtitles** (optional, later): fetch via OpenSubtitles.
- **Backup/restore** of configuration and DB from the UI.
- **Health checks** in the UI: indexer API limits, Usenet login, media server reachable, paths writable.

---

## 12. Security and privacy

- Login with argon2id hashes, session cookies (`HttpOnly`, `SameSite`), CSRF protection, rate limit on login.
- First start: the setup wizard creates the admin (no default password). This requires a one-time **setup token** that is only printed in the server log (section 13.2).
- Forgot password: `autodownloadarr reset-password <user>` in the container.
- Optional: OIDC (Authentik, Authelia, Keycloak) or trusting reverse proxy headers; Plex login later.
- Secrets (Usenet password, API keys) encrypted in the DB (key from env/file in `/config`).
- **Safe deletion**: only what is in `files` *and* below the configured temporary media folders is deleted. Sonarr/Radarr folders are therefore never affected.
- Logs without credentials.

---

## 13. Deployment

```yaml
services:
  autodownloadarr:
    image: ghcr.io/<user>/autodownloadarr:latest
    environment:
      - PUID=1000
      - PGID=1000
      - TZ=Europe/Berlin
    volumes:
      - ./config:/config            # DB, settings, logs
      - /mnt/media/temp:/data       # downloads (temporary) + temporary libraries
    ports:
      - "8686:8686"
    restart: unless-stopped
```

- **Dedicated libraries in the media server** recommended: e.g. `/data/library/movies` → Plex library "Temp Movies", `/data/library/tv` → "Temp Series".
- **Path mapping** between container and media server – see below.
- Download working folder and library on the **same filesystem** (atomic rename instead of copying).
- Naming compatible with Plex/Jellyfin: `Title (Year) {tmdb-123}/Title (Year).mkv`, `Series (Year) {tvdb-456}/Season 01/Series - S01E01 - Episode Title.mkv`.
- Multi-arch image (amd64, arm64), healthcheck endpoint, configuration via UI (env only for bootstrap).

### 13.1 Path mapping (container ↔ media server)

AutoDownloadarr and the media server usually see the same files under different paths, for example:

| AutoDownloadarr (in the container) | Plex server | Note |
|---|---|---|
| `/movies/` | `/mnt/hdd/movies/` | Plex directly on the host |
| `/series/` | `/mnt/hdd/series/` | |
| `/movies/` | `/data/movies/` | Plex also in a Docker container |
| `/movies/` | `D:\Media\Movies\` | Plex on Windows |

- **Mapping table per media server** (and later per external download client and Radarr/Sonarr handover): any number of *local path ↔ remote path* pairs.
- **Both directions**:
  - local → remote: trigger a scan (`NotifyAdded`/`NotifyRemoved`) with the path Plex knows.
  - remote → local: Plex reports playback/watched with its file path → we derive our movie or episode from it.
- **Longest matching prefix wins** (e.g. `/movies/4k/` before `/movies/`), comparison only at folder boundaries (`/movies` does not match `/movies-old`).
- **Normalisation**: trailing slashes, `\` vs `/`, case sensitivity on Windows servers, Unicode normalisation (umlauts).
- **Central component** `PathMapper` used by all media server adapters; the adapters themselves only work with media server paths.
- **Wizard in the UI**:
  - reads the library folders from Plex (`/library/sections` returns the `Location` paths),
  - suggests mappings based on identical folder names (`/movies` ↔ `/mnt/hdd/movies`),
  - shows a preview: "`/movies/Dune (2021)/Dune (2021).mkv` → `/mnt/hdd/movies/Dune (2021)/Dune (2021).mkv`".
- **Validation**: warning if a target folder of AutoDownloadarr is not inside any Plex library after mapping, or if no mapping applies. Optional live test: create a test folder, trigger a partial scan, check whether Plex sees it, remove it again.
- **Fallback for matching**: if a Plex item is not found via the path, it is matched via the IDs (`tmdb://`, `tvdb://` in the Plex GUIDs) – this protects against misconfigured mappings.
- Without mapping (same paths on both sides) everything works unchanged.

### 13.2 Setup wizard on first start

On first start (no account yet) the web interface guides through the complete setup:

| Step | Content | Validation | Available from |
|---|---|---|---|
| 1. Account | setup token from the container log, username, password | token must match (protects freshly started, publicly reachable instances) | **phase 0 (implemented)** |
| 2. Media server | sign-in via Plex (PIN flow via plex.tv, no token copying), choose server, choose libraries for temporary movies/series, path mapping with suggestions (13.1) | server reachable, libraries readable, mapping preview | phase 2 |
| 3. Indexer | Newznab URL + API key, categories | `t=caps` query, supported search IDs (IMDb/TMDB/TVDB) | phase 2 |
| 4. Usenet | server, port, SSL, user, password, connections; optional backup server | login test, number of allowed connections | phase 2 (downloader from phase 1) |
| 5. Defaults | quality profile (max resolution, "German" preset with required words), movie expiry deadline, series window size | – | phase 2 / 4 |
| 6. Done | summary, open items, link to the dashboard | – | phase 0 |

- Every step is **tested before saving**; errors are shown concretely ("login rejected", "host unreachable").
- All steps except the account are **skippable**; missing parts then appear as a hint on the dashboard.
- **Progress is stored**: if someone closes the browser, they return to the next open step after signing in.
- The wizard can be **started again** later from the settings; the same forms are also available there individually.
- All steps are implemented today; only the account step is mandatory.

---

## 14. Roadmap

| Phase | Content | Result |
|---|---|---|
| **0 – Foundation** ✅ | repo, Go module, config, SQLite+migrations, event bus, scheduler, auth + setup wizard (account step), React skeleton with i18n (en/de), Dockerfile, CI (tests, lint, image build) | empty app with login runs in the container |
| **1 – Downloader** ✅ | NZB parser, NNTP pool (TLS, auth, multiple servers with priority, retry), yEnc, assembler, **unpacking while downloading** (RAR, multi-volume, password), CLI command `download-nzb`, test NNTP server, benchmarks | any NZB can be downloaded reliably and quickly; the movie is ready as soon as the last volume arrives |
| **2 – MVP movies** ✅ | TMDB, Newznab (ID search with text search fallback), release parser, quality profile (max/min resolution, required/forbidden/bonus words, MB per minute, codec, password rule), Plex (PIN sign-in, library, path mapping with suggestions, partial scan, watched state via polling), deletion and expiry deadlines, error classes, candidate list, blocklist, automatic source switching, new search with backoff, passwords from NZB metadata, NZB/job name, release title and password list, encrypted credentials, setup wizard with all steps, UI: movie search, library, detail page with releases/history/blocklist, live progress, settings | complete movie cycle works, unavailable sources are replaced automatically |
| **3 – Robust downloader** ✅ | PAR2 repair as fallback (download only the needed recovery blocks, abort as soon as more is missing than can be repaired), 7z/ZIP (7-Zip in the image), `STAT` pre-check of a sample, restore obfuscated names via PAR2, passwords from subject lines and NFO files, spam detection ("password on web site"), watchdog for hanging downloads, indexer pause after repeated errors, bandwidth limit | unpacking while downloading, repair only when needed, few manual interventions |
| **4 – Series** ✅ | TMDB series (seasons, episodes, air dates), Newznab episode search (TVDB/IMDb ID, otherwise text), season/episode in release names, season selection, rolling window with deletion trigger (progress of the next episode, minutes after start, watched), search after airing, daily refresh of the episode list, Plex series library with playback progress, shared download core with movies, season packs as a fallback source (only when no single episode can be found), UI for series | series cycle works |
| **5 – Integrations** ✅ | Jellyfin/Emby, notifications, Plex watchlist, user management with admin/user roles and optional per-user quotas, storage management (Radarr/Sonarr deliberately dropped) | ready for everyday use |
| **6 – Comfort** | Trakt, bots, OIDC, statistics, media server user mapping, store-mode direct writing | polish |
| later | Overseerr/Jellyseerr-compatible API, more indexer types, torrent client | as needed |

---

## 15. Risks and open points

| Risk | Mitigation |
|---|---|
| Stream unpacking with missing articles | Stream optimistically, fall back to "download → PAR2 → unpack" on error |
| Media server handles growing files poorly | First scan only after 32 MB (container header complete), second scan after completion; seeking only into already downloaded parts (MKV index at the end of the file) |
| Plex keeps the watched state of a title beyond deletion | When a new file first appears, the Plex state is stored as the baseline; only changes after that count as watching (playback after download start counts). Matching only via our own file, not via the TMDB ID |
| Watch state detection (multi-user, Plex Pass) | WebSocket/polling instead of webhooks; clear user mapping in the settings |
| Indexer API limits | Caching of search results, RSS feed instead of repeated searches, limits configurable per indexer |
| Variety of release names (German releases) | Large test corpus of real names, table-driven parser tests |
| New/unknown password conventions | Password sources as modules, search patterns configurable via regex, test corpus with real NZB/release examples |
| Throughput of our own downloader | Early benchmark in phase 1 against SABnzbd/NZBGet |
| Accidental deletion | Only tracked files in allowed folders, dry-run mode, audit log |

---

## 16. Decisions

| Topic | Decision |
|---|---|
| Media server | **Plex first**; Jellyfin/Emby in phase 5 |
| Users | Single-person operation as default, multi-user part of the model (open source) |
| Indexers | **Newznab API** (generic); Prowlarr usable as a Newznab source, no dedicated adapter needed |
| Series deletion trigger | 10 % of the next episode watched (default) or a short wait after start, both configurable |
| Overseerr/Jellyseerr | not for now |
| Error handling | Errors are classified; unavailable/broken sources are replaced automatically by the next one (from the MVP) |
| Path mapping | Mapping table local path ↔ media server path, both directions, with suggestions from the Plex libraries (from the MVP) |
| Passwords | Multiple password sources (NZB meta, NZB name, title, list, subject, NFO, archive comment, manual); a source only counts as unusable when no password fits |
| Unpacking | Unpacking **while** downloading is a core feature (from phase 1); classic unpacking only as a fallback after a repair |
| Watching during download | A (growing file): video under its final name, Plex scan after 32 MB and after completion; B (virtual filesystem) later, see section 6 |
| Availability check | Can be disabled per Usenet server (on by default): before downloading, every article is checked via `STAT` with pipelining; only what is complete or repairable via PAR2 is downloaded (recovery data ≥ 1.2 × missing data). Off: sample of 20 articles. The next sources are not pre-checked in the background – that would need connections and NZB fetches during the running download |
| Future seasons | Per series: selected seasons plus all seasons after the highest one that had already aired at selection time (announced ones count as future). "All seasons" always includes future ones |
| Skipped episodes | Count as watched for all deletion triggers: deleted once a later episode reaches the trigger |
| Plex scan | Failed partial scans are retried up to six times with backoff and shown as a warning on the dashboard until the next successful scan |
| Download history | Every attempt with title, release, size, indexer, duration and result; kept when a movie or series is removed. "Downloads" page with running, queue and history (filter, pages of 50) |
| Release order | Non-rejected first, then by profile score (resolution, source, bonus words, codec, PROPER, grabs); for equal scores the newest upload first (older ones are deleted more often), releases without a date after that, then more grabs |
| Episode order | Episodes of a series download one after another in broadcast order; an earlier episode still being searched holds back the later ones. Free slots are used by other series and movies |
| Setup | Setup wizard on first start: account → Plex → indexer → Usenet → defaults; steps test before saving, skippable, can be reopened later |
| License | GPL-3.0 |
| Configuration | Environment variables with the prefix `AUTODOWNLOADARR_` only for startup (port, config folder, logging, proxy); everything else in the UI |
| Implementation phase 4 | Playback progress by polling the Plex series library every minute instead of WebSocket – sufficient for the triggers (deletion/refetch with at most one minute of delay). Season packs are the fallback source: a single episode is searched first, the whole season only without a usable hit; from the pack only the episodes of the window are kept, the rest is discarded. Specials (season 0) only when explicitly selected. Movies and series each have their own download slots ("parallel downloads" applies per area) but share the bandwidth limit. |
| Implementation phase 3 | Checking the finished file via `ffprobe` (runtime, audio track language) deferred: `ffmpeg` would grow the image by ~100 MB – later as an optional image or external tool. Learned passwords per release group also later; the password list counts hits and sorts by them. |
| Implementation phase 2 | Watched state by polling the Plex library (default every 2 min) instead of WebSocket – the WebSocket comes with series (phase 4), where the playback position matters. Manual password entry as "add password to the list + download release again". One Plex server, one movie library. |
| Implementation phase 0 | Go 1.26, chi, SQLite (modernc, no CGO) with goose; queries hand-written for now (`sqlc` only when the number of queries justifies it); React 19 + Vite + Tailwind 4, small custom UI components instead of shadcn/ui; Go module path `github.com/shyper/autodownloadarr` (placeholder until the GitHub repo) |

---

## 17. Open source

- **Language in the project**: code, commits, issues, documentation and default UI language in **English**; UI with i18n from the start, German as the first translation.
- **License**: recommendation **GPL-3.0** (like Sonarr/Radarr – forks stay open). Alternative: MIT if maximum reuse is desired.
- **No assumptions about one's own setup**: all paths, ports, users and timezone via configuration; setup wizard on first start.
- **Repo basics**: README with compose example, CONTRIBUTING, issue templates, CHANGELOG, SemVer releases.
- **CI**: tests, linting (golangci-lint, ESLint), multi-arch images to GHCR, release builds per tag.
- **Documentation for extensions**: short guide "adding a new indexer/media server" based on the interfaces.
- **Tests without real credentials**: NNTP test server and Newznab/Plex mocks in the repo, so contributors can test without their own Usenet account.
