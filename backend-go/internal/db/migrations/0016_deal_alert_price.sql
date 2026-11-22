-- The price the last sent alert for a listing named. A listing whose price
-- drops under it, still within the wish's limit, alerts again; the new
-- message names it as the earlier price. Null until an alert is sent.
ALTER TABLE deal_alert ADD COLUMN alerted_price_cents bigint;

-- Alerts sent before this column take the listing's stored price, the
-- latest one seen: the price each one named was overwritten since, and a
-- drop the hunter already stored never alerts retroactively.
UPDATE deal_alert a SET alerted_price_cents = o.price_cents
FROM price_observation o
WHERE o.id = a.observation_id AND a.sent_at IS NOT NULL;
