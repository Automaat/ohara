-- The owner's own rating of a title, a whole number from 1 to 10 (the
-- scale Filmweb and lubimyczytac.pl use). Null on every title stored
-- before it and on any title not rated yet.
ALTER TABLE title ADD COLUMN rating smallint;
ALTER TABLE title ADD CONSTRAINT title_rating_range CHECK (rating BETWEEN 1 AND 10);
