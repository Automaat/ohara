-- The Discogs collection instance an album copy was pushed to, and the
-- release it is an instance of: the copy's edition can name another
-- release later, and the instance stays the old release's. One instance
-- per copy; Ohara edits only the instance it stored here and never
-- removes one. discogs_instance_synced says its conditions were set; the
-- API shows the instance only then. discogs_add_release_id and
-- discogs_add_since mark an add whose answer never arrived, so a later
-- push can adopt the instance it made. Null or false on every copy never
-- pushed; each pair is set together.
ALTER TABLE copy ADD COLUMN discogs_instance_id bigint CHECK (discogs_instance_id > 0);
ALTER TABLE copy ADD COLUMN discogs_instance_release_id bigint;
ALTER TABLE copy ADD COLUMN discogs_instance_synced boolean NOT NULL DEFAULT false;
ALTER TABLE copy ADD COLUMN discogs_add_release_id bigint;
ALTER TABLE copy ADD COLUMN discogs_add_since timestamptz;
ALTER TABLE copy ADD CONSTRAINT copy_discogs_instance_pair
    CHECK ((discogs_instance_id IS NULL) = (discogs_instance_release_id IS NULL));
ALTER TABLE copy ADD CONSTRAINT copy_discogs_add_pair
    CHECK ((discogs_add_release_id IS NULL) = (discogs_add_since IS NULL));
