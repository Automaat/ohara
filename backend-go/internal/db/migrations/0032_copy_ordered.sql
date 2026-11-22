-- A copy can be on order: paid for, or at least ordered, but not yet on the
-- shelf. It keeps what it cost, the day it was ordered (its purchase date,
-- which it keeps once it arrives) and the shop, and may name the day it is
-- expected. Only an ordered copy carries that day. No existing row changes:
-- no copy is ordered yet.

ALTER TABLE copy DROP CONSTRAINT copy_status_check;
ALTER TABLE copy ADD CONSTRAINT copy_status_check
    CHECK (status IN ('owned', 'wishlist', 'sold', 'ordered'));

-- What was paid applies to a copy on the shelf, one sold from it and one on
-- its way to it.
ALTER TABLE copy DROP CONSTRAINT copy_wishlist_unpaid;
ALTER TABLE copy ADD CONSTRAINT copy_wishlist_unpaid CHECK (
    status IN ('owned', 'sold', 'ordered') OR (price_cents IS NULL AND purchased_on IS NULL)
);

ALTER TABLE copy ADD COLUMN expected_on date;
ALTER TABLE copy ADD CONSTRAINT copy_expected_ordered CHECK (
    status = 'ordered' OR expected_on IS NULL
);
