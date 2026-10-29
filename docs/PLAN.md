# AutoDownloadarr – Projektplan

> Temporäre Filme und Serien „on demand“ aus dem Usenet: anfordern, laden, beim Laden entpacken,
> im Medienserver anzeigen, nach dem Schauen automatisch wieder löschen.
> Ergänzt Sonarr/Radarr (dauerhafte Sammlung), ersetzt sie nicht.

Stand: 2026-10-29 · Status: Phase 0–5 umgesetzt

---

## 1. Ziele und Abgrenzung

**Ziele**

- Filme und Serien(-staffeln) zum *einmaligen* Ansehen anfordern.
- Eigener Suchclient (Newznab-Indexer) und eigener Usenet-Downloader (NNTP), der **während des Ladens entpackt**.
- Medienserver (Plex, Jellyfin, Emby) wird sofort benachrichtigt, Inhalt erscheint direkt in der Bibliothek.
- Gesehen-Status wird überwacht, Inhalte werden nach Regeln automatisch gelöscht.
- Serien laufen als „rollendes Fenster“: nur X Folgen liegen bereit, gesehene werden gelöscht, die nächste wird nachgeladen.
- Webinterface mit Anmeldung, Deployment als ein Docker-Container.
- Alles Austauschbare (Indexer, Downloader, Medienserver, Metadaten, Benachrichtigungen, Eingangskanäle) liegt hinter Schnittstellen.

**Nicht-Ziele (vorerst)**

- Keine dauerhafte Archivverwaltung, Upgrades oder Qualitäts-„Cutoff“-Upgrades wie bei Sonarr/Radarr.
- Kein Torrent-Support im MVP (über die Schnittstellen später möglich).
- Kein Transcoding.

---

## 2. Technologie-Empfehlung

| Bereich | Wahl | Begründung |
|---|---|---|
| Backend | **Go** | Sehr gute Nebenläufigkeit (viele parallele NNTP-Verbindungen über Goroutinen), hoher Netzwerk-Durchsatz, ein statisches Binary → winziges Docker-Image, schnelle Builds, Cross-Compile für amd64/arm64 (NAS, Raspberry Pi). |
| RAR-Entpacken | `github.com/nwaples/rardecode/v2` (pure Go) | Liest RAR 1.5–5, mehrteilige Archive, **als Stream** über `io.Reader` – genau das, was wir fürs Entpacken während des Ladens brauchen. Unterstützt verschlüsselte Archive (Passwort-Option). 7z/ZIP inkl. AES über Pure-Go-Bibliotheken. |
| PAR2 | `par2cmdline-turbo` als externes Binary im Image | Ausgereift und schnell; eine eigene PAR2-Reparatur lohnt sich nicht. Verifikation (MD5/CRC) machen wir selbst. |
| yEnc | eigene Implementierung in Go | Einfach, gut testbar; bei Bedarf später SIMD-optimiert. |
| Datenbank | **SQLite** (`modernc.org/sqlite`, ohne CGO) + Migrationen (`goose`) + `sqlc` | Kein extra Container, Backups = eine Datei. Repository-Schicht erlaubt später Postgres. |
| HTTP/API | `chi` Router, REST + Server-Sent Events für Live-Fortschritt | Schlank, Standardbibliothek-nah. |
| Frontend | **TypeScript + React + Vite**, Tailwind + shadcn/ui, TanStack Query | Großes Ökosystem; Build wird per `go:embed` ins Binary eingebettet → ein Artefakt. |
| Container | Multi-Stage-Build, Laufzeit-Image Alpine (wegen par2) | `PUID`/`PGID`, Volumes `/config`, `/data`. |

**Warum nicht …**

- *Python* (wie SABnzbd): bräuchte für yEnc/Netzwerk-Durchsatz C-Erweiterungen; Packaging ist schwerer.
- *Node/TypeScript im Backend*: machbar, aber CPU-lastiges Dekodieren/Entpacken passt schlechter zum Event-Loop.
- *Rust*: maximale Performance, aber deutlich langsamere Entwicklung für ein Projekt mit viel Integrations-Code.
- *C#/.NET* (wie Sonarr/Radarr): gute Alternative, aber größere Images und mehr Laufzeit-Overhead als Go.

---

## 3. Architektur

Modularer Monolith: ein Prozess, klar getrennte Pakete, Kommunikation über einen internen Event-Bus.
Alle externen Systeme sind *Provider* hinter Go-Interfaces, registriert über eine Registry
(`registry.Register("newznab", newznabFactory)`). Neue Indexer/Medienserver = neues Paket, das ein Interface implementiert.

```
                         ┌──────────────────────── Web-UI (React) ───────────────────────┐
                         │  Suche · Bibliothek · Downloads · Einstellungen · Benutzer    │
                         └──────────────────────────────┬────────────────────────────────┘
                                                        │ REST + SSE
┌───────────────┐   ┌───────────────────────────────────▼──────────────────────────────────┐
│ Eingangskanäle│──▶│                         Core / Domain                                │
│ UI, API,      │   │  Requests · Movies · Series · Episodes · Lifecycle-Engine · Scheduler │
│ Watchlists,   │   └──┬───────────┬────────────┬──────────────┬─────────────┬─────────────┘
│ Bots, *arr-API│      │           │            │              │             │
└───────────────┘      ▼           ▼            ▼              ▼             ▼
               ┌──────────┐ ┌───────────┐ ┌──────────────┐ ┌──────────┐ ┌──────────────┐
               │ Metadata │ │ Indexer   │ │ Decision     │ │ Download │ │ Media Server │
               │ TMDB,TVDB│ │ Newznab,  │ │ Engine       │ │ Client   │ │ Plex,        │
               │          │ │ Prowlarr  │ │ Parser,Profil│ │ intern / │ │ Jellyfin,    │
               └──────────┘ └───────────┘ │ Scoring      │ │ SAB/NZBG │ │ Emby         │
                                          └──────────────┘ └────┬─────┘ └──────┬───────┘
                                                                │              │ Watch-Events
                                              ┌─────────────────▼───┐          ▼
                                              │ Interner Downloader │   Watch-Tracker
                                              │ NZB→NNTP→yEnc→      │   (Webhook/WS/Polling)
                                              │ Stream-Unpack→PAR2  │
                                              └─────────────────────┘
```

### 3.1 Paketstruktur (Vorschlag)

```
cmd/autodownloadarr/        main, Wiring
internal/
  config/                   Env + config-Datei, Defaults
  db/                       Migrationen, sqlc-Queries, Repositories
  auth/                     Benutzer, Sessions, API-Keys, (später OIDC)
  domain/                   Entitäten + Zustandsautomaten
  events/                   interner Event-Bus
  scheduler/                persistente Jobs/Timer (Löschfristen, Suchen, Polling)
  lifecycle/                Regeln: Film-Löschung, Serien-Fenster, Speicherplatz
  metadata/        (iface)  tmdb/, tvdb/
  indexer/         (iface)  newznab/, prowlarr/
  release/                  Release-Namen-Parser, Qualitätsprofile, Scoring
  downloadclient/  (iface)  internal/, sabnzbd/, nzbget/
  usenet/                   nzb/, nntp/ (Pool), yenc/, assembler/, unpack/, par2/, deobfuscate/
  password/        (iface)  nzbmeta/, jobname/, release/, list/, subject/, nfo/, archivecomment/, manual/
  mediaserver/     (iface)  plex/, jellyfin/, emby/
  notify/          (iface)  telegram/, discord/, ntfy/, webhook/
  inputs/          (iface)  plexwatchlist/, trakt/, arrapi/ (Overseerr-kompatibel), telegrambot/
  library/                  Dateibenennung, Pfade, sicheres Löschen
  api/                      HTTP-Handler, SSE
web/                        React-Frontend (wird eingebettet)
```

