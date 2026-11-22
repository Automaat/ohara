-- The wants Ohara put on the Discogs wantlist of the token's owner. A row
-- is written before the add is sent and stays unconfirmed until Discogs
-- accepts it (confirmed_at); a sync later reads the want's date_added from
-- the wantlist (discogs_added_at). A sync removes a want only when its
-- row holds a date_added equal to the listed want's, so a want that was
-- on the wantlist before Ohara added it, or that the owner re-added by
-- hand, stays there.
CREATE TABLE discogs_want (
    username         text        NOT NULL,
    release_id       bigint      NOT NULL CHECK (release_id > 0),
    added_at         timestamptz NOT NULL DEFAULT now(),
    confirmed_at     timestamptz,
    discogs_added_at timestamptz,
    PRIMARY KEY (username, release_id),
    CHECK (discogs_added_at IS NULL OR confirmed_at IS NOT NULL)
);
