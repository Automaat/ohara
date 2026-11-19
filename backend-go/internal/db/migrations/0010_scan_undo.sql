-- Client-chosen keys that make POST /api/titles and POST /api/editions safe
-- to retry, as copy.request_key does for copies: a second request with the
-- same key returns the row the first one created. The scanner sends one key
-- per add, so a retry after a lost answer finds what the add already made.
ALTER TABLE title ADD COLUMN request_key text UNIQUE
    CHECK (char_length(request_key) BETWEEN 1 AND 100);

ALTER TABLE edition ADD COLUMN request_key text UNIQUE
    CHECK (char_length(request_key) BETWEEN 1 AND 100);

-- The scan queue entry an edition took out of the queue, as it was at that
-- moment, so undoing the add that created the edition can put it back with
-- its scans and times. It goes with the edition.
CREATE TABLE scan_queue_resolved (
    edition_id bigint PRIMARY KEY REFERENCES edition (id) ON DELETE CASCADE,
    barcode text NOT NULL,
    reason text NOT NULL,
    detail text NOT NULL,
    scans integer NOT NULL,
    created_at timestamptz NOT NULL,
    last_scanned_at timestamptz NOT NULL
);

CREATE OR REPLACE FUNCTION scan_queue_resolve() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.barcode IS NOT NULL THEN
        PERFORM scan_queue_lock(NEW.barcode);
        WITH gone AS (
            DELETE FROM scan_queue WHERE barcode = NEW.barcode
            RETURNING barcode, reason, detail, scans, created_at, last_scanned_at
        )
        INSERT INTO scan_queue_resolved
            (edition_id, barcode, reason, detail, scans, created_at, last_scanned_at)
        SELECT NEW.id, barcode, reason, detail, scans, created_at, last_scanned_at FROM gone
        ON CONFLICT (edition_id) DO UPDATE SET
            barcode = EXCLUDED.barcode,
            reason = EXCLUDED.reason,
            detail = EXCLUDED.detail,
            scans = EXCLUDED.scans,
            created_at = EXCLUDED.created_at,
            last_scanned_at = EXCLUDED.last_scanned_at;
    END IF;
    RETURN NULL;
END;
$$;