### 3.2 Zentrale Schnittstellen (Skizze)

```go
type MetadataProvider interface {
    SearchMovies(ctx context.Context, q string) ([]MovieMeta, error)
    SearchSeries(ctx context.Context, q string) ([]SeriesMeta, error)
    Movie(ctx context.Context, id ExternalIDs) (MovieMeta, error)
    Series(ctx context.Context, id ExternalIDs) (SeriesMeta, []SeasonMeta, error) // inkl. Episoden + Ausstrahlungsdaten
}

type Indexer interface {
    Name() string
    Capabilities(ctx context.Context) (Caps, error)        // unterstützte Suchtypen/IDs/Kategorien
    Search(ctx context.Context, q SearchQuery) ([]Release, error) // Movie(imdb/tmdb) | Episode(tvdb, S, E) | Season | Freitext
    FetchNZB(ctx context.Context, r Release) (io.ReadCloser, error)
}

type DownloadClient interface {
    Add(ctx context.Context, nzb io.Reader, job JobSpec) (JobID, error)
    Status(ctx context.Context, id JobID) (JobStatus, error)
    Remove(ctx context.Context, id JobID, deleteData bool) error
    Events() <-chan DownloadEvent                           // Fortschritt, Fertig, Fehler
}

type MediaServer interface { // alle Pfade hier sind Medienserver-Pfade; Umrechnung macht der PathMapper
    Name() string
    Test(ctx context.Context) error
    Libraries(ctx context.Context) ([]Library, error)
    NotifyAdded(ctx context.Context, path string) error     // gezielter Teil-Scan
    NotifyRemoved(ctx context.Context, path string) error
    ResolveItem(ctx context.Context, path string) (ItemRef, error)
    WatchState(ctx context.Context, item ItemRef, users []string) (WatchState, error)
    Subscribe(ctx context.Context) (<-chan PlaybackEvent, error) // Start/Progress/Stop/Watched – Webhook, WS oder Polling
}

type PasswordSource interface { // siehe Abschnitt 7
    Name() string
    Candidates(ctx context.Context, pc PasswordContext) ([]PasswordCandidate, error)
}

type Notifier interface { Send(ctx context.Context, n Notification) error }

type InputSource interface { // Watchlist o. ä., wird periodisch abgefragt
    Name() string
    Poll(ctx context.Context) ([]MediaRequest, error)
}
```

---

## 4. Datenmodell (Kern)

| Tabelle | Wichtige Felder |
|---|---|
| `users` | id, name, password_hash (argon2id), role (admin/user), media_server_user_ids |
| `quality_profiles` | name, max_resolution, min_resolution, allowed_sources, codecs, required_words, forbidden_words, preferred_words (+Score), min/max MB pro Minute, prefer_proper, password_policy |
| `movies` | tmdb_id, imdb_id, title, year, profile_id, requested_by, state, keep_flag, delete_after, attempts, next_search_at, last_error |
| `series` | tvdb_id, tmdb_id, title, profile_id, requested_by, window_size, season_selection, keep_flag |
| `episodes` | series_id, season, episode, air_date, state, file_id, watched_at, attempts, next_search_at, last_error |
| `releases` | Kandidaten pro Suche: indexer, guid, title, size, parsed_attrs, score, rejected_reason, fingerprint (Name+Größe), password_flag (Newznab), searched_at |
| `blocklist` | fingerprint, guid, media_item, Fehlerklasse, Grund (fehlende Artikel, Passwort unbekannt, Spam, Fake, falsche Sprache …), Zeitpunkt |
| `download_jobs` | release_id, client, state, progress, speed, error_kind, error, Pfade, last_progress_at, encrypted, password (verschlüsselt), password_source |
| `passwords` | Passwort (verschlüsselt), scope (global/indexer/gruppe), scope_ref, hits, last_hit_at, created_by |
| `attempts` | media_item, release_id, gestartet/beendet, Ergebnis, Fehlerklasse, Details – Historie aller Versuche |
| `files` | **jede von uns angelegte Datei**: Pfad, Größe, media_item, angelegt_am – Grundlage für sicheres Löschen |
| `scheduled_tasks` | type, ref, run_at, payload (persistente Timer, überleben Neustarts) |
| `events_log` | Historie/Audit für UI („Gelöscht, weil …“) |
| `providers` | Konfiguration je Indexer/Server/Medienserver/Notifier (Typ + JSON-Settings, Secrets verschlüsselt) |
| `path_mappings` | provider_id, local_path, remote_path, remote_os (posix/windows) – Pfad-Mapping je Medienserver/Client |

---

## 5. Abläufe

### 5.1 Zustandsautomat Medienelement (Film bzw. Episode)

```
Requested ─▶ Searching ─▶ Downloading ─▶ Unpacking/Verifying ─▶ Available ─▶ Watching ─▶ Watched ─▶ PendingDelete ─▶ Deleted
    ▲            │             │                  │
    │            ▼             └──── Fehler ──────┴──▶ Failed ─▶ (Release blocklisten, nächsten Kandidaten) ─▶ Searching
    │       NotFound ─▶ (Wiederholung nach Zeitplan, z. B. noch nicht ausgestrahlt)
    └────────────── „Erneut laden“ aus Deleted
```

Zusätzlicher Zwischenzustand `PasswordRequired` (nur im Passwort-Modus „warten“, Abschnitt 7.3): Downloading ⇄ PasswordRequired.

### 5.2 Film

1. Anfrage (UI/Watchlist/API) → Metadaten von TMDB.
2. Vorab-Check: Liegt der Film bereits im Medienserver (z. B. über Radarr)? → Hinweis statt Download.
3. Suche bei allen aktiven Indexern (ID-basiert, Fallback Freitext) → Parser → Profil filtert → Scoring → Kandidatenliste.
4. Bester Kandidat → optional Verfügbarkeits-Check (NNTP `STAT` auf Stichprobe der Artikel) → Download.
5. Stream-Entpacken direkt in den Zielordner unter dem endgültigen Namen; nach den ersten 32 MB `NotifyAdded` (schaubar während des Downloads), nach Abschluss erneut.
6. Watch-Tracker meldet „gesehen“ → Löschzeitpunkt = jetzt + `movie_delete_after` (Standard 48 h) → Benachrichtigung „wird am … gelöscht“.
7. Timer läuft ab → Dateien löschen (nur aus `files`-Tabelle), `NotifyRemoved`, Status `Deleted`.

Zusätzlich: **Ablauffrist** – nicht angesehene Filme werden nach N Tagen gelöscht (z. B. 14, konfigurierbar, abschaltbar).
**„Behalten“-Schalter** pausiert alle Löschregeln.

### 5.3 Serien – rollendes Fenster

Einstellungen pro Serie (Default aus globalen Einstellungen):

- **Staffelauswahl**: einzelne Staffel(n), alle, oder „ab S02E05“.
- **Fenstergröße X** (Default 3): Anzahl bereitliegender, noch nicht gesehener Folgen.
- **Löschauslöser** (schützt vor versehentlichem Anklicken der nächsten Folge):
  - *Fortschritt* (Default): Folge N wird gelöscht, sobald von Folge N+1 mindestens **10 %** gesehen wurden.
  - *Wartezeit*: Folge N wird **Y Minuten** (z. B. 15) nach dem Start von N+1 gelöscht, wenn N+1 noch läuft bzw. weitergeschaut wurde.
  - *Gesehen-Status*: Folge N wird gelöscht, sobald der Medienserver sie als gesehen markiert (+ optionale Karenzzeit).
  - Fortschritt und Wartezeit sind kombinierbar („was zuerst eintritt“); Prozentwert und Minuten sind konfigurierbar.
- **Staffelgrenze**: am Staffelende automatisch mit der nächsten ausgewählten Staffel weitermachen.
- **Laufende Serie**: Folgen, die noch nicht ausgestrahlt sind, werden nach Air-Date (+ Puffer) gesucht.

