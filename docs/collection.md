# Collection

Copies, titles and the Kolekcja pages. Read before changing ordered copies,
add request keys, ratings, series, loans, the finished mark, tags, box sets, the
export, cover photos, Kolekcja, "Dodaj tytuł" or Statystyki.

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- An `ordered` copy is bought but not delivered: it takes a price, its
  order date as `purchased_on`, a shop and an optional `expected_on`, which
  no other status takes (422); a wish's limits and a location stay off it.
  Its price counts in `spent_cents` and `totals.ordered` counts it apart
  from `owned` (left out at 0); the valuation (value, spend, copy count)
  and the deal hunter never read it. Kolekcja's default shelf keeps it,
  with a parcel on the cover and "Dostawa <date>" ("po terminie" once the
  day has passed, local time) or "Zamówiony"; the filter sheet's
  "Zamówione" switch (`?status=ordered`) lists them alone. The shelf's day
  (`src/lib/local-day.svelte.ts`) moves on at local midnight and when the
  page is shown again, for late deliveries and loans alike. The copy
  form marks an order with a "Zamówiony" switch under "Na półce". The copy
  page's "Odebrano" sends `POST /api/copies/{id}/receipt`, which turns the
  stored ordered copy `owned` and drops `expected_on`, keeping every other
  field as stored (409 on `status` for a copy not on order), so a page read
  before another save cannot put old values back. No reminder goes out for
  a late delivery.
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
- A title's `rating` is the owner's own, a whole number from 1 to 10, on
  any title, a wish's too; `null` clears it and 0, 11 or a fraction
  answers 422 on `rating`. A `PUT /api/titles/{id}` without `rating`
  keeps the stored one, so the edit form leaves it alone. The title
  page's "Ocena" select sends `PUT /api/titles/{id}/rating`
  (`{"rating": n | null}`, the key required), which changes only the
  rating, so a page loaded before another edit never writes the rest
  back. Picks are stored one after another in one queue for the whole
  app (`src/lib/title-rating.ts`), so a visit's late saves never land
  after a later visit's:
  only the latest pick's failure shows, with the last confirmed rating,
  and a title read that began before a pick or during one keeps the
  shown rating. `GET /api/collection` items carry it (absent while unrated),
  and Kolekcja's "Ocena malejąco" lists rated copies from 10 down,
  unrated ones last, ties in title order.
