-- Personal tags on titles: the owner's own groupings, such as a genre, a
-- director, "dla dzieci" or "do sprzedania". A tag describes the work, so it
-- belongs to the title and covers every edition, copy and wish of it. One row
-- per tag, matched case-insensitively by key and shown with the spelling it
-- was first saved with; a tag no title carries any more stays stored but is
-- not listed, and takes the next spelling saved for it.
--
-- key is the name as Go's foldCase makes it (each character lower-cased by
-- Go's Unicode tables, a final sigma as σ), the same fold the browser's
-- filter applies; Postgres's lower follows older tables and maps ς apart.
CREATE TABLE tag (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 500),
    key text NOT NULL UNIQUE CHECK (key <> '')
);

-- position keeps the order the title's tags were saved in.
CREATE TABLE title_tag (
    title_id bigint NOT NULL REFERENCES title (id) ON DELETE CASCADE,
    tag_id bigint NOT NULL REFERENCES tag (id) ON DELETE RESTRICT,
    position integer NOT NULL,
    PRIMARY KEY (title_id, tag_id)
);

CREATE INDEX title_tag_tag_id_idx ON title_tag (tag_id);