Logik (läuft bei jedem Playback-Event und periodisch):

```
position  = letzte Folge, die gestartet/gesehen wurde (pro getracktem Benutzer)
behalten  = Folgen ab position, max. X Stück voraus, plus die aktuell laufende
löschen   = alle verfügbaren Folgen vor position, deren Löschauslöser erfüllt ist
nachladen = nächste fehlende Folgen in Reihenfolge, bis X unverbrauchte Folgen verfügbar/im Download sind
```

Beispiel X = 3: Start → E1, E2, E3 geladen. Benutzer startet E2 → E4 wird sofort geladen; sobald 10 % von E2 gesehen sind, wird E1 gelöscht.
(Nachladen schon beim Start, Löschen erst beim Auslöser – so ist der Puffer nie kleiner als X.)

Sonderfälle: Benutzer springt vor (alle übersprungenen Folgen gelten als verbraucht? → Einstellung), Rewatch (keine erneute Löschung bei Keep), Doppelfolgen (S01E01E02 als eine Datei), Staffelpakete (wenn Einzelfolgen nicht auffindbar: Staffelpaket laden, nur die Fensterfolgen entpacken – bei Stream-Entpacken möglich, weil wir gezielt Dateien aus dem Archiv extrahieren).

### 5.4 Watch-Tracking

| Medienserver | Primär | Fallback |
|---|---|---|
| Plex | WebSocket-Notifications des Servers (`/:/websockets/notifications`, Playing-Events, kein Plex Pass nötig) | Polling `/status/sessions` + `viewCount` der Items; Webhooks (Plex Pass) optional |
| Jellyfin | WebSocket/Sessions-API | Polling `UserData.Played`; Webhook-Plugin optional |
| Emby | wie Jellyfin (gemeinsame Basis-Implementierung) | – |

Konfigurierbar: **welche Benutzer des Medienservers zählen** (einer, mehrere = „alle müssen gesehen haben“ oder „einer reicht“).
Default ist der Ein-Personen-Fall (Besitzer des Plex-Servers, wird beim Einrichten automatisch erkannt); Mehrbenutzer ist im Datenmodell von Anfang an vorgesehen, weil das Projekt Open Source wird.
Gesehen-Schwelle: Medienserver-Status oder eigener Wert (z. B. ≥ 90 % Fortschritt).

---

## 6. Interner Downloader (Kernstück)

```
NZB ─▶ Parser ─▶ Dateiliste (Volumes, PAR2, Sonstiges) ─▶ Priorisierung
                                         │
            ┌────────────────────────────▼─────────────────────────────┐
            │ NNTP-Pool: mehrere Server, Priorität, N Verbindungen/Srv │
            │ TLS, AUTH, Pipelining, Retry, Fallback auf Backup-Server │
            └────────────────────────────┬─────────────────────────────┘
                                         ▼
                           yEnc-Decode + CRC32 pro Segment
                                         ▼
                   Assembler: Segmente an Offset in Volume-Datei schreiben,
                   „verfügbar bis Byte n“ je Volume nachhalten
                                         ▼
           Stream-Unpacker liest Volumes sequenziell (blockiert, bis Daten da sind)
                                         ▼
                    Zieldatei im Medienordner (endgültiger Name, wächst)
```

**Entpacken während des Ladens**

- **Umgesetzt in Phase 1.** Volumes werden in Archivreihenfolge geladen (PAR2-Index zuerst, dann `part1, part2, …` bzw. `.rar, .r00, …`, dann der Rest).
- Der laufende Download stellt seine Dateien als Dateisystem (`fs.FS`) bereit. Lesezugriffe blockieren, bis die Bytes zusammenhängend auf der Platte liegen; `rardecode` liest darüber und schreibt die Zieldatei, während weitere Volumes noch laden. Ist das letzte Volume da, ist der Film praktisch sofort fertig.
- Das Video wird gleich unter seinem endgültigen Namen geschrieben. Sobald die ersten 32 MB auf der Platte liegen (Container-Kopf mit Laufzeit und Codecs), bekommt der Medienserver einen Teil-Scan – der Titel ist schaubar, während der Rest noch lädt. Nach Abschluss folgt ein zweiter Scan. Andere Dateien (Extras, Samples werden gar nicht entpackt) heißen beim Schreiben `name.partial`. Scheitert der Versuch, wird die Datei gelöscht und erneut gescannt.
- Fehlt ein Artikel endgültig, bricht das Stream-Entpacken mit „Daten fehlen“ ab statt zu hängen (Rückfall auf Reparatur siehe unten).
- Sonderfall *Store-Modus* (RAR m0, bei Scene-Releases üblich): Nutzdaten liegen unkomprimiert im Archiv → wir könnten Segmente direkt an den richtigen Offset der Zieldatei schreiben und die RAR-Volumes gar nicht erst speichern. Spart Plattenplatz und I/O. (Optimierung für später.)
- Nach dem Entpacken: Prüfung über CRC aus dem RAR-Header bzw. PAR2-Hashes.

**Schauen, bevor der Download fertig ist (Variante A umgesetzt)**

Damit man schon *während* des Downloads schauen kann, gibt es zwei Wege. Umgesetzt ist A (wachsende Datei, siehe oben); B bleibt eine Option für später, falls Spulen in noch nicht geladene Teile wichtig wird:

| | A: Wachsende Datei | B: Virtuelles Dateisystem |
|---|---|---|
| Idee | Die entpackte Datei erscheint sofort im Medienserver und wächst | Ein Laufwerk (WebDAV/FUSE) zeigt die *vollständige* Datei sofort; Lesezugriffe von Plex laden die nötigen Artikel gezielt nach (mit Vorausladen) |
| Spulen | Nur in bereits geladene Teile | Überallhin, fehlende Stellen werden sofort nachgeladen |
| Voraussetzung | keine | RAR im Store-Modus (üblich) oder Datei ohne Archiv; Plex muss das Laufwerk einbinden (FUSE im Container oder WebDAV-Mount auf dem Plex-Host) |
| Risiko | Plex liest Laufzeit/Index (MKV-Cues am Dateiende) beim ersten Scan falsch; Wiedergabe stoppt, wenn sie den Download einholt | Deutlich mehr Aufwand; Einrichtung des Mounts für Nutzer |
| Aufwand | gering | hoch (eigene Phase) |

Empfehlung: zuerst A als Option ausprobieren (wenig Aufwand, zeigt die Grenzen von Plex), B als eigene spätere Phase, falls A nicht reicht.

**Fehlerfälle** (Gesamtkonzept siehe Abschnitt 9)

- Fehlende Artikel (430) auf allen Servern → Stream-Entpacken pausiert, PAR2-Recovery-Blöcke werden nachgeladen (nur so viele wie nötig), `par2` repariert, dann klassisch entpacken.
- Verschlüsseltes Archiv → Passwortsuche (Abschnitt 7); erst wenn kein Passwort passt, gilt die Quelle als unbrauchbar.
- Nicht reparierbar, Fake (z. B. `.exe`, falsche Größe/Laufzeit) → Release auf Blocklist, nächster Kandidat automatisch.
- **Verfügbarkeits-Check vorab** per `STAT` reduziert Fehlstarts.
- **Deobfuskation**: zufällige Dateinamen über PAR2-Metadaten (Originalnamen + MD5 der ersten 16 KiB) und Datei-Signaturen auflösen.
- Nicht-RAR: 7z/zip (Pure-Go-Bibliotheken), direkte `.mkv`/`.mp4` ohne Archiv.
- Samples, `.nfo`, `.exe` etc. werden verworfen.

