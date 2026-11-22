-- The Discogs collection instance an album copy was pushed to, and the
-- release it is an instance of: the copy's edition can name another
-- release later, and the instance stays the old release's. Ohara edits
-- only the instance it stored here and never removes one. Null on every
-- copy never pushed; both set or both null.
ALTER TABLE copy ADD COLUMN discogs_instance_id bigint CHECK (discogs_instance_id > 0);
ALTER TABLE copy ADD COLUMN discogs_instance_release_id bigint;
ALTER TABLE copy ADD CONSTRAINT copy_discogs_instance_pair
    CHECK ((discogs_instance_id IS NULL) = (discogs_instance_release_id IS NULL));
