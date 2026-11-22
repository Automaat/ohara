-- A copy can be sold: it leaves the shelf but keeps what was paid, when and
-- where, beside the day it was sold and, when known, what it sold for. Only a
-- sold copy carries a sale, and every sold copy has its day. No existing row
-- changes: every copy is owned or wished, with no sale.

ALTER TABLE copy DROP CONSTRAINT copy_status_check;
ALTER TABLE copy ADD CONSTRAINT copy_status_check
    CHECK (status IN ('owned', 'wishlist', 'sold'));

-- What was paid applies to a copy on the shelf and to one sold from it.
ALTER TABLE copy DROP CONSTRAINT copy_wishlist_unpaid;
ALTER TABLE copy ADD CONSTRAINT copy_wishlist_unpaid CHECK (
    status IN ('owned', 'sold') OR (price_cents IS NULL AND purchased_on IS NULL)
);

ALTER TABLE copy ADD COLUMN sold_on date;
ALTER TABLE copy ADD COLUMN sale_price_cents bigint
    CHECK (sale_price_cents BETWEEN 0 AND 100000000);
ALTER TABLE copy ADD CONSTRAINT copy_sale_sold CHECK (
    CASE WHEN status = 'sold' THEN sold_on IS NOT NULL
         ELSE sold_on IS NULL AND sale_price_cents IS NULL END
);
