-- What a stored OLX listing asked when first seen and when the valuation
-- found it no longer offered. A listing seen again keeps its first price
-- while price_cents follows the latest one; a listing gone from a complete
-- answer to the search that last found it gets gone_at, and last_seen_at
-- keeps the last time it was offered. A gone listing offered again is no
-- longer gone. Rows stored before this migration keep an unknown (null)
-- first price: their first price was overwritten and is not guessed.
ALTER TABLE price_observation
    ADD COLUMN first_price_cents bigint CHECK (first_price_cents BETWEEN 0 AND 100000000),
    ADD COLUMN gone_at timestamptz;
