-- A wish can match only its own edition: the deal hunter then takes a
-- listing only when it names the edition's details (packaging, Polish
-- voice-over or subtitles, binding). Like the price limits it belongs to
-- wishlist copies only. False on every copy stored before it, so existing
-- wishes match as before.
ALTER TABLE copy ADD COLUMN exact_edition boolean NOT NULL DEFAULT false;
ALTER TABLE copy ADD CONSTRAINT copy_exact_edition_wishlist CHECK (
    status = 'wishlist' OR NOT exact_edition
);

-- The edition details a stored listing's title names (steelbook,
-- pl_voiceover, hardcover, ...), kept by the deal hunter, so alerts and the
-- deals lists hold a wish of only its edition to them after the wish
-- changes. Null on listings stored before it, which name none.
ALTER TABLE price_observation ADD COLUMN edition_details text[];
