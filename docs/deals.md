# Deals

The wishlist deal hunter. Read before changing deal sources, matching,
alerts, dismissals, Telegram, pacing or canaries. Outside-source metrics and
`fetch.Do` are in [lookup.md](lookup.md).

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- Deal sources implement `deals.Source`. Matching folds every Latin
  letter with a diacritic to its base letter ("Bjork" for "Björk") in
  names, listing titles and phrases; search queries keep the want's
  spelling. A want's names are its Polish title, original title and
  name; a Polish book edition (`deals.PolishBookEdition`: an ISBN-13
  starting `97883`, or no barcode)
  drops the original title, which names foreign editions, from both its
  matching and its queries, in hunts and valuations alike. A run
  matches listing titles to wishlist copies (all title
  words; a name of words of at most two letters ("M", "Up", "It") only
  where it stands as the title: at the start, after a separator, a media
  word or the author (in any case), and before the end, a separator, a
  media word, a release year or the author, or anything right after the
  author, and a lone letter before a space ("M Fritz Lang"); the
  edition's format for movies, the author's surname in any case for
  books ("Andrzeja Sapkowskiego", "Lalka Prusa") and, for an audiobook
  only, a word naming one ("audiobook", "audiobooka", "audiobuk",
  "audioksiążka", not after "bez", "brak" or "nie", and more often than the
  want's own name or author does; a print book skips such listings, and
  an audiobook's queries add "audiobook"), the artist's words
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
  A wish with "Tylko to wydanie" (`copy.exact_edition`, wishes only, off
  by default, cleared when the copy becomes owned) of a movie or book
  also needs the listing to name each detail its edition has: every
  packaging (steelbook, slipcover or "obwoluta", digipak, mediabook), a
  `yes` Polish voice-over ("lektor", "dubbing") or subtitles ("napisy"),
  and a hardcover ("twarda oprawa") or paperback ("miękka okładka",
  "broszurowa") binding (`deals.editionNamed`); `no`, `unknown` and an
  `other` binding need nothing, and a mention after "bez" or "brak"
  ("bez lektora", "brak polskich napisów") names nothing. Each stored
  listing keeps the details its title names (`edition_details`, null
  before migration 0035, naming none), so a wish turned exact later
  stops alerting on its stored listings and the wished deals lists
  (matched, "Pod limitem", "Wysłane", the badge) drop them. The wish form
  and the quick confirm's wish (Okazje's "Dodaj pozycję" too) offer it,
  except for albums. Albums and the valuation ignore it.
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

## Discogs sellers

- With `OHARA_DISCOGS_TOKEN` and `OHARA_DISCOGS_SELLERS` (Discogs
  usernames, comma-separated, each kept once), every run reads each
  seller's 100 newest listings
  (`GET /users/{seller}/inventory?sort=listed&sort_order=desc&per_page=100`)
  as a feed source of its own, `deals.DiscogsSeller`, all under the
  source name `discogs_inventory` (label "Discogs"). Without either
  variable no such source exists, so `/metrics` has no series for it.
  Each request waits `OHARA_DEALS_DELAY` after the run's previous one and
  its turn in the token's Discogs budget (background priority), and counts
  in `ohara_source_requests_total` under `discogs_inventory`, not under
  the Discogs client's `discogs`. A 429 or 403 stops only that seller
  until the next run; a body that is no inventory (`{}`, HTML) is a
  `parse_error`. The sellers share the canary and results gauges: the
  canary holds once any seller answered with listings, and results count
  them all.
- Each listing "For Sale" becomes one listing: the Discogs listing ID,
  the release description as its title ("Pink Floyd - A Momentary Lapse Of
  Reason (LP, Album, Gat)"), `https://www.discogs.com/sell/item/<id>` and
  the release thumbnail. Its price turns into grosze at the NBP table A
  mid rate (`valuation.NBP`, `OHARA_VALUATION_NBP_URL`); a currency
  without a rate leaves the listing out, so a foreign amount is never
  stored as złoty. A `Mint (M)` copy has no condition, every other grade
  is used.
- The listing's medium comes from the Discogs format, not its title
  (`Listing.Medium`, `deals.discogsMedium`): LP, vinyl and 7", 10" or 12"
  discs are vinyl, CD, CDr, SACD and HDCD a CD, Cass a cassette, the first
  of them in a box set's parts; anything else (DVD, File) is no album
  medium. Such a listing matches album wants only, a CD want only a CD
  and a vinyl want only vinyl. Listings of other sources still take their
  medium from the title.
- The Discogs API Terms of Use make seller inventory Restricted Data:
  no commercial use, no transfer to third parties, never shown more than
  six hours older than discogs.com, kept no longer than needed. So a
  stored listing of a seller whose answer no longer holds it is gone at
  once (`Store.UpdateGone`, scoped to the seller through the listing's
  query), a listing gone or not offered by an answer for 6 hours leaves
  Okazje and its alert is not sent (`freshListing`), and every run first
  deletes the Discogs listings no answer has offered for 30 days, alerts,
  dismissals and purchases included (`Store.ForgetStale`). The 30 days
  keep a listing's dismissal and sent alert while its seller cannot be
  read. Every Telegram alert for one ends with "Data provided by
  Discogs." under its link, and its card in Okazje carries the same
  notice as a followable link to the listing. The purchase happens on
  discogs.com.
- The valuation never reads `discogs_inventory`, and wants are not
  matched by Discogs release ID.

## Wishlist share link

- `internal/wishshare` keeps at most one read-only link to the wishlist
  (`wishlist_share`, a single row). `POST /api/wishlist/share` makes a new
  one and answers `{url, created_at}`, `url` being its path on this site,
  `/share/{token}`; the token is 256 random bits in base64url. Making one
  again replaces the token, so the old link answers 404.
  `GET /api/wishlist/share` shows the current link (404 when off) and
  `DELETE` turns it off (204, also when already off). The token is stored
  as is: anyone who can read the database already reads the whole
  wishlist, so a hash would protect nothing, and the owner can copy the
  same link again later.
- Okazje's "Udostępnij listę życzeń" asks for the link when opened and
  offers "Utwórz link", then the link with "Kopiuj", "Nowy link" and
  "Wyłącz link".
- Everything public sits under `/share/`, served by the Go server, not
  the SPA, so the page loads no app asset, script or service worker:
  `GET /share/{token}` is the page "Lista życzeń", rendered on the server
  with its own style and a CSP that allows no script, and
  `GET /share/{token}/wishes` is its data, `{"wishes": [...]}` with
  `title` (a TV season's carries ", sezon N"), `creators`, `year`, `kind`,
  `format`, `binding` and `cover_url` per wished copy, TV series included,
  by title. Nothing else leaves: no price, limit, note, ID, owned copy or
  "Gdzie szukać". `cover_url` is the edition's or title's outside cover;
  an edition's own photo, served only under `/api`, and any cover on the
  request's own host, an IP address in any spelling or a name without a
  dot such as `localhost` are left out. The page's `img-src` names only
  the shown covers' origins, plus `archive.org`, where Open Library and the
  Cover Art Archive redirect, so neither a cover nor its redirect reaches
  the app outside `/share/`. The token and the wishes are read in one read-only
  snapshot, so a link turned off mid-request shows nothing it would not
  have shown before.
- An unknown or turned-off token, any other path under `/share/` and any
  method but GET answer the API's own 404 (`{"error":"not found"}`).
  Share answers carry `Cache-Control: no-store`,
  `Referrer-Policy: no-referrer` and `X-Robots-Tag: noindex, nofollow`.
  Ohara's logs never record the token: the access log, the panic log and
  every request-context log line write the path as `/share/{token}`.
  Traefik logs request paths on its own, so its share router runs without
  access logging (Automaat/home-nas#946).
- Okazje offers "Utwórz link" only once the server answered that no link
  is on; after a failed read it offers a retry, since a new link would
  break the one already sent. The service worker leaves `/share/` to the
  network (`serverPaths`), so offline a share link fails instead of
  showing the app.
