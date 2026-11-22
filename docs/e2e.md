# Smoke suite

Read before changing `e2e/`, `scripts/e2e.sh` or a flow it covers.

Moved from `CLAUDE.md`, whose conventions still apply.

## Frontend

- The smoke suite (`e2e/`, `npm run test:e2e`) drives the production image
  in Chromium at 390 × 844 with touch: a wedge scan, quick confirm and
  "Cofnij", "Dodaj tytuł" by hand, the scan sheet sending no deals or queue
  read, the queue count following a queued scan, its add and undo,
  Kolekcja's filters, which also run at 1440 × 900, and its sort orders
  under active filters, tags from the title form to Kolekcja's filter,
  series from the title form and Kolekcja to the series page,
  cover photo uploads, the collection export, and the insurance report
  with its A4 printout. `scripts/e2e.sh` builds
  the image (or takes one as its first argument), starts it from
  `e2e/compose.yaml`
  with Postgres, `OHARA_LOOKUP_STUB=true`, `OHARA_MUSICBRAINZ=false` and no
  provider keys, and removes it afterwards. App and Postgres sit on an
  internal network, so no public provider is reachable; each test also
  fails on a browser request to another origin and checks that
  `ohara_source_requests_total` stayed 0. The camera starts only on "Włącz
  aparat" or a remembered choice, so a fresh browser gets no camera prompt.
  Needs `npx playwright install chromium` once.
