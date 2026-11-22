# Lookup

Barcode lookup, title search and outside sources. Read before changing a
lookup provider or chain, outside-source metrics, `fetch.Do`, the Discogs
client, rechecks, title search or Discogs attribution.

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- Lookup providers implement `lookup.Provider` (`Fetch` returns raw JSON,
  `Parse` turns raw JSON into candidates) and register in `cmd/api` with a
  cache TTL. Candidate `title`/`edition` fields match the POST bodies of
  `/api/titles` and `/api/editions`. A provider that only knows some
  barcodes implements `lookup.Scoped`. The movie and music providers are a
  `lookup.Fallback` for an ISBN-range code: they are asked in a second
  round, only when no book provider returned a candidate (a failed one
  counts as no answer and keeps its error). Both rounds together end within
  17 s, under the client's 20 s request deadline: a second round after a
  first that hit the 10 s provider timeout gets what is left. The stub
  provider waits in that round too for an ISBN. Book candidates for the scanned ISBN
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
  With `SkipBody` an accepted answer returns unread, for a write judged by
  its status (the Discogs wantlist sync).
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
- An audiobook stays kind `book`, its authors as authors: a book edition
  takes format `audiobook` or none for print, and only print takes a
  binding (a film or music format on a book, or a binding on an audiobook,
  answers 422). BN sets it from a record whose `kind` is "audiobook"; a
  merged candidate drops any binding another catalog named. The book
  edition form's "Nośnik" ("Druk" or "Audiobook") hides "Oprawa" for an
  audiobook, the quick confirm offers it beside the bindings, and the
  shelf, the edition summary and Kolekcja's "Nośnik" filter name it.
- BN fills a book's `series_name` from the first MARC 800 `$t` (an
  author's series) and `series_number` from the first whole number in its
  `$v` ("t. 2" and "2a" give 2, in any script's decimal digits; no digits
  give none). 490 and 830, where
  BN puts publishers' lines, are ignored. A merged candidate takes the
  name and number together from the first provider that names a series,
  so BN's wins. `Parse` reads the series from cached answers too, so
  earlier scans need no new request.
- Every Discogs request in the process (music lookup, title search,
  valuation, wantlist sync), and every redirect it follows, waits for one shared
  `discogs.Limiter`: a burst of 4, then one every 1.2 s. Each request
  sent, redirects included, has 8 s from its own turn
  (`discogs.RequestTimeout`) and its own HTTP/1.1
  connection (`discogs.NewHTTPClient`), so net/http cannot resend it
  outside the budget and no minute of arrivals holds more than the
  token's 60. Waiting scans and title searches go before the valuation's
  and the wantlist sync's `Background` requests, each in arrival order; a caller that gives up
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
- A failed answer is never cached, so a cached provider (one with a
  TTL) gets at most `lookup.FailureRequests` (3) requests per code within
  any `lookup.FailureWindow` (10 minutes) while none succeeds, for every
  lookup path: a scan, a typed code, "Spróbuj ponownie" and "Szukaj
  ponownie". Each request frees its slot a whole window after it was
  made; requests still in flight count, and so does one whose lookup the
  client gave up on after it went out (a lookup given up on before asks
  nothing). Past the limit the lookup answers with that provider's last
  failure, partial candidates included, without a request, and a success
  clears the count. The count lives in memory, so a restart clears it.
  A failure that names a source paused for its rate limit
  (`lookup.PausedError`, the movie chain's UPCitemdb after a 429) lets
  one more request past the limit once the provider reports the pause
  over, so the first lookup after the reset asks again; a pause already
  over when the request failed lets nothing past.
- A music barcode no source knows falls back to a Discogs text search for
  Allegro's product name.
- `GET /api/search?kind=&q=&barcode=&format=` searches by the title the
  user types (TMDB films, Google Books or, without a key, Open Library
  books, Discogs albums), answered as candidates for the barcode being
  added; a kind without its key answers 503.
- A film's series comes from its TMDB collection (`lookup.FilmFinder`,
  `movie.TMDB.Film`, enabled by `OHARA_TMDB_API_KEY`): two requests in
  Polish, `/3/movie/{id}` for the poster and `belongs_to_collection` and
  `/3/collection/{id}` for its name and parts, only for a title being
  created, never per search result, and not cached. A film in no
  collection costs the first request only. The name drops a trailing
  "kolekcja" or "Collection" TMDB adds ("Harry Potter - kolekcja" becomes
  "Harry Potter"); an empty name is no series. Only parts with a TMDB ID
  and a release date on or before today (UTC) are numbered, by release
  date and then TMDB ID, and counted. A movie answer without an `id`
  counts as a parse error. Both requests count under the `tmdb` source.

## Frontend

- Discogs data carries `DiscogsCredit`: "Data provided by Discogs." linked
  to its release page, never `nofollow`, under a quick confirm candidate
  with a `discogs_release_id`, a copy priced from Discogs on Wartość and,
  once per release, a Statystyki value group with such a copy.
  `/informacje` shows the API terms' non-affiliation notice. Both stay in
  English, word for word (`src/lib/discogs.ts`).

## Allegro authorization

Allegro authorization is one-time: with the client credentials set, run
`docker exec -it ohara api allegro-login`, open the printed link and
confirm the code. The refresh token is stored in `oauth_token` and rotated on
each refresh. A refresh runs on its own context, at most 20 s, so a lookup
that gives up first does not lose the rotated pair; a pair whose save failed
stays in use and is saved again by the next lookup, unless `allegro-login`
stored a pair meanwhile, which then wins. A refresh token Allegro rejects is
dropped, so the next lookup loads the pair a later `allegro-login` stored.
