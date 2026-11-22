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

## Discogs wantlist

- `internal/wantlist` mirrors the album wishes to the Discogs wantlist of
  the `OHARA_DISCOGS_TOKEN` owner (`GET /oauth/identity`), push only. Okazje
  shows "Synchronizuj z Discogs" when `GET /api/discogs/wantlist` says
  `enabled`; `POST /api/discogs/wantlist/sync` answers 202 and runs the sync
  in the background, 409 while one runs and 503 without the token. The page
  asks for the status every 2 s while a sync runs and shows its counts.
- A sync reads the whole wantlist (every page of
  `GET /users/{username}/wants`, at most 10,000 wants), then puts on it
  (`PUT .../wants/{release_id}`, no notes or rating) every distinct
  `discogs_release_id` of a music wishlist copy's edition that is not
  there yet. It counts those added, those already there and the album
  wishes (wishlist copies) whose edition has no release ID (skipped).
- `discogs_want` records each want Ohara added, per username. A recorded
  want whose release no wish holds any more (the copy deleted or no
  longer a wish) is taken off (`DELETE`) and forgotten; a want Ohara did
  not add is never removed, even when its wish goes. A sync with nothing
  to change sends no write.
- `discogs_want` changes before each request: an add is recorded first,
  a removal forgets the want first, so a database failure stops the sync
  before a write it could not record. Discogs refusing the write (a
  redirect or a 4xx, 429 included) undoes that change; an unknown outcome
  (a 5xx, a lost answer) keeps a recorded add and a forgotten removal, so
  a want the collector re-adds by hand is never taken. A write is judged
  by its status alone, its body unread, and a redirect that would turn it
  into a GET is not followed. The first failure stops the sync; the status
  keeps the counts so far and the failure, and the next sync retries the
  rest. The last sync's
  status lives until the process restarts. Sync requests wait in the
  Discogs budget as `Background` ([lookup.md](lookup.md)); deal runs never
  start a sync, and the sync never changes matching, limits or alerts.
