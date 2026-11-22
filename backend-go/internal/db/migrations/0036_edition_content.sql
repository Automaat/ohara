-- The titles a box set holds. A box set stays one title, edition and copy;
-- its edition also lists the films, books or albums inside, of any kind, so
-- they are found by search and known as owned. position keeps the order the
-- edition's contents were saved in. A listed title cannot be deleted while a
-- box set holds it; deleting the edition drops its list.
CREATE TABLE edition_content (
    edition_id bigint NOT NULL REFERENCES edition (id) ON DELETE CASCADE,
    title_id bigint NOT NULL REFERENCES title (id) ON DELETE RESTRICT,
    position integer NOT NULL,
    PRIMARY KEY (edition_id, title_id)
);

CREATE INDEX edition_content_title_id_idx ON edition_content (title_id);