**Weitere Downloader-Funktionen**: Bandbreitenlimit (mit Zeitplan), globale/parallele Job-Limits, Priorität („jetzt schauen“ vor Hintergrund-Nachladen), Speicherplatz-Wächter (Pausieren bei < Mindest-Freiplatz), Artikel-Cache im RAM.

**Austauschbarkeit**: Da der Downloader hinter `DownloadClient` liegt, können SABnzbd/NZBGet als alternative Clients angebunden werden (ohne Stream-Entpacken). Nützlich zum Vergleichen und als Rückfalloption.

---

## 7. Passwortgeschützte Releases

Gerade bei deutschen Releases sind verschlüsselte Archive häufig. **Passwortgeschützt heißt nicht defekt**: Eine Quelle gilt
erst dann als unbrauchbar, wenn aus keiner Quelle ein passendes Passwort gefunden wurde.

### 7.1 Passwortquellen

Jede Quelle ist ein eigenes Modul hinter einer Schnittstelle (`PasswordSource`). Alle Quellen liefern Kandidaten,
die zusammengeführt, entdoppelt und in dieser Reihenfolge getestet werden. Jeder Kandidat merkt sich seine Herkunft (für UI und Statistik).

| # | Quelle | Beispiele | Phase |
|---|---|---|---|
| 1 | **NZB-Metadaten** | `<head><meta type="password">geheim</meta></head>` (auch mehrere `password`-Einträge) | 2 |
| 2 | **NZB-Dateiname / Jobname** | `Film.2024.German.720p.WEB.x264{{geheim}}.nzb`, SABnzbd-Schreibweise `Jobname / geheim`, `password=geheim` – gilt für Upload, API und Indexer-Downloads (`Content-Disposition`) | 2 |
| 3 | **Release-Titel und Indexer-Daten** | `{{geheim}}` im Titel, Newznab-Attribute/Beschreibung/Kommentar, Standardpasswort pro Indexer | 2 |
| 4 | **Passwortliste** | global, pro Indexer, pro Release-Gruppe; sortiert nach Erfolgsquote („gelernte“ Passwörter) | 2 einfach, 3 mit Gruppen + Statistik |
| 5 | **Manuelle Eingabe** | Web-UI, API, später Antwort an den Bot | 2 |
| 6 | **Betreffzeilen / Dateinamen in der NZB** | `[PW: geheim]`, `Passwort - geheim`, `password.txt` als Dateiname | 3 |
| 7 | **Begleitdateien im Post** | `.nfo`, `.txt` werden (weil klein) zuerst geladen und nach Passwort-Mustern durchsucht | 3 |
| 8 | **Archiv-Kommentar** | RAR-Kommentar, lesbar solange die Header nicht verschlüsselt sind | 3 |

Die Suchmuster sind konfigurierbare reguläre Ausdrücke (Default u. a. `pw`, `pass`, `passwort`, `password`, `kennwort`, `{{…}}`),
damit Nutzer neue Konventionen ohne Code-Änderung ergänzen können. Gefundene Werte werden bereinigt (Leerzeichen, Anführungszeichen, Klammern).

```go
type PasswordSource interface {
    Name() string
    // ctx enthält NZB, Release, Indexer und bereits geladene Begleitdateien
    Candidates(ctx context.Context, pc PasswordContext) ([]PasswordCandidate, error)
}
```

### 7.2 Erkennen und Prüfen

- **Früh erkennen**: Das erste Volume wird zuerst geladen; aus den ersten Segmenten wird der Archiv-Header gelesen.
  - Nur Daten verschlüsselt → Dateiliste ist sichtbar, Fake-Prüfung (`.exe`, falsche Größe) ist sofort möglich.
  - Header verschlüsselt (`rar -hp`) → auch die Dateiliste erst mit Passwort.
- **Testen, bevor viel geladen wird**:
  - RAR5 enthält einen Passwort-Prüfwert → Kandidaten lassen sich in Millisekunden prüfen, ohne Daten zu entschlüsseln.
  - RAR3/4, 7z-AES, ZIP-AES: mit dem Kandidaten den ersten Datenblock entschlüsseln und gegen CRC bzw. Container-Signatur (MKV/MP4-Header) prüfen.
- **Treffer** → Stream-Entpacken läuft mit Passwort normal weiter; Passwort und Herkunft werden zum Job gespeichert, Erfolgsquote der Liste wird hochgezählt.
- **Verschachtelte Archive**: Innere Archive durchlaufen dieselbe Passwortsuche (zuletzt erfolgreiches Passwort zuerst).
- **Grenzen**: höchstens N Kandidaten aus der Liste pro Release (z. B. 50), kein Brute Force.
- **Spam erkennen**: Verweist die NFO/der Dateiname für das Passwort auf eine Webseite („Passwort auf www…“), gilt das Release als Spam → Blocklist mit eigenem Grund, nicht als „Passwort unbekannt“.

### 7.3 Wenn kein Passwort passt

```
Verschlüsselt erkannt ─▶ Kandidaten aus allen Quellen testen
   ├─ Treffer ─────────▶ weiter laden + Stream-Entpacken
   └─ kein Treffer
        ├─ Modus „automatisch“ (Default): Release blocklisten (Grund „Passwort unbekannt“) ─▶ nächster Kandidat.
        │      Das Release bleibt unter „Passwort benötigt“ sichtbar, falls man das Passwort später nachreicht.
        └─ Modus „warten“: Download pausiert, Status „Passwort benötigt“, Benachrichtigung.
               Nach Zeit T (z. B. 30 min) ohne Eingabe ─▶ wie „automatisch“.

Manuelle Eingabe ─▶ wird sofort geprüft; bereits geladene Daten werden weiterverwendet,
                    optional „Passwort zur Liste hinzufügen“ (global / Indexer / Gruppe).
```

### 7.4 Berücksichtigung bei der Release-Auswahl

- Das Newznab-Attribut `password` wird ausgewertet (0 = nein, 1 = passwortgeschützt, 2 = enthält inneres Archiv; je nach Indexer unterschiedlich gut gepflegt).
- Profiloption „Passwortgeschützte Releases“: *erlauben* · *nur wenn Passwort bekannt* · *vermeiden* (Score-Abzug) · *ablehnen*.
- Ist das Passwort bereits aus NZB oder Titel bekannt, gibt es keinen Abzug.

### 7.5 Sicherheit

- Passwortliste und Job-Passwörter werden wie andere Secrets verschlüsselt in der DB gespeichert und in Logs maskiert.
- Passwörter verlassen das System nie (keine Abfrage externer Dienste).
- Import/Export der Passwortliste als Textdatei (eine Zeile pro Passwort, optional mit Gültigkeitsbereich).

---

## 8. Release-Auswahl

**Parser** (eigenes Paket, Tabellen-getestet mit vielen echten Release-Namen):
Titel, Jahr, Staffel/Episode(n), Auflösung, Quelle (WEB-DL, WEBRip, BluRay, HDTV, …), Codec, HDR, Audio, Sprache(n) (`German`, `DL`, `ML`, …), Gruppe, PROPER/REPACK, Edition.

**Qualitätsprofil**

- **Maximale Auflösung** (z. B. 720p) – harte Grenze; innerhalb davon gewinnt die höchste.
- Minimale Auflösung (optional).
- **Required Words** (alle/einer muss passen, Regex möglich) – z. B. `German`.
- **Forbidden Words** – z. B. `CAM`, `TS`, `HDCAM`, `Subbed`, `LD`.
- Preferred Words mit Punktwerten (z. B. `DL` +10, bestimmte Gruppen +5).
- Größenlimits pro Laufzeitminute (filtert Fakes und riesige Remuxe).
- Bevorzugter Codec (x265 spart Platz bei temporären Inhalten).
- Mindestalter des Releases / Mindest-Grabs (optional, gegen Fakes).
- Umgang mit passwortgeschützten Releases: erlauben / nur mit bekanntem Passwort / vermeiden / ablehnen (Abschnitt 7.4).

