# Valuation

The daily value of owned copies. Read before changing price ranges,
likely-sold prices, Discogs pricing, retention, value history, market
prices, Wartość or the insurance report. Matching, the store and the OLX
source come from [deals.md](deals.md).

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- Valuation reuses the deal matcher, store and the OLX source for owned
  editions, and quotes owned books (ISBN barcodes) at SkupSzop.
  A copy's range comes from prices last seen within the window: floor =
  its edition's latest SkupSzop offer (0 when the shop does not buy it);
  mid = median of OLX prices not marked new, outliers beyond 1.5 IQR
  dropped, pooled over every edition of the same title and format, owned,
  wished or without copies, a title's Polish book editions apart from its
  other ones (the used market of a wish still pools both), and a Polish
  book edition's stored OLX price only while its listing title still
  matches the edition (a barcode that turned Polish drops the foreign
  listings matched before); an unowned edition's prices count only when
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
  with its trend when the copy's prices left the window. `GET /api/valuation`'s
  `moves` pair each copy's stored used value on the last valuation day with
  its value on `moves_from`, the newest valuation day 30 to 37 days before
  (one day for every copy, so a skipped run still gives a start; null and no
  moves without one), and Wartość's "Największe zmiany" lists the 5 owned
  copies that rose and the 5 that fell most in grosze
  (`movers` in `src/lib/valuation.ts`), as "+20 zł · +50%" (grosze under
  half a złoty); a copy without a used value on either day, or with the same
  value, is in neither, and without any the section is hidden. The summary's `gain` sums the used value
  and the price paid over the copies that have both; the Wartość hero's
  difference reads only that, and names its coverage when it counts
  fewer copies than the collection. History keeps no gain.
  The summary's `realized` is what sold copies made: sale price less
  purchase price (no fees), summed over the copies sold on or before
  today that have both, a gift sold for 0 zł losing its price; a sold
  copy missing either counts in `unpriced`, never as 0 zł. Each run
  stores it in the day's `valuation_history` row; the row, its copies'
  values and the gain take the day of the snapshot the run read its
  owned copies and sales from, so a sale committed or midnight passed
  before the save counts each copy once (days stored before migration
  0030 read 0). The
  Wartość hero shows it as "Zysk ze sprzedaży", signed, with its copies,
  and nothing without a sold copy; before the first valuation the line
  stands under the empty state.
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

## Frontend

- Wartość's "Raport dla ubezpieczyciela" (`/valuation/report`, linked
  before the first valuation too) lists every owned copy for an insurer:
  cover (the supplied URL, no thumbnail), title, creators, format or
  binding, barcode, condition, price paid and the used and new values,
  "brak wyceny" for a missing one, the Discogs grade beside a Discogs used
  value and `DiscogsCredit` beside a Discogs-priced copy, then totals that
  each name the copies they cover (`src/lib/insurance-report.ts`), the
  last valuation's day and the day the amounts stand for. It reads only
  `GET /api/valuation`, whose copies carry `condition`, `authors` and
  `binding` from the snapshot that priced them. A title or creators past
  160 characters are cut in the row and listed whole in a footnote
  appendix, so a row never outgrows a page or the screen.
  The browser's Print to PDF makes the document: an `@media print` rule
  puts the report on its own A4 page type (`@page insurance-report`, so
  other pages keep the printer's paper), hides the navigation, tab bar and
  buttons, fixes the column widths to the page, repeats the table header
  on each page and keeps every row whole. "Drukuj" waits until every
  cover has loaded or failed. Before the first valuation the page says
  none exists and links to Wartość.
