# Backend details

Read before changing the router's compression or `api seed`.

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- The router, not Traefik, compresses: `compressibleTypes` in
  `internal/server/compress.go` lists the types (HTML, JS, CSS, JSON, SVG,
  the manifest, `.wasm`) gzipped at the best level for a client whose
  `Accept-Encoding` allows gzip (quality values count; no deflate). Every
  answer carries `Vary: Accept-Encoding`. Other images go out as they are and
  a response that already has a `Content-Encoding` (`/metrics`) is left
  alone. A client that accepts gzip gets the whole resource for a `Range`
  request unless the path's extension names a type never compressed (a
  PNG keeps its range). A compressed answer drops `Accept-Ranges` and
  weakens its `ETag` (a 304 for that weak `ETag` keeps it weak) and a
  compressed HEAD sends no `Content-Length`; a client without gzip keeps
  its ranges.
- `api seed` (`internal/seed`) fills an empty database with demo rows for
  every page in one transaction and sends no request; it refuses (exit 1,
  nothing changed) a database holding titles, listings, value history,
  scan queue entries, a deal run, deal keywords that differ from the
  defaults the migrations insert (an added phrase or a deleted default)
  or a saved lookup selection, and one missing a migration of this
  build: it never migrates, since a migration commits on its own and a
  refusal could not undo it. It takes the scan queue's barcode locks,
  then locks those tables, before the check, the order a queued scan
  takes them in, so a write from a running app waits for it. Cached
  provider answers and an Allegro login (`oauth_token`) do not count:
  the app fetches the first again, and the second is a credential, not
  collection data.
  It seeds no deal run (a running hunter reads that only at startup) and
  never a pending deal alert. Its DB test asserts what each page needs,
  so a schema change that breaks the seed fails CI.
