-- Deal hunting. A wishlist copy carries the most its owner will pay; the
-- matcher's reject and accept phrases are editable; and every alert for a
-- listing is recorded, so a listing alerts at most once.

ALTER TABLE copy ADD COLUMN max_price_cents bigint
    CHECK (max_price_cents BETWEEN 0 AND 100000000);
ALTER TABLE copy ADD CONSTRAINT copy_max_price_wishlist CHECK (
    status = 'wishlist' OR max_price_cents IS NULL
);

-- A reject phrase drops a listing whose title contains it; an accept phrase
-- is cut from the title before the reject check, so "twarda okładka" keeps a
-- book that "okładka" alone would drop. Phrases match word prefixes after
-- folding case and Polish diacritics.
CREATE TABLE deal_keyword (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('reject', 'accept')),
    phrase text NOT NULL CHECK (btrim(phrase) <> ''),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX deal_keyword_phrase_key ON deal_keyword (kind, lower(phrase));

INSERT INTO deal_keyword (kind, phrase) VALUES
    ('reject', 'pudełko'),
    ('reject', 'okładka'),
    ('reject', 'uszkodzon'),
    ('reject', 'kupię'),
    ('reject', 'zamienię'),
    ('reject', 'wymienię'),
    ('reject', 'plakat'),
    ('reject', 'soundtrack'),
    ('accept', 'twarda okładka'),
    ('accept', 'miękka okładka');

-- One row per listing that met a wishlist copy's maximum price. sent_at
-- stays null until Telegram accepts the message.
CREATE TABLE deal_alert (
    observation_id bigint PRIMARY KEY REFERENCES price_observation (id) ON DELETE CASCADE,
    copy_id bigint REFERENCES copy (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    last_error text NOT NULL DEFAULT ''
);

CREATE INDEX deal_alert_pending_idx ON deal_alert (created_at) WHERE sent_at IS NULL;
