-- Purchases a Telegram alert's "Kupione" button recorded, by listing and
-- wish. A button buys once: a later press of it changes nothing, even
-- after the collector moved the copy back to the wishlist to correct the
-- purchase. A new table only; no stored row changes.
CREATE TABLE telegram_purchase (
    observation_id bigint NOT NULL REFERENCES price_observation (id) ON DELETE CASCADE,
    copy_id bigint NOT NULL REFERENCES copy (id) ON DELETE CASCADE,
    bought_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (observation_id, copy_id)
);
