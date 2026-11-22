-- A wish can alert on a share of the used market price: a listing at or
-- under max_market_percent percent of the valuation's used median for the
-- wished title and format alerts, beside or instead of max_price_cents.
-- Like the price limit it belongs to wishlist copies only. Null on every
-- copy stored before it, so existing wishes alert as before.
ALTER TABLE copy ADD COLUMN max_market_percent smallint
    CHECK (max_market_percent BETWEEN 1 AND 100);
ALTER TABLE copy ADD CONSTRAINT copy_max_market_percent_wishlist CHECK (
    status = 'wishlist' OR max_market_percent IS NULL
);
