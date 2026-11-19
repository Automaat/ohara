-- Client-chosen keys that make POST /api/titles and POST /api/editions safe
-- to retry, as copy.request_key does for copies: a second request with the
-- same key returns the row the first one created. The scanner sends one key
-- per add, so a retry after a lost answer finds what the add already made.
ALTER TABLE title ADD COLUMN request_key text UNIQUE
    CHECK (char_length(request_key) BETWEEN 1 AND 100);

ALTER TABLE edition ADD COLUMN request_key text UNIQUE
    CHECK (char_length(request_key) BETWEEN 1 AND 100);
