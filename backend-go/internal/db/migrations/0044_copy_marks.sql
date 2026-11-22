-- Collector marks that set one copy apart from its edition's others: an
-- autograph and the number of a numbered copy. Both belong to the copy,
-- since copies of one edition differ in them. A number is unique within
-- its edition; the API keeps it within the edition's run (limited_to) and
-- keeps both marks off wishlist copies. Every copy stored before it is
-- unsigned and unnumbered.
ALTER TABLE copy ADD COLUMN signed boolean NOT NULL DEFAULT false;
ALTER TABLE copy ADD COLUMN copy_number integer CHECK (copy_number BETWEEN 1 AND 1000000);
CREATE UNIQUE INDEX copy_number_key ON copy (edition_id, copy_number)
    WHERE copy_number IS NOT NULL;