- A series (`series`) holds titles of one kind (a composite reference to
  `(id, kind)`), and a title sits in at most one, optionally with a whole
  `series_number` from 1 to 10000, unique in the series (409 on
  `series_number`). `POST` and `PUT /api/titles` name it by `series_name`,
  not by ID, so a lookup candidate could carry one: a name matching a
  series of the title's kind by the Go case fold `foldCase` (trimmed) joins
  it, a new one creates it, blank or null is none. A number without a name,
  below 1, past 10000 or a fraction answers 422 on `series_number`; a
  `PUT` without both fields keeps the stored series, and a title turned
  into another kind takes its series' name and number into a series of
  that kind. A series has an optional `total_volumes` (`PUT
/api/series/{id}`, the key required, null clears it). `GET
/api/series/{id}` lists its titles by number, those without one last, each
  `owned`, `ordered`, `wishlist` or `none` by its copies (sold ones count as
  none; a box set with an owned copy holding the title makes it owned), and `missing`: every number from 1 to the larger of the total and
  the highest number held whose title has no owned copy, with that title and
  its state, or none. `GET /api/series` lists every series with its title
  and missing counts. The `title_series_drop_empty` trigger deletes a series
  once its last title leaves it or is deleted, after locking its row, which
  a save joining a title holds; a title moving between two series locks
  both in key order, and a title deleted locks its series first
  (`title_series_lock_for_delete`), so a create taking its number answers
  409 instead of a deadlock. A series page reads the series and its titles
  in one snapshot. Lookup candidates' `title` carries the same optional
  `series_name` and `series_number` (BN fills them, see
  [lookup](lookup.md)), which the scanner's add forwards when the API would
  take them, so a scanned book joins or creates its series. When another
  title of the series holds the number (a reprint under a new ISBN, or the
  same book saved at once from two tabs), the add saves the title in the
  series without one instead of failing on 409; a lookup only
  creates titles and never changes an existing title's series; the title form sends
  the two fields only when the reader changed them, so a form opened
  before another save gave the title its series keeps it. Kolekcja's
  desktop header links to `/series`, the list, and a phone's summary line
  does ("Serie"), since a third header control would cover the heading; a
  series page (`/series/{id}`) shows the volumes, "Brakujące tomy" and the
  "Liczba tomów" field; the title form has "Seria" (suggesting the series
  of the title's kind) and "Tom"; the title page shows "<series> · tom <n>"
  linking to its series.
- `POST /api/titles` for a film with a `tmdb_id` and neither
  `series_name` nor `series_number` in the body (an explicit null keeps
  none) puts it in the series named after its TMDB collection (see
  [lookup](lookup.md)), numbered by release order among the collection's
  released films, and sets the series' `total_volumes` to that count. A
  number another title of the series holds is dropped and the film joins
  without one. The same lookup gives a title without `cover_url` the
  film's TMDB poster, in place of the title search, so a save makes at
  most two TMDB requests. The lookup waits at most 3 s; a TMDB error,
  timeout or name the API would refuse saves the title without a series
  (and without a cover when the first request failed). Updates, TV
  seasons and other kinds never look a series up.
- A loan (`loan`) records who has an owned copy: `POST /api/copies/{id}/loans`
  (`borrower` trimmed, at most 500 bytes; `lent_on` today in Poland when
  null; optional `due_on`, not before it) answers 201, 409 for a wish or a
  copy already lent; `PUT /api/loans/{id}` sets `returned_on`, not before
  `lent_on`, once: the same day again answers 200, another day 409, so a
  stale "Zwrócono" keeps the first. A copy has at most one open loan and keeps its returned ones,
  newest first in `GET /api/copies/{id}`; they go with the copy. A lent copy
  stays owned (turning it into a wish answers 409 on `status`) and counts as
  before in spend, valuation and deals. `GET /api/collection` items carry the
  open loan's `loan_borrower` and `loan_due_on`; `?loaned=1` lists only lent
  copies. A discard keeps a copy that was lent: it locks the copy, as a
  loan does, before it looks for loans.
- A title records whether the owner finished it (watched, read or heard):
  `finished` and `finished_on`, the latest day when known, on
  `POST`/`PUT /api/titles`. `finished_on` without `finished` marks it
  finished; with `finished: false`, or after today in Poland, it answers 422
  on `finished_on`; `finished: true` alone is an unknown day and `false`
  clears the day. A `PUT` without `finished` (or with null) keeps both.
  One state per title, no log of rewatches. `GET /api/collection` items
  carry the title's `finished` and `finished_on`; `?unfinished=1` lists
  only owned copies of titles not finished. `PUT
/api/titles/{id}/finished` (`finished` and `finished_on`, checked the
  same way; `finished` required unless a day is given) writes that state
  alone, so a page holding an older title never writes its name or notes
  back. The title page shows "Obejrzane", "Przeczytane" or "Przesłuchane"
  with the day, marks it finished today in Poland (`todayInPoland`,
  whatever the browser's zone) in one tap and changes, forgets or takes
  back the mark in a sheet, all through that endpoint.
- A title carries the owner's tags (`tag`, `title_tag`): the bodies of
  `POST` and `PUT /api/titles` take `tags`, each trimmed, blank ones
  dropped and those differing only in case collapsed, at most 500 bytes
  each and 20 per title (else 422 on `tags`). A `PUT` without `tags` (or with null) keeps
  them, `[]` clears them. A tag matches by `tag.key`, the Go case fold
  `foldCase` locations use (final sigma as σ; the browser's `tagKey`
  mirrors it, not Postgres's `lower`), and shows with the spelling it was
  first saved with; one no title carries any more takes the next spelling
  saved. A save locks its tags in key order, then checks their use in a
  statement of its own, so concurrent saves neither deadlock nor respell a
  tag another title carries. `GET /api/tags` lists the tags in use with their
  title counts. `GET /api/collection?tag=` and `GET /api/titles?tag=`
  list what carries a tag, case aside; collection items carry their
  title's tags for the browser's filter.
- A box set stays one title, edition and copy; its edition lists the
  titles it holds (`edition_content`), of any kind:
  `POST`/`PUT /api/editions` take `contents`, title IDs in their order
  (repeats collapsed, at most 100; one that names no title, or the
  edition's own, answers 422 on `contents`), and a `PUT` without them keeps
  the stored list, `[]` clears it. Edition answers name them in
  `contents`. A listed title cannot be deleted (409); deleting the edition
  drops its list. `GET /api/titles/{id}` lists the box sets holding it in
  `box_sets` (`owned` when the box set has an owned copy) and titles carry
  `in_owned_box_set`, which Kolekcja's "Bez egzemplarzy" leaves out.
  Collection items carry `contents`, so a search for a contained title's
  name finds the box set's copy. Price, deals and valuation stay with the
  box set's copy. The edition form's "Zawartość zestawu" picks existing
  titles by name; the title page shows "W zestawie: <box set>", marked "na
  półce" when owned.
- An edition records what every copy of a pressing shares: `disc_count`
  (1 to 99) on film, TV and album editions, `limited_to` (a limited run,
  1 to 1 000 000) on any, and `color` (trimmed, one line, at most 500
  bytes; blank is none) on vinyl only. One on another kind or format, out
  of range or not a whole number answers 422 on that field. `POST` and
  `PUT /api/editions` take them, a `PUT` without one clears it, as for the
  other edition fields; edition and title answers and `GET /api/collection`
  items carry them, absent when unset. The edition form shows "Liczba płyt"
  for films, TV and albums, "Kolor winylu" once the medium is vinyl and
  "Nakład" always. The edition summary adds them after the medium, as in
  "Winyl · 2 płyty · czerwony · nakład 500 egz."; one disc adds nothing.
- `GET /api/export?format=csv|json` (any other `format` 422) downloads the
  whole collection as `ohara-YYYY-MM-DD.<format>` (the day in Poland),
  each owned copy with the floor, mid and ceiling `GET /api/valuation`
  shows now, all read in one snapshot. The CSV is for Polish Excel: UTF-8
  with a BOM, `;`, LF line ends (a CR inside a cell survives),
  Polish headers and labels (tags joined by ", "), złoty with a decimal
  comma, one row per copy
  (sold and ordered ones too) and one per title without copies, and a `'` before a
  cell starting with `=`, `+`, `-`, `@`, a tab or a CR. The JSON is the
  titles with their editions and copies under the API's field names,
  grosze as the API sends them. Kolekcja's "Eksportuj kolekcję" menu
  offers both.
- An edition's own cover photo is served at the `cover_override_url` the API
  hands out, `/api/editions/{id}/cover?v=` plus the edition's `updated_at`,
  which a new photo or any edit changes. A request whose query parses and
  holds one `v`, the current one, answers
  `Cache-Control: private, max-age=31536000, immutable`; any other `v`, a
  second one, none or an error answers `no-store`.
- An album copy (an edition of a `music` title, any format) takes the
  Goldmine grades Discogs uses, both optional: `media_grade` for the disc or
  tape (`mint`, `near_mint`, `very_good_plus`, `very_good`, `good_plus`,
  `good`, `fair`, `poor`) and `sleeve_grade` (the same plus `generic`,
  `not_graded`, `no_cover`). A grade on a copy of any other kind, or an
  unknown value, answers 422 on its field; a `PUT` without one clears it.
  They sit beside the five-step `condition`, which stays as stored and
  which the valuation's Discogs grade still reads: a `PUT /api/copies/{id}`
  without `condition` keeps the stored one (null clears it), so a client
  that only grades loses nothing. Deals and valuation ignore the grades. The album copy form adds "Stan nośnika" and "Stan
  okładki" with the English names ("Very Good Plus (VG+)"), and the copy's
  facts show "Gradacja: Nośnik VG+ · Okładka NM".
- A copy carries its own collector marks, since copies of one edition
  differ in them: `signed` (an autograph, false when left out) and
  `copy_number` (a numbered copy's number, a whole number from 1 to
  1 000 000). Copy, title and `GET /api/collection` answers carry them,
  absent when unset; a `PUT` without one clears it. A number above the
  edition's `limited_to` answers 422 on `copy_number`, and so does a move to
  an edition whose run is below it; two copies of one edition with one
  number answer 409 on `copy_number` (copies of two editions may share
  one). An edition `PUT` whose `limited_to` is below the highest number its
  copies carry answers 422 on `limited_to`; a copy save holds a share lock
  on its edition, so neither check misses a concurrent change of the other.
  Wishlist copies take neither mark (422 on its field): the deal hunter
  does not look for them, and deals and valuation ignore both. The form of
  an owned, ordered or sold copy has "Z autografem" and "Numer
  egzemplarza"; the copy's facts and the edition page show "Cechy: Z
  autografem · Egzemplarz nr 123/500" ("nr 123" without a run), and a
  signed copy's Kolekcja tile ends its line with "z autografem".

## Frontend

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
  price, and its "Wypożyczone" switch (`?loaned=1`, as on the server) the lent
  ones, whose shelf line reads "Wypożyczony: <borrower>", with "po
  terminie" once the due date has passed; a phone's header shows their
  count as its toggle, hidden at 0.
  Its "Sprzedane" switch (`?status=sold`) lists the sold copies, which
  every other view, the counts and the default shelf leave out.
  Its "Do nadrobienia" switch (`?unfinished=1`, as on the server) lists
  owned copies of titles not finished; a status without owned copies
  (wishes, sold, "Bez egzemplarzy") drops it, in the sheet and when
  read from the address.
  Its "Tag" (`?tag=`) offers the tags the loaded copies and titles carry,
  "Każdy" first; a title page's tags link there, and its form's "Tagi"
  suggests `GET /api/tags`. Enter or a suggestion adds a tag; one left
  typed stays a draft that saving adds.
  Its "Miejsce" (`?location=`) lists the copies stored at one location,
  the whole value, case aside, picked from the loaded copies' locations,
  the most used first. An owned copy's `location` is one free-text place
  (one line of at most 500 bytes, no control characters; a wish has none,
  422); the copy form suggests the places in use from
  `GET /api/copies/locations`, and `q` also searches it. Places match and
  merge by a Go case fold (final sigma as σ), which the browser's
  `foldLocation` mirrors, not by Postgres's `lower`. `?add=copy` on a title
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
- Statystyki (`/stats`, in the side navigation and the phone's settings
  sheet, never a tab) reads `GET /api/collection` once and computes in
  the browser (`src/lib/stats.ts`), over owned copies only: "Wydatki w
  miesiącach" sums the prices paid in each of the last 12 calendar months
  in Poland by purchase date (an unpriced copy counts, adds nothing) with
  one "Bez daty" figure for undated copies; "Przyrost kolekcji" counts
  owned copies at each month's end by purchase date, else the day the copy
  was added, and names apart those dated after this month; "Nowe czy
  używane" splits them new, used and not recorded by count and by money,
  in whole percents adding up to 100 (a 0 zł price is a price). The months
  move on once the month in Poland has changed (`src/lib/stats-month.svelte.ts`:
  checked every minute and when the page is shown again). Charts are plain
  SVG and CSS, each with its data under "Pokaż dane". It also reads
  `GET /api/valuation` and groups its copies (`src/lib/stats-value.ts`):
  "Wartość według rodzaju" and "Wartość według nośnika" (print books,
  which have no format, as one group and audiobooks as another) give each
  group's summed used value, price paid
  (with its unpriced copies), copy count and how many copies have no used
  value or a low-confidence one, the most valuable group first; no history,
  gains or movers. The figures are today's values from the window's prices,
  as Wartość computes them, and the note names the last valuation's day
  apart; a group priced from Discogs carries a `DiscogsCredit` per release.
  Before the first valuation (`last_valuation` null) one "Wartość kolekcji"
  card says so and links to Wartość, also beside the empty shelf; a failed
  read shows its own retry. Without an owned copy the page offers "Skanuj".
