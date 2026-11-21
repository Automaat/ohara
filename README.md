# Ohara

Self-hosted tracker for a physical media collection: movies (DVD / Blu-ray / 4K),
books and music (CD / vinyl / cassette). Barcode scanning from an iPhone,
purchase prices, used-deal hunting on Polish marketplaces and collection
valuation.

Roadmap: https://github.com/Automaat/ohara/issues/11

## Run locally

```bash
mise install
npm ci
mise run dev      # Postgres + app on http://localhost:8000
```

For backend or frontend work, run the parts from source:

```bash
mise run db             # Postgres only, in the background
mise run backend        # API on http://localhost:8000
mise run frontend       # Vite dev server on http://localhost:5173
mise run test-backend   # Go tests, DB tests in a separate ohara_test database
mise run down           # stop Postgres
```

Set `OHARA_DB_PORT` when another Postgres holds port 5432.

## Personal cover photos

Add, replace or remove a photo under **Zdjęcie okładki** on the edition page.
Photos must be JPEG or PNG, up to 8 MiB and 16 megapixels. Export HEIC photos
as JPEG before uploading. Removing a photo restores the provider cover when
one is available.

PostgreSQL stores the photos with their editions. Back up and restore the
database with `pg_dump` and the corresponding restore tool to preserve them.

## Image

`ghcr.io/automaat/ohara` serves the API, the web UI, `/health` and
`/metrics` on port 8000. See [CLAUDE.md](CLAUDE.md) for configuration,
conventions and deployment.
