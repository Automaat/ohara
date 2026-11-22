# Ohara

Self-hosted tracker for a physical media collection (movies, books, music):
barcode scanning from an iPhone, purchase prices, used-deal hunting and
valuation. Roadmap: umbrella issue #11.

**Stack:** Go (chi + pgx) and PostgreSQL 18 behind a SvelteKit + Svelte 5
SPA built with [`@smykla-skalski/sui`](https://github.com/smykla-skalski/sui).
Same layout and tooling as `../baratie`. `package.json` and
`backend-go/go.mod` are the source of truth for versions.

## Project structure

```
/backend-go/
  /cmd/api/            # main: env, migrations, HTTP server, `healthcheck` subcommand
  /cmd/apitypes/       # writes src/lib/api.gen.ts (`npm run generate:api`)
  /internal/
    /db/               # pgx pool + forward-only migrations (migrations/NNNN_name.sql)
    /dbtest/           # per-test Postgres schema for DB tests
    /httpx/            # JSON request/response and error helpers
    /apitypes/         # Go structs → TypeScript types: generator and the list of API types and enums
    /barcode/          # EAN/UPC/ISBN normalization (ISBN-10 → ISBN-13)
    /collection/       # titles, editions, copies, collection view
    /export/           # GET /api/export: the collection as a CSV or JSON file
    /lookup/           # GET /api/lookup/{barcode}: provider registry + lookup_cache
      /books/          # BN, Open Library, Google Books providers (fixtures in testdata/)
      /movie/          # movie chain: Allegro catalog, UPCitemdb → product name → TMDB search
      /music/          # music chain: Discogs barcode search → MusicBrainz + Cover Art Archive
    /discogs/          # Discogs API client: barcode search, marketplace stats, price suggestions, collection writes
    /discogscollection/ # POST /api/copies/{id}/discogs: an owned album copy into the Discogs collection
    /scanqueue/        # /api/scan-queue: barcodes the scanner could not resolve
    /deals/            # wishlist deal hunter: OLX, BlueDVD, Winylownia, Świat Książki, Pepper sources, matcher, Telegram alerts
    /wantlist/         # album wishes pushed to the Discogs wantlist (manual sync)
    /wishshare/        # the wishlist's read-only share link: owner API and the public /share/ pages
    /valuation/        # daily value of owned copies: OLX, SkupSzop buy-back, Discogs; value history
    /server/           # router: /health, /metrics, /api/*, SPA fallback
    /web/              # serves the built SPA, index.html for client routes
    /fetch/            # fetch.Do: one request to an outside source, capped body, query-free errors
    /metrics/          # Prometheus collectors on a private registry (ohara_*)
    /recovery/         # recovery.Call: a panic outside a request handler becomes a logged error
    /seed/             # `api seed`: demo data for an empty local database
    /reqlog/           # slog handler adding a request's request_id, method and path to *Context log lines
/src/                  # SvelteKit SPA (adapter-static, ssr off)
  /routes/             # pages; /scan is the scanner, /scan/queue the unresolved barcodes, /series the series
  /lib/                # nav table, API client, generated API types (api.gen.ts), form helpers, scan flow (pure TS, unit tested)
  /lib/components/     # forms shared by create and edit pages, camera and quick confirm
  /service-worker/     # SUI service worker (index.ts): precache, update only on the reader's consent
/static/               # icons, web app manifest, robots.txt
/docs/                 # per-area behavior (see Domain docs) and design mockups
/e2e/                  # Playwright smoke flows against the built image (compose.yaml, scripts/e2e.sh)
```

One image, `ghcr.io/automaat/ohara`: the Go binary serves `/api`, `/health`,
`/metrics` and the SPA from `OHARA_WEB_DIR`.

## Domain docs

Detailed behavior lives in `docs/`; read a file before changing its area:

- [collection](docs/collection.md): ordered copies, request keys, ratings,
  series, loans, finished titles, tags, box sets, export, cover photos, Kolekcja,
  Statystyki.
- [lookup](docs/lookup.md): lookup providers, outside-source metrics and
  `fetch.Do` (deals and valuation too), Discogs, Allegro, title search.
- [deals](docs/deals.md): the deal hunter, its matching, alerts and Telegram.
- [valuation](docs/valuation.md): copy values, value history, market prices,
  the insurance report.
- [scanner](docs/scanner.md): the scanner, pending scans and the scan queue.
- [backend](docs/backend.md): the router's compression, its panic and
  timeout answers, and `api seed`.
- [e2e](docs/e2e.md): the Playwright smoke suite.
- [ci](docs/ci.md): vulnerability checks, the shared Go version and the
  release tag protection.

## Conventions

### Working rules

- Change only what the task needs; no unrelated refactors or renames.
- Tests are the contract. Never edit a test to make it pass; if a spec and
  a test disagree, stop and report.
- Ask before a one-way change: a migration that rewrites or deletes data,
  an API response change, or a production setting.
- Add an outside source only when its terms (regulamin) allow automated
  access; robots.txt alone does not settle it.

### Backend

- One package per domain: `store.go` (pgx), `handler.go` (chi), validation as
  needed. Sentinel errors map to HTTP status codes.
- Schema changes are new files in `internal/db/migrations/`, applied once at
  startup in version order, each in its own transaction, under an advisory
  lock. Never edit an applied migration. Applied versions the binary has
  no file for (a rollback to an older release) log one error at startup
  and set `ohara_db_unknown_migrations`; the app still starts. A new
  migration takes a version above the current `main`'s highest:
  `scripts/check-migrations.sh origin/main` fails on one `main` already
  uses (see Git and CI).
- Linters: `golangci-lint` (`.golangci.yml`) and `nilaway`. Never add
  suppression directives; fix the root cause.
- Errors are `{"error": "...", "field": "..."}`: 422 for validation (field
  names the input), 409 for conflicts, 404 for a missing path resource.
  The router's own answers share the shape: 405 for a wrong method (with
  `Allow`), 500 for a handler panic (logged with its stack) and 504 for a
  request past the 30 s timeout whose handler wrote nothing.