**Scoring**: Auflösung (≤ max) > Quelle > Preferred-Words > Größe-nahe-Ziel > Alter/Grabs. Abgelehnte Kandidaten werden mit Grund gespeichert und in der UI angezeigt („warum wurde nichts gefunden?“). Manuelle Auswahl eines Releases aus der Liste ist möglich.

---

## 9. Fehlerbehandlung und automatischer Quellenwechsel

Grundsatz: Jeder Fehler wird **klassifiziert**, und die Klasse bestimmt die Reaktion. Eingreifen muss der Benutzer nur,
wenn wirklich keine Quelle mehr zu finden ist oder die Konfiguration falsch ist. Jeder Versuch ist mit Grund in der UI sichtbar.

### 9.1 Fehlerklassen

| Klasse | Beispiele | Reaktion |
|---|---|---|
| **Quelle defekt** (betrifft nur dieses Release) | Artikel fehlen (DMCA/Takedown, Retention), PAR2 reicht nicht, Passwort unbekannt (nach erfolgloser Passwortsuche, Abschnitt 7), Spam, Fake/Malware (`.exe`, `.lnk`), Entpacken/CRC schlägt fehl, Laufzeit oder Sprache passt nicht | Release auf Blocklist → **nächster Kandidat** |
| **Vorübergehend** (Infrastruktur) | Indexer-Timeout/5xx, API-Limit erreicht, NNTP-Verbindungsabbruch, „too many connections“, Medienserver offline | Wiederholen mit exponentiellem Backoff + Jitter, Job wird *pausiert*, nicht als fehlgeschlagen gewertet; Circuit Breaker pro Anbieter |
| **Konfiguration** | Falsche Usenet-Zugangsdaten (NNTP 481/502), ungültiger API-Key, Pfad nicht beschreibbar | Sofort anhalten, keine Wiederholungsschleife; Warnung in der UI + Benachrichtigung |
| **Ressourcen** | Platte voll, Mindest-Freiplatz unterschritten | Downloads pausieren, Speicherplatz-Regeln anwenden, automatisch fortsetzen |
| **Noch nicht verfügbar** | Keine passenden Releases, Film noch nicht als Digital/BluRay erschienen, Folge nicht ausgestrahlt | Status „Wartet“, Neusuche nach Zeitplan; Erscheinungsdaten von TMDB bestimmen den frühesten Suchzeitpunkt |

Im Code: eigene Fehlertypen mit Klasse (`errors.Is(err, ErrSourceBroken)`, `ErrTransient`, `ErrConfig`, …), die jede Schicht
(Indexer, NNTP, Unpacker, Medienserver) setzt. Die Lifecycle-Engine entscheidet ausschließlich anhand der Klasse.

### 9.2 Ablauf des Quellenwechsels

```
Suche ─▶ Kandidatenliste (nach Score sortiert, in DB gespeichert)
   │
   ▼
Kandidat k ─▶ Vorabprüfung: NZB laden · Dateitypen/Größe plausibel · STAT-Stichprobe der Artikel
   │   └─ fehlgeschlagen ─▶ blocklisten ─▶ Kandidat k+1
   ▼
Download + Stream-Entpacken
   │   ├─ fehlende Artikel > PAR2-Reserve (laufend berechnet) ─▶ sofort abbrechen ─▶ blocklisten ─▶ k+1
   │   ├─ fehlende Artikel ≤ PAR2-Reserve ─▶ reparieren ─▶ weiter
   │   ├─ verschlüsselt ─▶ Passwortsuche (Abschnitt 7) ─▶ Treffer: weiter · kein Treffer: blocklisten ─▶ k+1
   │   └─ Fake / Spam / Entpackfehler ─▶ blocklisten ─▶ k+1
   ▼
Prüfung vor Freigabe (ffprobe): Laufzeit ≈ TMDB, Video- und Audiospur vorhanden, Audiosprache passt
   │   └─ fehlgeschlagen ─▶ Datei löschen ─▶ blocklisten ─▶ k+1
   ▼
Available

Kandidaten erschöpft ─▶ Neusuche: alle Indexer, frische Ergebnisse, Freitext-Fallback mit alternativen Titeln
                        (deutscher Titel, Originaltitel, Aliase von TMDB)
Weiterhin nichts     ─▶ Status „Nicht gefunden“, Neusuche mit Backoff (1 h, 6 h, 24 h, danach täglich),
                        einmalige Benachrichtigung
```

**Details**

- **Kandidatenliste wird gespeichert**: Der Wechsel zur nächsten Quelle braucht keine neue Indexer-Abfrage (schont API-Limits) und passiert in Sekunden. Die Liste ist N Stunden gültig, danach wird neu gesucht.
- **Duplikate erkennen**: Dasselbe Release taucht oft bei mehreren Indexern auf. Ein Fingerprint aus normalisiertem Namen + Größe sorgt dafür, dass ein defektes Release überall als defekt gilt.
- **Fail-Fast**: Fehlende Artikel werden laufend gegen die verfügbaren PAR2-Blöcke gerechnet. Ist klar, dass die Reparatur nicht reicht, wird sofort abgebrochen statt erst nach dem kompletten Download.
- **Versuchslimit**: Nach z. B. 5 fehlgeschlagenen Quellen pausiert das Element und es kommt eine Benachrichtigung (verhindert Endlosschleifen bei systematischen Problemen, z. B. falscher Required Word).
- **Optionales Fallback-Profil**: Wenn im Profil nichts funktioniert, darf (wenn aktiviert) ein lockereres Profil verwendet werden, z. B. auch 480p. *Required Words werden nie gelockert.*
- **Aufräumen**: Teildaten, `.partial`-Dateien und Arbeitsordner eines fehlgeschlagenen Versuchs werden sofort gelöscht.
- **Serien**: Gleiche Logik pro Folge. Ist eine Einzelfolge nirgends verfügbar, wird ein Staffelpaket als Quelle versucht. Andere Folgen im Fenster laden unabhängig weiter.

### 9.3 Fehler, die erst beim Schauen auffallen

- Button **„Quelle defekt“** (mit Grund: falsche Sprache, schlechte Qualität, Bild-/Tonfehler, falscher Film) in der UI → Datei löschen, Release blocklisten, nächste Quelle wird automatisch geladen.
- Optional später: Plex-Wiedergabe bricht wiederholt früh ab → Hinweis in der UI („Quelle prüfen?“), keine automatische Löschung.

### 9.4 Robustheit des Systems

- **Persistenter Zustand**: Jeder Schritt wird in der DB festgehalten. Nach einem Neustart laufen Downloads weiter (Segment-Fortschritt gespeichert) oder werden sauber neu begonnen.
- **Idempotente Schritte**: Scan auslösen, Datei löschen, Benachrichtigung senden – mehrfaches Ausführen schadet nicht, daher gefahrlos wiederholbar.
- **Circuit Breaker** pro Indexer und Usenet-Server: nach N Fehlern für eine Zeit T deaktiviert, andere übernehmen; Zustand in der Health-Anzeige.
- **NNTP-Fallback pro Artikel**: Fehlt ein Artikel beim Hauptserver, wird er bei Backup-Servern (idealerweise anderes Backbone) angefragt, bevor er als fehlend gilt.
- **Watchdog**: Jobs ohne Fortschritt seit X Minuten werden abgebrochen und neu gestartet.
- **Medienserver offline**: Scans und Benachrichtigungen landen in einer Warteschlange und werden nachgeholt; Löschungen auf der Platte werden dadurch nicht blockiert.
- **Benachrichtigungen bündeln**: lieber „3 Quellen für *Film* fehlgeschlagen, nächste läuft“ als drei Einzelmeldungen.

### 9.5 Transparenz und manuelle Eingriffe

