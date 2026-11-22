-- The Discogs collection instance an album copy was pushed to, and the
-- release it is an instance of: the copy's edition can name another
-- release later, and the instance stays the old release's. Set only once
-- the push has also set the instance's conditions. One instance per copy;
-- Ohara edits only the instance it stored here and never removes one.
ALTER TABLE copy ADD COLUMN discogs_instance_id bigint CHECK (discogs_instance_id > 0);
ALTER TABLE copy ADD COLUMN discogs_instance_release_id bigint;
-- When the copy was linked: Discogs' listing can lag behind a write, so
-- an instance missing from it counts as removed only some time after.
ALTER TABLE copy ADD COLUMN discogs_instance_linked_at timestamptz;
ALTER TABLE copy ADD CONSTRAINT copy_discogs_instance_pair
    CHECK ((discogs_instance_id IS NULL) = (discogs_instance_release_id IS NULL)
        AND (discogs_instance_id IS NULL) = (discogs_instance_linked_at IS NULL));
-- One copy per instance: no two copies ever grade the same one.
CREATE UNIQUE INDEX copy_discogs_instance_unique ON copy (discogs_instance_id)
    WHERE discogs_instance_id IS NOT NULL;

-- An add of a copy's release to the Discogs collection that has not
-- finished: written and committed before the add is sent, so it outlives
-- the push. instance_id is the instance Discogs answered with, null while
-- the add's outcome is unknown (no answer, a cut-off answer, a 5xx). Only
-- an instance Discogs named here, or one the owner confirms, is ever
-- linked to the copy; the row goes once the copy is linked, once Discogs
-- clearly refused the add, or when the owner discards it.
CREATE TABLE discogs_add (
    copy_id     bigint PRIMARY KEY REFERENCES copy (id) ON DELETE CASCADE,
    release_id  bigint NOT NULL,
    sent_at     timestamptz NOT NULL DEFAULT now(),
    instance_id bigint CHECK (instance_id > 0) UNIQUE
);
