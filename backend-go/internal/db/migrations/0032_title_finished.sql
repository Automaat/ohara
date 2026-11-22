-- Whether the owner has watched, read or heard a title, and the latest day
-- they finished it when known. One state per title, no log of rewatches.
-- Existing titles start unfinished; no stored row changes otherwise.
ALTER TABLE title
    ADD COLUMN finished boolean NOT NULL DEFAULT false,
    ADD COLUMN finished_on date,
    ADD CONSTRAINT title_finished_on_check CHECK (finished_on IS NULL OR finished);