- Versuchs-Historie pro Film/Folge: Release, Indexer, Fehlerklasse, Grund, Dauer, Zeitpunkt.
- Aktionen: „Nächste Quelle“, „Neu suchen“, „Release manuell wählen“, „Passwort eingeben“, Blocklist-Eintrag entfernen, Element fortsetzen.
- Strukturiertes Logging (`log/slog`, JSON) mit Korrelations-ID pro Element und Versuch, damit Fehler in Issues nachvollziehbar sind.

---

## 10. Eingangskanäle („verschiedene Wege“)

| Kanal | Phase | Hinweis |
|---|---|---|
| Web-UI-Suche (TMDB) | MVP | Poster, Beschreibung, Staffelauswahl |
| REST-API mit API-Key | MVP | Basis für alles andere |
| Manueller NZB-Upload | MVP | Für Sonderfälle |
| Link einfügen (IMDb/TMDB/TVDB-URL) | 2 | Auch als Bookmarklet / Share-Ziel (PWA) |
| Plex-Watchlist (eigener + Freunde) | 3 | Auf jedem Gerät zur Watchlist → wird geladen; eigenes Tag/Filter möglich |
| Trakt-Watchlist/Listen | 4 | |
| Overseerr/Jellyseerr | später | Vorerst nicht geplant. Idee: Radarr/Sonarr-API teilweise emulieren, sodass Seerr AutoDownloadarr als „Radarr“ nutzen kann |
| Telegram-/Discord-Bot | 4 | `/film Dune` → Auswahl → fertig-Meldung |

---

## 11. Weitere Funktionen

- **Übergabe an Radarr/Sonarr**: „Doch behalten“ → Titel wird in Radarr/Sonarr angelegt, temporäre Kopie bleibt bis zum Import.
- **Duplikatschutz**: prüft Medienserver und optional Radarr/Sonarr, bevor geladen wird.
- **Speicherplatz-Management**: Maximalgröße der temporären Bibliothek; bei Überschreitung zuerst gesehene, dann älteste ungesehene löschen (mit Benachrichtigung).
- **Benachrichtigungen**: bereit, fehlgeschlagen, wird bald gelöscht, gelöscht (Telegram, Discord, ntfy, generischer Webhook).
- **Mehrbenutzer** (umgesetzt): Rollen Admin/User, Anfragen pro Benutzer, optionale Kontingente (Anzahl/GB). Die Zuordnung zu Medienserver-Benutzern folgt.
- **„Jetzt schauen“-Priorität**: Download mit höchster Priorität, Meldung sobald abspielbar.
- **Historie & Statistik**: Was wurde geladen/gelöscht und warum, verbrauchtes Datenvolumen.
- **Dashboard mit Live-Fortschritt** (SSE): Geschwindigkeit, ETA, Entpack-Status.
- **Untertitel** (optional, später): Abruf über OpenSubtitles.
- **Backup/Restore** der Konfiguration und DB aus der UI.
- **Health-Checks** in der UI: Indexer-API-Limits, Usenet-Login, Medienserver erreichbar, Pfade beschreibbar.

---

## 12. Sicherheit und Datenschutz

- Login mit argon2id-Hashes, Session-Cookies (`HttpOnly`, `SameSite`), CSRF-Schutz, Rate-Limit beim Login.
- Erster Start: Setup-Assistent legt Admin an (kein Standardpasswort). Dafür ist ein einmaliges **Setup-Token** nötig, das nur im Server-Log steht (Abschnitt 13.2).
- Passwort vergessen: `autodownloadarr reset-password <benutzer>` im Container.
- Optional: OIDC (Authentik, Authelia, Keycloak) bzw. Vertrauen auf Reverse-Proxy-Header; später Plex-Login.
- Secrets (Usenet-Passwort, API-Keys) verschlüsselt in der DB (Schlüssel aus Env/Datei in `/config`).
- **Sicheres Löschen**: Es wird nur gelöscht, was in `files` steht *und* unterhalb der konfigurierten temporären Medienordner liegt. Sonarr/Radarr-Ordner sind damit nie betroffen.
- Logs ohne Zugangsdaten.

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
      - ./config:/config            # DB, Einstellungen, Logs
      - /mnt/media/temp:/data       # Downloads (temporär) + temporäre Bibliotheken
    ports:
      - "8686:8686"
    restart: unless-stopped
```

- **Eigene Bibliotheken im Medienserver** empfohlen: z. B. `/data/library/movies` → Plex-Bibliothek „Temp Filme“, `/data/library/tv` → „Temp Serien“.
- **Pfad-Mapping** zwischen Container und Medienserver – siehe unten.
- Download-Arbeitsordner und Bibliothek auf **demselben Dateisystem** (atomares Umbenennen statt Kopieren).
- Benennung kompatibel zu Plex/Jellyfin: `Titel (Jahr) {tmdb-123}/Titel (Jahr).mkv`, `Serie (Jahr) {tvdb-456}/Season 01/Serie - S01E01 - Episodentitel.mkv`.
- Multi-Arch-Image (amd64, arm64), Healthcheck-Endpoint, Konfiguration über UI (Env nur für Bootstrap).

### 13.1 Pfad-Mapping (Container ↔ Medienserver)

AutoDownloadarr und der Medienserver sehen dieselben Dateien meist unter verschiedenen Pfaden, z. B.:

| AutoDownloadarr (im Container) | Plex-Server | Hinweis |
|---|---|---|
| `/filme/` | `/mnt/hdd/filme/` | Plex direkt auf dem Host |
| `/serien/` | `/mnt/hdd/serien/` | |
| `/filme/` | `/data/movies/` | Plex ebenfalls im Docker-Container |
| `/filme/` | `D:\Medien\Filme\` | Plex unter Windows |

- **Mapping-Tabelle pro Medienserver** (und später pro externem Download-Client und Radarr/Sonarr-Übergabe): beliebig viele Paare *lokaler Pfad ↔ entfernter Pfad*.
- **In beide Richtungen**:
  - lokal → entfernt: Scan auslösen (`NotifyAdded`/`NotifyRemoved`) mit dem Pfad, den Plex kennt.
  - entfernt → lokal: Plex meldet Wiedergabe/Gesehen mit seinem Dateipfad → daraus wird unser Film bzw. unsere Folge ermittelt.
- **Längster passender Präfix gewinnt** (z. B. `/filme/4k/` vor `/filme/`), Vergleich nur an Ordnergrenzen (`/filme` passt nicht auf `/filme-alt`).
- **Normalisierung**: abschließende Schrägstriche, `\` vs. `/`, Groß-/Kleinschreibung bei Windows-Servern, Unicode-Normalisierung (Umlaute).
- **Zentrale Komponente** `PathMapper`, die alle Medienserver-Adapter benutzen; die Adapter selbst arbeiten nur mit Medienserver-Pfaden.
- **Assistent in der UI**:
  - liest die Bibliotheksordner aus Plex (`/library/sections` liefert die `Location`-Pfade),
  - schlägt Mappings anhand gleicher Ordnernamen vor (`/filme` ↔ `/mnt/hdd/filme`),
  - zeigt eine Vorschau: „`/filme/Dune (2021)/Dune (2021).mkv` → `/mnt/hdd/filme/Dune (2021)/Dune (2021).mkv`“.
- **Prüfung**: Warnung, wenn ein Zielordner von AutoDownloadarr nach dem Mapping in keiner Plex-Bibliothek liegt, oder wenn kein Mapping greift. Optionaler Live-Test: Testordner anlegen, Teil-Scan auslösen, prüfen ob Plex ihn sieht, wieder entfernen.
- **Rückfall bei der Zuordnung**: Findet sich ein Plex-Element nicht über den Pfad, wird über die IDs (`tmdb://`, `tvdb://` in den Plex-GUIDs) zugeordnet – das schützt vor falsch konfigurierten Mappings.
- Ohne Mapping (gleiche Pfade auf beiden Seiten) funktioniert alles unverändert.

