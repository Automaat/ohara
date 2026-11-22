-- When a stored listing was last offered at all: every answer that holds
-- it, priced or not, matching or not, moves last_offered_at, while
-- last_seen_at moves only when a run records the listing's price, so it
-- keeps the valuation window. Pruning by OHARA_PRICE_RETENTION_DAYS counts
-- from the later of the two, so a listing the runs still see stays. Rows
-- stored before this migration keep an unknown (null) time, not a guess.
ALTER TABLE price_observation ADD COLUMN last_offered_at timestamptz;
