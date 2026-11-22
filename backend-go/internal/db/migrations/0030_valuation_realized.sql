-- Each day of value also keeps what the copies sold by then made over what
-- they cost: the sale price less the purchase price summed over the sold
-- copies that have both, how many those are and how many sold copies lack
-- either price. A gain can be negative. Days stored before this read 0.

ALTER TABLE valuation_history
    ADD COLUMN realized_gain_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN realized_copies integer NOT NULL DEFAULT 0 CHECK (realized_copies >= 0),
    ADD COLUMN realized_unpriced integer NOT NULL DEFAULT 0 CHECK (realized_unpriced >= 0);