### 13.2 Einrichtungsassistent beim ersten Start

Beim ersten Start (noch kein Konto vorhanden) führt die Weboberfläche durch die komplette Einrichtung:

| Schritt | Inhalt | Prüfung | Verfügbar ab |
|---|---|---|---|
| 1. Konto | Setup-Token aus dem Container-Log, Benutzername, Passwort | Token muss stimmen (schützt frisch gestartete, öffentlich erreichbare Instanzen) | **Phase 0 (umgesetzt)** |
| 2. Medienserver | Anmeldung über Plex (PIN-Verfahren über plex.tv, kein Token-Kopieren), Server wählen, Bibliotheken für temporäre Filme/Serien wählen, Pfad-Mapping mit Vorschlägen (13.1) | Server erreichbar, Bibliotheken lesbar, Mapping-Vorschau | Phase 2 |
| 3. Indexer | Newznab-URL + API-Key, Kategorien | `t=caps`-Abfrage, unterstützte Such-IDs (IMDb/TMDB/TVDB) | Phase 2 |
| 4. Usenet | Server, Port, SSL, Benutzer, Passwort, Verbindungen; optional Backup-Server | Login-Test, Anzahl erlaubter Verbindungen | Phase 2 (Downloader aus Phase 1) |
| 5. Standards | Qualitätsprofil (max. Auflösung, Vorlage „Deutsch“ mit Required Words), Löschfrist Filme, Fenstergröße Serien | – | Phase 2 / 4 |
| 6. Fertig | Zusammenfassung, offene Punkte, Link zur Übersicht | – | Phase 0 |

- Jeder Schritt wird **vor dem Speichern getestet**; Fehler werden konkret angezeigt („Login abgelehnt“, „Host nicht erreichbar“).
- Alle Schritte außer dem Konto sind **überspringbar**; fehlende Teile erscheinen danach als Hinweis in der Übersicht.
- Der **Fortschritt wird gespeichert**: Wer den Browser schließt, landet nach der Anmeldung wieder beim nächsten offenen Schritt. (Kommt mit Phase 2, sobald es Schritte mit gespeicherten Einstellungen gibt.)
- Der Assistent lässt sich später aus den Einstellungen **erneut starten**; dieselben Formulare stehen dort auch einzeln zur Verfügung.
- Stand Phase 0: Schritt 1 und 6 funktionieren, Schritte 2–4 sind als Vorschau sichtbar und können übersprungen werden.

---

## 14. Roadmap

| Phase | Inhalt | Ergebnis |
|---|---|---|
| **0 – Fundament** ✅ | Repo, Go-Modul, Config, SQLite+Migrationen, Event-Bus, Scheduler, Auth + Einrichtungsassistent (Konto-Schritt), React-Gerüst mit i18n (en/de), Dockerfile, CI (Tests, Lint, Image-Build) | Leere App mit Login läuft im Container |
| **1 – Downloader** ✅ | NZB-Parser, NNTP-Pool (TLS, Auth, mehrere Server mit Priorität, Retry), yEnc, Assembler, **Entpacken während des Downloads** (RAR, mehrteilig, Passwort), CLI-Befehl `download-nzb`, Test-NNTP-Server, Benchmarks | Beliebige NZB lässt sich zuverlässig und schnell laden; der Film ist fertig, sobald das letzte Volume da ist |
| **2 – MVP Filme** ✅ | TMDB, Newznab (ID-Suche mit Textsuche als Rückfall), Release-Parser, Qualitätsprofil (max./min. Auflösung, Pflicht-/Verbots-/Bonuswörter, MB pro Minute, Codec, Passwort-Regel), Plex (Anmeldung per PIN, Bibliothek, Pfad-Mapping mit Vorschlägen, Teil-Scan, Gesehen-Status per Abfrage), Lösch- und Ablauffristen, Fehlerklassen, Kandidatenliste, Sperrliste, automatischer Quellenwechsel, Neusuche mit Backoff, Passwörter aus NZB-Metadaten, NZB-/Jobname, Release-Titel und Passwortliste, verschlüsselte Zugangsdaten, Einrichtungsassistent mit allen Schritten, UI: Filmsuche, Bibliothek, Detailseite mit Releases/Verlauf/Sperrliste, Live-Fortschritt, Einstellungen | Kompletter Film-Kreislauf funktioniert, nicht verfügbare Quellen werden automatisch ersetzt |
| **3 – Robuster Downloader** ✅ | PAR2-Reparatur als Rückfall (nur benötigte Recovery-Blöcke laden, Abbruch sobald mehr fehlt als reparierbar), 7z/ZIP (7-Zip im Image), `STAT`-Vorabprüfung einer Stichprobe, Wiederherstellen verschleierter Namen per PAR2, Passwörter aus Betreffzeilen und NFO-Dateien, Spam-Erkennung („Passwort auf Webseite“), Watchdog für hängende Downloads, Indexer-Pause nach wiederholten Fehlern, Bandbreitenlimit | Entpacken während des Ladens, Reparatur nur wenn nötig, wenig manuelle Eingriffe |
| **4 – Serien** ✅ | TMDB-Serien (Staffeln, Folgen, Ausstrahlungstermine), Newznab-Folgensuche (TVDB/IMDb-ID, sonst Text), Staffel/Folge im Release-Namen, Staffelauswahl, rollendes Fenster mit Löschauslöser (Fortschritt der nächsten Folge, Minuten nach Start, gesehen), Suche ab Ausstrahlung, tägliche Aktualisierung der Folgenliste, Plex-Serienbibliothek mit Wiedergabe-Fortschritt, gemeinsamer Download-Kern mit den Filmen, Staffelpakete als Ausweichquelle (nur wenn keine Einzelfolge auffindbar), UI für Serien | Serien-Kreislauf funktioniert |
| **5 – Integrationen** ✅ | Jellyfin/Emby, Benachrichtigungen, Plex-Watchlist, Benutzerverwaltung mit Rollen Admin/User und optionalen Kontingenten je Benutzer, Speicherplatz-Management (Radarr/Sonarr bewusst verworfen) | Alltagstauglich |
| **6 – Komfort** | Trakt, Bots, OIDC, Statistik, Medienserver-Benutzerzuordnung, Store-Modus-Direktschreiben | Feinschliff |
| später | Overseerr/Jellyseerr-kompatible API, weitere Indexer-Typen, Torrent-Client | nach Bedarf |

---

## 15. Risiken und offene Punkte

| Risiko | Gegenmaßnahme |
|---|---|
| Stream-Entpacken bei fehlenden Artikeln | Optimistisch streamen, bei Fehler auf „Laden → PAR2 → Entpacken“ zurückfallen |
| Medienserver erkennt wachsende Dateien schlecht | Erster Scan erst nach 32 MB (Container-Kopf vollständig), zweiter Scan nach Abschluss; Spulen nur in bereits geladene Teile (MKV-Index am Dateiende) |
| Plex behält den Gesehen-Status eines Titels über das Löschen hinaus | Beim ersten Erscheinen einer neuen Datei wird der Plex-Stand als Ausgangswert gespeichert; nur Änderungen danach zählen als Schauen (Wiedergabe nach Download-Beginn zählt). Zuordnung nur über die eigene Datei, nicht über die TMDB-ID |
| Watch-Status-Erkennung (Mehrbenutzer, Plex Pass) | WebSocket/Polling statt Webhooks; klare Benutzerzuordnung in den Einstellungen |
| Indexer-API-Limits | Caching der Suchergebnisse, RSS-Feed statt wiederholter Suche, Limits je Indexer konfigurierbar |
| Release-Namen-Vielfalt (deutsche Releases) | Großes Test-Korpus echter Namen, Parser tabellengetrieben testen |
| Neue/unbekannte Passwort-Konventionen | Passwortquellen als Module, Suchmuster per Regex konfigurierbar, Testkorpus mit echten NZB-/Release-Beispielen |
| Durchsatz des eigenen Downloaders | Früher Benchmark in Phase 1 gegen SABnzbd/NZBGet |
| Versehentliches Löschen | Nur getrackte Dateien in erlaubten Ordnern, Dry-Run-Modus, Audit-Log |

