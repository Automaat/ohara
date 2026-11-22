-- The listings the deal hunter recorded for a wish, newest first: the deals
-- list's filters read them in this order and stop once the page is full,
-- instead of sorting every wished listing when nearly every stored listing
-- was recorded for a wish.
CREATE INDEX price_observation_wish_recent_idx ON price_observation (observed_at DESC, id DESC)
    WHERE wishlist_copy_id IS NOT NULL;
