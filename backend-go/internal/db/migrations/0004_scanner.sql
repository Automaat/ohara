-- Barcodes the scanner could not resolve: no provider knew them, or every
-- provider failed. They wait here to be added by hand or looked up again.
-- One row per normalized barcode; scanning it again bumps scans.

CREATE TABLE scan_queue (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    barcode text NOT NULL UNIQUE CHECK (barcode ~ '^[0-9]{8}$|^[0-9]{12,14}$'),
    reason text NOT NULL CHECK (reason IN ('not_found', 'lookup_failed')),
    detail text NOT NULL DEFAULT '',
    scans integer NOT NULL DEFAULT 1 CHECK (scans > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    last_scanned_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX scan_queue_last_scanned_idx ON scan_queue (last_scanned_at DESC);

-- Queueing a barcode and giving an edition that barcode take the same
-- per-barcode transaction lock, so neither can miss the other's
-- uncommitted row and leave a resolved barcode queued.
CREATE FUNCTION scan_queue_lock(code text) RETURNS void LANGUAGE sql AS $$
    SELECT pg_advisory_xact_lock(hashtextextended('ohara.scan_queue:' || code, 0));
$$;

-- A barcode leaves the queue once an edition carries it, however that
-- edition was created.
CREATE FUNCTION scan_queue_resolve() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.barcode IS NOT NULL THEN
        PERFORM scan_queue_lock(NEW.barcode);
        DELETE FROM scan_queue WHERE barcode = NEW.barcode;
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER edition_resolves_scan_queue
    AFTER INSERT OR UPDATE OF barcode ON edition
    FOR EACH ROW EXECUTE FUNCTION scan_queue_resolve();

-- A client-chosen key that makes POST /api/copies safe to retry: a second
-- request with the same key returns the copy the first one created.
ALTER TABLE copy ADD COLUMN request_key text UNIQUE
    CHECK (char_length(request_key) BETWEEN 1 AND 100);
