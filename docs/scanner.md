# Scanner

The scanner and the scan queue. Read before changing `/scan`, the scan
sheet, pending scans, the scanner's visit or the queue. Quick confirm and
undo also follow the add request keys in [collection.md](collection.md) and
`needs_confirmation` in [lookup.md](lookup.md).

Moved from `CLAUDE.md`, whose conventions still apply.

## Backend

- A barcode no provider resolves goes to `scan_queue`; a trigger on `edition`
  removes it once an edition carries that barcode. `ohara_scan_queue_size`
  counts it on each scrape. The trigger keeps the entry it removes in
  `scan_queue_resolved`; `DELETE /api/editions/{id}?requeue=1` puts it back
  with its scans and times in the same transaction, as the scanner does when
  it undoes an add that created the edition.

## Frontend

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
  time (Web Locks), each POST within 15 s. A run first waits until the
  browser has settled the scan queue DELETEs this tab has on their way
  (`src/lib/queue-deletes.ts`). `request`'s 20 s deadline fails a DELETE
  that gets no answer (the queue page shows its error and keeps the
  entry), so the wait ends there. A DELETE the server gets after the POST
  for the same code still removes that entry: one held past the deadline,
  one started during the run, or one from another tab (#506). `request`
  does not follow redirects, so a 401 or Authelia's cross-origin redirect is an expired
  session ("Odśwież"), not a lost connection; the code is kept either way.
- The queue page (`/scan/queue`, `QueueList` in `src/lib/queue.svelte.ts`)
  reads the queue when it opens and again each time codes kept on the
  device leave it, so a code sent in the background after the first read
  still shows up. It watches `PendingScans.departures`, which goes up when
  a run of this tab took codes off the device or a read found fewer than it
  counted, so a code another tab kept meanwhile cannot hide a departure as
  it does in the count. An answer older than the last one shown is dropped,
  and so is an entry a removal took out after that read went out. A failed
  first read shows the error state; a later failed read keeps the list with
  a warning and "Spróbuj ponownie" until a read asked after it answers.
- The scanner's visit ("Ta sesja" adds, "Ostatnie skany" and the purchase
  date carried to the next add) is kept in `localStorage`
  (`ohara.scan.visit`, `src/lib/visit.ts`) and restored on load until 12
  hours after its last add or scan. Each change starts from what storage
  holds, so tabs keep each other's adds, and an add writes its date, scan
  and tray entry at once. A waiting scan turns queued once the queue takes
  its code. Undo toasts are not restored; without storage the visit lasts
  until the next load.