- Every router answer carries `X-Request-Id`, the ID the access log and
  the panic log record; one a client sends is ignored. `cmd/api`'s logger
  wraps its text handler in `reqlog.NewHandler`, so a `*Context` log call
  with the request's context adds `request_id`, `method` and `path`;
  `httpx.WriteInternal` takes the request for that. Lines logged outside a
  request keep their form.
- Money is integer grosze (`price_cents`). Barcodes are stored normalized
  (`barcode.Normalize`: EAN-8, EAN-13 or GTIN-14; a UPC-A becomes its
  EAN-13 with a leading 0, ISBN-10 becomes ISBN-13), one edition per barcode.
- A copy is `owned`, `wishlist`, `sold` or `ordered`. A sold copy keeps its
  purchase price, date and shop and needs `sold_on`; `sale_price_cents` is
  optional. Only a sold copy carries either, so setting it back to `owned`
  clears them. Sold copies count in no owned total or `spent_cents`, and the
  valuation and deal hunter never read them. `GET /api/collection` lists
  them without a status, for the page that filters in the browser.
- Goroutines started outside a request handler, where the router's recoverer
  does not reach, call third-party code through `recovery.Call`: a lookup
  provider's `Fetch` and `Parse`, a movie name source, a deal or valuation
  run. A panic there is logged at error level with its stack and counts as
  that provider's, source's or run's failure.
- DB tests call `dbtest.Migrated(t)`; each gets its own schema.

### Frontend

- Svelte 5 runes only. Data comes from `/api` on the same origin.
- The API's JSON types are generated from the Go structs into
  `src/lib/api.gen.ts` (`npm run generate:api`, committed, never edited by
  hand); `collection.ts`, `scan.ts`, `deals.ts`, `valuation.ts` and
  `status.ts` re-export them. `internal/apitypes.Ohara` lists each struct
  under its TypeScript name and each enum with its values, taken from the
  Go constants or validation lists. A pointer field is `T | null`, an
  omitempty or omitzero one optional, a string field tagged `ts:"Kind"`
  takes the enum's union, and `ts:",null"` (or `ts:"Kind,null"`) also
  admits null; any other json tag option fails the generator. CI's `go-tests` job regenerates the file and fails on any
  difference, so change a JSON struct, then regenerate and commit.
