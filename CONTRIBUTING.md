# Contributing

Thanks for helping! A few conventions keep the project easy to work on.

## Basics

- Code, comments, commit messages, issues and documentation are in English.
- Every user-facing string in the web UI goes through i18next. Add new keys to
  `web/src/i18n/locales/en.json` **and** `de.json`; a test checks that both
  have the same keys.
- Run `make lint test` before opening a pull request.

## Backend (Go)

- Packages live under `internal/`. External systems (indexers, media servers,
  download clients, notifiers) sit behind interfaces so that new ones can be
  added as separate packages; see `docs/PLAN.md` section 3.
- Database changes are new files in `internal/db/migrations/`
  (`NNNNN_description.sql`, goose format). Never edit a released migration.
- Work that must eventually happen (deletions, searches, retries) goes through
  the `scheduler`, not goroutines, so it survives restarts. Handlers must be
  idempotent.
- API errors use `{"error": {"code": "...", "message": "..."}}`. The `code` is
  stable; the web UI translates it.

## Frontend

- React + TypeScript + Tailwind CSS. Colours come from the semantic tokens in
  `web/src/styles.css` (`bg-surface`, `text-muted`, …), so dark mode works
  without extra classes.
- Server state goes through TanStack Query hooks in `web/src/api/`.

## Tests without real accounts

Tests must not need a Usenet account, an indexer or a media server. Use fakes
or local test servers.
