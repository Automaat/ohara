-- Series: the Wiedźmin books, the Bond films, a numbered publisher line. A
-- series holds titles of one kind, and a title sits in at most one series,
-- optionally with its number there. Titles name their series by name: a name
-- matching a series of the title's kind by key joins it, a new one creates
-- it, and a series whose last title leaves it is deleted.
--
-- key is the name as Go's foldCase makes it (each character lower-cased by
-- Go's Unicode tables, a final sigma as σ), the fold tags and locations use.
-- total_volumes is how many volumes the series has, when the owner knows.
CREATE TABLE series (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('movie', 'book', 'music')),
    name text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 500),
    key text NOT NULL CHECK (key <> ''),
    total_volumes integer CHECK (total_volumes BETWEEN 1 AND 10000),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT series_kind_key_key UNIQUE (kind, key),
    -- The target of title's (series_id, kind) reference, which keeps a
    -- series to titles of its own kind.
    CONSTRAINT series_id_kind_key UNIQUE (id, kind)
);

ALTER TABLE title ADD COLUMN series_id bigint;
ALTER TABLE title ADD COLUMN series_number integer;
ALTER TABLE title ADD CONSTRAINT title_series_fkey
    FOREIGN KEY (series_id, kind) REFERENCES series (id, kind);
ALTER TABLE title ADD CONSTRAINT title_series_number_range
    CHECK (series_number BETWEEN 1 AND 10000);
ALTER TABLE title ADD CONSTRAINT title_series_number_needs_series
    CHECK (series_number IS NULL OR series_id IS NOT NULL);

-- One title per number in a series; the index also finds a series' titles.
CREATE UNIQUE INDEX title_series_number_key ON title (series_id, series_number)
    WHERE series_number IS NOT NULL;
CREATE INDEX title_series_id_idx ON title (series_id) WHERE series_id IS NOT NULL;

-- Deletes the series a title left, or was in when deleted, once no title is
-- in it. The series row is locked first and checked in a statement of its
-- own: a save that joins a title to the series holds that lock until it
-- commits, so the check then sees its title and the series stays.
CREATE FUNCTION series_drop_empty() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.series_id IS NULL OR (TG_OP = 'UPDATE' AND NEW.series_id IS NOT DISTINCT FROM OLD.series_id) THEN
        RETURN NULL;
    END IF;
    PERFORM 1 FROM series WHERE id = OLD.series_id FOR UPDATE;
    DELETE FROM series s WHERE s.id = OLD.series_id
        AND NOT EXISTS (SELECT 1 FROM title t WHERE t.series_id = OLD.series_id);
    RETURN NULL;
END;
$$;

CREATE TRIGGER title_series_drop_empty
    AFTER UPDATE OF series_id OR DELETE ON title
    FOR EACH ROW EXECUTE FUNCTION series_drop_empty();