---

## 16. Entscheidungen

| Thema | Entscheidung |
|---|---|
| Medienserver | **Plex zuerst**; Jellyfin/Emby in Phase 5 |
| Benutzer | Ein-Personen-Betrieb als Default, Mehrbenutzer im Modell vorgesehen (Open Source) |
| Indexer | **Newznab-API** (generisch); Prowlarr als Newznab-Quelle nutzbar, kein eigener Adapter nötig |
| Serien-Löschauslöser | 10 % der nächsten Folge gesehen (Default) oder kurze Wartezeit nach Start, beides konfigurierbar |
| Overseerr/Jellyseerr | vorerst nicht |
| Fehlerbehandlung | Fehler werden klassifiziert; nicht verfügbare/defekte Quellen werden automatisch durch die nächste ersetzt (ab MVP) |
| Pfad-Mapping | Mapping-Tabelle lokaler Pfad ↔ Medienserver-Pfad, in beide Richtungen, mit Vorschlägen aus den Plex-Bibliotheken (ab MVP) |
| Passwörter | Mehrere Passwortquellen (NZB-Meta, NZB-Name, Titel, Liste, Betreff, NFO, Archiv-Kommentar, manuell); Quelle gilt erst als unbrauchbar, wenn kein Passwort passt |
| Entpacken | Entpacken **während** des Downloads ist Kernfunktion (ab Phase 1); klassisches Entpacken nur als Rückfall nach einer Reparatur |
| Schauen während des Downloads | A (wachsende Datei): Video unter endgültigem Namen, Plex-Scan nach 32 MB und nach Abschluss; B (virtuelles Dateisystem) später, siehe Abschnitt 6 |
| Verfügbarkeitsprüfung | Pro Usenet-Server abschaltbar (Standard an): vor dem Download wird jeder Artikel per `STAT` mit Pipelining geprüft; geladen wird nur, was vollständig oder per PAR2 reparierbar ist (Recovery-Daten ≥ 1,2 × fehlende Daten). Aus: Stichprobe von 20 Artikeln. Die nächsten Quellen werden nicht im Hintergrund vorab geprüft – das bräuchte Verbindungen und NZB-Abrufe während des laufenden Downloads |
| Künftige Staffeln | Pro Serie: gewählte Staffeln plus alle nach der höchsten Staffel, die bei der Auswahl schon lief (angekündigte zählen als künftig). „Alle Staffeln“ schließt künftige immer ein |
| Übersprungene Folgen | Gelten bei allen Löschauslösern als gesehen: gelöscht, sobald eine spätere Folge den Auslöser erreicht |
| Plex-Scan | Fehlgeschlagene Teil-Scans werden bis zu sechsmal mit Backoff wiederholt und bis zum nächsten erfolgreichen Scan als Warnung im Dashboard angezeigt |
| Download-Verlauf | Jeder Versuch mit Titel, Release, Größe, Indexer, Dauer und Ergebnis; bleibt erhalten, wenn Film oder Serie entfernt werden. Seite „Downloads“ mit Laufend, Warteschlange und Verlauf (Filter, Seiten à 50) |
| Reihenfolge der Releases | Nicht abgelehnte zuerst, dann nach Punktzahl des Profils (Auflösung, Quelle, Bonuswörter, Codec, PROPER, Grabs); bei gleicher Punktzahl das neueste Upload zuerst (ältere werden häufiger gelöscht), Releases ohne Datum danach, dann mehr Grabs |
| Reihenfolge der Folgen | Folgen einer Serie laden nacheinander in Ausstrahlungsreihenfolge; eine frühere Folge, die noch gesucht wird, hält die späteren zurück. Freie Plätze nutzen andere Serien und Filme |
| Einrichtung | Einrichtungsassistent beim ersten Start: Konto → Plex → Indexer → Usenet → Standards; Schritte testen vor dem Speichern, überspringbar, später erneut aufrufbar |
| Lizenz | GPL-3.0 |
| Konfiguration | Env-Variablen mit Präfix `AUTODOWNLOADARR_` nur für den Start (Port, Config-Ordner, Logging, Proxy); alles andere in der UI |
| Umsetzung Phase 4 | Wiedergabe-Fortschritt per Abfrage der Plex-Serienbibliothek jede Minute statt WebSocket – reicht für die Auslöser (Löschen/Nachladen mit höchstens einer Minute Verzug). Staffelpakete sind die Ausweich-Quelle: erst wird die Einzelfolge gesucht, nur ohne brauchbaren Treffer die ganze Staffel; aus dem Paket werden ausschließlich die Folgen des Fensters übernommen, der Rest wird verworfen. Spezialfolgen (Staffel 0) nur, wenn ausdrücklich gewählt. Filme und Serien haben je eigene Download-Plätze („Parallele Downloads“ gilt pro Bereich), teilen sich aber das Geschwindigkeitslimit. |
| Umsetzung Phase 3 | Prüfung der fertigen Datei per `ffprobe` (Laufzeit, Tonspur-Sprache) zurückgestellt: `ffmpeg` würde das Image um ~100 MB vergrößern – später als optionales Image oder externes Werkzeug. Gelernte Passwörter pro Release-Gruppe ebenfalls später; die Passwortliste zählt Treffer und sortiert danach. |
| Umsetzung Phase 2 | Gesehen-Status per Abfrage der Plex-Bibliothek (Standard alle 2 Min.) statt WebSocket – der WebSocket kommt mit den Serien (Phase 4), wo die Wiedergabe-Position zählt. Manuelle Passworteingabe als „Passwort zur Liste hinzufügen + Release erneut laden“. Ein Plex-Server, eine Film-Bibliothek. |
| Umsetzung Phase 0 | Go 1.26, chi, SQLite (modernc, ohne CGO) mit goose; Abfragen vorerst handgeschrieben (`sqlc` erst, wenn die Zahl der Abfragen es lohnt); React 19 + Vite + Tailwind 4, eigene kleine UI-Komponenten statt shadcn/ui; Go-Modulpfad `github.com/shyper/autodownloadarr` (Platzhalter bis zum GitHub-Repo) |

---

## 17. Open Source

- **Sprache im Projekt**: Code, Commits, Issues, Doku und UI-Standardsprache auf **Englisch**; UI mit i18n von Anfang an, Deutsch als erste Übersetzung.
- **Lizenz**: Empfehlung **GPL-3.0** (wie Sonarr/Radarr – Forks bleiben offen). Alternative: MIT, wenn maximale Weiterverwendung gewünscht ist.
- **Keine Annahmen über das eigene Setup**: alle Pfade, Ports, Benutzer, Zeitzone über Konfiguration; Setup-Assistent beim ersten Start.
- **Repo-Grundausstattung**: README mit Compose-Beispiel, CONTRIBUTING, Issue-Templates, CHANGELOG, SemVer-Releases.
- **CI**: Tests, Linting (golangci-lint, ESLint), Multi-Arch-Images nach GHCR, Release-Builds per Tag.
- **Doku für Erweiterungen**: kurze Anleitung „neuen Indexer/Medienserver hinzufügen“ anhand der Interfaces.
- **Tests ohne echte Zugangsdaten**: NNTP-Testserver und Newznab-/Plex-Mocks im Repo, damit Beitragende ohne eigenen Usenet-Account testen können.