- Build UI from SUI components; import `@smykla-skalski/sui/styles.css` once in
  the root layout. `build.cssTarget` in `vite.config.ts` keeps SUI's
  `:dir(rtl)` selectors intact.
- UI labels are Polish. No `any`. Prettier + oxlint, tabs.
- `static/manifest.webmanifest` is linked with `crossorigin="use-credentials"`
  because Authelia guards every path.
- The SPA enforces a Content-Security-Policy (`src/lib/csp.ts`, SvelteKit's
  `csp` option): built pages carry it in a `<meta>` tag with the hash of the
  inline boot script, `vite dev` sends it as a header. `script-src` is
  `'self'` plus `'wasm-unsafe-eval'` for zxing, never `'unsafe-inline'` or
  `'unsafe-eval'`. Styles allow `'unsafe-inline'` (Svelte and SUI style
  attributes), fonts `data:` (SUI inlines its Latin fonts), images any
  `https:` or `http:` host. Anything the page loads from another origin
  needs its directive there. `npm run build` ends with
  `scripts/check-csp.mjs`, which fails when a built page lacks the policy
  or an inline script's hash.

### Auth

None in the app. Production sits behind Traefik with the Authelia forward-auth
middleware, so the app trusts every request that reaches it. Do not expose
the container port beyond the LAN. With `OHARA_REQUIRE_REMOTE_USER=true` the
router answers 401 (`{"error": "..."}`, never a redirect) to a request
without a non-empty `Remote-User` header, the one Traefik copies from
Authelia's answer, so a request that skips the middleware reaches no handler,
page or file. Only `GET /health` and `GET /metrics` stay open, for the Docker
healthcheck and Prometheus, and `GET /share/…`, the wishlist's read-only
share link (see [deals](docs/deals.md)), which the owner chose to open to
anyone who has its unguessable token; each path is matched as chi routes
it, and a 401 still counts in `ohara_http_requests_total`. Production
needs an Authelia bypass for `/share/` alone, the one prefix that serves
anything without a login. The guard checks only that the
header is there, so it holds only while Traefik sets it from Authelia's
answer and the container port accepts nothing but Traefik and Prometheus
(home-nas firewall); anyone else who can reach the port can send the header. The name is never
used for authorization or stored. It is off by default (dev, CI and the Vite
proxy have no Authelia) and the startup log says whether it is on.

Authelia's session cookie covers every `*.mskalski.dev` site, so the router
answers 403 to any method but GET, HEAD and OPTIONS that a browser marks as
cross-origin: a `Sec-Fetch-Site` other than `same-origin` or `none`, or an
`Origin` whose host is not `Host`.
Requests with neither header, like curl, pass. The Vite dev proxy keeps the
browser's `Host` (`changeOrigin: false`) so dev writes pass this check.

## Configuration

