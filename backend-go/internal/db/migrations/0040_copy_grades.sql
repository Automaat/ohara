-- An album copy's Goldmine grades, the scale Discogs and record sellers
-- use: the disc (or tape) and its sleeve apart, since a worn sleeve and a
-- worn record price differently. The sleeve also takes Generic, Not
-- Graded and No Cover. Both optional, kept to copies of album editions by
-- the API (a check cannot read the edition's kind). Null on every copy
-- stored before it; the five-step condition stays as it was.
ALTER TABLE copy ADD COLUMN media_grade text CHECK (media_grade IN (
    'mint', 'near_mint', 'very_good_plus', 'very_good', 'good_plus', 'good', 'fair', 'poor'
));
ALTER TABLE copy ADD COLUMN sleeve_grade text CHECK (sleeve_grade IN (
    'mint', 'near_mint', 'very_good_plus', 'very_good', 'good_plus', 'good', 'fair', 'poor',
    'generic', 'not_graded', 'no_cover'
));
