# Ohara

Self-hosted tracker for a physical media collection (movies, books, later
music): barcode scanning from an iPhone, purchase prices, used-deal hunting and
valuation. Roadmap: umbrella issue #11.

**Stack:** Go (chi + pgx) and PostgreSQL 18 behind a SvelteKit 2 + Svelte 5
SPA built with [`@smykla-skalski/sui`](https://github.com/smykla-skalski/sui).
Same layout and tooling as `../baratie`. `package.json` and
`backend-go/go.mod` are the source of truth for versions.

## Project structure

```
/backend-go/
  /cmd/api/            # main: env, migrations, HTTP server, `healthcheck` subcommand
  /internal/
    /db/               # pgx pool + forward-only migrations (migrations/NNNN_name.sql)
    /dbtest/           # per-test Postgres schema for DB tests
    /httpx/            # JSON request/response and error helpers
    /barcode/          # EAN/UPC/ISBN normalization (ISBN-10 → ISBN-13)
    /collection/       # titles, editions, copies, collection view
    /lookup/           # GET /api/lookup/{barcode}: provider registry + lookup_cache
      /books/          # BN, Open Library, Google Books providers (fixtures in testdata/)
    /scanqueue/        # /api/scan-queue: barcodes the scanner could not resolve
    /server/           # router: /health, /metrics, /api/*, SPA fallback
    /web/              # serves the built SPA, index.html for client routes
    /metrics/          # Prometheus collectors on a private registry (ohara_*)
/src/                  # SvelteKit SPA (adapter-static, ssr off)
  /routes/             # pages; /scan is the scanner, /scan/queue the unresolved barcodes
  /lib/                # nav table, API client, collection types, form helpers, scan flow (pure TS, unit tested)
  /lib/components/     # forms shared by create and edit pages, camera and quick confirm
  service-worker.ts    # SUI service worker: precache, update only on the reader's consent
/static/               # icons, web app manifest, robots.txt
```

One image, `ghcr.io/automaat/ohara`: the Go binary serves `/api`, `/health`,
`/metrics` and the SPA from `OHARA_WEB_DIR`.

## Conventions

### Backend

- One package per domain: `store.go` (pgx), `handler.go` (chi), validation as
  needed. Sentinel errors map to HTTP status codes.
- Schema changes are new files in `internal/db/migrations/`, applied once at
  startup in version order, each in its own transaction, under an advisory
  lock. Never edit an applied migration.
- Linters: `golangci-lint` (`.golangci.yml`) and `nilaway`. Never add
  suppression directives; fix the root cause.
- Errors are `{"error": "...", "field": "..."}`: 422 for validation (field
  names the input), 409 for conflicts, 404 for a missing path resource.
- Money is integer grosze (`price_cents`). Barcodes are stored normalized
  (`barcode.Normalize`: EAN-8, EAN-13 or GTIN-14; a UPC-A becomes its
  EAN-13 with a leading 0, ISBN-10 becomes ISBN-13), one edition per barcode.
- Lookup providers implement `lookup.Provider` (`Fetch` returns raw JSON,
  `Parse` turns raw JSON into candidates) and register in `cmd/api` with a
  cache TTL. Candidate `title`/`edition` fields match the POST bodies of
  `/api/titles` and `/api/editions`. A provider that only knows some
  barcodes implements `lookup.Scoped`. Book candidates for the scanned ISBN
  from several providers merge into one; registration order is the field
  priority. Every provider gets `ohara_lookup_provider_requests_total`,
  `ohara_lookup_provider_errors_total` and `ohara_lookup_cache_hits_total`.
- DB tests call `dbtest.Migrated(t)`; each gets its own schema.
- A barcode no provider resolves goes to `scan_queue`; a trigger on `edition`
  removes it once an edition carries that barcode.

### Frontend

- Svelte 5 runes only. Data comes from `/api` on the same origin.
- Build UI from SUI components; import `@smykla-skalski/sui/styles.css` once in
  the root layout. `build.cssTarget` in `vite.config.ts` keeps SUI's
  `:dir(rtl)` selectors intact.
- UI labels are Polish. No `any`. Prettier + oxlint, tabs.
- The scanner (`/scan`) is the PWA's start page. Camera decoding uses
  `zxing-wasm` (Safari has no `BarcodeDetector`); its `.wasm` is bundled by
  Vite, never loaded from a CDN. A keyboard-wedge scanner types into the
  focused barcode field. The flow lives in `src/lib/scan.ts`: existing
  edition → add a copy, lookup candidates → quick confirm, nothing → queue.
- `static/manifest.webmanifest` is linked with `crossorigin="use-credentials"`
  because Authelia guards every path.

### Auth

None in the app. Production sits behind Traefik with the Authelia forward-auth
middleware, so the app trusts every request that reaches it. Do not expose
the container port beyond the LAN.

## Configuration

| Variable                     | Default                         | Purpose                                                                                    |
| ---------------------------- | ------------------------------- | ------------------------------------------------------------------------------------------ |
| `DATABASE_URL` or `PG*`      | —                               | Postgres connection (required). Prefer `PG*` when the password has URL-special characters. |
| `OHARA_ADDR`                 | `:8000`                         | Listen address                                                                             |
| `OHARA_WEB_DIR`              | unset (`/app/web` in the image) | Built SPA to serve                                                                         |
| `OHARA_ACCESS_LOG`           | `false`                         | One log line per request                                                                   |
| `OHARA_LOOKUP_STUB`          | `false`                         | Register the stub lookup provider (placeholder candidate for every barcode)                |
| `OHARA_GOOGLE_BOOKS_API_KEY` | unset                           | Enable the Google Books provider (keyless clients get no quota)                            |

## Commands

```bash
mise run dev                         # Postgres + app from source (docker compose)
mise run backend                     # API only, needs DATABASE_URL or PG*
mise run frontend                    # Vite dev server, proxies /api to :8000

npm run lint && npm run check        # oxlint + prettier, svelte-check
npm run test:coverage                # vitest
npm run build                        # SPA into build/
cd backend-go && go test ./...       # set OHARA_TEST_DATABASE_URL for DB tests
cd backend-go && golangci-lint run ./...
./scripts/run-nilaway.sh backend-go github.com/Automaat/ohara/backend-go
```

## Deployment

Runs on `custom-workloads` from `home-nas`
(`ansible/docker-compose/ohara-stack.yml`, `playbooks/deploy-ohara.yml`) at
`https://ohara.mskalski.dev` behind Traefik + Authelia. Nightly `pg_dump`
goes offsite through restic.

Release: run `release.yml` (empty input bumps the minor) → tag `vX.Y.Z`,
GitHub release, `ghcr.io/automaat/ohara:vX.Y.Z`. Then bump the tag in the
home-nas stack file.

## Git and CI

- Conventional commits `type(scope): description`, signed with `-s -S`.
- CI: frontend (lint, check, coverage, build), Go (vet, test against
  Postgres, golangci-lint, nilaway), image build; `main` pushes
  `:latest` and `:<sha>`.
