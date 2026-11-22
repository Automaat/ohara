-- Special edition details every copy of a pressing shares: how many discs
-- a film or album edition holds, how many were made of a limited run (any
-- kind) and a vinyl pressing's color. The API checks which kinds and
-- formats each applies to.

ALTER TABLE edition ADD COLUMN disc_count integer CHECK (disc_count BETWEEN 1 AND 99);
ALTER TABLE edition ADD COLUMN limited_to integer CHECK (limited_to BETWEEN 1 AND 1000000);
ALTER TABLE edition ADD COLUMN color text CHECK (color <> '' AND octet_length(color) <= 500);
