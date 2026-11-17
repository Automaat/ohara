-- Collection model: a title (the work) has editions (concrete releases keyed
-- by barcode), and each edition has copies (owned or wanted). Money is stored
-- in grosze so totals stay exact, capped at 1 000 000 zł so sums cannot
-- overflow.

CREATE TABLE title (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('movie', 'book')),
    name text NOT NULL CHECK (btrim(name) <> ''),
    original_title text,
    polish_title text,
    year integer CHECK (year BETWEEN 1800 AND 2200),
    tmdb_id bigint CHECK (tmdb_id > 0),
    authors text[] NOT NULL DEFAULT '{}',
    bn_id text,
    openlibrary_id text,
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX title_tmdb_id_key ON title (tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX title_kind_name_idx ON title (kind, lower(name));

CREATE TABLE edition (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    title_id bigint NOT NULL REFERENCES title (id) ON DELETE RESTRICT,
    -- Normalized GTIN: EAN-8, UPC-A, EAN-13 or GTIN-14; ISBN-10 is stored as
    -- its ISBN-13. One edition per barcode.
    barcode text UNIQUE CHECK (barcode ~ '^[0-9]{8}$|^[0-9]{12,14}$'),
    label text NOT NULL DEFAULT '',
    publisher text NOT NULL DEFAULT '',
    year integer CHECK (year BETWEEN 1800 AND 2200),
    format text CHECK (format IN ('dvd', 'bluray', 'uhd')),
    pl_subtitles text NOT NULL DEFAULT 'unknown' CHECK (pl_subtitles IN ('yes', 'no', 'unknown')),
    pl_voiceover text NOT NULL DEFAULT 'unknown' CHECK (pl_voiceover IN ('yes', 'no', 'unknown')),
    packaging text[] NOT NULL DEFAULT '{}',
    binding text CHECK (binding IN ('hardcover', 'paperback', 'other')),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX edition_title_id_idx ON edition (title_id);

CREATE TABLE copy (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    edition_id bigint NOT NULL REFERENCES edition (id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('owned', 'wishlist')),
    price_cents bigint CHECK (price_cents BETWEEN 0 AND 100000000),
    purchased_on date,
    shop text NOT NULL DEFAULT '',
    acquired_as text CHECK (acquired_as IN ('new', 'used')),
    condition text CHECK (condition IN ('mint', 'very_good', 'good', 'fair', 'poor')),
    notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    -- What was paid only applies to a copy on the shelf.
    CONSTRAINT copy_wishlist_unpaid CHECK (
        status = 'owned' OR (price_cents IS NULL AND purchased_on IS NULL)
    )
);

CREATE INDEX copy_edition_id_idx ON copy (edition_id);

-- Raw responses from barcode metadata providers.
CREATE TABLE lookup_cache (
    provider text NOT NULL,
    barcode text NOT NULL,
    response jsonb NOT NULL,
    fetched_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, barcode)
);

-- Prices seen in shops and listings; filled by the deal and valuation
-- sources.
CREATE TABLE price_observation (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source text NOT NULL,
    external_id text,
    observed_at timestamptz NOT NULL DEFAULT now(),
    price_cents bigint NOT NULL CHECK (price_cents BETWEEN 0 AND 100000000),
    acquired_as text CHECK (acquired_as IN ('new', 'used')),
    url text NOT NULL DEFAULT '',
    edition_id bigint REFERENCES edition (id) ON DELETE SET NULL,
    title_id bigint REFERENCES title (id) ON DELETE SET NULL,
    raw jsonb
);

CREATE UNIQUE INDEX price_observation_listing_key
    ON price_observation (source, external_id) WHERE external_id IS NOT NULL;
CREATE INDEX price_observation_edition_idx ON price_observation (edition_id, observed_at);
CREATE INDEX price_observation_title_idx ON price_observation (title_id, observed_at);
