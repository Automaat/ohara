-- The newest time a run that matched a stored listing to its current
-- title had read the wishlist, holdings and titles it matched with. A run
-- that read them earlier matched from older data and does not move the
-- listing to another title. Null for a listing stored before this column:
-- the next run that matches it may move it.
ALTER TABLE price_observation ADD COLUMN assigned_as_of timestamptz;
