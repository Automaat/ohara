-- Repeat alerts on a price drop. alerted_price_cents is the price the last
-- sent alert for a listing named: a later price under it, within the wish's
-- limit, alerts again and the message names it. queued_price_cents is the
-- lowest price the alert waiting to go out was queued or tried at: once
-- that alert gave up or expired, only a price under it queues it again.
--
-- Both stay null on alerts stored before this migration. The price a sent
-- one named was overwritten since, so those listings never alert again
-- rather than repeat a price the collector already got.
ALTER TABLE deal_alert
    ADD COLUMN alerted_price_cents bigint,
    ADD COLUMN queued_price_cents bigint;
