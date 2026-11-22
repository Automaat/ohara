-- The wants Ohara put on the Discogs wantlist of the token's owner. A
-- sync removes a want only when this table holds it, so a want that was
-- on the wantlist before Ohara added it stays there.
CREATE TABLE discogs_want (
    username   text        NOT NULL,
    release_id bigint      NOT NULL CHECK (release_id > 0),
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (username, release_id)
);
