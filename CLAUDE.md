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
    /lookup/           # GET /api/lookup/{barcode}: provider registry + lookup_cache
      /books/          # BN, Open Library, Google Books providers (fixtures in testdata/)
      /movie/          # movie chain: Allegro catalog, UPCitemdb → product name → TMDB search
      /music/          # music chain: Discogs barcode search → MusicBrainz + Cover Art Archive
    /discogs/          # Discogs API client: barcode search, marketplace stats, price suggestions
    /scanqueue/        # /api/scan-queue: barcodes the scanner could not resolve
    /deals/            # wishlist deal hunter: OLX, Pepper sources, matcher, Telegram alerts
    /valuation/        # daily value of owned copies: OLX, SkupSzop buy-back, Discogs; value history
    /server/           # router: /health, /metrics, /api/*, SPA fallback
    /web/              # serves the built SPA, index.html for client routes
    /fetch/            # fetch.Do: one request to an outside source, capped body, query-free errors
    /metrics/          # Prometheus collectors on a private registry (ohara_*)
    /recovery/         # recovery.Call: a panic outside a request handler becomes a logged error
    /seed/             # `api seed`: demo data for an empty local database
    /reqlog/           # slog handler adding a request's request_id, method and path to *Context log lines
/src/                  # SvelteKit SPA (adapter-static, ssr off)
  /routes/             # pages; /scan is the scanner, /scan/queue the unresolved barcodes
  /lib/                # nav table, API client, generated API types (api.gen.ts), form helpers, scan flow (pure TS, unit tested)
  /lib/components/     # forms shared by create and edit pages, camera and quick confirm
  /service-worker/     # SUI service worker (index.ts): precache, update only on the reader's consent
