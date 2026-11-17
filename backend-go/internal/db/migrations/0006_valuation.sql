-- Valuation. A listing seen again keeps its one observation row; the
-- valuation run moves last_seen_at (and the price) forward, so the valuation
-- window counts listings a source still offers. Rows recorded before this
-- migration were last seen when they were first observed.

ALTER TABLE price_observation ADD COLUMN last_seen_at timestamptz;
UPDATE price_observation SET last_seen_at = observed_at;
ALTER TABLE price_observation
    ALTER COLUMN last_seen_at SET NOT NULL,
    ALTER COLUMN last_seen_at SET DEFAULT now();

CREATE INDEX price_observation_edition_seen_idx ON price_observation (edition_id, last_seen_at);

-- One row per day: the owned collection's value range and what was paid for
-- it. Each bound sums the copies that have that bound, and says how many
-- that is.
CREATE TABLE valuation_history (
    day date PRIMARY KEY,
    copies integer NOT NULL CHECK (copies >= 0),
    spent_cents bigint NOT NULL CHECK (spent_cents >= 0),
    floor_cents bigint NOT NULL CHECK (floor_cents >= 0),
    floor_copies integer NOT NULL CHECK (floor_copies >= 0),
    mid_cents bigint NOT NULL CHECK (mid_cents >= 0),
    mid_copies integer NOT NULL CHECK (mid_copies >= 0),
    ceiling_cents bigint NOT NULL CHECK (ceiling_cents >= 0),
    ceiling_copies integer NOT NULL CHECK (ceiling_copies >= 0),
    low_confidence integer NOT NULL CHECK (low_confidence >= 0),
    computed_at timestamptz NOT NULL DEFAULT now()
);

-- Players and drives share film names ("Dune HD" with a Blu-ray module) and
-- would price an owned film at a device's price.
INSERT INTO deal_keyword (kind, phrase) VALUES
    ('reject', 'odtwarzacz'),
    ('reject', 'moduł')
ON CONFLICT DO NOTHING;
