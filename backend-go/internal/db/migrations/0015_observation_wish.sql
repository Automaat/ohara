-- The wishlist copy the deal hunter recorded a listing for, kept in raw, as
-- a column the deals list can index: its filtered lists start from the
-- wished listings instead of reading every stored observation and its raw.
-- Valuation listings of owned editions, most of the table, carry none.
ALTER TABLE price_observation ADD COLUMN wishlist_copy_id bigint
    GENERATED ALWAYS AS ((raw->>'wishlist_copy_id')::bigint) STORED;

CREATE INDEX price_observation_wish_idx ON price_observation (wishlist_copy_id)
    WHERE wishlist_copy_id IS NOT NULL;

-- No query reads observations by edition and first observation time;
-- price_observation_edition_seen_idx serves every lookup by edition.
DROP INDEX price_observation_edition_idx;