/static/               # icons, web app manifest, robots.txt
/e2e/                  # Playwright smoke flows against the built image (compose.yaml, scripts/e2e.sh)
```

One image, `ghcr.io/automaat/ohara`: the Go binary serves `/api`, `/health`,
`/metrics` and the SPA from `OHARA_WEB_DIR`.

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
- Money is integer grosze (`price_cents`). Barcodes are stored normalized
  (`barcode.Normalize`: EAN-8, EAN-13 or GTIN-14; a UPC-A becomes its
  EAN-13 with a leading 0, ISBN-10 becomes ISBN-13), one edition per barcode.
- A copy is `owned`, `wishlist` or `sold`. A sold copy keeps its purchase
  price, date and shop and needs `sold_on`; `sale_price_cents` is optional.
  Only a sold copy carries either, so setting it back to `owned` clears
  them. Sold copies count in no owned total or `spent_cents`, and the
  valuation and deal hunter never read them. `GET /api/collection` lists
  them without a status, for the page that filters in the browser.
- `POST /api/titles`, `/api/editions` and `/api/copies` take an optional
  `request_key`: a repeat with the same key answers 200 with the row the
  first request created. The scanner sends one key per add, so a retry
  after a lost answer knows what the add made and undo removes only that.
  `DELETE /api/requests/{key}` removes what a failed barcode-less add made
  with its key: the copy, then the edition and title unless another copy
  or edition uses them or a person changed them (`edited_at`: an edit or a
  cover photo; rows keyed before that column count as changed once
  they were updated or got a photo). The key
  goes into `discarded_request`, so a create that arrives with it later
  (a stalled request) answers 409 on `request_key` before any other
  check; a per-key advisory lock orders creates and discards, and a create
  reads its row back under it. The collection's add calls it after a
  refused save and before saving another valid pick over a key that may
  have written; a pick's provider values the API would refuse are left
  out of the save. A key is at most 100 bytes of UTF-8 without NUL.
- Lookup providers implement `lookup.Provider` (`Fetch` returns raw JSON,
  `Parse` turns raw JSON into candidates) and register in `cmd/api` with a
  cache TTL. Candidate `title`/`edition` fields match the POST bodies of
  `/api/titles` and `/api/editions`. A provider that only knows some
  barcodes implements `lookup.Scoped`. Book candidates for the scanned ISBN
  from several providers merge into one; registration order is the field
  priority. Every provider gets `ohara_lookup_provider_requests_total`,
  `ohara_lookup_provider_errors_total` and `ohara_lookup_cache_hits_total`.
  Every external request (book providers, the movie chain's sources, deal
  sources, valuation sources) also counts in the source health metrics
  through `metrics.ObserveSource`:
  `ohara_source_requests_total{source,status}`,
  `ohara_source_errors_total{source}`, `ohara_source_parse_errors_total`
  and `ohara_source_last_success_timestamp_seconds`. A request counts as
  ok only once its response parses; it then sets `ohara_source_results` to
  the products or candidates found. `fetch.Do` times every request whose
  `fetch.Request` names its `Source` in
  `ohara_source_request_duration_seconds{source}`, whatever the outcome,
  from the call until the body is read or the request fails; a pacing
  or redirect wait inside the client counts, as it does toward the
  client's timeout. A Discogs base URL that does not parse is timed with
  its error. A SkupSzop quote counts once, as ok when the cart shows the
  offer or the shop does not buy the book, with results 1 for an offer,
  and is timed once over all its requests. Register each source with
  `metrics.InitSource` so its counters exist before the first failure.
  Every finished deal or valuation run, scheduled or manual, counts in
  `ohara_background_runs_total{task="deals"|"valuation",outcome}`: `failed`
  when the run returns an error (database, panic, shutdown), else `ok`,
  failing sources included. It also observes
  `ohara_background_run_duration_seconds{task}` (1 s to 2 h buckets), and
  an `ok` run sets `ohara_background_last_success_timestamp_seconds{task}`.
  Each Telegram send counts in `ohara_deal_alerts_total{outcome="sent"|"failed"}`.
  The run counters exist at 0 from startup, the alert counters when
  Telegram is configured.
  Every source request but Telegram's goes through `fetch.Do`: a body
  over the caller's cap fails (`fetch.Outcome` counts it a parse error),
  a status the caller does not accept is a `fetch.StatusError` returned
  with the response headers, and errors never carry the URL's query.
  Browser-like sites get `fetch.BrowserUserAgent`.
  When some of its sources fail but others answered, it returns a
  `*lookup.PartialError`: the lookup reports the failure next to the
  candidates and does not cache the answer. The movie chain waits at most
  4 s for each name source and 6 s for all of them, fallbacks included,
  so a hanging catalog fails as that source and leaves TMDB the rest of
  the 10 s lookup. An Allegro token refresh runs on past that wait.
- Music is the kind `music`: an album title whose `authors` are its credited
  artists, editions with a `cd`, `vinyl` or `cassette` format (required, like
  a movie's `dvd`/`bluray`/`uhd`) and an optional `discogs_release_id`. The
  `music` provider asks Discogs first (needs `OHARA_DISCOGS_TOKEN`; a 13-digit
  code with a leading 0 is retried as its 12-digit UPC-A) and MusicBrainz
  only when Discogs finds nothing or fails, with covers from the Cover Art
  Archive. Every release becomes a candidate: one barcode often names
  several pressings, so the user picks the edition.
- Every Discogs request in the process (music lookup, title search,
  valuation), and every redirect it follows, waits for one shared
  `discogs.Limiter`: a burst of 4, then one every 1.2 s. Each request
  sent, redirects included, has 8 s from its own turn
  (`discogs.RequestTimeout`) and its own HTTP/1.1
  connection (`discogs.NewHTTPClient`), so net/http cannot resend it
  outside the budget and no minute of arrivals holds more than the
  token's 60. Waiting scans and title searches go before the valuation's
  `Background` requests, each in arrival order; a caller that gives up
  leaves the queue, and a wait that would outlast the caller's context
  returns its error at once without a request.
- A lookup result sets `needs_confirmation` unless exactly one candidate
  reaches `lookup.ConfirmThreshold` (0.8); clients never auto-pick otherwise.
  `PUT /api/lookup/{barcode}/selection` stores the picked candidate, which
  answers later scans of that barcode without provider requests.
- The movie chain asks UPCitemdb (free: 100 requests a day, 6 a minute)
  only after Allegro's catalog names no product with a searchable title.
  After a 429 it skips UPCitemdb until the `X-RateLimit-Reset` time, or
  for an hour when that header is missing or more than two days ahead; a
  reset already past needs no pause. A skipped UPCitemdb counts as a
  failed source, so a lookup that finds nothing is not cached.
- `GET /api/lookup/{barcode}?recheck=1`, sent only by the queue's "Szukaj
  ponownie" (`/scan/{barcode}?recheck=1`), asks a provider again when its
  cached answer is older than `lookup.RecheckAfter` (1 hour). The server
  keeps that floor: it remembers each recheck request in memory, so
  repeated taps cost one request per provider and code per hour even
  while one is in flight or after it failed. The page drops the flag
  from the address once used, so a reload reads the cache.
- A music barcode no source knows falls back to a Discogs text search for
  Allegro's product name.
- `GET /api/search?kind=&q=&barcode=&format=` searches by the title the
  user types (TMDB films, Google Books or, without a key, Open Library
  books, Discogs albums), answered as candidates for the barcode being
  added; a kind without its key answers 503.
- Goroutines started outside a request handler, where the router's recoverer
  does not reach, call third-party code through `recovery.Call`: a lookup
  provider's `Fetch` and `Parse`, a movie name source, a deal or valuation
  run. A panic there is logged at error level with its stack and counts as
  that provider's, source's or run's failure.
- DB tests call `dbtest.Migrated(t)`; each gets its own schema.
- `api seed` (`internal/seed`) fills an empty database with demo rows for
  every page in one transaction and sends no request; it refuses (exit 1,
  nothing changed) a database holding titles, listings, value history,
  scan queue entries or a deal run, and one missing a migration of this
  build: it never migrates, since a migration commits on its own and a
  refusal could not undo it. It takes the scan queue's barcode locks,
  then locks those tables, before the check, the order a queued scan
  takes them in, so a write from a running app waits for it.
  It seeds no deal run (a running hunter reads that only at startup) and
  never a pending deal alert. Its DB test asserts what each page needs,
  so a schema change that breaks the seed fails CI.
- Deal sources implement `deals.Source`. Matching folds every Latin
  letter with a diacritic to its base letter ("Bjork" for "Björk") in
  names, listing titles and phrases; search queries keep the want's
  spelling. A run matches listing titles to wishlist copies (all title
  words; a name of words of at most two letters ("M", "Up", "It") only
  where it stands as the title: at the start, after a separator, a media
  word or the author (in any case), and before the end, a separator, a
  media word, a release year or the author, or anything right after the
  author, and a lone letter before a space ("M Fritz Lang"); the
  edition's format for movies, the author's surname in any case for
  books ("Andrzeja Sapkowskiego", "Lalka Prusa"), the artist's words
  and a fitting medium for
  albums, where a listing naming no medium counts as a CD, minus the
  editable reject phrases a want's own name or artist does not contain),
  skips lots of several titles: a count of titles ("3 filmy", "20 szt.",
  "kolekcja filmów") unless a want's name makes it a set ("Trylogia"),
  and, before the wanted name, a set word ("zestaw", "pakiet", "komplet")
  or five discs or volumes or more ("5 płyt", "8 tomów"); past the name
  those describe one title's edition ("6 płyt CD"), and one title on a
  few discs ("2 płyty", "5xDVD") still matches,
  skips a want for a listing that names another title of its kind
  (wished, owned or neither) more specifically: one phrase, past media
  words and disc counts, holding every word of a want's name it carries
  and more ("Alien: Covenant" for an "Alien" want; a short name counts
  its single letters, "X-Men" for an "X" want), stores
  each listing once in `price_observation` (`source`, `external_id`) and
  queues one `deal_alert` per listing at or below the copy's
  `max_price_cents` or its `max_market_percent` (1 to 100) of the
  title and format's used market price, when either holds; a listing seen again keeps its row, takes the new
  price and `last_seen_at`, and alerts if it has no alert yet. Alerts are retried up to 5 times within a week.
  A hunt or valuation that matches a stored listing to another title (its
  edition's) moves it to that title and the matched edition and sets
  `moved_at`; a valuation match drops the wish it was recorded for. Its
  unsent alert for a wish of another title moves to the new wish, or to
  none (the deals view then shows no alert), so the old wish never gets
  it; a sent alert stays, and the deals view no longer shows the listing
  under the old title's wish. Each run reads the database time with its
  wishlist or holdings and titles; a listing keeps in `assigned_as_of` the
  newest such time of a run that matched it to its current title. A run
  that read its data before that does not move the listing (it matched
  from older data) and queues no alert for a wish of another title than
  the listing's. Another edition of the same title keeps the
  stored one.
  A sent alert stores the price it named (`deal_alert.alerted_price_cents`);
  a later price under it, within the limit, queues the same row again and
  its message names the earlier price ("było …"). An alert still waiting
  goes out at the current price; one that gave up or expired is queued
  again only under the lowest price it was queued or tried at
  (`queued_price_cents`). An unsent alert whose wish is gone moves to the
  wish that now matches, keeping its attempts and age. Alerts sent before
  migration 0016 have no stored price and never repeat.
  Okazje's "Ignoruj" (`POST /api/deals/{id}/dismissal`, undone by `DELETE`,
  both 204 when repeated, 404 for an id no OLX or Pepper listing has) sets
  the listing's `dismissed_at`, for every wish it matches: it leaves every
  deals list and the badge, its unsent alert is never sent and a run that
  sees it again keeps it dismissed and queues no alert. The hunter sends
  each alert holding the listing's row from its last check until the
  outcome is stored (`Store.SendAlert`), so a dismissal waits for a
  message already on its way. The observation, its alert and its price
  for the valuation stay. The page's toasts (`src/lib/deal-dismissals.svelte.ts`)
  offer "Cofnij", or "Spróbuj ponownie" after a failed request, and are
  taken back when the page goes away.
  Every Telegram alert carries inline "Ignoruj" and "Kupione" buttons
  (callback data `ignore:<observation id>` and
  `bought:<observation id>:<copy id>`, the wish the message was sent
  for, even after its alert moves to another wish). A "Kupione" press
  (`Store.BuyPress`) turns that wish into an owned copy, with no
  confirmation: the listing's price, bought today in Poland, the source's
  label as the shop, new or used from the listing (the wish's own value
  when it says nothing), both limits cleared; the copy keeps its id,
  edition, condition and notes and the listing is dismissed. The message
  then gets a "Kupione" line (its text cut short with "…" when the line
  would pass Telegram's 4096 characters) and loses its buttons. A button
  buys once, and only on a press's first delivery: every "Kupione" press
  is recorded in `telegram_press`, refused ones too, and every purchase in
  `telegram_purchase` (listing and wish), so a press sent again or pressed
  again only reads the state. A copy already owned whose listing is
  dismissed is answered "Już zapisano jako kupione" and its message is
  edited again, so a message whose edits failed is fixed by the next
  press; a purchase the collector changed back ("Ten zakup był już
  zapisany") or a wish no longer on the wishlist changes nothing and is
  only answered. With Telegram configured, the hunter's
  `TelegramPoller` long-polls `getUpdates` (no webhook: Telegram cannot
  reach the LAN) for callback queries only, 20 per call (halved after a
  reply over 1 MiB); a press in the alert chat dismisses the listing as
  Okazje's "Ignoruj" does, is answered, then has its button taken off the
  message, and a press from another chat changes nothing but is answered.
  The next poll's `offset`, and one more call on shutdown, confirm the
  handled updates; shutdown leaves the rest of a batch unconfirmed. A
  press Telegram still holds when polling resumes is handled then
  (Telegram keeps a callback only minutes, not the 24 hours it keeps
  other updates), and one sent again is applied once:
  `Store.DismissPress` records its callback id in `telegram_press` in the
  dismissal's transaction, so a replay never undoes a later "Cofnij"; it
  is only answered and its button taken off again. A keyboard edit the
  Bot API fails for now (no answer, 5xx, 429) is tried 3 times; a button
  still left works, since a new press answers and edits again. A dismissal that fails
  for now (a database error, or the listing's row held past the step's
  10 s) leaves the press unanswered and unconfirmed, and the poller asks
  for it again after 30 s. A failed poll (a webhook set on the bot
  answers 409) is logged and retried after 30 s.
  The wish a listing was recorded for (`raw.wishlist_copy_id`) is also the
  generated, indexed column `wishlist_copy_id`, so the deals list's filters
  start from wished listings instead of reading every observation.
  A source that answers 403 or 429 is asked nothing more until the next
  run, in a hunt and in a valuation (SkupSzop quotes too); the run's
  errors name the refusal once with the requests it skipped, and skipped
  requests count in no source metric. Other failures do not stop asking.
  Hunts and valuations share one `deals.Pacer`, built in `cmd/api`, each
  in a lane of its own that paces every request of its HTTP client,
  redirects too: a request to a host is sent at least half of
  `OHARA_DEALS_DELAY` after the other lane's last one there. A request
  holds the site until it is written, never while it waits for an answer,
  so neither run waits for the other to finish; a lane never waits for
  itself, so a run on its own keeps its pacing.
  Every run also asks each query source the canary query, wishlist or not,
  and sets `ohara_source_canary_ok` (a feed's one request is its canary)
  and `ohara_source_results`. A canary without listings is a broken source.
  The last run, scheduled, manual or cut short by shutdown, is kept in
  `deal_run`: after a restart the status shows it and the first scheduled
  run comes one interval after it started, at least 5 minutes on.
- Valuation reuses the deal matcher, store and the OLX source for owned
  editions, and quotes owned books (ISBN barcodes) at SkupSzop.
  A copy's range comes from prices last seen within the window: floor =
  its edition's latest SkupSzop offer (0 when the shop does not buy it);
  mid = median of OLX prices not marked new, outliers beyond 1.5 IQR
  dropped, pooled over every edition of the same title and format, owned,
  wished or without copies; an unowned edition's prices count only when
  first seen, or moved to it from another title (`moved_at`), after its
  last change. A mid from fewer than 3 prices is low
  confidence. Only a Discogs release has a ceiling. A stored OLX listing
  keeps the price it was first seen at (`first_price_cents`, null for rows
  stored before migration 0021) and the query that last found it
  (`raw.query`, `deals.QueryKey`). After a valuation run asks OLX every
  query, a listing last found by a query whose answer held fewer offers
  than the 50 asked for, each with a readable ID (`deals.Pager`, a
  complete answer), that no answer of the run held (priced or not) and
  neither seen with a price nor offered at all (`last_seen_at`,
  `last_offered_at`) since the run started on the database clock gets
  `gone_at`; `last_seen_at` keeps its last sighting. A first page, a failed or skipped query tells nothing, and a
  gone listing any answer holds again, matching or not, is no longer gone.
  Gone listings stay stored and price as before; no request is sent per
  listing. Every hunt or valuation answer, canary and feed answers with an
  empty wishlist included, moves `last_offered_at` (null for rows not
  offered since migration 0024) of every stored listing it holds, priced
  or not, matching or not, as it arrives: an OLX or Pepper `deals.Pager`
  names every offer with a readable ID in `Page.Seen`, also those its
  listings leave out; `last_seen_at` moves only with a recorded price.
  Stored listings stay forever unless `OHARA_PRICE_RETENTION_DAYS` is set
  (never below the valuation window): then the valuation runner, scheduled
  or not, deletes in batches of 1000 those whose later of the two is
  older, 5 minutes after start and then daily, logs the count and adds it
  to `ohara_price_observations_pruned_total`. A hunt or valuation holds a
  shared advisory lock (keyed by schema) on a connection of its own for
  its whole run and is counted as running in the process, which still
  holds if that lock's session ends; a prune takes the lock exclusive
  without waiting and checks the count, so it never runs during one: it
  deletes nothing and tries again 10 minutes later. A
  dismissed listing and one with a `deal_alert` row, sent or not, are
  never pruned, so a listing still offered past every answer keeps its
  dismissal and alert (it does not come back as a new row that alerts
  again) and a "Kupione" press keeps its listing, alert, `telegram_press`
  and `telegram_purchase` rows; neither is one a transaction holds. A
  listing the runs still see is never pruned.
  Beside its range each copy gets `likely_sold`: the trimmed
  median latest price of the same pool's used OLX listings whose `gone_at`
  is at most `valuation.LikelySoldWithin` (21 days, under OLX's 30-day
  listing run) after `observed_at`. Wartość shows it as "Prawdopodobnie
  sprzedane", "mało danych" under 3 listings, nothing without one; it
  never enters the range, the summary or the history. An owned album edition
  with a `discogs_release_id` is
  priced on Discogs (marketplace stats in EUR, price suggestions in the
  seller-settings currency, converted to PLN at the NBP table A rate): its
  floor is the cheapest copy for sale, its ceiling the suggested Mint
  price and each copy's mid the suggestion for the grade its condition
  maps to (`mint` Near Mint, `very_good` Very Good Plus, `good` Very
  Good, `fair` Good, `poor` Poor; `mid_grade` names it), Very Good Plus
  for a copy without a condition or whose grade has no suggestion. A
  release's prices are stored in one transaction, so a grade counts only
  when the edition's newest answer naming any middle grade named it; an
  answer naming none (`{}`, Mint only) or a failed request keeps the last
  answer (#411). The suggestion replaces the OLX mid and is never
  low confidence. Each run upserts today's `valuation_history` row and
  sets the `ohara_collection_*` gauges. Skupksiazek.pl (cart behind an
  app flow) and Rivendo (manual form with photos) have no automatic
  quote, so movie discs have no floor. A title page reads
  `GET /api/valuation/copies?title_id=`: that title's owned copies valued
  as `GET /api/valuation` values them, from that title's prices only,
  with no summary or history. Each run also stores every owned copy's
  buy-back, used and new value of the day in `copy_value_history`, one
  row per copy and day (null for a bound without prices), in one
  transaction with the day's `valuation_history` row; a later run that
  day replaces the rows of the copies it values, a copy keeps its rows,
  today's too, once no longer owned and deleting it deletes them. No past
  days are filled in. `GET /api/valuation/copies/history?title_id=` gives
  a title's copy days of the last 90 days, and the title page's "Wartość
  dziś" draws the copy's used value as a trend once two days have one, a
  day without one or without a stored row leaving a gap; the card stays
  with its trend when the copy's prices left the window. The summary's `gain` sums the used value
  and the price paid over the copies that have both; the Wartość hero's
  difference reads only that, and names its coverage when it counts
  fewer copies than the collection. History keeps no gain.
  `GET /api/valuation/market` gives each
  wished title and format that used middle from stored listings, owned
  or not (`?title_id=`: every format of one title), and sends no request
  out: Okazje's deal cards and a wish form's "Cena maksymalna" show it as
  "Rynek ok. 42,50 zł (4 oferty)", with "mało danych" under 3 listings.
  A wish's market percent reads that price from a 3-listing market up
  (`valuation.Runner.DealMarkets`, given to `deals.Store.UseMarkets` in
  `cmd/api`, since `deals` cannot import `valuation`), at queue time, at
  send time (read once the listing's row is held) and for "Pod limitem",
  the badge and "Wysłane"; the alert message and deal card name it ("70%
  ceny rynkowej 50,00 zł"). A run reads it again after each answer while
  a listing it stored met no rule, and queues the alerts of those that now
  meet it, so a listing counts in the market that judges it, as on later
  runs.
- A barcode no provider resolves goes to `scan_queue`; a trigger on `edition`
  removes it once an edition carries that barcode. `ohara_scan_queue_size`
  counts it on each scrape. The trigger keeps the entry it removes in
  `scan_queue_resolved`; `DELETE /api/editions/{id}?requeue=1` puts it back
  with its scans and times in the same transaction, as the scanner does when
  it undoes an add that created the edition.
- An edition's own cover photo is served at the `cover_override_url` the API
  hands out, `/api/editions/{id}/cover?v=` plus the edition's `updated_at`,
  which a new photo or any edit changes. A request whose query parses and
  holds one `v`, the current one, answers
  `Cache-Control: private, max-age=31536000, immutable`; any other `v`, a
  second one, none or an error answers `no-store`.

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
- The scanner (`/scan`) is the PWA's start page. Camera decoding uses
  `zxing-wasm` (Safari has no `BarcodeDetector`); its `.wasm` is bundled by
  Vite, never loaded from a CDN. A keyboard-wedge scanner types into the
  focused barcode field. The flow lives in `src/lib/scan.ts`: existing
  edition → add a copy, lookup candidates → quick confirm, nothing → queue.
  A camera scan leaves focus where it was: the sheet reads each resolved
  result line and the item's name once from a polite `status`
  (`ScanFlow.announcement`); the other results speak through their live
  `Callout`. Only a typed code moves focus to the next step.
- The scan sheet's history entries are shallow navigations, which skip the
  Okazje badge refresh. The camera's queue count
  (`src/lib/queue-tally.svelte.ts`) is read once when the scanner opens,
  then moved where the requests answer: a queue POST (also for a sheet
  already replaced), an edition POST for a queued code (even when its copy
  then fails) and that add's undo. Only undoing an add made before the
  first read answered, or one kept from before a reload, reads the queue
  again.
- A code whose check gets no answer is kept in IndexedDB
  (`src/lib/pending-scans.svelte.ts`, barcode only, once in the server's
  normalized form) and posted to `/api/scan-queue` as `lookup_failed` on app
  start, on `online`, when the app is shown and every 20 s, by one tab at a
  time (Web Locks), each POST within 15 s. `request` does not follow
  redirects, so a 401 or Authelia's cross-origin redirect is an expired
  session ("Odśwież"), not a lost connection; the code is kept either way.
- The scanner's visit ("Ta sesja" adds, "Ostatnie skany" and the purchase
  date carried to the next add) is kept in `localStorage`
  (`ohara.scan.visit`, `src/lib/visit.ts`) and restored on load until 12
  hours after its last add or scan. Each change starts from what storage
  holds, so tabs keep each other's adds, and an add writes its date, scan
  and tray entry at once. A waiting scan turns queued once the queue takes
  its code. Undo toasts are not restored; without storage the visit lasts
  until the next load.
- Kolekcja's "Dodaj tytuł" (`/titles/new`) is the scanner's quick confirm
  without a barcode (`ScanFlow.start`): search by title or fill in by hand;
  the save creates the title, a barcode-less edition and the copy. Such
  adds stay out of the scanner's recent scans. Okazje's "Dodaj pozycję"
  opens it as `/titles/new?from=deals`: the item starts as a wish and the
  page leads back to `/deals`.
- Kolekcja (`/`) lists copies. It loads all of `GET /api/collection` once
  per visit and filters, searches and counts it in the browser
  (`src/lib/collection-filter.ts`, the server's `Store.Collection` filter
  with the search normalized by barcode.Normalize's rules), so a chip, the
  sheet or a search sends no request; Back draws the kept collection and
  refreshes it once. Its filter sheet's "Bez egzemplarzy"
  (`?status=none`) lists titles without one instead, filtered in the client
  from `GET /api/titles`; `/titles` redirects to `/`. Its "Bez ceny"
  (`?status=unpriced`, client only) lists owned copies without a purchase
  price; a phone's header shows their count as its toggle, hidden at 0.
  Its "Sprzedane" switch (`?status=sold`) lists the sold copies, which
  every other view, the counts and the default shelf leave out.
  `?add=copy` on a title
  or edition page opens the form for its next copy. The shelf draws its
  tiles in parts (`src/lib/shelf-drawing.ts`): 60 with the page, then 150
  a task, each tile carrying `aria-setsize`. Until the last part lands the
  list keeps the height all tiles will take and a scroll past the drawn
  tiles draws them at once; End, or a scroll to the end of the page, draws
  every tile left before scrolling, so it reaches the last copy. A restored
  scroll first draws down to its place. Only the layout on screen
  is mounted: a phone always gets the shelf, never the hidden table, and a
  shelf back from the table (a phone turned sideways and back) returns to
  the tile that was at the top when the reader last scrolled.
- "Zmień okładkę" shrinks the chosen photo in the browser before upload
  (`shrinkPhoto` in `src/lib/edition-cover.ts`): `createImageBitmap` turns
  it upright from its EXIF orientation, a canvas at most 1600 px on the
  long edge draws it on white and sends it as JPEG, lowering the quality
  until it fits under 1 MB. A file that is no JPEG or PNG, or one the
  browser cannot decode, sends nothing, and a photo still shrinking when
  its sheet is gone is never sent. The server's 8 MiB and 16 megapixel
  limits stay as a backstop.
- Discogs data carries `DiscogsCredit`: "Data provided by Discogs." linked
  to its release page, never `nofollow`, under a quick confirm candidate
  with a `discogs_release_id` and a copy priced from Discogs on Wartość.
  `/informacje` shows the API terms' non-affiliation notice. Both stay in
  English, word for word (`src/lib/discogs.ts`).
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
- The smoke suite (`e2e/`, `npm run test:e2e`) drives the production image
  in Chromium at 390 × 844 with touch: a wedge scan, quick confirm and
  "Cofnij", "Dodaj tytuł" by hand, the scan sheet sending no deals or queue
  read, the queue count following a queued scan, its add and undo,
  Kolekcja's filters, which also run at 1440 × 900, and its sort orders
  under active filters, and cover photo uploads. `scripts/e2e.sh` builds
  the image (or takes one as its first argument), starts it from
  `e2e/compose.yaml`
  with Postgres, `OHARA_LOOKUP_STUB=true`, `OHARA_MUSICBRAINZ=false` and no
  provider keys, and removes it afterwards. App and Postgres sit on an
  internal network, so no public provider is reachable; each test also
  fails on a browser request to another origin and checks that
  `ohara_source_requests_total` stayed 0. The camera starts only on "Włącz
  aparat" or a remembered choice, so a fresh browser gets no camera prompt.
  Needs `npx playwright install chromium` once.

### Auth

None in the app. Production sits behind Traefik with the Authelia forward-auth
middleware, so the app trusts every request that reaches it. Do not expose
the container port beyond the LAN. With `OHARA_REQUIRE_REMOTE_USER=true` the
router answers 401 (`{"error": "..."}`, never a redirect) to a request
without a non-empty `Remote-User` header, the one Traefik copies from
Authelia's answer, so a request that skips the middleware reaches no handler,
page or file. Only `GET /health` and `GET /metrics` stay open, for the Docker
healthcheck and Prometheus, with the path matched as chi routes it; a 401
still counts in `ohara_http_requests_total`. The guard checks only that the
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

| Variable                                                 | Default                                            | Purpose                                                                                                   |
| -------------------------------------------------------- | -------------------------------------------------- | --------------------------------------------------------------------------------------------------------- |
| `DATABASE_URL` or `PG*`                                  | —                                                  | Postgres connection (required). Prefer `PG*` when the password has URL-special characters.                |
| `OHARA_ADDR`                                             | `:8000`                                            | Listen address                                                                                            |
| `OHARA_WEB_DIR`                                          | unset (`/app/web` in the image)                    | Built SPA to serve                                                                                        |
| `OHARA_ACCESS_LOG`                                       | `false`                                            | One log line per request                                                                                  |
| `OHARA_REQUIRE_REMOTE_USER`                              | `false`                                            | Answer 401 to requests without Authelia's `Remote-User` header, except `/health` and `/metrics`           |
| `OHARA_LOOKUP_STUB`                                      | `false`                                            | Register the stub lookup provider (placeholder candidate for every barcode)                               |
| `OHARA_GOOGLE_BOOKS_API_KEY`                             | unset                                              | Enable the Google Books provider (keyless clients get no quota)                                           |
| `OHARA_DEALS_INTERVAL`                                   | `0s` (off)                                         | Deal search schedule, at least `10m` (e.g. `2h`), jittered ±10%; `POST /api/deals/run` works either way   |
| `OHARA_DEALS_DELAY`                                      | `5s`                                               | Pause between two source requests, jittered ±50%                                                          |
| `OHARA_DEALS_CANARY_QUERY`                               | `dvd`                                              | Query asked of OLX every run that should always find listings (`ohara_source_canary_ok`)                  |
| `OHARA_TELEGRAM_BOT_TOKEN`, `OHARA_TELEGRAM_CHAT_ID`     | unset (no alerts)                                  | Telegram bot for deal alerts; set both or neither                                                         |
| `OHARA_DEALS_{OLX,PEPPER}_URL`, `OHARA_TELEGRAM_URL`     | public endpoints                                   | Point the sources and Telegram at test doubles                                                            |
| `OHARA_VALUATION_INTERVAL`                               | `0s` (off)                                         | Valuation schedule, at least `1h` (e.g. `24h`), jittered ±10%; `POST /api/valuation/run` works either way |
| `OHARA_VALUATION_WINDOW_DAYS`                            | `90`                                               | How many days back a price still counts                                                                   |
| `OHARA_PRICE_RETENTION_DAYS`                             | unset (keep every listing)                         | Delete stored listings last seen more days ago, daily; at least `OHARA_VALUATION_WINDOW_DAYS`             |
| `OHARA_VALUATION_SKUPSZOP_URL`                           | public site                                        | Point the SkupSzop quote at a test double                                                                 |
| `OHARA_TMDB_API_KEY`                                     | unset                                              | TMDB v3 key or v4 read token; enables the movie lookup chain                                              |
| `OHARA_ALLEGRO_CLIENT_ID`, `OHARA_ALLEGRO_CLIENT_SECRET` | unset                                              | Allegro device-flow app; adds the Allegro catalog to the movie chain after `api allegro-login`            |
| `OHARA_ALLEGRO_USER_AGENT`                               | `Ohara/1.0.0 (+https://github.com/Automaat/ohara)` | User-Agent sent on every Allegro request; must match the one registered for the app                       |
| `OHARA_UPCITEMDB`                                        | `true`                                             | UPCitemdb free-tier source (100 requests a day) in the movie chain                                        |
| `OHARA_DISCOGS_TOKEN`                                    | unset                                              | Discogs personal access token; enables the Discogs music lookup and album valuation                       |
| `OHARA_DISCOGS_PRICE_SUGGESTIONS`                        | `true`                                             | Ask Discogs price suggestions (need the token owner's seller settings) for album mid and ceiling          |
| `OHARA_MUSICBRAINZ`                                      | `true`                                             | MusicBrainz + Cover Art Archive in the music chain                                                        |
| `OHARA_{DISCOGS,MUSICBRAINZ,COVERART}_URL`               | public endpoints                                   | Point the music sources at test doubles                                                                   |
| `OHARA_VALUATION_NBP_URL`                                | `https://api.nbp.pl`                               | Point the NBP exchange rates at a test double                                                             |

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

Allegro authorization is one-time: with the client credentials set, run
`docker exec -it ohara api allegro-login`, open the printed link and
confirm the code. The refresh token is stored in `oauth_token` and rotated on
each refresh. A refresh runs on its own context, at most 20 s, so a lookup
that gives up first does not lose the rotated pair; a pair whose save failed
stays in use and is saved again by the next lookup, unless `allegro-login`
stored a pair meanwhile, which then wins. A refresh token Allegro rejects is
dropped, so the next lookup loads the pair a later `allegro-login` stored.

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
- CI fails on known vulnerabilities (`scripts/check-vulnerabilities.sh`),
  on every push and PR and nightly: `govulncheck` for Go code the backend
  calls, under the go.mod toolchain; `npm audit` for high and critical
  advisories in any package; Trivy for fixable high and critical ones in
  the image's OS packages, before the push (the Dockerfile pins the runtime
  base by digest, so the push rebuild uses the scanned base). govulncheck
  runs with `GOOS=linux CGO_ENABLED=0`, like the image build. A finding
  is accepted only through `.github/vulnerability-allowlist.json` (`go`,
  `npm`, `image` lists of `id`, `reason` and `expires`, YYYY-MM-DD); an
  entry counts through its expiry day. Renovate's `osvVulnerabilityAlerts` opens
  fix PRs for vulnerable dependencies.
- One Go patch release everywhere: `toolchain` in `backend-go/go.mod`
  (CI's `setup-go` reads it), the Dockerfile's `golang` tag and `go` in
  `.mise.toml`. Renovate's `go` group bumps them in one PR, opened only
  once all three have the release (`minimumGroupSize: 3`), and CI's
  `scripts/check-go-versions.sh` fails when they differ; the `go`
  language line stays where it is.
