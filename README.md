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

For frontend work, run `mise run backend` against a Postgres and
`mise run frontend` for the Vite dev server on http://localhost:5173.

## Image

`ghcr.io/automaat/ohara` serves the API, the web UI, `/health` and
`/metrics` on port 8000. See [AGENTS.md](AGENTS.md) for configuration,
conventions and deployment.
