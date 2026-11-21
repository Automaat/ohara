-- Ceneo's terms forbid automated access, so Ohara no longer asks it. Its
-- stored listings go too; their deal alerts follow through ON DELETE
-- CASCADE.
DELETE FROM price_observation WHERE source = 'ceneo';

-- A movie lookup cached from a Ceneo product name would keep answering its
-- barcode until the cache expires. Drop it so the next scan asks the
-- remaining sources. A picked candidate is stored under the selection
-- provider and stays.
DELETE FROM lookup_cache
    WHERE provider = 'movie' AND response -> 'products' @> '[{"source": "ceneo"}]';
