-- Presses of a Telegram alert's "Ignoruj" button that dismissed a listing,
-- by callback query id. Telegram sends an update again when the poller
-- could not confirm it, so a press is applied once: a replay after a
-- restart must not dismiss again a listing the collector brought back on
-- Okazje meanwhile. Telegram keeps a callback only minutes, so rows older
-- than two days are pruned on the next press.
CREATE TABLE telegram_press (
    callback_query_id text PRIMARY KEY,
    pressed_at timestamptz NOT NULL DEFAULT now()
);
