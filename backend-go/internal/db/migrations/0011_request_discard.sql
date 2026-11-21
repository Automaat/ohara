-- When a person last changed a title or an edition: an edit or a cover
-- photo. updated_at moves with the automatic cover fill too, so it cannot
-- tell. DELETE /api/requests/{key} removes the rows a failed add made only
-- while nobody changed them.
ALTER TABLE title ADD COLUMN edited_at timestamptz;

ALTER TABLE edition ADD COLUMN edited_at timestamptz;

-- Rows from before this column may have been changed by a person already,
-- so none of them is left for a discard to remove.
UPDATE title SET edited_at = updated_at WHERE request_key IS NOT NULL;

UPDATE edition SET edited_at = updated_at WHERE request_key IS NOT NULL;

-- Keys a discard has given up. A create that arrives with one later, such as
-- a request that stalled on the way, is refused instead of adding rows the
-- add no longer stands behind. A lock per key orders creates and discards.
CREATE TABLE discarded_request (
    request_key text PRIMARY KEY,
    discarded_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION request_key_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.request_key IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(hashtextextended('request:' || NEW.request_key, 0));
        IF EXISTS (SELECT 1 FROM discarded_request WHERE request_key = NEW.request_key) THEN
            RAISE EXCEPTION 'request key was discarded'
                USING ERRCODE = 'unique_violation', CONSTRAINT = 'discarded_request_key';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER title_request_key_guard BEFORE INSERT ON title
    FOR EACH ROW EXECUTE FUNCTION request_key_guard();

CREATE TRIGGER edition_request_key_guard BEFORE INSERT ON edition
    FOR EACH ROW EXECUTE FUNCTION request_key_guard();

CREATE TRIGGER copy_request_key_guard BEFORE INSERT ON copy
    FOR EACH ROW EXECUTE FUNCTION request_key_guard();
