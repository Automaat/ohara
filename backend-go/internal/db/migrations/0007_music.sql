-- Music: CDs, vinyl and cassettes. An album is a title of kind music whose
-- authors are its credited artists; its editions carry the medium as their
-- format and, once known, the Discogs release they are, which the
-- valuation prices.

ALTER TABLE title DROP CONSTRAINT title_kind_check;
ALTER TABLE title ADD CONSTRAINT title_kind_check
    CHECK (kind IN ('movie', 'book', 'music'));

ALTER TABLE edition DROP CONSTRAINT edition_format_check;
ALTER TABLE edition ADD CONSTRAINT edition_format_check
    CHECK (format IN ('dvd', 'bluray', 'uhd', 'cd', 'vinyl', 'cassette'));

ALTER TABLE edition ADD COLUMN discogs_release_id bigint
    CHECK (discogs_release_id > 0);