| Variable                                                                                     | Default                                            | Purpose                                                                                                                                                    |
| -------------------------------------------------------------------------------------------- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `DATABASE_URL` or `PG*`                                                                      | —                                                  | Postgres connection (required). Prefer `PG*` when the password has URL-special characters.                                                                 |
| `OHARA_ADDR`                                                                                 | `:8000`                                            | Listen address                                                                                                                                             |
| `OHARA_WEB_DIR`                                                                              | unset (`/app/web` in the image)                    | Built SPA to serve                                                                                                                                         |
| `OHARA_ACCESS_LOG`                                                                           | `false`                                            | One log line per request                                                                                                                                   |
| `OHARA_REQUIRE_REMOTE_USER`                                                                  | `false`                                            | Answer 401 to requests without Authelia's `Remote-User` header, except `/health` and `/metrics`                                                            |
| `OHARA_LOOKUP_STUB`                                                                          | `false`                                            | Register the stub lookup provider (placeholder candidate for every barcode)                                                                                |
| `OHARA_GOOGLE_BOOKS_API_KEY`                                                                 | unset                                              | Enable the Google Books provider (keyless clients get no quota)                                                                                            |
| `OHARA_{BN,OPENLIBRARY}_URL`                                                                 | public endpoints                                   | Point the book sources at test doubles                                                                                                                     |
| `OHARA_DEALS_INTERVAL`                                                                       | `0s` (off)                                         | Deal search schedule, at least `10m` (e.g. `2h`), jittered ±10%; `POST /api/deals/run` works either way                                                    |
| `OHARA_DEALS_DELAY`                                                                          | `5s`                                               | Pause between two source requests, jittered ±50%                                                                                                           |
| `OHARA_DEALS_CANARY_QUERY`                                                                   | `dvd`                                              | Query asked of OLX, Świat Książki, Tezeusz and Winylownia every run that should always find listings (`ohara_source_canary_ok`); BlueDVD asks `4k` instead |
| `OHARA_TELEGRAM_BOT_TOKEN`, `OHARA_TELEGRAM_CHAT_ID`                                         | unset (no alerts)                                  | Telegram bot for deal alerts; set both or neither                                                                                                          |
| `OHARA_BLUEDVD`                                                                              | `true`                                             | BlueDVD shop (WooCommerce Store API) in the deal hunter; `false` leaves it out of runs and `/metrics`                                                      |
| `OHARA_SWIATKSIAZKI`                                                                         | `true`                                             | Świat Książki shop (GraphQL products search) in the deal hunter; `false` leaves it out of runs and `/metrics`                                              |
| `OHARA_TEZEUSZ`                                                                              | `true`                                             | Search the Tezeusz used-book shop for wished books (deals only, not the valuation); `false` leaves it out of runs and `/metrics`                           |
| `OHARA_WINYLOWNIA`                                                                           | `true`                                             | Winylownia vinyl shop (IdoSell search page) in the deal hunter, asked only for album wishes; `false` leaves it out of runs and `/metrics`                  |
| `OHARA_DEALS_{OLX,BLUEDVD,WINYLOWNIA,SWIATKSIAZKI,PEPPER,TEZEUSZ}_URL`, `OHARA_TELEGRAM_URL` | public endpoints                                   | Point the sources and Telegram at test doubles                                                                                                             |
| `OHARA_VALUATION_INTERVAL`                                                                   | `0s` (off)                                         | Valuation schedule, at least `1h` (e.g. `24h`), jittered ±10%; `POST /api/valuation/run` works either way                                                  |
| `OHARA_VALUATION_WINDOW_DAYS`                                                                | `90`                                               | How many days back a price still counts                                                                                                                    |
| `OHARA_PRICE_RETENTION_DAYS`                                                                 | unset (keep every listing)                         | Delete stored listings last seen more days ago, daily; at least `OHARA_VALUATION_WINDOW_DAYS`                                                              |
| `OHARA_VALUATION_SKUPSZOP_URL`                                                               | public site                                        | Point the SkupSzop quote at a test double                                                                                                                  |
| `OHARA_TMDB_API_KEY`                                                                         | unset                                              | TMDB v3 key or v4 read token; enables the movie lookup chain                                                                                               |
| `OHARA_ALLEGRO_CLIENT_ID`, `OHARA_ALLEGRO_CLIENT_SECRET`                                     | unset                                              | Allegro device-flow app; adds the Allegro catalog to the movie chain after `api allegro-login`                                                             |
| `OHARA_ALLEGRO_USER_AGENT`                                                                   | `Ohara/1.0.0 (+https://github.com/Automaat/ohara)` | User-Agent sent on every Allegro request; must match the one registered for the app                                                                        |
| `OHARA_UPCITEMDB`                                                                            | `true`                                             | UPCitemdb free-tier source (100 requests a day) in the movie chain                                                                                         |
| `OHARA_DISCOGS_TOKEN`                                                                        | unset                                              | Discogs personal access token; enables the Discogs music lookup, album valuation and wantlist sync                                                         |
| `OHARA_DISCOGS_FOLDER_ID`                                                                    | `1` (Uncategorized)                                | Discogs collection folder an album copy push adds to; a positive folder ID                                                                                 |
| `OHARA_DISCOGS_SELLERS`                                                                      | unset                                              | Discogs usernames, comma-separated, whose newest listings the deals read (needs the token)                                                                 |
| `OHARA_DISCOGS_PRICE_SUGGESTIONS`                                                            | `true`                                             | Ask Discogs price suggestions (need the token owner's seller settings) for album mid and ceiling                                                           |
| `OHARA_MUSICBRAINZ`                                                                          | `true`                                             | MusicBrainz + Cover Art Archive in the music chain                                                                                                         |
| `OHARA_{DISCOGS,MUSICBRAINZ,COVERART}_URL`                                                   | public endpoints                                   | Point the music sources at test doubles                                                                                                                    |
| `OHARA_VALUATION_NBP_URL`                                                                    | `https://api.nbp.pl`                               | Point the NBP exchange rates at a test double                                                                                                              |

## Commands

```bash
mise run dev                         # Postgres + app from source (docker compose)
mise run db                          # Postgres only, in the background
mise run backend                     # API only; the `mise run db` Postgres unless DATABASE_URL or PGHOST is set
mise run frontend                    # Vite dev server, proxies /api to :8000
mise run seed                        # demo data into the empty `mise run dev` database
mise run test-backend                # go test ./... with DB tests in ohara_test; args go to go test

npm run lint && npm run check        # oxlint + prettier, svelte-check
npm run test:coverage                # vitest
npm run test:e2e                     # Playwright smoke flows against the built image (Docker)
npm run build                        # SPA into build/
npm run generate:api                 # src/lib/api.gen.ts from the Go structs (needs Go only)
cd backend-go && go test ./...       # DB tests skip unless OHARA_TEST_DATABASE_URL is set
cd backend-go && golangci-lint run ./...
./scripts/run-nilaway.sh backend-go github.com/Automaat/ohara/backend-go
```

`OHARA_DB_PORT` (default `5432`) moves the dev Postgres's host port when
another one holds 5432; `db`, `backend` and `test-backend` follow it.

## Deployment

Runs on `custom-workloads` from `home-nas`
(`ansible/docker-compose/ohara-stack.yml`, `playbooks/deploy-ohara.yml`) at
`https://ohara.mskalski.dev` behind Traefik + Authelia. Nightly `pg_dump`
goes offsite through restic.

Release: run `release.yml` on `main` (empty input bumps the minor). It
refuses another ref, a commit whose `CI` run has not passed and a version
whose tag exists, then pushes `ghcr.io/automaat/ohara:vX.Y.Z`, tags `vX.Y.Z`
and cuts the GitHub release, in that order. `:latest` comes only from CI on
`main`. Then bump the tag in the home-nas stack file.

## Git and CI

- Conventional commits `type(scope): description`, signed with `-s -S`.
- CI: frontend (lint, check, coverage, build), Go (vet, test against
  Postgres, golangci-lint, nilaway), browser smoke flows (`e2e`), image
  build; `main` pushes `:latest` and `:<sha>` only after all of them pass. Pushed images (`main` and `release.yml`)
  carry BuildKit's SBOM and max-mode provenance attestations.
- CI's `migrations` job (`scripts/check-migrations.sh`) fails when two
  migrations share a version: a PR's merge commit, or a migration the PR
  adds to its base branch whose version the current `main` already uses
  (both files named), and `main` itself after a merge. A PR stacked on
  another answers only for its own migrations. Nothing reruns a PR's CI
  when `main` moves, so each push to `main` checks every open PR that adds
  a migration against the `main` of that moment and sets the
  `migrations against main` commit status on its head
  (`migrations-open-prs`); a PR without migrations gets none. A red status
  means renumbering above `main`'s highest.
