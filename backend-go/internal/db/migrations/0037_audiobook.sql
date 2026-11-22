-- A book edition can be an audiobook, the one format a book takes; a
-- print book keeps none. The format still says nothing of the title's
-- kind, which the API checks.
ALTER TABLE edition DROP CONSTRAINT edition_format_check;
ALTER TABLE edition ADD CONSTRAINT edition_format_check
    CHECK (format IN ('dvd', 'bluray', 'uhd', 'cd', 'vinyl', 'cassette', 'audiobook'));
